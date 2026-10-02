package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newIdentityCluster(image, runAs, chown string) *Cluster {
	return &Cluster{Conf: &config.Config{
		ProvVolumeData:        "default",
		ProvDbImg:             image,
		ProvDBRunAsUID:        runAs,
		ProvDBVolumeUID:       chown,
		ProvType:              "docker",
		ProvDBDockerTmpfsSize: "0",
	}}
}

func TestOpenSVCDatabaseIdentity(t *testing.T) {
	type want struct{ volume, runArgs, initEnv, jobsArgs string }
	const initBase = "REPLICATION_MANAGER_CLUSTER_NAME={namespace} REPLICATION_MANAGER_HOST_NAME={fqdn} REPLICATION_MANAGER_HOST_PORT=3306"
	owner := func(uid, gid string) string {
		return initBase + " REPLICATION_MANAGER_DB_VOLUME_UID=" + uid + " REPLICATION_MANAGER_DB_VOLUME_GID=" + gid
	}
	for _, tc := range []struct {
		name, image, runAs, chown string
		want                      want
	}{
		// Both empty is the legacy rendering, unchanged: volume 999:999, `--user mysql`
		// only for images named mysql, no owner in the bootstrap, jobs untouched.
		{"empty mariadb is legacy", "mariadb:11.8", "", "", want{"999:999", "", initBase, ""}},
		{"empty mysql is legacy", "mysql:8.4", "", "", want{"999:999", " --user mysql", initBase, ""}},
		// Percona Server images are built for 1001: the empty owner is 1001 for them, the
		// process keeps running as the image's own user (1001) as it always did.
		{"empty percona owns the volume 1001", "percona/percona-server:8.4", "", "", want{"1001:1001", "", owner("1001", "1001"), "--user 0:0"}},
		{"empty percona by name, any case", "repman-lab/Percona-Server-pxb:8.4", "", "", want{"1001:1001", "", owner("1001", "1001"), "--user 0:0"}},
		// The two settings are independent.
		{"run as alone leaves the owner legacy", "mariadb:11.8", "0", "", want{"999:999", " --user 0:0", initBase, "--user 0:0"}},
		{"chown alone leaves the process legacy", "mariadb:11.8", "", "1234", want{"1234:1234", "", owner("1234", "1234"), "--user 0:0"}},
		{"chown alone on mysql keeps --user mysql", "mysql:8.4", "", "1234", want{"1234:1234", " --user mysql", owner("1234", "1234"), "--user 0:0"}},
		{"both", "mariadb:11.8", "1001:1002", "1001:1002", want{"1001:1002", " --user 1001:1002", owner("1001", "1002"), "--user 0:0"}},
		{"different values", "mariadb:11.8", "2000", "999:1001", want{"999:1001", " --user 2000:2000", owner("999", "1001"), "--user 0:0"}},
		{"root owner is rendered literally", "mariadb:11.8", "", "0", want{"0:0", "", owner("0", "0"), "--user 0:0"}},
		{"run as beats the mysql legacy rule", "mysql:8.4", "1234", "", want{"999:999", " --user 1234:1234", initBase, "--user 0:0"}},
		{"chown beats the percona default", "percona/percona-server:8.4", "", "999", want{"999:999", "", owner("999", "999"), "--user 0:0"}},
		{"invalid values are handled as empty", "mariadb:11.8", "mysql", "x:y", want{"999:999", "", initBase, ""}},
		// an invalid value is logged and handled as empty, which on a Percona Server image is still 1001
		{"invalid values on percona fall back to its 1001", "percona/percona-server:8.4", "mysql", "x:y", want{"1001:1001", "", owner("1001", "1001"), "--user 0:0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := newIdentityCluster(tc.image, tc.runAs, tc.chown)
			server := &ServerMonitor{ClusterGroup: cluster}
			volume := cluster.OpenSVCGetVolumeDataSection()
			if got := volume["user"] + ":" + volume["group"]; got != tc.want.volume {
				t.Errorf("data volume ownership = %s, want %s", got, tc.want.volume)
			}
			if got := server.OpenSVCGetDBContainerSection()["run_args"]; got != tc.want.runArgs {
				t.Errorf("database run arguments = %q, want %q", got, tc.want.runArgs)
			}
			if got := cluster.OpenSVCGetDBInitContainerSection("3306")["environment"]; got != tc.want.initEnv {
				t.Errorf("init environment = %q, want %q", got, tc.want.initEnv)
			}
			if got := server.OpenSVCGetJobsContainerSection()["run_args"]; got != tc.want.jobsArgs {
				t.Errorf("jobs run arguments = %q, want %q", got, tc.want.jobsArgs)
			}
		})
	}

	t.Run("jobs run as root ahead of operator run args when managed", func(t *testing.T) {
		cluster := newIdentityCluster("mariadb:11.8", "", "1001")
		cluster.Conf.ProvDBJobsDockerRunArgs = "--ulimit nofile=262144:262144"
		server := &ServerMonitor{ClusterGroup: cluster}
		if got := server.OpenSVCGetJobsContainerSection()["run_args"]; got != "--user 0:0 --ulimit nofile=262144:262144" {
			t.Fatalf("jobs run arguments = %q, want root first then the operator arguments", got)
		}
		cluster.Conf.ProvDBVolumeUID = ""
		if got := server.OpenSVCGetJobsContainerSection()["run_args"]; got != "--ulimit nofile=262144:262144" {
			t.Fatalf("jobs run arguments = %q, want the operator arguments alone (legacy)", got)
		}
	})
}

func TestParseDBIdentity(t *testing.T) {
	for _, tc := range []struct {
		value    string
		uid, gid int
		set, ok  bool
	}{
		{value: "", ok: true},
		{value: "  ", ok: true},
		{value: "0", set: true, ok: true},
		{value: " 1001 ", uid: 1001, gid: 1001, set: true, ok: true},
		{value: "1001:999", uid: 1001, gid: 999, set: true, ok: true},
		{value: "0:0", set: true, ok: true},
		{value: "2147483647:2147483647", uid: 2147483647, gid: 2147483647, set: true, ok: true},
		{value: "2147483648"},
		{value: "1001:2147483648"},
		{value: "-1"},
		{value: "mysql"},
		{value: "mysql:mysql"},
		{value: "1001:"},
		{value: ":1001"},
		{value: "1001:1001:1001"},
		{value: "+1001"},
		{value: "1001 : 1001"},
		{value: "0x10"},
	} {
		uid, gid, set, err := ParseDBIdentity("prov-db-run-as-uid", tc.value)
		if (err == nil) != tc.ok || uid != tc.uid || gid != tc.gid || set != tc.set {
			t.Errorf("ParseDBIdentity(%q) = %d, %d, %v, %v; want %d, %d, %v, ok=%v", tc.value, uid, gid, set, err, tc.uid, tc.gid, tc.set, tc.ok)
		}
	}
}

// The init container section is shared with the OpenSVC proxy services, whose
// data (ProxySQL, ShardProxy) is chowned by the same bootstrap. They must keep
// the legacy 999 owner whatever identity the database runs with.
func TestOpenSVCInitContainerDatabaseIdentityNotLeakedToProxies(t *testing.T) {
	cluster := newIdentityCluster("percona/percona-server:8.4", "", "1234")
	if env := cluster.OpenSVCGetInitContainerSection("1999")["environment"]; strings.Contains(env, "DB_VOLUME") {
		t.Fatalf("proxy init container must not carry the database identity, got %q", env)
	}
	env := cluster.OpenSVCGetDBInitContainerSection("3306")["environment"]
	if !strings.Contains(env, "REPLICATION_MANAGER_DB_VOLUME_UID=1234 REPLICATION_MANAGER_DB_VOLUME_GID=1234") {
		t.Fatalf("database init container must carry the identity, got %q", env)
	}
}

// With a managed identity an operator --user in prov-db-docker-run-args keeps winning
// (docker takes the last --user), as it did for every image that is not named "mysql".
func TestOpenSVCDBContainerKeepsOperatorUser(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		want       bool
	}{
		{"none", "--ulimit nofile=262144:262144", false},
		{"long flag", "--user 2000:2000", true},
		{"long flag with equals", "--sysctl a=1 --user=2000", true},
		{"short flag", "-u 2000", true},
		{"short flag attached", "-u2000:2000", true},
		{"ulimit is not a user", "--ulimit nofile=1:1 -e USER=x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := newIdentityCluster("mariadb:11.8", "999", "")
			cluster.Conf.ProvDBDockerRunArgs = tc.args
			got := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetDBContainerSection()["run_args"]
			if appended := strings.Contains(got, "--user 999:999"); appended == tc.want {
				t.Fatalf("run_args = %q: --user appended = %v, want %v", got, appended, !tc.want)
			}
		})
	}
}

// An explicit --user in prov-db-docker-run-args is an escape hatch that wins over
// prov-db-run-as-uid: nothing is appended after it, while the data volume and the bootstrap
// still follow prov-db-volume-uid and the process follows the operator's --user. The
// operator then owns the match (see the OpenSVC section of
// doc/implementation/cluster/DATABASE_RUNTIME_UID_GID.md). Pinned here so the
// behavior cannot change silently.
func TestOpenSVCDBContainerExplicitUserWinsOverConfiguredPair(t *testing.T) {
	cluster := newIdentityCluster("mariadb:11.8", "1001", "1001")
	cluster.Conf.ProvDBDockerRunArgs = "--ulimit nofile=1:1 --user 2000:2000"
	run := (&ServerMonitor{ClusterGroup: cluster}).OpenSVCGetDBContainerSection()["run_args"]
	if strings.Count(run, "--user") != 1 || !strings.Contains(run, "--user 2000:2000") {
		t.Fatalf("run_args = %q, want exactly the operator's --user 2000:2000", run)
	}
	volume := cluster.OpenSVCGetVolumeDataSection()
	if volume["user"] != "1001" || volume["group"] != "1001" {
		t.Errorf("data volume ownership = %s:%s, want the configured 1001:1001", volume["user"], volume["group"])
	}
	if env := cluster.OpenSVCGetDBInitContainerSection("3306")["environment"]; !strings.Contains(env, "REPLICATION_MANAGER_DB_VOLUME_UID=1001 REPLICATION_MANAGER_DB_VOLUME_GID=1001") {
		t.Errorf("bootstrap must still receive the configured owner, got %q", env)
	}
}

func TestDBVolumeOwner(t *testing.T) {
	for _, tc := range []struct {
		name, image, value string
		uid, gid           int
		managed            bool
	}{
		{"empty is the legacy owner", "mariadb:11.8", "", 999, 999, false},
		{"empty on a Percona Server image", "percona/percona-server:8.4", "", 1001, 1001, true},
		{"empty on a private image whose name contains percona", "registry.example/Percona-custom:8.4", "", 1001, 1001, true},
		{"a value wins over the Percona default", "percona/percona-server:8.4", "999", 999, 999, true},
		{"uid:gid", "mariadb:11.8", "1234:1235", 1234, 1235, true},
		{"root is literal", "mariadb:11.8", "0", 0, 0, true},
		{"invalid on MariaDB is the legacy owner", "mariadb:11.8", "mysql", 999, 999, false},
		{"invalid on Percona is its 1001, the error being logged", "percona/percona-server:8.4", "x:y", 1001, 1001, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uid, gid, managed := newIdentityCluster(tc.image, "", tc.value).dbVolumeOwner()
			if uid != tc.uid || gid != tc.gid || managed != tc.managed {
				t.Fatalf("dbVolumeOwner = %d:%d managed=%v, want %d:%d managed=%v", uid, gid, managed, tc.uid, tc.gid, tc.managed)
			}
		})
	}
}

// The process UID and the volume owner are independent settings, so a mismatch is only reported.
func TestDBRunAsVolumeMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, orchestrator, image, runAs, volume string
		warn                                     bool
	}{
		{"nothing set", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "", "", false},
		{"root with the legacy owner", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "0", "", false},
		{"non-root with no owner on OpenSVC", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "1234", "", true},
		{"same uid", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "1234", "1234", false},
		{"same uid, other gid", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "1234", "1234:999", false},
		{"legacy owner equals the run-as uid", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "999", "", false},
		{"different uids", config.ConstOrchestratorOpenSVC, "mariadb:11.8", "2000", "999:1001", true},
		{"Percona default owner 1001", config.ConstOrchestratorOpenSVC, "percona/percona-server:8.4", "1001", "", false},
		{"Percona with another run-as uid", config.ConstOrchestratorOpenSVC, "percona/percona-server:8.4", "1234", "", true},
		{"Kubernetes, no owner managed", config.ConstOrchestratorKubernetes, "mariadb:11.8", "1234", "", true},
		{"Kubernetes, even 999 needs an owner", config.ConstOrchestratorKubernetes, "mariadb:11.8", "999", "", true},
		{"Kubernetes, same uid", config.ConstOrchestratorKubernetes, "mariadb:11.8", "1234", "1234", false},
		{"Kubernetes, root", config.ConstOrchestratorKubernetes, "mariadb:11.8", "0", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cluster := newIdentityCluster(tc.image, tc.runAs, tc.volume)
			cluster.Conf.ProvOrchestrator = tc.orchestrator
			if msg := cluster.dbRunAsVolumeMismatch(); (msg != "") != tc.warn {
				t.Fatalf("dbRunAsVolumeMismatch = %q, want a warning: %v", msg, tc.warn)
			}
		})
	}
}
