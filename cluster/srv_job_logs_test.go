package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hpcloud/tail"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func newTestServerForDBLogs(t *testing.T, workingDir string) *ServerMonitor {
	t.Helper()
	return newTestServerForDBLogsWithHost(t, workingDir, "node1", "3306")
}

// newTestServerForDBLogsWithHost is newTestServerForDBLogs for tests that
// need multiple distinct servers -- Datadir (and so every DBLogFilePath) is
// derived from host/port, so distinct hosts get genuinely distinct canonical
// DB log paths. Needed because the DB log writer cache is now keyed by
// absolute path (see getDBLogRotatingWriter), so two ServerMonitor fixtures
// that happened to share a Datadir would collide in the cache regardless of
// their Host field.
func newTestServerForDBLogsWithHost(t *testing.T, workingDir string, host string, port string) *ServerMonitor {
	t.Helper()
	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir: workingDir,
		},
		Name: "testcluster",
	}
	server := &ServerMonitor{
		ClusterGroup: cluster,
		Datadir:      filepath.Join(workingDir, "cluster-dir", host+"_"+port),
		Host:         host,
		Port:         port,
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

// TestGetDBLogRotatingWriter_ReusesSameWriterAcrossCalls guards against the
// original bug: NewRotateWriter used to be called fresh on every fetch/job,
// and each fresh *lumberjack.Logger leaks a millRun goroutine on Close (mill()
// starts it on first Write; Close never stops it). Repeated calls for the
// same server/kind must now return the exact same writer instance instead of
// allocating a new one.
func TestGetDBLogRotatingWriter_ReusesSameWriterAcrossCalls(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true

	w1, release1, err := server.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("first getDBLogRotatingWriter call returned error: %v", err)
	}
	defer release1()
	w2, release2, err := server.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("second getDBLogRotatingWriter call returned error: %v", err)
	}
	defer release2()
	if w1 != w2 {
		t.Fatal("expected repeated calls for the same kind to return the same cached writer instance")
	}

	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected exactly one cached writer entry, got %d", len(server.ClusterGroup.dbLogWriters))
	}
}

// TestGetDBLogRotatingWriter_DistinctKindsGetDistinctWriters confirms the
// cache is keyed per canonical path, not shared across all four fetched log
// files for one server.
func TestGetDBLogRotatingWriter_DistinctKindsGetDistinctWriters(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true

	errW, releaseErr, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter(DBLogError) returned error: %v", err)
	}
	defer releaseErr()
	slowW, releaseSlow, err := server.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter(DBLogSlowQuery) returned error: %v", err)
	}
	defer releaseSlow()
	if errW == slowW {
		t.Fatal("expected distinct DBLogKinds to get distinct writers")
	}
	if len(server.ClusterGroup.dbLogWriters) != 2 {
		t.Fatalf("expected two cached writer entries, got %d", len(server.ClusterGroup.dbLogWriters))
	}
}

// TestGetDBLogRotatingWriter_DistinctServersGetDistinctWriters confirms the
// cluster-scoped cache does not accidentally collapse two different
// servers' entries for the same DBLogKind onto one writer.
func TestGetDBLogRotatingWriter_DistinctServersGetDistinctWriters(t *testing.T) {
	tmp := t.TempDir()
	node1 := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	node2 := newTestServerForDBLogsWithHost(t, tmp, "node2", "3306")
	node2.ClusterGroup = node1.ClusterGroup
	node1.ClusterGroup.Conf.DBLogRotate = true

	w1, release1, err := node1.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter for node1 returned error: %v", err)
	}
	defer release1()
	w2, release2, err := node2.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter for node2 returned error: %v", err)
	}
	defer release2()

	if w1 == w2 {
		t.Fatal("expected distinct servers to get distinct writers even for the same DBLogKind")
	}
	if len(node1.ClusterGroup.dbLogWriters) != 2 {
		t.Fatalf("expected two cached writer entries (one per server), got %d", len(node1.ClusterGroup.dbLogWriters))
	}
}

// TestGetDBLogRotatingWriter_OrdinaryReloadReusesSameWriter is the core
// regression test for the cluster-scoped, path-keyed redesign: an ordinary
// reload (same host, brand new *ServerMonitor object -- see
// newServerMonitor, which never reuses an existing instance) must resolve to
// the SAME cached writer as the old ServerMonitor, not a second independent
// one. Two independent *lumberjack.Logger instances for the same path have
// independent size/rotation bookkeeping and can lose data into a backup file
// if either one rotates -- which is exactly what a per-ServerMonitor cache
// allowed across a reload racing a long-running GetSlowLogTable export.
func TestGetDBLogRotatingWriter_OrdinaryReloadReusesSameWriter(t *testing.T) {
	tmp := t.TempDir()
	oldServer := newTestServerForDBLogs(t, tmp)
	oldServer.ClusterGroup.Conf.DBLogRotate = true

	oldWriter, oldRelease, err := oldServer.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter on old server returned error: %v", err)
	}
	defer oldRelease()

	// Simulate a reload: a brand new *ServerMonitor for the identical host
	// (same Datadir/Host/Port -- same ClusterGroup), exactly as
	// newServerMonitor produces on every reload regardless of whether the
	// host actually changed.
	newServer := newTestServerForDBLogs(t, tmp)
	newServer.ClusterGroup = oldServer.ClusterGroup

	newWriter, newRelease, err := newServer.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter on new server returned error: %v", err)
	}
	defer newRelease()

	if oldWriter != newWriter {
		t.Fatal("expected the new ServerMonitor to resolve to the same cached writer as the old one for an unchanged path")
	}
	if len(oldServer.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected exactly one cache entry shared across both ServerMonitor instances, got %d", len(oldServer.ClusterGroup.dbLogWriters))
	}
}

// TestGetDBLogRotatingWriter_ReplacesWriterWhenMaxSettingsChange guards
// against a regression where db-log-rotate-max-size/backup/age (mutable at
// runtime via the cluster settings API, server/api_cluster.go) would get
// baked into a cached writer at creation and then silently stop applying
// after that, since lumberjack.Logger's MaxSize/MaxBackups/MaxAge are plain
// fields copied in once, not re-read from cluster.Conf on every write.
func TestGetDBLogRotatingWriter_ReplacesWriterWhenMaxSettingsChange(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	server.ClusterGroup.Conf.DBLogRotateMaxSize = 100
	server.ClusterGroup.Conf.DBLogRotateMaxBackup = 10
	server.ClusterGroup.Conf.DBLogRotateMaxAge = 7

	w1, release1, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("first getDBLogRotatingWriter call returned error: %v", err)
	}
	release1()

	// Simulate a runtime "db-log-rotate-max-size" API call.
	server.ClusterGroup.Conf.DBLogRotateMaxSize = 250

	w2, release2, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter after threshold change returned error: %v", err)
	}
	defer release2()
	if w1 == w2 {
		t.Fatal("expected a new writer instance once db-log-rotate-max-size changed at runtime")
	}
	logfile := server.DBLogFilePath(DBLogError)
	if server.ClusterGroup.dbLogWriters[logfile].maxSize != 250 {
		t.Fatalf("expected cached entry to record the new maxSize, got %d", server.ClusterGroup.dbLogWriters[logfile].maxSize)
	}

	// A repeated call with unchanged settings must go back to reusing the
	// writer, not keep recreating it forever.
	w3, release3, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	defer release3()
	if w2 != w3 {
		t.Fatal("expected the writer to be reused once settings stop changing")
	}
}

// TestGetDBLogRotatingWriter_EvictionDoesNotCloseWriterWhileBorrowed guards
// against the core risk this refcounting exists to prevent: an eviction
// (threshold change, CloseAllDBLogWriters, pruneStaleDBLogWriters) racing
// with a long-lived borrower -- an SST receiver's async stream_copy_to_file
// goroutine can hold a writer open for as long as sstStreamIdleTimeout
// allows -- must not actually Close the underlying writer until that
// borrower releases it, since lumberjack silently reopens a closed writer on
// next Write rather than erroring, which would otherwise leave two
// independent *lumberjack.Logger instances (with independent size/rotation
// bookkeeping) writing the same path concurrently.
func TestGetDBLogRotatingWriter_EvictionDoesNotCloseWriterWhileBorrowed(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true

	if err := os.MkdirAll(server.legacyDBLogDir(), 0755); err != nil {
		t.Fatalf("failed to create legacy dir: %v", err)
	}

	// Simulate a long-lived borrower (an in-flight SST receiver) that has
	// acquired the writer but not released it yet.
	w, release, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	if _, err := w.Write([]byte("in-flight receiver data\n")); err != nil {
		t.Fatalf("write through the borrowed writer failed: %v", err)
	}

	// Evict it (mirrors CloseAllDBLogWriters/pruneStaleDBLogWriters).
	server.ClusterGroup.CloseAllDBLogWriters()

	// It must still be cached (marked stale, not removed): the borrower
	// isn't done with it yet.
	logfile := server.DBLogFilePath(DBLogError)
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected the borrowed entry to remain cached (stale) after eviction, got %d entries", len(server.ClusterGroup.dbLogWriters))
	}

	// The borrower must still be able to write successfully after eviction:
	// the real Close is deferred until it releases.
	if _, err := w.Write([]byte("more data while still borrowed\n")); err != nil {
		t.Fatalf("write through the evicted-but-still-borrowed writer failed: %v", err)
	}

	got, err := os.ReadFile(logfile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !strings.Contains(string(got), "in-flight receiver data") || !strings.Contains(string(got), "more data while still borrowed") {
		t.Fatalf("expected both writes to land in the log file, got %q", got)
	}

	// Release: this is the last (only) borrower, so the deferred close/removal
	// happens synchronously inside release().
	release()

	if len(server.ClusterGroup.dbLogWriters) != 0 {
		t.Fatalf("expected release() to close and remove the stale entry once the last borrower is done, got %d entries", len(server.ClusterGroup.dbLogWriters))
	}
}

// TestCloseAllDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing is the
// CloseAllDBLogWriters-specific companion to
// TestGetDBLogRotatingWriter_EvictionDoesNotCloseWriterWhileBorrowed: with
// two borrowers on the same entry, it must take both releases -- not just
// one -- before the entry is actually closed and removed.
func TestCloseAllDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	server.ClusterGroup.Servers = serverList{server}

	_, release1, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("first getDBLogRotatingWriter call returned error: %v", err)
	}
	_, release2, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("second getDBLogRotatingWriter call returned error: %v", err)
	}

	server.ClusterGroup.CloseAllDBLogWriters()

	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected the twice-borrowed entry to remain cached (stale), got %d entries", len(server.ClusterGroup.dbLogWriters))
	}

	release1()
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatal("expected the entry to remain cached after only one of two borrowers released")
	}

	release2()
	if len(server.ClusterGroup.dbLogWriters) != 0 {
		t.Fatalf("expected the last release to close and remove the entry, got %d entries", len(server.ClusterGroup.dbLogWriters))
	}
}

// TestPruneStaleDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing is the
// pruneStaleDBLogWriters-specific companion to
// TestCloseAllDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing: a
// server dropping out of the topology (host removal, reload) while its
// writer is still borrowed must not have that writer closed out from under
// the borrower -- it should be marked stale and only actually closed once
// the last borrower releases it.
func TestPruneStaleDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	cluster := server.ClusterGroup
	cluster.Servers = serverList{server}

	_, release, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}

	// The server drops out of the topology entirely (e.g. host removed from
	// config on reload) while its writer is still borrowed.
	cluster.Servers = serverList{}
	cluster.pruneStaleDBLogWriters()

	if len(cluster.dbLogWriters) != 1 {
		t.Fatalf("expected the borrowed entry to remain cached (stale) after pruning, got %d entries", len(cluster.dbLogWriters))
	}

	release()

	if len(cluster.dbLogWriters) != 0 {
		t.Fatalf("expected release() to close and remove the pruned-but-borrowed entry once drained, got %d entries", len(cluster.dbLogWriters))
	}
}

// TestGetDBLogRotatingWriter_ReleaseIsSafeToCallMoreThanOnce guards against a
// caller mistake (e.g. both an explicit release call and an unconditional
// deferred one) desyncing the borrower count from actual outstanding
// borrows, which could cause a still-in-use writer to be closed out from
// under an unrelated, later borrower. The release func returned by
// getDBLogRotatingWriter must be idempotent.
func TestGetDBLogRotatingWriter_ReleaseIsSafeToCallMoreThanOnce(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true

	_, release, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}

	// Force the entry stale so a release can actually trigger a close --
	// otherwise a non-stale entry's release is pure accounting and a repeat
	// call wouldn't reveal anything (the bug only bites once stale=true).
	server.ClusterGroup.Conf.DBLogRotateMaxSize++
	_, release2, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("second getDBLogRotatingWriter call returned error: %v", err)
	}
	defer release2()

	// First (legitimate) release: one borrower left (release2's), entry
	// stays cached.
	release()
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatal("expected the stale entry to remain cached while release2's borrow is still outstanding")
	}

	// Calling release again must be a no-op. Without the sync.Once guard,
	// this would decrement borrowers a second time (to -1, since release2's
	// borrow is the only real one left) and, because the entry is stale,
	// incorrectly close and remove it out from under release2's still-active
	// borrow.
	release()

	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatal("expected a repeated release call to be a no-op and not close the entry out from under a later, unrelated borrower")
	}
}

// TestGetDBLogRotatingWriter_ThresholdChangeWhileBorrowedDefersReplacement is
// the regression test for the gap CloseAllDBLogWriters/
// pruneStaleDBLogWriters already handled but getDBLogRotatingWriter's own
// threshold-mismatch path did not: on a db-log-rotate-max-* change
// (server/api_cluster.go) while the current writer is still borrowed, a
// naive "evict now, create the replacement now" would spin up a second live
// *lumberjack.Logger for the same path immediately -- not just delay closing
// the old one -- which is exactly the dual-writer hazard this cache exists
// to prevent. The fix keeps serving the same (now stale) writer to every
// acquirer until the last borrower releases it, and only then creates the
// replacement.
func TestGetDBLogRotatingWriter_ThresholdChangeWhileBorrowedDefersReplacement(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	server.ClusterGroup.Conf.DBLogRotateMaxSize = 100

	// Acquire A and do not release it -- simulates an in-flight SST receiver
	// or GetSlowLogTable export still writing through it.
	writerA, releaseA, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("first getDBLogRotatingWriter call returned error: %v", err)
	}

	// Simulate a runtime "db-log-rotate-max-size" API call while A is still
	// borrowed.
	server.ClusterGroup.Conf.DBLogRotateMaxSize = 250

	// A second acquire for the same path, while A is still outstanding, must
	// get A back -- NOT a second, independent writer -- even though the
	// thresholds no longer match what A was created with.
	writerAAgain, releaseAAgain, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("second getDBLogRotatingWriter call returned error: %v", err)
	}
	if writerAAgain != writerA {
		t.Fatal("expected a threshold change to keep serving the still-borrowed writer instead of creating a second one for the same path")
	}
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected exactly one cache entry while A is still borrowed, got %d", len(server.ClusterGroup.dbLogWriters))
	}

	// Release one of the two borrows: still not the last one, so no
	// replacement yet.
	releaseA()
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatal("expected the entry to remain cached after only one of two borrowers released")
	}

	// Release the last borrow: NOW the stale entry is closed and removed.
	releaseAAgain()
	if len(server.ClusterGroup.dbLogWriters) != 0 {
		t.Fatalf("expected the last release to close and remove the stale entry, got %d entries", len(server.ClusterGroup.dbLogWriters))
	}

	// A fresh acquire now gets a genuinely new writer, with the current
	// (changed) threshold.
	writerB, releaseB, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter after drain returned error: %v", err)
	}
	defer releaseB()
	if writerB == writerA {
		t.Fatal("expected a genuinely new writer instance once the stale one fully drained")
	}
	logfile := server.DBLogFilePath(DBLogError)
	if server.ClusterGroup.dbLogWriters[logfile].maxSize != 250 {
		t.Fatalf("expected the new writer to be created with the current maxSize, got %d", server.ClusterGroup.dbLogWriters[logfile].maxSize)
	}
}

// TestClusterClose_ClosesAllDBLogWriters guards against the reload/teardown
// paths bypassing DB log writer cleanup: Cluster.Close used to close each
// server's Conn directly without ever touching the cached rotating writers,
// leaking their millRun goroutines on every cluster shutdown.
func TestClusterClose_ClosesAllDBLogWriters(t *testing.T) {
	tmp := t.TempDir()
	server := newTestServerForDBLogs(t, tmp)
	server.ClusterGroup.Conf.DBLogRotate = true
	server.ClusterGroup.Servers = serverList{server}
	// A real but unconnected *sqlx.DB: Cluster.Close unconditionally calls
	// Conn.Close(), which panics on a nil *sqlx.DB, so the test needs a live
	// (if never-dialed) handle rather than leaving Conn nil.
	conn, err := sqlx.Open("mysql", "user:pass@tcp(127.0.0.1:3306)/db")
	if err != nil {
		t.Fatalf("failed to open test DB handle: %v", err)
	}
	server.Conn = conn

	_, release, err := server.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	if len(server.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected one cached writer entry before Close, got %d", len(server.ClusterGroup.dbLogWriters))
	}
	// Release before Close: this test is about Close reaching an unborrowed
	// writer, not about the stale/borrow deferral (see
	// TestGetDBLogRotatingWriter_EvictionDoesNotCloseWriterWhileBorrowed for
	// that).
	release()

	server.ClusterGroup.Close()

	if len(server.ClusterGroup.dbLogWriters) != 0 {
		t.Fatalf("expected Cluster.Close to clear the cluster's DB log writer cache, got %d entries", len(server.ClusterGroup.dbLogWriters))
	}
}

// TestNewServerList_PrunesWritersForHostsDroppedFromTopology guards against
// a reload (ReloadConfig -> InitFromConf -> newServerList) silently
// abandoning cached writers for hosts that actually left the topology, with
// no cleanup call at all.
func TestNewServerList_PrunesWritersForHostsDroppedFromTopology(t *testing.T) {
	tmp := t.TempDir()
	oldServer := newTestServerForDBLogs(t, tmp)
	oldServer.ClusterGroup.Conf.DBLogRotate = true

	_, release, err := oldServer.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	if len(oldServer.ClusterGroup.dbLogWriters) != 1 {
		t.Fatalf("expected one cached writer entry before reload, got %d", len(oldServer.ClusterGroup.dbLogWriters))
	}
	// Release before the reload: this test is about pruning reaching an
	// unborrowed writer, not about the stale/borrow deferral.
	release()

	cluster := oldServer.ClusterGroup
	cluster.Servers = serverList{oldServer}
	// No hosts configured: newServerList will rebuild Servers into an empty
	// slice, i.e. this host actually left the topology, which is what should
	// make its cached writer stale.
	cluster.Conf.Hosts = ""

	if err := cluster.newServerList(); err != nil {
		t.Fatalf("newServerList returned error: %v", err)
	}

	if len(cluster.dbLogWriters) != 0 {
		t.Fatalf("expected newServerList to prune the dropped host's DB log writers, got %d entries", len(cluster.dbLogWriters))
	}
}

// TestPruneStaleDBLogWriters_OrdinaryReloadLeavesStillValidWriterAlone is the
// complement of the drop test above: when a host's canonical DB log path is
// still produced by some current server (the common reload case -- see
// newServerMonitor, srv.go:421, which deterministically derives Datadir from
// WorkingDir/cluster name/host/port, so an unchanged host always recomputes
// the same path regardless of which *ServerMonitor generation asks), pruning
// must NOT close its cached writer just because the specific *ServerMonitor
// instance that created it is no longer the one in cluster.Servers.
//
// This is the core regression test for the cluster-scoped, path-keyed
// redesign: it is what makes an ordinary reload -- which always replaces
// every *ServerMonitor object, even for unchanged hosts -- safe to run
// concurrently with a long-running GetSlowLogTable export instead of
// spawning a second independent *lumberjack.Logger for the same path.
func TestPruneStaleDBLogWriters_OrdinaryReloadLeavesStillValidWriterAlone(t *testing.T) {
	tmp := t.TempDir()
	oldServer := newTestServerForDBLogs(t, tmp)
	oldServer.ClusterGroup.Conf.DBLogRotate = true
	cluster := oldServer.ClusterGroup
	cluster.Servers = serverList{oldServer}

	writer, release, err := oldServer.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	defer release()

	// Simulate a reload: a brand new *ServerMonitor for the identical host
	// (same Datadir/Host/Port), exactly as newServerMonitor produces on every
	// reload regardless of whether the host actually changed, replaces the
	// old one in cluster.Servers.
	newServer := newTestServerForDBLogs(t, tmp)
	newServer.ClusterGroup = cluster
	cluster.Servers = serverList{newServer}

	cluster.pruneStaleDBLogWriters()

	if len(cluster.dbLogWriters) != 1 {
		t.Fatalf("expected the still-valid writer to remain cached across an ordinary reload, got %d entries", len(cluster.dbLogWriters))
	}

	// The new ServerMonitor for the same host must resolve to the exact same
	// writer instance -- not a second one -- for this to be a real fix.
	newWriter, newRelease, err := newServer.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter on the post-reload server returned error: %v", err)
	}
	defer newRelease()
	if newWriter != writer {
		t.Fatal("expected the post-reload ServerMonitor to reuse the pre-reload cached writer for the same path")
	}
}

// TestRemoveServerFromIndex_PrunesRemovedServersDBLogWriters guards against
// dynamic server removal (e.g. topology churn dropping an unlinked
// child-cluster server, cluster_topo.go) silently abandoning cached writers
// for the removed server's paths, with no cleanup call.
func TestRemoveServerFromIndex_PrunesRemovedServersDBLogWriters(t *testing.T) {
	tmp := t.TempDir()
	removed := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	removed.ClusterGroup.Conf.DBLogRotate = true

	kept := newTestServerForDBLogsWithHost(t, tmp, "node2", "3306")
	kept.ClusterGroup = removed.ClusterGroup

	cluster := removed.ClusterGroup
	cluster.Servers = serverList{removed, kept}

	_, removedRelease, err := removed.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	// Release before removal: this test is about pruning reaching an
	// unborrowed writer, not about the stale/borrow deferral.
	removedRelease()
	_, keptRelease, err := kept.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	defer keptRelease()

	cluster.RemoveServerFromIndex(0)

	if len(cluster.Servers) != 1 || cluster.Servers[0] != kept {
		t.Fatalf("expected only the kept server to remain, got %v", cluster.Servers)
	}
	if len(cluster.dbLogWriters) != 1 {
		t.Fatalf("expected only the kept server's writer to remain cached, got %d entries", len(cluster.dbLogWriters))
	}
}

// TestRemoveServerMonitor_PrunesRemovedServersDBLogWriters is the
// RemoveServerMonitor (host/port-based removal, used by the server-removal
// API) counterpart of TestRemoveServerFromIndex_PrunesRemovedServersDBLogWriters.
func TestRemoveServerMonitor_PrunesRemovedServersDBLogWriters(t *testing.T) {
	tmp := t.TempDir()
	removed := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	removed.ClusterGroup.Conf.DBLogRotate = true

	kept := newTestServerForDBLogsWithHost(t, tmp, "node2", "3306")
	kept.ClusterGroup = removed.ClusterGroup

	cluster := removed.ClusterGroup
	cluster.Servers = serverList{removed, kept}
	// RemoveServerMonitor drives the cluster's failover state machine; the
	// bare test fixture doesn't set one up, unlike a real cluster (see
	// InitFromConf).
	cluster.StateMachine = new(state.StateMachine)
	cluster.StateMachine.Init()

	_, removedRelease, err := removed.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	// Release before removal: this test is about pruning reaching an
	// unborrowed writer, not about the stale/borrow deferral.
	removedRelease()
	_, keptRelease, err := kept.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	defer keptRelease()

	if err := cluster.RemoveServerMonitor("node1", "3306"); err != nil {
		t.Fatalf("RemoveServerMonitor returned error: %v", err)
	}

	if len(cluster.Servers) != 1 || cluster.Servers[0] != kept {
		t.Fatalf("expected only the kept server to remain, got %v", cluster.Servers)
	}
	if len(cluster.dbLogWriters) != 1 {
		t.Fatalf("expected only the kept server's writer to remain cached, got %d entries", len(cluster.dbLogWriters))
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

// TestCloseAllDBLogWriters_ClosesEveryCachedWriter guards against runtime
// db-log-rotate=false leaving cached writers behind: once the flag is off,
// neither GetSlowLogTable nor SSTRunReceiverToDBLogFile ever calls
// getDBLogRotatingWriter again for that cluster, so nothing else would ever
// evict/close writers created while it was on. CloseAllDBLogWriters (called
// from server/api_cluster.go's db-log-rotate handlers on an actual flip) is
// the only thing that does.
func TestCloseAllDBLogWriters_ClosesEveryCachedWriter(t *testing.T) {
	tmp := t.TempDir()
	first := newTestServerForDBLogsWithHost(t, tmp, "node1", "3306")
	first.ClusterGroup.Conf.DBLogRotate = true

	second := newTestServerForDBLogsWithHost(t, tmp, "node2", "3306")
	second.ClusterGroup = first.ClusterGroup

	cluster := first.ClusterGroup
	cluster.Servers = serverList{first, second}

	_, releaseFirst, err := first.getDBLogRotatingWriter(DBLogError)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	_, releaseSecond, err := second.getDBLogRotatingWriter(DBLogSlowQuery)
	if err != nil {
		t.Fatalf("getDBLogRotatingWriter returned error: %v", err)
	}
	if len(cluster.dbLogWriters) != 2 {
		t.Fatalf("expected two cached writer entries before disabling, got %d", len(cluster.dbLogWriters))
	}
	// Release before disabling: this test is about CloseAllDBLogWriters
	// reaching unborrowed writers, not about the stale/borrow deferral (see
	// TestCloseAllDBLogWriters_MarksBorrowedWriterStaleInsteadOfClosing).
	releaseFirst()
	releaseSecond()

	// Simulate the runtime "db-log-rotate" toggle/set-value handlers in
	// server/api_cluster.go flipping the flag off and calling this.
	cluster.Conf.DBLogRotate = false
	cluster.CloseAllDBLogWriters()

	if len(cluster.dbLogWriters) != 0 {
		t.Fatalf("expected CloseAllDBLogWriters to close every cached writer, got %d entries", len(cluster.dbLogWriters))
	}
}
