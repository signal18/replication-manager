package backupmgr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"
	"github.com/sirupsen/logrus"
)

var (
	sharedRepoDir  string
	sharedCacheDir string
	sharedDataDir  string
	testMsgChan    chan sharedlog.Message
	logStop        chan struct{}
)

func TestMain(m *testing.M) {
	base, err := os.MkdirTemp("", "restic-test-*")
	if err != nil {
		panic(err)
	}
	sharedRepoDir = filepath.Join(base, "repo")
	sharedCacheDir = filepath.Join(base, "cache")
	sharedDataDir = filepath.Join(base, "data")
	testMsgChan = make(chan sharedlog.Message, 128)
	logStop = make(chan struct{})

	go func() {
		for {
			select {
			case msg := <-testMsgChan:
				fmt.Printf("[%s] %s\n", msg.Level, msg.Text)
			case <-logStop:
				return
			}
		}
	}()

	code := m.Run()
	close(logStop)
	_ = os.RemoveAll(base)
	os.Exit(code)
}

func getTestingDirs(t *testing.T) (string, string, string) {
	t.Helper()

	return sharedRepoDir, sharedCacheDir, sharedDataDir
}

func resetSharedDirs(t *testing.T) {
	t.Helper()

	_, cacheDir, dataDir := getTestingDirs(t)
	dirs := []string{sharedRepoDir, cacheDir, dataDir}
	for _, dir := range dirs {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("remove dir %s: %v", dir, err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatalf("mkdir dir %s: %v", dir, err)
		}
	}
}

func getResticBinaryPath(t *testing.T) string {
	t.Helper()

	cmd := exec.Command("which", "restic")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("which restic: %v", err)
	}
	path := strings.TrimSpace(out.String())
	if path == "" {
		t.Fatalf("restic binary not found")
	}
	return path
}

func newPausedRepo(t *testing.T) *ResticManager {
	t.Helper()

	repo := NewResticRepo(getResticBinaryPath(t), testMsgChan, 0)
	repo.PauseWorker()
	t.Cleanup(func() {
		repo.ShutdownWorker()
	})
	return repo
}

func newResticRepo(t *testing.T, withBackup bool) (*ResticManager, string, string, string) {
	t.Helper()

	repoDir, cacheDir, dataDir := getTestingDirs(t)
	resetSharedDirs(t)

	repo := NewResticRepo(getResticBinaryPath(t), testMsgChan, 0)
	repo.PauseWorker()
	t.Cleanup(func() {
		repo.ShutdownWorker()
	})
	repo.SetEnv([]string{
		"RESTIC_PASSWORD=testpassword",
		"RESTIC_REPOSITORY=" + repoDir,
		"RESTIC_CACHE_DIR=" + cacheDir,
	})

	if err := repo.InitRepo(true); err != nil {
		t.Fatalf("init repo: %v", err)
	}

	if withBackup {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			t.Fatalf("mkdir data: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dataDir, "file.txt"), []byte("payload"), 0644); err != nil {
			t.Fatalf("write data file: %v", err)
		}
		if _, err := repo.Backup(dataDir, []string{"tag1"}); err != nil {
			t.Fatalf("backup: %v", err)
		}
		if err := repo.FetchRepo(); err != nil {
			t.Fatalf("fetch repo: %v", err)
		}
	}

	return repo, repoDir, cacheDir, dataDir
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for condition")
}

func waitForMountReady(t *testing.T, repo *ResticManager, mountDir string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		repo.mountMutex.Lock()
		mountCmd := repo.mountCmd
		mountDone := repo.mountDone
		repo.mountMutex.Unlock()

		if mountCmd == nil && mountDone == nil {
			t.Fatalf("mount exited before readiness; check logs for details")
		}

		entries, err := os.ReadDir(mountDir)
		if err == nil && len(entries) > 0 {
			return
		}
		if mountDone != nil {
			select {
			case err := <-mountDone:
				if err != nil {
					t.Fatalf("mount exited early: %v", err)
				}
				t.Fatalf("mount exited unexpectedly")
			default:
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for mount readiness")
}

func TestGetOldestSnapshot(t *testing.T) {
	repo := newPausedRepo(t)
	if _, _, err := repo.GetOldestSnapshot(); err == nil {
		t.Fatalf("expected error with no snapshots")
	}

	repo.Backups = []BackupSnapshot{
		{Time: "2025-01-03T00:00:00Z"},
		{Time: "2025-01-01T00:00:00Z"},
		{Time: "invalid"},
	}

	oldest, oldestTime, err := repo.GetOldestSnapshot()
	if err != nil {
		t.Fatalf("get oldest snapshot: %v", err)
	}
	if oldest == nil || oldest.Time != "2025-01-01T00:00:00Z" {
		t.Fatalf("unexpected oldest snapshot: %+v", oldest)
	}
	if oldestTime.IsZero() {
		t.Fatalf("expected oldest time to be set")
	}
}

func TestPauseResumeWorker(t *testing.T) {
	repo := newPausedRepo(t)
	repo.PauseWorker()
	if !repo.IsPaused() {
		t.Fatalf("expected paused state")
	}
	repo.ResumeWorker()
	if repo.IsPaused() {
		t.Fatalf("expected resumed state")
	}
	repo.PauseWorkerOnDisk()
	if !repo.IsPaused() || !repo.isPausedByDisk {
		t.Fatalf("expected paused by disk")
	}
}

func TestErrorHelpers(t *testing.T) {
	repo := newPausedRepo(t)
	if repo.HasAnyError() {
		t.Fatalf("expected no errors")
	}
	repo.SetError(BackupTask, errors.New("boom"))
	if !repo.HasAnyError() {
		t.Fatalf("expected error flag")
	}
	err := repo.FetchAndClearError(BackupTask)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.HasAnyError() {
		t.Fatalf("expected cleared errors")
	}

	repo.SetError(FetchTask, errors.New("fetch"))
	repo.SetError(PurgeTask, errors.New("purge"))
	all := repo.FetchAndClearErrors()
	if len(all) != 2 {
		t.Fatalf("unexpected error count: %d", len(all))
	}
	if repo.HasAnyError() {
		t.Fatalf("expected cleared errors")
	}
}

func TestEnvHelpers(t *testing.T) {
	repo := newPausedRepo(t)
	repo.SetEnv([]string{"RESTIC_REPOSITORY=/tmp/repo", "RESTIC_CACHE_DIR=/tmp/cache"})
	repo.UpdateEnvKey("RESTIC_PASSWORD", "secret")
	if got := repo.GetRepoPath(); got != "/tmp/repo" {
		t.Fatalf("unexpected repo path: %s", got)
	}
	if got := repo.GetCacheDirPath(); got != "/tmp/cache" {
		t.Fatalf("unexpected cache dir: %s", got)
	}
}

func TestGenerateTaskIDAndCanFetch(t *testing.T) {
	repo := newPausedRepo(t)
	if repo.GenerateTaskID() != 1 || repo.GenerateTaskID() != 2 {
		t.Fatalf("unexpected task IDs")
	}
	repo.SetCanFetch(false)
	if repo.GetCanFetch() {
		t.Fatalf("expected CanFetch false")
	}
	repo.SetCanFetch(true)
	if !repo.GetCanFetch() {
		t.Fatalf("expected CanFetch true")
	}
}

func TestPrint(t *testing.T) {
	msgCh := make(chan sharedlog.Message, 1)
	repo := NewResticRepo(getResticBinaryPath(t), msgCh, 42)
	repo.Printf(logrus.InfoLevel, "hello %s", "world")
	select {
	case msg := <-msgCh:
		if msg.Module != 42 || !strings.Contains(msg.Text, "hello world") {
			t.Fatalf("unexpected message: %+v", msg)
		}
	default:
		t.Fatalf("expected message")
	}
}

func TestAppendAndQueueHelpers(t *testing.T) {
	repo := newPausedRepo(t)
	repo.ShutdownWorker()
	task := &ResticTask{ID: 1, Type: BackupTask}
	repo.appendTask(task)
	if len(repo.TaskQueue) != 1 || repo.TaskQueue[0].ID != 1 {
		t.Fatalf("unexpected queue state")
	}

	repo.AddFetchTask()
	if len(repo.TaskQueue) != 2 || repo.TaskQueue[1].Type != FetchTask {
		t.Fatalf("expected fetch task queued")
	}

	err := repo.AddPurgeTask(ResticPurgeOption{SnapshotID: "snap1", Prune: true}, true)
	if err != nil || !repo.NeedPurgeNow {
		t.Fatalf("expected purge now")
	}

	repo.appendPurgeTask(ResticPurgeOption{SnapshotID: "snap2", Prune: true})
	if repo.TaskQueue[len(repo.TaskQueue)-1].Type != PurgeTask {
		t.Fatalf("expected purge task")
	}

	repo.AddBackupTask("/data", []string{"tag1"})
	if repo.TaskQueue[len(repo.TaskQueue)-1].Type != BackupTask {
		t.Fatalf("expected backup task")
	}

	repo.AddUnlockTask()
	if repo.TaskQueue[len(repo.TaskQueue)-1].Type != UnlockTask {
		t.Fatalf("expected unlock task")
	}
}

func TestMoveTaskHelpers(t *testing.T) {
	repo := newPausedRepo(t)
	repo.TaskQueue = []*ResticTask{
		{ID: 1, Type: FetchTask},
		{ID: 2, Type: BackupTask},
		{ID: 3, Type: PurgeTask},
	}
	if err := repo.MoveTask("last", 1, 0); err != nil {
		t.Fatalf("move last: %v", err)
	}
	if repo.TaskQueue[2].ID != 1 {
		t.Fatalf("expected task 1 last")
	}

	if err := repo.MoveTask("first", 3, 0); err != nil {
		t.Fatalf("move first: %v", err)
	}
	if repo.TaskQueue[0].ID != 3 {
		t.Fatalf("expected task 3 first")
	}

	if err := repo.MoveTask("after", 1, 3); err != nil {
		t.Fatalf("move after: %v", err)
	}
	if repo.TaskQueue[1].ID != 1 {
		t.Fatalf("expected task 1 after task 3")
	}
}

func TestHasFetchQueueCancelClear(t *testing.T) {
	repo := newPausedRepo(t)
	repo.TaskQueue = []*ResticTask{
		{ID: 0, Type: FetchTask},
		{ID: 1, Type: BackupTask},
	}
	if !repo.HasFetchQueue() {
		t.Fatalf("expected fetch task")
	}
	repo.CancelTask(0)
	if len(repo.TaskQueue) != 1 || repo.TaskQueue[0].ID != 1 {
		t.Fatalf("unexpected queue after cancel")
	}
	repo.ClearQueue()
	if len(repo.TaskQueue) != 0 {
		t.Fatalf("expected queue cleared")
	}
}

func TestShutdownWorker(t *testing.T) {
	repo := NewResticRepo(getResticBinaryPath(t), testMsgChan, 0)
	repo.ShutdownWorker()
	if !repo.Shutdown {
		t.Fatalf("expected shutdown flag set")
	}
}

func TestCheckRepoFiles(t *testing.T) {
	repo := newPausedRepo(t)
	repoDir, _, _ := getTestingDirs(t)
	resetSharedDirs(t)
	repo.SetEnv([]string{
		"RESTIC_PASSWORD=testpassword",
		"RESTIC_REPOSITORY=" + repoDir,
	})

	if err := repo.CheckRepoFiles(); err != nil {
		t.Fatalf("expected init on missing config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "config")); err != nil {
		t.Fatalf("expected config after init: %v", err)
	}

	if err := os.Remove(filepath.Join(repoDir, "config")); err != nil {
		t.Fatalf("remove config: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "data"), 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := repo.CheckRepoFiles(); err == nil {
		t.Fatalf("expected error when config missing but data exists")
	}
}

func TestRunCommandAndInitRepo(t *testing.T) {
	repo, repoDir, _, _ := newResticRepo(t, false)
	if _, err := os.Stat(filepath.Join(repoDir, "config")); err != nil {
		t.Fatalf("expected config: %v", err)
	}

	stdout, stderr, err := repo.RunCommand([]string{"stats", "--mode", "raw-data", "--json"}, logrus.InfoLevel, true)
	if err != nil {
		t.Fatalf("run command: %v", err)
	}
	if len(stderr) != 0 || !strings.Contains(string(stdout), "\"total_size\"") {
		t.Fatalf("unexpected output: stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestFetchRepoStatSnapshotsAndFetchRepo(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)

	if err := repo.fetchRepoStat(); err != nil {
		t.Fatalf("fetch stat: %v", err)
	}
	if err := repo.fetchRepoSnapshots(); err != nil {
		t.Fatalf("fetch snapshots: %v", err)
	}
	if len(repo.Backups) == 0 {
		t.Fatalf("expected snapshots")
	}
	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo: %v", err)
	}
}

func TestPurgeAndBackupCommands(t *testing.T) {
	repo, _, _, dataDir := newResticRepo(t, true)
	if len(repo.Backups) == 0 {
		t.Fatalf("expected snapshot")
	}

	snapshotID := repo.Backups[0].Id
	if err := repo.purgeSingleSnapshot(snapshotID); err != nil {
		t.Fatalf("purge single: %v", err)
	}

	if _, err := repo.Backup(dataDir, []string{"tag1"}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if err := repo.purgeWithPolicy(ResticPurgeOption{KeepLast: 1, Prune: true}); err != nil {
		t.Fatalf("purge policy: %v", err)
	}

	if err := repo.PurgeRepo(ResticPurgeOption{SnapshotID: snapshotID, Prune: true}); err != nil {
		t.Fatalf("purge repo: %v", err)
	}
}

func TestCheckResticLocksAndUnlock(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)

	if err := repo.CheckResticLocks(); err != nil {
		t.Fatalf("expected no locks: %v", err)
	}

	err := repo.UnlockRepo()
	if err != nil {
		if strings.Contains(err.Error(), "no locks") {
			t.Skip("restic unlock reports no locks")
		}
		t.Fatalf("unlock repo: %v", err)
	}
}

func TestKeyManagementAndPassword(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, false)

	_, cacheDir, _ := getTestingDirs(t)
	newpassfile := filepath.Join(cacheDir, "newpass.txt")
	if err := os.WriteFile(newpassfile, []byte("newpass"), 0600); err != nil {
		t.Fatalf("write new pass: %v", err)
	}

	if err := repo.AddRepoKey(newpassfile); err != nil {
		t.Fatalf("add repo key: %v", err)
	}

	keys, err := repo.GetRepoKeyList()
	if err != nil || len(keys) == 0 {
		t.Fatalf("unexpected keys: %+v err=%v", keys, err)
	}

	removeID := keys[0].Id
	if len(keys) > 1 {
		for _, key := range keys {
			if !key.Current {
				removeID = key.Id
				break
			}
		}
	}
	if err := repo.RemoveRepoKey(removeID); err != nil {
		t.Fatalf("remove repo key: %v", err)
	}

	if err := repo.TestPassword("testpassword"); err != nil {
		t.Fatalf("test password: %v", err)
	}
}

func TestPurgeOldestBackup(t *testing.T) {
	repo := newPausedRepo(t)
	if err := repo.PurgeOldestBackup(); err == nil {
		t.Fatalf("expected error without snapshots")
	}

	repo.Backups = []BackupSnapshot{
		{Id: "snap1", Time: "2025-01-02T00:00:00Z"},
		{Id: "snap2", Time: "2025-01-01T00:00:00Z"},
	}
	if err := repo.PurgeOldestBackup(); err != nil {
		t.Fatalf("purge oldest: %v", err)
	}
	if !repo.NeedPurgeNow || repo.PurgeNowOption.SnapshotID != "snap2" {
		t.Fatalf("expected purge now for snap2")
	}
}

func TestRestoreDumpMountUnmount(t *testing.T) {
	repo, _, _, dataDir := newResticRepo(t, true)
	if len(repo.Backups) == 0 {
		t.Fatalf("expected snapshot")
	}

	snapshotID := repo.Backups[0].Id
	_, _, sharedData := getTestingDirs(t)
	targetDir := filepath.Join(sharedData, "restore")
	if err := os.RemoveAll(targetDir); err != nil {
		t.Fatalf("remove restore dir: %v", err)
	}
	if err := repo.RestoreSnapshot(snapshotID, targetDir, []string{dataDir}, ""); err != nil {
		t.Fatalf("restore snapshot: %v", err)
	}

	filePath := filepath.Join(dataDir, "file.txt")
	expected := filepath.Join(targetDir, strings.TrimPrefix(filePath, string(filepath.Separator)))
	if _, err := os.Stat(expected); err != nil {
		t.Fatalf("expected restored file: %v", err)
	}

	var buf bytes.Buffer
	if err := repo.DumpSnapshot(snapshotID, filePath, &buf); err != nil {
		t.Fatalf("dump snapshot: %v", err)
	}
	if buf.String() == "" {
		t.Fatalf("expected dump output")
	}

	mountDir := filepath.Join(sharedData, "mnt")
	if err := os.RemoveAll(mountDir); err != nil {
		t.Fatalf("remove mount dir: %v", err)
	}
	err := repo.MountRepo(mountDir)
	if err != nil {
		if strings.Contains(err.Error(), "fuse") || strings.Contains(err.Error(), "operation not permitted") {
			t.Logf("mount failed (fuse): err=%s", err.Error())
			return
		}
		t.Fatalf("%s", err.Error())
	}
	waitForMountReady(t, repo, mountDir, 5*time.Second)
	var mountEntries []string
	err = filepath.WalkDir(mountDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mountEntries = append(mountEntries, path)
		return nil
	})
	if err != nil {
		_ = repo.UnmountRepo()
		t.Fatalf("walk mount dir: %v", err)
	}
	if len(mountEntries) == 0 {
		_ = repo.UnmountRepo()
		t.Fatalf("expected mounted entries")
	}
	if err := repo.UnmountRepo(); err != nil {
		if strings.Contains(err.Error(), "signal") || strings.Contains(err.Error(), "terminated") {
			return
		}
		t.Fatalf("unmount repo: %v", err)
	}
}

func TestStreamMountOutput(t *testing.T) {
	msgCh := make(chan sharedlog.Message, 2)
	repo := NewResticRepo(getResticBinaryPath(t), msgCh, 0)

	pr, pw := io.Pipe()
	go repo.streamMountOutput(pr, "[OUT] ", nil)
	_, _ = pw.Write([]byte("line1\n"))
	_ = pw.Close()

	select {
	case msg := <-msgCh:
		if !strings.Contains(msg.Text, "[OUT] line1") {
			t.Fatalf("unexpected message: %+v", msg)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected log message")
	}
}

func TestWorkerProcessesFetchTask(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)

	repo.AddFetchTask()
	waitFor(t, 2*time.Second, func() bool {
		return len(repo.Backups) > 0
	})
}

// TestPermissionConfiguration tests SetPermissions and GetPermissions methods
func TestPermissionConfiguration(t *testing.T) {
	repo := newPausedRepo(t)

	// Test default secure permissions
	dirMode, fileMode := repo.GetPermissions()
	if dirMode != 0700 {
		t.Errorf("expected default dirMode 0700, got %#o", dirMode)
	}
	if fileMode != 0600 {
		t.Errorf("expected default fileMode 0600, got %#o", fileMode)
	}

	// Test custom permissions
	repo.SetPermissions(0750, 0640)
	dirMode, fileMode = repo.GetPermissions()
	if dirMode != 0750 {
		t.Errorf("expected dirMode 0750, got %#o", dirMode)
	}
	if fileMode != 0640 {
		t.Errorf("expected fileMode 0640, got %#o", fileMode)
	}

	// Test zero values return secure defaults
	repo.DirMode = 0
	repo.FileMode = 0
	dirMode, fileMode = repo.GetPermissions()
	if dirMode != 0700 {
		t.Errorf("expected zero dirMode to return default 0700, got %#o", dirMode)
	}
	if fileMode != 0600 {
		t.Errorf("expected zero fileMode to return default 0600, got %#o", fileMode)
	}
}

// TestTimeoutConfiguration tests SetOperationTimeout and GetOperationTimeout methods
func TestTimeoutConfiguration(t *testing.T) {
	repo := newPausedRepo(t)

	// Test default timeout
	timeout := repo.GetOperationTimeout()
	if timeout != 2*time.Hour {
		t.Errorf("expected default timeout 2h, got %v", timeout)
	}

	// Test custom timeout
	customTimeout := 6 * time.Hour
	repo.SetOperationTimeout(customTimeout)
	timeout = repo.GetOperationTimeout()
	if timeout != customTimeout {
		t.Errorf("expected timeout %v, got %v", customTimeout, timeout)
	}

	// Test zero value returns default
	repo.OperationTimeout = 0
	timeout = repo.GetOperationTimeout()
	if timeout != 2*time.Hour {
		t.Errorf("expected zero timeout to return default 2h, got %v", timeout)
	}
}

// TestDirectoryPermissions tests that directories are created with correct permissions
func TestDirectoryPermissions(t *testing.T) {
	repo := newPausedRepo(t)
	resetSharedDirs(t)

	// Create a test directory for restore target
	testDir, err := os.MkdirTemp("", "restic-perm-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Set custom permissions
	repo.SetPermissions(0700, 0600)

	// Create a directory using the same pattern as restoreSnapshot
	dirMode, _ := repo.GetPermissions()
	targetDir := filepath.Join(testDir, "restore-target")
	if err := os.MkdirAll(targetDir, dirMode); err != nil {
		t.Fatalf("failed to create target dir: %v", err)
	}

	// Verify directory permissions
	info, err := os.Stat(targetDir)
	if err != nil {
		t.Fatalf("failed to stat dir: %v", err)
	}

	actualMode := info.Mode().Perm()
	if actualMode != 0700 {
		t.Errorf("expected dir permissions 0700, got %#o", actualMode)
	}
}

// TestFilePermissions tests that restored files have correct permissions set
func TestFilePermissions(t *testing.T) {
	repo := newPausedRepo(t)

	// Create a test directory with some files
	testDir, err := os.MkdirTemp("", "restic-file-perm-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	// Create test files with world-readable permissions (simulating umask behavior)
	testFile1 := filepath.Join(testDir, "file1.txt")
	testFile2 := filepath.Join(testDir, "subdir", "file2.txt")

	if err := os.MkdirAll(filepath.Dir(testFile2), 0755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}

	if err := os.WriteFile(testFile1, []byte("test1"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("test2"), 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}

	// Verify files are currently world-readable
	info1, _ := os.Stat(testFile1)
	if info1.Mode().Perm() != 0644 {
		t.Fatalf("test setup failed: expected 0644, got %#o", info1.Mode().Perm())
	}

	// Set secure permissions
	repo.SetPermissions(0700, 0600)

	// Apply permissions using setRestorePermissions
	if err := repo.setRestorePermissions(testDir); err != nil {
		t.Fatalf("setRestorePermissions failed: %v", err)
	}

	// Verify directory permissions
	dirInfo, err := os.Stat(filepath.Dir(testFile2))
	if err != nil {
		t.Fatalf("failed to stat subdir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0700 {
		t.Errorf("expected subdir permissions 0700, got %#o", dirInfo.Mode().Perm())
	}

	// Verify file permissions
	info1, err = os.Stat(testFile1)
	if err != nil {
		t.Fatalf("failed to stat file1: %v", err)
	}
	if info1.Mode().Perm() != 0600 {
		t.Errorf("expected file1 permissions 0600, got %#o", info1.Mode().Perm())
	}

	info2, err := os.Stat(testFile2)
	if err != nil {
		t.Fatalf("failed to stat file2: %v", err)
	}
	if info2.Mode().Perm() != 0600 {
		t.Errorf("expected file2 permissions 0600, got %#o", info2.Mode().Perm())
	}
}

// TestMountReady tests the isMountReady function for various scenarios
func TestMountReady(t *testing.T) {
	// Test with valid empty directory
	emptyDir, err := os.MkdirTemp("", "mount-ready-empty-*")
	if err != nil {
		t.Fatalf("failed to create empty dir: %v", err)
	}
	defer os.RemoveAll(emptyDir)

	if !isMountReady(emptyDir) {
		t.Error("isMountReady should return true for empty directory")
	}

	// Test with directory containing files
	fullDir, err := os.MkdirTemp("", "mount-ready-full-*")
	if err != nil {
		t.Fatalf("failed to create full dir: %v", err)
	}
	defer os.RemoveAll(fullDir)

	testFile := filepath.Join(fullDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if !isMountReady(fullDir) {
		t.Error("isMountReady should return true for directory with files")
	}

	// Test with non-existent path
	if isMountReady("/nonexistent/path/that/does/not/exist") {
		t.Error("isMountReady should return false for non-existent path")
	}

	// Test with file instead of directory
	tempFile, err := os.CreateTemp("", "mount-ready-file-*")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tempFilePath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempFilePath)

	if isMountReady(tempFilePath) {
		t.Error("isMountReady should return false for file path")
	}
}

// TestSetRestorePermissionsErrorHandling tests error handling in permission setting
func TestSetRestorePermissionsErrorHandling(t *testing.T) {
	repo := newPausedRepo(t)

	// Test with non-existent directory (should not return error, only warn)
	err := repo.setRestorePermissions("/nonexistent/path/for/testing")
	if err != nil {
		t.Errorf("setRestorePermissions should not return error for non-existent path, got: %v", err)
	}

	// Test with valid directory should succeed
	testDir, err := os.MkdirTemp("", "restic-perm-error-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(testDir)

	if err := repo.setRestorePermissions(testDir); err != nil {
		t.Errorf("setRestorePermissions should succeed for valid directory, got: %v", err)
	}
}

// TestOperationTimeoutIntegration tests timeout integration with context
func TestOperationTimeoutIntegration(t *testing.T) {
	repo := newPausedRepo(t)

	// Set a very short timeout
	repo.SetOperationTimeout(100 * time.Millisecond)

	// Verify timeout is set
	timeout := repo.GetOperationTimeout()
	if timeout != 100*time.Millisecond {
		t.Errorf("expected timeout 100ms, got %v", timeout)
	}

	// Note: Full integration test with actual command timeout would require
	// a mock restic binary or a slow operation, which is beyond unit test scope.
	// The timeout mechanism is tested indirectly through existing integration tests.
}

// TestMountDisabledConfiguration tests mount disable flag configuration
func TestMountDisabledConfiguration(t *testing.T) {
	repo := newPausedRepo(t)

	// Default should be false (mount enabled)
	if repo.IsMountDisabled() {
		t.Error("mount should be enabled by default")
	}

	// Enable mount disable
	repo.SetMountDisabled(true)
	if !repo.IsMountDisabled() {
		t.Error("mount should be disabled after SetMountDisabled(true)")
	}

	// Disable mount disable (enable mount)
	repo.SetMountDisabled(false)
	if repo.IsMountDisabled() {
		t.Error("mount should be enabled after SetMountDisabled(false)")
	}
}

// TestMountRepoWhenDisabled tests that MountRepo returns error when disabled
func TestMountRepoWhenDisabled(t *testing.T) {
	repo := newPausedRepo(t)

	// Disable mount operations
	repo.SetMountDisabled(true)

	// Create temporary mount directory
	tempDir, err := os.MkdirTemp("", "restic-mount-disabled-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Try to mount - should fail with clear error
	err = repo.MountRepo(tempDir)
	if err == nil {
		t.Error("MountRepo should fail when mount is disabled")
	}

	expectedMsg := "mount operations are disabled"
	if !strings.Contains(err.Error(), expectedMsg) {
		t.Errorf("error should mention mount is disabled, got: %v", err)
	}
}

// TestCheckFUSEAvailability tests FUSE availability detection
func TestCheckFUSEAvailability(t *testing.T) {
	repo := newPausedRepo(t)

	// Check FUSE availability (result depends on environment)
	available := repo.CheckFUSEAvailability()

	// We can't assert the result (depends on environment), but we can test
	// that the method doesn't panic and returns a boolean
	t.Logf("FUSE availability on this system: %v", available)

	// The method should consistently return the same result
	available2 := repo.CheckFUSEAvailability()
	if available != available2 {
		t.Error("CheckFUSEAvailability should return consistent results")
	}
}

// TestAutoDetectAndDisableMount tests automatic FUSE detection and mount disable
func TestAutoDetectAndDisableMount(t *testing.T) {
	repo := newPausedRepo(t)

	// Run auto-detection
	wasDisabled := repo.AutoDetectAndDisableMount()

	// If FUSE is not available, mount should be disabled
	if !repo.CheckFUSEAvailability() {
		if !repo.IsMountDisabled() {
			t.Error("mount should be disabled when FUSE is not available")
		}
		if !wasDisabled {
			t.Error("AutoDetectAndDisableMount should return true when disabling mount")
		}
	} else {
		// If FUSE is available, mount should still be enabled
		if repo.IsMountDisabled() {
			t.Error("mount should not be disabled when FUSE is available")
		}
		if wasDisabled {
			t.Error("AutoDetectAndDisableMount should return false when FUSE is available")
		}
	}

	t.Logf("Mount disabled: %v (FUSE available: %v)", repo.IsMountDisabled(), repo.CheckFUSEAvailability())
}

// TestMountDisabledWorkflow tests complete workflow with mount disabled
func TestMountDisabledWorkflow(t *testing.T) {
	repo := newPausedRepo(t)

	// Simulate environment without FUSE
	repo.SetMountDisabled(true)

	// Create temporary directory
	tempDir, err := os.MkdirTemp("", "restic-workflow-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Verify mount operations fail gracefully
	err = repo.MountRepo(tempDir)
	if err == nil {
		t.Fatal("MountRepo should fail when disabled")
	}
	if !strings.Contains(err.Error(), "disabled") {
		t.Errorf("error should mention disabled, got: %v", err)
	}

	// Verify other operations still work (e.g., permission configuration)
	repo.SetPermissions(0750, 0640)
	dirMode, fileMode := repo.GetPermissions()
	if dirMode != 0750 || fileMode != 0640 {
		t.Errorf("permission configuration should work independently of mount status")
	}

	// Re-enable mount
	repo.SetMountDisabled(false)

	// Mount should be allowed now (even if it fails due to missing FUSE/restic binary)
	// We just verify it doesn't fail with "disabled" error
	err = repo.MountRepo(tempDir)
	// Error is expected (no restic binary), but shouldn't be "disabled" error
	if err != nil && strings.Contains(err.Error(), "operations are disabled") {
		t.Errorf("MountRepo should not fail with 'disabled' error when enabled, got: %v", err)
	}
}
