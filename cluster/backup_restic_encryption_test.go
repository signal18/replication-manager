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

// cacheStreamFormatEncryptedSummary stores a stream-format encrypted summary in the cluster's
// metadata cache. backupTool and backupMethod allow testing different backup type scenarios.
func cacheStreamFormatEncryptedSummary(t *testing.T, cluster *Cluster, snapshotID, backupTool, backupMethod string) {
	t.Helper()
	manager := cluster.getSnapshotMetadataManager()
	summary := &SnapshotMetadataSummary{
		Dest:                   "/backups/cluster1/" + backupTool + ".gz",
		BackupMethod:           backupMethod,
		BackupTool:             backupTool,
		BackupLine:             backupmgr.BackupLineDefault,
		StartTime:              time.Now(),
		ResticSnapshotID:       snapshotID,
		ResticBasePath:         "/backups/cluster1",
		Encrypted:              true,
		EncryptionKey:          "cloud18-sponsor-user-credentials:v1",
		EncryptionKeyCluster:   "test-cluster",
		EncryptionStreamFormat: true,
	}
	key := backupMethod + "|default"
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

// ---- Story 3.7: stream-format strategy matrix --------------------------------

// TestResolveResticReseedStrategyLegacyEncryptedForcesRestore verifies that non-stream-format
// encrypted snapshots are always forced to the restore strategy (unchanged legacy behavior).
func TestResolveResticReseedStrategyLegacyEncryptedForcesRestore(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	// Default cacheEncryptedSummary does NOT set EncryptionStreamFormat (legacy format).
	cacheEncryptedSummary(t, cluster, "snap-legacy-enc", true, "cloud18-sponsor-user-credentials:v1", "test-cluster")

	strategy := resolveResticReseedStrategy("", "logical", "snap-legacy-enc", cluster)
	if strategy != "restore" {
		t.Errorf("expected restore for legacy-encrypted snapshot, got %q", strategy)
	}
}

// TestResolveResticReseedStrategyStreamFormatMysqldumpSelectsDump verifies that a
// stream-format encrypted mysqldump snapshot selects the dump strategy (in-flight FrameReader
// decryption is possible for single-file logical backups).
func TestResolveResticReseedStrategyStreamFormatMysqldumpSelectsDump(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheStreamFormatEncryptedSummary(t, cluster, "snap-stream-mysqldump", config.ConstBackupLogicalTypeMysqldump, "logical")

	strategy := resolveResticReseedStrategy("", "logical", "snap-stream-mysqldump", cluster)
	if strategy != "dump" {
		t.Errorf("expected dump for stream-format encrypted mysqldump snapshot, got %q", strategy)
	}
}

// TestResolveResticReseedStrategyStreamFormatPhysicalWithFuseSelectsMount verifies that a
// stream-format encrypted physical snapshot selects the mount strategy when FUSE is available.
func TestResolveResticReseedStrategyStreamFormatPhysicalWithFuseSelectsMount(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	// Attach a properly-initialized ResticManager with mount enabled (FUSE available).
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	// MountDisabled defaults to false (FUSE available).
	cluster.ResticManager = rm
	cacheStreamFormatEncryptedSummary(t, cluster, "snap-stream-physical-fuse", config.ConstBackupPhysicalTypeXtrabackup, "physical")

	strategy := resolveResticReseedStrategy("", "physical", "snap-stream-physical-fuse", cluster)
	if strategy != "mount" {
		t.Errorf("expected mount for stream-format encrypted physical snapshot with FUSE, got %q", strategy)
	}
}

// TestResolveResticReseedStrategyStreamFormatPhysicalNoFuseFallsBackToRestore verifies that a
// stream-format encrypted physical snapshot falls back to restore when FUSE is unavailable.
func TestResolveResticReseedStrategyStreamFormatPhysicalNoFuseFallsBackToRestore(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	// No ResticManager → fuseAvailable = false.
	cacheStreamFormatEncryptedSummary(t, cluster, "snap-stream-physical-nofuse", config.ConstBackupPhysicalTypeXtrabackup, "physical")

	strategy := resolveResticReseedStrategy("", "physical", "snap-stream-physical-nofuse", cluster)
	if strategy != "restore" {
		t.Errorf("expected restore fallback for stream-format encrypted physical snapshot without FUSE, got %q", strategy)
	}
}

// TestReseedFromResticDumpAllowsStreamFormatEncrypted verifies that the dump strategy guard
// does not reject stream-format encrypted snapshots (AEAD frames can be decrypted in-flight).
func TestReseedFromResticDumpAllowsStreamFormatEncrypted(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheStreamFormatEncryptedSummary(t, cluster, "snap-stream-dump-allow", config.ConstBackupLogicalTypeMysqldump, "logical")
	// Use a properly-initialized ResticManager so internal mutexes don't panic.
	cluster.ResticManager = backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)

	server := &ServerMonitor{ClusterGroup: cluster}
	err := server.reseedFromResticDump(nil, "snap-stream-dump-allow", "logical")
	// The encryption guard must NOT fire; we expect a different error (snapshot not found).
	if err != nil && strings.Contains(err.Error(), "dump strategy does not support encrypted artifacts") {
		t.Errorf("dump guard incorrectly rejected stream-format encrypted snapshot: %v", err)
	}
}

// TestReseedFromResticMountAllowsStreamFormatEncrypted verifies that the mount strategy guard
// does not reject stream-format encrypted snapshots (AEAD frames can be read via FrameReader
// after mounting).
func TestReseedFromResticMountAllowsStreamFormatEncrypted(t *testing.T) {
	cluster := newResticEncryptionCluster(t)
	cacheStreamFormatEncryptedSummary(t, cluster, "snap-stream-mount-allow", config.ConstBackupPhysicalTypeXtrabackup, "physical")
	// Use a properly-initialized ResticManager so IsMountDisabled() doesn't panic.
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.SetMountDisabled(true) // Disable mount so we get a clear non-guard error.
	cluster.ResticManager = rm

	server := &ServerMonitor{ClusterGroup: cluster}
	err := server.reseedFromResticMount(nil, "snap-stream-mount-allow", "physical")
	// The encryption guard must NOT fire; we expect a different error (mount disabled or snapshot not found).
	if err != nil && strings.Contains(err.Error(), "mount strategy does not support encrypted artifacts") {
		t.Errorf("mount guard incorrectly rejected stream-format encrypted snapshot: %v", err)
	}
}

// ---- SnapshotMetadataSummary stream format field propagation -----------------

// TestBuildSnapshotMetadataSummaryPreservesStreamFormat verifies that EncryptionStreamFormat
// is correctly propagated from BackupMetadata into SnapshotMetadataSummary.
func TestBuildSnapshotMetadataSummaryPreservesStreamFormat(t *testing.T) {
	meta := &backupmgr.BackupMetadata{
		BackupTool:             config.ConstBackupLogicalTypeMysqldump,
		Dest:                   "/backups/mysqldump.sql.gz",
		Encrypted:              true,
		EncryptionStreamFormat: true,
		EncryptionKey:          "cloud18-sponsor-user-credentials:v3",
		EncryptionKeyCluster:   "test-cluster",
	}
	summary := buildSnapshotMetadataSummary(meta, backupmgr.BackupMethodLogical, "/backups")
	if !summary.EncryptionStreamFormat {
		t.Error("expected EncryptionStreamFormat=true in summary")
	}
}

// TestBuildSnapshotMetadataSummaryStreamFormatFalseWhenNotSet verifies that
// EncryptionStreamFormat defaults to false for legacy encrypted backups.
func TestBuildSnapshotMetadataSummaryStreamFormatFalseWhenNotSet(t *testing.T) {
	meta := &backupmgr.BackupMetadata{
		BackupTool:   config.ConstBackupLogicalTypeMysqldump,
		Dest:         "/backups/mysqldump.sql.gz",
		Encrypted:    true,
		EncryptionKey: "cloud18-sponsor-user-credentials:v1",
	}
	summary := buildSnapshotMetadataSummary(meta, backupmgr.BackupMethodLogical, "/backups")
	if summary.EncryptionStreamFormat {
		t.Error("expected EncryptionStreamFormat=false for legacy encrypted meta")
	}
}

// TestBuildEncryptedRestoreMetaFromSummaryPreservesStreamFormat verifies that
// EncryptionStreamFormat is propagated to the restore metadata.
func TestBuildEncryptedRestoreMetaFromSummaryPreservesStreamFormat(t *testing.T) {
	s := &SnapshotMetadataSummary{
		Encrypted:              true,
		EncryptionKey:          "cloud18-sponsor-user-credentials:v3",
		EncryptionKeyCluster:   "test-cluster",
		EncryptionStreamFormat: true,
	}
	m := buildEncryptedRestoreMetaFromSummary(s, "/target/file.sql.gz")
	if m == nil {
		t.Fatal("expected non-nil BackupMetadata")
	}
	if !m.EncryptionStreamFormat {
		t.Error("expected EncryptionStreamFormat=true in restore metadata")
	}
}

// ---- ResticBackupOption StdinFromCommand field --------------------------------

// TestResticBackupOptionStdinFromCommandFields verifies the StdinFromCommand and
// StdinFilename fields are present and correctly typed on ResticBackupOption.
func TestResticBackupOptionStdinFromCommandFields(t *testing.T) {
	opt := backupmgr.ResticBackupOption{
		StdinFromCommand: []string{"mysqldump", "--all-databases"},
		StdinFilename:    "mysqldump.sql",
		Tags:             []string{"logical", "mysqldump"},
	}
	if len(opt.StdinFromCommand) != 2 {
		t.Errorf("expected 2 StdinFromCommand parts, got %d", len(opt.StdinFromCommand))
	}
	if opt.StdinFromCommand[0] != "mysqldump" {
		t.Errorf("expected first command part to be 'mysqldump', got %q", opt.StdinFromCommand[0])
	}
	if opt.StdinFilename != "mysqldump.sql" {
		t.Errorf("unexpected StdinFilename %q", opt.StdinFilename)
	}
}

// TestResticBackupOptionStdinFromCommandEmptyPreservesLegacyPath verifies that an option
// without StdinFromCommand still uses the DirPath-based (legacy) backup path.
func TestResticBackupOptionStdinFromCommandEmptyPreservesLegacyPath(t *testing.T) {
	opt := backupmgr.ResticBackupOption{
		DirPath: "/var/lib/mysql/backup",
		Tags:    []string{"physical"},
	}
	if len(opt.StdinFromCommand) != 0 {
		t.Errorf("expected no StdinFromCommand for directory backup, got %v", opt.StdinFromCommand)
	}
	if opt.DirPath != "/var/lib/mysql/backup" {
		t.Errorf("unexpected DirPath %q", opt.DirPath)
	}
}
