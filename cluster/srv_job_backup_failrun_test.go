// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// The job of a physical backup reports an error (API mode) while the receiver of the backup waits
// for the job's terminal state, with backup-keep-until-valid on and a previous valid backup kept
// as .old. The receiver finalizes the metadata then and rolls back to the previous backup
// (LastBackupMeta.Physical switches to it); the error handler must never withdraw or rewrite that
// previous backup. Real receiver over TCP, no mock.
type failRunEnv struct {
	cl        *Cluster
	server    *ServerMonitor
	port      string
	logical   string // the failed run's artifact name without its .enc suffix
	prevDest  string // where the previous valid backup is back after the rollback
	prevBytes string
	failedID  int64
	task      string
}

const failRunPreviousID = 1

func newFailRunEnv(t *testing.T, encrypted bool) *failRunEnv {
	t.Helper()
	cl, server := newPhysicalBackupFixture(t, "api")
	cl.Conf.BackupKeepUntilValid = true
	cl.Conf.BackupEncryption = encrypted
	if encrypted {
		cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{"db-servers-credential": {Value: "root:s3cr3t"}})
	}
	task := config.ConstBackupPhysicalTypeMariaBackup
	e := &failRunEnv{cl: cl, server: server, task: task, prevBytes: "previous good backup"}
	e.logical = server.GetMyBackupDirectory() + task + ".xbtream.gz"
	e.prevDest = e.logical
	if encrypted {
		e.prevDest = e.logical + ".enc"
	}
	if err := os.MkdirAll(server.GetMyBackupDirectory(), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.prevDest, []byte(e.prevBytes), 0600); err != nil {
		t.Fatal(err)
	}
	prev := &backupmgr.BackupMetadata{Id: failRunPreviousID, BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: task,
		Dest: e.prevDest, Encrypted: encrypted, Completed: true, ResticEnabled: true, Source: server.URL}
	cl.BackupMetaMap.Set(int64(failRunPreviousID), prev)
	server.LastBackupMeta.Physical = prev

	before := sstConnectionPorts(t)
	t.Cleanup(func() { closeAllNewReceivers(t, server, before) })
	if err := server.JobBackupPhysicalWithOptions(BackupRunOptions{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	var err error
	if e.port, err = server.PhysicalBackupReceiverPort(task); err != nil {
		t.Fatal(err)
	}
	server.backupMetaMutex.Lock()
	e.failedID = server.LastBackupMeta.Physical.Id
	server.LastBackupMeta.Physical.ResticEnabled = true // restic must not start for the failed run
	server.backupMetaMutex.Unlock()
	if e.failedID == failRunPreviousID {
		t.Fatal("the dispatch did not start a new backup")
	}
	return e
}

// streamAndWaitPublished streams a backup and waits until the receiver has published it (encrypted)
// or finished writing it, and waits for the job's terminal state.
func (e *failRunEnv) streamAndWaitPublished(t *testing.T, encrypted bool) {
	t.Helper()
	sendToReceiver(t, e.port, physicalReceiverPayload())
	if encrypted {
		concWait(t, "the encrypted artifact is published", func() bool {
			e.server.backupMetaMutex.Lock()
			defer e.server.backupMetaMutex.Unlock()
			return e.server.LastBackupMeta.Physical.Completed && fileExists(e.logical+".enc")
		})
	} else {
		concWait(t, "the stream is written", func() bool {
			e.server.backupMetaMutex.Lock()
			defer e.server.backupMetaMutex.Unlock()
			return e.server.LastBackupMeta.Physical.Completed
		})
	}
}

// assertPreviousIntact checks the previous valid backup (artifact, metadata, map registration) and
// that nothing of the failed run is published, marked successful or sent to restic.
func (e *failRunEnv) assertPreviousIntact(t *testing.T) {
	t.Helper()
	got, err := os.ReadFile(e.prevDest)
	if err != nil || string(got) != e.prevBytes {
		t.Errorf("the previous valid backup is not intact under its name: %q, %v", got, err)
	}
	if fileExists(e.prevDest + ".old") {
		t.Error("the previous valid backup is still renamed .old after the rollback")
	}
	e.server.backupMetaMutex.Lock()
	last := e.server.LastBackupMeta.Physical
	e.server.backupMetaMutex.Unlock()
	if last == nil || last.Id != failRunPreviousID || !last.Completed || last.SourceJobFailed || last.Dest != e.prevDest {
		t.Errorf("the last physical backup is not the previous valid one, untouched: %+v", last)
	}
	if v, ok := e.cl.BackupMetaMap.Load(int64(failRunPreviousID)); !ok || v.(*backupmgr.BackupMetadata) != last {
		t.Error("the previous valid backup is no longer registered under its id")
	}
	if _, ok := e.cl.BackupMetaMap.Load(e.failedID); ok {
		t.Error("the failed run is still registered")
	}
	for _, p := range []string{e.logical + ".enc", e.logical + partialSuffixForCleanup, e.logical + ".err", e.logical + ".enc.err"} {
		if p != e.prevDest && fileExists(p) {
			t.Errorf("the failed run left %s", p)
		}
	}
	if e.server.HasBackupMariabackupCookie() {
		t.Error("the failed run set the success cookie")
	}
	if e.cl.InResticPhysicalBackup {
		t.Error("restic was scheduled for the failed run")
	}
}

// S5a: the error handler runs, and withdraws the artifact, before the terminal state is
// published: until then the receiver cannot finalize, so it cannot roll back under the handler.
func TestFailedPhysicalBackupWithdrawnBeforeTerminalStatePublished(t *testing.T) {
	for name, encrypted := range map[string]bool{"encrypted": true, "plain": false} {
		t.Run(name, func(t *testing.T) {
			e := newFailRunEnv(t, encrypted)
			reached, proceed, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			jobStateFailBeforePublish = func() { close(reached); <-proceed }
			t.Cleanup(func() { jobStateFailBeforePublish = nil })
			e.streamAndWaitPublished(t, encrypted)

			go func() { e.server.JobsUpdateState(e.task, "error", JobStateErrorExec, 1); close(done) }()
			<-reached
			// the handler is between "failed run withdrawn" and "terminal state published"
			if st := e.server.JobResults.Get(e.task).State; st >= JobStateFinished {
				t.Errorf("the terminal state %d is already published", st)
			}
			if encrypted && fileExists(e.logical+".enc") {
				t.Error("the failed run's artifact is still published when the state becomes visible")
			}
			e.server.backupMetaMutex.Lock()
			rolledBack, flagged := e.server.LastBackupMeta.Physical.Id == failRunPreviousID, e.server.LastBackupMeta.Physical.SourceJobFailed
			e.server.backupMetaMutex.Unlock()
			if rolledBack || !flagged {
				t.Errorf("the failed run must still be the last backup and flagged: rolledBack=%v flagged=%v", rolledBack, flagged)
			}
			if !fileExists(e.prevDest + ".old") {
				t.Error("the receiver rolled back before the terminal state was published")
			}
			close(proceed)
			<-done
			waitReceiverGone(t, e.port)
			waitBackupStateReleased(t, e.cl, e.server)
			e.assertPreviousIntact(t)
		})
	}
}

// S5b: the receiver finalized and rolled back to the previous backup; an error reported again
// afterwards (LastBackupMeta.Physical is the previous backup now) leaves it untouched.
func TestRepeatedJobErrorAfterRollbackLeavesPreviousBackupIntact(t *testing.T) {
	for name, encrypted := range map[string]bool{"encrypted": true, "plain": false} {
		t.Run(name, func(t *testing.T) {
			e := newFailRunEnv(t, encrypted)
			e.streamAndWaitPublished(t, encrypted)
			e.server.JobsUpdateState(e.task, "error", JobStateErrorExec, 1)
			waitReceiverGone(t, e.port)
			waitBackupStateReleased(t, e.cl, e.server)
			e.assertPreviousIntact(t)

			e.server.JobsUpdateState(e.task, "error", JobStateErrorExec, 1)
			e.assertPreviousIntact(t)
		})
	}
}

// An error after a success the receiver already finalized (nobody else finalizes it) still
// withdraws the run's artifact and rewrites its metadata, but never the metadata of another backup.
func TestLateFailedPhysicalBackupNeverRewritesAnotherBackup(t *testing.T) {
	e := newFailRunEnv(t, true)
	e.streamAndWaitPublished(t, true)
	e.server.JobsUpdateState(e.task, "completed", JobStateSuccess, 1)
	waitReceiverGone(t, e.port)
	waitBackupStateReleased(t, e.cl, e.server)

	e.server.backupMetaMutex.Lock()
	run := e.server.LastBackupMeta.Physical
	e.server.backupMetaMutex.Unlock()
	other := &backupmgr.BackupMetadata{Id: 99, BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: e.task, Completed: true, Source: e.server.URL}
	e.server.backupMetaMutex.Lock()
	e.server.LastBackupMeta.Physical = other // a newer run took the run's place
	e.server.backupMetaMutex.Unlock()

	e.server.lateFailedPhysicalBackup(run, "late error")
	if fileExists(e.logical + ".enc") {
		t.Error("the artifact of the run whose job failed is still published")
	}
	if !other.Completed || other.SourceJobFailed || other.MetaFile != "" {
		t.Errorf("the metadata of another backup was rewritten: %+v", other)
	}
}

// concWait polls cond (the receiver runs in its own goroutine) until it holds.
func concWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for: %s", what)
}

// After a restart of replication-manager JobResults holds no run, while the last valid encrypted
// physical backup is loaded from disk. A stray error report of the backup task (a job of an
// earlier run reporting late) is not the failure of a running backup: the artifact, the metadata
// file and the registration of that backup stay as they are.
func TestStrayJobErrorWithNoTrackedRunLeavesLastBackupIntact(t *testing.T) {
	cl, server := newPhysicalBackupFixture(t, "api")
	cl.Conf.BackupEncryption = true
	cl.Conf.BackupKeepUntilValid = true
	task := config.ConstBackupPhysicalTypeMariaBackup
	dest := server.GetMyBackupDirectory() + task + ".xbtream.gz.enc"
	if err := os.MkdirAll(server.GetMyBackupDirectory(), 0755); err != nil {
		t.Fatal(err)
	}
	artifact := []byte("last valid encrypted backup")
	if err := os.WriteFile(dest, artifact, 0600); err != nil {
		t.Fatal(err)
	}
	last := &backupmgr.BackupMetadata{Id: failRunPreviousID, BackupMethod: backupmgr.BackupMethodPhysical, BackupTool: task,
		Dest: dest, Encrypted: true, Completed: true, Source: server.URL}
	metaFile := server.backupMetaFilePath(last)
	metaBytes := []byte("{\"completed\": true}\n")
	if err := os.WriteFile(metaFile, metaBytes, 0644); err != nil {
		t.Fatal(err)
	}
	cl.BackupMetaMap.Set(int64(failRunPreviousID), last)
	server.LastBackupMeta.Physical = last
	if server.JobResults.Get(task) != nil {
		t.Fatal("the restarted server must track no run of the task")
	}

	for i := 0; i < 2; i++ { // the first report creates a task entry already failed: a repeat does nothing either
		server.JobsUpdateState(task, "error", JobStateErrorExec, 1)

		if got, err := os.ReadFile(dest); err != nil || string(got) != string(artifact) {
			t.Fatalf("report %d: the artifact of the last valid backup changed: %q, %v", i+1, got, err)
		}
		if got, err := os.ReadFile(metaFile); err != nil || string(got) != string(metaBytes) {
			t.Fatalf("report %d: the metadata file of the last valid backup changed: %q, %v", i+1, got, err)
		}
		server.backupMetaMutex.Lock()
		same := server.LastBackupMeta.Physical == last
		altered := last.SourceJobFailed || !last.Completed || !last.Encrypted || last.Dest != dest || last.EncryptionFailed
		server.backupMetaMutex.Unlock()
		if !same || altered {
			t.Fatalf("report %d: the last valid backup was altered: %+v", i+1, last)
		}
		if v, ok := cl.BackupMetaMap.Load(int64(failRunPreviousID)); !ok || v.(*backupmgr.BackupMetadata) != last {
			t.Fatalf("report %d: the last valid backup is no longer registered", i+1)
		}
	}
}
