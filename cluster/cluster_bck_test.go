package cluster

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestSplitResticKeepTagTemplates(t *testing.T) {
	cases := []struct {
		input    string
		expected []string
	}{
		{``, []string{}},
		{`   `, []string{}},
		{`"role:primary,critical" "env:prod"`, []string{`"role:primary,critical"`, `"env:prod"`}},
		{`cluster,"env:prod,team:dev"`, []string{`cluster`, `"env:prod,team:dev"`}},
		{`  tag1   tag2  `, []string{`tag1`, `tag2`}},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got, hadUnmatched := splitResticKeepTagTemplates(tc.input)
			if hadUnmatched {
				t.Fatalf("unexpected unmatched quotes for input %q", tc.input)
			}
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("unexpected split result: got %v, want %v", got, tc.expected)
			}
		})
	}
}

func TestSplitResticKeepTagTemplates_UnmatchedQuotes(t *testing.T) {
	got, hadUnmatched := splitResticKeepTagTemplates(`"role:primary,critical env:prod`)
	if !hadUnmatched {
		t.Fatalf("expected unmatched quotes to be detected")
	}
	if len(got) != 0 {
		t.Fatalf("expected unmatched token to be dropped, got %v", got)
	}
}

func TestValidateResticKeepTagTemplatesStrict(t *testing.T) {
	if err := validateResticKeepTagTemplatesStrict(`"role:primary,critical env:prod`); err == nil {
		t.Fatalf("expected error for unmatched quotes")
	}
	if err := validateResticKeepTagTemplatesStrict(`line:adhoc env:prod`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newTestClusterWithSessionMap(sessionMap map[string]string) *Cluster {
	cluster := &Cluster{
		Conf:                  &config.Config{},
		BackupMetaMap:         backupmgr.NewBackupMetaMap(),
		snapshotMetadataCache: newSnapshotMetadataCache(),
	}
	for snapshotID, sessionID := range sessionMap {
		if snapshotID == "" || sessionID == "" {
			continue
		}
		key := fmt.Sprintf("%d|%s", backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault)
		summary := &SnapshotMetadataSummary{
			BackupMethod:     "logical",
			BackupLine:       backupmgr.BackupLineDefault,
			StartTime:        time.Now(),
			BackupSessionID:  sessionID,
			ResticSnapshotID: snapshotID,
		}
		cluster.snapshotMetadataCache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
			entry.Status = snapshotMetadataStatusReady
			entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
		})
	}
	return cluster
}

func TestFilterMostRecentSnapshotsPerSession(t *testing.T) {
	baseTime := time.Now()

	tests := []struct {
		name     string
		input    []backupmgr.BackupSnapshot
		expected int // expected number of snapshots after filtering
	}{
		{
			name:     "Empty list",
			input:    []backupmgr.BackupSnapshot{},
			expected: 0,
		},
		{
			name: "Single snapshot",
			input: []backupmgr.BackupSnapshot{
				{
					Id:   "snap1",
					Time: baseTime.Format(time.RFC3339),
				},
			},
			expected: 1,
		},
		{
			name: "Multiple snapshots, different sessions",
			input: []backupmgr.BackupSnapshot{
				{
					Id:   "snap1",
					Time: baseTime.Format(time.RFC3339),
				},
				{
					Id:   "snap2",
					Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				},
			},
			expected: 2,
		},
		{
			name: "Multiple snapshots, same session - keep newest",
			input: []backupmgr.BackupSnapshot{
				{
					Id:   "snap1",
					Time: baseTime.Format(time.RFC3339),
				},
				{
					Id:   "snap2",
					Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				},
				{
					Id:   "snap3",
					Time: baseTime.Add(2 * time.Hour).Format(time.RFC3339),
				},
			},
			expected: 1, // Only the newest (snap3) should remain
		},
		{
			name: "Mixed legacy and modern snapshots",
			input: []backupmgr.BackupSnapshot{
				{
					Id:   "legacy1",
					Time: baseTime.Format(time.RFC3339),
				},
				{
					Id:   "legacy2",
					Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				},
				{
					Id:   "modern1",
					Time: baseTime.Add(2 * time.Hour).Format(time.RFC3339),
				},
				{
					Id:   "modern2",
					Time: baseTime.Add(3 * time.Hour).Format(time.RFC3339),
				},
			},
			expected: 3, // 2 legacy (treated separately) + 1 modern (grouped)
		},
		{
			name: "Multiple sessions with multiple snapshots each",
			input: []backupmgr.BackupSnapshot{
				{
					Id:   "snap1",
					Time: baseTime.Format(time.RFC3339),
				},
				{
					Id:   "snap2",
					Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				},
				{
					Id:   "snap3",
					Time: baseTime.Format(time.RFC3339),
				},
				{
					Id:   "snap4",
					Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339),
				},
			},
			expected: 2, // One per session
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionMap := map[string]string{}
			switch tt.name {
			case "Single snapshot":
				sessionMap["snap1"] = "session1"
			case "Multiple snapshots, different sessions":
				sessionMap["snap1"] = "session1"
				sessionMap["snap2"] = "session2"
			case "Multiple snapshots, same session - keep newest":
				sessionMap["snap1"] = "session1"
				sessionMap["snap2"] = "session1"
				sessionMap["snap3"] = "session1"
			case "Mixed legacy and modern snapshots":
				sessionMap["modern1"] = "session1"
				sessionMap["modern2"] = "session1"
			case "Multiple sessions with multiple snapshots each":
				sessionMap["snap1"] = "session1"
				sessionMap["snap2"] = "session1"
				sessionMap["snap3"] = "session2"
				sessionMap["snap4"] = "session2"
			}
			cluster := newTestClusterWithSessionMap(sessionMap)
			result := FilterMostRecentSnapshotsPerSession(cluster, tt.input)
			if len(result) != tt.expected {
				t.Errorf("FilterMostRecentSnapshotsPerSession() returned %d snapshots, want %d", len(result), tt.expected)
			}

			// Verify sorting (newest first)
			for i := 1; i < len(result); i++ {
				prevTime, _ := time.Parse(time.RFC3339, result[i-1].Time)
				currTime, _ := time.Parse(time.RFC3339, result[i].Time)
				if prevTime.Before(currTime) {
					t.Errorf("Result not sorted correctly: snapshot at index %d is older than snapshot at index %d", i-1, i)
				}
			}
		})
	}
}

func TestFilterMostRecentSnapshotsPerSession_VerifyNewestKept(t *testing.T) {
	baseTime := time.Now()

	input := []backupmgr.BackupSnapshot{
		{
			Id:       "snap1",
			Time:     baseTime.Format(time.RFC3339),
			ShortId:  "snap1abc",
			Hostname: "host1",
		},
		{
			Id:       "snap2",
			Time:     baseTime.Add(2 * time.Hour).Format(time.RFC3339), // Newest
			ShortId:  "snap2def",
			Hostname: "host1",
		},
		{
			Id:       "snap3",
			Time:     baseTime.Add(1 * time.Hour).Format(time.RFC3339),
			ShortId:  "snap3ghi",
			Hostname: "host1",
		},
	}

	cluster := newTestClusterWithSessionMap(map[string]string{
		"snap1": "session1",
		"snap2": "session1",
		"snap3": "session1",
	})
	result := FilterMostRecentSnapshotsPerSession(cluster, input)

	if len(result) != 1 {
		t.Fatalf("Expected 1 snapshot, got %d", len(result))
	}

	// Verify that snap2 (the newest) was kept
	if result[0].Id != "snap2" {
		t.Errorf("Expected newest snapshot (snap2) to be kept, got %s", result[0].Id)
	}

	if result[0].ShortId != "snap2def" {
		t.Errorf("Expected ShortId 'snap2def', got %s", result[0].ShortId)
	}
}

func TestReconcileSnapshotMetadata(t *testing.T) {
	// Test basic reconciliation report structure
	report := &ReconciliationReport{
		OrphanedMetadata: []string{"/path/to/orphaned.json"},
		MissingMetadata:  []string{"abc123def456"},
		Timestamp:        time.Now(),
		CleanedUp:        false,
	}

	if len(report.OrphanedMetadata) != 1 {
		t.Errorf("Expected 1 orphaned metadata, got %d", len(report.OrphanedMetadata))
	}

	if len(report.MissingMetadata) != 1 {
		t.Errorf("Expected 1 missing metadata, got %d", len(report.MissingMetadata))
	}

	if report.CleanedUp {
		t.Error("Expected CleanedUp to be false")
	}

	if report.Timestamp.IsZero() {
		t.Error("Expected non-zero Timestamp")
	}
}

func TestReconcileSnapshotMetadata_EmptyReport(t *testing.T) {
	// Test empty report (no drift detected)
	report := &ReconciliationReport{
		OrphanedMetadata: make([]string, 0),
		MissingMetadata:  make([]string, 0),
		Timestamp:        time.Now(),
		CleanedUp:        false,
	}

	if len(report.OrphanedMetadata) != 0 {
		t.Errorf("Expected 0 orphaned metadata, got %d", len(report.OrphanedMetadata))
	}

	if len(report.MissingMetadata) != 0 {
		t.Errorf("Expected 0 missing metadata, got %d", len(report.MissingMetadata))
	}
}
