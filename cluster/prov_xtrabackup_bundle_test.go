package cluster

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestXtrabackupAutoImage(t *testing.T) {
	for _, tc := range []struct {
		image, want string
		err         bool
	}{
		{image: "mysql:8.4.11", want: "percona/percona-xtrabackup:8.4"},
		{image: "mysql:8.4", want: "percona/percona-xtrabackup:8.4"},
		{image: "mysql:8.0.35", want: "percona/percona-xtrabackup:8.0"},
		{image: "docker.io/library/mysql:8.0", want: "percona/percona-xtrabackup:8.0"},
		{image: "registry.example:5000/mirror/mysql:8.4.11-oracle", want: "percona/percona-xtrabackup:8.4"},
		{image: "mysql:5.7.44", want: "percona/percona-xtrabackup:2.4"},
		{image: "percona/percona-server:8.4.11-11", want: "percona/percona-xtrabackup:8.4"},
		{image: "percona/percona-server:8.0.35", want: "percona/percona-xtrabackup:8.0"},
		{image: "mariadb:11.8", want: ""},
		{image: "mariadb", want: ""},
		{image: "mysql:latest", err: true},
		{image: "mysql", err: true},
		{image: "mysql:9.1", err: true},
		{image: "mysql@sha256:abc", err: true},
		{image: "percona/percona-server@sha256:abc", err: true},
		{image: "registry.example/mysql-custom:8.0.35", err: true},
		{image: "acme/custom:8.4", err: true},
		{image: "", err: true},
	} {
		got, err := xtrabackupAutoImage(tc.image, nil)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("xtrabackupAutoImage(%q) = %q, %v; want %q, error=%v", tc.image, got, err, tc.want, tc.err)
		}
	}
}

// With the catalog's list of xtrabackup tags the series is answered for when its tag is published: a new series needs
// no change of the code, and one the catalog does not list is refused.
func TestXtrabackupAutoImageFromCatalog(t *testing.T) {
	published := map[string]bool{"8.0": true, "8.4": true, "9.7": true, "2.4": true, "latest": true}
	for _, tc := range []struct {
		image, want string
		err         bool
	}{
		{image: "mysql:8.4.11", want: "percona/percona-xtrabackup:8.4"},
		{image: "mysql:9.7.0", want: "percona/percona-xtrabackup:9.7"}, // not in the built-in list: the catalog has it
		{image: "mysql:5.7.44", want: "percona/percona-xtrabackup:2.4"},
		{image: "mysql:9.1", err: true}, // the catalog has no 9.1 tag
		{image: "percona/percona-server:8.0.35", want: "percona/percona-xtrabackup:8.0"},
		{image: "mariadb:11.8", want: ""},
	} {
		got, err := xtrabackupAutoImage(tc.image, published)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("xtrabackupAutoImage(%q, catalog) = %q, %v; want %q, error=%v", tc.image, got, err, tc.want, tc.err)
		}
	}
	// a catalog that lists the repository without the tag of the series: refused, even for a series the built-in list knows
	if _, err := xtrabackupAutoImage("mysql:8.4", map[string]bool{"8.0": true}); err == nil {
		t.Error("8.4 must be refused when the catalog does not publish the 8.4 tag")
	}
}

// The catalog embedded in the binary must list the xtrabackup repository with the tags the built-in series need: it
// is what the render reads when no back-office catalog was pushed.
func TestEmbeddedCatalogListsXtrabackup(t *testing.T) {
	cluster := newBundleCluster("mysql:8.4", "auto")
	tags := cluster.xtrabackupPublishedTags()
	if tags == nil {
		t.Fatal("the embedded repos.json does not list the xtrabackup repository (scripts/updaterepo.sh)")
	}
	for series := range xtrabackupKnownSeries {
		if !tags[xtrabackupSeriesTag(series)] {
			t.Errorf("the embedded catalog does not list the xtrabackup tag %s of the series %s", xtrabackupSeriesTag(series), series)
		}
	}
}

// A catalog without the xtrabackup repository (a back-office file that predates it) falls back to the built-in series.
func TestXtrabackupCatalogWithoutRepository(t *testing.T) {
	saved := xtrabackupCatalog
	defer func() { xtrabackupCatalog = saved }()
	xtrabackupCatalog = func(*config.Config) []config.DockerRepo {
		return []config.DockerRepo{{Name: "mariadb"}, {Name: "proxysql"}}
	}
	cluster := newBundleCluster("mysql:8.4", "auto")
	if tags := cluster.xtrabackupPublishedTags(); tags != nil {
		t.Fatalf("no xtrabackup repository in the catalog: want nil, got %v", tags)
	}
	if got := cluster.xtrabackupBundleImageSetting(); got != "percona/percona-xtrabackup:8.4" {
		t.Errorf("fallback to the built-in series: image = %q", got)
	}
}

func TestValidateXtrabackupImage(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"", true},
		{"auto", true},
		{"percona/percona-xtrabackup:8.4", true},
		{"registry.example:5000/percona/percona-xtrabackup:8.4.0-7", true},
		{"percona/percona-xtrabackup@sha256:0123456789abcdef", true},
		{"percona/percona-xtrabackup:8.4 --privileged", false},
		{"img;rm -rf /", false},
		{"img$(id)", false},
		{"img`id`", false},
		{`img"x`, false},
		{"img'x", false},
		{"img\nx", false},
		{"-v /:/host", false},
		{"/absolute", false},
		{strings.Repeat("a", 256), false},
	} {
		if err := ValidateXtrabackupImage(tc.value); (err == nil) != tc.ok {
			t.Errorf("ValidateXtrabackupImage(%q) = %v, want ok=%v", tc.value, err, tc.ok)
		}
	}
}

func newBundleCluster(dbImage, xtrabackupImage string) *Cluster {
	c := newIdentityCluster(dbImage, "", "")
	c.Conf.ProvDbDockerXtrabackupImg = xtrabackupImage
	return c
}

// The pipeline that runs the bundle script carries the script itself, with the mount path filled in, and the
// image reference; nothing else.
func TestXtrabackupBundleCommand(t *testing.T) {
	cluster := newBundleCluster("mysql:8.4", "percona/percona-xtrabackup:8.4")
	command, err := cluster.xtrabackupBundleCommand()
	if err != nil {
		t.Fatal(err)
	}
	const suffix = " | base64 -d | XB_IMAGE=percona/percona-xtrabackup:8.4 XB_SERIES=8.4 sh"
	if !strings.HasPrefix(command, "echo ") || !strings.HasSuffix(command, suffix) {
		t.Fatalf("unexpected pipeline shape: %.80s ... %s", command, command[max(0, len(command)-90):])
	}
	script, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(command, "echo "), suffix))
	if err != nil {
		t.Fatalf("the script is not valid base64: %v", err)
	}
	if strings.Contains(string(script), "@MOUNT@") || !strings.Contains(string(script), `MOUNT="/opt/xtrabackup"`) {
		t.Error("the mount path must be substituted in the script")
	}
	for _, want := range []string{"xtrabackup xbstream socat", "--library-path", "XB_IMAGE"} {
		if !strings.Contains(string(script), want) {
			t.Errorf("script does not contain %q", want)
		}
	}
	if strings.Contains(string(script), "LD_LIBRARY_PATH=") {
		t.Error("the bundle must never set LD_LIBRARY_PATH")
	}
}

func TestXtrabackupBundleOpenSVC(t *testing.T) {
	const legacyMounts = `/etc/localtime:/etc/localtime:ro {name}/jobs:/var/lib/replication-manager-jobs:rw {name}/data:/var/lib/mysql:rw {name}/etc/mysql:/etc/mysql:rw {name}/init:/docker-entrypoint-initdb.d:rw {name}/run/mysqld:/run/mysqld:rw {name}-sec/:/credentials`

	t.Run("empty setting renders what it rendered before", func(t *testing.T) {
		cluster := newBundleCluster("mysql:8.4", "")
		if section := cluster.OpenSVCGetXtrabackupBundleContainerSection(); len(section) != 0 {
			t.Fatalf("no helper container expected, got %v", section)
		}
		if got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetJobsContainerSection()["volume_mounts"]; got != legacyMounts {
			t.Errorf("jobs mounts = %q, want the legacy ones", got)
		}
		if got := cluster.OpenSVCGetVolumeDataSection()["directories"]; got != "run/mysqld" {
			t.Errorf("volume directories = %q, want run/mysqld", got)
		}
	})

	t.Run("an image renders the helper, the read-only jobs mount and the volume directory", func(t *testing.T) {
		cluster := newBundleCluster("mysql:8.4", "percona/percona-xtrabackup:8.4")
		section := cluster.OpenSVCGetXtrabackupBundleContainerSection()
		for k, want := range map[string]string{
			"image": "percona/percona-xtrabackup:8.4", "detach": "false", "optional": "true", "rm": "true",
			"run_args": "--user 0:0", "entrypoint": "/bin/sh",
			"volume_mounts": "/etc/localtime:/etc/localtime:ro {name}/xtrabackup:/bundle",
		} {
			if section[k] != want {
				t.Errorf("helper %s = %q, want %q", k, section[k], want)
			}
		}
		if !strings.HasPrefix(section["command"], `-c "echo `) || !strings.HasSuffix(section["command"], ` sh"`) {
			t.Errorf("helper command has an unexpected shape")
		}
		if got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetJobsContainerSection()["volume_mounts"]; got != legacyMounts+" {name}/xtrabackup:/opt/xtrabackup:ro" {
			t.Errorf("jobs mounts = %q, want the legacy ones plus the read-only bundle", got)
		}
		if got := cluster.OpenSVCGetVolumeDataSection()["directories"]; got != "run/mysqld xtrabackup" {
			t.Errorf("volume directories = %q", got)
		}
		// the database container keeps running the database image
		if got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetDBContainerSection()["image"]; got != "{env.docker_image}" {
			t.Errorf("database container image = %q, must stay the database image", got)
		}
	})

	t.Run("force pull follows the existing setting", func(t *testing.T) {
		cluster := newBundleCluster("mysql:8.4", "percona/percona-xtrabackup:8.4")
		cluster.Conf.ProvOpensvcImageForcePull = true
		if got := cluster.OpenSVCGetXtrabackupBundleContainerSection()["image_pull_policy"]; got != "always" {
			t.Errorf("image_pull_policy = %q, want always", got)
		}
	})

	t.Run("one template uses one resolved helper image everywhere", func(t *testing.T) {
		cluster := newBundleCluster("mysql:8.4", "percona/percona-xtrabackup:8.4")
		cluster.Conf.ProvDiskType = "volume"
		cluster.Conf.ProvAgents = "agent"
		server := &ServerMonitor{Id: "db1", Port: "3306", ClusterGroup: cluster}
		cluster.Servers = []*ServerMonitor{server}
		cluster.Agents = []Agent{{HostName: "agent"}}
		section := server.GenerateDBTemplateMap()
		if got := section["container#03"]["image"]; got != "percona/percona-xtrabackup:8.4" {
			t.Errorf("helper image = %q", got)
		}
		if got := section["volume#01"]["directories"]; got != "run/mysqld xtrabackup" {
			t.Errorf("volume directories = %q", got)
		}
		if got := section["container#jobs"]["volume_mounts"]; got != legacyMounts+" {name}/xtrabackup:/opt/xtrabackup:ro" {
			t.Errorf("jobs mounts = %q", got)
		}
		if got := section["container#jobs"]["environment"]; got != "MYSQL_INITDB_SKIP_TZINFO=yes PATH="+bundlePathStock {
			t.Errorf("jobs environment = %q", got)
		}
	})

	t.Run("auto derives the image from the database image, and stays off when it cannot", func(t *testing.T) {
		if got := newBundleCluster("mysql:8.0.35", "auto").OpenSVCGetXtrabackupBundleContainerSection()["image"]; got != "percona/percona-xtrabackup:8.0" {
			t.Errorf("auto on mysql:8.0.35 = %q", got)
		}
		for _, image := range []string{"mariadb:11.8", "mysql:latest", "registry.example/mysql-custom:8.0.35", "registry.example:5000/mirror/mysql:8.4", "myco/mysql:8.0-custom"} {
			cluster := newBundleCluster(image, "auto")
			if cluster.xtrabackupBundleEnabled() || len(cluster.OpenSVCGetXtrabackupBundleContainerSection()) != 0 {
				t.Errorf("auto on %s must leave the injection off", image)
			}
			if got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetJobsContainerSection()["volume_mounts"]; got != legacyMounts {
				t.Errorf("auto on %s changed the jobs mounts: %q", image, got)
			}
		}
	})
}

// The tools must be on the PATH of the jobs container itself, not only inside dbjobs_new.sh: a jobs container of a stock
// image that runs an older script has no socat, cannot reach the repman API and so can never fetch the new script.
// Appended last, so a tool the image ships wins; set only for the stock images (their default PATH is known, and
// the environment replaces the whole PATH of the image).
const bundlePathStock = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/opt/xtrabackup/current/bin"

func TestXtrabackupBundlePathOpenSVC(t *testing.T) {
	environment := func(dbImage, setting string) string {
		return (&ServerMonitor{ClusterGroup: newBundleCluster(dbImage, setting)}).OpenSVCGetJobsContainerSection()["environment"]
	}
	if got := environment("mysql:8.4", ""); got != "MYSQL_INITDB_SKIP_TZINFO=yes" {
		t.Errorf("empty setting: environment = %q, want the legacy one", got)
	}
	for _, image := range []string{"mysql:8.0.35", "percona/percona-server:8.4", "docker.io/library/mysql:8.4"} {
		if got := environment(image, "auto"); got != "MYSQL_INITDB_SKIP_TZINFO=yes PATH="+bundlePathStock {
			t.Errorf("%s with auto: environment = %q", image, got)
		}
	}
	if got := environment("mysql:8.4", "percona/percona-xtrabackup:8.4"); got != "MYSQL_INITDB_SKIP_TZINFO=yes PATH="+bundlePathStock {
		t.Errorf("an explicit xtrabackup image on a stock image: environment = %q", got)
	}
	// not an official image: nothing is rendered at all, the PATH of the container is left alone and no helper,
	// volume or mount is added (a copied bundle that is not on the PATH would be a half-done injection)
	for _, image := range []string{"registry.example/team/mysql-custom:8.0", "myco/mysql:8.0-custom", "registry.example/mirror/mysql:8.4", "mariadb:11.8"} {
		cluster := newBundleCluster(image, "percona/percona-xtrabackup:8.0")
		if cluster.xtrabackupBundleEnabled() || len(cluster.OpenSVCGetXtrabackupBundleContainerSection()) != 0 {
			t.Errorf("%s with an explicit image: the injection must be off", image)
		}
		if got := environment(image, "percona/percona-xtrabackup:8.0"); got != "MYSQL_INITDB_SKIP_TZINFO=yes" {
			t.Errorf("%s with an explicit image: environment = %q, the PATH must not be set", image, got)
		}
	}
}

func TestIsStockMySQLImage(t *testing.T) {
	for ref, want := range map[string]bool{
		"mysql:8.0.35":                                true,
		"percona/percona-server:8.4":                  true,
		"docker.io/library/mysql:8.4":                 true,
		"docker.io/percona/percona-server:8.0":        true,
		"registry.example/mirror/mysql:8.4":           false,
		"myco/mysql:8.0-custom":                       false,
		"registry.example/percona/percona-server:8.4": false,
		"mysql@sha256:abcdef":                         true,
		"mariadb:10.11":                               false,
		"registry.example/team/mysql-custom:8":        false,
		"":                                            false,
	} {
		if got := IsStockMySQLImage(ref); got != want {
			t.Errorf("IsStockMySQLImage(%q) = %v, want %v", ref, got, want)
		}
	}
}

func TestXtrabackupBundleKubernetes(t *testing.T) {
	build := func(setting string) (spec struct {
		volumes   int
		inits     []string
		jobsMount string
		dbMounts  int
		initImage string
		initRoot  bool
		limit     string
		jobsPath  string
		dbPath    string
	}) {
		cluster := newTestCluster("k8stest")
		cluster.Conf.ProvDbImg = "mysql:8.4"
		cluster.Conf.ProvDbDockerXtrabackupImg = setting
		pod := cluster.k8sDatabaseDeployment(&ServerMonitor{Name: "db1", Port: "3306"}, 3306, "node-a").Spec.Template.Spec
		spec.volumes = len(pod.Volumes)
		for _, v := range pod.Volumes {
			if v.Name == "db1-xtrabackup" && v.EmptyDir != nil && v.EmptyDir.SizeLimit != nil {
				spec.limit = v.EmptyDir.SizeLimit.String()
			}
		}
		for _, c := range pod.InitContainers {
			spec.inits = append(spec.inits, c.Name)
			if c.Name == "db1-xtrabackup" {
				spec.initImage = c.Image
				spec.initRoot = c.SecurityContext != nil && c.SecurityContext.RunAsUser != nil && *c.SecurityContext.RunAsUser == 0
			}
		}
		for _, c := range pod.Containers {
			for _, e := range c.Env {
				if e.Name == "PATH" {
					if c.Name == "db1-dbjobs" {
						spec.jobsPath = e.Value
					} else {
						spec.dbPath = e.Value
					}
				}
			}
			for _, m := range c.VolumeMounts {
				if m.Name != "db1-xtrabackup" {
					continue
				}
				if c.Name == "db1-dbjobs" && m.MountPath == "/opt/xtrabackup" && m.ReadOnly {
					spec.jobsMount = m.MountPath
				} else {
					spec.dbMounts++
				}
			}
		}
		return spec
	}

	t.Run("empty setting renders what it rendered before", func(t *testing.T) {
		spec := build("")
		if spec.volumes != 1 || len(spec.inits) != 1 || spec.jobsMount != "" || spec.dbMounts != 0 || spec.jobsPath != "" || spec.dbPath != "" {
			t.Fatalf("expected the legacy pod (1 volume, 1 init container, no bundle mount), got %+v", spec)
		}
	})

	t.Run("an image renders a bounded emptyDir, the helper init and the read-only jobs mount", func(t *testing.T) {
		spec := build("percona/percona-xtrabackup:8.4")
		if spec.volumes != 2 || spec.limit != "256Mi" {
			t.Errorf("volumes = %d, emptyDir limit = %q; want 2 and 256Mi", spec.volumes, spec.limit)
		}
		if len(spec.inits) != 2 || spec.inits[0] != "db1-init" || spec.inits[1] != "db1-xtrabackup" {
			t.Errorf("init containers = %v, want the bootstrap one first, then the helper", spec.inits)
		}
		if spec.initImage != "percona/percona-xtrabackup:8.4" || !spec.initRoot {
			t.Errorf("helper image = %q root=%v", spec.initImage, spec.initRoot)
		}
		if spec.jobsMount != "/opt/xtrabackup" || spec.dbMounts != 0 {
			t.Errorf("the bundle must be mounted read-only in dbjobs only: jobs=%q other=%d", spec.jobsMount, spec.dbMounts)
		}
		if spec.jobsPath != bundlePathStock || spec.dbPath != "" {
			t.Errorf("the PATH with the bundle last goes to dbjobs only: jobs=%q database=%q", spec.jobsPath, spec.dbPath)
		}
	})

	t.Run("auto follows the database image", func(t *testing.T) {
		if got := build("auto").initImage; got != "percona/percona-xtrabackup:8.4" {
			t.Errorf("auto on mysql:8.4 = %q", got)
		}
	})
}

// A value written in a TOML file or a flag never goes through the settings API: it must be checked before it is
// rendered into a shell command and a service definition.
func TestXtrabackupBundleImageFromConfig(t *testing.T) {
	for _, tc := range []struct {
		value, want string
	}{
		{"percona/percona-xtrabackup:8.4", "percona/percona-xtrabackup:8.4"},
		{"  percona/percona-xtrabackup:8.4  ", "percona/percona-xtrabackup:8.4"},
		{"", ""},
		{"percona/percona-xtrabackup:8.4 --privileged", ""},
		{"percona/percona-xtrabackup:8.4; touch /tmp/x", ""},
		{`percona/percona-xtrabackup:8.4" sh -c "id`, ""},
		{"percona/percona-xtrabackup:8.4\nid", ""},
		{"$(id)", ""},
		{strings.Repeat("a", 300), ""},
	} {
		cluster := newBundleCluster("mysql:8.4", tc.value)
		if got := cluster.xtrabackupBundleImage(); got != tc.want {
			t.Errorf("image %q resolved to %q, want %q", tc.value, got, tc.want)
		}
		if tc.want == "" {
			if cluster.xtrabackupBundleEnabled() {
				t.Errorf("image %q must leave the injection off", tc.value)
			}
			if section := cluster.OpenSVCGetXtrabackupBundleContainerSection(); len(section) != 0 {
				t.Errorf("image %q rendered an OpenSVC helper: %v", tc.value, section)
			}
			if got := cluster.OpenSVCGetVolumeDataSection()["directories"]; got != "run/mysqld" {
				t.Errorf("image %q changed the volume directories to %q", tc.value, got)
			}
		}
	}
}

func TestXtrabackupServerSeries(t *testing.T) {
	cases := map[string]string{
		"mysql:8.0.35":                       "8.0",
		"percona/percona-server:8.4":         "8.4",
		"registry.local/mirror/mysql:5.7.44": "",
		"mysql:latest":                       "",
		"mysql":                              "",
		"mysql@sha256:abcdef":                "",
		"mariadb:10.11":                      "",
		"private/db:8.4":                     "",
	}
	for image, want := range cases {
		cluster := newBundleCluster(image, "percona/percona-xtrabackup:8.4")
		if got := cluster.xtrabackupServerSeries(); got != want {
			t.Errorf("%q: series %q, want %q", image, got, want)
		}
	}
}

// The unit tests never reach a registry: the check of the helper image answers "can be pulled" unless a test says
// otherwise (prov_xtrabackup_helper_check_test.go).
func init() {
	xtrabackupHelperCheck = func(context.Context, string) error { return nil }
}

// The settings API refuses a bad value to the operator, leaves the setting as it was, and refuses an orchestrator that
// does not render the injection. (The cookie that marks the services stale is raised per server and needs a working
// directory: it is covered by the reprovision tests of the other image settings, not here.)
func TestSetProvDbDockerXtrabackupImg(t *testing.T) {
	cluster := newBundleCluster("mysql:8.4", "")
	cluster.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC

	for _, value := range []string{"auto", "percona/percona-xtrabackup:8.4", "  percona/percona-xtrabackup:8.0  ", ""} {
		if err := cluster.SetProvDbDockerXtrabackupImg(value); err != nil {
			t.Errorf("%q must be accepted: %v", value, err)
		}
		if got, want := cluster.Conf.ProvDbDockerXtrabackupImg, strings.TrimSpace(value); got != want {
			t.Errorf("after %q the setting is %q, want %q", value, got, want)
		}
	}

	cluster.Conf.ProvDbDockerXtrabackupImg = "percona/percona-xtrabackup:8.4"
	for _, value := range []string{"a b", "image;rm -rf /", "$(id)", "x`y`", "image\nother", "percona/percona-xtrabackup:8.4\"", strings.Repeat("a", 300)} {
		if err := cluster.SetProvDbDockerXtrabackupImg(value); err == nil {
			t.Errorf("%q must be refused", value)
		}
		if cluster.Conf.ProvDbDockerXtrabackupImg != "percona/percona-xtrabackup:8.4" {
			t.Errorf("a refused value changed the setting to %q", cluster.Conf.ProvDbDockerXtrabackupImg)
		}
	}

	for _, orchestrator := range []string{config.ConstOrchestratorKubernetes} {
		cluster.Conf.ProvOrchestrator = orchestrator
		if err := cluster.SetProvDbDockerXtrabackupImg("auto"); err != nil {
			t.Errorf("%s renders the injection, the setting must be accepted: %v", orchestrator, err)
		}
	}
	for _, orchestrator := range []string{config.ConstOrchestratorLocalhost, config.ConstOrchestratorOnPremise, config.ConstOrchestratorSlapOS} {
		cluster.Conf.ProvOrchestrator = orchestrator
		cluster.Conf.ProvDbDockerXtrabackupImg = ""
		if err := cluster.SetProvDbDockerXtrabackupImg("auto"); err == nil {
			t.Errorf("%s does not render the injection, the setting must be refused", orchestrator)
		}
		if cluster.Conf.ProvDbDockerXtrabackupImg != "" {
			t.Errorf("%s: a refused setting changed the value to %q", orchestrator, cluster.Conf.ProvDbDockerXtrabackupImg)
		}
		// clearing is always allowed: the dashboard's Off must work on a cluster where the setting can no longer be set
		cluster.Conf.ProvDbDockerXtrabackupImg = "percona/percona-xtrabackup:8.4"
		if err := cluster.SetProvDbDockerXtrabackupImg(""); err != nil {
			t.Errorf("%s: clearing the setting must be accepted: %v", orchestrator, err)
		}
		if cluster.Conf.ProvDbDockerXtrabackupImg != "" {
			t.Errorf("%s: the setting is %q after clearing, want empty", orchestrator, cluster.Conf.ProvDbDockerXtrabackupImg)
		}
	}
}

// What a cluster read of the catalog is its own: the catalog depends on the cluster's configuration (the back-office
// file), so a cluster does not get the answer another one read, and each keeps its answer for a while.
func TestXtrabackupCatalogIsPerCluster(t *testing.T) {
	saved := xtrabackupCatalog
	defer func() { xtrabackupCatalog = saved }()
	reads := 0
	xtrabackupCatalog = func(conf *config.Config) []config.DockerRepo {
		reads++
		// the catalog of the cluster whose database image is mysql:8.0 publishes 8.0 only, the other publishes 8.4 only
		tag := "8.4"
		if conf.ProvDbImg == "mysql:8.0" {
			tag = "8.0"
		}
		return []config.DockerRepo{{Name: xtrabackupCatalogName, Tags: config.DockerTag{Results: []config.TagResult{{Name: tag}}}}}
	}
	a := newBundleCluster("mysql:8.0", "auto")
	b := newBundleCluster("mysql:8.4", "auto")
	for i := 0; i < 3; i++ {
		if got := a.xtrabackupBundleImageSetting(); got != "percona/percona-xtrabackup:8.0" {
			t.Errorf("cluster A: image = %q", got)
		}
		if got := b.xtrabackupBundleImageSetting(); got != "percona/percona-xtrabackup:8.4" {
			t.Errorf("cluster B: image = %q", got)
		}
	}
	if reads != 2 {
		t.Errorf("the catalog was read %d times for two clusters asking three times each, want 2 (one each)", reads)
	}
}
