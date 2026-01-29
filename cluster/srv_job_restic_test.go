// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/sirupsen/logrus"
)

func TestResolveStrategyChain(t *testing.T) {
	snapshotID := "snap-123"

	tests := []struct {
		name              string
		requestedStrategy string
		method            string
		mountDisabled     bool
		summary           *SnapshotMetadataSummary
		expected          []string
	}{
		{
			name:              "requested restore",
			requestedStrategy: "restore",
			expected:          []string{"restore"},
		},
		{
			name:              "requested dump",
			requestedStrategy: "dump",
			expected:          []string{"dump", "restore"},
		},
		{
			name:              "requested mount with fuse",
			requestedStrategy: "mount",
			mountDisabled:     false,
			expected:          []string{"mount", "restore"},
		},
		{
			name:              "requested mount without fuse",
			requestedStrategy: "mount",
			mountDisabled:     true,
			expected:          []string{"restore"},
		},
		{
			name:              "auto with mysqldump logical",
			requestedStrategy: "auto",
			method:            "logical",
			summary: &SnapshotMetadataSummary{
				BackupTool:   config.ConstBackupLogicalTypeMysqldump,
				BackupMethod: "logical",
			},
			expected: []string{"dump", "restore"},
		},
		{
			name:              "auto with mydumper and fuse",
			requestedStrategy: "auto",
			method:            "logical",
			mountDisabled:     false,
			summary: &SnapshotMetadataSummary{
				BackupTool:   config.ConstBackupLogicalTypeMydumper,
				BackupMethod: "logical",
			},
			expected: []string{"mount", "restore"},
		},
		{
			name:              "auto with mydumper without fuse",
			requestedStrategy: "auto",
			method:            "logical",
			mountDisabled:     true,
			summary: &SnapshotMetadataSummary{
				BackupTool:   config.ConstBackupLogicalTypeMydumper,
				BackupMethod: "logical",
			},
			expected: []string{"restore"},
		},
		{
			name:              "auto with physical backup",
			requestedStrategy: "auto",
			method:            "physical",
			summary: &SnapshotMetadataSummary{
				BackupTool:   config.ConstBackupPhysicalTypeXtrabackup,
				BackupMethod: "physical",
			},
			expected: []string{"restore"},
		},
		{
			name:              "invalid strategy falls back to auto",
			requestedStrategy: "weird",
			method:            "logical",
			summary: &SnapshotMetadataSummary{
				BackupTool:   config.ConstBackupLogicalTypeMysqldump,
				BackupMethod: "logical",
			},
			expected: []string{"dump", "restore"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cluster := newTestClusterWithMetadata(snapshotID, test.summary, test.mountDisabled)
			strategies := resolveStrategyChain(test.requestedStrategy, test.method, snapshotID, cluster)
			if len(strategies) == 0 {
				t.Fatalf("expected non-empty strategy chain")
			}
			if !reflect.DeepEqual(strategies, test.expected) {
				t.Fatalf("unexpected strategies: got=%v want=%v", strategies, test.expected)
			}
		})
	}
}

func TestJobReseedFromRestic_MountDisabled(t *testing.T) {
	snapshotID := "snap-mount-disabled"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMydumper,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, true)
	cluster.Logrus = logrus.New()
	server := &ServerMonitor{ClusterGroup: cluster}

	err := server.JobReseedFromRestic(snapshotID, "logical", "mount", ResticReseedOptions{})
	if err == nil {
		t.Fatal("expected error when mount strategy used without FUSE")
	}
	if !strings.Contains(err.Error(), "mount operations are disabled") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareResticReseedPaths(t *testing.T) {
	snapshotID := "snap-prepare"

	tests := []struct {
		name           string
		backupTool     string
		backupMethod   string
		confCompressed bool
		metaCompressed *bool
		wantErr        bool
		wantIsDir      bool
		wantType       string
		wantSources    []string
	}{
		{
			name:           "mysqldump compressed from metadata",
			backupTool:     config.ConstBackupLogicalTypeMysqldump,
			backupMethod:   "logical",
			confCompressed: false,
			metaCompressed: boolPtr(true),
			wantType:       config.ConstBackupLogicalTypeMysqldump,
			wantSources:    []string{"mysqldump.sql.gz"},
		},
		{
			name:           "mysqldump uncompressed from metadata",
			backupTool:     config.ConstBackupLogicalTypeMysqldump,
			backupMethod:   "logical",
			confCompressed: true,
			metaCompressed: boolPtr(false),
			wantType:       config.ConstBackupLogicalTypeMysqldump,
			wantSources:    []string{"mysqldump.sql"},
		},
		{
			name:           "mydumper directory",
			backupTool:     config.ConstBackupLogicalTypeMydumper,
			backupMethod:   "logical",
			confCompressed: true,
			wantIsDir:      true,
			wantType:       config.ConstBackupLogicalTypeMydumper,
			wantSources:    []string{"mydumper"},
		},
		{
			name:           "dumpling directory",
			backupTool:     config.ConstBackupLogicalTypeDumpling,
			backupMethod:   "logical",
			confCompressed: true,
			wantIsDir:      true,
			wantType:       config.ConstBackupLogicalTypeDumpling,
			wantSources:    []string{"dumpling"},
		},
		{
			name:           "xtrabackup compressed from config",
			backupTool:     config.ConstBackupPhysicalTypeXtrabackup,
			backupMethod:   "physical",
			confCompressed: true,
			wantType:       config.ConstBackupPhysicalTypeXtrabackup,
			wantSources:    []string{"xtrabackup.xbtream.gz"},
		},
		{
			name:           "mariabackup uncompressed from config",
			backupTool:     config.ConstBackupPhysicalTypeMariaBackup,
			backupMethod:   "physical",
			confCompressed: false,
			wantType:       config.ConstBackupPhysicalTypeMariaBackup,
			wantSources:    []string{"mariabackup.xbtream"},
		},
		{
			name:           "unknown backup tool",
			backupTool:     "unknown",
			backupMethod:   "logical",
			confCompressed: false,
			wantErr:        true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := &SnapshotMetadataSummary{
				BackupTool:       test.backupTool,
				BackupMethod:     test.backupMethod,
				ResticSnapshotID: snapshotID,
			}
			cluster := newTestClusterWithMetadata(snapshotID, summary, false)
			cluster.Conf.CompressBackups = test.confCompressed
			if test.metaCompressed != nil {
				cluster.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{
					ResticSnapshotID: snapshotID,
					Compressed:       *test.metaCompressed,
				})
			}
			server := &ServerMonitor{ClusterGroup: cluster}

			paths, err := server.prepareResticReseedPaths(snapshotID, test.backupMethod)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if paths.IsDirectory != test.wantIsDir {
				t.Fatalf("unexpected IsDirectory: got=%t want=%t", paths.IsDirectory, test.wantIsDir)
			}
			if paths.BackupType != test.wantType {
				t.Fatalf("unexpected BackupType: got=%s want=%s", paths.BackupType, test.wantType)
			}
			if !reflect.DeepEqual(paths.SourcePaths, test.wantSources) {
				t.Fatalf("unexpected SourcePaths: got=%v want=%v", paths.SourcePaths, test.wantSources)
			}
		})
	}
}

func TestCleanupResticReseed(t *testing.T) {
	tests := []struct {
		name               string
		createTempDir      bool
		buildPaths         func(string) *ResticReseedPaths
		unmountErr         error
		wantErr            bool
		wantUnmountCalls   int
		wantTempDirRemoved bool
	}{
		{
			name:       "nil paths",
			buildPaths: func(string) *ResticReseedPaths { return nil },
			wantErr:    true,
		},
		{
			name:          "cleanup disabled skips temp dir",
			createTempDir: true,
			buildPaths: func(tempDir string) *ResticReseedPaths {
				return &ResticReseedPaths{RequiresCleanup: false, TempDir: tempDir}
			},
			wantTempDirRemoved: false,
		},
		{
			name: "cleanup disabled skips unmount",
			buildPaths: func(string) *ResticReseedPaths {
				return &ResticReseedPaths{RequiresCleanup: false, IsMounted: true}
			},
			wantUnmountCalls: 0,
		},
		{
			name:          "cleanup removes temp dir",
			createTempDir: true,
			buildPaths: func(tempDir string) *ResticReseedPaths {
				return &ResticReseedPaths{RequiresCleanup: true, TempDir: tempDir}
			},
			wantTempDirRemoved: true,
		},
		{
			name: "cleanup unmounts repo",
			buildPaths: func(string) *ResticReseedPaths {
				return &ResticReseedPaths{RequiresCleanup: true, IsMounted: true}
			},
			wantUnmountCalls: 1,
		},
		{
			name:          "unmount error still removes temp dir",
			createTempDir: true,
			buildPaths: func(tempDir string) *ResticReseedPaths {
				return &ResticReseedPaths{RequiresCleanup: true, IsMounted: true, TempDir: tempDir}
			},
			unmountErr:         errors.New("unmount failed"),
			wantUnmountCalls:   1,
			wantTempDirRemoved: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stub := &resticUnmountStub{err: test.unmountErr}
			server := newTestServerWithUnmountStub(stub)

			tempDir := ""
			if test.createTempDir {
				dir, err := os.MkdirTemp("", "restic-cleanup-")
				if err != nil {
					t.Fatalf("failed to create temp dir: %v", err)
				}
				tempDir = dir
				if _, err := os.Stat(tempDir); err != nil {
					t.Fatalf("temp dir missing before cleanup: %v", err)
				}
				defer func() {
					_ = os.RemoveAll(tempDir)
				}()
			}

			paths := test.buildPaths(tempDir)
			err := server.cleanupResticReseed(paths)
			if test.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if stub.Calls() != test.wantUnmountCalls {
				t.Fatalf("unexpected unmount calls: got=%d want=%d", stub.Calls(), test.wantUnmountCalls)
			}

			if tempDir != "" {
				_, statErr := os.Stat(tempDir)
				if test.wantTempDirRemoved {
					if !os.IsNotExist(statErr) {
						t.Fatalf("expected temp dir to be removed")
					}
				} else if os.IsNotExist(statErr) {
					t.Fatalf("expected temp dir to exist")
				}
			}
		})
	}
}

func TestCleanupResticReseed_RemoveAllFailure(t *testing.T) {
	stub := &resticUnmountStub{}
	server := newTestServerWithUnmountStub(stub)

	originalRemoveAll := resticRemoveAll
	resticRemoveAll = func(string) error {
		return fmt.Errorf("remove failed")
	}
	defer func() {
		resticRemoveAll = originalRemoveAll
	}()

	tempDir := t.TempDir()
	paths := &ResticReseedPaths{RequiresCleanup: true, TempDir: tempDir}
	err := server.cleanupResticReseed(paths)
	if err == nil {
		t.Fatal("expected error from RemoveAll failure, got nil")
	}
	if stub.Calls() != 0 {
		t.Fatalf("unexpected unmount calls: got=%d want=0", stub.Calls())
	}
	if _, statErr := os.Stat(tempDir); os.IsNotExist(statErr) {
		t.Fatal("expected temp dir to remain after cleanup failure")
	}
}

func TestReseedFromResticRestore_CancelTriggersCleanup(t *testing.T) {
	snapshotID := "snap-cancel-restore"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, false)
	cluster.Logrus = logrus.New()
	cluster.Conf.BackupResticReseedCleanup = true
	cluster.resticUnmounter = &resticUnmountStub{}
	server := &ServerMonitor{ClusterGroup: cluster}

	baseDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	createdPath := ""
	originalMkdirTemp := resticMkdirTemp
	resticMkdirTemp = func(dir, pattern string) (string, error) {
		path, err := originalMkdirTemp(dir, pattern)
		if err == nil {
			createdPath = path
			cancel()
		}
		return path, err
	}
	defer func() {
		resticMkdirTemp = originalMkdirTemp
	}()

	err := server.reseedFromResticRestore(ctx, snapshotID, "logical", ResticReseedOptions{TempDir: baseDir, Cleanup: boolPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected cancel error, got: %v", err)
	}
	if createdPath == "" {
		t.Fatal("expected temp dir path, got empty")
	}
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp dir to be removed, stat error: %v", statErr)
	}
}

func TestReseedFromResticRestore_InsufficientDiskSpaceTriggersCleanup(t *testing.T) {
	snapshotID := "snap-low-disk"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, false)
	cluster.Logrus = logrus.New()
	cluster.Conf.BackupResticReseedCleanup = true
	cluster.BackupMetaMap.Set(1, &backupmgr.BackupMetadata{
		ResticSnapshotID: snapshotID,
		Size:             1024,
	})
	server := &ServerMonitor{ClusterGroup: cluster}

	baseDir := t.TempDir()
	createdPath := ""
	removedPath := ""

	originalMkdirTemp := resticMkdirTemp
	originalRemoveAll := resticRemoveAll
	originalCheckDiskSpace := resticCheckDiskSpace

	resticMkdirTemp = func(dir, pattern string) (string, error) {
		path, err := originalMkdirTemp(dir, pattern)
		if err == nil {
			createdPath = path
		}
		return path, err
	}
	resticRemoveAll = func(path string) error {
		removedPath = path
		return os.RemoveAll(path)
	}
	diskErr := errors.New("insufficient disk space")
	resticCheckDiskSpace = func(string, uint64) error {
		return diskErr
	}
	defer func() {
		resticMkdirTemp = originalMkdirTemp
		resticRemoveAll = originalRemoveAll
		resticCheckDiskSpace = originalCheckDiskSpace
	}()

	err := server.reseedFromResticRestore(context.Background(), snapshotID, "logical", ResticReseedOptions{TempDir: baseDir, Cleanup: boolPtr(true)})
	if err == nil {
		t.Fatal("expected disk space error, got nil")
	}
	if !errors.Is(err, diskErr) {
		t.Fatalf("unexpected error: %v", err)
	}
	if createdPath == "" {
		t.Fatal("expected temp dir path, got empty")
	}
	if removedPath != createdPath {
		t.Fatalf("expected temp dir removal for %s, got %s", createdPath, removedPath)
	}
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp dir to be removed, stat error: %v", statErr)
	}
}

func TestReseedFromResticMount_CancelTriggersCleanup(t *testing.T) {
	snapshotID := "snap-cancel-mount"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, false)
	cluster.Logrus = logrus.New()
	cluster.Conf.BackupResticReseedCleanup = true
	cluster.resticUnmounter = &resticUnmountStub{}
	server := &ServerMonitor{ClusterGroup: cluster}

	baseDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	createdPath := ""
	originalMkdirTemp := resticMkdirTemp
	resticMkdirTemp = func(dir, pattern string) (string, error) {
		path, err := originalMkdirTemp(dir, pattern)
		if err == nil {
			createdPath = path
			cancel()
		}
		return path, err
	}
	defer func() {
		resticMkdirTemp = originalMkdirTemp
	}()

	err := server.reseedFromResticMount(ctx, snapshotID, "logical", ResticReseedOptions{TempDir: baseDir, Cleanup: boolPtr(true)})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected cancel error, got: %v", err)
	}
	if createdPath == "" {
		t.Fatal("expected temp dir path, got empty")
	}
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp dir to be removed, stat error: %v", statErr)
	}
}

func TestJobReseedFromRestic_TimeoutRestoreTriggersCleanup(t *testing.T) {
	snapshotID := "snap-timeout-restore"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMysqldump,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, false)
	cluster.Logrus = logrus.New()
	cluster.Conf.BackupResticReseedCleanup = true
	server := &ServerMonitor{ClusterGroup: cluster}

	baseDir := t.TempDir()
	createdPath := ""
	removedPath := ""

	originalMkdirTemp := resticMkdirTemp
	originalRemoveAll := resticRemoveAll
	originalRestoreSnapshot := resticRestoreSnapshot
	originalTimeout := resticReseedTimeout

	resticMkdirTemp = func(dir, pattern string) (string, error) {
		path, err := originalMkdirTemp(dir, pattern)
		if err == nil {
			createdPath = path
		}
		return path, err
	}
	resticRemoveAll = func(path string) error {
		removedPath = path
		return os.RemoveAll(path)
	}
	resticRestoreSnapshot = func(ctx context.Context, _ *backupmgr.ResticManager, snapshotID, targetDir string, sourcePaths []string, overwrite string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	resticReseedTimeout = func(ResticReseedOptions, *config.Config) time.Duration {
		return time.Millisecond
	}
	defer func() {
		resticMkdirTemp = originalMkdirTemp
		resticRemoveAll = originalRemoveAll
		resticRestoreSnapshot = originalRestoreSnapshot
		resticReseedTimeout = originalTimeout
	}()

	err := server.JobReseedFromRestic(snapshotID, "logical", "restore", ResticReseedOptions{TempDir: baseDir, Cleanup: boolPtr(true)})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "restic reseed timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if createdPath == "" {
		t.Fatal("expected temp dir path, got empty")
	}
	if removedPath != createdPath {
		t.Fatalf("expected cleanup for %s, got %s", createdPath, removedPath)
	}
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp dir to be removed, stat error: %v", statErr)
	}
}

func TestJobReseedFromRestic_TimeoutMountTriggersCleanup(t *testing.T) {
	snapshotID := "snap-timeout-mount"
	summary := &SnapshotMetadataSummary{
		BackupTool:       config.ConstBackupLogicalTypeMydumper,
		BackupMethod:     "logical",
		ResticSnapshotID: snapshotID,
	}
	cluster := newTestClusterWithMetadata(snapshotID, summary, false)
	cluster.Logrus = logrus.New()
	cluster.Conf.BackupResticReseedCleanup = true
	unmountStub := &resticUnmountStub{}
	cluster.resticUnmounter = unmountStub
	server := &ServerMonitor{ClusterGroup: cluster}

	baseDir := t.TempDir()
	createdPath := ""
	removedPath := ""

	originalMkdirTemp := resticMkdirTemp
	originalRemoveAll := resticRemoveAll
	originalMountRepo := resticMountRepo
	originalTimeout := resticReseedTimeout

	resticMkdirTemp = func(dir, pattern string) (string, error) {
		path, err := originalMkdirTemp(dir, pattern)
		if err == nil {
			createdPath = path
		}
		return path, err
	}
	resticRemoveAll = func(path string) error {
		removedPath = path
		return os.RemoveAll(path)
	}
	resticMountRepo = func(ctx context.Context, _ *backupmgr.ResticManager, mountDir string) error {
		<-ctx.Done()
		return ctx.Err()
	}
	resticReseedTimeout = func(ResticReseedOptions, *config.Config) time.Duration {
		return time.Millisecond
	}
	defer func() {
		resticMkdirTemp = originalMkdirTemp
		resticRemoveAll = originalRemoveAll
		resticMountRepo = originalMountRepo
		resticReseedTimeout = originalTimeout
	}()

	err := server.JobReseedFromRestic(snapshotID, "logical", "mount", ResticReseedOptions{TempDir: baseDir, Cleanup: boolPtr(true)})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "restic reseed timeout") {
		t.Fatalf("expected timeout error, got: %v", err)
	}
	if unmountStub.Calls() != 1 {
		t.Fatalf("expected unmount call, got: %d", unmountStub.Calls())
	}
	if createdPath == "" {
		t.Fatal("expected temp dir path, got empty")
	}
	if removedPath != createdPath {
		t.Fatalf("expected cleanup for %s, got %s", createdPath, removedPath)
	}
	if _, statErr := os.Stat(createdPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp dir to be removed, stat error: %v", statErr)
	}
}

func newTestClusterWithMetadata(snapshotID string, summary *SnapshotMetadataSummary, mountDisabled bool) *Cluster {
	cluster := &Cluster{
		Conf:                  &config.Config{},
		BackupMetaMap:         backupmgr.NewBackupMetaMap(),
		ResticManager:         newTestResticManager(mountDisabled),
		snapshotMetadataCache: newSnapshotMetadataCache(),
	}

	if summary == nil {
		return cluster
	}
	copySummary := *summary
	if copySummary.ResticSnapshotID == "" {
		copySummary.ResticSnapshotID = snapshotID
	}
	cluster.snapshotMetadataCache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
		entry.Summaries = map[string]*SnapshotMetadataSummary{
			"primary": &copySummary,
		}
		entry.Status = snapshotMetadataStatusReady
	})

	return cluster
}

func newTestResticManager(mountDisabled bool) *backupmgr.ResticManager {
	return &backupmgr.ResticManager{
		Mutex:         &sync.Mutex{},
		MountDisabled: mountDisabled,
	}
}

type resticUnmountStub struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (stub *resticUnmountStub) UnmountRepo() error {
	stub.mu.Lock()
	stub.calls++
	stub.mu.Unlock()
	return stub.err
}

func (stub *resticUnmountStub) Calls() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return stub.calls
}

func newTestServerWithUnmountStub(stub *resticUnmountStub) *ServerMonitor {
	cluster := &Cluster{
		Conf:          &config.Config{},
		ResticManager: newTestResticManager(false),
	}
	cluster.resticUnmounter = stub
	return &ServerMonitor{ClusterGroup: cluster}
}

// TestTrySetInReseedBackupConcurrency tests the race condition fix for concurrent reseed operations
func TestTrySetInReseedBackupConcurrency(t *testing.T) {
	t.Run("first set succeeds", func(t *testing.T) {
		server := &ServerMonitor{}

		ok, currentTask := server.TrySetInReseedBackup("task1")
		if !ok {
			t.Fatal("First TrySetInReseedBackup should succeed")
		}
		if currentTask != "" {
			t.Fatalf("Expected empty currentTask, got: %s", currentTask)
		}

		// Verify state was set
		server.reseedMutex.Lock()
		if server.IsReseeding != "task1" {
			t.Fatalf("Expected IsReseeding='task1', got: %s", server.IsReseeding)
		}
		server.reseedMutex.Unlock()
	})

	t.Run("concurrent set fails with correct error", func(t *testing.T) {
		server := &ServerMonitor{}

		// First set succeeds
		ok, _ := server.TrySetInReseedBackup("task1")
		if !ok {
			t.Fatal("First TrySetInReseedBackup should succeed")
		}

		// Second set should fail and return current task
		ok, currentTask := server.TrySetInReseedBackup("task2")
		if ok {
			t.Fatal("Second TrySetInReseedBackup should fail while first is active")
		}
		if currentTask != "task1" {
			t.Fatalf("Expected currentTask='task1', got: %s", currentTask)
		}

		// Verify state unchanged
		server.reseedMutex.Lock()
		if server.IsReseeding != "task1" {
			t.Fatalf("Expected IsReseeding='task1', got: %s", server.IsReseeding)
		}
		server.reseedMutex.Unlock()
	})

	t.Run("after clearing, new set succeeds", func(t *testing.T) {
		server := &ServerMonitor{}

		// First set
		ok, _ := server.TrySetInReseedBackup("task1")
		if !ok {
			t.Fatal("First TrySetInReseedBackup should succeed")
		}

		// Clear the state
		server.SetInReseedBackup("")

		// New set should succeed
		ok, currentTask := server.TrySetInReseedBackup("task2")
		if !ok {
			t.Fatalf("TrySetInReseedBackup should succeed after clearing, got currentTask: %s", currentTask)
		}

		// Verify new state
		server.reseedMutex.Lock()
		if server.IsReseeding != "task2" {
			t.Fatalf("Expected IsReseeding='task2', got: %s", server.IsReseeding)
		}
		server.reseedMutex.Unlock()
	})

	t.Run("multiple goroutines racing - only one succeeds", func(t *testing.T) {
		server := &ServerMonitor{}

		const numGoroutines = 50
		var wg sync.WaitGroup
		startCh := make(chan struct{})

		// Track which goroutines succeeded
		type result struct {
			id      int
			success bool
			current string
		}
		results := make(chan result, numGoroutines)

		// Launch goroutines
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				// Wait for all goroutines to be ready
				<-startCh
				ok, currentTask := server.TrySetInReseedBackup("task" + string(rune('A'+id)))
				results <- result{id: id, success: ok, current: currentTask}
			}(i)
		}

		// Start all goroutines at once
		close(startCh)
		wg.Wait()
		close(results)

		// Collect results
		successCount := 0
		var successID int
		for res := range results {
			if res.success {
				successCount++
				successID = res.id
				if res.current != "" {
					t.Fatalf("Successful attempt should have empty currentTask, got: %s", res.current)
				}
			} else {
				if res.current == "" {
					t.Fatal("Failed attempt should return non-empty currentTask")
				}
			}
		}

		// Only one should have succeeded
		if successCount != 1 {
			t.Fatalf("Expected exactly 1 success, got: %d", successCount)
		}

		// Verify final state matches the winner
		server.reseedMutex.Lock()
		expectedTask := "task" + string(rune('A'+successID))
		if server.IsReseeding != expectedTask {
			t.Fatalf("Expected IsReseeding='%s', got: %s", expectedTask, server.IsReseeding)
		}
		server.reseedMutex.Unlock()
	})

	t.Run("stress test - sequential acquire and release", func(t *testing.T) {
		server := &ServerMonitor{}

		const iterations = 100
		for i := 0; i < iterations; i++ {
			taskName := "task" + string(rune('0'+i%10))

			ok, _ := server.TrySetInReseedBackup(taskName)
			if !ok {
				t.Fatalf("Iteration %d: TrySetInReseedBackup should succeed", i)
			}

			// Verify state
			server.reseedMutex.Lock()
			if server.IsReseeding != taskName {
				t.Fatalf("Iteration %d: Expected IsReseeding='%s', got: %s", i, taskName, server.IsReseeding)
			}
			server.reseedMutex.Unlock()

			// Clear for next iteration
			server.SetInReseedBackup("")
		}
	})

	t.Run("concurrent attempts with periodic releases", func(t *testing.T) {
		server := &ServerMonitor{}

		const numWorkers = 20
		const duration = 500 * time.Millisecond

		var wg sync.WaitGroup
		stopCh := make(chan struct{})
		successCount := make(chan int, numWorkers)

		// Launch workers
		for i := 0; i < numWorkers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				count := 0
				taskName := "worker" + string(rune('A'+id))

				for {
					select {
					case <-stopCh:
						successCount <- count
						return
					default:
						ok, _ := server.TrySetInReseedBackup(taskName)
						if ok {
							count++
							// Hold briefly
							time.Sleep(5 * time.Millisecond)
							// Release
							server.SetInReseedBackup("")
						}
						// Brief pause before retry
						time.Sleep(time.Millisecond)
					}
				}
			}(i)
		}

		// Let it run
		time.Sleep(duration)
		close(stopCh)
		wg.Wait()
		close(successCount)

		// Verify all workers got some successes
		totalSuccess := 0
		workerSuccess := 0
		for count := range successCount {
			totalSuccess += count
			if count > 0 {
				workerSuccess++
			}
		}

		t.Logf("Total successful acquisitions: %d across %d workers", totalSuccess, workerSuccess)

		if totalSuccess == 0 {
			t.Fatal("Expected at least some successful acquisitions")
		}

		// Verify final state is cleared
		server.reseedMutex.Lock()
		if server.IsReseeding != "" {
			t.Fatalf("Expected IsReseeding to be empty at end, got: %s", server.IsReseeding)
		}
		server.reseedMutex.Unlock()
	})

	t.Run("no deadlock under contention", func(t *testing.T) {
		server := &ServerMonitor{}

		done := make(chan bool, 1)
		timeout := time.After(5 * time.Second)

		go func() {
			const numIterations = 100
			var wg sync.WaitGroup

			for i := 0; i < numIterations; i++ {
				wg.Add(2)

				// Goroutine 1: Try to set
				go func(iter int) {
					defer wg.Done()
					taskName := "task" + string(rune('0'+iter%10))
					ok, _ := server.TrySetInReseedBackup(taskName)
					if ok {
						time.Sleep(time.Millisecond)
						server.SetInReseedBackup("")
					}
				}(i)

				// Goroutine 2: Try to set different task
				go func(iter int) {
					defer wg.Done()
					taskName := "other" + string(rune('0'+iter%10))
					ok, _ := server.TrySetInReseedBackup(taskName)
					if ok {
						time.Sleep(time.Millisecond)
						server.SetInReseedBackup("")
					}
				}(i)
			}

			wg.Wait()
			done <- true
		}()

		select {
		case <-done:
			t.Log("No deadlock detected")
		case <-timeout:
			t.Fatal("Test timed out - possible deadlock")
		}
	})

	t.Run("informative error messages", func(t *testing.T) {
		server := &ServerMonitor{}

		// Set initial task
		ok, _ := server.TrySetInReseedBackup("mysqldump-backup")
		if !ok {
			t.Fatal("Initial set should succeed")
		}

		// Try different tasks and verify error messages
		testCases := []string{
			"xtrabackup-restore",
			"mydumper-backup",
			"mariabackup-restore",
		}

		for _, taskName := range testCases {
			ok, currentTask := server.TrySetInReseedBackup(taskName)
			if ok {
				t.Fatalf("Should fail when trying to set '%s'", taskName)
			}
			if currentTask != "mysqldump-backup" {
				t.Fatalf("Expected currentTask='mysqldump-backup', got: %s", currentTask)
			}
		}
	})
}
