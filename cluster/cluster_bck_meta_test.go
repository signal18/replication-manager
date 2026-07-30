package cluster

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestGetSnapshotTagValueKeyValue(t *testing.T) {
	tags := []string{
		"line:" + backupmgr.BackupLineAdhoc,
		"adhoc",
		"backup-tool:" + config.ConstBackupLogicalTypeMysqldump,
		"mysqldump",
	}
	lineExpected := backupmgr.BackupLineAdhoc
	toolExpected := config.ConstBackupLogicalTypeMysqldump
	t.Logf("input tags=%v", tags)
	if got := getSnapshotTagValue(tags, "line"); got != lineExpected {
		t.Logf("case=line expected=%q got=%q", lineExpected, got)
		t.Errorf("getSnapshotTagValue(line) = %q, want %q", got, lineExpected)
	}
	if got := getSnapshotTagValue(tags, "backup-tool"); got != toolExpected {
		t.Logf("case=backup-tool expected=%q got=%q", toolExpected, got)
		t.Errorf("getSnapshotTagValue(backup-tool) = %q, want %q", got, toolExpected)
	}
}

func TestGetSnapshotTagValueLegacyLine(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "adhoc", tags: []string{"adhoc"}, want: backupmgr.BackupLineAdhoc},
		{name: "default", tags: []string{"default"}, want: backupmgr.BackupLineDefault},
		{name: "conflict", tags: []string{"adhoc", "default"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("input tags=%v", tt.tags)
			if got := getSnapshotTagValue(tt.tags, "line"); got != tt.want {
				t.Logf("expected=%q got=%q", tt.want, got)
				t.Errorf("getSnapshotTagValue(line) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetSnapshotTagValueLegacyTool(t *testing.T) {
	toolA := config.ConstBackupLogicalTypeMysqldump
	toolB := config.ConstBackupPhysicalTypeXtrabackup
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "single", tags: []string{strings.ToUpper(toolA)}, want: strings.ToLower(toolA)},
		{name: "multiple", tags: []string{toolA, toolB}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("input tags=%v", tt.tags)
			if got := getSnapshotTagValue(tt.tags, "backup-tool"); got != tt.want {
				t.Logf("expected=%q got=%q", tt.want, got)
				t.Errorf("getSnapshotTagValue(backup-tool) = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---- snapshot metadata cache schema migration (Issue 2: SplitDump added to
// SnapshotMetadataSummary, but a pre-upgrade on-disk cache entry loads with
// it silently zero-valued and, being already Ready with Summaries, would
// never get recomputed by markSnapshotMetadataReadyFromSummary's guard) ----

func TestLoadSnapshotMetadataFromDisk_DiscardsPreUpgradeSchema(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()

	// An old-format file: no "schemaVersion" key at all, as if written
	// before SplitDump (and schema versioning) existed. Status 2 ==
	// snapshotMetadataStatusReady.
	oldFormat := `{
		"summaries": {
			"1|default": {
				"dest": "/backups/db1/splitdump",
				"backupMethod": "logical",
				"backupTool": "mysqldump",
				"backupLine": "default",
				"resticSnapshotID": "snap-old"
			}
		},
		"status": 2
	}`
	path, err := cl.snapshotMetadataFilePath("snap-old")
	if err != nil {
		t.Fatalf("snapshotMetadataFilePath: %v", err)
	}
	if err := os.WriteFile(path, []byte(oldFormat), 0644); err != nil {
		t.Fatalf("write old-format file: %v", err)
	}

	entry, err := cl.loadSnapshotMetadataFromDisk("snap-old")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entry.Status == snapshotMetadataStatusReady {
		t.Fatal("BUG: pre-upgrade schema entry must not be trusted as Ready")
	}
	if len(entry.Summaries) != 0 {
		t.Fatalf("expected stale pre-upgrade Summaries to be discarded, got %+v", entry.Summaries)
	}
}

func TestLoadSnapshotMetadataFromDisk_KeepsCurrentSchema(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()

	entry := &snapshotMetadataCacheEntry{
		Status: snapshotMetadataStatusReady,
		Summaries: map[string]*SnapshotMetadataSummary{
			"1|default": {BackupTool: "mysqldump", BackupMethod: "logical", SplitDump: true, ResticSnapshotID: "snap-current"},
		},
	}
	if err := cl.persistSnapshotMetadataEntry("snap-current", entry); err != nil {
		t.Fatalf("persist: %v", err)
	}

	loaded, err := cl.loadSnapshotMetadataFromDisk("snap-current")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded.Status != snapshotMetadataStatusReady {
		t.Fatalf("expected current-schema entry to stay Ready, got %v", loaded.Status)
	}
	if len(loaded.Summaries) != 1 || !loaded.Summaries["1|default"].SplitDump {
		t.Fatalf("expected SplitDump to survive a current-schema round-trip, got %+v", loaded.Summaries)
	}
}

// ---- EnsureSnapshotMetadataScheduled (explicit-pinned-snapshot self-heal) ----

func TestEnsureSnapshotMetadataScheduled_NoopWhenAlreadyReady(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.Conf.BackupRestic = true
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()
	manager.cache.Update("snap-ready", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
	})
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-ready", Paths: []string{"/backups/db1"}},
	}}

	cl.EnsureSnapshotMetadataScheduled("snap-ready")

	if got := cl.GetSnapshotMetadataStatus("snap-ready"); got != snapshotMetadataStatusReady {
		t.Fatalf("expected status to remain Ready (no-op), got %v", got)
	}
}

func TestEnsureSnapshotMetadataScheduled_NoopWhenAlreadyPending(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.Conf.BackupRestic = true
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()
	manager.cache.Update("snap-pending", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusPending
		entry.LastAttempt = time.Now()
	})
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-pending", Paths: []string{"/backups/db1"}},
	}}

	cl.EnsureSnapshotMetadataScheduled("snap-pending")

	if got := cl.GetSnapshotMetadataStatus("snap-pending"); got != snapshotMetadataStatusPending {
		t.Fatalf("expected status to remain Pending (no-op, no duplicate scheduling), got %v", got)
	}
}

func TestEnsureSnapshotMetadataScheduled_TriggersExtractionWhenUnknown(t *testing.T) {
	// The actual fix: an explicit-pinned snapshot whose cache entry is
	// Unknown (e.g. schema-invalidated by loadSnapshotMetadataFromDisk, or
	// never scanned) must have extraction actively kicked off here rather
	// than sitting untouched until an unrelated periodic catalog rebuild
	// happens to pass by.
	cl := newCatalogTestCluster(t)
	cl.Conf.BackupRestic = true
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()
	// Status defaults to snapshotMetadataStatusUnknown (zero value) -- as if
	// never scanned, or invalidated by the schema migration.
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}, Backups: []backupmgr.BackupSnapshot{
		{Id: "snap-unknown", Paths: []string{"/backups/db1"}},
	}}

	cl.EnsureSnapshotMetadataScheduled("snap-unknown")

	if got := cl.GetSnapshotMetadataStatus("snap-unknown"); got != snapshotMetadataStatusPending {
		t.Fatalf("expected extraction to be scheduled (status Pending), got %v", got)
	}
}

func TestEnsureSnapshotMetadataScheduled_NoopWhenSnapshotMissing(t *testing.T) {
	cl := newCatalogTestCluster(t)
	cl.Conf.BackupRestic = true
	cl.WorkingDir = t.TempDir()
	manager := cl.getSnapshotMetadataManager()
	manager.resticMetadataDir = t.TempDir()
	cl.ResticManager = &backupmgr.ResticManager{Mutex: &sync.Mutex{}}

	// Must not panic when the snapshot ID doesn't exist in ResticManager.
	cl.EnsureSnapshotMetadataScheduled("snap-missing")

	if got := cl.GetSnapshotMetadataStatus("snap-missing"); got != snapshotMetadataStatusUnknown {
		t.Fatalf("expected status to remain Unknown for a nonexistent snapshot, got %v", got)
	}
}
