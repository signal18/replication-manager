package cluster

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
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

func TestResolveResticMountDirFromConfigStrictAllowsDefaultBaseDir(t *testing.T) {
	tmpDir := t.TempDir()
	cluster := &Cluster{
		Name:       "cluster1",
		WorkingDir: tmpDir,
		Conf:       &config.Config{},
	}

	mountDir, source, err := cluster.ResolveResticMountDirFromConfigStrict()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := filepath.Join(tmpDir, resticDefaultMountSubdir)
	if source != "default" {
		t.Fatalf("expected source default, got %q", source)
	}
	if mountDir != expected {
		t.Fatalf("expected mount dir %q, got %q", expected, mountDir)
	}
}

func TestSanitizeAndValidateResticMountOptionsAllowsDefaultBaseTargetDir(t *testing.T) {
	tmpDir := t.TempDir()
	cluster := &Cluster{
		Name:       "cluster1",
		WorkingDir: tmpDir,
		Conf:       &config.Config{},
	}

	defaultMountDir := filepath.Join(tmpDir, resticDefaultMountSubdir)
	mountOpt := backupmgr.NewResticMountOption(defaultMountDir)
	err := cluster.sanitizeAndValidateResticMountOptions(&mountOpt, resticMountOptionMeta{
		mountDirSource:  "default",
		targetDirSource: "default",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mountOpt.TargetDir != defaultMountDir {
		t.Fatalf("expected target dir %q, got %q", defaultMountDir, mountOpt.TargetDir)
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

func TestBuildResticS3RepoSpecAppendCluster(t *testing.T) {
	repo, prefix := buildResticS3RepoSpec("https://s3.example.com", "bucket", "base", "cluster1", true)
	if repo != "s3:https://s3.example.com/bucket/base/cluster1" {
		t.Fatalf("unexpected repo path: %s", repo)
	}
	if prefix != "base/cluster1" {
		t.Fatalf("unexpected prefix: %s", prefix)
	}

	repo, prefix = buildResticS3RepoSpec("", "bucket", "base", "cluster1", false)
	if repo != "s3:bucket/base" {
		t.Fatalf("unexpected repo path: %s", repo)
	}
	if prefix != "base" {
		t.Fatalf("unexpected prefix: %s", prefix)
	}

	repo, prefix = buildResticS3RepoSpec("", "cluster1", "", "cluster1", true)
	if repo != "s3:cluster1" {
		t.Fatalf("unexpected repo path: %s", repo)
	}
	if prefix != "" {
		t.Fatalf("unexpected prefix: %s", prefix)
	}

	repo, prefix = buildResticS3RepoSpec("", "bucket", "base/cluster1", "cluster1", true)
	if repo != "s3:bucket/base/cluster1" {
		t.Fatalf("unexpected repo path: %s", repo)
	}
	if prefix != "base/cluster1" {
		t.Fatalf("unexpected prefix: %s", prefix)
	}
}

func TestResolveResticRepoAppendClusterGuards(t *testing.T) {
	conf := &config.Config{BackupResticRepoAppendCluster: false}
	cluster := &Cluster{Conf: conf, Name: "cluster1"}
	cluster.Conf.WorkingDir = "/var/lib/repman"

	localRepo, shouldAppend := resolveResticRepoPolicy(conf, "", cluster)
	if !shouldAppend {
		t.Fatalf("expected guardrail to force append when local repo is empty")
	}
	if localRepo != "" {
		t.Fatalf("expected local repo to remain empty when not configured")
	}

	conf.BackupResticRepository = ""
	conf.BackupResticAws = true
	localRepo, shouldAppend = resolveResticRepoPolicy(conf, conf.BackupResticLocalRepository, cluster)
	if shouldAppend {
		t.Fatalf("expected append to remain disabled when AWS restic is enabled")
	}
	if localRepo != "" {
		t.Fatalf("expected local repo to remain empty when AWS restic is enabled")
	}
	conf.BackupResticAws = false

	defaultParent := filepath.Join(cluster.Conf.WorkingDir, config.ConstStreamingSubDir, "archive")
	conf.BackupResticLocalRepository = defaultParent
	localRepo, shouldAppend = resolveResticRepoPolicy(conf, conf.BackupResticLocalRepository, cluster)
	if !shouldAppend {
		t.Fatalf("expected guardrail to force append when local repo is within default parent")
	}
	if localRepo != "" {
		t.Fatalf("expected local repo to be rejected when within default parent")
	}

	conf.BackupResticLocalRepository = filepath.Join(defaultParent, "cluster1")
	localRepo, shouldAppend = resolveResticRepoPolicy(conf, conf.BackupResticLocalRepository, cluster)
	if !shouldAppend {
		t.Fatalf("expected guardrail to force append when local repo is within default parent")
	}
	if localRepo != "" {
		t.Fatalf("expected local repo to be rejected when within default parent subdir")
	}

	conf.BackupResticLocalRepository = "/custom/restic"
	localRepo, shouldAppend = resolveResticRepoPolicy(conf, conf.BackupResticLocalRepository, cluster)
	if shouldAppend {
		t.Fatalf("expected append to remain disabled with custom local repo")
	}
	if localRepo != "/custom/restic" {
		t.Fatalf("expected local repo to be accepted when outside default parent")
	}

	conf.BackupResticRepoAppendCluster = true
	conf.BackupResticLocalRepository = "/custom/cluster1"
	localRepo, shouldAppend = resolveResticRepoPolicy(conf, conf.BackupResticLocalRepository, cluster)
	if !shouldAppend {
		t.Fatalf("expected append to remain enabled with custom local repo")
	}
	if localRepo != "/custom/cluster1" {
		t.Fatalf("expected local repo to be accepted when outside default parent")
	}
}

func TestResticS3EffectivePrefixForInit(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{}, Name: "cluster1"}
	cluster.Conf.WorkingDir = "/var/lib/repman"
	cluster.Conf.BackupResticAws = true
	cluster.Conf.BackupResticAwsBucket = "bucket"
	cluster.Conf.BackupResticAwsPrefix = ""
	cluster.Conf.BackupResticRepoAppendCluster = true

	effective, ok := cluster.ResticS3EffectivePrefixForInit()
	if !ok {
		t.Fatalf("expected effective prefix to be available")
	}
	if effective != "cluster1" {
		t.Fatalf("expected effective prefix cluster1, got %s", effective)
	}

	cluster.Conf.BackupResticAwsPrefix = "base"
	effective, ok = cluster.ResticS3EffectivePrefixForInit()
	if !ok {
		t.Fatalf("expected effective prefix to be available")
	}
	if effective != "base/cluster1" {
		t.Fatalf("expected effective prefix base/cluster1, got %s", effective)
	}

	cluster.Conf.BackupResticAwsPrefix = "base/cluster1"
	effective, ok = cluster.ResticS3EffectivePrefixForInit()
	if ok {
		t.Fatalf("expected no update when prefix already ends with cluster name")
	}
}

func TestCheckResticErrors_InitTaskWithCanInitRepo(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()

	conf := &config.Config{BackupRestic: true}
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.CanInitRepo = true
	rm.SetError(backupmgr.InitTask, errors.New("repository initialization required: repo config is missing"))

	cluster := &Cluster{
		Name:          "test",
		Conf:          conf,
		StateMachine:  sm,
		ResticManager: rm,
	}

	cluster.CheckResticErrors()

	if !cluster.StateMachine.IsInState("WARN0095") {
		t.Fatalf("expected WARN0095 to be set after CheckResticErrors with InitTask error")
	}

	st, ok := (*cluster.StateMachine.OldState)["WARN0095"]
	if !ok {
		t.Fatalf("expected WARN0095 entry in state machine")
	}
	if !strings.Contains(st.ErrDesc, "initialization required") {
		t.Fatalf("expected WARN0095 ErrDesc to contain 'initialization required', got %q", st.ErrDesc)
	}
}

// TestCheckResticErrors_WARN0095Lifecycle verifies that WARN0095 stays open
// across monitor cycles while an init issue persists, produces no reopen churn
// on the second cycle, and resolves exactly once after the issue is cleared.
func TestCheckResticErrors_WARN0095Lifecycle(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()

	conf := &config.Config{BackupRestic: true}
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.SetError(backupmgr.InitTask, errors.New("repo config is missing"))

	cluster := &Cluster{
		Name:          "test",
		Conf:          conf,
		StateMachine:  sm,
		ResticManager: rm,
	}

	// Cycle 1: issue present → WARN0095 opens.
	cluster.CheckResticErrors()
	sm.ClearState() // OldState = CurState (has WARN0095), CurState = empty

	if !sm.IsInState("WARN0095") {
		t.Fatal("cycle 1: expected WARN0095 to be open")
	}

	// Cycle 2: issue still present → WARN0095 must remain open with no reopen event.
	cluster.CheckResticErrors() // sets CurState again
	newlyOpened := sm.GetLastOpenedStates()
	if _, reopened := newlyOpened["WARN0095"]; reopened {
		t.Fatal("cycle 2: WARN0095 should not reopen (no churn expected)")
	}
	sm.ClearState()

	if !sm.IsInState("WARN0095") {
		t.Fatal("cycle 2: expected WARN0095 to remain open")
	}

	// Simulate init success: clear both TaskErrors[InitTask] and lastInitError.
	rm.FetchAndClearError(backupmgr.InitTask)
	rm.ClearInitErrorBackoffManual()

	// Cycle 3: no issue → WARN0095 must resolve.
	cluster.CheckResticErrors() // does NOT set WARN0095
	resolved := sm.GetLastResolvedStates()
	found := false
	for _, s := range resolved {
		if s.ErrKey == "WARN0095" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("cycle 3: expected WARN0095 to appear in resolved states")
	}
	sm.ClearState()

	if sm.IsInState("WARN0095") {
		t.Fatal("cycle 3: expected WARN0095 to be gone after init success")
	}
}

// TestCheckResticErrors_WARN0095ResolvesWithoutPstates30 proves that WARN0095
// resolves in the very next cycle after the init issue clears, without relying
// on pstates30 preservation to carry the state. This is the regression guard
// for removing WARN0095 from pstates30.
func TestCheckResticErrors_WARN0095ResolvesWithoutPstates30(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()

	conf := &config.Config{BackupRestic: true}
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.SetError(backupmgr.InitTask, errors.New("repo config is missing"))

	cluster := &Cluster{
		Name:          "test",
		Conf:          conf,
		StateMachine:  sm,
		ResticManager: rm,
	}

	// Cycle 1: issue present → open WARN0095, then end cycle.
	cluster.CheckResticErrors()
	sm.ClearState()

	// Clear the init issue before cycle 2 (no pstates30 preservation applied).
	rm.FetchAndClearError(backupmgr.InitTask)
	rm.ClearInitErrorBackoffManual()

	// Cycle 2: CheckResticErrors does not set WARN0095; it must resolve immediately.
	cluster.CheckResticErrors()
	resolved := sm.GetLastResolvedStates()
	found := false
	for _, s := range resolved {
		if s.ErrKey == "WARN0095" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected WARN0095 to resolve in the same cycle the issue clears, without pstates30 preservation")
	}
}

// TestCheckResticErrors_TransientErrorsCleared verifies that non-init task errors
// (FetchTask, PurgeTask, UnlockTask) are consumed after one cycle and do not
// prevent the state from resolving the following cycle.
func TestCheckResticErrors_TransientErrorsCleared(t *testing.T) {
	cases := []struct {
		name     string
		taskType backupmgr.TaskType
		warnKey  string
	}{
		{"fetch", backupmgr.FetchTask, "WARN0093"},
		{"purge", backupmgr.PurgeTask, "WARN0094"},
		{"unlock", backupmgr.UnlockTask, "WARN0095"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sm := new(state.StateMachine)
			sm.Init()
			conf := &config.Config{BackupRestic: true}
			rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
			rm.SetError(tc.taskType, errors.New("transient error"))

			cluster := &Cluster{
				Name:          "test",
				Conf:          conf,
				StateMachine:  sm,
				ResticManager: rm,
			}

			// Cycle 1: error present → warning set.
			cluster.CheckResticErrors()
			sm.ClearState()
			if !sm.IsInState(tc.warnKey) {
				t.Fatalf("cycle 1: expected %s to be open", tc.warnKey)
			}

			// Cycle 2: error consumed, not re-raised → warning resolves.
			cluster.CheckResticErrors()
			resolved := sm.GetLastResolvedStates()
			found := false
			for _, s := range resolved {
				if s.ErrKey == tc.warnKey {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("cycle 2: expected %s to resolve after transient error cleared", tc.warnKey)
			}
		})
	}
}

// --- ResticCopyRepoWithOptions unit tests ---

func newCopyTestCluster(t *testing.T, resticEnabled bool) *Cluster {
	t.Helper()
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.PauseWorker()
	t.Cleanup(rm.ShutdownWorker)
	return &Cluster{
		Name:          "test-cluster",
		Conf:          &config.Config{BackupRestic: resticEnabled},
		ResticManager: rm,
	}
}

func TestResticCopyRepoWithOptionsResticDisabled(t *testing.T) {
	cluster := newCopyTestCluster(t, false)
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/some/path",
			Password:   "pass",
		},
	}
	if err := cluster.ResticCopyRepoWithOptions(opt); err == nil {
		t.Fatal("expected error when restic is disabled, got nil")
	}
}

func TestResticCopyRepoWithOptionsInvalidMode(t *testing.T) {
	cluster := newCopyTestCluster(t, true)
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       "invalid-mode",
			Repository: "/some/path",
			Password:   "pass",
		},
	}
	if err := cluster.ResticCopyRepoWithOptions(opt); err == nil {
		t.Fatal("expected error for invalid source mode, got nil")
	}
}

func TestResticCopyRepoWithOptionsMissingPassword(t *testing.T) {
	cluster := newCopyTestCluster(t, true)
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/some/path",
		},
	}
	if err := cluster.ResticCopyRepoWithOptions(opt); err == nil {
		t.Fatal("expected error for missing source password, got nil")
	}
}

func TestResticCopyRepoWithOptionsMalformedSFTP(t *testing.T) {
	cluster := newCopyTestCluster(t, true)
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticSftp,
			Repository: "not-sftp-format",
			Password:   "pass",
		},
	}
	err := cluster.ResticCopyRepoWithOptions(opt)
	if err == nil {
		t.Fatal("expected error for malformed SFTP repo, got nil")
	}
	if !strings.Contains(err.Error(), "sftp:") {
		t.Errorf("expected error to mention sftp format, got: %v", err)
	}
}

func TestResticCopyRepoWithOptionsChunkerWithoutInit(t *testing.T) {
	cluster := newCopyTestCluster(t, true)
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/some/path",
			Password:   "pass",
		},
		CopyChunkerParams: true,
		InitDestination:   false,
	}
	err := cluster.ResticCopyRepoWithOptions(opt)
	if err == nil {
		t.Fatal("expected error when copy_chunker_params=true without init_destination, got nil")
	}
	if !strings.Contains(err.Error(), "copy_chunker_params") {
		t.Errorf("expected error to mention copy_chunker_params, got: %v", err)
	}
}

func TestResticCopyRepoWithOptionsValidQueues(t *testing.T) {
	cluster := newCopyTestCluster(t, true) // worker paused + shutdown registered
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/some/src",
			Password:   "pass",
		},
	}
	if err := cluster.ResticCopyRepoWithOptions(opt); err != nil {
		t.Fatalf("unexpected error for valid options: %v", err)
	}
	cluster.ResticManager.Mutex.Lock()
	qlen := len(cluster.ResticManager.TaskQueue)
	var firstType backupmgr.TaskType
	if qlen > 0 {
		firstType = cluster.ResticManager.TaskQueue[0].Type
	}
	cluster.ResticManager.Mutex.Unlock()
	if qlen == 0 {
		t.Fatal("expected copy task to be queued, got empty queue")
	}
	if firstType != backupmgr.CopyTask {
		t.Errorf("expected CopyTask type, got %v", firstType)
	}
}

// TestResticCopyRepoWithOptionsG4Preflight verifies that copy_chunker_params=true
// against an already-initialized destination is rejected synchronously (before
// queueing), not silently inside the worker. Requires the restic binary.
func TestResticCopyRepoWithOptionsG4Preflight(t *testing.T) {
	resticPath, err := exec.LookPath("restic")
	if err != nil {
		t.Skip("restic binary not found, skipping integration test")
	}

	base := t.TempDir()
	dstDir := filepath.Join(base, "dst")
	cacheDir := filepath.Join(base, "cache")
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		t.Fatal(err)
	}

	rm := backupmgr.NewResticRepo(resticPath, nil, config.ConstLogModRestic)
	rm.SetEnv([]string{
		"RESTIC_REPOSITORY=" + dstDir,
		"RESTIC_PASSWORD=dstpass",
		"RESTIC_CACHE_DIR=" + cacheDir,
		"HOME=" + base,
	})
	defer rm.ShutdownWorker()
	if err := rm.InitRepo(false); err != nil {
		t.Fatalf("init dst repo: %v", err)
	}
	if err := rm.FetchRepo(); err != nil {
		t.Fatalf("fetch dst repo: %v", err)
	}

	cluster := &Cluster{
		Name:          "test-cluster",
		Conf:          &config.Config{BackupRestic: true},
		ResticManager: rm,
	}
	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:       config.ConstBackupArchiveModeResticLocal,
			Repository: "/some/src",
			Password:   "srcpass",
		},
		InitDestination:   true,
		CopyChunkerParams: true,
	}
	copyErr := cluster.ResticCopyRepoWithOptions(opt)
	if copyErr == nil {
		t.Fatal("expected error for G4 preflight violation, got nil")
	}
	if !strings.Contains(copyErr.Error(), "already initialized") {
		t.Errorf("expected 'already initialized' in error, got: %v", copyErr)
	}
	// Verify nothing was queued — the rejection happened before AddCopyTask.
	rm.Mutex.Lock()
	for _, task := range rm.TaskQueue {
		if task.Type == backupmgr.CopyTask {
			rm.Mutex.Unlock()
			t.Error("copy task was queued despite preflight rejection")
			return
		}
	}
	rm.Mutex.Unlock()
}

// --- saved-source resolution tests ---

// newSavedSourceCluster builds a minimal cluster configured for saved-source tests.
// password and awsSecret are stored in conf.Secrets so GetDecryptedValue returns them.
func newSavedSourceCluster(t *testing.T, conf *config.Config, password, awsSecret string) *Cluster {
	t.Helper()
	if conf.Secrets == nil {
		conf.Secrets = make(map[string]config.Secret)
	}
	conf.Secrets["backup-restic-password"] = config.Secret{Value: password}
	conf.Secrets["backup-restic-aws-access-secret"] = config.Secret{Value: awsSecret}
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.PauseWorker()
	t.Cleanup(rm.ShutdownWorker)
	return &Cluster{
		Name:          "mycluster",
		Conf:          conf,
		ResticManager: rm,
	}
}

// TestSavedS3SourceAppendClusterFalseNonAwsDest verifies that when the current
// destination is NOT S3 (BackupResticAws=false) and BackupResticRepoAppendCluster=false,
// the saved S3 preset does NOT append the cluster name to the prefix.
// This is the core regression case: resolveResticRepoPolicy used to force
// appendCluster=true when BackupResticAws was false, corrupting the saved S3 prefix.
func TestSavedS3SourceAppendClusterFalseNonAwsDest(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:                true,
		BackupResticAws:             false, // current destination is local, not S3
		BackupResticRepoAppendCluster: false,
		BackupResticAwsBucket:       "mybucket",
		BackupResticAwsPrefix:       "myprefix",
		BackupResticAwsEndpoint:     "https://s3.example.com",
		BackupResticAwsRegion:       "us-east-1",
		BackupResticAwsAccessKeyId:  "AKID",
	}, "resticpass", "secretkey")

	resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.AWS == nil {
		t.Fatal("expected AWS config in resolved option, got nil")
	}
	// With appendCluster=false and cluster name not already in bucket/prefix,
	// the effective prefix should be exactly the stored prefix with no cluster suffix.
	if resolved.AWS.Prefix != "myprefix" {
		t.Errorf("expected prefix %q, got %q (cluster name must not be appended when append=false)", "myprefix", resolved.AWS.Prefix)
	}
	if resolved.AWS.Bucket != "mybucket" {
		t.Errorf("expected bucket %q, got %q", "mybucket", resolved.AWS.Bucket)
	}
	if resolved.Password != "resticpass" {
		t.Errorf("expected password resolved from config, got %q", resolved.Password)
	}
}

// TestSavedS3SourceAppendClusterTrueAddsClusterName verifies that when
// BackupResticRepoAppendCluster=true and the cluster name is not yet in the prefix,
// the saved S3 resolution appends it exactly once.
func TestSavedS3SourceAppendClusterTrueAddsClusterName(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:                true,
		BackupResticAws:             false, // current destination is local — should not affect S3 source
		BackupResticRepoAppendCluster: true,
		BackupResticAwsBucket:       "mybucket",
		BackupResticAwsPrefix:       "backup",
		BackupResticAwsEndpoint:     "",
		BackupResticAwsRegion:       "eu-west-1",
		BackupResticAwsAccessKeyId:  "AKID",
	}, "pass", "secret")

	resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "backup/" + cluster.Name
	if resolved.AWS.Prefix != want {
		t.Errorf("expected prefix %q, got %q", want, resolved.AWS.Prefix)
	}
}

// TestSavedS3SourceFreezeOnQueueTime verifies that after ResticCopyRepoWithOptions
// resolves and queues a saved-source task, mutating the cluster config does not
// change the copy option already stored in the queue.
func TestSavedS3SourceFreezeOnQueueTime(t *testing.T) {
	conf := &config.Config{
		BackupRestic:                true,
		BackupResticAws:             false,
		BackupResticRepoAppendCluster: true,
		BackupResticAwsBucket:       "original-bucket",
		BackupResticAwsPrefix:       "pfx",
		BackupResticAwsRegion:       "us-east-1",
		BackupResticAwsAccessKeyId:  "AKID",
	}
	cluster := newSavedSourceCluster(t, conf, "original-pass", "original-secret")

	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:           config.ConstBackupArchiveModeResticAws,
			UseSavedConfig: true,
		},
	}
	if err := cluster.ResticCopyRepoWithOptions(opt); err != nil {
		t.Fatalf("unexpected error queueing saved-source task: %v", err)
	}

	// Mutate config after queueing.
	conf.BackupResticAwsBucket = "changed-bucket"
	conf.Secrets["backup-restic-password"] = config.Secret{Value: "changed-pass"}

	// Read the queued task option.
	cluster.ResticManager.Mutex.Lock()
	var queued *backupmgr.ResticCopyOption
	for _, task := range cluster.ResticManager.TaskQueue {
		if task.Type == backupmgr.CopyTask {
			queued = task.CopyOpt
			break
		}
	}
	cluster.ResticManager.Mutex.Unlock()

	if queued == nil {
		t.Fatal("no copy task in queue")
	}
	if queued.Source.AWS == nil {
		t.Fatal("expected AWS config in queued task, got nil")
	}
	if queued.Source.AWS.Bucket != "original-bucket" {
		t.Errorf("queued task bucket changed: got %q, want %q (config mutation must not affect frozen queue entry)", queued.Source.AWS.Bucket, "original-bucket")
	}
	if queued.Source.Password != "original-pass" {
		t.Errorf("queued task password changed: got %q (config mutation must not affect frozen queue entry)", queued.Source.Password)
	}
}

// TestSavedS3SourceRejectsHybridPayload verifies that a request combining
// use_saved_config=true with inline manual fields is rejected.
func TestSavedS3SourceRejectsHybridPayload(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:          true,
		BackupResticAwsBucket: "mybucket",
	}, "pass", "secret")

	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:           config.ConstBackupArchiveModeResticAws,
			UseSavedConfig: true,
			Password:       "inline-password", // hybrid: inline field present
		},
	}
	err := cluster.ResticCopyRepoWithOptions(opt)
	if err == nil {
		t.Fatal("expected error for hybrid saved-source payload, got nil")
	}
	if !strings.Contains(err.Error(), "use_saved_config") {
		t.Errorf("expected error to mention use_saved_config, got: %v", err)
	}
}

// TestSavedS3SourceRejectsWhenNeitherStructuredNorLegacy verifies that the saved S3
// preset fails with a clear error when both backup-restic-aws-bucket and
// backup-restic-repository (S3 URL) are absent or unusable.
func TestSavedS3SourceRejectsWhenNeitherStructuredNorLegacy(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:            true,
		BackupResticAwsBucket:   "",            // no structured bucket
		BackupResticRepository:  "/local/path", // not an S3 URL
	}, "pass", "secret")

	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:           config.ConstBackupArchiveModeResticAws,
			UseSavedConfig: true,
		},
	}
	err := cluster.ResticCopyRepoWithOptions(opt)
	if err == nil {
		t.Fatal("expected error when neither structured nor legacy S3 config is present, got nil")
	}
	if !strings.Contains(err.Error(), "backup-restic-aws-bucket") || !strings.Contains(err.Error(), "backup-restic-repository") {
		t.Errorf("expected error to mention both config fields, got: %v", err)
	}
}

// TestSavedS3SourceLegacyFallbackResolvesSuccessfully verifies that the saved S3 preset
// resolves from backup-restic-repository when backup-restic-aws-bucket is absent.
func TestSavedS3SourceLegacyFallbackResolvesSuccessfully(t *testing.T) {
	cases := []struct {
		name           string
		legacyURL      string
		wantBucket     string
		wantEndpoint   string
		wantPrefixHint string // substring that must appear in resolved prefix (or empty for any)
	}{
		{
			name:       "bucket only",
			legacyURL:  "s3:mybucket",
			wantBucket: "mybucket",
		},
		{
			name:           "bucket with prefix",
			legacyURL:      "s3:mybucket/myprefix",
			wantBucket:     "mybucket",
			wantPrefixHint: "myprefix",
		},
		{
			name:         "hostname endpoint",
			legacyURL:    "s3:s3.amazonaws.com/mybucket",
			wantEndpoint: "s3.amazonaws.com",
			wantBucket:   "mybucket",
		},
		{
			name:           "hostname endpoint with prefix",
			legacyURL:      "s3:s3.amazonaws.com/mybucket/pfx",
			wantEndpoint:   "s3.amazonaws.com",
			wantBucket:     "mybucket",
			wantPrefixHint: "pfx",
		},
		{
			name:         "https endpoint",
			legacyURL:    "s3:https://minio.example.com/mybucket",
			wantEndpoint: "https://minio.example.com",
			wantBucket:   "mybucket",
		},
		{
			name:           "https endpoint with prefix",
			legacyURL:      "s3:https://minio.example.com/mybucket/pfx",
			wantEndpoint:   "https://minio.example.com",
			wantBucket:     "mybucket",
			wantPrefixHint: "pfx",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cluster := newSavedSourceCluster(t, &config.Config{
				BackupRestic:                  true,
				BackupResticAwsBucket:         "", // force legacy path
				BackupResticRepository:        tc.legacyURL,
				BackupResticRepoAppendCluster: false,
			}, "resticpass", "awssecret")

			resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resolved.AWS == nil {
				t.Fatal("expected AWS config, got nil")
			}
			if resolved.AWS.Bucket != tc.wantBucket {
				t.Errorf("bucket: got %q, want %q", resolved.AWS.Bucket, tc.wantBucket)
			}
			if resolved.AWS.Endpoint != tc.wantEndpoint {
				t.Errorf("endpoint: got %q, want %q", resolved.AWS.Endpoint, tc.wantEndpoint)
			}
			if tc.wantPrefixHint != "" && !strings.Contains(resolved.AWS.Prefix, tc.wantPrefixHint) {
				t.Errorf("prefix %q does not contain expected hint %q", resolved.AWS.Prefix, tc.wantPrefixHint)
			}
			if resolved.Password != "resticpass" {
				t.Errorf("password: got %q", resolved.Password)
			}
		})
	}
}

// TestSavedS3SourceStructuredPrecedenceOverLegacy verifies that when both
// backup-restic-aws-bucket and backup-restic-repository are configured, the
// structured bucket config is used and the legacy URL is ignored.
func TestSavedS3SourceStructuredPrecedenceOverLegacy(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:                  true,
		BackupResticAwsBucket:         "structured-bucket",
		BackupResticAwsPrefix:         "structured-prefix",
		BackupResticRepository:        "s3:legacy-bucket/legacy-prefix",
		BackupResticRepoAppendCluster: false,
	}, "pass", "secret")

	resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.AWS == nil {
		t.Fatal("expected AWS config, got nil")
	}
	if resolved.AWS.Bucket != "structured-bucket" {
		t.Errorf("expected structured bucket, got %q", resolved.AWS.Bucket)
	}
	if strings.Contains(resolved.AWS.Prefix, "legacy") {
		t.Errorf("legacy prefix leaked into resolved config: %q", resolved.AWS.Prefix)
	}
}

// TestSavedS3SourceRejectsUnsupportedMode verifies that use_saved_config=true with
// mode restic-sftp is rejected (sftp is local-preset territory, not saved-S3).
func TestSavedS3SourceRejectsUnsupportedMode(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic: true,
	}, "pass", "")

	opt := backupmgr.ResticCopyOption{
		Source: backupmgr.ResticCopySourceOption{
			Mode:           config.ConstBackupArchiveModeResticSftp,
			UseSavedConfig: true,
		},
	}
	err := cluster.ResticCopyRepoWithOptions(opt)
	if err == nil {
		t.Fatal("expected error for unsupported mode with use_saved_config, got nil")
	}
}

// ── backup-restic-s3-mode resolver tests ──────────────────────────────────────

func newS3ModeCluster(conf *config.Config) *Cluster {
	if conf.Secrets == nil {
		conf.Secrets = make(map[string]config.Secret)
		conf.Secrets["backup-restic-aws-access-secret"] = config.Secret{Value: "secret"}
		conf.Secrets["backup-restic-password"] = config.Secret{Value: "pass"}
	}
	conf.BackupResticAws = true
	return &Cluster{Name: "mycluster", Conf: conf}
}

func TestResolveResticS3_AutoResolvesToNew(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:            config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:         "mybucket",
		BackupResticAwsPrefix:         "myprefix",
		BackupResticAwsEndpoint:       "https://s3.example.com",
		BackupResticRepoAppendCluster: false,
	})
	res, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mode != config.ConstResticS3ModeNew {
		t.Errorf("expected mode=new, got %q", res.Mode)
	}
	if res.Bucket != "mybucket" {
		t.Errorf("expected bucket=mybucket, got %q", res.Bucket)
	}
	if res.Endpoint != "https://s3.example.com" {
		t.Errorf("expected endpoint, got %q", res.Endpoint)
	}
}

func TestResolveResticS3_AutoResolvesToLegacy(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:            config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:         "", // no bucket → fall back
		BackupResticRepository:        "s3:https://s3.example.com/legacybucket/prefix",
		BackupResticRepoAppendCluster: false,
	})
	res, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mode != config.ConstResticS3ModeLegacy {
		t.Errorf("expected mode=legacy, got %q", res.Mode)
	}
	if res.Bucket != "legacybucket" {
		t.Errorf("expected bucket=legacybucket, got %q", res.Bucket)
	}
}

func TestResolveResticS3_AutoFailsWhenNeitherConfigured(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:     config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:  "",
		BackupResticRepository: "", // not an S3 URL
	})
	_, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err == nil {
		t.Fatal("expected error when neither bucket nor S3 URL is configured")
	}
}

func TestResolveResticS3_NewIgnoresLegacyURL(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:            config.ConstResticS3ModeNew,
		BackupResticAwsBucket:         "newbucket",
		BackupResticRepository:        "s3:legacy-bucket/prefix", // should be ignored
		BackupResticRepoAppendCluster: false,
	})
	res, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mode != config.ConstResticS3ModeNew {
		t.Errorf("expected mode=new, got %q", res.Mode)
	}
	if res.Bucket != "newbucket" {
		t.Errorf("expected newbucket, got %q", res.Bucket)
	}
	if strings.Contains(res.RepoPath, "legacy") {
		t.Errorf("legacy URL leaked into resolved path: %q", res.RepoPath)
	}
}

func TestResolveResticS3_NewFailsWhenNoBucket(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:     config.ConstResticS3ModeNew,
		BackupResticAwsBucket:  "",
		BackupResticRepository: "s3:legacy-bucket/prefix",
	})
	_, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err == nil {
		t.Fatal("expected error for mode=new with no bucket")
	}
}

func TestResolveResticS3_LegacyIgnoresNewFields(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:            config.ConstResticS3ModeLegacy,
		BackupResticAwsBucket:         "newbucket", // present but must be ignored
		BackupResticRepository:        "s3:legacy-bucket/prefix",
		BackupResticRepoAppendCluster: false,
	})
	res, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Mode != config.ConstResticS3ModeLegacy {
		t.Errorf("expected mode=legacy, got %q", res.Mode)
	}
	if res.Bucket != "legacy-bucket" {
		t.Errorf("expected legacy-bucket, got %q", res.Bucket)
	}
	if strings.Contains(res.RepoPath, "newbucket") {
		t.Errorf("new bucket leaked into resolved path: %q", res.RepoPath)
	}
}

func TestResolveResticS3_LegacyFailsForNonS3URL(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:     config.ConstResticS3ModeLegacy,
		BackupResticRepository: "/local/path/to/repo",
	})
	_, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err == nil {
		t.Fatal("expected error for mode=legacy with non-S3 repository URL")
	}
}

func TestResolveResticS3_AppendCluster(t *testing.T) {
	cluster := newS3ModeCluster(&config.Config{
		BackupResticS3Mode:            config.ConstResticS3ModeNew,
		BackupResticAwsBucket:         "mybucket",
		BackupResticAwsPrefix:         "",
		BackupResticRepoAppendCluster: true,
	})
	res, err := resolveResticS3(cluster.Conf, cluster.Name, cluster)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Prefix != "mycluster" {
		t.Errorf("expected prefix=mycluster, got %q", res.Prefix)
	}
	if !strings.HasSuffix(res.RepoPath, "/mycluster") {
		t.Errorf("expected repo path to end with /mycluster, got %q", res.RepoPath)
	}
}

// ── boot promotion tests ──────────────────────────────────────────────────────

func newBootCluster(conf *config.Config) (*Cluster, *backupmgr.ResticManager) {
	if conf.Secrets == nil {
		conf.Secrets = make(map[string]config.Secret)
		conf.Secrets["backup-restic-aws-access-secret"] = config.Secret{Value: "secret"}
		conf.Secrets["backup-restic-password"] = config.Secret{Value: "pass"}
	}
	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.PauseWorker()
	cluster := &Cluster{
		Name:          "mycluster",
		Conf:          conf,
		ResticManager: rm,
	}
	return cluster, rm
}

func TestPromoteResticS3Mode_AutoToNew(t *testing.T) {
	cluster, _ := newBootCluster(&config.Config{
		BackupResticAws:               true,
		BackupResticS3Mode:            config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:         "mybucket",
		BackupResticRepoAppendCluster: false,
	})
	cluster.promoteResticS3ModeOnBootSuccess()
	if cluster.Conf.BackupResticS3Mode != config.ConstResticS3ModeNew {
		t.Errorf("expected mode=new after promotion, got %q", cluster.Conf.BackupResticS3Mode)
	}
}

func TestPromoteResticS3Mode_AutoToLegacy(t *testing.T) {
	cluster, _ := newBootCluster(&config.Config{
		BackupResticAws:               true,
		BackupResticS3Mode:            config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:         "",
		BackupResticRepository:        "s3:legacy-bucket/prefix",
		BackupResticRepoAppendCluster: false,
	})
	cluster.promoteResticS3ModeOnBootSuccess()
	if cluster.Conf.BackupResticS3Mode != config.ConstResticS3ModeLegacy {
		t.Errorf("expected mode=legacy after promotion, got %q", cluster.Conf.BackupResticS3Mode)
	}
}

func TestPromoteResticS3Mode_AlreadyNewSkips(t *testing.T) {
	cluster, _ := newBootCluster(&config.Config{
		BackupResticAws:       true,
		BackupResticS3Mode:    config.ConstResticS3ModeNew,
		BackupResticAwsBucket: "mybucket",
	})
	cluster.promoteResticS3ModeOnBootSuccess()
	// Should remain "new", not re-derive.
	if cluster.Conf.BackupResticS3Mode != config.ConstResticS3ModeNew {
		t.Errorf("expected mode=new unchanged, got %q", cluster.Conf.BackupResticS3Mode)
	}
}

func TestPromoteResticS3Mode_NonS3Skips(t *testing.T) {
	cluster, _ := newBootCluster(&config.Config{
		BackupResticAws:    false, // not S3
		BackupResticS3Mode: config.ConstResticS3ModeAuto,
	})
	cluster.promoteResticS3ModeOnBootSuccess()
	// Should remain auto; not S3, so no promotion.
	if cluster.Conf.BackupResticS3Mode != config.ConstResticS3ModeAuto {
		t.Errorf("expected mode=auto unchanged for non-S3, got %q", cluster.Conf.BackupResticS3Mode)
	}
}

func TestPromoteResticS3Mode_HookClearsItself(t *testing.T) {
	cluster, rm := newBootCluster(&config.Config{
		BackupResticAws:               true,
		BackupResticS3Mode:            config.ConstResticS3ModeAuto,
		BackupResticAwsBucket:         "mybucket",
		BackupResticRepoAppendCluster: false,
	})
	rm.OnBootFetchSuccess = func() {
		cluster.promoteResticS3ModeOnBootSuccess()
	}
	// Call twice to verify the hook clears itself after the first invocation.
	cluster.promoteResticS3ModeOnBootSuccess()
	cluster.Conf.BackupResticS3Mode = config.ConstResticS3ModeAuto // reset manually
	cluster.promoteResticS3ModeOnBootSuccess()                      // second call should be no-op (hook already nil)
	// The hook should have been cleared on the first call; the second should be a no-op.
	if rm.OnBootFetchSuccess != nil {
		t.Error("expected OnBootFetchSuccess to be nil after first invocation")
	}
}

// ── config normalization tests ────────────────────────────────────────────────

func TestNormalizeResticS3Mode_EmptyBecomesAuto(t *testing.T) {
	conf := &config.Config{BackupResticS3Mode: ""}
	conf.NormalizeResticS3Mode()
	if conf.BackupResticS3Mode != config.ConstResticS3ModeAuto {
		t.Errorf("expected auto, got %q", conf.BackupResticS3Mode)
	}
}

func TestNormalizeResticS3Mode_InvalidBecomesAuto(t *testing.T) {
	conf := &config.Config{BackupResticS3Mode: "invalid-value"}
	conf.NormalizeResticS3Mode()
	if conf.BackupResticS3Mode != config.ConstResticS3ModeAuto {
		t.Errorf("expected auto, got %q", conf.BackupResticS3Mode)
	}
}

func TestNormalizeResticS3Mode_ValidUnchanged(t *testing.T) {
	for _, mode := range []string{"auto", "new", "legacy"} {
		conf := &config.Config{BackupResticS3Mode: mode}
		conf.NormalizeResticS3Mode()
		if conf.BackupResticS3Mode != mode {
			t.Errorf("mode %q changed to %q after normalization", mode, conf.BackupResticS3Mode)
		}
	}
}

// ── saved-source copy selector alignment test ─────────────────────────────────

func TestSavedS3SourceObeysS3ModeNew(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:                  true,
		BackupResticS3Mode:            config.ConstResticS3ModeNew,
		BackupResticAwsBucket:         "newbucket",
		BackupResticRepository:        "s3:legacy-bucket/prefix",
		BackupResticRepoAppendCluster: false,
	}, "pass", "secret")

	resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.AWS == nil {
		t.Fatal("expected AWS config, got nil")
	}
	if resolved.AWS.Bucket != "newbucket" {
		t.Errorf("expected newbucket, got %q", resolved.AWS.Bucket)
	}
	if strings.Contains(resolved.AWS.Bucket, "legacy") {
		t.Errorf("legacy bucket leaked into resolved config: %q", resolved.AWS.Bucket)
	}
}

func TestSavedS3SourceObeysS3ModeLegacy(t *testing.T) {
	cluster := newSavedSourceCluster(t, &config.Config{
		BackupRestic:                  true,
		BackupResticS3Mode:            config.ConstResticS3ModeLegacy,
		BackupResticAwsBucket:         "newbucket", // present but must be ignored
		BackupResticRepository:        "s3:legacy-bucket/legacy-prefix",
		BackupResticRepoAppendCluster: false,
	}, "pass", "secret")

	resolved, err := cluster.resticResolveSavedCopySource(config.ConstBackupArchiveModeResticAws, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved.AWS == nil {
		t.Fatal("expected AWS config, got nil")
	}
	if resolved.AWS.Bucket != "legacy-bucket" {
		t.Errorf("expected legacy-bucket, got %q", resolved.AWS.Bucket)
	}
	if strings.Contains(resolved.AWS.Bucket, "new") {
		t.Errorf("new bucket leaked into resolved config: %q", resolved.AWS.Bucket)
	}
}
