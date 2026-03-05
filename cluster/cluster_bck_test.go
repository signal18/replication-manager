package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
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
		{`cluster "env:prod,team:dev"`, []string{`cluster`, `"env:prod,team:dev"`}},
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

func TestSplitResticAdditionalEnvTokensValid(t *testing.T) {
	input := `AWS_SESSION_TOKEN="abc",AWS_DEFAULT_REGION=us-east-1 OTHER=value`
	got, err := splitResticAdditionalEnvTokens(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []string{`AWS_SESSION_TOKEN="abc"`, `AWS_DEFAULT_REGION=us-east-1`, `OTHER=value`}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("unexpected split result: got %v, want %v", got, expected)
	}
}

func TestSplitResticAdditionalEnvTokensUnmatchedQuotes(t *testing.T) {
	_, err := splitResticAdditionalEnvTokens(`AWS_SESSION_TOKEN="abc`)
	if err == nil {
		t.Fatalf("expected error for unmatched quotes")
	}
}

func TestSplitResticAdditionalEnvTokensInvalidTrailingChars(t *testing.T) {
	_, err := splitResticAdditionalEnvTokens(`KEY='single'extra`)
	if err == nil {
		t.Fatalf("expected error for trailing characters after closing quote")
	}
}

func TestParseResticAdditionalEnvOverridesValid(t *testing.T) {
	overrides, allowlist, err := parseResticAdditionalEnvOverrides(`AWS_SESSION_TOKEN="abc" AWS_DEFAULT_REGION=us-east-1 AWS_PROFILE`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if overrides["AWS_SESSION_TOKEN"] != "abc" {
		t.Fatalf("expected session token override, got %q", overrides["AWS_SESSION_TOKEN"])
	}
	if overrides["AWS_DEFAULT_REGION"] != "us-east-1" {
		t.Fatalf("expected default region override, got %q", overrides["AWS_DEFAULT_REGION"])
	}
	if _, ok := allowlist["AWS_PROFILE"]; !ok {
		t.Fatalf("expected allowlist to include AWS_PROFILE")
	}
}

func TestParseResticAdditionalEnvOverridesInvalid(t *testing.T) {
	_, _, err := parseResticAdditionalEnvOverrides(`KEY='single'extra`)
	if err == nil {
		t.Fatalf("expected error for invalid additional env")
	}
}

func TestFilterResticEnvSkipsAwsForNonS3(t *testing.T) {
	baseEnv := []string{
		"PATH=/bin",
		"AWS_DEFAULT_REGION=us-west-2",
		"OTHER=base",
	}
	env := filterResticEnv(nil, baseEnv, "/tmp/repo", "pw", "/tmp/cache", "ak", "sk", "", "AWS_PROFILE=dev AWS_DEFAULT_REGION=us-east-1 OTHER=override")
	if containsEnvPrefix(env, "AWS_PROFILE=") {
		t.Fatalf("expected AWS_PROFILE to be filtered for non-S3 repo")
	}
	if containsEnvPrefix(env, "AWS_DEFAULT_REGION=") {
		t.Fatalf("expected AWS_DEFAULT_REGION to be filtered for non-S3 repo")
	}
	if containsEnvPrefix(env, "AWS_ACCESS_KEY_ID=") || containsEnvPrefix(env, "AWS_SECRET_ACCESS_KEY=") {
		t.Fatalf("expected AWS credentials to be omitted for non-S3 repo")
	}
	if !containsEnvPrefix(env, "OTHER=override") {
		t.Fatalf("expected override for OTHER to be applied")
	}
}

func containsEnvPrefix(env []string, prefix string) bool {
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return true
		}
	}
	return false
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

func TestResticPurgeRepoBuildsOptions(t *testing.T) {
	conf := &config.Config{
		BackupRestic:                              true,
		BackupKeepLast:                            1,
		BackupResticPurgeKeepTag:                  `"role:primary"`,
		BackupResticPurgeHost:                     "host1 host2",
		BackupResticPurgeTag:                      "tag1 tag2",
		BackupResticPurgePath:                     "/data/one /data/two relative",
		BackupResticPurgePrune:                    true,
		BackupResticPurgePruneCompact:             true,
		BackupResticPurgePruneMaxUnused:           "1G",
		BackupResticPurgePruneMaxRepackSize:       "2G",
		BackupResticPurgePruneRepackSmall:         true,
		BackupResticPurgePruneRepackUncompressed:  true,
		BackupResticPurgePruneRepackCacheableOnly: true,
	}
	cluster := &Cluster{
		Name:          "cluster1",
		Conf:          conf,
		ResticManager: backupmgr.NewResticRepo("", nil, config.ConstLogModRestic),
	}

	if err := cluster.ResticPurgeRepoWithOptions(true, true); err != nil {
		t.Fatalf("unexpected purge error: %v", err)
	}
	if !cluster.ResticManager.NeedPurgeNow {
		t.Fatalf("expected purge-now to be scheduled")
	}
	opt := cluster.ResticManager.PurgeNowOption
	if opt.KeepLast != 1 {
		t.Fatalf("expected keep-last 1, got %d", opt.KeepLast)
	}
	if !reflect.DeepEqual(opt.KeepTag, []string{"role:primary"}) {
		t.Fatalf("unexpected keep-tags: %v", opt.KeepTag)
	}
	if !reflect.DeepEqual(opt.Host, []string{"host1", "host2"}) {
		t.Fatalf("unexpected hosts: %v", opt.Host)
	}
	if !reflect.DeepEqual(opt.Tag, []string{"tag1", "tag2"}) {
		t.Fatalf("unexpected tags: %v", opt.Tag)
	}
	if !reflect.DeepEqual(opt.Path, []string{"/data/one", "/data/two"}) {
		t.Fatalf("unexpected paths: %v", opt.Path)
	}
	if !opt.DryRun {
		t.Fatalf("expected dry-run to be enabled")
	}
	if !opt.Prune {
		t.Fatalf("expected prune to remain enabled in dry-run")
	}
	if opt.SnapshotID != "" {
		t.Fatalf("expected snapshot id to be empty, got %q", opt.SnapshotID)
	}
	if !opt.Compact {
		t.Fatalf("expected prune compact to be enabled")
	}
	if opt.PruneOption.MaxUnused != "1G" {
		t.Fatalf("expected max-unused 1G, got %q", opt.PruneOption.MaxUnused)
	}
	if opt.PruneOption.MaxRepackSize != "2G" {
		t.Fatalf("expected max-repack-size 2G, got %q", opt.PruneOption.MaxRepackSize)
	}
	if !opt.PruneOption.RepackCacheableOnly {
		t.Fatalf("expected repack-cacheable-only to be enabled")
	}
	if !opt.PruneOption.RepackSmall {
		t.Fatalf("expected repack-small to be enabled")
	}
	if !opt.PruneOption.RepackUncompressed {
		t.Fatalf("expected repack-uncompressed to be enabled")
	}
}

func TestResticPurgeSnapshotUsesSnapshotID(t *testing.T) {
	conf := &config.Config{
		BackupRestic:           true,
		BackupResticPurgePrune: true,
	}
	cluster := &Cluster{
		Name:          "cluster1",
		Conf:          conf,
		ResticManager: backupmgr.NewResticRepo("", nil, config.ConstLogModRestic),
	}

	if err := cluster.ResticPurgeSnapshotWithOptions(" snap-1 ", true, true); err != nil {
		t.Fatalf("unexpected purge snapshot error: %v", err)
	}
	if !cluster.ResticManager.NeedPurgeNow {
		t.Fatalf("expected purge-now to be scheduled")
	}
	opt := cluster.ResticManager.PurgeNowOption
	if opt.SnapshotID != "snap-1" {
		t.Fatalf("expected snapshot id snap-1, got %q", opt.SnapshotID)
	}
	if !opt.Prune {
		t.Fatalf("expected prune to remain enabled")
	}
	if !opt.DryRun {
		t.Fatalf("expected dry-run to be enabled")
	}
}

func TestResolveResticMountDirFromConfigStrictRejectsDotDot(t *testing.T) {
	tmpDir := t.TempDir()
	cluster := &Cluster{
		Name:       "cluster1",
		WorkingDir: tmpDir,
		Conf: &config.Config{
			BackupResticMountDir: "../etc",
		},
	}
	if _, _, err := cluster.ResolveResticMountDirFromConfigStrict(); err == nil {
		t.Fatalf("expected error for mount dir containing '..'")
	}
}

func TestResolveResticMountDirFromConfigStrictCleansPath(t *testing.T) {
	tmpDir := t.TempDir()
	raw := tmpDir + string(filepath.Separator) + string(filepath.Separator) + "restic"
	cluster := &Cluster{
		Name:       "cluster1",
		WorkingDir: tmpDir,
		Conf: &config.Config{
			BackupResticMountDir: raw,
		},
	}
	mountDir, _, err := cluster.ResolveResticMountDirFromConfigStrict()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, "restic")
	if mountDir != expected {
		t.Fatalf("expected mount dir %q, got %q", expected, mountDir)
	}
}

func TestNormalizeSplitDumpOutputDirDefault(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	baseDirAbs, err := filepath.Abs(filepath.Clean(server.GetMyBackupDirectory()))
	if err != nil {
		t.Fatalf("failed to resolve base dir: %v", err)
	}

	outputDir, err := cluster.normalizeSplitDumpOutputDir(server, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(baseDirAbs, "splitdump")
	if outputDir != expected {
		t.Fatalf("outputdir = %q, want %q", outputDir, expected)
	}
}

func TestNormalizeSplitDumpOutputDirTimestamped(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	baseDirAbs, err := filepath.Abs(filepath.Clean(server.GetMyBackupDirectory()))
	if err != nil {
		t.Fatalf("failed to resolve base dir: %v", err)
	}

	requested := filepath.Join(baseDirAbs, "splitdump.1700000000")
	outputDir, err := cluster.normalizeSplitDumpOutputDir(server, requested)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outputDir != requested {
		t.Fatalf("outputdir = %q, want %q", outputDir, requested)
	}
}

func TestNormalizeSplitDumpOutputDirRelative(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	baseDirAbs, err := filepath.Abs(filepath.Clean(server.GetMyBackupDirectory()))
	if err != nil {
		t.Fatalf("failed to resolve base dir: %v", err)
	}

	outputDir, err := cluster.normalizeSplitDumpOutputDir(server, "splitdump.1700000000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(baseDirAbs, "splitdump.1700000000")
	if outputDir != expected {
		t.Fatalf("outputdir = %q, want %q", outputDir, expected)
	}
}

func TestNormalizeSplitDumpOutputDirRejectsInvalidSuffix(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	baseDirAbs, err := filepath.Abs(filepath.Clean(server.GetMyBackupDirectory()))
	if err != nil {
		t.Fatalf("failed to resolve base dir: %v", err)
	}

	requested := filepath.Join(baseDirAbs, "splitdump.bad")
	if _, err := cluster.normalizeSplitDumpOutputDir(server, requested); err == nil {
		t.Fatalf("expected error for invalid splitdump suffix")
	}
}

func TestNormalizeSplitDumpOutputDirRejectsOutsideBase(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	outsideDir := filepath.Join(t.TempDir(), "splitdump.1700000000")
	if _, err := cluster.normalizeSplitDumpOutputDir(server, outsideDir); err == nil {
		t.Fatalf("expected error for splitdump path outside backup directory")
	}
}

func TestNormalizeSplitDumpOutputDirRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink test not supported on windows")
	}
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	baseDirAbs, err := filepath.Abs(filepath.Clean(server.GetMyBackupDirectory()))
	if err != nil {
		t.Fatalf("failed to resolve base dir: %v", err)
	}
	outsideDir := filepath.Join(t.TempDir(), "outside")
	if err := os.MkdirAll(outsideDir, 0755); err != nil {
		t.Fatalf("failed to create outside dir: %v", err)
	}
	linkPath := filepath.Join(baseDirAbs, "splitdump")
	if err := os.Symlink(outsideDir, linkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	if _, err := cluster.normalizeSplitDumpOutputDir(server, linkPath); err == nil {
		t.Fatalf("expected error for splitdump symlink escape")
	}
}

func TestBuildSnapshotMetadataIndexFromCache(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
	key := fmt.Sprintf("%d|%s", backupmgr.BackupMethodLogical, backupmgr.BackupLineDefault)
	summary := &SnapshotMetadataSummary{
		BackupMethod:     "logical",
		BackupLine:       backupmgr.BackupLineDefault,
		StartTime:        time.Now(),
		BackupSessionID:  "session-1",
		ResticSnapshotID: "snap-1",
	}
	manager.cache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{key: summary}
	})

	snapshots := []backupmgr.BackupSnapshot{
		{
			Id:    "snap-1",
			Time:  time.Now().Format(time.RFC3339Nano),
			Paths: []string{"/backup"},
		},
	}
	index := cluster.BuildSnapshotMetadataIndex(snapshots)
	if len(index) != 1 {
		t.Fatalf("expected index size 1, got %d", len(index))
	}
	entries := index["snap-1"]
	if len(entries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(entries))
	}
	if entries[0].BackupSessionID != "session-1" {
		t.Fatalf("expected session id session-1, got %q", entries[0].BackupSessionID)
	}
}

func newTestClusterWithSessionMap(sessionMap map[string]string) *Cluster {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	manager := cluster.getSnapshotMetadataManager()
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
		manager.cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
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

func TestFilterMostRecentSnapshotsPerSessionWithIndex(t *testing.T) {
	baseTime := time.Now()
	snapshots := []backupmgr.BackupSnapshot{
		{Id: "snap-1", Time: baseTime.Format(time.RFC3339)},
		{Id: "snap-2", Time: baseTime.Add(2 * time.Hour).Format(time.RFC3339)},
		{Id: "snap-3", Time: baseTime.Add(1 * time.Hour).Format(time.RFC3339)},
	}
	index := SnapshotMetadataIndex{
		"snap-1": {{BackupSessionID: "session-a"}},
		"snap-2": {{BackupSessionID: "session-a"}},
		"snap-3": {{BackupSessionID: "session-b"}},
	}
	filtered := FilterMostRecentSnapshotsPerSessionWithIndex(nil, snapshots, index)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(filtered))
	}
	ids := map[string]struct{}{}
	for _, snap := range filtered {
		ids[snap.Id] = struct{}{}
	}
	if _, ok := ids["snap-2"]; !ok {
		t.Fatalf("expected snap-2 to be retained")
	}
	if _, ok := ids["snap-3"]; !ok {
		t.Fatalf("expected snap-3 to be retained")
	}
}

func TestIsSnapshotMetadataCandidatePath(t *testing.T) {
	allowed := map[string]bool{config.ConstBackupLogicalTypeMysqldump: true}
	if !isSnapshotMetadataCandidatePath("/backups/mysqldump.meta.json", allowed) {
		t.Fatalf("expected mysqldump metadata file to match allowed tool")
	}
	if isSnapshotMetadataCandidatePath("/backups/mydumper.meta.json", allowed) {
		t.Fatalf("did not expect mydumper metadata file to match allowed tool")
	}
	if isSnapshotMetadataCandidatePath("/backups/somefile.sql", allowed) {
		t.Fatalf("did not expect non-metadata file to match")
	}
}

func TestResolveSnapshotDestPath(t *testing.T) {
	base := "/var/backups/cluster1"
	path, ok := resolveSnapshotDestPath(base, "/var/backups/cluster1/mysqldump.sql.gz")
	if !ok {
		t.Fatalf("expected absolute dest under base to resolve")
	}
	if path != "/var/backups/cluster1/mysqldump.sql.gz" {
		t.Fatalf("unexpected path %q", path)
	}
	path, ok = resolveSnapshotDestPath(base, "mysqldump.sql.gz")
	if !ok {
		t.Fatalf("expected relative dest to resolve")
	}
	if path != "/var/backups/cluster1/mysqldump.sql.gz" {
		t.Fatalf("unexpected path %q", path)
	}
	_, ok = resolveSnapshotDestPath(base, "/other/path/mysqldump.sql.gz")
	if ok {
		t.Fatalf("expected unrelated absolute path to be rejected")
	}
}

func TestReconcileSnapshotMetadataUsesCacheAndResticEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	cluster := &Cluster{
		Conf:       &config.Config{WorkingDir: tmpDir},
		WorkingDir: tmpDir,
		ResticManager: &backupmgr.ResticManager{Backups: []backupmgr.BackupSnapshot{
			{Id: "snap-1", ShortId: "snap-1", Time: time.Now().Format(time.RFC3339Nano)},
			{Id: "snap-2", ShortId: "snap-2", Time: time.Now().Add(1 * time.Hour).Format(time.RFC3339Nano)},
		}},
	}
	manager := cluster.getSnapshotMetadataManager()
	manager.cache.Update("snap-1", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			"1|default": {ResticSnapshotID: "snap-1", BackupSessionID: "session-1"},
		}
	})
	manager.cache.Update("snap-2", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			"1|default": {ResticSnapshotID: "snap-2", BackupSessionID: "session-2"},
		}
	})
	report, err := cluster.ReconcileSnapshotMetadata()
	if err != nil {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if len(report.MissingMetadata) != 0 {
		t.Fatalf("expected no missing metadata, got %v", report.MissingMetadata)
	}
	if len(report.OrphanedMetadata) != 0 {
		t.Fatalf("expected no orphaned metadata, got %v", report.OrphanedMetadata)
	}
}

func TestMarkSnapshotMetadataPending(t *testing.T) {
	cluster := &Cluster{
		Conf: &config.Config{},
	}
	manager := cluster.getSnapshotMetadataManager()
	entry, started := cluster.markSnapshotMetadataPending("snap-1")
	if !started {
		t.Fatalf("expected pending transition to start")
	}
	if entry == nil || entry.Status != snapshotMetadataStatusPending {
		t.Fatalf("expected pending status, got %v", entry)
	}
	entry, started = cluster.markSnapshotMetadataPending("snap-1")
	if started {
		t.Fatalf("expected no start when already pending")
	}
	manager.cache.Update("snap-2", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusFailed
		entry.LastAttempt = time.Now()
	})
	_, started = cluster.markSnapshotMetadataPending("snap-2")
	if started {
		t.Fatalf("expected retry to be throttled")
	}
	manager.cache.Update("snap-3", func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusFailed
		entry.LastAttempt = time.Now().Add(-snapshotMetadataExtractionRetryInterval * 2)
	})
	entry, started = cluster.markSnapshotMetadataPending("snap-3")
	if !started {
		t.Fatalf("expected retry to start after interval")
	}
	if entry.Status != snapshotMetadataStatusPending {
		t.Fatalf("expected pending status after retry, got %v", entry.Status)
	}
}

func TestSummarizeSnapshotMetadataFromBackupMetaMap(t *testing.T) {
	cluster := &Cluster{
		Conf:          &config.Config{},
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	start := time.Now().Add(-1 * time.Hour)
	meta := &backupmgr.BackupMetadata{
		Id:               1,
		StartTime:        start,
		EndTime:          time.Now(),
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupLine:       backupmgr.BackupLineDefault,
		BackupMethod:     backupmgr.BackupMethodLogical,
		Dest:             filepath.Join("/backups", "snap-1", "mysqldump.sql.gz"),
		ResticSnapshotID: "snap-1",
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	snapshot := &backupmgr.BackupSnapshot{
		Id:    "snap-1",
		Time:  time.Now().Format(time.RFC3339Nano),
		Paths: []string{filepath.Join("/backups", "snap-1")},
	}
	summaries := cluster.SummarizeSnapshotMetadata(snapshot)
	if len(summaries) != 1 {
		t.Fatalf("expected 1 summary, got %d", len(summaries))
	}
	if summaries[0].BackupTool != config.ConstBackupLogicalTypeMysqldump {
		t.Fatalf("unexpected backup tool %q", summaries[0].BackupTool)
	}
	if summaries[0].ResticBasePath != filepath.Join("/backups", "snap-1") {
		t.Fatalf("unexpected base path %q", summaries[0].ResticBasePath)
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
