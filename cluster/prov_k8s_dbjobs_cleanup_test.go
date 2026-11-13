package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"
)

// The Pod-replacing paths -- restart, force re-pull, and the scale-to-zero rolling upgrade -- all
// remove the dbjobs memory request and limit an older version gave the template (#2369), in either
// prov-db-docker-run-args-limit mode (OpenSVC's jobs container carries none in either), and touch the
// database container's memory only with the limit on.

// legacyDeployment is a Deployment as develop built it: database limit 384Mi (prov-db-memory 512M
// minus the old 128Mi carve-out), dbjobs request and limit 128Mi.
func legacyDeployment(replicas int32) *appsv1.Deployment {
	db, jobs := resource.MustParse("384Mi"), resource.MustParse("128Mi")
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "db1", Namespace: "k8stest"},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: apiv1.PodTemplateSpec{Spec: apiv1.PodSpec{Containers: []apiv1.Container{
				{Name: "db1", Resources: apiv1.ResourceRequirements{
					Requests: apiv1.ResourceList{apiv1.ResourceMemory: db},
					Limits:   apiv1.ResourceList{apiv1.ResourceMemory: db},
				}},
				{Name: "db1-dbjobs", Resources: apiv1.ResourceRequirements{
					Requests: apiv1.ResourceList{apiv1.ResourceMemory: jobs},
					Limits:   apiv1.ResourceList{apiv1.ResourceMemory: jobs},
				}},
			}}},
		},
	}
}

// capturePatches records the body of every Deployment patch and lets the fake tracker apply it.
func capturePatches(client *fake.Clientset) *[]string {
	var patches []string
	client.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		patches = append(patches, string(action.(k8stesting.PatchAction).GetPatch()))
		return false, nil, nil
	})
	return &patches
}

func k8sDBJobsCleanupCluster(limitOn bool) *Cluster {
	cl := newTestCluster("k8stest")
	cl.Conf.ProvDBDockerRunArgsLimit = limitOn
	cl.Conf.ProvMem = "512M" // no DBU tier wired: the cap is prov-db-memory, 512Mi
	return cl
}

func assertDBJobsClean(t *testing.T, dep *appsv1.Deployment) {
	t.Helper()
	jobs := dep.Spec.Template.Spec.Containers[1]
	if len(jobs.Resources.Requests) != 0 || len(jobs.Resources.Limits) != 0 {
		t.Fatalf("dbjobs must lose its old memory request and limit, got %#v", jobs.Resources)
	}
}

func assertDBMemory(t *testing.T, dep *appsv1.Deployment, want string) {
	t.Helper()
	db := dep.Spec.Template.Spec.Containers[0].Resources
	for kind, got := range map[string]apiv1.ResourceList{"request": db.Requests, "limit": db.Limits} {
		mem := got[apiv1.ResourceMemory]
		if mem.Cmp(resource.MustParse(want)) != 0 {
			t.Fatalf("database memory %s = %s, want %s", kind, mem.String(), want)
		}
	}
}

func TestK8SPodReplacingPathsCleanLegacyDBJobsResources(t *testing.T) {
	paths := map[string]func(*Cluster, *fake.Clientset) error{
		"restart": func(cl *Cluster, c *fake.Clientset) error { return cl.k8sRestartDatabaseServiceWithClient(c, "db1") },
		"repull": func(cl *Cluster, c *fake.Clientset) error {
			return cl.k8sForceRepullDatabaseServiceWithClient(c, "db1")
		},
		"rolling upgrade (scale 0)": func(cl *Cluster, c *fake.Clientset) error {
			return cl.k8sUpdateDatabaseServiceConfigWithClient(c, "db1", false)
		},
	}
	for name, run := range paths {
		for _, limitOn := range []bool{true, false} {
			t.Run(name+"/limit="+map[bool]string{true: "on", false: "off"}[limitOn], func(t *testing.T) {
				client := fake.NewSimpleClientset(legacyDeployment(0)) // scale 0: also valid for the upgrade path
				cl := k8sDBJobsCleanupCluster(limitOn)

				if err := run(cl, client); err != nil {
					t.Fatalf("unexpected error: %s", err)
				}
				dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				assertDBJobsClean(t, dep)
				if limitOn {
					assertDBMemory(t, dep, "512Mi") // the cap, on every Pod-replacing path
				} else {
					assertDBMemory(t, dep, "384Mi") // limit off: the database memory is left as it was
				}
			})
		}
	}
}

// A template without legacy dbjobs resources gets no resources in the patch at all when the limit is
// off (byte-identical to before), so nothing triggers a spurious change.
func TestK8SPodReplacingPathsSendNoResourcesWhenNothingToClean(t *testing.T) {
	clean := legacyDeployment(0)
	clean.Spec.Template.Spec.Containers[1].Resources = apiv1.ResourceRequirements{}
	for name, run := range map[string]func(*Cluster, *fake.Clientset) error{
		"restart": func(cl *Cluster, c *fake.Clientset) error { return cl.k8sRestartDatabaseServiceWithClient(c, "db1") },
		"repull": func(cl *Cluster, c *fake.Clientset) error {
			return cl.k8sForceRepullDatabaseServiceWithClient(c, "db1")
		},
		"update": func(cl *Cluster, c *fake.Clientset) error {
			return cl.k8sUpdateDatabaseServiceConfigWithClient(c, "db1", false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			client := fake.NewSimpleClientset(clean.DeepCopy())
			patches := capturePatches(client)
			if err := run(k8sDBJobsCleanupCluster(false), client); err != nil {
				t.Fatalf("unexpected error: %s", err)
			}
			if len(*patches) != 1 || strings.Contains((*patches)[0], `"resources"`) {
				t.Fatalf("flag off, clean template: want one patch without resources, got %v", *patches)
			}
		})
	}
}

// Limit off: a failed read of the Deployment only skips the dbjobs clean-up; the restart and the
// re-pull go on as they always did. Limit on, the read stays required.
func TestK8SPodReplacingPathsReadFailure(t *testing.T) {
	failGet := func(client *fake.Clientset) {
		client.PrependReactor("get", "deployments", func(k8stesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("api unavailable")
		})
	}
	for name, run := range map[string]func(*Cluster, *fake.Clientset) error{
		"restart": func(cl *Cluster, c *fake.Clientset) error { return cl.k8sRestartDatabaseServiceWithClient(c, "db1") },
		"repull": func(cl *Cluster, c *fake.Clientset) error {
			return cl.k8sForceRepullDatabaseServiceWithClient(c, "db1")
		},
	} {
		t.Run(name+"/limit off goes on", func(t *testing.T) {
			client := fake.NewSimpleClientset(legacyDeployment(1))
			failGet(client)
			if err := run(k8sDBJobsCleanupCluster(false), client); err != nil {
				t.Fatalf("limit off: a failed read must not fail the %s: %s", name, err)
			}
		})
		t.Run(name+"/limit on is an error", func(t *testing.T) {
			client := fake.NewSimpleClientset(legacyDeployment(1))
			failGet(client)
			if err := run(k8sDBJobsCleanupCluster(true), client); err == nil {
				t.Fatalf("limit on: a failed read must fail the %s", name)
			}
		})
	}
}

// --- native resize confirmation (cluster_resize_k8s.go) ---

// podWithApplied is a database Pod whose spec limit is memLimit. applied "" = the server reports no
// applied resources at all; "none" = it reports a structure with no memory limit in it; otherwise
// the applied memory limit.
func podWithApplied(memLimit, applied string) *apiv1.Pod {
	pod := k8sResizeTestPod("k8stest", "db1-abc", "db1", memLimit, "", "rs-uid")
	switch applied {
	case "":
	case "none":
		pod.Status.ContainerStatuses[0].Resources = &apiv1.ResourceRequirements{Limits: apiv1.ResourceList{}}
	default:
		pod.Status.ContainerStatuses[0].Resources = &apiv1.ResourceRequirements{
			Limits: apiv1.ResourceList{apiv1.ResourceMemory: resource.MustParse(applied)},
		}
	}
	return pod
}

// The spec at the target is what the API accepted. When the kubelet reports the applied resources
// they must hold the target; a reported structure without the memory limit is an incomplete state,
// not a confirmation; only a server that reports nothing keeps the spec check alone.
func TestK8sResizeConfirmedChecksAppliedLimitWhenReported(t *testing.T) {
	target := resource.MustParse("512Mi")
	cases := []struct {
		name    string
		spec    string
		applied string
		want    bool
	}{
		{"spec and applied at target", "512Mi", "512Mi", true},
		{"spec at target, no applied resources reported", "512Mi", "", true},
		{"spec at target, applied still lower", "512Mi", "384Mi", false},
		{"spec at target, applied structure has no memory limit", "512Mi", "none", false},
		{"spec not at target", "384Mi", "384Mi", false},
	}
	for _, tc := range cases {
		if got := k8sResizeConfirmed(podWithApplied(tc.spec, tc.applied), "db1", target, 0); got != tc.want {
			t.Errorf("%s: confirmed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The same distinction for the CPU limit of the pair.
func TestK8sResizeConfirmedChecksAppliedCPUWhenReported(t *testing.T) {
	target := resource.MustParse("512Mi")
	mk := func(applied *apiv1.ResourceRequirements) *apiv1.Pod {
		pod := podWithApplied("512Mi", "512Mi")
		cpu := resource.MustParse("2")
		pod.Spec.Containers[0].Resources.Limits[apiv1.ResourceCPU] = cpu
		pod.Status.ContainerStatuses[0].Resources = applied
		return pod
	}
	mem := resource.MustParse("512Mi")
	if !k8sResizeConfirmed(mk(nil), "db1", target, 2000) {
		t.Error("no applied resources reported: the spec is what there is")
	}
	if !k8sResizeConfirmed(mk(&apiv1.ResourceRequirements{Limits: apiv1.ResourceList{apiv1.ResourceMemory: mem, apiv1.ResourceCPU: resource.MustParse("2")}}), "db1", target, 2000) {
		t.Error("memory and cpu applied: confirmed")
	}
	if k8sResizeConfirmed(mk(&apiv1.ResourceRequirements{Limits: apiv1.ResourceList{apiv1.ResourceMemory: mem}}), "db1", target, 2000) {
		t.Error("applied structure without a cpu limit must not confirm a cpu target")
	}
	if k8sResizeConfirmed(mk(&apiv1.ResourceRequirements{Limits: apiv1.ResourceList{apiv1.ResourceMemory: mem, apiv1.ResourceCPU: resource.MustParse("1")}}), "db1", target, 2000) {
		t.Error("applied cpu below the target must not confirm")
	}
}

func resizeSetup(t *testing.T, pod *apiv1.Pod) (*Cluster, *ServerMonitor, *fake.Clientset, func()) {
	t.Helper()
	cl, s, cleanup := k8sResizeTestServer(t, "k8stest", "db1")
	cl.Conf.ProvMem = "512M" // no DBU tier wired: the cap is prov-db-memory, 512Mi
	dep, rs := k8sTestController("k8stest", "db1")
	pod.OwnerReferences = []metav1.OwnerReference{{UID: rs.UID}}
	return cl, s, fake.NewSimpleClientset(dep, rs, pod), cleanup
}

func countPodPatches(client *fake.Clientset) *int {
	n := 0
	client.PrependReactor("patch", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { n++; return false, nil, nil })
	return &n
}

// A container that already carries the target limit is not a resize, and the Kubernetes resizer has
// no counterpart of OpenSVC's service-level step (pg_mem_limit + pg update): it is NOT reported as
// applied. A grow follows the shared "no live infra step" outcome -- not applied, a restart
// scheduled, the database memory not raised -- and a shrink is not applied with nothing scheduled.
func TestK8sResizeUnchangedLimitIsNeverReportedApplied(t *testing.T) {
	for _, applied := range []string{"512Mi", ""} { // reported and confirmed, or not reported at all
		t.Run("grow/applied="+applied, func(t *testing.T) {
			cl, s, client, cleanup := resizeSetup(t, podWithApplied("512Mi", applied))
			defer cleanup()
			patches := countPodPatches(client)

			ok, err := cl.k8sResizeWithClient(client, s, true)
			if err != nil || ok {
				t.Fatalf("applied=%v err=%v, want applied=false with no error", ok, err)
			}
			if !s.HasRestartCookie() {
				t.Fatal("a grow with no live service-level step schedules a restart (the shared outcome)")
			}
			if *patches != 0 || s.GetPendingK8sMemoryResize() != nil {
				t.Fatalf("no Pod change expected: patches=%d pending=%v", *patches, s.GetPendingK8sMemoryResize())
			}
			if s.IssuedBufferPoolBytes != 0 {
				t.Fatal("the database memory must not be raised")
			}
		})
		t.Run("shrink/applied="+applied, func(t *testing.T) {
			cl, s, client, cleanup := resizeSetup(t, podWithApplied("512Mi", applied))
			defer cleanup()
			patches := countPodPatches(client)

			ok, err := cl.k8sResizeWithClient(client, s, false)
			if err != nil || ok {
				t.Fatalf("applied=%v err=%v, want applied=false with no error", ok, err)
			}
			if *patches != 0 || s.GetPendingK8sMemoryResize() != nil || s.HasRestartCookie() {
				t.Fatalf("nothing to do on a shrink: patches=%d pending=%v restart=%v", *patches, s.GetPendingK8sMemoryResize(), s.HasRestartCookie())
			}
		})
	}
}

// Regression: the spec already shows the target but the kubelet still applies an older limit.
// The resize is not confirmed, it is re-patched and tracked as a memory change, and when the
// kubelet finally applies it the confirmation raises the database memory. The memory flag used to
// be taken from the spec, so this case was recorded as a CPU-only step and the grow was skipped.
func TestK8sResizeStaleAppliedLimitStillRaisesDatabaseMemory(t *testing.T) {
	cl, s, client, cleanup := resizeSetup(t, podWithApplied("512Mi", "384Mi"))
	defer cleanup()

	ok, err := cl.k8sResizeWithClient(client, s, true)
	if err != nil || ok {
		t.Fatalf("applied=%v err=%v, want applied=false: the applied limit is stale", ok, err)
	}
	pending := s.GetPendingK8sMemoryResize()
	if pending == nil || pending.TargetMB != 512 {
		t.Fatalf("expected a pending resize to 512, got %#v", pending)
	}
	if !pending.MemChanged {
		t.Fatal("a stale applied limit is a memory change not yet done: MemChanged must be true")
	}
	if s.HasRestartCookie() {
		t.Fatal("a pending resize must not schedule a restart")
	}

	// the kubelet applies it: the confirmation raises the database memory
	pod := podWithApplied("512Mi", "512Mi")
	dep, rs := k8sTestController("k8stest", "db1")
	pod.OwnerReferences = []metav1.OwnerReference{{UID: rs.UID}}
	client2 := fake.NewSimpleClientset(dep, rs, pod)
	s.SetPendingK8sMemoryResize(&K8sMemoryResizeState{TargetMB: 512, Grow: true, MemChanged: true, StartedAt: time.Now(), Observed: true})
	cl.k8sCompletePendingMemoryResizeWithClient(client2, s, "")
	if s.GetPendingK8sMemoryResize() != nil {
		t.Fatal("pending must be cleared once confirmed")
	}
	if s.IssuedBufferPoolBytes <= 0 {
		t.Fatal("the confirmed memory grow must have issued the database memory change")
	}
}

// A reported applied structure that has no memory limit is incomplete, not unavailable: the same
// spec-at-target Pod is not confirmed and goes through the patch.
func TestK8sResizeAppliedStructureWithoutMemoryLimitIsNotConfirmed(t *testing.T) {
	cl, s, client, cleanup := resizeSetup(t, podWithApplied("512Mi", "none"))
	defer cleanup()
	patches := countPodPatches(client)

	ok, err := cl.k8sResizeWithClient(client, s, true)
	if err != nil || ok {
		t.Fatalf("applied=%v err=%v, want applied=false", ok, err)
	}
	if *patches != 1 {
		t.Fatalf("expected the resize to be patched, patches=%d", *patches)
	}
	if p := s.GetPendingK8sMemoryResize(); p == nil || !p.MemChanged {
		t.Fatalf("expected a pending memory change, got %#v", p)
	}
	if s.HasRestartCookie() {
		t.Fatal("no restart expected for a pending resize")
	}
}

// prov-db-memory is validated as openSVCResize does: it must parse, or the resize fails and the
// database memory is kept; a non-positive value is refused as well. No API call is made, no resize is
// tracked, no restart is scheduled.
func TestK8sResizeRefusesInvalidProvMem(t *testing.T) {
	// With a DBU tier wired the cap is derivable whatever prov-db-memory says (GetDBContainerMemoryCapMB
	// ignores its parse error): only the resize's own input validation refuses it.
	for _, tier := range []bool{false, true} {
		for _, bad := range []string{"", "abc", "-5M", "0", "0M"} {
			for _, grow := range []bool{true, false} {
				name := bad + "/tier=" + map[bool]string{true: "true", false: "false"}[tier] + "/grow=" + map[bool]string{true: "true", false: "false"}[grow]
				t.Run(name, func(t *testing.T) {
					cl, s, client, cleanup := resizeSetup(t, podWithApplied("384Mi", ""))
					defer cleanup()
					if tier {
						cl.Conf.ProvDbDbu = 2
						cl.resources = NewResourceManager()
					}
					cl.Conf.ProvMem = bad
					patches := countPodPatches(client)

					ok, err := cl.k8sResizeWithClient(client, s, grow)
					if ok || err == nil {
						t.Fatalf("applied=%v err=%v, want applied=false and an error", ok, err)
					}
					if *patches != 0 || s.GetPendingK8sMemoryResize() != nil || s.HasRestartCookie() {
						t.Fatalf("an invalid input must change nothing: patches=%d pending=%v restart=%v", *patches, s.GetPendingK8sMemoryResize(), s.HasRestartCookie())
					}
				})
			}
		}
	}
}

// --- the scale-to-zero rolling upgrade reconciles the database memory too (OpenSVC re-renders the
// full deployment before the start) ---

// develop's Deployment: database 384Mi request == limit plus a 2-core CPU pair, images and pull
// policies of the old template, dbjobs 128Mi request == limit, scaled to zero by the upgrade.
func developDeploymentForUpgrade() *appsv1.Deployment {
	dep := legacyDeployment(0)
	cpu := resource.MustParse("2")
	db := &dep.Spec.Template.Spec.Containers[0]
	db.Image, db.ImagePullPolicy = "mariadb:10.6", apiv1.PullIfNotPresent
	db.Resources.Requests[apiv1.ResourceCPU], db.Resources.Limits[apiv1.ResourceCPU] = cpu, cpu
	jobs := &dep.Spec.Template.Spec.Containers[1]
	jobs.Image, jobs.ImagePullPolicy = "mariadb:10.6", apiv1.PullIfNotPresent
	return dep
}

func upgradeCluster(limitOn bool) *Cluster {
	cl := k8sDBJobsCleanupCluster(limitOn)
	cl.Conf.ProvDbImg = "mariadb:10.11"
	return cl
}

// Limit on: from a develop-created Deployment the database container goes to the OpenSVC cap, request
// AND limit; its CPU pair, the scale-to-zero state and the image semantics are untouched; dbjobs loses
// its old request and limit. The cap is the DBU tier's, not prov-db-memory's, when the plan is larger.
func TestK8SUpdateDatabaseServiceConfigReconcilesDatabaseMemoryLimitOn(t *testing.T) {
	client := fake.NewSimpleClientset(developDeploymentForUpgrade())
	patches := capturePatches(client)
	cl := upgradeCluster(true)
	s := &ServerMonitor{Name: "db1", Port: "3306"}
	cl.Servers = []*ServerMonitor{s}
	cl.Conf.ProvDbDbu = 2 // plan tier: GetDBContainerMemoryCapMB() = 8192, above prov-db-memory 512M
	cl.resources = NewResourceManager()
	wantCap := cl.GetDBContainerMemoryCapMB()
	if wantCap != 8192 {
		t.Fatalf("fixture: cap = %d, want 8192", wantCap)
	}

	if err := cl.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	dep, err := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertDBMemory(t, dep, strconv.Itoa(wantCap)+"Mi")
	db := dep.Spec.Template.Spec.Containers[0]
	if cpu := db.Resources.Limits[apiv1.ResourceCPU]; cpu.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("the CPU pair must be untouched, got %s", cpu.String())
	}
	if cpu := db.Resources.Requests[apiv1.ResourceCPU]; cpu.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("the CPU request must be untouched, got %s", cpu.String())
	}
	assertDBJobsClean(t, dep)
	// image semantics and lifecycle unchanged: both containers on the new image, pull policy as set,
	// still scaled to zero (the start is the caller's)
	for _, c := range dep.Spec.Template.Spec.Containers {
		if c.Image != "mariadb:10.11" {
			t.Fatalf("container %s image = %q, want mariadb:10.11", c.Name, c.Image)
		}
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 0 {
		t.Fatalf("the upgrade must not scale the Deployment, replicas = %v", dep.Spec.Replicas)
	}
	if len(*patches) != 1 {
		t.Fatalf("expected a single patch, got %d", len(*patches))
	}
}

// A restart keeps the running image (#1861): the reconciliation does not change that.
func TestK8SUpdateDatabaseServiceConfigKeepImageStillReconcilesDatabaseMemory(t *testing.T) {
	client := fake.NewSimpleClientset(developDeploymentForUpgrade())
	cl := upgradeCluster(true)
	if err := cl.k8sUpdateDatabaseServiceConfigWithClientImage(client, "db1", false, true); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	dep, _ := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	assertDBMemory(t, dep, "512Mi")
	if img := dep.Spec.Template.Spec.Containers[0].Image; img != "mariadb:10.6" {
		t.Fatalf("keepImage: the running image must stay, got %q", img)
	}
}

// Limit off: the database memory AND CPU of the old template are left exactly as they were; dbjobs is
// still cleaned and the image still moves.
func TestK8SUpdateDatabaseServiceConfigLimitOffLeavesDatabaseResources(t *testing.T) {
	client := fake.NewSimpleClientset(developDeploymentForUpgrade())
	cl := upgradeCluster(false)
	if err := cl.k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	dep, _ := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	assertDBMemory(t, dep, "384Mi")
	if cpu := dep.Spec.Template.Spec.Containers[0].Resources.Limits[apiv1.ResourceCPU]; cpu.Cmp(resource.MustParse("2")) != 0 {
		t.Fatalf("CPU must be untouched, got %s", cpu.String())
	}
	assertDBJobsClean(t, dep)
	if img := dep.Spec.Template.Spec.Containers[0].Image; img != "mariadb:10.11" {
		t.Fatalf("image = %q, want mariadb:10.11", img)
	}
}

// The scale-to-zero precondition is unchanged: a Deployment that is not at zero is refused and
// nothing, memory included, is patched.
func TestK8SUpdateDatabaseServiceConfigStillRefusesALiveDeployment(t *testing.T) {
	dep := developDeploymentForUpgrade()
	one := int32(1)
	dep.Spec.Replicas = &one
	client := fake.NewSimpleClientset(dep)
	patches := capturePatches(client)
	if err := upgradeCluster(true).k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err == nil {
		t.Fatal("expected an error: the Deployment is not scaled to zero")
	}
	if len(*patches) != 0 {
		t.Fatalf("nothing may be patched, got %v", *patches)
	}
}

// No dbjobs container (an older Deployment): the database memory is reconciled and no container is
// conjured up.
func TestK8SUpdateDatabaseServiceConfigWithoutDBJobsContainer(t *testing.T) {
	dep := developDeploymentForUpgrade()
	dep.Spec.Template.Spec.Containers = dep.Spec.Template.Spec.Containers[:1]
	client := fake.NewSimpleClientset(dep)
	if err := upgradeCluster(true).k8sUpdateDatabaseServiceConfigWithClient(client, "db1", false); err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	got, _ := client.AppsV1().Deployments("k8stest").Get(context.TODO(), "db1", metav1.GetOptions{})
	if len(got.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected the single database container, got %d", len(got.Spec.Template.Spec.Containers))
	}
	assertDBMemory(t, got, "512Mi")
}

// --- the optional read has its own deadline: a timed-out read must not expire the patch ---

// The fake clientset ignores contexts, so these tests use a real client-go client against an HTTP
// server whose GET of the Deployment never answers within the (shortened) read deadline. They
// record every PATCH that reaches the server.
func deadlineServer(t *testing.T) (kubernetes.Interface, func() []string) {
	t.Helper()
	const dep = `{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"db1","namespace":"k8stest"}}`
	var mu sync.Mutex
	var patches []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			select { // hangs until the client gives up (its deadline) or a safety net
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Second):
			}
		case http.MethodPatch:
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			patches = append(patches, string(body))
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(dep))
	}))
	t.Cleanup(srv.Close)
	client, err := kubernetes.NewForConfig(&rest.Config{Host: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	old := k8sDeploymentReadTimeout
	k8sDeploymentReadTimeout = 150 * time.Millisecond
	t.Cleanup(func() { k8sDeploymentReadTimeout = old })
	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), patches...)
	}
}

func TestK8SPodReplacingPathsReadDeadlineDoesNotExpireThePatch(t *testing.T) {
	paths := map[string]func(*Cluster, kubernetes.Interface) error{
		"restart": func(cl *Cluster, c kubernetes.Interface) error {
			return cl.k8sRestartDatabaseServiceWithClient(c, "db1")
		},
		"repull": func(cl *Cluster, c kubernetes.Interface) error {
			return cl.k8sForceRepullDatabaseServiceWithClient(c, "db1")
		},
	}
	for name, run := range paths {
		t.Run(name+"/limit off: the read times out, the patch still goes", func(t *testing.T) {
			client, patches := deadlineServer(t)
			start := time.Now()
			if err := run(k8sDBJobsCleanupCluster(false), client); err != nil {
				t.Fatalf("a timed-out optional read must not fail the %s: %s", name, err)
			}
			if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
				t.Fatalf("the read did not run into its deadline (took %s): the test proves nothing", elapsed)
			}
			got := patches()
			if len(got) != 1 {
				t.Fatalf("expected exactly one patch after the timed-out read, got %d", len(got))
			}
			if !strings.Contains(got[0], "restartedAt") || strings.Contains(got[0], `"resources"`) {
				t.Fatalf("a clean patch (restart annotation, no resources) was expected, got %s", got[0])
			}
		})
		t.Run(name+"/limit on: the read is required, nothing is patched", func(t *testing.T) {
			client, patches := deadlineServer(t)
			err := run(k8sDBJobsCleanupCluster(true), client)
			if err == nil {
				t.Fatalf("limit on: a timed-out read must fail the %s", name)
			}
			if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "deadline") {
				t.Fatalf("expected a deadline error, got %v", err)
			}
			if got := patches(); len(got) != 0 {
				t.Fatalf("fail closed: no patch may be sent, got %v", got)
			}
		})
	}
}
