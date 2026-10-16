package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func verificationRun(seq uint64, status string) backupmgr.BackupVerificationRun {
	return backupmgr.BackupVerificationRun{Sequence: seq, RunID: string(rune('a' + seq)), Level: "integrity", Status: status, StartTime: time.Now()}
}

// the history keeps its bound: the oldest finished runs go first, a running one is kept while
// a finished one can go, and the runs kept are in a new slice
func TestBoundVerificationRuns(t *testing.T) {
	runs := []backupmgr.BackupVerificationRun{
		verificationRun(1, backupmgr.BackupVerificationRunning),
		verificationRun(2, backupmgr.BackupVerificationPassed),
		verificationRun(3, backupmgr.BackupVerificationFailed),
		verificationRun(4, backupmgr.BackupVerificationSkipped),
	}
	if got := boundVerificationRuns(runs, 4); len(got) != 4 {
		t.Fatalf("within the bound: %d kept", len(got))
	}
	got := boundVerificationRuns(runs, 2)
	if len(got) != 2 || got[0].Sequence != 1 || got[1].Sequence != 4 {
		t.Fatalf("kept %+v, want the running run 1 and the newest finished 4", got)
	}
	if &got[0] == &runs[0] {
		t.Fatal("the runs kept must be a new slice")
	}
	all := []backupmgr.BackupVerificationRun{verificationRun(1, backupmgr.BackupVerificationRunning), verificationRun(2, backupmgr.BackupVerificationRunning)}
	if got := boundVerificationRuns(all, 1); len(got) != 1 || got[0].Sequence != 2 {
		t.Fatalf("no finished run to drop: the oldest goes, got %+v", got)
	}
}

// a run's outcome, passed or failed, ends its own record and no other; one dropped from the
// history, or an automatic verification (no run id), changes nothing
func TestEndVerificationRunExactRecord(t *testing.T) {
	_, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	a, err := server.startVerificationRun(meta, config.ConstBackupVerificationLevelIntegrity, "run-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := server.startVerificationRun(meta, config.ConstBackupVerificationLevelIntegrity, "run-b")
	if err != nil || a != 1 || b != 2 {
		t.Fatalf("sequences %d %d, err %v", a, b, err)
	}
	// the run id of run a with another sequence: not run a's record
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed, RunID: "run-a", RunSequence: b, EndTime: time.Now()})
	if r := meta.VerificationRuns[0]; r.Status != backupmgr.BackupVerificationRunning {
		t.Fatalf("a run id with another sequence ended run a: %+v", r)
	}
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationFailed, RunID: "run-a", RunSequence: a, EndTime: time.Now(), Error: "cut"})
	server.verifyBackupRun(meta, config.ConstBackupVerificationLevelIntegrity, "run-b", b)
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed})
	server.recordBackupVerification(meta, &backupmgr.BackupVerification{Level: "integrity", Status: backupmgr.BackupVerificationPassed, RunID: "dropped"})
	if len(meta.VerificationRuns) != 2 {
		t.Fatalf("runs %+v", meta.VerificationRuns)
	}
	if r := meta.VerificationRuns[0]; r.RunID != "run-a" || r.Status != backupmgr.BackupVerificationFailed || r.Error != "cut" || r.EndTime.IsZero() {
		t.Fatalf("run a %+v", r)
	}
	if r := meta.VerificationRuns[1]; r.RunID != "run-b" || r.Status != backupmgr.BackupVerificationPassed {
		t.Fatalf("run b %+v", r)
	}
}

// replication-manager stopped during a run, or before it ran: its record still running is
// ended failed, interrupted, when the backup is registered again; not while a run of this
// process holds the backup
func TestCloseInterruptedVerificationRuns(t *testing.T) {
	c, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	if _, err := server.startVerificationRun(meta, config.ConstBackupVerificationLevelIntegrity, "run-a"); err != nil {
		t.Fatal(err)
	}
	c.backupVerifying.Store(meta.Id, "integrity")
	server.closeInterruptedVerificationRuns(meta)
	if r := meta.VerificationRuns[0]; r.Status != backupmgr.BackupVerificationRunning {
		t.Fatalf("a run this process holds is not interrupted: %+v", r)
	}
	c.backupVerifying.Delete(meta.Id)
	server.closeInterruptedVerificationRuns(meta)
	r := meta.VerificationRuns[0]
	if r.Status != backupmgr.BackupVerificationFailed || !strings.Contains(r.Error, "interrupted") || r.EndTime.IsZero() {
		t.Fatalf("interrupted run %+v", r)
	}
	if onDisk, err := readBackupMetadataFile(server.backupMetaFilePath(meta)); err != nil || len(onDisk.VerificationRuns) != 1 || onDisk.VerificationRuns[0].Status != backupmgr.BackupVerificationFailed || onDisk.VerificationRunSequence != 1 {
		t.Fatalf("meta file %+v, err %v", onDisk.VerificationRuns, err)
	}
}

// the history is bounded by backup-verification-run-history when runs are accepted
func TestVerificationRunHistoryBound(t *testing.T) {
	c, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	c.Conf.BackupVerificationRunHistory = 3
	for i := 0; i < 5; i++ {
		id := string(rune('a' + i))
		seq, err := server.startVerificationRun(meta, "integrity", id)
		if err != nil {
			t.Fatal(err)
		}
		server.recordBackupVerificationSkipped(meta, "integrity", id, seq, "not run")
	}
	if len(meta.VerificationRuns) != 3 || meta.VerificationRuns[0].Sequence != 3 || meta.VerificationRunSequence != 5 {
		t.Fatalf("runs %+v, sequence %d", meta.VerificationRuns, meta.VerificationRunSequence)
	}
	c.Conf.BackupVerificationRunHistory = 0
	if c.verificationRunHistory() != config.ConstBackupVerificationRunHistory {
		t.Fatal("0: the default")
	}
}

// a run whose record can not be written is not accepted, and leaves the history and the
// sequence as they were: no run left running in memory
func TestStartVerificationRunWriteFailure(t *testing.T) {
	_, server, meta := newVerificationTest(t, false, func(dest string) { writeValidationGzip(t, dest, validationDump) })
	if _, err := server.startVerificationRun(meta, "integrity", "run-a"); err != nil {
		t.Fatal(err)
	}
	before := append([]backupmgr.BackupVerificationRun(nil), meta.VerificationRuns...)
	// a directory where the meta file is: the write fails
	path := server.backupMetaFilePath(meta)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "x"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := server.startVerificationRun(meta, "integrity", "run-b"); err == nil {
		t.Fatal("a run whose record can not be written must not be accepted")
	}
	if meta.VerificationRunSequence != 1 || len(meta.VerificationRuns) != len(before) || meta.VerificationRuns[0] != before[0] {
		t.Fatalf("history changed by a failed start: sequence %d runs %+v", meta.VerificationRunSequence, meta.VerificationRuns)
	}
}
