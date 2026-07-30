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
	paths, err := server.prepareResticReseedPaths("snap-1", "logical", "")
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
	paths, err := server.prepareResticReseedPaths("snap-2", "logical", "")
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

func TestPrepareResticReseedPathsSplitdumpIsDirectory(t *testing.T) {
	// The execution-side counterpart to the selector/strategy splitdump
	// fixes: a splitdump-mode mysqldump snapshot (BackupTool=="mysqldump",
	// SplitDump=true) must be reported as IsDirectory==true, matching
	// isDirectoryBackupLayout (restore_catalog.go) -- not the previous
	// tool-name-only inference, which always treated "mysqldump" as
	// single-file regardless of SplitDump and left reseedFromResticDump's
	// directory guard bypassable.
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest:             "/backups/cluster1/splitdump",
		BackupMethod:     "logical",
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupLine:       backupmgr.BackupLineDefault,
		StartTime:        time.Now(),
		ResticSnapshotID: "snap-splitdump",
		ResticBasePath:   "/backups/cluster1",
		SplitDump:        true,
	}
	key := "1|default"
	manager.cache.Update("snap-splitdump", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-splitdump", "logical", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !paths.IsDirectory {
		t.Fatalf("BUG: splitdump-mode mysqldump snapshot must report IsDirectory=true")
	}
	if len(paths.SourcePaths) != 1 || paths.SourcePaths[0] != "splitdump" {
		t.Fatalf("expected source path [splitdump], got %+v", paths.SourcePaths)
	}
}

func TestPrepareResticReseedPathsSingleFileMysqldumpStaysFile(t *testing.T) {
	// Regression companion to the above: a genuine single-file mysqldump
	// (SplitDump=false) must still report IsDirectory==false.
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
		ResticSnapshotID: "snap-singlefile-exec",
		ResticBasePath:   "/backups/cluster1",
		SplitDump:        false,
	}
	key := "1|default"
	manager.cache.Update("snap-singlefile-exec", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-singlefile-exec", "logical", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if paths.IsDirectory {
		t.Fatalf("single-file mysqldump snapshot must not report IsDirectory=true")
	}
	if len(paths.SourcePaths) != 1 || paths.SourcePaths[0] != "mysqldump.sql.gz" {
		t.Fatalf("expected source path [mysqldump.sql.gz], got %+v", paths.SourcePaths)
	}
}

// ---- resolveResticReseedStrategy + splitdump layout signal ----

func TestResolveResticReseedStrategy_AutoPicksDumpForSingleFileMysqldump(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest: "/backups/cluster1/mysqldump.sql.gz", BackupMethod: "logical",
		BackupTool: config.ConstBackupLogicalTypeMysqldump, BackupLine: backupmgr.BackupLineDefault,
		StartTime: time.Now(), ResticSnapshotID: "snap-singlefile", SplitDump: false,
	}
	key := "1|default"
	manager.cache.Update("snap-singlefile", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})

	if got := resolveResticReseedStrategy("auto", "logical", "snap-singlefile", "", cluster); got != "dump" {
		t.Fatalf("expected auto-selection to pick dump for a single-file mysqldump snapshot, got %q", got)
	}
}

func TestResolveResticReseedStrategy_AutoDoesNotPickDumpForSplitdump(t *testing.T) {
	// The exact "Option B" regression: a splitdump-mode mysqldump summary
	// (BackupTool=="mysqldump", SplitDump=true) reaching the strategy
	// resolver via the summary-backed metadata path must NOT resolve to
	// "dump" -- isDirectoryBackupLayout (restore_catalog.go) is the shared
	// signal that keeps this decision consistent with
	// ResolveResticSnapshot's requireSingleFile exclusion.
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest: "/backups/cluster1/splitdump", BackupMethod: "logical",
		BackupTool: config.ConstBackupLogicalTypeMysqldump, BackupLine: backupmgr.BackupLineDefault,
		StartTime: time.Now(), ResticSnapshotID: "snap-splitdump", SplitDump: true,
	}
	key := "1|default"
	manager.cache.Update("snap-splitdump", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})

	if got := resolveResticReseedStrategy("auto", "logical", "snap-splitdump", "", cluster); got == "dump" {
		t.Fatalf("BUG: auto-selection must not pick dump for a splitdump-mode mysqldump snapshot, got %q", got)
	}
}

func TestResolveResticReseedStrategy_MydumperUnaffected(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest: "/backups/cluster1/mydumper", BackupMethod: "logical",
		BackupTool: config.ConstBackupLogicalTypeMydumper, BackupLine: backupmgr.BackupLineDefault,
		StartTime: time.Now(), ResticSnapshotID: "snap-mydumper",
	}
	key := "1|default"
	manager.cache.Update("snap-mydumper", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})

	if got := resolveResticReseedStrategy("auto", "logical", "snap-mydumper", "", cluster); got == "dump" {
		t.Fatalf("mydumper must never resolve to dump strategy, got %q", got)
	}
}

// ---- getSnapshotMetadataForMethod tool disambiguation (identity preserved
// across selection -> execution for a snapshot ID with multiple same-method
// summaries) ----

func mixedToolMetadataCluster() *Cluster {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	mysqldumpSummary := &SnapshotMetadataSummary{
		Dest: "/backups/db1/mysqldump.sql.gz", BackupMethod: "logical", BackupTool: "mysqldump",
		BackupLine: backupmgr.BackupLineDefault, StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0),
		ResticSnapshotID: "snap-mixed",
	}
	mydumperSummary := &SnapshotMetadataSummary{
		Dest: "/backups/db1/mydumper", BackupMethod: "logical", BackupTool: "mydumper",
		BackupLine: backupmgr.BackupLineAdhoc, StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0),
		ResticSnapshotID: "snap-mixed",
	}
	manager.cache.Update("snap-mixed", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault): mysqldumpSummary,
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineAdhoc):    mydumperSummary,
		}
	})
	return cluster
}

func TestGetSnapshotMetadataForMethod_DeterministicWithoutToolHint(t *testing.T) {
	// Regression pin for the bug: before snapshotSummaryBetter, this
	// returned a RANDOM tool across calls (Go map iteration order) for a
	// snapshot ID carrying two same-method summaries. Without a tool hint,
	// the result must at least be stable across repeated calls, even though
	// which one wins (newest, since both share EndTime here — arbitrary but
	// consistent) isn't itself meaningful.
	cluster := mixedToolMetadataCluster()
	first := getSnapshotMetadataForMethod(cluster, "snap-mixed", "logical", "", nil)
	if first == nil {
		t.Fatal("expected a summary")
	}
	for i := 0; i < 50; i++ {
		got := getSnapshotMetadataForMethod(cluster, "snap-mixed", "logical", "", nil)
		if got == nil || got.BackupTool != first.BackupTool {
			t.Fatalf("non-deterministic: call %d returned tool %v, first call returned %q", i, got, first.BackupTool)
		}
	}
}

func TestGetSnapshotMetadataForMethod_ToolHintSelectsExactSummary(t *testing.T) {
	// The actual fix: ResolveResticSnapshot picks a specific tool (e.g.
	// mysqldump, having excluded mydumper via requireSingleFile); execution
	// must resolve to THAT summary, not an arbitrary same-ID sibling.
	cluster := mixedToolMetadataCluster()
	for i := 0; i < 20; i++ {
		got := getSnapshotMetadataForMethod(cluster, "snap-mixed", "logical", "mysqldump", nil)
		if got == nil || got.BackupTool != "mysqldump" {
			t.Fatalf("tool hint mysqldump must always resolve to the mysqldump summary, got %+v", got)
		}
	}
	for i := 0; i < 20; i++ {
		got := getSnapshotMetadataForMethod(cluster, "snap-mixed", "logical", "mydumper", nil)
		if got == nil || got.BackupTool != "mydumper" {
			t.Fatalf("tool hint mydumper must always resolve to the mydumper summary, got %+v", got)
		}
	}
}

func TestGetSnapshotMetadataForMethod_ToolConstraintFallsThroughSources(t *testing.T) {
	// tool must be a HARD constraint spanning all three sources
	// (index, cache entry, live BackupMetaMap fallback), not just a ranking
	// preference within whichever source answers first: the cache entry
	// here has ONLY a mydumper summary (Ready), while the live
	// BackupMetaMap fallback has the correct mysqldump record for the same
	// snapshot ID. Requesting tool="mysqldump" must fall through the cache
	// source (no match there) to the live fallback instead of returning the
	// cached mydumper summary just because that source answered first.
	cl := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	sv := &ServerMonitor{URL: "db1:3306", Host: "db1", Port: "3306", ClusterGroup: cl}
	cl.Servers = serverList{sv}

	manager := cl.getSnapshotMetadataManager()
	mydumperSummary := &SnapshotMetadataSummary{
		Dest: "/backups/db1/mydumper", BackupMethod: "logical", BackupTool: "mydumper",
		BackupLine: backupmgr.BackupLineDefault, StartTime: time.Unix(900, 0), EndTime: time.Unix(1000, 0),
		ResticSnapshotID: "snap-cross-source",
	}
	manager.cache.Update("snap-cross-source", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			snapshotMetadataKey(backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault): mydumperSummary,
		}
	})

	cl.ResticManager = &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-cross-source", Time: time.Unix(1000, 0).Format(time.RFC3339Nano), Paths: []string{"/backups/db1"}},
	}}
	mysqldumpMeta := &backupmgr.BackupMetadata{
		Id: 1, BackupMethod: backupmgr.BackupMethodLogical, BackupTool: "mysqldump",
		Source: sv.URL, Dest: "/backups/db1/mysqldump.sql.gz",
		ResticSnapshotID: "snap-cross-source", Completed: true, EndTime: time.Unix(1000, 0),
	}
	cl.BackupMetaMap.Store(mysqldumpMeta.Id, mysqldumpMeta)

	got := getSnapshotMetadataForMethod(cl, "snap-cross-source", "logical", "mysqldump", nil)
	if got == nil || got.BackupTool != "mysqldump" {
		t.Fatalf("tool=mysqldump must fall through the wrong-tool cache source to the correct-tool live fallback, got %+v", got)
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
	server.updateResticReseedJobError("snap-1", "physical", "", fmt.Errorf("mount already running"))
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
