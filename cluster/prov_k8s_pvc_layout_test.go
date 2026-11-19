package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

// #2395: the database PVC layout. A PVC replication-manager creates is OpenSVC's service directory
// (data/, etc/mysql/, init/); a PVC that already exists without the layout annotation keeps the legacy
// layout it was built with, whatever its Deployment is re-rendered for.

func layoutTestCluster() *Cluster {
	c := newTestCluster("k8stest")
	c.Conf.ProvDbStartFetchConfig = true
	c.Conf.MonitorAddress = "repman.k8stest.svc.cluster.local"
	c.Conf.APIPort = "10005"
	return c
}

var layoutTestServer = &ServerMonitor{Name: "db1", Port: "3306"}

func layoutContainers(dep *appsv1.Deployment) (init, db, jobs apiv1.Container) {
	init = dep.Spec.Template.Spec.InitContainers[0]
	for _, c := range dep.Spec.Template.Spec.Containers {
		switch c.Name {
		case "db1":
			db = c
		case "db1-dbjobs":
			jobs = c
		}
	}
	return
}

func mountPairs(c apiv1.Container) [][2]string {
	var out [][2]string
	for _, m := range c.VolumeMounts {
		out = append(out, [2]string{m.MountPath, m.SubPath})
	}
	return out
}

// service-dirs-v1 mounts exactly OpenSVC's three service directories, in the three containers.
func TestK8SServiceDirsDeployment_Mounts(t *testing.T) {
	dep := layoutTestCluster().k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutServiceDirs)
	init, db, jobs := layoutContainers(dep)

	data := [2]string{"/var/lib/mysql", "data"}
	etc := [2]string{"/etc/mysql", "etc/mysql"}
	ini := [2]string{"/docker-entrypoint-initdb.d", "init"}
	for name, c := range map[string]struct {
		got  apiv1.Container
		want [][2]string
	}{
		"init":   {init, [][2]string{data, etc, ini}},
		"db":     {db, [][2]string{data, etc}},
		"dbjobs": {jobs, [][2]string{data, ini, etc}},
	} {
		if got := mountPairs(c.got); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s mounts = %v, want %v", name, got, c.want)
		}
		for _, m := range c.got.VolumeMounts {
			if m.Name != "db1-data" || m.ReadOnly {
				t.Errorf("%s: mount %s must be read-write from db1-data, got name=%q readOnly=%t", name, m.MountPath, m.Name, m.ReadOnly)
			}
		}
	}
	// one PVC, the claim the Deployment always used
	if vols := dep.Spec.Template.Spec.Volumes; len(vols) != 1 || vols[0].PersistentVolumeClaim == nil || vols[0].PersistentVolumeClaim.ClaimName != "k8stest-db1-claim" {
		t.Errorf("volumes = %+v, want the single claim k8stest-db1-claim", vols)
	}
	// the Recreate strategy of #2390 is untouched
	if dep.Spec.Strategy.Type != appsv1.RecreateDeploymentStrategyType {
		t.Errorf("strategy = %q, want Recreate", dep.Spec.Strategy.Type)
	}
}

// The mounts resolve to what the contract says: the three subPaths are disjoint (none inside another,
// none the PVC root), the init container's writes are seen by the database and dbjobs containers, and
// .system lands inside data/, not at the PVC root.
func TestK8SServiceDirsDeployment_MountsResolveOnAVolume(t *testing.T) {
	dep := layoutTestCluster().k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutServiceDirs)
	init, db, jobs := layoutContainers(dep)

	pvc := t.TempDir()
	// resolve: the host directory a container path is bound to
	resolve := func(c apiv1.Container, containerPath string) string {
		for _, m := range c.VolumeMounts {
			if m.MountPath == containerPath {
				d := filepath.Join(pvc, m.SubPath)
				if err := os.MkdirAll(d, 0755); err != nil { // the kubelet creates a missing subPath
					t.Fatal(err)
				}
				return d
			}
		}
		t.Fatalf("container %s has no mount at %s", c.Name, containerPath)
		return ""
	}
	seen := map[string]string{}
	for _, p := range []string{"/var/lib/mysql", "/etc/mysql", "/docker-entrypoint-initdb.d"} {
		d := resolve(init, p)
		rel, _ := filepath.Rel(pvc, d)
		if rel == "." {
			t.Errorf("%s is the PVC root", p)
		}
		for other, orel := range seen {
			if strings.HasPrefix(rel+"/", orel+"/") || strings.HasPrefix(orel+"/", rel+"/") {
				t.Errorf("%s (%s) and %s (%s) are not disjoint", p, rel, other, orel)
			}
		}
		seen[p] = rel
	}
	for _, p := range []string{"/var/lib/mysql", "/etc/mysql"} {
		if resolve(db, p) != resolve(init, p) {
			t.Errorf("database and init containers do not share %s", p)
		}
	}
	for _, p := range []string{"/var/lib/mysql", "/etc/mysql", "/docker-entrypoint-initdb.d"} {
		if resolve(jobs, p) != resolve(init, p) {
			t.Errorf("dbjobs and init containers do not share %s", p)
		}
	}
	// what the init container writes is what the others read
	if err := os.WriteFile(filepath.Join(resolve(init, "/etc/mysql"), "my.cnf"), []byte("[mysqld]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []apiv1.Container{db, jobs} {
		if _, err := os.Stat(filepath.Join(resolve(c, "/etc/mysql"), "my.cnf")); err != nil {
			t.Errorf("%s does not see my.cnf written by the init container: %v", c.Name, err)
		}
	}
	// .system is created under /var/lib/mysql by the init command: inside data/
	if err := os.MkdirAll(filepath.Join(resolve(init, "/var/lib/mysql"), ".system/innodb"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(pvc, "data/.system/innodb")); err != nil {
		t.Errorf(".system is not inside data/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(pvc, ".system")); err == nil {
		t.Error(".system exists at the PVC root")
	}
	// the PVC root holds only the service directories
	entries, _ := os.ReadDir(pvc)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if want := []string{"data", "etc", "init"}; !reflect.DeepEqual(names, want) {
		t.Errorf("PVC root = %v, want %v", names, want)
	}
}

// The legacy rendering is what it always was: the layout-less builder is the legacy layout.
func TestK8SLegacyDeployment_IsTheUnchangedRendering(t *testing.T) {
	c := layoutTestCluster()
	legacy := c.k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutLegacy)
	if !reflect.DeepEqual(legacy, c.k8sDatabaseDeployment(layoutTestServer, 3306, "node-a")) {
		t.Fatal("k8sDatabaseDeployment is no longer the legacy layout")
	}
	init, db, jobs := layoutContainers(legacy)
	conf := [2]string{"/etc/mysql/conf.d", ".system/conf.d"}
	data := [2]string{"/var/lib/mysql", ""}
	ini := [2]string{"/docker-entrypoint-initdb.d", ".system/init"}
	for name, w := range map[string]struct {
		got  apiv1.Container
		want [][2]string
	}{
		"init":   {init, [][2]string{data, conf, ini}},
		"db":     {db, [][2]string{data, conf}},
		"dbjobs": {jobs, [][2]string{data, ini}},
	} {
		if got := mountPairs(w.got); !reflect.DeepEqual(got, w.want) {
			t.Errorf("legacy %s mounts = %v, want %v", name, got, w.want)
		}
	}
	cmd := init.Command[2]
	for _, frag := range []string{"rm -f /etc/mysql/conf.d/*.cnf 2>/dev/null; ", "cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/ 2>/dev/null; "} {
		if !strings.Contains(cmd, frag) {
			t.Errorf("legacy installer lost %q", frag)
		}
	}
	if strings.Contains(cmd, "no usable configuration") {
		t.Error("the configuration guard must not be in the legacy installer")
	}
}

// OpenSVC's bootstrap gives the data volume to dbVolumeOwner() after a successful configuration fetch: the configured
// owner, the legacy 999:999 when the owner is not managed, 1001:1001 for a Percona Server image. The new layout follows
// that for the init container's
// ownership step, whatever the process identity (prov-db-run-as-uid is independent); a legacy volume keeps its
// behaviour: the step exists only when the owner is managed.
func TestK8SServiceDirsDeployment_DatadirOwner(t *testing.T) {
	step := func(owner string) string {
		return `find /var/lib/mysql \( ! -user ` + strings.Split(owner, ":")[0] + ` -o ! -group ` + strings.Split(owner, ":")[1] +
			` \) -exec chown -h ` + owner + ` {} +`
	}
	for _, tc := range []struct {
		name, image, runAs, volume string
		svcOwner                   string // the owner step of service-dirs-v1
		legacyOwner                string // "" = no step on a legacy volume
	}{
		{"default MySQL", "mysql:8.4", "", "", "999:999", ""},
		{"default MariaDB", "mariadb:11.8", "", "", "999:999", ""},
		{"run-as alone is the process, the owner stays the legacy one", "mysql:8.4", "999:999", "", "999:999", ""},
		{"another run-as uid does not move the owner", "mysql:8.4", "1234:1235", "", "999:999", ""},
		{"default Percona Server", "percona/percona-server:8.4", "", "", "1001:1001", "1001:1001"},
		{"explicit owner", "mysql:8.4", "", "1234:1235", "1234:1235", "1234:1235"},
		{"explicit owner and the same process", "mysql:8.4", "1234:1235", "1234:1235", "1234:1235", "1234:1235"},
		{"root owner", "mysql:8.4", "", "0", "0:0", "0:0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := layoutTestCluster()
			c.Conf.ProvDbImg = tc.image
			c.Conf.ProvDBRunAsUID = tc.runAs
			c.Conf.ProvDBVolumeUID = tc.volume
			svc := c.k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutServiceDirs).Spec.Template.Spec.InitContainers[0].Command[2]
			legacy := c.k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutLegacy).Spec.Template.Spec.InitContainers[0].Command[2]

			if got := strings.Count(svc, "chown -h"); got != 1 || !strings.Contains(svc, step(tc.svcOwner)) {
				t.Fatalf("service-dirs-v1 init must hand /var/lib/mysql to %s exactly once (chown steps: %d): %s", tc.svcOwner, got, svc)
			}
			// before the guard, so a failed first fetch does not skip it, and never on /etc/mysql or the init directory
			if strings.Index(svc, "chown -h") > strings.Index(svc, "no usable configuration in /etc/mysql") {
				t.Fatalf("the owner step must come before the guard: %s", svc)
			}
			for _, other := range []string{"/etc/mysql", "/docker-entrypoint-initdb.d"} {
				for _, seg := range strings.Split(svc, " ; ") {
					if strings.Contains(seg, "chown") && strings.Contains(seg, other) && !strings.Contains(seg, "WARNING") {
						t.Fatalf("%s must not be chowned: %s", other, seg)
					}
				}
			}
			if tc.legacyOwner == "" {
				if strings.Contains(legacy, "chown") {
					t.Fatalf("a legacy volume must keep having no ownership step when the owner is not managed: %s", legacy)
				}
			} else if strings.Count(legacy, "chown -h") != 1 || !strings.Contains(legacy, step(tc.legacyOwner)) {
				t.Fatalf("a legacy volume must keep its managed owner %s: %s", tc.legacyOwner, legacy)
			}
		})
	}
}

// The two layouts differ in the init command and the mounts, and in nothing else.
func TestK8SServiceDirsDeployment_DiffersFromLegacyOnlyInInstallerAndMounts(t *testing.T) {
	c := layoutTestCluster()
	legacy := c.k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutLegacy)
	svc := c.k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", k8sLayoutServiceDirs)

	// the installer is the legacy one with exactly the archive fragments swapped and the guard added
	lcmd := legacy.Spec.Template.Spec.InitContainers[0].Command[2]
	want := strings.Replace(lcmd, "rm -f /etc/mysql/conf.d/*.cnf 2>/dev/null; ", "rm -f /etc/mysql/replication-manager.d/* /etc/mysql/conf.d/* 2>/dev/null; ", 1)
	want = strings.Replace(want, "cp /tmp/cfg/etc/mysql/conf.d/*.cnf /etc/mysql/conf.d/ 2>/dev/null; ", "cp -a /tmp/cfg/etc/mysql/. /etc/mysql/ 2>/dev/null; ", 1)
	const guard = ` ; if [ ! -s /etc/mysql/my.cnf ] || [ -z "$(ls -A /etc/mysql/conf.d 2>/dev/null)" ]; then echo "ERROR: no usable configuration in /etc/mysql (my.cnf missing or empty, or conf.d empty): the database is not started; the init container is retried" >&2; exit 1; fi`
	// the new layout gives the data to dbVolumeOwner() like OpenSVC's bootstrap, also when it is not managed
	// (here the legacy 999:999): the one step the legacy installer has no counterpart of for an unmanaged owner
	const owner = ` ; find /var/lib/mysql \( ! -user 999 -o ! -group 999 \) -exec chown -h 999:999 {} + || echo "WARNING: could not set the owner of /var/lib/mysql to 999:999" >&2`
	want = strings.Replace(want, ` ; exit "$MKDIR_STATUS"`, owner+guard+` ; exit "$MKDIR_STATUS"`, 1)
	if got := svc.Spec.Template.Spec.InitContainers[0].Command[2]; got != want {
		t.Fatalf("service-dirs installer is not the legacy one plus the two fragments, the owner step and the guard:\n got: %s\nwant: %s", got, want)
	}

	// nothing else differs: images, env, resources, identity, ports, probes, volumes, strategy
	norm := func(d *appsv1.Deployment) *appsv1.Deployment {
		d = d.DeepCopy()
		d.Spec.Template.Spec.InitContainers[0].Command = nil
		d.Spec.Template.Spec.InitContainers[0].VolumeMounts = nil
		for i := range d.Spec.Template.Spec.Containers {
			d.Spec.Template.Spec.Containers[i].VolumeMounts = nil
		}
		return d
	}
	if !reflect.DeepEqual(norm(legacy), norm(svc)) {
		t.Fatal("the layouts differ in more than the init command and the volume mounts")
	}
}

func TestK8SDatabasePVC_CarriesTheLayoutAnnotation(t *testing.T) {
	pvc := layoutTestCluster().k8sDatabasePVC(layoutTestServer)
	if got := pvc.Annotations[k8sPVCLayoutAnnotation]; got != "service-dirs-v1" {
		t.Fatalf("annotation = %q, want service-dirs-v1", got)
	}
}

func TestK8SPVCLayoutOf(t *testing.T) {
	mk := func(a map[string]string) *apiv1.PersistentVolumeClaim {
		return &apiv1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "x", Annotations: a}}
	}
	if l, err := k8sPVCLayoutOf(mk(nil)); err != nil || l != k8sLayoutLegacy {
		t.Errorf("no annotation: %v, %v; want legacy", l, err)
	}
	if l, err := k8sPVCLayoutOf(mk(map[string]string{"other": "v"})); err != nil || l != k8sLayoutLegacy {
		t.Errorf("unrelated annotation: %v, %v; want legacy", l, err)
	}
	if l, err := k8sPVCLayoutOf(mk(map[string]string{k8sPVCLayoutAnnotation: "service-dirs-v1"})); err != nil || l != k8sLayoutServiceDirs {
		t.Errorf("service-dirs-v1: %v, %v; want service-dirs", l, err)
	}
	if _, err := k8sPVCLayoutOf(mk(map[string]string{k8sPVCLayoutAnnotation: "service-dirs-v2"})); err == nil {
		t.Error("an unknown layout value must be refused, not rendered as a known one")
	}
}

func pvcVerbs(client *fake.Clientset) []string {
	var verbs []string
	for _, a := range client.Actions() {
		if a.GetResource().Resource == "persistentvolumeclaims" {
			verbs = append(verbs, a.GetVerb())
		}
	}
	return verbs
}

// A new PVC is created service-dirs-v1; an existing one is read and never modified.
func TestK8SEnsureDatabasePVC(t *testing.T) {
	c := layoutTestCluster()
	existing := func(a map[string]string) *apiv1.PersistentVolumeClaim {
		return &apiv1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "k8stest-db1-claim", Namespace: "k8stest", Annotations: a}}
	}

	t.Run("new PVC", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		l, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer))
		if err != nil || l != k8sLayoutServiceDirs {
			t.Fatalf("layout %v err %v, want service-dirs", l, err)
		}
		got, err := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), "k8stest-db1-claim", metav1.GetOptions{})
		if err != nil || got.Annotations[k8sPVCLayoutAnnotation] != "service-dirs-v1" {
			t.Fatalf("created PVC = %+v, %v; want the annotation", got, err)
		}
	})

	t.Run("existing PVC without annotation stays legacy and is not modified", func(t *testing.T) {
		client := fake.NewSimpleClientset(existing(nil))
		l, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer))
		if err != nil || l != k8sLayoutLegacy {
			t.Fatalf("layout %v err %v, want legacy", l, err)
		}
		for _, v := range pvcVerbs(client) {
			if v != "create" && v != "get" {
				t.Errorf("an existing PVC was touched: verb %s", v)
			}
		}
		got, _ := client.CoreV1().PersistentVolumeClaims("k8stest").Get(context.TODO(), "k8stest-db1-claim", metav1.GetOptions{})
		if len(got.Annotations) != 0 {
			t.Errorf("an existing legacy PVC was relabelled: %v", got.Annotations)
		}
	})

	t.Run("existing annotated PVC", func(t *testing.T) {
		client := fake.NewSimpleClientset(existing(map[string]string{k8sPVCLayoutAnnotation: "service-dirs-v1"}))
		if l, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer)); err != nil || l != k8sLayoutServiceDirs {
			t.Fatalf("layout %v err %v, want service-dirs", l, err)
		}
	})

	t.Run("existing PVC with an unknown layout is refused", func(t *testing.T) {
		client := fake.NewSimpleClientset(existing(map[string]string{k8sPVCLayoutAnnotation: "service-dirs-v9"}))
		if _, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer)); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("a refused create is not an existing PVC", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		client.PrependReactor("create", "persistentvolumeclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, apierrors.NewForbidden(apiv1.Resource("persistentvolumeclaims"), "x", errors.New("no"))
		})
		if _, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer)); err == nil {
			t.Fatal("expected the forbidden error")
		}
	})

	t.Run("an existing PVC that cannot be read is an error, not a guess", func(t *testing.T) {
		client := fake.NewSimpleClientset(existing(nil))
		client.PrependReactor("get", "persistentvolumeclaims", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("boom")
		})
		if _, err := c.k8sEnsureDatabasePVC(client, c.k8sDatabasePVC(layoutTestServer)); err == nil {
			t.Fatal("expected an error")
		}
	})
}

// ---- the installer, run for real ------------------------------------------------------------

type layoutEnv struct {
	t      *testing.T
	tmp    string
	script string
	shell  []string
}

// newLayoutEnv relocates the init command of one layout into a temporary directory: etc-mysql is the
// subPath directory that exists and is empty at the first start, init-out the init one.
func newLayoutEnv(t *testing.T, layout k8sPVCLayout, shell ...string) *layoutEnv {
	t.Helper()
	if len(shell) == 0 {
		shell = []string{"sh", "-c"}
	}
	if _, err := exec.LookPath(shell[0]); err != nil {
		t.Skipf("%s not available", shell[0])
	}
	tmp := t.TempDir()
	dep := layoutTestCluster().k8sDatabaseDeploymentWithLayout(layoutTestServer, 3306, "node-a", layout)
	script := dep.Spec.Template.Spec.InitContainers[0].Command[2]
	for _, r := range [][2]string{
		{"/tmp/config.tar.gz", tmp + "/config.tar.gz"},
		{"/tmp/replication-manager-cli.new", tmp + "/cli.new"},
		{"/tmp/cfg", tmp + "/cfg"},
		// the destinations are separate arguments, preceded by a space; the archive's own source path
		// (already relocated above) must not be rewritten again
		{" /etc/mysql", " " + tmp + "/etc-mysql"},
		{"/docker-entrypoint-initdb.d", tmp + "/init-out"},
		{"/var/lib/mysql", tmp + "/var-lib-mysql"},
	} {
		script = strings.ReplaceAll(script, r[0], r[1])
	}
	for _, d := range []string{"etc-mysql", "init-out", "var-lib-mysql"} {
		if err := os.MkdirAll(tmp+"/"+d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	return &layoutEnv{t: t, tmp: tmp, script: script, shell: shell}
}

func (e *layoutEnv) etc() string { return e.tmp + "/etc-mysql" }

// run executes the init command once, as a pod start does (/tmp is empty at every start).
func (e *layoutEnv) run(archive string, gate, fetch bool) (int, string) {
	e.t.Helper()
	os.RemoveAll(e.tmp + "/cfg")
	os.RemoveAll(e.tmp + "/config.tar.gz")
	bin := e.t.TempDir()
	stub := `#!/bin/sh
prev=""; out=""
for a in "$@"; do [ "$prev" = "-qO" ] && out="$a"; prev="$a"; done
case "$out" in
  /dev/null) [ "$GATE" = 1 ] || exit 1; exit 0 ;;
  */config.tar.gz) [ "$FETCH" = 1 ] || exit 1; cp "$ARCHIVE" "$out"; exit 0 ;;
esac
exit 1
`
	if err := os.WriteFile(bin+"/wget", []byte(stub), 0755); err != nil {
		e.t.Fatal(err)
	}
	b2i := map[bool]string{true: "1", false: "0"}
	// a shell function, not only a PATH entry: busybox sh runs its own wget applet before PATH
	prelude := "umask 022\nwget() { \"$WGETSTUB\" \"$@\"; }\n"
	args := append(append([]string{}, e.shell[1:]...), prelude+e.script)
	cmd := exec.Command(e.shell[0], args...)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "WGETSTUB="+bin+"/wget", "ARCHIVE="+archive, "GATE="+b2i[gate], "FETCH="+b2i[fetch])
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, string(out)
	case errors.As(err, &ee):
		return ee.ExitCode(), string(out)
	}
	e.t.Fatalf("run: %v", err)
	return -1, ""
}

func writeLayoutFile(t *testing.T, p, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// layoutArchive packs a generated-tree archive (etc/, init/, data/) shaped like the real one: my.cnf,
// conf.d symlinks into replication-manager.d, custom.d, ssl.
func layoutArchive(t *testing.T, generation string) string {
	t.Helper()
	root := t.TempDir()
	e := root + "/etc/mysql"
	writeLayoutFile(t, e+"/my.cnf", "# generation "+generation+"\n[mysqld]\n!includedir /etc/mysql/conf.d\n!includedir /etc/mysql/custom.d\n", 0644)
	writeLayoutFile(t, e+"/replication-manager.d/default_path.cnf", "[mysqld]\ndatadir=/var/lib/mysql\n# "+generation+"\n", 0644)
	writeLayoutFile(t, e+"/replication-manager.d/engine_innodb.cnf", "[mysqld]\ninnodb_buffer_pool_size=128M\n", 0644)
	writeLayoutFile(t, e+"/custom.d/03_agreed.cnf", "[mysqld]\n", 0644)
	writeLayoutFile(t, e+"/ssl/ca-cert.pem", "ca\n", 0644)
	writeLayoutFile(t, root+"/init/dbjobs_new", "#!/bin/sh\n", 0755)
	writeLayoutFile(t, root+"/init/dbjobs_launcher_with_sigterm", "#!/bin/sh\n", 0755)
	if err := os.MkdirAll(root+"/data/.system/tmp", 0755); err != nil {
		t.Fatal(err)
	}
	link := func(name, target string) {
		if err := os.MkdirAll(e+"/conf.d", 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, e+"/conf.d/"+name); err != nil {
			t.Fatal(err)
		}
	}
	link("01_default_path.cnf", "../replication-manager.d/default_path.cnf")
	if generation == "v1" {
		link("01_engine_innodb.cnf", "../replication-manager.d/engine_innodb.cnf")
	}
	out := filepath.Join(t.TempDir(), "config.tar.gz")
	if b, err := exec.Command("tar", "czf", out, "-C", root, "etc", "init", "data").CombinedOutput(); err != nil {
		t.Fatalf("pack: %v: %s", err, b)
	}
	return out
}

func corruptLayoutArchive(t *testing.T) string {
	t.Helper()
	good, err := os.ReadFile(layoutArchive(t, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.tar.gz")
	if err := os.WriteFile(p, good[:len(good)/2], 0644); err != nil { // a truncated download
		t.Fatal(err)
	}
	return p
}

func etcSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		info, _ := os.Lstat(p)
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(p)
			snap[rel] = "link " + target
		case info.IsDir():
			snap[rel] = "dir"
		default:
			b, _ := os.ReadFile(p)
			snap[rel] = fmt.Sprintf("file %o %s", info.Mode().Perm(), b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func layoutShells() map[string][]string {
	return map[string][]string{"sh": nil, "busybox": {"busybox", "sh", "-c"}}
}

// A first install that works leaves the generated tree, and the init container exits 0.
func TestK8SServiceDirsInstaller_FirstInstall(t *testing.T) {
	for name, shell := range layoutShells() {
		t.Run(name, func(t *testing.T) {
			e := newLayoutEnv(t, k8sLayoutServiceDirs, shell...)
			if rc, out := e.run(layoutArchive(t, "v1"), true, true); rc != 0 {
				t.Fatalf("exit %d: %s", rc, out)
			}
			snap := etcSnapshot(t, e.etc())
			for _, want := range []string{"my.cnf", "conf.d/01_default_path.cnf", "replication-manager.d/engine_innodb.cnf", "custom.d/03_agreed.cnf", "ssl/ca-cert.pem"} {
				if _, ok := snap[want]; !ok {
					t.Errorf("%s was not installed", want)
				}
			}
			if _, err := os.Stat(e.tmp + "/var-lib-mysql/.system/innodb/redo"); err != nil {
				t.Errorf(".system dirs were not created under the data mount: %v", err)
			}
			if _, err := os.Stat(e.tmp + "/init-out/dbjobs_new"); err != nil {
				t.Errorf("init scripts were not installed: %v", err)
			}
		})
	}
}

// The case of the failed first fetch on a new volume: whatever fails, the init container fails and no
// database starts without configuration. Legacy behaviour is exit 0 with an empty directory.
func TestK8SServiceDirsInstaller_FirstInstallFailureFailsClosed(t *testing.T) {
	cases := []struct {
		name          string
		archive       func(*testing.T) string
		gate, fetch   bool
		seedOperators bool
	}{
		{"need-config-fetch gate unreachable", func(t *testing.T) string { return layoutArchive(t, "v1") }, false, true, false},
		{"gate answers, /config fetch fails (HTTP 403)", func(t *testing.T) string { return layoutArchive(t, "v1") }, true, false, false},
		{"archive truncated", corruptLayoutArchive, true, true, false},
		{"a failed fetch beside operator files", func(t *testing.T) string { return layoutArchive(t, "v1") }, true, false, true},
	}
	for shellName, shell := range layoutShells() {
		for _, c := range cases {
			t.Run(shellName+"/"+c.name, func(t *testing.T) {
				e := newLayoutEnv(t, k8sLayoutServiceDirs, shell...)
				if c.seedOperators {
					writeLayoutFile(t, e.etc()+"/custom.d/50_operator.cnf", "[mysqld]\nop=1\n", 0600)
				}
				rc, out := e.run(c.archive(t), c.gate, c.fetch)
				if rc != 1 {
					t.Fatalf("exit %d, want 1 (the database must not start): %s", rc, out)
				}
				if !strings.Contains(out, "no usable configuration") {
					t.Errorf("the failure is not explained: %s", out)
				}
				if _, err := os.Stat(e.etc() + "/my.cnf"); err == nil {
					t.Error("my.cnf exists after a failed first install")
				}
			})
		}
	}
}

// The same failure on a legacy volume exits 0, as it always did: that layout is not changed here.
func TestK8SLegacyInstaller_FirstFetchFailureIsNotFailClosed(t *testing.T) {
	cluster := layoutTestCluster()
	tmp := t.TempDir()
	script := buildRelocatedInitScript(t, cluster, layoutTestServer, tmp)
	if strings.Contains(script, "no usable configuration") {
		t.Fatal("the guard is in the legacy installer")
	}
	bin := t.TempDir()
	if err := os.WriteFile(bin+"/wget", []byte("#!/bin/sh\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("legacy installer exit: %v: %s", err, out)
	}
}

// A later refresh that fails changes nothing, whatever fails: the last valid tree stays, operator
// files included, and the init container exits 0 so the database starts on it.
func TestK8SServiceDirsInstaller_LaterRefreshFailurePreservesTheTree(t *testing.T) {
	failures := map[string]struct {
		archive     func(*testing.T) string
		gate, fetch bool
	}{
		"gate unreachable":      {func(t *testing.T) string { return layoutArchive(t, "v2") }, false, true},
		"/config fetch fails":   {func(t *testing.T) string { return layoutArchive(t, "v2") }, true, false},
		"archive truncated":     {corruptLayoutArchive, true, true},
		"gate and fetch failed": {func(t *testing.T) string { return layoutArchive(t, "v2") }, false, false},
	}
	for shellName, shell := range layoutShells() {
		for name, f := range failures {
			t.Run(shellName+"/"+name, func(t *testing.T) {
				e := newLayoutEnv(t, k8sLayoutServiceDirs, shell...)
				if rc, out := e.run(layoutArchive(t, "v1"), true, true); rc != 0 {
					t.Fatalf("first install exit %d: %s", rc, out)
				}
				writeLayoutFile(t, e.etc()+"/custom.d/50_operator.cnf", "[mysqld]\nop=1\n", 0600)
				before := etcSnapshot(t, e.etc())
				rc, out := e.run(f.archive(t), f.gate, f.fetch)
				if rc != 0 {
					t.Fatalf("exit %d after a failed refresh, want 0: %s", rc, out)
				}
				if after := etcSnapshot(t, e.etc()); !reflect.DeepEqual(before, after) {
					t.Errorf("the installed tree changed on a failed refresh:\n before=%v\n after=%v", before, after)
				}
			})
		}
	}
}

// A successful later refresh replaces what the generator owns and keeps what the operator added.
func TestK8SServiceDirsInstaller_RefreshKeepsOperatorFiles(t *testing.T) {
	e := newLayoutEnv(t, k8sLayoutServiceDirs)
	if rc, out := e.run(layoutArchive(t, "v1"), true, true); rc != 0 {
		t.Fatalf("exit %d: %s", rc, out)
	}
	writeLayoutFile(t, e.etc()+"/custom.d/50_operator.cnf", "[mysqld]\nop=1\n", 0600)
	writeLayoutFile(t, e.etc()+"/extra.cnf", "[mysqld]\nextra=1\n", 0640)
	if rc, out := e.run(layoutArchive(t, "v2"), true, true); rc != 0 {
		t.Fatalf("refresh exit %d: %s", rc, out)
	}
	snap := etcSnapshot(t, e.etc())
	if _, ok := snap["conf.d/01_engine_innodb.cnf"]; ok {
		t.Error("a conf.d entry the generator dropped is still there")
	}
	if !strings.Contains(snap["my.cnf"], "generation v2") {
		t.Errorf("my.cnf was not refreshed: %q", snap["my.cnf"])
	}
	if snap["custom.d/50_operator.cnf"] != "file 600 [mysqld]\nop=1\n" || snap["extra.cnf"] != "file 640 [mysqld]\nextra=1\n" {
		t.Errorf("operator files were not preserved: %q %q", snap["custom.d/50_operator.cnf"], snap["extra.cnf"])
	}
}

// What "usable" means: a non-empty my.cnf and a conf.d with entries; a partial tree does not pass.
func TestK8SServiceDirsInstaller_GuardRejectsAPartialTree(t *testing.T) {
	seed := func(e *layoutEnv, myCnf string, confd bool) {
		writeLayoutFile(t, e.etc()+"/my.cnf", myCnf, 0644)
		if confd {
			writeLayoutFile(t, e.etc()+"/conf.d/01_x.cnf", "[mysqld]\n", 0644)
		} else if err := os.MkdirAll(e.etc()+"/conf.d", 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, c := range map[string]struct {
		myCnf string
		confd bool
		rc    int
	}{
		"empty my.cnf":            {"", true, 1},
		"my.cnf but empty conf.d": {"[mysqld]\n", false, 1},
		"complete tree":           {"[mysqld]\n", true, 0},
	} {
		t.Run(name, func(t *testing.T) {
			e := newLayoutEnv(t, k8sLayoutServiceDirs)
			seed(e, c.myCnf, c.confd)
			if rc, out := e.run(layoutArchive(t, "v1"), false, false); rc != c.rc {
				t.Fatalf("exit %d, want %d: %s", rc, c.rc, out)
			}
		})
	}
}
