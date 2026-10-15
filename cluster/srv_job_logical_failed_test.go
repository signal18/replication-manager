package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
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
