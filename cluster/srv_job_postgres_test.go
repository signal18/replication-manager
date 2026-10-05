package cluster

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

func postgresJobTestServer(t *testing.T) *ServerMonitor {
	t.Helper()
	c := &Cluster{Name: "pgtest", Conf: &config.Config{}}
	s := &ServerMonitor{URL: "pg1:5432", ClusterGroup: c, Datadir: t.TempDir(), JobResults: config.NewTasksMap()}
	s.DBVersion, _ = version.NewVersion("PostgreSQL", 17, 11, 0)
	return s
}

// A PostgreSQL server dumps with pg_dumpall in its sidecar whatever the cluster's tool,
// the task is a remote one, and asking for it consumes the request once.
func TestPostgresLogicalBackupIsASidecarTask(t *testing.T) {
	s := postgresJobTestServer(t)
	s.ClusterGroup.Conf.BackupLogicalType = config.ConstBackupLogicalTypeMysqldump
	if s.logicalBackupType() != "pgdump" {
		t.Fatalf("tool: %s", s.logicalBackupType())
	}
	if !config.IsRemoteTask(config.ConstTaskPgDump, nil) {
		t.Fatal("pgdump runs in the sidecar")
	}
	if ok, _ := s.CheckTaskNeeded("pgdump"); ok {
		t.Fatal("nothing requested yet")
	}
	if err := s.setTaskCookie("pgdump"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.CheckTaskNeeded("pgdump"); !ok {
		t.Fatal("the requested task is needed")
	}
	if ok, _ := s.CheckTaskNeeded("pgdump"); ok {
		t.Fatal("a request is given once")
	}
}

// The common path waits for the end of the sidecar's stream: it goes on when the receiver
// signals it and data was received, fails on an empty stream and on a cancellation.
func TestRunPostgresStreamTask(t *testing.T) {
	s := postgresJobTestServer(t)
	dest := filepath.Join(t.TempDir(), "pg_dumpall.sql.gz")

	finish := func(content string) {
		for i := 0; i < 200 && !s.hasCookie(postgresJobCookie("pgdump")); i++ {
			time.Sleep(5 * time.Millisecond)
		}
		s.CheckTaskNeeded("pgdump") // the sidecar takes the task
		os.WriteFile(dest, []byte(content), 0600)
		s.signalStreamTaskDone("pgdump")
	}

	go finish(strings.Repeat("x", 500))
	if err := s.runPostgresStreamTask(context.Background(), "pgdump", dest); err != nil {
		t.Fatalf("a received dump ends the wait: %v", err)
	}

	go finish("") // the sender connected and sent nothing
	if err := s.runPostgresStreamTask(context.Background(), "pgdump", dest); err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("an empty stream is an error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(50 * time.Millisecond); cancel() }()
	if err := s.runPostgresStreamTask(ctx, "pgdump", dest); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("a cancellation ends the wait: %v", err)
	}
	if s.hasCookie(postgresJobCookie("pgdump")) {
		t.Fatal("a cancelled request is withdrawn")
	}
	s.signalStreamTaskDone("pgdump") // no waiter: tolerated
}

// A PostgreSQL server takes its physical backup with pg_basebackup in the sidecar: a tar,
// no receiver opened ahead (the sidecar asks for it), written where the running backup says.
func TestPostgresPhysicalBackupIsASidecarTask(t *testing.T) {
	s := postgresJobTestServer(t)
	s.ClusterGroup.Conf.BackupPhysicalType = config.ConstBackupPhysicalTypeMariaBackup
	s.ClusterGroup.Conf.WorkingDir = t.TempDir()
	if s.physicalBackupType() != "pgbasebackup" || s.physicalBackupExtension("pgbasebackup") != ".tar" || s.physicalBackupExtension("mariabackup") != ".xbtream" {
		t.Fatalf("tool %s", s.physicalBackupType())
	}
	if port, err := s.openPhysicalBackupReceiver(true, "/nowhere", "pgbasebackup"); err != nil || port != "0" {
		t.Fatalf("no receiver is opened ahead for the sidecar: %q %v", port, err)
	}
	if !config.IsRemoteTask(config.ConstTaskPgBaseBackup, nil) {
		t.Fatal("pgbasebackup runs in the sidecar")
	}
	if !strings.HasSuffix(s.PostgresStreamDest("pgbasebackup"), "pgbasebackup.tar.gz") {
		t.Fatalf("default destination: %s", s.PostgresStreamDest("pgbasebackup"))
	}
	s.LastBackupMeta.Physical = &backupmgr.BackupMetadata{BackupTool: "pgbasebackup", Dest: "/x/pgbasebackup.1.tar.gz.partial"}
	if s.PostgresStreamDest("pgbasebackup") != "/x/pgbasebackup.1.tar.gz.partial" {
		t.Fatal("the receiver writes where the running backup recorded")
	}
	s.LastBackupMeta.Physical.Completed = true
	if !strings.HasSuffix(s.PostgresStreamDest("pgbasebackup"), "pgbasebackup.tar.gz") {
		t.Fatal("a completed backup no longer steers the receiver")
	}
}

// The scheduled optimize of a PostgreSQL cluster asks the PRIMARY's sidecar (a single
// instance has no replica, and a standby cannot be vacuumed); the task is given once.
func TestRollingOptimizePostgreSQLRunsOnThePrimary(t *testing.T) {
	s := postgresJobTestServer(t)
	c := s.ClusterGroup
	c.master = s
	c.Servers = []*ServerMonitor{s}
	c.StateMachine = new(state.StateMachine)
	c.StateMachine.Init()
	s.State = stateMaster
	c.RollingOptimize()
	if ok, _ := s.CheckTaskNeeded("optimize"); !ok {
		t.Fatal("the primary's sidecar is asked to optimize")
	}
	if ok, _ := s.CheckTaskNeeded("optimize"); ok {
		t.Fatal("a request is given once")
	}
}
