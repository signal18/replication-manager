package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
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
	if alt := alternateCompressionPath("/tmp/mariabackup.xbtream.gz.enc"); alt != "/tmp/mariabackup.xbtream.enc" {
		t.Fatalf("expected alternate without .gz before .enc, got %q", alt)
	}
	if alt := alternateCompressionPath("/tmp/mariabackup.xbtream.enc"); alt != "/tmp/mariabackup.xbtream.gz.enc" {
		t.Fatalf("expected alternate with .gz before .enc, got %q", alt)
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
