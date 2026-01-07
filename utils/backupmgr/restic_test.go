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
		if err := repo.Backup(dataDir, []string{"tag1"}); err != nil {
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

	err := repo.AddPurgeTask(ResticPurgeOption{SnapshotID: "snap1"}, true)
	if err != nil || !repo.NeedPurgeNow {
		t.Fatalf("expected purge now")
	}

	repo.appendPurgeTask(ResticPurgeOption{SnapshotID: "snap2"})
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

	if err := repo.Backup(dataDir, []string{"tag1"}); err != nil {
		t.Fatalf("backup: %v", err)
	}

	if err := repo.purgeWithPolicy(ResticPurgeOption{KeepLast: 1}); err != nil {
		t.Fatalf("purge policy: %v", err)
	}

	if err := repo.PurgeRepo(ResticPurgeOption{SnapshotID: snapshotID}); err != nil {
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
	if err := repo.RestoreSnapshot(snapshotID, targetDir, []string{dataDir}); err != nil {
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
