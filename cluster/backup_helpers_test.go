// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

func TestParseRegisteredMetaFileID(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		expectedID int64
		expectedOK bool
	}{
		{"Valid registered record", "mysqldump.1234567890.meta.json", 1234567890, true},
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
			id, ok := parseRegisteredMetaFileID(tt.filename)
			if id != tt.expectedID || ok != tt.expectedOK {
				t.Errorf("parseRegisteredMetaFileID(%q) = (%d, %v), want (%d, %v)",
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

// the record of the backup of its type is <tool>.meta.json; a registered one's, named by its
// id, <tool>.<id>.meta.json
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
	for _, tt := range []struct {
		name       string
		backupTool string
		backupID   int64
		byID       bool
		expectFile string
	}{
		{"backup of its type", "mysqldump", 0, false, "mysqldump.meta.json"},
		{"registered", "mariabackup", 1234567890, true, "mariabackup.1234567890.meta.json"},
		{"registered without id", "mydumper", 0, true, "mydumper.meta.json"},
		{"empty tool", "", 123, false, ""},
		{"invalid tool path", "../mysqldump", 123, false, ""},
	} {
		result := server.buildBackupMetaFileName(tt.backupTool, tt.backupID, tt.byID)
		want := ""
		if tt.expectFile != "" {
			want = filepath.Join(backupDir, tt.expectFile)
		}
		if result != want {
			t.Errorf("%s: buildBackupMetaFileName() = %q, want %q", tt.name, result, want)
		}
	}
}

// the backup of its type is always recorded as <tool>.meta.json; a registered one keeps the
// record file it has
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

	ofItsType := &backupmgr.BackupMetadata{BackupTool: "mysqldump", MetaFile: "custom.meta.json"}
	if got, want := server.backupMetaFilePath(ofItsType), filepath.Join(backupDir, "mysqldump.meta.json"); got != want {
		t.Fatalf("metadata path of the backup of its type = %q, want %q", got, want)
	}
	registered := &backupmgr.BackupMetadata{BackupTool: "mysqldump", MetaFile: "custom.meta.json", Discovered: true}
	if got, want := server.backupMetaFilePath(registered), filepath.Join(backupDir, "custom.meta.json"); got != want {
		t.Fatalf("metadata path of a registered backup = %q, want %q", got, want)
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

// #2091: the backup a new one of its type replaces stays in the catalogue, only in the archive,
// when a snapshot holds it; held by none, it leaves the catalogue; it is never the backup the
// next one replaces
func TestRetireReplacedBackup(t *testing.T) {
	cluster, master, _, _ := newBackupLineCluster(t)
	cluster.BackupMetaMap = backupmgr.NewBackupMetaMap()
	archived := &backupmgr.BackupMetadata{Id: 1, BackupTool: "mydumper", Source: master.URL, ArchiveRefs: map[string]string{"a1": "aaa"}}
	alone := &backupmgr.BackupMetadata{Id: 2, BackupTool: "mysqldump", Source: master.URL}
	cluster.BackupMetaMap.Set(1, archived)
	cluster.BackupMetaMap.Set(2, alone)
	master.retireReplacedBackup(1)
	master.retireReplacedBackup(2)
	if m := cluster.BackupMetaMap.Get(1); m == nil || !m.OriginalRemoved {
		t.Fatalf("archived backup: %+v", m)
	}
	if cluster.BackupMetaMap.Get(2) != nil {
		t.Fatal("a backup no snapshot holds leaves the catalogue")
	}
	if p := cluster.BackupMetaMap.GetPreviousBackup("mydumper", master.URL); p != nil {
		t.Fatalf("a replaced backup is not replaced again: %+v", p)
	}
}

// a mysqldump backup has one name in the backup directory: mysqldump.sql.gz, or splitdump
func TestResolveMysqldumpDest(t *testing.T) {
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
	dir := server.GetMyBackupDirectory()
	file, split := dir+"mysqldump.sql.gz", filepath.Join(dir, "splitdump")
	for _, tt := range []struct {
		splitDump                         bool
		wantFilename, wantOutputDir, dest string
		compressed                        bool
	}{
		{false, file, "", file, true},
		{true, file, split, split, false},
	} {
		filename, outputdir, dest, compressed := server.resolveMysqldumpDest(tt.splitDump)
		if filename != tt.wantFilename || outputdir != tt.wantOutputDir || dest != tt.dest || compressed != tt.compressed {
			t.Fatalf("split %v: (%q, %q, %q, %v), want (%q, %q, %q, %v)", tt.splitDump, filename, outputdir, dest, compressed, tt.wantFilename, tt.wantOutputDir, tt.dest, tt.compressed)
		}
	}
}

func boolPtr(b bool) *bool { return &b }

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
	// a record written before keeps reading: its retention is no field any more
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

func TestLoadRegisteredBackupMetadataReturnsError(t *testing.T) {
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

	validData := `{"backupTool":"mysqldump","backupMethod":1,"completed":true}`
	if err := os.WriteFile(validPath, []byte(validData), 0644); err != nil {
		t.Fatalf("failed to write valid metadata: %v", err)
	}
	if err := os.WriteFile(invalidPath, []byte("{invalid"), 0644); err != nil {
		t.Fatalf("failed to write invalid metadata: %v", err)
	}

	metas, err := server.LoadRegisteredBackupMetadata()
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

func TestIsActiveBackupMetaSessionMismatch(t *testing.T) {
	target := &backupmgr.BackupMetadata{
		Id:              10,
		Dest:            "/tmp/backup",
		BackupSessionID: "session-a",
	}
	active := &backupmgr.BackupMetadata{
		Id:              11,
		Dest:            "/tmp/backup",
		BackupSessionID: "session-b",
	}
	if isActiveBackupMeta(target, active) {
		t.Fatalf("expected session mismatch to be inactive")
	}
}

func TestIsBackupMetaInProgressUnknownTool(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.InLogicalBackup = true

	active := &backupmgr.BackupMetadata{
		Id:              101,
		BackupTool:      config.ConstBackupLogicalTypeMysqldump,
		Dest:            filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz"),
		BackupSessionID: "session-101",
		Source:          server.URL,
	}
	server.backupMetaMutex.Lock()
	server.LastBackupMeta.Logical = active
	server.backupMetaMutex.Unlock()

	meta := &backupmgr.BackupMetadata{
		Id:              101,
		BackupTool:      "customtool",
		Dest:            active.Dest,
		BackupSessionID: "session-101",
		Source:          server.URL,
	}
	if !cluster.isBackupMetaInProgress(meta, server) {
		t.Fatalf("expected unknown tool backup to be detected as in progress")
	}

	meta.Dest = filepath.Join(server.GetMyBackupDirectory(), "other.sql.gz")
	meta.BackupSessionID = "session-102"
	if cluster.isBackupMetaInProgress(meta, server) {
		t.Fatalf("expected non-matching backup to be inactive")
	}
}

func TestDeleteBackupByIDSuccess(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	backupDir := server.GetMyBackupDirectory()
	dest := filepath.Join(backupDir, "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("payload"), 0644); err != nil {
		t.Fatalf("failed to create backup payload: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		Id:         time.Now().Unix(),
		BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Source:     server.URL,
		Dest:       dest,
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)

	metaPath := server.backupMetaFilePath(meta)
	if err := os.WriteFile(metaPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("failed to create backup metadata: %v", err)
	}

	if _, err := cluster.DeleteBackupByID(meta.Id); err != nil {
		t.Fatalf("unexpected error deleting backup: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected backup payload to be removed")
	}
	if _, err := os.Stat(metaPath); !os.IsNotExist(err) {
		t.Fatalf("expected metadata file to be removed")
	}
	if got := cluster.BackupMetaMap.Get(meta.Id); got != nil {
		t.Fatalf("expected backup metadata to be removed from map")
	}
}

func TestDeleteBackupByIDRefusesDiscoveredBackup(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "discovered.sql.gz")
	if err := os.WriteFile(dest, []byte("client backup"), 0644); err != nil {
		t.Fatalf("create backup payload: %v", err)
	}
	meta := &backupmgr.BackupMetadata{
		Id:         time.Now().UnixNano(),
		BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Source:     server.URL,
		Dest:       dest,
		Discovered: true,
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	metaPath := server.backupMetaFilePath(meta)
	if err := os.WriteFile(metaPath, []byte("{}"), 0644); err != nil {
		t.Fatalf("create metadata: %v", err)
	}

	if _, err := cluster.DeleteBackupByID(meta.Id); !errors.Is(err, ErrBackupDiscovered) {
		t.Fatalf("expected ErrBackupDiscovered, got %v", err)
	}
	if !fileExists(dest) || !fileExists(metaPath) || cluster.BackupMetaMap.Get(meta.Id) == nil {
		t.Fatal("deleting a discovered backup must leave its artifact and metadata unchanged")
	}
}

func TestDeleteBackupByIDRemovesEncryptedIntegritySidecar(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	dest := filepath.Join(server.GetMyBackupDirectory(), "mysqldump.sql.gz.enc")
	if err := os.WriteFile(dest, []byte("ciphertext"), 0600); err != nil {
		t.Fatalf("create encrypted backup: %v", err)
	}
	sidecar := backupmgr.IntegritySidecarPath(dest)
	if err := os.WriteFile(sidecar, []byte("hmac-sha256-v1:"+strings.Repeat("0", 64)+"\n"), 0600); err != nil {
		t.Fatalf("create integrity sidecar: %v", err)
	}
	meta := &backupmgr.BackupMetadata{
		Id:         time.Now().UnixNano(),
		BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Source:     server.URL,
		Dest:       dest,
		Encrypted:  true,
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	if err := os.WriteFile(server.backupMetaFilePath(meta), []byte("{}"), 0600); err != nil {
		t.Fatalf("create metadata: %v", err)
	}
	if _, err := cluster.DeleteBackupByID(meta.Id); err != nil {
		t.Fatalf("delete encrypted backup: %v", err)
	}
	if fileExists(dest) || fileExists(sidecar) {
		t.Fatalf("encrypted backup and integrity sidecar must delete together")
	}
}

func TestDeleteBackupByIDInProgress(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.InLogicalBackup = true
	backupDir := server.GetMyBackupDirectory()
	dest := filepath.Join(backupDir, "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("payload"), 0644); err != nil {
		t.Fatalf("failed to create backup payload: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		Id:         123,
		BackupTool: config.ConstBackupLogicalTypeMysqldump,
		Source:     server.URL,
		Dest:       dest,
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	server.backupMetaMutex.Lock()
	server.LastBackupMeta.Logical = meta
	server.backupMetaMutex.Unlock()

	if _, err := cluster.DeleteBackupByID(meta.Id); !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("expected ErrBackupInProgress, got %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("expected backup payload to remain, got %v", err)
	}
}

func TestDeleteBackupByIDInvalidOrMissing(t *testing.T) {
	cluster, _ := newTestClusterServer(t)
	if _, err := cluster.DeleteBackupByID(0); !errors.Is(err, ErrBackupInvalidID) {
		t.Fatalf("expected ErrBackupInvalidID, got %v", err)
	}
	if _, err := cluster.DeleteBackupByID(999); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("expected ErrBackupNotFound, got %v", err)
	}
}

func newTestClusterServer(t *testing.T) (*Cluster, *ServerMonitor) {
	t.Helper()
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
	cluster.Servers = serverList{server}
	return cluster, server
}

func TestSetLastPhysicalRestoreMeta(t *testing.T) {
	server := &ServerMonitor{}

	meta := &PhysicalRestoreMeta{Vendor: "MySQL", GTID: "1-2-3", BinLogFile: "mysql-bin.000001", BinLogPos: "456"}
	server.SetLastPhysicalRestoreMeta(meta)
	if server.LastPhysicalRestoreMeta != meta {
		t.Fatalf("LastPhysicalRestoreMeta = %+v, want %+v", server.LastPhysicalRestoreMeta, meta)
	}

	// A nil restoreMeta (e.g. unparseable API-mode payload) must not clobber
	// a previously recorded restore.
	server.SetLastPhysicalRestoreMeta(nil)
	if server.LastPhysicalRestoreMeta != meta {
		t.Fatalf("LastPhysicalRestoreMeta was cleared by a nil call: got %+v, want %+v", server.LastPhysicalRestoreMeta, meta)
	}
}

func TestPhysicalRestoreRecoveryMode(t *testing.T) {
	tests := []struct {
		name             string
		v                *version.Version
		meta             *PhysicalRestoreMeta
		wantHasGTID      bool
		wantIsPositional bool
	}{
		{"nil meta", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}, nil, false, false},
		{"MySQL 8.0 with GTID", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}, &PhysicalRestoreMeta{GTID: "1-2-3", BinLogFile: "mysql-bin.000001", BinLogPos: "456"}, true, false},
		{"MySQL 5.6 without GTID but with binlog position", &version.Version{Flavor: "MySQL", Major: 5, Minor: 6}, &PhysicalRestoreMeta{BinLogFile: "mysql-bin.000001", BinLogPos: "456"}, false, true},
		{"Percona 5.6 without GTID but with binlog position", &version.Version{Flavor: "Percona", Major: 5, Minor: 6}, &PhysicalRestoreMeta{BinLogFile: "mysql-bin.000001", BinLogPos: "456"}, false, true},
		{"MySQL without GTID and without binlog position", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}, &PhysicalRestoreMeta{}, false, false},
		{"MariaDB without GTID but with binlog position stays non-positional", &version.Version{Flavor: "MariaDB", Major: 10, Minor: 3}, &PhysicalRestoreMeta{BinLogFile: "mysql-bin.000001", BinLogPos: "456"}, false, false},
		{"nil version with binlog position", nil, &PhysicalRestoreMeta{BinLogFile: "mysql-bin.000001", BinLogPos: "456"}, false, false},
		{"MySQL without GTID, binlog file but no position", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}, &PhysicalRestoreMeta{BinLogFile: "mysql-bin.000001"}, false, false},
		{"MySQL without GTID, binlog position but no file", &version.Version{Flavor: "MySQL", Major: 8, Minor: 0}, &PhysicalRestoreMeta{BinLogPos: "456"}, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hasGTID, isPositional := physicalRestoreRecoveryMode(tt.v, tt.meta)
			if hasGTID != tt.wantHasGTID || isPositional != tt.wantIsPositional {
				t.Fatalf("physicalRestoreRecoveryMode(%+v, %+v) = (%v, %v), want (%v, %v)",
					tt.v, tt.meta, hasGTID, isPositional, tt.wantHasGTID, tt.wantIsPositional)
			}
		})
	}
}

// TestMarkBackupPhysicalDoneRaceWithResticUpdate reproduces the concurrency
// window described in review of the API-mode physical restore path:
// MarkBackupPhysicalDone is now called from the API job-state HTTP handler
// (server/api_database.go), and can run concurrently with the restic
// completion goroutine's UpdateBackupMetadataWithRestic (srv_bck.go), both of
// which read/mutate the same server.LastBackupMeta.Physical. Run with -race;
// before MarkBackupPhysicalDone took backupMetaMutex, this reliably reported
// a data race.
func TestMarkBackupPhysicalDoneRaceWithResticUpdate(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	server.Datadir = t.TempDir()
	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{
		BackupTool: config.ConstBackupPhysicalTypeMariaBackup,
	}
	cluster.ResticManager = backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			server.MarkBackupPhysicalDone(config.ConstBackupPhysicalTypeMariaBackup)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			server.UpdateBackupMetadataWithRestic(backupmgr.BackupMethodPhysical, nil, "snap-id")
		}
	}()
	wg.Wait()

	if !server.LastBackupMeta.Physical.Completed {
		t.Fatalf("expected LastBackupMeta.Physical.Completed=true after MarkBackupPhysicalDone")
	}
	if server.LastBackupMeta.Physical.ResticSnapshotID != "snap-id" {
		t.Fatalf("ResticSnapshotID = %q, want %q", server.LastBackupMeta.Physical.ResticSnapshotID, "snap-id")
	}
}

// TestWaitForBinlogMetaNoDeadlock reproduces the WriteBackupMetadata self-deadlock: the
// metadata writer held backupMetaMutex while polling for lastmeta.BinLogFileName, but that
// field is only ever published by the writelog API path, which takes the SAME mutex. The
// writer therefore blocked forever on a value it was preventing anyone from setting -- this
// wedged belair/db2's rejoin for a day. waitForBinlogMeta must poll with the mutex RELEASED
// so the publisher can make progress, then return holding it again.
func TestWaitForBinlogMetaNoDeadlock(t *testing.T) {
	_, server := newTestClusterServer(t)
	lastmeta := &backupmgr.BackupMetadata{}

	done := make(chan struct{})
	go func() {
		// Match WriteBackupMetadata's caller state: enter holding the mutex.
		server.backupMetaMutex.Lock()
		if !server.waitForBinlogMeta(lastmeta) {
			t.Error("waitForBinlogMeta returned false, want true: the position was published")
		}
		server.backupMetaMutex.Unlock() // helper returns holding it, per its contract
		close(done)
	}()

	// The writelog API path publishes BinLogFileName under the same mutex. With the old code
	// (poll while holding the lock) this Lock() would block forever -> deadlock.
	go func() {
		time.Sleep(100 * time.Millisecond)
		server.backupMetaMutex.Lock()
		lastmeta.BinLogFileName = "binlog.000042"
		server.backupMetaMutex.Unlock()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBinlogMeta did not return within 5s: backupMetaMutex self-deadlock regressed")
	}
}

// TestWaitForBinlogMetaDeadline: a position that never comes (a line the pattern did not match,
// a log pipe that stopped) must not keep the backup slot forever (#2250). The wait returns false
// after its deadline, holding the mutex as its contract says.
func TestWaitForBinlogMetaDeadline(t *testing.T) {
	_, server := newTestClusterServer(t)
	saved := binlogMetaWaitTimeout
	binlogMetaWaitTimeout = 100 * time.Millisecond
	defer func() { binlogMetaWaitTimeout = saved }()

	done := make(chan bool)
	go func() {
		server.backupMetaMutex.Lock()
		got := server.waitForBinlogMeta(&backupmgr.BackupMetadata{})
		server.backupMetaMutex.Unlock()
		done <- got
	}()
	select {
	case got := <-done:
		if got {
			t.Fatal("waitForBinlogMeta returned true with no position, want false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForBinlogMeta did not return after its deadline")
	}
}

// TestParseLogEntriesEmptyGTID: mariabackup on a server without GTID prints an empty GTID; the
// file and position must still be recorded, or the metadata writer waits for them (#2250).
func TestParseLogEntriesEmptyGTID(t *testing.T) {
	_, server := newTestClusterServer(t)
	server.LastBackupMeta.Physical = &backupmgr.BackupMetadata{}
	entry := config.LogEntry{Server: server.URL, Level: config.LvlInfo,
		Log: "[00] 2026-10-30 15:45:39 MySQL binlog position: filename 'binlog.000001', position '43551187', GTID of the last change ''"}
	if err := server.ParseLogEntries(entry, config.ConstLogModTask, "mariabackup"); err != nil {
		t.Fatal(err)
	}
	meta := server.LastBackupMeta.Physical
	if meta.BinLogFileName != "binlog.000001" || meta.BinLogFilePos != 43551187 || meta.BinLogGtid != "" {
		t.Fatalf("position = %q:%d gtid %q, want binlog.000001:43551187 with no GTID", meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid)
	}
}

// TestReseedPointInTimeRefusesBackupWithoutPosition: a PITR with binary logs from a backup with
// no recorded position would replay every binlog from the oldest one over the restored data. It
// is refused before anything is restored (#2256); the scheduler is off here, so a request that
// went further would fail on it instead.
func TestReseedPointInTimeRefusesBackupWithoutPosition(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.BackupMetaMap.Set(7, &backupmgr.BackupMetadata{Id: 7, BackupTool: "mariabackup"})

	err := server.ReseedPointInTime(backupmgr.PointInTimeMeta{Backup: 7, UseBinlog: true})
	if err == nil || !strings.Contains(err.Error(), "no recorded binary log position") {
		t.Fatalf("PITR with binary logs from a backup without position: err = %v, want the position refusal", err)
	}
	err = server.ReseedPointInTime(backupmgr.PointInTimeMeta{Backup: 7})
	if err == nil || strings.Contains(err.Error(), "no recorded binary log position") {
		t.Fatalf("PITR without binary logs: err = %v, want it to go past the position check", err)
	}
}

// TestJobsCheckStatesApiModeSkipsSQL verifies the api-mode guard. In api mode the jobs table
// is not the source of truth, so JobsCheckStates must return before the SQL path. With Conn
// nil and no guard the function would instead return the "No connection pool" error (and in a
// live server spam ERROR 1146 against the non-existent jobs table every tick).
func TestJobsCheckStatesApiModeSkipsSQL(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.Conf.SchedulerJobsMode = "api"

	if err := server.JobsCheckStates(); err != nil {
		t.Fatalf("JobsCheckStates in api mode returned %v, want nil (must skip the SQL path)", err)
	}
}

// A logical backup fills its binlog position while the dump is read; once the dump function
// has returned nothing can publish it. WriteBackupMetadata must therefore not wait for it:
// the wait never ended, kept InLogicalBackup raised and held the global backup slot forever.
func TestWriteBackupMetadataLogicalDoesNotWaitForBinlogPosition(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	cluster.DiskStatManager = misc.NewDiskStatManager()
	server.JobResults = config.NewTasksMap()
	server.JobResults.Set("mysqldump", &config.Task{Task: "mysqldump", State: 3, Done: 1})

	dest := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(dest, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		Id:         1,
		BackupTool: "mysqldump",
		Dest:       dest,
		StartTime:  time.Now(),
	}
	done := make(chan struct{})
	go func() {
		server.WriteBackupMetadata(backupmgr.BackupMethodLogical)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("WriteBackupMetadata kept waiting for a binlog position a finished logical backup can no longer receive")
	}
	if !server.LastBackupMeta.Logical.Completed {
		t.Fatal("a finished logical backup must be written as completed even without a binlog position")
	}
}

// waitSlotsHeld polls until the semaphore holds want slots or the timeout elapses.
func waitSlotsHeld(sem chan struct{}, want int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(sem) == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return len(sem) == want
}

// While another backup of the cluster is running (here a binlog copy, which takes no backup
// slot), a physical backup request waits for it while holding exactly the one slot it took.
// The old code called itself again, which took a second slot while still holding the first:
// with a single slot it waited for its own slot forever, with more it leaked one per retry.
func TestJobBackupPhysicalWaitsHoldingOneSlotAndReleasesWhenAborted(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	sem := make(chan struct{}, 2)
	cluster.ServerGlobals = &ServerGlobals{BackupSemaphore: sem}
	cluster.SetInBinlogBackupState(true)

	done := make(chan error, 1)
	go func() { done <- server.JobBackupPhysicalWithOptions(BackupRunOptions{}) }()

	if !waitSlotsHeld(sem, 1, 5*time.Second) {
		t.Fatalf("holding %d slots, want the one it waits with", len(sem))
	}
	// Past the retry delay the old code had taken a second slot by now.
	time.Sleep(1500 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("returned while another backup was running: %v", err)
	default:
	}
	if got := len(sem); got != 1 {
		t.Fatalf("holding %d slots while waiting, want exactly 1", got)
	}

	cluster.exit.Store(true)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected an error when the cluster is shutting down")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kept waiting after the cluster started shutting down")
	}
	if got := len(sem); got != 0 {
		t.Fatalf("slot not given back after the wait was aborted: %d held", got)
	}
}

// The abort conditions must also be checked on the pass that ends the wait. If the cluster
// started shutting down while the request slept and the backup it waited for ended in the
// meantime, it must not go on to raise InPhysicalBackup and open a receiver; it gives the
// slot back instead (WARN0073 is not open yet, so nothing else would release it).
//
// It ends the awaited backup by clearing a plain bool flag while the request reads it, as the
// monitor does in production, so it is not clean under -race (neither is the rest of this package).
func TestJobBackupPhysicalChecksAbortWhenTheAwaitedBackupEnds(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	sem := make(chan struct{}, 1)
	cluster.ServerGlobals = &ServerGlobals{BackupSemaphore: sem}
	cluster.SetInBinlogBackupState(true)

	done := make(chan error, 1)
	go func() {
		// A request that wrongly goes on would run into the unconfigured test server: report
		// that as a failure of this test instead of crashing the whole test binary.
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Errorf("went on with the backup: %v", r)
			}
		}()
		done <- server.JobBackupPhysicalWithOptions(BackupRunOptions{})
	}()

	if !waitSlotsHeld(sem, 1, 5*time.Second) {
		t.Fatalf("holding %d slots, want the one it waits with", len(sem))
	}
	cluster.exit.Store(true)              // shutdown starts while the request sleeps...
	cluster.SetInBinlogBackupState(false) // ...and the backup it waits for ends

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "shutting down") {
			t.Fatalf("got %v, want a shutting down error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not return")
	}
	if cluster.InPhysicalBackup {
		t.Fatal("raised InPhysicalBackup for a cluster that is shutting down")
	}
	if got := len(sem); got != 0 {
		t.Fatalf("slot not given back: %d held", got)
	}
}

// Same as above for the server going down while the request waits. Like the previous test it
// changes a plain field the monitor also changes in production (server.State), so it is not
// clean under -race.
func TestJobBackupPhysicalGivesSlotBackWhenServerGoesDownWhileWaiting(t *testing.T) {
	cluster, server := newTestClusterServer(t)
	sem := make(chan struct{}, 1)
	cluster.ServerGlobals = &ServerGlobals{BackupSemaphore: sem}
	cluster.SetInBinlogBackupState(true)

	done := make(chan error, 1)
	go func() { done <- server.JobBackupPhysicalWithOptions(BackupRunOptions{}) }()

	if !waitSlotsHeld(sem, 1, 5*time.Second) {
		t.Fatalf("holding %d slots, want the one it waits with", len(sem))
	}
	server.State = stateFailed

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "server down") {
			t.Fatalf("got %v, want a server down error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kept waiting after the server went down")
	}
	if got := len(sem); got != 0 {
		t.Fatalf("slot not given back: %d held", got)
	}
}
