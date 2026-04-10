package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ---- helpers ----------------------------------------------------------------

func newResticEncryptionCluster(t *testing.T) *Cluster {
	t.Helper()
	return &Cluster{
		Name:          "restic-enc-cluster",
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
}

func cacheEncryptedSummary(t *testing.T, cluster *Cluster, snapshotID string, encrypted bool, keyRef, keyCl string) {
	t.Helper()
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest:                 "/backups/cluster1/mysqldump.sql.gz",
		BackupMethod:         "logical",
		BackupTool:           config.ConstBackupLogicalTypeMysqldump,
		BackupLine:           backupmgr.BackupLineDefault,
		StartTime:            time.Now(),
		ResticSnapshotID:     snapshotID,
		ResticBasePath:       "/backups/cluster1",
		Encrypted:            encrypted,
		EncryptionKey:        keyRef,
		EncryptionKeyCluster: keyCl,
	}
	key := "1|default"
	manager.cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})
}

// ---- Task 1: SnapshotMetadataSummary encryption fields ----------------------

func TestBuildSnapshotMetadataSummaryPreservesEncryptionFields(t *testing.T) {
	meta := &backupmgr.BackupMetadata{
		BackupTool:           config.ConstBackupLogicalTypeMysqldump,
		Dest:                 "/backups/mysqldump.sql.gz",
		Encrypted:            true,
		EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
		EncryptionIV:         "hex:aabbccdd00112233aabbccdd00112233",
		EncryptionMAC:        "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd",
		EncryptionKey:        "cloud18-sponsor-user-credentials:v3",
		EncryptionKeyCluster: "test-cluster",
		ResticSnapshotID:     "abc123",
	}
	summary := buildSnapshotMetadataSummary(meta, backupmgr.BackupMethodLogical, "/backups")

	if !summary.Encrypted {
		t.Error("expected Encrypted=true in summary")
	}
	if summary.EncryptionAlgo != backupmgr.BackupEncryptionAlgorithm {
		t.Errorf("unexpected EncryptionAlgo %q", summary.EncryptionAlgo)
	}
	if summary.EncryptionIV != "hex:aabbccdd00112233aabbccdd00112233" {
		t.Errorf("unexpected EncryptionIV %q", summary.EncryptionIV)
	}
	if summary.EncryptionMAC != "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd" {
		t.Errorf("unexpected EncryptionMAC %q", summary.EncryptionMAC)
	}
	if summary.EncryptionKey != "cloud18-sponsor-user-credentials:v3" {
		t.Errorf("unexpected EncryptionKey %q", summary.EncryptionKey)
	}
	if summary.EncryptionKeyCluster != "test-cluster" {
		t.Errorf("unexpected EncryptionKeyCluster %q", summary.EncryptionKeyCluster)
	}
}

func TestBuildSnapshotMetadataSummaryNilMeta(t *testing.T) {
	if summary := buildSnapshotMetadataSummary(nil, backupmgr.BackupMethodLogical, "/backups"); summary != nil {
		t.Error("expected nil summary for nil meta")
	}
}

func TestBuildSnapshotMetadataSummaryNotEncrypted(t *testing.T) {
	meta := &backupmgr.BackupMetadata{
		BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Dest:       "/backups/mysqldump.sql.gz",
	}
	summary := buildSnapshotMetadataSummary(meta, backupmgr.BackupMethodLogical, "/backups")
	if summary.Encrypted {
		t.Error("expected Encrypted=false for non-encrypted meta")
	}
	if summary.EncryptionKey != "" {
		t.Errorf("expected empty EncryptionKey, got %q", summary.EncryptionKey)
	}
}

// ---- encryptionFromSummary helper -------------------------------------------

func TestEncryptionFromSummaryNilReturnsNotOk(t *testing.T) {
	encrypted, keyRef, keyCl, ok := encryptionFromSummary(nil)
	if ok || encrypted || keyRef != "" || keyCl != "" {
		t.Errorf("expected zero values for nil summary, got encrypted=%t keyRef=%q keyCl=%q ok=%t", encrypted, keyRef, keyCl, ok)
	}
}

func TestEncryptionFromSummaryExtractsFields(t *testing.T) {
	s := &SnapshotMetadataSummary{
		Encrypted:            true,
		EncryptionKey:        "cloud18-sponsor-user-credentials:v5",
		EncryptionKeyCluster: "test-cluster",
	}
	encrypted, keyRef, keyCl, ok := encryptionFromSummary(s)
	if !ok {
		t.Fatal("expected ok=true for non-nil summary")
	}
	if !encrypted {
		t.Error("expected Encrypted=true")
	}
	if keyRef != "cloud18-sponsor-user-credentials:v5" {
		t.Errorf("unexpected keyRef %q", keyRef)
	}
	if keyCl != "test-cluster" {
		t.Errorf("unexpected keyCluster %q", keyCl)
	}
}

// ---- Task 2: prepareResticReseedPaths encryption enforcement ----------------

func TestPrepareResticReseedPathsFailsOnEncryptedMissingKeyRef(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	// Cache a summary that says encrypted=true but has no keyRef
	cacheEncryptedSummary(t, cluster, "snap-enc-nokey", true, "", "test-cluster")

	server := &ServerMonitor{ClusterGroup: cluster}
	_, err := server.prepareResticReseedPaths("snap-enc-nokey", "logical")
	if err == nil {
		t.Fatal("expected error for encrypted snapshot missing key reference")
	}
	if !strings.Contains(err.Error(), "encryption key reference is missing") {
		t.Errorf("expected key reference error, got: %v", err)
	}
}

func TestPrepareResticReseedPathsSucceedsOnEncryptedWithKeyRef(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-enc-withkey", true, "cloud18-sponsor-user-credentials:v3", "test-cluster")

	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-enc-withkey", "logical")
	if err != nil {
		t.Fatalf("unexpected error for encrypted snapshot with valid key ref: %v", err)
	}
	if len(paths.SourcePaths) == 0 {
		t.Fatal("expected at least one source path")
	}
}

func TestPrepareResticReseedPathsSucceedsOnNonEncrypted(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-plain", false, "", "")

	server := &ServerMonitor{ClusterGroup: cluster}
	paths, err := server.prepareResticReseedPaths("snap-plain", "logical")
	if err != nil {
		t.Fatalf("unexpected error for non-encrypted snapshot: %v", err)
	}
	if len(paths.SourcePaths) == 0 {
		t.Fatal("expected at least one source path")
	}
}

// ---- Task 3: strategy selection for encrypted snapshots ---------------------

func TestResolveResticReseedStrategyForcesRestoreForEncrypted(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-enc-strategy", true, "cloud18-sponsor-user-credentials:v1", "test-cluster")

	strategy := resolveResticReseedStrategy("", "logical", "snap-enc-strategy", cluster)
	if strategy != "restore" {
		t.Errorf("expected restore strategy for encrypted snapshot, got %q", strategy)
	}
}

func TestResolveResticReseedStrategyAutoForNonEncrypted(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-plain-strategy", false, "", "")

	// Without FUSE, non-encrypted mysqldump → dump strategy
	strategy := resolveResticReseedStrategy("", "logical", "snap-plain-strategy", cluster)
	if strategy != "dump" {
		// non-encrypted mysqldump → dump when no FUSE
		t.Errorf("expected dump strategy for non-encrypted mysqldump snapshot, got %q", strategy)
	}
}

// ---- dump strategy encryption guard ----------------------------------------

func TestReseedFromResticDumpRejectsEncryptedArtifacts(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-enc-dump", true, "cloud18-sponsor-user-credentials:v2", "test-cluster")
	cluster.ResticManager = &backupmgr.ResticManager{}

	server := &ServerMonitor{ClusterGroup: cluster}
	err := server.reseedFromResticDump(nil, "snap-enc-dump", "logical")
	if err == nil {
		t.Fatal("expected error when dump strategy used for encrypted snapshot")
	}
	if !strings.Contains(err.Error(), "dump strategy does not support encrypted artifacts") {
		t.Errorf("expected dump-strategy encryption error, got: %v", err)
	}
}

// ---- mount strategy encryption guard ----------------------------------------

func TestReseedFromResticMountRejectsEncryptedArtifacts(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheEncryptedSummary(t, cluster, "snap-enc-mount", true, "cloud18-sponsor-user-credentials:v2", "test-cluster")
	rm := &backupmgr.ResticManager{}
	cluster.ResticManager = rm

	server := &ServerMonitor{ClusterGroup: cluster}
	err := server.reseedFromResticMount(nil, "snap-enc-mount", "logical")
	if err == nil {
		t.Fatal("expected error when mount strategy used for encrypted snapshot")
	}
	if !strings.Contains(err.Error(), "mount strategy does not support encrypted artifacts") {
		t.Errorf("expected mount-strategy encryption error, got: %v", err)
	}
}

// ---- buildEncryptedRestoreMetaFromSummary -----------------------------------

func TestBuildEncryptedRestoreMetaFromSummaryNilReturnsNil(t *testing.T) {
	if m := buildEncryptedRestoreMetaFromSummary(nil, "/some/path"); m != nil {
		t.Error("expected nil for nil summary")
	}
}

func TestBuildEncryptedRestoreMetaFromSummaryPopulatesFields(t *testing.T) {
	s := &SnapshotMetadataSummary{
		Encrypted:            true,
		EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
		EncryptionIV:         "hex:aabbccdd00112233aabbccdd00112233",
		EncryptionMAC:        "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd",
		EncryptionKey:        "cloud18-sponsor-user-credentials:v4",
		EncryptionKeyCluster: "my-cluster",
	}
	m := buildEncryptedRestoreMetaFromSummary(s, "/target/file.sql.gz")
	if m == nil {
		t.Fatal("expected non-nil BackupMetadata")
	}
	if !m.Encrypted {
		t.Error("expected Encrypted=true")
	}
	if m.EncryptionAlgo != backupmgr.BackupEncryptionAlgorithm {
		t.Errorf("unexpected EncryptionAlgo %q", m.EncryptionAlgo)
	}
	if m.EncryptionIV != "hex:aabbccdd00112233aabbccdd00112233" {
		t.Errorf("unexpected EncryptionIV %q", m.EncryptionIV)
	}
	if m.EncryptionMAC != "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd" {
		t.Errorf("unexpected EncryptionMAC %q", m.EncryptionMAC)
	}
	if m.EncryptionKey != "cloud18-sponsor-user-credentials:v4" {
		t.Errorf("unexpected EncryptionKey %q", m.EncryptionKey)
	}
	if m.EncryptionKeyCluster != "my-cluster" {
		t.Errorf("unexpected EncryptionKeyCluster %q", m.EncryptionKeyCluster)
	}
	if m.Dest != "/target/file.sql.gz" {
		t.Errorf("unexpected Dest %q", m.Dest)
	}
}
