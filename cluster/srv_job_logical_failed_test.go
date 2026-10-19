package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// GH-2044: the server's last logical backup decides ERR00118 (config domain): a failed
// one opens it, a later one ended OK closes it, other jobs (a reseed) do not count.
func TestLastFailedLogicalBackup(t *testing.T) {
	s := &ServerMonitor{URL: "db2:3306", JobResults: config.NewTasksMap()}
	if s.LastFailedLogicalBackup() != nil {
		t.Fatal("no backup ran: nothing")
	}
	s.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 1, State: 5, End: 100, Result: "mysqldump: exit status 2 | stderr: Column count of mysql.proc is wrong"})
	if got := s.LastFailedLogicalBackup(); got == nil || got.Task != "mysqldump" {
		t.Fatalf("the last logical backup failed: %+v", got)
	}
	s.JobResults.Set("direct", &config.Task{Task: "direct", Done: 1, State: 4, End: 200, Result: "Reseed done"})
	if s.LastFailedLogicalBackup() == nil {
		t.Fatal("a reseed is not a logical backup of the server: still failed")
	}
	s.JobResults.Set("mydumper", &config.Task{Task: "mydumper", Done: 1, State: 3, End: 300, Result: "Backup completed"})
	if got := s.LastFailedLogicalBackup(); got != nil {
		t.Fatalf("a later logical backup ended OK: %+v", got)
	}
	s.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 0, State: 2, End: 0})
	if got := s.LastFailedLogicalBackup(); got != nil {
		t.Fatalf("a running backup does not reopen it: %+v", got)
	}
}

// #2113: a logical backup replication-manager took itself is in the catalogue, not in the
// jobs table: completed after the failed job, it closes ERR00118; older, it does not; another
// server's or a physical one never does.
func TestLastFailedLogicalBackupCatalogue(t *testing.T) {
	cl := newEventLogTestCluster(t, 1)
	s := &ServerMonitor{URL: "db1:3306", JobResults: config.NewTasksMap(), ClusterGroup: cl}
	s.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", Done: 1, State: 5, End: 1000, Result: "mysqldump: exit status 2"})
	s.JobResults.Set("mydumper", &config.Task{Task: "mydumper", Done: 0, State: 0}) // never run
	cl.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{Id: 1, Source: "db1:3306", BackupMethod: backupmgr.BackupMethodLogical, Completed: true, EndTime: time.Unix(900, 0)})
	cl.BackupMetaMap.Set(2, &backupmgr.BackupMetadata{Id: 2, Source: "db2:3306", BackupMethod: backupmgr.BackupMethodLogical, Completed: true, EndTime: time.Unix(2000, 0)})
	cl.BackupMetaMap.Set(3, &backupmgr.BackupMetadata{Id: 3, Source: "db1:3306", BackupMethod: backupmgr.BackupMethodPhysical, Completed: true, EndTime: time.Unix(2000, 0)})
	cl.BackupMetaMap.Set(4, &backupmgr.BackupMetadata{Id: 4, Source: "db1:3306", BackupMethod: backupmgr.BackupMethodLogical, Completed: false, EndTime: time.Unix(2000, 0)})
	if s.LastFailedLogicalBackup() == nil {
		t.Fatal("older, another server's, physical or not completed: the failure stands")
	}
	cl.BackupMetaMap.Set(5, &backupmgr.BackupMetadata{Id: 5, Source: "db1:3306", BackupMethod: backupmgr.BackupMethodLogical, Completed: true, EndTime: time.Unix(1500, 0)})
	if got := s.LastFailedLogicalBackup(); got != nil {
		t.Fatalf("a completed logical backup after the failure closes it: %+v", got)
	}
}
