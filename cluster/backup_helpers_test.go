// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

func TestNormalizeBackupLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"Default lowercase", "default", backupmgr.BackupLineDefault},
		{"Default uppercase", "DEFAULT", backupmgr.BackupLineDefault},
		{"Default with spaces", "  default  ", backupmgr.BackupLineDefault},
		{"Default with hyphens", "de-fault", backupmgr.BackupLineDefault},
		{"Adhoc lowercase", "adhoc", backupmgr.BackupLineAdhoc},
		{"Adhoc uppercase", "ADHOC", backupmgr.BackupLineAdhoc},
		{"Adhoc with spaces", "  adhoc  ", backupmgr.BackupLineAdhoc},
		{"Adhoc with hyphens", "ad-hoc", backupmgr.BackupLineAdhoc},
		{"Invalid input", "invalid", ""},
		{"Empty string", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeBackupLine(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeBackupLine(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestParseAdhocMetaFileID(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		expectedID int64
		expectedOK bool
	}{
		{"Valid adhoc file", "mysqldump.1234567890.meta.json", 1234567890, true},
		{"Valid with tool name", "mariabackup.9876543210.meta.json", 9876543210, true},
		{"Invalid - no ID", "mysqldump.meta.json", 0, false},
		{"Invalid - not numeric", "mysqldump.abc.meta.json", 0, false},
		{"Invalid - wrong extension", "mysqldump.123.meta.txt", 0, false},
		{"Invalid - no extension", "mysqldump.123", 0, false},
		{"Edge case - zero ID", "tool.0.meta.json", 0, true},
		{"Edge case - negative (invalid)", "tool.-123.meta.json", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, ok := parseAdhocMetaFileID(tt.filename)
			if id != tt.expectedID || ok != tt.expectedOK {
				t.Errorf("parseAdhocMetaFileID(%q) = (%d, %v), want (%d, %v)",
					tt.filename, id, ok, tt.expectedID, tt.expectedOK)
			}
		})
	}
}

func TestParseBackupToolFromMetaFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{"Mysqldump default", "mysqldump.meta.json", "mysqldump"},
		{"Mysqldump adhoc", "mysqldump.1234567890.meta.json", "mysqldump"},
		{"Mariabackup default", "mariabackup.meta.json", "mariabackup"},
		{"Xtrabackup adhoc", "xtrabackup.9876543210.meta.json", "xtrabackup"},
		{"Invalid - no extension", "mysqldump", ""},
		{"Invalid - wrong extension", "mysqldump.meta.txt", ""},
		{"Invalid - no dot separator", "mysqldumpmeta.json", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseBackupToolFromMetaFilename(tt.filename)
			if result != tt.expected {
				t.Errorf("parseBackupToolFromMetaFilename(%q) = %q, want %q",
					tt.filename, result, tt.expected)
			}
		})
	}
}

func TestRetentionDeadline(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name      string
		meta      *backupmgr.BackupMetadata
		expectOK  bool
		checkDiff time.Duration // Expected time difference from now
	}{
		{
			name: "Valid with EndTime",
			meta: &backupmgr.BackupMetadata{
				EndTime:       now.Add(-48 * time.Hour),
				RetentionDays: 7,
			},
			expectOK:  true,
			checkDiff: (7*24 - 48) * time.Hour,
		},
		{
			name: "Valid with StartTime only",
			meta: &backupmgr.BackupMetadata{
				StartTime:     now.Add(-24 * time.Hour),
				RetentionDays: 3,
			},
			expectOK:  true,
			checkDiff: (3*24 - 24) * time.Hour,
		},
		{
			name: "Valid with ID as timestamp",
			meta: &backupmgr.BackupMetadata{
				Id:            now.Add(-12 * time.Hour).Unix(),
				RetentionDays: 1,
			},
			expectOK:  true,
			checkDiff: 12 * time.Hour,
		},
		{
			name: "Invalid - ID not a timestamp",
			meta: &backupmgr.BackupMetadata{
				Id:            12345,
				RetentionDays: 1,
			},
			expectOK: false,
		},
		{
			name: "Invalid - zero retention",
			meta: &backupmgr.BackupMetadata{
				EndTime:       now,
				RetentionDays: 0,
			},
			expectOK: false,
		},
		{
			name: "Invalid - negative retention",
			meta: &backupmgr.BackupMetadata{
				EndTime:       now,
				RetentionDays: -1,
			},
			expectOK: false,
		},
		{
			name: "Invalid - no timestamp",
			meta: &backupmgr.BackupMetadata{
				RetentionDays: 7,
			},
			expectOK: false,
		},
		{
			name:     "Invalid - nil metadata",
			meta:     nil,
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deadline, ok := retentionDeadline(tt.meta)
			if ok != tt.expectOK {
				t.Errorf("retentionDeadline() ok = %v, want %v", ok, tt.expectOK)
			}
			if tt.expectOK {
				if deadline.IsZero() {
					t.Error("Expected non-zero deadline")
				}
				// Check that deadline is approximately correct (within 1 second tolerance)
				expectedDeadline := now.Add(tt.checkDiff)
				diff := deadline.Sub(expectedDeadline)
				if diff < -time.Second || diff > time.Second {
					t.Errorf("Deadline difference too large: %v (expected within 1s)", diff)
				}
			}
		})
	}
}

func TestBuildBackupMetaFileName(t *testing.T) {
	cluster := &Cluster{
		Name:   "test-cluster",
		Conf:   &config.Config{WorkingDir: t.TempDir()},
		Logrus: logrus.New(),
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	backupDir := server.GetMyBackupDirectory()

	tests := []struct {
		name       string
		backupTool string
		backupID   int64
		line       string
		expectFile string
	}{
		{
			name:       "Default line",
			backupTool: "mysqldump",
			backupID:   0,
			line:       "default",
			expectFile: "mysqldump.meta.json",
		},
		{
			name:       "Adhoc line with ID",
			backupTool: "mariabackup",
			backupID:   1234567890,
			line:       "adhoc",
			expectFile: "mariabackup.1234567890.meta.json",
		},
		{
			name:       "Adhoc normalized",
			backupTool: "xtrabackup",
			backupID:   9999,
			line:       "ad-hoc",
			expectFile: "xtrabackup.9999.meta.json",
		},
		{
			name:       "Adhoc without ID uses default",
			backupTool: "mydumper",
			backupID:   0,
			line:       "adhoc",
			expectFile: "mydumper.meta.json",
		},
		{
			name:       "Empty tool",
			backupTool: "",
			backupID:   123,
			line:       "default",
			expectFile: "",
		},
		{
			name:       "Invalid tool path",
			backupTool: "../mysqldump",
			backupID:   123,
			line:       "default",
			expectFile: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := server.buildBackupMetaFileName(tt.backupTool, tt.backupID, tt.line)
			if tt.expectFile == "" {
				if result != "" {
					t.Errorf("Expected empty result, got %q", result)
				}
			} else {
				expectedPath := filepath.Join(backupDir, tt.expectFile)
				if result != expectedPath {
					t.Errorf("buildBackupMetaFileName() = %q, expected %q", result, expectedPath)
				}
			}
		})
	}
}

func TestBackupMetaFilePathRespectsDefaultNaming(t *testing.T) {
	cluster := &Cluster{
		Name:   "test-cluster",
		Conf:   &config.Config{WorkingDir: t.TempDir()},
		Logrus: logrus.New(),
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	backupDir := server.GetMyBackupDirectory()

	defaultMeta := &backupmgr.BackupMetadata{
		BackupTool: "mysqldump",
		BackupLine: backupmgr.BackupLineDefault,
		MetaFile:   "custom.meta.json",
	}
	defaultPath := server.backupMetaFilePath(defaultMeta)
	defaultExpected := filepath.Join(backupDir, "mysqldump.meta.json")
	if defaultPath != defaultExpected {
		t.Fatalf("default metadata path = %q, want %q", defaultPath, defaultExpected)
	}

	adhocMeta := &backupmgr.BackupMetadata{
		BackupTool: "mysqldump",
		BackupLine: backupmgr.BackupLineAdhoc,
		MetaFile:   "custom.meta.json",
	}
	adhocPath := server.backupMetaFilePath(adhocMeta)
	adhocExpected := filepath.Join(backupDir, "custom.meta.json")
	if adhocPath != adhocExpected {
		t.Fatalf("adhoc metadata path = %q, want %q", adhocPath, adhocExpected)
	}
}

func TestShouldUncompressOnSenderForReseed(t *testing.T) {
	cluster := &Cluster{Conf: &config.Config{}}
	if !cluster.shouldUncompressOnSenderForReseed() {
		t.Fatalf("expected default to uncompress on sender")
	}
	cluster.Conf.BackupReseedRemoteDecompress = true
	if cluster.shouldUncompressOnSenderForReseed() {
		t.Fatalf("expected remote decompress to disable sender uncompress")
	}
}

func newBackupLineCluster(t *testing.T) (*Cluster, *ServerMonitor, *ServerMonitor, *ServerMonitor) {
	t.Helper()

	sm := &state.StateMachine{}
	sm.Init()
	sm.Discovered = true

	cluster := &Cluster{
		Name:         "test-cluster",
		Conf:         &config.Config{},
		StateMachine: sm,
		Logrus:       logrus.New(),
	}

	master := &ServerMonitor{
		Id:           "1",
		URL:          "master:3306",
		State:        stateMaster,
		ClusterGroup: cluster,
	}
	backup := &ServerMonitor{
		Id:             "2",
		URL:            "backup:3306",
		State:          stateSlave,
		PreferedBackup: true,
		ClusterGroup:   cluster,
	}
	other := &ServerMonitor{
		Id:           "3",
		URL:          "other:3306",
		State:        stateSlave,
		ClusterGroup: cluster,
	}

	cluster.master = master
	cluster.Servers = serverList{master, backup, other}

	return cluster, master, backup, other
}

func TestResolveBackupLine(t *testing.T) {
	tests := []struct {
		name     string
		server   *ServerMonitor
		opts     BackupRunOptions
		expected string
	}{
		{
			name:     "Default with retention forces adhoc",
			server:   nil,
			opts:     BackupRunOptions{Line: "default", RetentionDays: 7},
			expected: backupmgr.BackupLineAdhoc,
		},
		{
			name:     "Explicit adhoc",
			server:   nil,
			opts:     BackupRunOptions{Line: "adhoc"},
			expected: backupmgr.BackupLineAdhoc,
		},
		{
			name:     "Default on master stays default",
			server:   nil,
			opts:     BackupRunOptions{Line: "default"},
			expected: backupmgr.BackupLineDefault,
		},
		{
			name:     "Default on backup server stays default",
			server:   nil,
			opts:     BackupRunOptions{Line: "default"},
			expected: backupmgr.BackupLineDefault,
		},
		{
			name:     "Default on other server forces adhoc",
			server:   nil,
			opts:     BackupRunOptions{Line: "default"},
			expected: backupmgr.BackupLineAdhoc,
		},
		{
			name:     "Empty line on other server forces adhoc",
			server:   nil,
			opts:     BackupRunOptions{},
			expected: backupmgr.BackupLineAdhoc,
		},
	}

	_, master, backup, other := newBackupLineCluster(t)

	for i := range tests {
		switch tests[i].name {
		case "Default on master stays default":
			tests[i].server = master
		case "Default on backup server stays default":
			tests[i].server = backup
		case "Default on other server forces adhoc", "Empty line on other server forces adhoc":
			tests[i].server = other
		default:
			tests[i].server = master
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.server.resolveBackupLine(tt.opts)
			if result != tt.expected {
				t.Errorf("resolveBackupLine() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestShouldRunRestic(t *testing.T) {
	tests := []struct {
		name             string
		resticConfigured bool
		resticEnabled    *bool
		clusterNil       bool
		expected         bool
	}{
		{
			name:             "Configured and enabled",
			resticConfigured: true,
			resticEnabled:    nil,
			expected:         true,
		},
		{
			name:             "Configured and explicitly enabled",
			resticConfigured: true,
			resticEnabled:    boolPtr(true),
			expected:         true,
		},
		{
			name:             "Configured but explicitly disabled",
			resticConfigured: true,
			resticEnabled:    boolPtr(false),
			expected:         false,
		},
		{
			name:             "Not configured, no override",
			resticConfigured: false,
			resticEnabled:    nil,
			expected:         false,
		},
		{
			name:             "Not configured, explicit enable (should be false)",
			resticConfigured: false,
			resticEnabled:    boolPtr(true),
			expected:         false,
		},
		{
			name:       "Cluster is nil",
			clusterNil: true,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &ServerMonitor{}
			if !tt.clusterNil {
				cluster := &Cluster{
					Name:   "test-cluster",
					Conf:   &config.Config{BackupRestic: tt.resticConfigured},
					Logrus: logrus.New(),
				}
				server.ClusterGroup = cluster
				server.URL = "server:3306"
			}
			opts := BackupRunOptions{
				ResticEnabled: tt.resticEnabled,
			}
			result := server.shouldRunRestic(opts)
			if result != tt.expected {
				t.Errorf("shouldRunRestic() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestReadBackupMetadataFile(t *testing.T) {
	// Create a temporary test file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.meta.json")

	testData := `{
		"id": 1234567890,
		"backupTool": "mysqldump",
		"backupMethod": 1,
		"completed": true,
		"retentionDays": 7
	}`

	err := os.WriteFile(testFile, []byte(testData), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	meta, err := readBackupMetadataFile(testFile)
	if err != nil {
		t.Fatalf("readBackupMetadataFile() error = %v", err)
	}

	if meta.Id != 1234567890 {
		t.Errorf("meta.Id = %d, want 1234567890", meta.Id)
	}
	if meta.BackupTool != "mysqldump" {
		t.Errorf("meta.BackupTool = %q, want 'mysqldump'", meta.BackupTool)
	}
	if !meta.Completed {
		t.Error("meta.Completed = false, want true")
	}
	if meta.RetentionDays != 7 {
		t.Errorf("meta.RetentionDays = %d, want 7", meta.RetentionDays)
	}
}

func TestReadBackupMetadataFile_Invalid(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"Invalid JSON", `{invalid json`},
		{"Empty file", ``},
		{"Wrong format", `["array", "not", "object"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "invalid.meta.json")

			err := os.WriteFile(testFile, []byte(tt.content), 0644)
			if err != nil {
				t.Fatalf("Failed to create test file: %v", err)
			}

			_, err = readBackupMetadataFile(testFile)
			if err == nil {
				t.Error("Expected error for invalid metadata file, got nil")
			}
		})
	}
}

func TestReadBackupMetadataFile_NotExists(t *testing.T) {
	_, err := readBackupMetadataFile("/nonexistent/path/file.json")
	if err == nil {
		t.Error("Expected error for non-existent file, got nil")
	}
}

func TestLoadAdhocBackupMetadataReturnsError(t *testing.T) {
	cluster := &Cluster{
		Name:          "test-cluster",
		Conf:          &config.Config{WorkingDir: t.TempDir()},
		StateMachine:  &state.StateMachine{},
		Logrus:        logrus.New(),
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	cluster.StateMachine.Init()
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		URL:          "127.0.0.1:3306",
		ClusterGroup: cluster,
	}

	backupDir := server.GetMyBackupDirectory()
	validPath := filepath.Join(backupDir, "mysqldump.123.meta.json")
	invalidPath := filepath.Join(backupDir, "mysqldump.456.meta.json")

	validData := `{"backupTool":"mysqldump","backupMethod":1,"completed":true,"retentionDays":1}`
	if err := os.WriteFile(validPath, []byte(validData), 0644); err != nil {
		t.Fatalf("failed to write valid metadata: %v", err)
	}
	if err := os.WriteFile(invalidPath, []byte("{invalid"), 0644); err != nil {
		t.Fatalf("failed to write invalid metadata: %v", err)
	}

	metas, err := server.LoadAdhocBackupMetadata()
	if err == nil {
		t.Fatalf("expected error when reading invalid metadata")
	}
	if !strings.Contains(err.Error(), "failed reading") {
		t.Fatalf("expected error summary, got %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 valid metadata, got %d", len(metas))
	}
}

func TestIsPathWithinBase(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "child")

	if !isPathWithinBase(base, child) {
		t.Errorf("expected %s to be within %s", child, base)
	}
	if isPathWithinBase(base, "/tmp") {
		t.Errorf("expected /tmp to be outside %s", base)
	}
}

func TestPurgeExpiredAdhocBackupsConcurrent(t *testing.T) {
	cluster := &Cluster{
		Name:          "test-cluster",
		Conf:          &config.Config{WorkingDir: t.TempDir()},
		StateMachine:  &state.StateMachine{},
		Logrus:        logrus.New(),
		BackupMetaMap: backupmgr.NewBackupMetaMap(),
	}
	cluster.StateMachine.Init()

	servers := serverList{
		{Host: "127.0.0.1", Port: "3306", URL: "127.0.0.1:3306", ClusterGroup: cluster},
		{Host: "127.0.0.2", Port: "3307", URL: "127.0.0.2:3307", ClusterGroup: cluster},
	}
	cluster.Servers = servers

	baseTime := time.Now().Add(-48 * time.Hour)
	type metaPaths struct {
		id       int64
		metaFile string
		destDir  string
	}
	paths := make([]metaPaths, 0, len(servers))

	for i, server := range servers {
		backupDir := server.GetMyBackupDirectory()
		destDir := filepath.Join(backupDir, "adhoc", server.Host)
		if err := os.MkdirAll(destDir, 0755); err != nil {
			t.Fatalf("failed to create dest dir: %v", err)
		}

		id := baseTime.Unix() + int64(i+1)
		meta := &backupmgr.BackupMetadata{
			Id:            id,
			BackupTool:    "mysqldump",
			RetentionDays: 1,
			Completed:     true,
			EndTime:       baseTime,
			Dest:          destDir,
			BackupLine:    backupmgr.BackupLineAdhoc,
			Source:        server.URL,
		}
		metaFile := server.buildBackupMetaFileName(meta.BackupTool, meta.Id, backupmgr.BackupLineAdhoc)
		if metaFile == "" {
			t.Fatalf("failed to build metadata file path")
		}
		payload, err := json.Marshal(meta)
		if err != nil {
			t.Fatalf("failed to marshal metadata: %v", err)
		}
		if err := os.WriteFile(metaFile, payload, 0644); err != nil {
			t.Fatalf("failed to write metadata file: %v", err)
		}

		cluster.BackupMetaMap.Set(meta.Id, meta)
		paths = append(paths, metaPaths{id: meta.Id, metaFile: metaFile, destDir: destDir})
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cluster.PurgeExpiredAdhocBackups()
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cluster.BackupMetaMap.Range(func(_, _ any) bool { return true })
		}()
	}

	wg.Wait()

	for _, entry := range paths {
		if got := cluster.BackupMetaMap.Get(entry.id); got != nil {
			t.Errorf("expected metadata %d to be removed", entry.id)
		}
		if _, err := os.Stat(entry.metaFile); err == nil {
			t.Errorf("expected metadata file %s to be removed", entry.metaFile)
		}
		if _, err := os.Stat(entry.destDir); err == nil {
			t.Errorf("expected dest dir %s to be removed", entry.destDir)
		}
	}
}

// Helper function
func boolPtr(b bool) *bool {
	return &b
}
