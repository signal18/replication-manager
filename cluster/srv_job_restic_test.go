package cluster

import (
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
