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

// A logical backup, local or on the DB node, and the direct rejoin stream write the active
// position (--master-data=1 on a primary, --dump-slave=1 on a replica) with
// --apply-slave-statements: the dump positions the server it is restored on.
func TestGetMysqlDumpOptionsPosition(t *testing.T) {
	for _, c := range []struct {
		name          string
		replica       bool
		active, other string
	}{
		{"master", false, "--master-data=1", "--master-data=2"},
		{"replica", true, "--dump-slave=1", "--dump-slave=2"},
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
		args := cl.GetMysqlDumpOptions(server, "--gtid=true", cl.VersionsMap.Get("client-dump"))
		if !slices.Contains(args, c.active) || slices.Contains(args, c.other) || !slices.Contains(args, "--apply-slave-statements") {
			t.Fatalf("%s: want %s and --apply-slave-statements, no %s: %v", c.name, c.active, c.other, args)
		}
	}
}

// --skip-log-queries only when the dump client and the server name the slow log the same way
// (slow_query_log before MariaDB 10.11, log_slow_query from 10.11): a 10.11+ client's dump
// fails on a 10.6 server (ERROR 1193 Unknown system variable 'LOG_SLOW_QUERY').
func TestMariadbSkipLogQueriesCompatible(t *testing.T) {
	v := func(flavor string, major, minor, release int) *version.Version {
		return &version.Version{Flavor: flavor, Major: major, Minor: minor, Release: release}
	}
	for _, c := range []struct {
		name           string
		client, server *version.Version
		want           bool
	}{
		{"client 11.4, server 10.6", v("MariaDB", 11, 4, 13), v("MariaDB", 10, 6, 28), false},
		{"client 11.4, server 10.11", v("MariaDB", 11, 4, 13), v("MariaDB", 10, 11, 19), true},
		{"client 10.6, server 10.6 (remote dump)", v("MariaDB", 10, 6, 28), v("MariaDB", 10, 6, 28), true},
		{"client 10.6, server 11.4", v("MariaDB", 10, 6, 28), v("MariaDB", 11, 4, 13), false},
		{"unknown client", nil, v("MariaDB", 10, 11, 19), false},
		{"MySQL", v("MySQL", 8, 0, 45), v("MySQL", 8, 0, 35), false},
	} {
		if got := mariadbSkipLogQueriesCompatible(c.client, c.server); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// The dump client cases seen failing in the lab are flagged, the working ones are not.
func TestDumpClientIncompatibility(t *testing.T) {
	v := func(flavor string, major, minor, release int) *version.Version {
		return &version.Version{Flavor: flavor, Major: major, Minor: minor, Release: release}
	}
	for _, c := range []struct {
		name           string
		client, server *version.Version
		flagged        bool
	}{
		{"MariaDB client, MySQL 8.0", v("MariaDB", 11, 4, 13), v("MySQL", 8, 0, 35), true},
		{"MariaDB client, Percona 5.7", v("MariaDB", 11, 4, 13), v("Percona", 5, 7, 44), true},
		{"8.0 client, MySQL 8.4", v("MySQL", 8, 0, 45), v("MySQL", 8, 4, 11), true},
		{"8.4 client, MySQL 8.0", v("MySQL", 8, 4, 10), v("MySQL", 8, 0, 35), true},
		{"8.4 client, Percona 8.0", v("MySQL", 8, 4, 10), v("Percona", 8, 0, 46), true},
		{"8.0 client, MySQL 8.0", v("MySQL", 8, 0, 45), v("MySQL", 8, 0, 35), false},
		{"8.0 client, Percona 5.7", v("MySQL", 8, 0, 45), v("Percona", 5, 7, 44), false},
		{"8.4 client, MySQL 5.7", v("MySQL", 8, 4, 10), v("MySQL", 5, 7, 42), false},
		{"8.4 client, Percona 8.4", v("MySQL", 8, 4, 10), v("Percona", 8, 4, 11), false},
		{"MariaDB client, MariaDB", v("MariaDB", 11, 4, 13), v("MariaDB", 10, 6, 28), false},
		{"unknown client", nil, v("MySQL", 8, 4, 11), false},
	} {
		if got := dumpClientIncompatibility(c.client, c.server); (got != "") != c.flagged {
			t.Errorf("%s: %q, flagged want %v", c.name, got, c.flagged)
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
		args := cl.GetMysqlDumpOptions(server, "", cl.VersionsMap.Get("client-dump"))
		if got := slices.Contains(args, "--ignore-table=mysql.slave_worker_info"); got != want {
			t.Fatalf("%s: --ignore-table=mysql.slave_worker_info %v, want %v: %v", flavor, got, want, args)
		}
	}
}
