package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func expectedExecutablePath(t *testing.T) string {
	t.Helper()

	path, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to get executable path: %v", err)
	}

	finfo, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("failed to lstat executable path: %v", err)
	}

	if finfo.Mode()&os.ModeSymlink != 0 {
		path, err = filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatalf("failed to eval symlink: %v", err)
		}
	}

	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	return path
}

func TestGetRepManAbsolutePath(t *testing.T) {
	cluster := &Cluster{}

	got, err := cluster.GetRepManAbsolutePath()
	if err != nil {
		t.Fatalf("GetRepManAbsolutePath error: %v", err)
	}

	if !filepath.IsAbs(got) {
		t.Fatalf("expected absolute path, got %q", got)
	}

	expected := expectedExecutablePath(t)
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

func TestGetReplicationManagerCliPathUsesConfiguredValue(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ReplicationManagerCliPath: "/custom/replication-manager-cli"}}

	got := cluster.GetReplicationManagerCliPath()
	if got != cluster.Conf.ReplicationManagerCliPath {
		t.Fatalf("expected configured path %q, got %q", cluster.Conf.ReplicationManagerCliPath, got)
	}
}

func TestGetReplicationManagerCliPathUsesExecutableDir(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{ReplicationManagerCliPath: "  "}}

	got := cluster.GetReplicationManagerCliPath()
	expected := "replication-manager-cli"
	exeDir := filepath.Dir(expectedExecutablePath(t))
	localPath := filepath.Join(exeDir, "replication-manager-cli")
	if _, err := os.Stat(localPath); err == nil {
		expected = localPath
	} else if path, err := exec.LookPath("replication-manager-cli"); err == nil {
		expected = path
	}
	if got != expected {
		t.Fatalf("expected %q, got %q", expected, got)
	}
}

// A backup writes the replication position as a comment only, without STOP/START SLAVE: a
// statement without a channel can not be replayed on a multi-source replica. The direct
// reseed stream keeps the active position it is restored with.
func TestGetMysqlBackupDumpOptionsPositionComment(t *testing.T) {
	for _, c := range []struct {
		name          string
		replica       bool
		backup, other string
	}{
		{"master", false, "--master-data=2", "--master-data=1"},
		{"replica", true, "--dump-slave=2", "--dump-slave=1"},
	} {
		cl, server := newTestClusterServer(t)
		server.Id = "db1"
		server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11, Release: 8}
		server.Variables = config.NewStringsMap()
		cl.VersionsMap = config.NewVersionsMap()
		cl.VersionsMap.Set("client-dump", server.DBVersion)
		cl.master = server
		if c.replica {
			cl.master = &ServerMonitor{Id: "db2", ClusterGroup: cl}
		}
		backup := cl.GetMysqlBackupDumpOptions(server, "--gtid=true")
		if !slices.Contains(backup, c.backup) || slices.Contains(backup, c.other) || slices.Contains(backup, "--apply-slave-statements") {
			t.Fatalf("%s backup: want %s, no %s, no --apply-slave-statements: %v", c.name, c.backup, c.other, backup)
		}
		direct := cl.GetMysqlDumpOptions(server, "--gtid=true")
		if !slices.Contains(direct, c.other) || !slices.Contains(direct, "--apply-slave-statements") {
			t.Fatalf("%s direct: want %s and --apply-slave-statements: %v", c.name, c.other, direct)
		}
	}
}

// A MySQL/Percona dump leaves out the replica's parallel applier state; MariaDB has no such table.
func TestGetMysqlDumpOptionsIgnoreWorkerInfo(t *testing.T) {
	for flavor, want := range map[string]bool{"MySQL": true, "Percona": true, "MariaDB": false} {
		cl, server := newTestClusterServer(t)
		server.Id = "db1"
		server.DBVersion = &version.Version{Flavor: flavor, Major: 8, Minor: 0, Release: 35}
		server.Variables = config.NewStringsMap()
		cl.VersionsMap = config.NewVersionsMap()
		cl.VersionsMap.Set("client-dump", server.DBVersion)
		cl.master = server
		for _, args := range [][]string{cl.GetMysqlBackupDumpOptions(server, ""), cl.GetMysqlDumpOptions(server, "")} {
			if got := slices.Contains(args, "--ignore-table=mysql.slave_worker_info"); got != want {
				t.Fatalf("%s: --ignore-table=mysql.slave_worker_info %v, want %v: %v", flavor, got, want, args)
			}
		}
	}
}

func TestMariadbSkipLogQueriesCompatible(t *testing.T) {
	v := func(flavor string, major, minor, release int) *version.Version {
		return &version.Version{Flavor: flavor, Major: major, Minor: minor, Release: release}
	}
	for _, c := range []struct {
		client, server *version.Version
		want           bool
	}{
		{v("MariaDB", 11, 4, 13), v("MariaDB", 10, 6, 28), false},
		{v("MariaDB", 11, 4, 13), v("MariaDB", 10, 11, 19), true},
		{v("MariaDB", 10, 6, 28), v("MariaDB", 10, 6, 28), true},
		{v("MariaDB", 10, 6, 28), v("MariaDB", 11, 4, 13), false},
		{nil, v("MariaDB", 10, 11, 19), false},
	} {
		if got := mariadbSkipLogQueriesCompatible(c.client, c.server); got != c.want {
			t.Errorf("%v, %v: %v, want %v", c.client, c.server, got, c.want)
		}
	}
}

func TestDumpClientIncompatibility(t *testing.T) {
	v := func(flavor string, major, minor, release int) *version.Version {
		return &version.Version{Flavor: flavor, Major: major, Minor: minor, Release: release}
	}
	for _, c := range []struct {
		client, server *version.Version
		flagged        bool
	}{
		{v("MariaDB", 11, 4, 13), v("MySQL", 8, 0, 35), true},
		{v("MariaDB", 11, 4, 13), v("Percona", 5, 7, 44), true},
		{v("MySQL", 8, 0, 45), v("MySQL", 8, 4, 11), true},
		{v("MySQL", 8, 4, 10), v("MySQL", 8, 0, 35), true},
		{v("MySQL", 8, 4, 10), v("MySQL", 5, 7, 42), false},
		{v("MySQL", 8, 0, 45), v("Percona", 5, 7, 44), false},
		{nil, v("MySQL", 8, 4, 11), false},
	} {
		if got := dumpClientIncompatibility(c.client, c.server); (got != "") != c.flagged {
			t.Errorf("%v, %v: %q, flagged want %v", c.client, c.server, got, c.flagged)
		}
	}
}
