package cluster

import (
	"fmt"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestPrepareResticReseedPathsUsesMetadataDest(t *testing.T) {
	cluster := &Cluster{
		Conf:                  &config.Config{},
		BackupMetaMap:         backupmgr.NewBackupMetaMap(),
		snapshotMetadataCache: newSnapshotMetadataCache(),
	}
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
	cluster.snapshotMetadataCache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
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
		Conf:                  &config.Config{},
		BackupMetaMap:         backupmgr.NewBackupMetaMap(),
		snapshotMetadataCache: newSnapshotMetadataCache(),
	}
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
	cluster.snapshotMetadataCache.Update("snap-2", func(entry *snapshotMetadataCacheEntry) {
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
		Conf:                  &config.Config{BackupLogicalType: "mysqldump", BackupPhysicalType: "xtrabackup"},
		BackupMetaMap:         backupmgr.NewBackupMetaMap(),
		snapshotMetadataCache: newSnapshotMetadataCache(),
	}
	summary := &SnapshotMetadataSummary{
		BackupMethod:     "physical",
		BackupTool:       config.ConstBackupPhysicalTypeXtrabackup,
		BackupLine:       backupmgr.BackupLineDefault,
		ResticSnapshotID: "snap-1",
	}
	cluster.snapshotMetadataCache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
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
