package cluster

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	sharedlog "github.com/signal18/replication-manager/utils/s18log/shared"
)

func TestPrepareResticReseedPathsUsesMetadataDest(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest:             "/backups/cluster1/mysqldump.sql.gz",
		BackupMethod:     "logical",
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupLine:       backupmgr.BackupLineDefault,
		StartTime:        time.Now(),
		ResticSnapshotID: "snap-1",
		ResticBasePath:   "/backups/cluster1",
	}
	key := "1|default"
	manager.cache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-1", "logical")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths.SourcePaths) != 1 {
		t.Fatalf("expected 1 source path, got %d", len(paths.SourcePaths))
	}
	if paths.SourcePaths[0] != "mysqldump.sql.gz" {
		t.Fatalf("unexpected source path %q", paths.SourcePaths[0])
	}
}

func TestPrepareResticReseedPathsUsesMetadataDir(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest:             "/backups/cluster1/custom_dir",
		BackupMethod:     "logical",
		BackupTool:       config.ConstBackupLogicalTypeMydumper,
		BackupLine:       backupmgr.BackupLineDefault,
		StartTime:        time.Now(),
		ResticSnapshotID: "snap-2",
		ResticBasePath:   "/backups/cluster1",
	}
	key := "1|default"
	manager.cache.Update("snap-2", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-2", "logical")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !paths.IsDirectory {
		t.Fatalf("expected directory-based paths")
	}
	if len(paths.SourcePaths) != 1 {
		t.Fatalf("expected 1 source path, got %d", len(paths.SourcePaths))
	}
	if paths.SourcePaths[0] != "custom_dir" {
		t.Fatalf("unexpected source path %q", paths.SourcePaths[0])
	}
}

func TestUpdateResticReseedJobErrorUsesMetadataTool(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{BackupLogicalType: "mysqldump", BackupPhysicalType: "xtrabackup"},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		BackupMethod:     "physical",
		BackupTool:       config.ConstBackupPhysicalTypeXtrabackup,
		BackupLine:       backupmgr.BackupLineDefault,
		ResticSnapshotID: "snap-1",
	}
	manager.cache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			"2|default": summary,
		}
	})
	server := &ServerMonitor{ClusterGroup: cluster, JobResults: config.NewTasksMap()}
	server.updateResticReseedJobError("snap-1", "physical", fmt.Errorf("mount already running"))
	if job, ok := server.JobResults.CheckAndGet("reseedxtrabackup"); ok {
		if job.State != 5 || job.Done != 1 {
			t.Fatalf("expected job state 5 done 1, got state=%d done=%d", job.State, job.Done)
		}
	} else {
		t.Fatalf("expected reseedxtrabackup job to be updated")
	}
}

func TestVerifyRestoredBackupUsesAlternateCompressionPath(t *testing.T) {
	tmpDir := t.TempDir()
	baseFile := filepath.Join(tmpDir, "mariabackup.xbtream")
	if err := os.WriteFile(baseFile, []byte("payload"), 0644); err != nil {
		t.Fatalf("failed to write base file: %v", err)
	}
	paths := &ResticReseedPaths{
		SnapshotID:  "snap-1",
		IsDirectory: false,
		SourcePaths: []string{"mariabackup.xbtream.gz"},
		TargetPaths: []string{baseFile + ".gz"},
	}
	server := &ServerMonitor{}
	if err := server.verifyRestoredBackup(paths); err != nil {
		t.Fatalf("expected fallback to alternate path, got %v", err)
	}
	if paths.TargetPaths[0] != baseFile {
		t.Fatalf("expected target path to update to %q, got %q", baseFile, paths.TargetPaths[0])
	}
	if paths.SourcePaths[0] != filepath.Base(baseFile) {
		t.Fatalf("expected source path to update to %q, got %q", filepath.Base(baseFile), paths.SourcePaths[0])
	}
}

func TestBuildResticReseedPayload(t *testing.T) {
	server := &ServerMonitor{}
	summary := &SnapshotMetadataSummary{
		Dest:             "/backups/cluster1/mariabackup.xbtream.gz",
		ResticSnapshotID: "snap-1",
		ResticBasePath:   "/backups/cluster1",
	}
	payload := server.buildResticReseedPayload(summary, "/base", "mount")
	if payload["restic_snapshot_id"] != "snap-1" {
		t.Fatalf("expected restic snapshot id")
	}
	if payload["restic_reseed_strategy"] != "mount" {
		t.Fatalf("expected restic reseed strategy")
	}
	if payload["restic_source_base_path"] != "/base" {
		t.Fatalf("expected source base path override")
	}
	if payload["restic_source_path"] != "/backups/cluster1/mariabackup.xbtream.gz" {
		t.Fatalf("expected source path from dest")
	}
}

func TestAlternateCompressionPathSwap(t *testing.T) {
	if alt := alternateCompressionPath("/tmp/mariabackup.xbtream.gz"); alt != "/tmp/mariabackup.xbtream" {
		t.Fatalf("expected alternate without .gz, got %q", alt)
	}
	if alt := alternateCompressionPath("/tmp/mariabackup.xbtream"); alt != "/tmp/mariabackup.xbtream.gz" {
		t.Fatalf("expected alternate with .gz, got %q", alt)
	}
}

func TestExpandResticMountTemplateReplacesTokens(t *testing.T) {
	path, idUsed, ok := expandResticMountTemplate(
		"hosts/%h/%T",
		"shortid",
		"fullid",
		"repman",
		"db1",
		"tag1,tag2",
		"2026-02-01_10-00-00",
	)
	if !ok {
		t.Fatalf("expected template expansion to succeed")
	}
	if idUsed != "" {
		t.Fatalf("expected empty idUsed, got %q", idUsed)
	}
	if path != filepath.Join("hosts", "db1", "2026-02-01_10-00-00") {
		t.Fatalf("unexpected expanded path %q", path)
	}
}

func TestExpandResticMountTemplateRejectsUnknownTokens(t *testing.T) {
	if _, _, ok := expandResticMountTemplate(
		"ids/%x",
		"shortid",
		"fullid",
		"repman",
		"db1",
		"tag1",
		"2026-02-01_10-00-00",
	); ok {
		t.Fatalf("expected template expansion to fail for unknown token")
	}
}

func TestBuildResticMountSnapshotCandidatesNoSwap(t *testing.T) {
	mountDir := "/var/lib/replication-manager/cluster1/mount"
	seen := make(map[string]struct{})
	candidates := buildResticMountSnapshotCandidates(
		mountDir,
		[]string{"ids/%I"},
		"shortid",
		"fullid",
		"repman",
		"db1",
		"tag1",
		"2026-02-01_10-00-00",
		"configured",
		seen,
	)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Path != filepath.Join(mountDir, "ids", "fullid") {
		t.Fatalf("unexpected candidate path %q", candidates[0].Path)
	}
}

func TestBuildResticMountSnapshotCandidatesTimeTemplate(t *testing.T) {
	mountDir := "/var/lib/replication-manager/cluster1/mount"
	candidates := buildResticMountSnapshotCandidates(
		mountDir,
		[]string{"snapshots/%T"},
		"shortid",
		"fullid",
		"repman",
		"db1",
		"tag1",
		"2026-02-01_10-00-00",
		"configured",
		make(map[string]struct{}),
	)
	if len(candidates) != 1 {
		t.Fatalf("expected 1 candidate, got %d", len(candidates))
	}
	if candidates[0].Path != filepath.Join(mountDir, "snapshots", "2026-02-01_10-00-00") {
		t.Fatalf("unexpected candidate path %q", candidates[0].Path)
	}
}

// TestResticCheckConfigManualNoSideEffects verifies that ResticCheckConfigManual:
//   - returns initialization_required for a fresh (uninitialized) repo
//   - does not create a repository config file
//   - does not enqueue unlock or fetch tasks
func TestResticCheckConfigManualNoSideEffects(t *testing.T) {
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic binary not found, skipping cluster manual-check test")
	}

	repoDir, err := os.MkdirTemp("", "cluster-restic-check-*")
	if err != nil {
		t.Fatalf("create temp repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	msgChan := make(chan sharedlog.Message, 64)
	t.Cleanup(func() {
		for len(msgChan) > 0 {
			<-msgChan
		}
	})

	cl := &Cluster{
		Conf: &config.Config{
			BackupRestic:           true,
			BackupResticBinaryPath: resticPath,
			BackupResticPassword:   "testpassword",
			BackupResticLocalRepository: repoDir,
		},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
		MessageChan:   msgChan,
	}
	// Inject env so SetEnv inside ensureResticManagerBase picks up the right repo.
	cl.Conf.BackupResticRepository = repoDir

	if cl.ResticManager != nil {
		t.Fatal("precondition: ResticManager should be nil before the call")
	}

	result := cl.ResticCheckConfigManual(false)

	if result.Status != backupmgr.ManualCheckStatusInitRequired {
		t.Fatalf("expected initialization_required, got %s: %s", result.Status, result.Message)
	}
	if result.FetchQueued {
		t.Fatal("fetch must not be queued for initialization_required result")
	}

	// No repo config should have been created.
	if _, statErr := os.Stat(filepath.Join(repoDir, "config")); statErr == nil {
		t.Fatal("repository config must not be created during manual check")
	}

	if cl.ResticManager == nil {
		t.Fatal("ResticManager should be set after manual check (ensureResticManagerBase ran)")
	}

	// No unlock or fetch tasks should be in the queue.
	cl.ResticManager.Mutex.Lock()
	queue := make([]*backupmgr.ResticTask, len(cl.ResticManager.TaskQueue))
	copy(queue, cl.ResticManager.TaskQueue)
	cl.ResticManager.Mutex.Unlock()

	for _, task := range queue {
		switch task.Type {
		case backupmgr.UnlockTask:
			t.Errorf("unexpected unlock task in queue after manual check")
		case backupmgr.FetchTask:
			t.Errorf("unexpected fetch task in queue after manual check")
		}
	}

	// Confirm the string fields are populated.
	if strings.TrimSpace(result.Message) == "" {
		t.Fatal("expected non-empty message in result")
	}
	if !result.CanInit {
		t.Fatal("expected CanInit true for fresh repo")
	}
}

// TestResticCheckConfigManualSuppressesFetch verifies that after a manual check
// returns initialization_required, a subsequent ResticFetchRepo call does not
// enqueue a new fetch task.
func TestResticCheckConfigManualSuppressesFetch(t *testing.T) {
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic binary not found, skipping cluster fetch-suppression test")
	}

	repoDir, err := os.MkdirTemp("", "cluster-restic-suppress-*")
	if err != nil {
		t.Fatalf("create temp repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	msgChan := make(chan sharedlog.Message, 64)
	t.Cleanup(func() {
		for len(msgChan) > 0 {
			<-msgChan
		}
	})

	cl := &Cluster{
		Conf: &config.Config{
			BackupRestic:                true,
			BackupResticBinaryPath:      resticPath,
			BackupResticPassword:        "testpassword",
			BackupResticLocalRepository: repoDir,
			BackupResticRepository:      repoDir,
		},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
		MessageChan:   msgChan,
	}

	// Manual check classifies the fresh repo and sets repoState = initialization_required.
	result := cl.ResticCheckConfigManual(false)
	if result.Status != backupmgr.ManualCheckStatusInitRequired {
		t.Fatalf("precondition: expected initialization_required, got %s", result.Status)
	}

	// Drain any tasks queued during the check (there should be none, but guard anyway).
	cl.ResticManager.Mutex.Lock()
	cl.ResticManager.TaskQueue = cl.ResticManager.TaskQueue[:0]
	cl.ResticManager.Mutex.Unlock()

	// ResticFetchRepo must not enqueue a fetch when repo state is initialization_required.
	cl.ResticFetchRepo()

	cl.ResticManager.Mutex.Lock()
	queueLen := len(cl.ResticManager.TaskQueue)
	cl.ResticManager.Mutex.Unlock()

	if queueLen != 0 {
		t.Fatalf("expected no tasks queued after fetch on init-required repo, got %d", queueLen)
	}
}

// TestReloadResticEnvClearsRepoState verifies that ReloadResticEnv resets a
// stale initialization_required state so ResticFetchRepo is no longer suppressed.
func TestReloadResticEnvClearsRepoState(t *testing.T) {
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic binary not found, skipping reload-clears-state test")
	}

	repoDir, err := os.MkdirTemp("", "cluster-restic-reload-*")
	if err != nil {
		t.Fatalf("create temp repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(repoDir) })

	msgChan := make(chan sharedlog.Message, 64)
	t.Cleanup(func() {
		for len(msgChan) > 0 {
			<-msgChan
		}
	})

	cl := &Cluster{
		Conf: &config.Config{
			BackupRestic:                true,
			BackupResticBinaryPath:      resticPath,
			BackupResticPassword:        "testpassword",
			BackupResticLocalRepository: repoDir,
			BackupResticRepository:      repoDir,
		},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
		MessageChan:   msgChan,
	}

	// Step 1: manual check classifies the fresh repo as initialization_required.
	result := cl.ResticCheckConfigManual(false)
	if result.Status != backupmgr.ManualCheckStatusInitRequired {
		t.Fatalf("precondition: expected initialization_required, got %s", result.Status)
	}
	if cl.ResticManager.GetRepoState() != backupmgr.ManualCheckStatusInitRequired {
		t.Fatal("precondition: repoState should be initialization_required after manual check")
	}

	// Step 2: simulate operator changing repo config (e.g. different path).
	newRepoDir, err := os.MkdirTemp("", "cluster-restic-reload-new-*")
	if err != nil {
		t.Fatalf("create new temp repo dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(newRepoDir) })
	cl.Conf.BackupResticLocalRepository = newRepoDir
	cl.Conf.BackupResticRepository = newRepoDir

	// Step 3: reload must clear the cached state.
	cl.ReloadResticEnv()
	if cl.ResticManager.GetRepoState() != "" {
		t.Fatalf("expected repoState cleared after ReloadResticEnv, got %q", cl.ResticManager.GetRepoState())
	}

	// Step 4: ResticFetchRepo should now be unblocked (will enqueue a fetch task).
	cl.ResticManager.Mutex.Lock()
	cl.ResticManager.TaskQueue = cl.ResticManager.TaskQueue[:0]
	cl.ResticManager.Mutex.Unlock()

	cl.ResticFetchRepo()

	cl.ResticManager.Mutex.Lock()
	queueLen := len(cl.ResticManager.TaskQueue)
	cl.ResticManager.Mutex.Unlock()

	if queueLen == 0 {
		t.Fatal("expected fetch task to be enqueued after repoState was cleared by ReloadResticEnv")
	}
}
