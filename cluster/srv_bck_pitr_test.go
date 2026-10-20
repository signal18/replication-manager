package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// The PITR flag lives as long as the PITR, not as long as the request (#2117): set while the
// restore runs, cleared once the reseeding state is cleared and the run's result is read.
func TestFinishPointInTimeKeepsFlagUntilRestoreEnds(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "testcluster", "node1", "3306")
	meta := backupmgr.PointInTimeMeta{IsInPITR: true, Backup: 42}
	backup := &backupmgr.BackupMetadata{Id: 42, BackupTool: config.ConstBackupLogicalTypeMysqldump}
	task := "reseed" + backup.BackupTool

	server.SetPointInTimeMeta(meta)
	if ok, _ := server.TrySetInReseedBackup(task); !ok {
		t.Fatal("arm the reseed")
	}
	done := make(chan struct{})
	go func() {
		server.finishPointInTime(meta, backup)
		close(done)
	}()

	time.Sleep(1500 * time.Millisecond)
	if !server.PointInTimeMeta.IsInPITR {
		t.Fatal("the PITR flag is cleared while the restore runs")
	}

	server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", JobStateFinished, 1)
	server.SetInReseedBackup("")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("finishPointInTime did not end once the restore ended")
	}
	if server.PointInTimeMeta.IsInPITR {
		t.Fatal("the PITR flag stays set after the PITR")
	}
}

// A PITR whose restore fails after the request has returned records why (#2117), for
// ERR00119: the restore and the job's result, then clears the PITR flag.
func TestFinishPointInTimeFailedRestoreRecordsCause(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "testcluster", "node1", "3306")
	meta := backupmgr.PointInTimeMeta{IsInPITR: true, Backup: 42}
	backup := &backupmgr.BackupMetadata{Id: 42, BackupTool: config.ConstBackupPhysicalTypeMariaBackup}
	task := "reseed" + backup.BackupTool

	server.SetPointInTimeMeta(meta)
	if ok, _ := server.TrySetInReseedBackup(task); !ok {
		t.Fatal("arm the reseed")
	}
	done := make(chan struct{})
	go func() {
		server.finishPointInTime(meta, backup)
		close(done)
	}()

	server.JobsUpdateStateRuntimeOnly(task, "Waiting backup.\nBackup transfer stopped: it would leave less than 10% free on the datadir volume\n", JobStateErrorExec, 1)
	server.SetInReseedBackup("")
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("finishPointInTime did not end once the restore failed")
	}
	if server.PointInTimeMeta.IsInPITR {
		t.Fatal("the PITR flag stays set after a failed PITR")
	}
	links := server.reseedLinks
	if !links.removed || links.reason != "point-in-time restore from backup 42" {
		t.Fatalf("failed PITR record: %+v", links)
	}
	want := "the restore with mariabackup did not complete: Waiting backup.\nBackup transfer stopped: it would leave less than 10% free on the datadir volume"
	if links.failed != want {
		t.Fatalf("cause %q, want %q", links.failed, want)
	}
}

func TestPointInTimeJobResult(t *testing.T) {
	if got := pointInTimeJobResult("  cancelled by user\n"); got != "cancelled by user" {
		t.Fatalf("trimmed: %q", got)
	}
	long := "H" + strings.Repeat("x", 500) + "T"
	got := pointInTimeJobResult(long)
	if len(got) != 405 || got[0] != 'H' || got[len(got)-1] != 'T' || !strings.Contains(got, " ... ") {
		t.Fatalf("long result keeps its head and tail: %d %q...", len(got), got[:10])
	}
}

// In sql mode the in-memory result of a job just queued still holds the previous run's (here
// a success) until the jobs table is read again: a PITR cancelled right after its request
// reads the table first and records the cancel, not the previous success (#2117).
func TestFinishPointInTimeReadsJobsTableBeforeResult(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "testcluster", "node1", "3306")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	server.Conn = sqlx.NewDb(db, "sqlmock")
	meta := backupmgr.PointInTimeMeta{IsInPITR: true, Backup: 42}
	backup := &backupmgr.BackupMetadata{Id: 42, BackupTool: config.ConstBackupPhysicalTypeMariaBackup}
	task := "reseed" + backup.BackupTool
	previous := time.Now().Add(-time.Hour).Unix()
	server.JobResults.Store(task, &config.Task{Task: task, State: JobStateSuccess, Done: 1, Result: "Partial restore completed successfully", Start: previous, End: previous})

	mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
	now := time.Now().Unix()
	mock.ExpectQuery("SELECT id, task, port, server, done, state, result, payload").WillReturnRows(
		sqlmock.NewRows([]string{"id", "task", "port", "server", "done", "state", "result", "payload", "start", "end"}).
			AddRow(2, task, 4444, "monitor", 1, JobStateErrorExec, "cancelled by user", nil, now, nil))

	server.SetPointInTimeMeta(meta)
	server.finishPointInTime(meta, backup)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("the jobs table was not read before the result: %v", err)
	}
	want := "the restore with mariabackup did not complete: cancelled by user"
	if server.reseedLinks.failed != want {
		t.Fatalf("cause %q, want %q (the previous run's success was trusted)", server.reseedLinks.failed, want)
	}
}
