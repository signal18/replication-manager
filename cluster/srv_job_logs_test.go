package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hpcloud/tail"
	"github.com/signal18/replication-manager/config"
)

func newTestServerForDBLogs(t *testing.T, workingDir string) *ServerMonitor {
	t.Helper()
	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir: workingDir,
		},
		Name: "testcluster",
	}
	server := &ServerMonitor{
		ClusterGroup: cluster,
		Datadir:      filepath.Join(workingDir, "cluster-dir", "node1_3306"),
		Host:         "node1",
		Port:         "3306",
	}
	return server
}

func TestMigrateDBLogsToBackupStorage_MovesActiveAndRotatedHistory(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	// Active file plus rotated history from both naming schemes: the old
	// manual rename scheme (log_error_<ts>.log) and lumberjack's own scheme
	// (log_error-<ts>.log).
	files := map[string]string{
		"log_error.log":                         "active error log",
		"log_error_20200101_000000.log":         "legacy-rotated error log",
		"log_error-2020-01-01T00-00-00.000.log": "lumberjack-rotated error log",
		"log_slow_query.log":                    "active slow log",
		"log_audit.log":                         "active audit log",
		"log_sql_error.log":                     "active sql error log",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(legacyDir, name), []byte(content), 0600); err != nil {
			t.Fatalf("failed to seed legacy file %s: %v", name, err)
		}
	}

	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected migration to report success")
	}

	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	for name, content := range files {
		if _, err := os.Stat(filepath.Join(legacyDir, name)); !os.IsNotExist(err) {
			t.Errorf("expected legacy file %s to be moved away, stat err = %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(newDir, name))
		if err != nil {
			t.Fatalf("expected migrated file %s at destination: %v", name, err)
		}
		if string(got) != content {
			t.Errorf("migrated file %s content = %q, want %q", name, got, content)
		}
	}
}

func TestMigrateDBLogsToBackupStorage_PreservesExistingDestination(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("failed to create backup-backed dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(legacyDir, "log_error.log"), []byte("legacy content"), 0600); err != nil {
		t.Fatalf("failed to seed legacy file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(newDir, "log_error.log"), []byte("already-migrated content"), 0600); err != nil {
		t.Fatalf("failed to seed destination file: %v", err)
	}

	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected migration to report success even when skipping a collision")
	}

	legacyGot, err := os.ReadFile(filepath.Join(legacyDir, "log_error.log"))
	if err != nil {
		t.Fatalf("expected legacy file to remain untouched: %v", err)
	}
	if string(legacyGot) != "legacy content" {
		t.Errorf("legacy file was modified: got %q", legacyGot)
	}

	newGot, err := os.ReadFile(filepath.Join(newDir, "log_error.log"))
	if err != nil {
		t.Fatalf("expected destination file to remain: %v", err)
	}
	if string(newGot) != "already-migrated content" {
		t.Errorf("destination file was overwritten: got %q, want %q", newGot, "already-migrated content")
	}
}

func TestMigrateDBLogsToBackupStorage_SkipsFileOpenForSSTReceive(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	errorLogPath := filepath.Join(legacyDir, "log_error.log")
	auditLogPath := filepath.Join(legacyDir, "log_audit.log")
	if err := os.WriteFile(errorLogPath, []byte("in-flight error log"), 0600); err != nil {
		t.Fatalf("failed to seed error log: %v", err)
	}
	if err := os.WriteFile(auditLogPath, []byte("idle audit log"), 0600); err != nil {
		t.Fatalf("failed to seed audit log: %v", err)
	}

	// Simulate an in-flight SST receiver (scheduler- or API-mode) still
	// appending to log_error.log.
	const fakePort = 999999
	SSTs.Lock()
	SSTs.SSTconnections[fakePort] = &SST{Filename: errorLogPath}
	SSTs.Unlock()
	t.Cleanup(func() {
		SSTs.Lock()
		delete(SSTs.SSTconnections, fakePort)
		SSTs.Unlock()
	})

	if ok := server.migrateDBLogsToBackupStorage(); ok {
		t.Fatal("expected migration to report incomplete while a file is open for SST receive")
	}

	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")

	if _, err := os.Stat(errorLogPath); err != nil {
		t.Fatalf("expected in-flight error log to remain in the legacy dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(newDir, "log_error.log")); !os.IsNotExist(err) {
		t.Fatalf("expected in-flight error log NOT to be migrated yet, stat err = %v", err)
	}

	if _, err := os.Stat(auditLogPath); !os.IsNotExist(err) {
		t.Fatalf("expected idle audit log to be migrated away, stat err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(newDir, "log_audit.log"))
	if err != nil {
		t.Fatalf("expected idle audit log at destination: %v", err)
	}
	if string(got) != "idle audit log" {
		t.Errorf("migrated audit log content = %q, want %q", got, "idle audit log")
	}
}

func TestMigrateDBLogsToBackupStorage_ClearsEmptyPlaceholderAtDestination(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("failed to create backup-backed dir: %v", err)
	}

	if err := os.WriteFile(filepath.Join(legacyDir, "log_error.log"), []byte("real historical content"), 0600); err != nil {
		t.Fatalf("failed to seed legacy file: %v", err)
	}
	// Simulate a placeholder some writer (e.g. NewLogTailer's "create if
	// missing", or a fresh receiver open) created at the new canonical path
	// while the real migration for this kind was still pending.
	if err := os.WriteFile(filepath.Join(newDir, "log_error.log"), nil, 0600); err != nil {
		t.Fatalf("failed to seed placeholder file: %v", err)
	}

	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected migration to report success")
	}

	if _, err := os.Stat(filepath.Join(legacyDir, "log_error.log")); !os.IsNotExist(err) {
		t.Fatalf("expected legacy file to be migrated away, stat err = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(newDir, "log_error.log"))
	if err != nil {
		t.Fatalf("expected migrated file at destination: %v", err)
	}
	if string(got) != "real historical content" {
		t.Fatalf("expected the empty placeholder to be replaced by the real content, got %q", got)
	}
}

// TestMigrateDBLogsToBackupStorage_InFlightPlaceholderDoesNotPermanentlyStrandRealFile
// exercises the exact sequence a runtime db-log-on-backup-storage flip can
// produce: one kind is skipped because it's open for SST receive, a
// placeholder gets created at its new-location path in the meantime (as
// NewLogTailer would), and a *later* migration attempt (once the receiver
// has closed) must still recover the real content rather than treating the
// placeholder as "already migrated".
func TestMigrateDBLogsToBackupStorage_InFlightPlaceholderDoesNotPermanentlyStrandRealFile(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	errorLogPath := filepath.Join(legacyDir, "log_error.log")
	if err := os.WriteFile(errorLogPath, []byte("real historical content"), 0600); err != nil {
		t.Fatalf("failed to seed legacy file: %v", err)
	}

	// First attempt: the receiver is in flight, so migration for this kind
	// is skipped.
	const fakePort = 999998
	SSTs.Lock()
	SSTs.SSTconnections[fakePort] = &SST{Filename: errorLogPath}
	SSTs.Unlock()
	if ok := server.migrateDBLogsToBackupStorage(); ok {
		t.Fatal("expected first migration attempt to report incomplete")
	}

	// Meanwhile, something resolves the new canonical path for this kind and
	// creates an empty file there (mirroring NewLogTailer's "create if
	// missing" behavior when the tailer is restarted onto the new path
	// immediately after the location flip).
	newDir := filepath.Join(server.GetMyBackupDirectory(), "dblogs")
	if err := os.WriteFile(filepath.Join(newDir, "log_error.log"), nil, 0600); err != nil {
		t.Fatalf("failed to simulate placeholder creation: %v", err)
	}

	// The receiver finishes.
	SSTs.Lock()
	delete(SSTs.SSTconnections, fakePort)
	SSTs.Unlock()

	// A later migration attempt (e.g. triggered by maybeRetryDBLogMigration
	// on receiver close) must still recover the real content.
	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected the later migration attempt to succeed once the receiver closed")
	}
	got, err := os.ReadFile(filepath.Join(newDir, "log_error.log"))
	if err != nil {
		t.Fatalf("expected the real file to end up at the destination: %v", err)
	}
	if string(got) != "real historical content" {
		t.Fatalf("expected real content to replace the placeholder, got %q", got)
	}
	if _, err := os.Stat(errorLogPath); !os.IsNotExist(err) {
		t.Fatalf("expected the legacy file to be gone once really migrated: %v", err)
	}
}

func TestMaybeRetryDBLogMigration_RestartsTailersWhenMigrationPending(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	errorLogPath := filepath.Join(legacyDir, "log_error.log")
	if err := os.WriteFile(errorLogPath, []byte("in-flight error log"), 0600); err != nil {
		t.Fatalf("failed to seed error log: %v", err)
	}

	stopAll := func() {
		for _, tl := range []*tail.Tail{server.ErrorLogTailer, server.SlowLogTailer, server.AuditLogTailer, server.SqlErrorLogTailer} {
			if tl != nil {
				tl.Stop()
				tl.Cleanup()
			}
		}
	}

	// Simulate an in-flight SST receiver on log_error.log, blocking a full
	// migration pass, then start tailers -- this mirrors a runtime flip that
	// happens while a receiver is still writing.
	const fakePort = 999997
	SSTs.Lock()
	SSTs.SSTconnections[fakePort] = &SST{Filename: errorLogPath}
	SSTs.Unlock()

	server.startLogTailers()
	defer stopAll()

	if server.dbLogMigrated.Load() {
		t.Fatal("test setup error: expected migration to still be pending while log_error.log is open for SST receive")
	}

	// The receiver finishes.
	SSTs.Lock()
	delete(SSTs.SSTconnections, fakePort)
	SSTs.Unlock()

	// Simulate that receiver's JobFinishReceiveFile calling this:
	// maybeRetryDBLogMigration should retry migration (now unblocked) and
	// restart tailers.
	server.maybeRetryDBLogMigration()
	defer stopAll()

	if !server.dbLogMigrated.Load() {
		t.Fatal("expected maybeRetryDBLogMigration to trigger a successful migration retry")
	}
}

func TestMaybeRetryDBLogMigration_NoopWhenAlreadyMigrated(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true
	server.dbLogMigrated.Store(true)

	// No tailers started at all: if maybeRetryDBLogMigration is truly a
	// no-op here, RestartLogTailers must never run.
	server.maybeRetryDBLogMigration()

	if server.ErrorLogTailer != nil {
		t.Fatal("expected maybeRetryDBLogMigration to do nothing once migration is already marked done")
	}
}

func TestMaybeRetryDBLogMigration_NoopWhenBackupStorageDisabled(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	// DBLogOnBackupStorage left at its zero value (false).

	server.maybeRetryDBLogMigration()

	if server.ErrorLogTailer != nil {
		t.Fatal("expected maybeRetryDBLogMigration to do nothing when db-log-on-backup-storage is disabled")
	}
}

func TestRestartDBLogTailers_ResetsMigrationLatchOnFlip(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Servers = serverList{server}

	if err := os.MkdirAll(server.legacyDBLogDir(), 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	// Simulate a migration that already completed once in this process.
	server.dbLogMigrated.Store(true)

	server.ClusterGroup.RestartDBLogTailers()
	defer func() {
		for _, tl := range []*tail.Tail{server.ErrorLogTailer, server.SlowLogTailer, server.AuditLogTailer, server.SqlErrorLogTailer} {
			if tl != nil {
				tl.Stop()
				tl.Cleanup()
			}
		}
	}()

	if server.dbLogMigrated.Load() {
		t.Fatal("expected RestartDBLogTailers to reset the migration latch so a later re-enable re-sweeps the legacy dir")
	}
}

func TestMigrateDBLogsToBackupStorage_NoLegacyDirIsSuccess(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	// legacyDBLogDir is never created.
	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected migration to report success when there is nothing to migrate")
	}
}

func TestMigrateDBLogsToBackupStorage_UnreadableLegacyDirRetriesLater(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipping: running as root, permission bits do not block reads")
	}

	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "log_error.log"), []byte("x"), 0600); err != nil {
		t.Fatalf("failed to seed legacy file: %v", err)
	}
	if err := os.Chmod(legacyDir, 0000); err != nil {
		t.Fatalf("failed to make legacy dir unreadable: %v", err)
	}
	t.Cleanup(func() { os.Chmod(legacyDir, 0755) })

	if ok := server.migrateDBLogsToBackupStorage(); ok {
		t.Fatal("expected migration to report failure on a permission error, not silently succeed")
	}

	// ensureDBLogsMigrated must not latch success on this failure, so a later
	// call keeps retrying instead of assuming migration already happened.
	server.dbLogMigrated.Store(false)
	os.Chmod(legacyDir, 0755)
	if ok := server.migrateDBLogsToBackupStorage(); !ok {
		t.Fatal("expected migration to succeed once the legacy dir becomes readable again")
	}
}

func TestEnsureDBLogsMigrated_DoesNotLatchOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipping: running as root, permission bits do not block reads")
	}

	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	if err := os.Chmod(legacyDir, 0000); err != nil {
		t.Fatalf("failed to make legacy dir unreadable: %v", err)
	}
	t.Cleanup(func() { os.Chmod(legacyDir, 0755) })

	server.ensureDBLogsMigrated()
	if server.dbLogMigrated.Load() {
		t.Fatal("expected dbLogMigrated to stay false after a permission error")
	}

	os.Chmod(legacyDir, 0755)
	server.ensureDBLogsMigrated()
	if !server.dbLogMigrated.Load() {
		t.Fatal("expected dbLogMigrated to become true once a retry succeeds")
	}
}

// sameDevice reports whether a and b live on the same filesystem/device.
func sameDevice(a, b string) (bool, error) {
	var stA, stB syscall.Stat_t
	if err := syscall.Stat(a, &stA); err != nil {
		return false, err
	}
	if err := syscall.Stat(b, &stB); err != nil {
		return false, err
	}
	return stA.Dev == stB.Dev, nil
}

func TestRenameOrCopyFile_CrossDeviceFallback(t *testing.T) {
	const altDir = "/dev/shm"
	if _, err := os.Stat(altDir); err != nil {
		t.Skipf("skipping: %s not available: %v", altDir, err)
	}

	tmp := t.TempDir()
	same, err := sameDevice(tmp, altDir)
	if err != nil {
		t.Skipf("skipping: cannot stat devices: %v", err)
	}
	if same {
		t.Skip("skipping: no distinct filesystem available to exercise a real cross-device rename")
	}

	dstDir, err := os.MkdirTemp(altDir, "dblog-migrate-test-")
	if err != nil {
		t.Skipf("skipping: cannot create dir under %s: %v", altDir, err)
	}
	defer os.RemoveAll(dstDir)

	src := filepath.Join(tmp, "log_error.log")
	dst := filepath.Join(dstDir, "log_error.log")
	content := "cross-device migration content\n"
	if err := os.WriteFile(src, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	if err := renameOrCopyFile(src, dst); err != nil {
		t.Fatalf("renameOrCopyFile returned error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != content {
		t.Fatalf("destination content = %q, want %q", got, content)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("expected source file to be removed after migration, stat err = %v", err)
	}
}

func TestRenameOrCopyFile_CrossDeviceDoesNotOverwriteDestination(t *testing.T) {
	const altDir = "/dev/shm"
	if _, err := os.Stat(altDir); err != nil {
		t.Skipf("skipping: %s not available: %v", altDir, err)
	}

	tmp := t.TempDir()
	same, err := sameDevice(tmp, altDir)
	if err != nil {
		t.Skipf("skipping: cannot stat devices: %v", err)
	}
	if same {
		t.Skip("skipping: no distinct filesystem available to exercise a real cross-device rename")
	}

	dstDir, err := os.MkdirTemp(altDir, "dblog-migrate-test-")
	if err != nil {
		t.Skipf("skipping: cannot create dir under %s: %v", altDir, err)
	}
	defer os.RemoveAll(dstDir)

	src := filepath.Join(tmp, "log_error.log")
	dst := filepath.Join(dstDir, "log_error.log")
	if err := os.WriteFile(src, []byte("source content"), 0600); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}
	if err := os.WriteFile(dst, []byte("pre-existing content"), 0600); err != nil {
		t.Fatalf("failed to write destination file: %v", err)
	}

	if err := renameOrCopyFile(src, dst); err == nil {
		t.Fatal("expected renameOrCopyFile to fail rather than overwrite an existing cross-device destination")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != "pre-existing content" {
		t.Fatalf("destination content was overwritten: got %q", got)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("expected source file to remain after a failed copy: %v", err)
	}
}

func TestRestartLogTailers_ReopensAtNewCanonicalPath(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)

	if err := os.MkdirAll(server.legacyDBLogDir(), 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	server.startLogTailers()
	defer func() {
		for _, tl := range []*tail.Tail{server.ErrorLogTailer, server.SlowLogTailer, server.AuditLogTailer, server.SqlErrorLogTailer} {
			if tl != nil {
				tl.Stop()
				tl.Cleanup()
			}
		}
	}()

	wantLegacy := filepath.Join(server.legacyDBLogDir(), "log_error.log")
	if server.ErrorLogTailer == nil || server.ErrorLogTailer.Filename != wantLegacy {
		t.Fatalf("expected initial tailer path %q, got tailer=%v", wantLegacy, server.ErrorLogTailer)
	}

	// Flip the location switch at runtime and restart, as setClusterSetting
	// does for "db-log-on-backup-storage".
	server.ClusterGroup.Conf.DBLogOnBackupStorage = true
	server.RestartLogTailers()
	defer func() {
		for _, tl := range []*tail.Tail{server.ErrorLogTailer, server.SlowLogTailer, server.AuditLogTailer, server.SqlErrorLogTailer} {
			if tl != nil {
				tl.Stop()
				tl.Cleanup()
			}
		}
	}()

	wantNew := filepath.Join(server.GetMyBackupDirectory(), "dblogs", "log_error.log")
	if server.ErrorLogTailer == nil || server.ErrorLogTailer.Filename != wantNew {
		t.Fatalf("expected tailer to reopen at backup-backed path %q, got tailer=%v", wantNew, server.ErrorLogTailer)
	}
	if wantNew == wantLegacy {
		t.Fatal("test setup error: legacy and new paths must differ")
	}

	// Confirm the reopened tailer is actually live, not just holding the
	// right filename: writing to the new canonical file should reach
	// server.ErrorLog (the ring buffer RestartLogTailers' ErrorLogWatcher
	// populates). We read via ErrorLog rather than server.ErrorLogTailer.Lines
	// directly, since RestartLogTailers already started ErrorLogWatcher as a
	// goroutine draining that same (unbuffered) channel -- reading it again
	// here would race that goroutine for the one delivery. Give the tail
	// goroutine a moment to finish registering its inotify watch before
	// writing, to avoid racing that one-time startup step.
	time.Sleep(200 * time.Millisecond)
	f, err := os.OpenFile(wantNew, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatalf("failed to open new canonical log file: %v", err)
	}
	if _, err := f.WriteString("hello from backup-backed path\n"); err != nil {
		t.Fatalf("failed to write to new canonical log file: %v", err)
	}
	f.Close()

	deadline := time.Now().Add(5 * time.Second)
	for {
		server.ErrorLog.L.Lock()
		found := len(server.ErrorLog.Buffer) > 0 && strings.Contains(server.ErrorLog.Buffer[0].Text, "hello from backup-backed path")
		server.ErrorLog.L.Unlock()
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the restarted tailer to pick up new content at the new path")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestNewLogTailer_PrunesOldStyleRotatedFilesWhenRotateEnabled(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	server.ClusterGroup.Conf.DBLogRotateMaxAge = 7

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	oldStyle := filepath.Join(legacyDir, "log_error_20200101_000000.log")
	lumberjackStyle := filepath.Join(legacyDir, "log_error-2020-01-01T00-00-00.000.log")
	if err := os.WriteFile(oldStyle, []byte("old"), 0600); err != nil {
		t.Fatalf("failed to seed old-style rotated file: %v", err)
	}
	if err := os.WriteFile(lumberjackStyle, []byte("lumberjack"), 0600); err != nil {
		t.Fatalf("failed to seed lumberjack-style rotated file: %v", err)
	}

	tl, err := server.NewLogTailer("error")
	if err != nil {
		t.Fatalf("NewLogTailer returned error: %v", err)
	}
	defer func() { tl.Stop(); tl.Cleanup() }()

	if _, err := os.Stat(oldStyle); !os.IsNotExist(err) {
		t.Fatalf("expected old-style rotated file (2020, well past MaxAge=7d) to be pruned, stat err = %v", err)
	}
	if _, err := os.Stat(lumberjackStyle); err != nil {
		t.Fatalf("expected lumberjack-style rotated file to be left alone (lumberjack manages its own scheme): %v", err)
	}
}

func TestNewLogTailer_DoesNotPruneWhenRotateDisabled(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	// DBLogRotate left at its zero value (false): compatibility-first, no
	// pruning of any kind should happen.

	legacyDir := server.legacyDBLogDir()
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}
	oldStyle := filepath.Join(legacyDir, "log_error_20200101_000000.log")
	if err := os.WriteFile(oldStyle, []byte("old"), 0600); err != nil {
		t.Fatalf("failed to seed old-style rotated file: %v", err)
	}

	tl, err := server.NewLogTailer("error")
	if err != nil {
		t.Fatalf("NewLogTailer returned error: %v", err)
	}
	defer func() { tl.Stop(); tl.Cleanup() }()

	if _, err := os.Stat(oldStyle); err != nil {
		t.Fatalf("expected old-style rotated file to remain untouched when db-log-rotate is disabled: %v", err)
	}
}
