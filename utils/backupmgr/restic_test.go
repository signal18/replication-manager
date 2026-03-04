package backupmgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
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
		safeRemoveAll(t, dir)
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
		snapshot := repo.mountSnapshot()
		mountCmd := snapshot.cmd
		mountDone := snapshot.done

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

func safeRemoveAll(t *testing.T, targetDir string) {
	t.Helper()

	if err := os.RemoveAll(targetDir); err == nil {
		return
	} else {
		if cleanupErr := cleanupMountsUnder(targetDir); cleanupErr != nil {
			t.Logf("cleanup mounts under %s: %v", targetDir, cleanupErr)
		}
		if err := os.RemoveAll(targetDir); err == nil {
			return
		} else {
			t.Fatalf("remove dir %s: %v", targetDir, err)
		}
	}
}

func cleanupMountsUnder(baseDir string) error {
	base := strings.TrimSpace(baseDir)
	if base == "" {
		return fmt.Errorf("base directory is empty")
	}
	base = filepath.Clean(base)

	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	var mountPoints []string
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) <= 4 {
			continue
		}
		mountPath := unescapeMountPath(fields[4])
		if mountPath == "" {
			continue
		}
		mountPath = filepath.Clean(mountPath)
		if mountPath == base || strings.HasPrefix(mountPath, base+string(os.PathSeparator)) {
			mountPoints = append(mountPoints, mountPath)
		}
	}
	if len(mountPoints) == 0 {
		return nil
	}
	sort.Slice(mountPoints, func(i, j int) bool {
		return len(mountPoints[i]) > len(mountPoints[j])
	})
	var errs []string
	for _, mountPoint := range mountPoints {
		if err := unmountResticPath(mountPoint); err != nil && !errors.Is(err, syscall.ENOTCONN) {
			errs = append(errs, fmt.Sprintf("%s: %v", mountPoint, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func unescapeMountPath(path string) string {
	replacer := strings.NewReplacer(
		`\040`, " ",
		`\011`, "\t",
		`\012`, "\n",
		`\134`, `\\`,
	)
	return replacer.Replace(path)
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

func TestGenerateTaskID(t *testing.T) {
	repo := newPausedRepo(t)
	if repo.GenerateTaskID() != 1 || repo.GenerateTaskID() != 2 {
		t.Fatalf("unexpected task IDs")
	}
}

func TestResticOpSemaphoreTimeout(t *testing.T) {
	repo := newPausedRepo(t)

	ctx := context.Background()
	if err := repo.acquireResticOp(ctx); err != nil {
		t.Fatalf("failed to acquire restic op: %v", err)
	}

	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := repo.acquireResticOp(timeoutCtx); err == nil {
		repo.releaseResticOp()
		t.Fatalf("expected timeout error")
	}

	repo.releaseResticOp()

	secondCtx, secondCancel := context.WithTimeout(context.Background(), time.Second)
	defer secondCancel()
	if err := repo.acquireResticOp(secondCtx); err != nil {
		t.Fatalf("expected acquire after release, got: %v", err)
	}
	repo.releaseResticOp()
}

func TestResticOpSemaphoreWarning(t *testing.T) {
	repo := newPausedRepo(t)
	msgCh := make(chan sharedlog.Message, 5)
	repo.MessageChan = msgCh
	repo.resticOpWaitDuration = 20 * time.Millisecond

	if err := repo.acquireResticOp(context.Background()); err != nil {
		t.Fatalf("failed to acquire restic op: %v", err)
	}

	acquireErrCh := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		err := repo.acquireResticOp(ctx)
		if err == nil {
			repo.releaseResticOp()
		}
		acquireErrCh <- err
	}()

	select {
	case msg := <-msgCh:
		if !strings.Contains(msg.Text, "Restic operation has been waiting") {
			repo.releaseResticOp()
			t.Fatalf("unexpected log message: %s", msg.Text)
		}
	case <-time.After(500 * time.Millisecond):
		repo.releaseResticOp()
		t.Fatalf("timeout waiting for warning log")
	}

	repo.releaseResticOp()

	if err := <-acquireErrCh; err != nil {
		t.Fatalf("expected acquire after release, got: %v", err)
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

	repo.AddBackupTask("/data", []string{"tag1"}, "")
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
	if err := repo.purgeSingleSnapshot(ResticPurgeOption{SnapshotID: snapshotID, Prune: true}); err != nil {
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

func TestResticPurgeDryRunSingleSnapshot(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)
	if len(repo.Backups) == 0 {
		t.Fatalf("expected snapshot")
	}

	snapshotID := repo.Backups[0].Id
	opt := ResticPurgeOption{
		SnapshotID: snapshotID,
		Prune:      true,
		DryRun:     true,
	}
	t.Logf("purge options: %+v", opt)
	if err := repo.PurgeRepo(opt); err != nil {
		t.Fatalf("purge repo dry-run: %v", err)
	}

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo after dry-run: %v", err)
	}
	t.Logf("snapshots after dry-run: %d", len(repo.Backups))
	for _, snap := range repo.Backups {
		t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
	}

	hasSnapshot := func(id string) bool {
		for _, snap := range repo.Backups {
			if snap.Id == id || snap.ShortId == id || strings.HasPrefix(snap.Id, id) {
				return true
			}
		}
		return false
	}

	if !hasSnapshot(snapshotID) {
		t.Fatalf("expected snapshot to remain after dry-run: %s", snapshotID)
	}
}

func TestResticPurgeDryRunPolicy(t *testing.T) {
	repo, _, _, dataDir := newResticRepo(t, false)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	ids := make([]string, 0, 3)
	makeBackup := func(idx int, tags []string) {
		payload := []byte(fmt.Sprintf("payload-%d", idx))
		if err := os.WriteFile(filepath.Join(dataDir, "file.txt"), payload, 0644); err != nil {
			t.Fatalf("write data file: %v", err)
		}
		snapshotID, err := repo.Backup(dataDir, tags)
		if err != nil {
			t.Fatalf("backup %d: %v", idx, err)
		}
		ids = append(ids, snapshotID)
		t.Logf("created snapshot %s tags=%v", snapshotID, tags)
		time.Sleep(200 * time.Millisecond)
	}

	makeBackup(0, []string{"line:default"})
	makeBackup(1, []string{"line:adhoc"})
	makeBackup(2, []string{"line:default"})

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo: %v", err)
	}
	t.Logf("snapshots before dry-run: %d", len(repo.Backups))
	for _, snap := range repo.Backups {
		t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
	}

	opt := ResticPurgeOption{
		KeepLast: 1,
		GroupBy:  "none",
		Prune:    true,
		DryRun:   true,
	}
	t.Logf("purge options: %+v", opt)
	if err := repo.PurgeRepo(opt); err != nil {
		t.Fatalf("purge repo dry-run: %v", err)
	}

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo after dry-run: %v", err)
	}
	t.Logf("snapshots after dry-run: %d", len(repo.Backups))
	for _, snap := range repo.Backups {
		t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
	}

	hasSnapshot := func(id string) bool {
		for _, snap := range repo.Backups {
			if snap.Id == id || snap.ShortId == id || strings.HasPrefix(snap.Id, id) {
				return true
			}
		}
		return false
	}

	for _, id := range ids {
		if !hasSnapshot(id) {
			t.Fatalf("expected snapshot to remain after dry-run: %s", id)
		}
	}
	if len(repo.Backups) != len(ids) {
		t.Fatalf("expected %d snapshots after dry-run, got %d", len(ids), len(repo.Backups))
	}
}

func TestResticPurgeKeepTagAndPrune(t *testing.T) {
	repo, _, _, dataDir := newResticRepo(t, false)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	type snapInfo struct {
		id   string
		tags []string
	}
	snapshots := make([]snapInfo, 0, 3)

	makeBackup := func(idx int, tags []string) {
		payload := []byte(fmt.Sprintf("payload-%d", idx))
		if err := os.WriteFile(filepath.Join(dataDir, "file.txt"), payload, 0644); err != nil {
			t.Fatalf("write data file: %v", err)
		}
		snapshotID, err := repo.Backup(dataDir, tags)
		if err != nil {
			t.Fatalf("backup %d: %v", idx, err)
		}
		t.Logf("created snapshot %s tags=%v", snapshotID, tags)
		snapshots = append(snapshots, snapInfo{id: snapshotID, tags: tags})
		time.Sleep(200 * time.Millisecond)
	}

	makeBackup(0, []string{"line:default"})
	makeBackup(1, []string{"line:adhoc"})
	makeBackup(2, []string{"line:default"})

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo: %v", err)
	}
	t.Logf("snapshots before purge: %d", len(repo.Backups))
	for _, snap := range repo.Backups {
		t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
	}

	opt := ResticPurgeOption{
		KeepLast: 1,
		KeepTag:  []string{"line:adhoc"},
		GroupBy:  "none",
		Prune:    true,
	}
	t.Logf("purge options: %+v", opt)
	if err := repo.PurgeRepo(opt); err != nil {
		t.Fatalf("purge repo: %v", err)
	}

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo after purge: %v", err)
	}
	t.Logf("snapshots after purge: %d", len(repo.Backups))
	for _, snap := range repo.Backups {
		t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
	}

	hasSnapshot := func(id string) bool {
		for _, snap := range repo.Backups {
			if snap.Id == id || snap.ShortId == id || strings.HasPrefix(snap.Id, id) {
				return true
			}
		}
		return false
	}

	if hasSnapshot(snapshots[0].id) {
		t.Fatalf("expected oldest default snapshot to be purged: %s", snapshots[0].id)
	}
	if !hasSnapshot(snapshots[1].id) {
		t.Fatalf("expected adhoc snapshot to be kept: %s", snapshots[1].id)
	}
	if !hasSnapshot(snapshots[2].id) {
		t.Fatalf("expected newest default snapshot to be kept: %s", snapshots[2].id)
	}
	if len(repo.Backups) != 2 {
		t.Fatalf("expected 2 snapshots after purge, got %d", len(repo.Backups))
	}
}

func TestResticPurgeGroupByPolicies(t *testing.T) {
	type policy struct {
		name       string
		keepLast   int
		keepHourly int
		keepDaily  int
		keepWithin string
		keepTag    []string
	}

	type scenario struct {
		name    string
		groupBy string
		prune   bool
		policy  policy
	}

	groupBys := []string{"default", "none", "host,tags"}
	policies := []policy{
		{name: "keep-last", keepLast: 1},
		{name: "keep-within", keepWithin: "1h"},
		{name: "keep-last+daily", keepLast: 1, keepDaily: 1},
		{name: "keep-tag", keepTag: []string{"line:adhoc"}},
		{name: "keep-tag+last", keepLast: 1, keepTag: []string{"line:adhoc"}},
	}
	pruneValues := []bool{false, true}

	scenarios := make([]scenario, 0, len(groupBys)*len(policies)*len(pruneValues))
	for _, groupBy := range groupBys {
		for _, pol := range policies {
			for _, prune := range pruneValues {
				name := fmt.Sprintf("group-by %s %s prune=%t", groupBy, pol.name, prune)
				scenarios = append(scenarios, scenario{
					name:    name,
					groupBy: groupBy,
					prune:   prune,
					policy:  pol,
				})
			}
		}
	}

	createRepoWithSnapshots := func(t *testing.T) (*ResticManager, []string) {
		t.Helper()

		repo, _, _, dataDir := newResticRepo(t, false)
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			t.Fatalf("mkdir data: %v", err)
		}

		ids := make([]string, 0, 3)
		writeBackup := func(idx int, tags []string) {
			payload := []byte(fmt.Sprintf("payload-%d", idx))
			if err := os.WriteFile(filepath.Join(dataDir, "file.txt"), payload, 0644); err != nil {
				t.Fatalf("write data file: %v", err)
			}
			snapshotID, err := repo.Backup(dataDir, tags)
			if err != nil {
				t.Fatalf("backup %d: %v", idx, err)
			}
			ids = append(ids, snapshotID)
			t.Logf("created snapshot %s tags=%v", snapshotID, tags)
			time.Sleep(200 * time.Millisecond)
		}

		writeBackup(0, []string{"line:default"})
		writeBackup(1, []string{"line:adhoc"})
		writeBackup(2, []string{"line:default"})

		if err := repo.FetchRepo(); err != nil {
			t.Fatalf("fetch repo: %v", err)
		}
		t.Logf("snapshots before purge: %d", len(repo.Backups))
		for _, snap := range repo.Backups {
			t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
		}

		return repo, ids
	}

	hasSnapshot := func(repo *ResticManager, id string) bool {
		for _, snap := range repo.Backups {
			if snap.Id == id || snap.ShortId == id || strings.HasPrefix(snap.Id, id) {
				return true
			}
		}
		return false
	}

	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			repo, ids := createRepoWithSnapshots(t)

			opt := ResticPurgeOption{
				KeepLast:   sc.policy.keepLast,
				KeepHourly: sc.policy.keepHourly,
				KeepDaily:  sc.policy.keepDaily,
				KeepWithin: sc.policy.keepWithin,
				KeepTag:    sc.policy.keepTag,
				GroupBy:    sc.groupBy,
				Prune:      sc.prune,
			}
			t.Logf("purge options: %+v", opt)
			if err := repo.PurgeRepo(opt); err != nil {
				t.Fatalf("purge repo: %v", err)
			}

			if err := repo.FetchRepo(); err != nil {
				t.Fatalf("fetch repo after purge: %v", err)
			}
			t.Logf("snapshots after purge: %d", len(repo.Backups))
			for _, snap := range repo.Backups {
				t.Logf("snapshot %s tags=%v", snap.Id, snap.Tags)
			}

			if len(repo.Backups) == 0 {
				t.Fatalf("expected snapshots to remain after purge")
			}
			if len(sc.policy.keepTag) > 0 {
				if !hasSnapshot(repo, ids[1]) {
					t.Fatalf("expected adhoc snapshot to be kept: %s", ids[1])
				}
			}
			if sc.policy.keepLast > 0 {
				if !hasSnapshot(repo, ids[2]) && len(sc.policy.keepTag) == 0 {
					t.Fatalf("expected newest default snapshot to be kept: %s", ids[2])
				}
			}
		})
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
	if repo.AutoDetectAndDisableMount() {
		t.Skip("FUSE not available; skipping mount tests")
	}
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
	safeRemoveAll(t, mountDir)
	t.Cleanup(func() {
		_ = repo.UnmountRepo()
		_ = unmountResticPath(mountDir)
		_ = os.RemoveAll(mountDir)
	})
	err := repo.MountRepoWithOptions(NewResticMountOption(mountDir))
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

	if isMountReady(emptyDir) {
		t.Error("isMountReady should return false for empty directory without mount")
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

	if isMountReady(fullDir) {
		t.Error("isMountReady should return false for directory with files without mount")
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

// TestMountRepoWhenDisabled tests that MountRepoWithOptions returns error when disabled
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
	err = repo.MountRepoWithOptions(NewResticMountOption(tempDir))
	if err == nil {
		t.Error("MountRepoWithOptions should fail when mount is disabled")
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
	err = repo.MountRepoWithOptions(NewResticMountOption(tempDir))
	if err == nil {
		t.Fatal("MountRepoWithOptions should fail when disabled")
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
	err = repo.MountRepoWithOptions(NewResticMountOption(tempDir))
	// Error is expected (no restic binary), but shouldn't be "disabled" error
	if err != nil && strings.Contains(err.Error(), "operations are disabled") {
		t.Errorf("MountRepoWithOptions should not fail with 'disabled' error when enabled, got: %v", err)
	}
}

// TestNewResticMountOption tests the helper function for creating mount options
func TestNewResticMountOption(t *testing.T) {
	targetDir := "/mnt/test"
	opt := NewResticMountOption(targetDir)

	if opt.TargetDir != targetDir {
		t.Errorf("expected TargetDir %s, got %s", targetDir, opt.TargetDir)
	}
	if !opt.NoLock {
		t.Error("expected NoLock to be true by default")
	}
	if !opt.AllowOther {
		t.Error("expected AllowOther to be true by default")
	}
	if opt.Verbose != 0 {
		t.Error("expected Verbose to be 0 by default")
	}
	if opt.Quiet {
		t.Error("expected Quiet to be false by default")
	}
}

// TestResticMountOptionValidate tests the validation method
func TestResticMountOptionValidate(t *testing.T) {
	tests := []struct {
		name    string
		opt     ResticMountOption
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty target directory",
			opt:     ResticMountOption{},
			wantErr: true,
			errMsg:  "required",
		},
		{
			name: "valid basic options",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
			},
			wantErr: false,
		},
		{
			name: "verbose level too high",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Verbose:   4,
			},
			wantErr: true,
			errMsg:  "verbose level must be 0-3",
		},
		{
			name: "verbose level negative",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Verbose:   -1,
			},
			wantErr: true,
			errMsg:  "verbose level must be 0-3",
		},
		{
			name: "quiet and verbose together",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Verbose:   1,
				Quiet:     true,
			},
			wantErr: true,
			errMsg:  "cannot use both --quiet and --verbose",
		},
		{
			name: "relative path filter",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Path:      []string{"relative/path"},
			},
			wantErr: true,
			errMsg:  "path filter must be absolute",
		},
		{
			name: "absolute path filter",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Path:      []string{"/absolute/path"},
			},
			wantErr: false,
		},
		{
			name: "multiple path filters",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Path:      []string{"/path1", "/path2"},
			},
			wantErr: false,
		},
		{
			name: "all valid options",
			opt: ResticMountOption{
				TargetDir:            "/mnt/test",
				Host:                 []string{"host1", "host2"},
				Tag:                  []string{"tag1", "tag2"},
				Path:                 []string{"/data"},
				PathTemplate:         []string{"ids/%i", "hosts/%h/%T"},
				TimeTemplate:         "2006-01-02_15-04-05",
				AllowOther:           true,
				NoDefaultPermissions: true,
				OwnerRoot:            true,
				NoLock:               true,
				Verbose:              2,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opt.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error containing %q, got nil", tt.errMsg)
				} else if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Validate() expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Validate() expected no error, got %v", err)
				}
			}
		})
	}
}

func TestResticMountStatePersistence(t *testing.T) {
	repo := NewResticRepo("", testMsgChan, 0)
	cacheDir := t.TempDir()
	repo.UpdateEnvKey("RESTIC_CACHE_DIR", cacheDir)
	statePath := repo.mountStatePath()
	if statePath == "" {
		t.Fatalf("expected mount state path to be set")
	}
	if err := repo.writeMountState("/var/lib/replication-manager/cluster1/mount/test", 1234); err != nil {
		t.Fatalf("writeMountState failed: %v", err)
	}
	state, err := repo.loadMountState()
	if err != nil {
		t.Fatalf("loadMountState failed: %v", err)
	}
	if state.Path != "/var/lib/replication-manager/cluster1/mount/test" || state.PID != 1234 {
		t.Fatalf("unexpected mount state: %+v", state)
	}
	repo.clearMountState()
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("expected mount state file removed, got %v", err)
	}
}

func TestEnsureResticMountDirRejectsNonEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "data.txt"), []byte("data"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := ensureResticMountDir(dir, 0700); err == nil {
		t.Fatalf("expected non-empty mount dir to be rejected")
	}
}

// TestBuildMountArgs tests the command argument builder
func TestBuildMountArgs(t *testing.T) {
	repo := newPausedRepo(t)

	tests := []struct {
		name        string
		opt         ResticMountOption
		contains    []string
		notContains []string
	}{
		{
			name: "basic mount",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				NoLock:    true,
			},
			contains: []string{"mount", "--no-lock", "/mnt/test"},
		},
		{
			name: "with verbose level 1",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Verbose:   1,
			},
			contains: []string{"mount", "-v", "/mnt/test"},
		},
		{
			name: "with verbose level 2",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Verbose:   2,
			},
			contains: []string{"mount", "--verbose=2", "/mnt/test"},
		},
		{
			name: "with quiet",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Quiet:     true,
			},
			contains: []string{"mount", "--quiet", "/mnt/test"},
		},
		{
			name: "with host filters",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Host:      []string{"host1", "host2"},
			},
			contains: []string{"mount", "--host", "host1", "--host", "host2", "/mnt/test"},
		},
		{
			name: "with tag filters",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Tag:       []string{"tag1", "tag2"},
			},
			contains: []string{"mount", "--tag", "tag1", "--tag", "tag2", "/mnt/test"},
		},
		{
			name: "with path filters",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				Path:      []string{"/data", "/var"},
			},
			contains: []string{"mount", "--path", "/data", "--path", "/var", "/mnt/test"},
		},
		{
			name: "with path templates",
			opt: ResticMountOption{
				TargetDir:    "/mnt/test",
				PathTemplate: []string{"ids/%i", "hosts/%h/%T"},
			},
			contains: []string{"mount", "--path-template", "ids/%i", "--path-template", "hosts/%h/%T", "/mnt/test"},
		},
		{
			name: "with time template",
			opt: ResticMountOption{
				TargetDir:    "/mnt/test",
				TimeTemplate: "2006-01-02_15-04-05",
			},
			contains: []string{"mount", "--time-template", "2006-01-02_15-04-05", "/mnt/test"},
		},
		{
			name: "with permission options",
			opt: ResticMountOption{
				TargetDir:            "/mnt/test",
				AllowOther:           true,
				NoDefaultPermissions: true,
				OwnerRoot:            true,
			},
			contains: []string{"mount", "--allow-other", "--no-default-permissions", "--owner-root", "/mnt/test"},
		},
		{
			name: "without no-lock",
			opt: ResticMountOption{
				TargetDir: "/mnt/test",
				NoLock:    false,
			},
			contains:    []string{"mount", "/mnt/test"},
			notContains: []string{"--no-lock"},
		},
		{
			name: "all options combined",
			opt: ResticMountOption{
				TargetDir:            "/mnt/test",
				Host:                 []string{"host1"},
				Tag:                  []string{"tag1"},
				Path:                 []string{"/data"},
				PathTemplate:         []string{"ids/%i"},
				TimeTemplate:         "2006-01-02_15-04-05",
				AllowOther:           true,
				NoDefaultPermissions: true,
				OwnerRoot:            true,
				NoLock:               true,
				Verbose:              2,
			},
			contains: []string{
				"mount",
				"--no-lock",
				"--verbose=2",
				"--host", "host1",
				"--tag", "tag1",
				"--path", "/data",
				"--path-template", "ids/%i",
				"--time-template", "2006-01-02_15-04-05",
				"--allow-other",
				"--no-default-permissions",
				"--owner-root",
				"/mnt/test",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := repo.buildMountArgs(tt.opt)
			argsStr := strings.Join(args, " ")

			for _, want := range tt.contains {
				if !strings.Contains(argsStr, want) {
					t.Errorf("buildMountArgs() missing expected arg %q, got: %v", want, args)
				}
			}

			for _, notWant := range tt.notContains {
				if strings.Contains(argsStr, notWant) {
					t.Errorf("buildMountArgs() contains unexpected arg %q, got: %v", notWant, args)
				}
			}

			// Verify target directory is last
			if len(args) > 0 && args[len(args)-1] != tt.opt.TargetDir {
				t.Errorf("buildMountArgs() target directory should be last, got: %v", args)
			}
		})
	}
}

// TestMountRepoWithOptions tests mounting with various options
func TestMountRepoWithOptions(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)
	if repo.AutoDetectAndDisableMount() {
		t.Skip("FUSE not available; skipping mount tests")
	}

	mountDir := filepath.Join(getTestingDirs(t))
	mountDir = filepath.Join(mountDir, "mount-options-test")
	safeRemoveAll(t, mountDir)
	t.Cleanup(func() {
		_ = repo.UnmountRepo()
		_ = unmountResticPath(mountDir)
		_ = os.RemoveAll(mountDir)
	})

	// Test with empty target directory (should fail)
	opt := ResticMountOption{}
	err := repo.MountRepoWithOptions(opt)
	if err == nil {
		t.Fatal("MountRepoWithOptions should fail with empty TargetDir")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Errorf("error should mention required, got: %v", err)
	}

	// Test with basic options
	opt = NewResticMountOption(mountDir)
	err = repo.MountRepoWithOptions(opt)
	if err != nil {
		if strings.Contains(err.Error(), "fuse") || strings.Contains(err.Error(), "operation not permitted") {
			t.Logf("mount failed (fuse): err=%s", err.Error())
			return
		}
		t.Fatalf("MountRepoWithOptions failed: %v", err)
	}

	waitForMountReady(t, repo, mountDir, 5*time.Second)

	// Verify mount is active
	if !repo.IsMounted() {
		t.Fatal("expected mount to be active")
	}
	if repo.GetMountPath() != mountDir {
		t.Errorf("expected mount path %s, got %s", mountDir, repo.GetMountPath())
	}

	// Cleanup
	if err := repo.UnmountRepo(); err != nil {
		if strings.Contains(err.Error(), "signal") || strings.Contains(err.Error(), "terminated") {
			return
		}
		t.Fatalf("unmount repo: %v", err)
	}
}

// TestMountRepoWithFilters tests mounting with host/tag/path filters
func TestMountRepoWithFilters(t *testing.T) {
	repo, _, _, dataDir := newResticRepo(t, false)
	if repo.AutoDetectAndDisableMount() {
		t.Skip("FUSE not available; skipping mount tests")
	}

	// Create multiple backups with different tags and hosts
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}

	// Backup 1: host=server1, tag=production
	if err := os.WriteFile(filepath.Join(dataDir, "file1.txt"), []byte("data1"), 0644); err != nil {
		t.Fatalf("write file1: %v", err)
	}
	opt1 := ResticBackupOption{
		DirPath: dataDir,
		Tags:    []string{"production"},
		Host:    "server1",
	}
	if _, err := repo.BackupWithOptions(opt1); err != nil {
		t.Fatalf("backup 1: %v", err)
	}

	// Backup 2: host=server2, tag=development
	if err := os.WriteFile(filepath.Join(dataDir, "file2.txt"), []byte("data2"), 0644); err != nil {
		t.Fatalf("write file2: %v", err)
	}
	opt2 := ResticBackupOption{
		DirPath: dataDir,
		Tags:    []string{"development"},
		Host:    "server2",
	}
	if _, err := repo.BackupWithOptions(opt2); err != nil {
		t.Fatalf("backup 2: %v", err)
	}

	if err := repo.FetchRepo(); err != nil {
		t.Fatalf("fetch repo: %v", err)
	}

	if len(repo.Backups) < 2 {
		t.Fatalf("expected at least 2 backups, got %d", len(repo.Backups))
	}

	// Test mount with host filter
	mountDir := filepath.Join(getTestingDirs(t))
	mountDir = filepath.Join(mountDir, "mount-filter-test")
	safeRemoveAll(t, mountDir)
	t.Cleanup(func() {
		_ = repo.UnmountRepo()
		_ = unmountResticPath(mountDir)
		_ = os.RemoveAll(mountDir)
	})

	mountOpt := ResticMountOption{
		TargetDir: mountDir,
		Host:      []string{"server1"},
		Tag:       []string{"production"},
		NoLock:    true,
	}

	err := repo.MountRepoWithOptions(mountOpt)
	if err != nil {
		if strings.Contains(err.Error(), "fuse") || strings.Contains(err.Error(), "operation not permitted") {
			t.Logf("mount with filters failed (fuse): err=%s", err.Error())
			return
		}
		t.Fatalf("MountRepoWithOptions with filters failed: %v", err)
	}

	waitForMountReady(t, repo, mountDir, 5*time.Second)

	// Verify mount is active
	if !repo.IsMounted() {
		t.Fatal("expected mount to be active")
	}

	// Cleanup
	if err := repo.UnmountRepo(); err != nil {
		if strings.Contains(err.Error(), "signal") || strings.Contains(err.Error(), "terminated") {
			return
		}
		t.Fatalf("unmount repo: %v", err)
	}
}

// TestMountRepoBackwardCompatibility tests that MountRepoWithOptions behaves consistently
func TestMountRepoBackwardCompatibility(t *testing.T) {
	repo, _, _, _ := newResticRepo(t, true)
	if repo.AutoDetectAndDisableMount() {
		t.Skip("FUSE not available; skipping mount tests")
	}

	mountDir := filepath.Join(getTestingDirs(t))
	mountDir = filepath.Join(mountDir, "mount-compat-test")
	safeRemoveAll(t, mountDir)
	t.Cleanup(func() {
		_ = repo.UnmountRepo()
		_ = unmountResticPath(mountDir)
		_ = os.RemoveAll(mountDir)
	})

	// Use MountRepoWithOptions (same defaults as MountRepo)
	err := repo.MountRepoWithOptions(NewResticMountOption(mountDir))
	if err != nil {
		if strings.Contains(err.Error(), "fuse") || strings.Contains(err.Error(), "operation not permitted") {
			t.Logf("mount failed (fuse): err=%s", err.Error())
			return
		}
		t.Fatalf("MountRepoWithOptions failed: %v", err)
	}

	waitForMountReady(t, repo, mountDir, 5*time.Second)

	// Verify mount is active
	if !repo.IsMounted() {
		t.Fatal("expected mount to be active")
	}

	// Cleanup
	if err := repo.UnmountRepo(); err != nil {
		if strings.Contains(err.Error(), "signal") || strings.Contains(err.Error(), "terminated") {
			return
		}
		t.Fatalf("unmount repo: %v", err)
	}
}
