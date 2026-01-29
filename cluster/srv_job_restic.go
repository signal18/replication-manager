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
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ResticReseedPaths tracks paths and temporary locations used during a restic reseed.
type ResticReseedPaths struct {
	// SnapshotID is the restic snapshot ID to restore from.
	SnapshotID string
	// BackupType is the backup type for the snapshot (mysqldump, mydumper, xtrabackup, mariabackup).
	BackupType string
	// IsDirectory reports whether the backup is directory-based (true for mydumper).
	IsDirectory bool
	// SourcePaths lists the paths inside the restic snapshot to restore.
	SourcePaths []string
	// TempDir is the local temporary directory used for extracted files.
	TempDir string
	// TargetPaths lists final paths where extracted files will be placed.
	TargetPaths []string
	// RequiresCleanup reports whether the temporary directory should be cleaned up.
	RequiresCleanup bool
	// IsMounted reports whether the restore is using a mount strategy (FUSE filesystem).
	IsMounted bool
}

// ResticReseedOptions describes overrides for restic reseed behavior.
type ResticReseedOptions struct {
	// TempDir overrides the temporary directory used during restore.
	TempDir string
	// Cleanup overrides cleanup behavior; nil means use the configuration default.
	Cleanup *bool
	// Timeout is the reseed timeout in seconds; 0 means use the configuration default.
	Timeout int
	// Overwrite defines the restic overwrite policy (e.g. "", "always", "if-newer").
	Overwrite string
}

// resolveStrategyChain determines the ordered list of restic reseed strategies to attempt.
//
// It honors explicit user requests (with minimal fallbacks) and otherwise auto-selects
// the safest strategy based on snapshot metadata, reseed method, and FUSE availability.
// Any unknown or missing inputs fall back to the conservative "restore" strategy.
func resolveStrategyChain(requestedStrategy, method, snapshotID string, cluster *Cluster) []string {
	normalizedRequested := strings.ToLower(strings.TrimSpace(requestedStrategy))
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))

	// Determine FUSE availability early for mount-based strategies.
	fuseAvailable := cluster != nil && cluster.ResticManager != nil && !cluster.ResticManager.IsMountDisabled()

	var strategies []string

	// Honor explicit user requests first; only "dump" and "mount" receive a restore fallback.
	switch normalizedRequested {
	case "restore":
		strategies = []string{"restore"}
	case "dump":
		strategies = []string{"dump", "restore"}
	case "mount":
		if fuseAvailable {
			strategies = []string{"mount", "restore"}
		} else {
			strategies = []string{"restore"}
		}
	case "", "auto":
		// Continue to auto-selection below.
	default:
		// Unknown strategy name; fall back to auto-selection instead of failing.
	}

	if len(strategies) == 0 {
		metadata := getSnapshotMetadata(cluster, snapshotID)
		if metadata == nil {
			// Without metadata we only choose the safest option.
			strategies = []string{"restore"}
		} else {
			backupTool := strings.ToLower(strings.TrimSpace(metadata.BackupTool))
			backupMethod := strings.ToLower(strings.TrimSpace(metadata.BackupMethod))

			// Physical backups can only be handled via direct restore.
			switch backupTool {
			case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
				strategies = []string{"restore"}
			}

			// Directory-based logical backups benefit from mount when FUSE is available.
			switch backupTool {
			case config.ConstBackupLogicalTypeMydumper, config.ConstBackupLogicalTypeDumpling:
				if fuseAvailable {
					strategies = []string{"mount", "restore"}
				} else {
					strategies = []string{"restore"}
				}
			}

			// Single-file logical backups can be streamed when using the logical method.
			if backupTool == config.ConstBackupLogicalTypeMysqldump {
				if normalizedMethod == "logical" || (normalizedMethod == "" && backupMethod == "logical") {
					strategies = []string{"dump", "restore"}
				} else {
					strategies = []string{"restore"}
				}
			}

			// Unknown or unsupported tool; default to the safest strategy.
			if len(strategies) == 0 {
				strategies = []string{"restore"}
			}
		}
	}

	if cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Resolved restic reseed strategies: requested=%s method=%s snapshot=%s strategies=%v",
			normalizedRequested, normalizedMethod, snapshotID, strategies)
	}

	return strategies
}

// getSnapshotMetadata selects the best available metadata summary for a restic snapshot.
// It prefers cached metadata derived from snapshot metadata files, with a fallback to
// backup metadata summaries when no extracted metadata is available.
func getSnapshotMetadata(cluster *Cluster, snapshotID string) *SnapshotMetadataSummary {
	if cluster == nil || strings.TrimSpace(snapshotID) == "" {
		return nil
	}
	selectBestSummary := func(candidates []*SnapshotMetadataSummary) *SnapshotMetadataSummary {
		var selected *SnapshotMetadataSummary
		for _, candidate := range candidates {
			if candidate == nil {
				continue
			}
			resticID := strings.TrimSpace(candidate.ResticSnapshotID)
			if resticID != "" && resticID == snapshotID {
				return candidate
			}
			if selected == nil {
				selected = candidate
				continue
			}
			if selected.EndTime.IsZero() && !candidate.EndTime.IsZero() {
				selected = candidate
				continue
			}
			if candidate.EndTime.After(selected.EndTime) {
				selected = candidate
				continue
			}
			if candidate.EndTime.Equal(selected.EndTime) && candidate.StartTime.After(selected.StartTime) {
				selected = candidate
			}
		}
		return selected
	}
	// Prefer the cached metadata extracted from the snapshot itself.
	if entry, ok := cluster.getSnapshotMetadataCacheEntry(snapshotID); ok && entry != nil {
		candidates := make([]*SnapshotMetadataSummary, 0, len(entry.Summaries))
		for _, summary := range entry.Summaries {
			if summary == nil {
				continue
			}
			candidates = append(candidates, summary)
		}
		if selected := selectBestSummary(candidates); selected != nil {
			return selected
		}
	}
	// Fall back to best-effort summaries derived from backup metadata.
	snapshots := cluster.GetSnapshots()
	for i := range snapshots {
		if snapshots[i].Id == snapshotID {
			return selectBestSummary(cluster.SummarizeSnapshotMetadata(&snapshots[i]))
		}
	}
	return nil
}

// getSnapshotCompression checks backup metadata for an explicit compression flag.
// Returns (compressed, true) when a matching metadata entry is found.
func getSnapshotCompression(cluster *Cluster, snapshotID string) (bool, bool) {
	if cluster == nil || cluster.BackupMetaMap == nil || strings.TrimSpace(snapshotID) == "" {
		return false, false
	}
	var selected *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(_, value any) bool {
		meta, ok := value.(*backupmgr.BackupMetadata)
		if !ok || meta == nil {
			return true
		}
		if strings.TrimSpace(meta.ResticSnapshotID) != snapshotID {
			return true
		}
		if selected == nil {
			selected = meta
			return true
		}
		if selected.EndTime.IsZero() && !meta.EndTime.IsZero() {
			selected = meta
			return true
		}
		if meta.EndTime.After(selected.EndTime) {
			selected = meta
			return true
		}
		if meta.EndTime.Equal(selected.EndTime) && meta.StartTime.After(selected.StartTime) {
			selected = meta
		}
		return true
	})
	if selected == nil {
		return false, false
	}
	return selected.Compressed, true
}

// getSnapshotSizeBytes returns the snapshot size (in bytes) when backup metadata is available.
func getSnapshotSizeBytes(cluster *Cluster, snapshotID string) (uint64, bool) {
	if cluster == nil || cluster.BackupMetaMap == nil || strings.TrimSpace(snapshotID) == "" {
		return 0, false
	}
	var selected *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(_, value any) bool {
		meta, ok := value.(*backupmgr.BackupMetadata)
		if !ok || meta == nil {
			return true
		}
		if strings.TrimSpace(meta.ResticSnapshotID) != snapshotID {
			return true
		}
		if selected == nil {
			selected = meta
			return true
		}
		if selected.EndTime.IsZero() && !meta.EndTime.IsZero() {
			selected = meta
			return true
		}
		if meta.EndTime.After(selected.EndTime) {
			selected = meta
			return true
		}
		if meta.EndTime.Equal(selected.EndTime) && meta.StartTime.After(selected.StartTime) {
			selected = meta
		}
		return true
	})
	if selected == nil || selected.Size <= 0 {
		return 0, false
	}
	return uint64(selected.Size), true
}

// prepareResticReseedPaths builds the list of snapshot paths to restore for a restic reseed.
// It inspects snapshot metadata to determine backup tool, layout (file vs directory), and
// compression, then returns a populated ResticReseedPaths descriptor for later stages.
func (server *ServerMonitor) prepareResticReseedPaths(snapshotID, method string) (*ResticReseedPaths, error) {
	if strings.TrimSpace(snapshotID) == "" {
		return nil, fmt.Errorf("snapshot id is required")
	}
	cluster := server.ClusterGroup
	if cluster == nil {
		return nil, fmt.Errorf("cluster not available")
	}
	metadata := getSnapshotMetadata(cluster, snapshotID)
	if metadata == nil {
		return nil, fmt.Errorf("snapshot metadata not available for %s", snapshotID)
	}
	backupTool := strings.ToLower(strings.TrimSpace(metadata.BackupTool))
	if backupTool == "" {
		return nil, fmt.Errorf("snapshot metadata missing backup tool for %s", snapshotID)
	}
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))

	compressed := cluster.Conf.CompressBackups
	compressionSource := "config"
	// Prefer metadata-derived compression flags when available to handle historical snapshots.
	if metaCompressed, ok := getSnapshotCompression(cluster, snapshotID); ok {
		compressed = metaCompressed
		compressionSource = "snapshot-metadata"
	}

	isDirectory := false
	var sourcePaths []string
	var fileName string
	switch backupTool {
	case config.ConstBackupLogicalTypeMydumper:
		isDirectory = true
		sourcePaths = []string{"mydumper"}
	case config.ConstBackupLogicalTypeDumpling:
		isDirectory = true
		sourcePaths = []string{"dumpling"}
	case config.ConstBackupLogicalTypeMysqldump:
		fileName = "mysqldump.sql"
		if compressed {
			fileName += ".gz"
		}
		sourcePaths = []string{fileName}
	case config.ConstBackupPhysicalTypeXtrabackup:
		fileName = "xtrabackup.xbtream"
		if compressed {
			fileName += ".gz"
		}
		sourcePaths = []string{fileName}
	case config.ConstBackupPhysicalTypeMariaBackup:
		fileName = "mariabackup.xbtream"
		if compressed {
			fileName += ".gz"
		}
		sourcePaths = []string{fileName}
	default:
		return nil, fmt.Errorf("unsupported backup tool %s for snapshot %s (method %s)", backupTool, snapshotID, normalizedMethod)
	}

	paths := &ResticReseedPaths{
		SnapshotID:      snapshotID,
		BackupType:      backupTool,
		IsDirectory:     isDirectory,
		SourcePaths:     sourcePaths,
		TempDir:         "",
		TargetPaths:     []string{},
		RequiresCleanup: false,
		IsMounted:       false,
	}

	logSource := ""
	if len(sourcePaths) == 1 {
		logSource = sourcePaths[0]
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Prepared restic reseed paths: snapshot=%s tool=%s isDir=%t sources=%d source=%s compressed=%t (%s)",
		snapshotID, backupTool, isDirectory, len(sourcePaths), logSource, compressed, compressionSource)

	return paths, nil
}

// verifyRestoredBackup confirms the restic restore produced the expected files or directories.
// It ensures target paths exist and match the expected backup type (file vs directory).
func (server *ServerMonitor) verifyRestoredBackup(paths *ResticReseedPaths) error {
	if paths == nil {
		return fmt.Errorf("paths is nil")
	}
	if len(paths.TargetPaths) == 0 {
		return fmt.Errorf("no target paths to verify")
	}
	for i, targetPath := range paths.TargetPaths {
		if targetPath == "" {
			return fmt.Errorf("target path %d is empty", i)
		}
		info, err := os.Stat(targetPath)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("backup file/dir not found: %s", targetPath)
			}
			return fmt.Errorf("failed to stat %s: %w", targetPath, err)
		}
		if paths.IsDirectory && !info.IsDir() {
			return fmt.Errorf("expected directory but got file: %s", targetPath)
		}
		if !paths.IsDirectory && info.IsDir() {
			return fmt.Errorf("expected file but got directory: %s", targetPath)
		}
	}

	if paths.IsDirectory {
		for _, targetPath := range paths.TargetPaths {
			isEmpty, err := isDirEmpty(targetPath)
			if err != nil {
				return fmt.Errorf("failed to check directory %s: %w", targetPath, err)
			}
			if isEmpty {
				return fmt.Errorf("backup directory is empty: %s", targetPath)
			}
		}
	} else {
		for _, targetPath := range paths.TargetPaths {
			info, err := os.Stat(targetPath)
			if err != nil {
				return fmt.Errorf("failed to stat %s: %w", targetPath, err)
			}
			if info.Size() == 0 {
				return fmt.Errorf("backup file is empty: %s", targetPath)
			}
		}
	}

	if cluster := server.ClusterGroup; cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Verified restored backup: snapshot=%s targets=%d dir=%t",
			paths.SnapshotID, len(paths.TargetPaths), paths.IsDirectory)
	}

	return nil
}

// isDirEmpty reports whether a directory contains no entries.
func isDirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return false, err
	}
	return len(entries) == 0, nil
}

// checkDiskSpace validates that path has at least requiredBytes available.
func checkDiskSpace(path string, requiredBytes uint64) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return fmt.Errorf("statfs failed for %s: %w", path, err)
	}
	available := stat.Bavail * uint64(stat.Bsize)
	if available < requiredBytes {
		return fmt.Errorf("insufficient disk space at %s: required=%s available=%s", path, humanize.Bytes(requiredBytes), humanize.Bytes(available))
	}
	return nil
}

type resticUnmounter interface {
	UnmountRepo() error
}

// cleanupResticReseed safely removes temporary resources created during a restic reseed operation.
// It unmounts any FUSE-mounted restic filesystem and removes temporary directories created during restore.
//
// This function is designed to be safe for use with defer and implements best-effort cleanup:
//   - Unmount failures are logged as warnings but don't prevent directory cleanup
//   - Temporary directory removal failures are logged and returned as errors
//   - Returns an error on nil inputs
//   - Never panics
//
// The function respects the RequiresCleanup flag in paths to skip unnecessary cleanup operations.
func (server *ServerMonitor) cleanupResticReseed(paths *ResticReseedPaths) error {
	if paths == nil {
		return fmt.Errorf("restic reseed paths are nil")
	}

	cluster := server.ClusterGroup

	// Skip cleanup if not required.
	if !paths.RequiresCleanup {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Skipping restic reseed cleanup (disabled)")
		}
		return nil
	}
	if cluster == nil {
		return fmt.Errorf("cluster not available for cleanup")
	}

	// Unmount restic FUSE filesystem if mounted.
	if paths.IsMounted {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Unmounting restic repo for cleanup")

		unmounter := cluster.resticUnmounter
		if unmounter == nil {
			unmounter = cluster.ResticManager
		}
		if unmounter == nil {
			return fmt.Errorf("restic manager not available for cleanup")
		}

		if err := unmounter.UnmountRepo(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to unmount restic repo: %s", err)
			// Don't return error, continue with other cleanup.
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Successfully unmounted restic repo")
		}
	}

	// Remove temporary directory if it exists.
	if paths.TempDir != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Cleaning up temp directory: %s", paths.TempDir)

		if err := resticRemoveAll(paths.TempDir); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to cleanup temp dir %s: %s", paths.TempDir, err)
			return fmt.Errorf("cleanup temp dir failed: %w", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Successfully cleaned up temp directory")
	}

	return nil
}

var resticMkdirTemp = os.MkdirTemp
var resticRemoveAll = os.RemoveAll
var resticCheckDiskSpace = checkDiskSpace
var resticReseedTimeout = func(opts ResticReseedOptions, conf *config.Config) time.Duration {
	if opts.Timeout > 0 {
		return time.Duration(opts.Timeout) * time.Second
	}
	if conf != nil && conf.BackupResticReseedTimeout > 0 {
		return time.Duration(conf.BackupResticReseedTimeout) * time.Second
	}
	return 1 * time.Hour
}
var resticRestoreSnapshot = func(ctx context.Context, manager *backupmgr.ResticManager, snapshotID, targetDir string, sourcePaths []string, overwrite string) error {
	return manager.RestoreSnapshot(snapshotID, targetDir, sourcePaths, overwrite)
}
var resticMountRepo = func(ctx context.Context, manager *backupmgr.ResticManager, mountDir string) error {
	return manager.MountRepo(mountDir)
}

// JobReseedFromRestic orchestrates a restic reseed workflow with strategy fallback and timeout handling.
//
// The function validates inputs, ensures snapshot metadata is ready, resolves the strategy chain,
// and then attempts each strategy in order until one succeeds or all fail. Cleanup is handled
// defensively via defer by the underlying strategy implementations.
func (server *ServerMonitor) JobReseedFromRestic(snapshotID, method, strategy string, opts ResticReseedOptions) error {
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("snapshot ID is required")
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if normalizedMethod != "logical" && normalizedMethod != "physical" {
		return fmt.Errorf("invalid method: %s (must be logical or physical)", method)
	}

	normalizedStrategy := strings.ToLower(strings.TrimSpace(strategy))
	switch normalizedStrategy {
	case "", "auto", "restore", "dump", "mount":
		// Supported strategies.
	default:
		return fmt.Errorf("invalid strategy: %s", strategy)
	}

	if normalizedStrategy == "mount" {
		if cluster.ResticManager == nil {
			return fmt.Errorf("restic manager not available")
		}
		if cluster.ResticManager.IsMountDisabled() {
			return fmt.Errorf("mount operations are disabled (FUSE not available)")
		}
	}

	if opts.Timeout < 0 {
		return fmt.Errorf("timeout must be zero or positive, got %d", opts.Timeout)
	}

	normalizedOverwrite := strings.ToLower(strings.TrimSpace(opts.Overwrite))
	if normalizedOverwrite != "" {
		switch normalizedOverwrite {
		case "always", "if-newer", "never":
			opts.Overwrite = normalizedOverwrite
		default:
			return fmt.Errorf("invalid overwrite policy: %s", opts.Overwrite)
		}
	}

	if err := cluster.RequireSnapshotMetadataReady(snapshotID); err != nil {
		return fmt.Errorf("snapshot metadata not ready: %w", err)
	}

	strategies := resolveStrategyChain(strategy, normalizedMethod, snapshotID, cluster)

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Starting restic reseed: snapshot=%s, method=%s, strategies=%v",
		snapshotID, normalizedMethod, strategies)

	timeout := resticReseedTimeout(opts, cluster.Conf)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var lastErr error
	for i, strat := range strategies {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Attempting reseed with strategy: %s (attempt %d/%d)",
			strat, i+1, len(strategies))

		select {
		case <-ctx.Done():
			return fmt.Errorf("restic reseed timeout after %v", timeout)
		default:
		}

		var err error
		switch strat {
		case "restore":
			err = server.reseedFromResticRestore(ctx, snapshotID, normalizedMethod, opts)
		case "dump":
			err = server.reseedFromResticDump(ctx, snapshotID, normalizedMethod, opts)
		case "mount":
			err = server.reseedFromResticMount(ctx, snapshotID, normalizedMethod, opts)
		default:
			err = fmt.Errorf("unknown strategy: %s", strat)
		}

		if err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Successfully completed restic reseed using strategy: %s",
				strat)
			return nil
		}

		lastErr = err
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Strategy %s failed: %s%s",
			strat, err,
			func() string {
				if i < len(strategies)-1 {
					return fmt.Sprintf(", trying next strategy: %s", strategies[i+1])
				}
				return ""
			}())
	}

	return fmt.Errorf("all reseed strategies failed (%v), last error: %w", strategies, lastErr)
}

func (server *ServerMonitor) reseedFromResticRestore(ctx context.Context, snapshotID, method string, opts ResticReseedOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
	}

	paths, err := server.prepareResticReseedPaths(snapshotID, method)
	if err != nil {
		return fmt.Errorf("failed to prepare paths: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	tempDir := opts.TempDir
	if tempDir == "" {
		if cluster.Conf.BackupResticReseedTempDir != "" {
			tempDir = cluster.Conf.BackupResticReseedTempDir
		} else {
			tempDir = os.TempDir()
		}
	}

	extractDir, err := resticMkdirTemp(tempDir, "restic-reseed-*")
	if err != nil {
		return fmt.Errorf("failed to create temp dir: %w", err)
	}

	// Register cleanup immediately after resource allocation to prevent leaks
	paths.TempDir = extractDir
	paths.RequiresCleanup = true
	defer func() {
		shouldCleanup := true
		if opts.Cleanup != nil {
			shouldCleanup = *opts.Cleanup
		} else if cluster.Conf != nil {
			shouldCleanup = cluster.Conf.BackupResticReseedCleanup
		}
		if shouldCleanup {
			paths.RequiresCleanup = true
			if err := server.cleanupResticReseed(paths); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Cleanup failed: %s", err)
			}
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Cleanup disabled for restic restore reseed")
		}
	}()

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Created restic restore temp dir: %s",
		extractDir)

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	requiredBytes, ok := getSnapshotSizeBytes(cluster, snapshotID)
	if !ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Skipping disk space check for snapshot %s: size metadata unavailable",
			snapshotID)
	} else {
		requiredWithMargin := requiredBytes + (requiredBytes / 10)
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Checking disk space for snapshot %s restore: required=%s (includes 10%% safety margin)",
			snapshotID, humanize.Bytes(requiredWithMargin))
		if err := resticCheckDiskSpace(extractDir, requiredWithMargin); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Disk space check failed for snapshot %s: %s", snapshotID, err)
			return err
		}
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Extracting snapshot %s to %s using restore strategy",
		snapshotID, extractDir)

	overwrite := opts.Overwrite
	if overwrite == "" {
		overwrite = "if-newer"
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Restoring snapshot %s with overwrite policy %s (sources=%d)",
		snapshotID, overwrite, len(paths.SourcePaths))

	if err := resticRestoreSnapshot(ctx, cluster.ResticManager, snapshotID, extractDir, paths.SourcePaths, overwrite); err != nil {
		return fmt.Errorf("failed to restore from restic: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	paths.TargetPaths = make([]string, len(paths.SourcePaths))
	for i, sourcePath := range paths.SourcePaths {
		paths.TargetPaths[i] = filepath.Join(extractDir, sourcePath)
	}
	if len(paths.TargetPaths) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic restore target ready: %s",
			paths.TargetPaths[0])
	}

	if err := server.verifyRestoredBackup(paths); err != nil {
		return fmt.Errorf("backup verification failed: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully extracted and verified snapshot %s",
		snapshotID)
	if len(paths.TargetPaths) == 0 {
		return fmt.Errorf("no target paths available for reseed")
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	switch normalizedMethod {
	case "logical":
		return server.JobReseedLogicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	case "physical":
		return server.JobReseedPhysicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	default:
		return fmt.Errorf("invalid reseed method: %s", method)
	}
}

func (server *ServerMonitor) reseedFromResticDump(ctx context.Context, snapshotID, method string, opts ResticReseedOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic dump reseed canceled: %w", ctx.Err())
	default:
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
	}

	paths, err := server.prepareResticReseedPaths(snapshotID, method)
	if err != nil {
		return fmt.Errorf("failed to prepare paths: %w", err)
	}

	if paths.IsDirectory {
		return fmt.Errorf("dump strategy cannot be used with directory-based backups (type: %s)", paths.BackupType)
	}

	if len(paths.SourcePaths) != 1 {
		return fmt.Errorf("dump strategy requires exactly one file, got %d", len(paths.SourcePaths))
	}

	sourceFile := paths.SourcePaths[0]
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if normalizedMethod == "logical" && paths.BackupType == config.ConstBackupLogicalTypeMysqldump {
		// Supported
	} else if normalizedMethod == "physical" {
		return fmt.Errorf("dump strategy not supported for physical backups (use restore strategy)")
	} else {
		return fmt.Errorf("dump strategy not supported for backup type: %s", paths.BackupType)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Dump strategy supported for backup type %s; starting stream",
		paths.BackupType)

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Streaming snapshot %s using dump strategy (file: %s)",
		snapshotID, sourceFile)

	return server.reseedMysqldumpFromResticStream(ctx, snapshotID, sourceFile)
}

func (server *ServerMonitor) executeMysqlRestore(reader io.Reader) error {
	if reader == nil {
		return fmt.Errorf("mysql restore reader is nil")
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}

	mysqlPath := cluster.GetMysqlclientPath()
	if strings.TrimSpace(mysqlPath) == "" {
		return fmt.Errorf("mysql client path is empty")
	}
	if _, err := os.Stat(mysqlPath); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"mysql client not found at %s: %s", mysqlPath, err)
		return fmt.Errorf("mysql client not found at %s: %w", mysqlPath, err)
	}

	args := []string{
		fmt.Sprintf("--host=%s", server.Host),
		fmt.Sprintf("--port=%s", server.Port),
		fmt.Sprintf("--user=%s", cluster.GetDbUser()),
		fmt.Sprintf("--password=%s", cluster.GetDbPass()),
		"--force",
	}

	cmd := exec.Command(mysqlPath, args...)
	cmd.Stdin = reader
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errOutput := strings.TrimSpace(stderr.String())
		if errOutput == "" {
			errOutput = err.Error()
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"mysql restore failed: %s", errOutput)
		return fmt.Errorf("mysql restore failed: %s: %w", errOutput, err)
	}

	return nil
}

func (server *ServerMonitor) reseedMysqldumpFromResticStream(ctx context.Context, snapshotID, filePath string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
	}
	if strings.TrimSpace(snapshotID) == "" {
		return fmt.Errorf("snapshot id is required")
	}
	if strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("file path is required")
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic dump reseed canceled: %w", ctx.Err())
	default:
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Starting mysqldump stream from restic: snapshot=%s file=%s",
		snapshotID, filePath)

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	ctxErrCh := make(chan error, 1)

	go func() {
		<-ctx.Done()
		_ = pr.CloseWithError(ctx.Err())
		_ = pw.CloseWithError(ctx.Err())
		select {
		case ctxErrCh <- ctx.Err():
		default:
		}
	}()

	go func() {
		var dumpErr error
		if ctx.Err() != nil {
			dumpErr = ctx.Err()
			_ = pw.CloseWithError(dumpErr)
			errCh <- dumpErr
			return
		}
		dumpErr = cluster.ResticManager.DumpSnapshot(snapshotID, filePath, pw)
		if dumpErr != nil {
			_ = pw.CloseWithError(dumpErr)
		} else {
			_ = pw.Close()
		}
		errCh <- dumpErr
	}()

	var reader io.Reader = pr
	var gzReader *gzip.Reader
	if strings.HasSuffix(strings.ToLower(filePath), ".gz") {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Detected gzip-compressed mysqldump stream: %s",
			filePath)
		var err error
		gzReader, err = gzip.NewReader(pr)
		if err != nil {
			_ = pr.CloseWithError(err)
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	restoreErr := server.executeMysqlRestore(reader)
	if restoreErr != nil {
		_ = pr.CloseWithError(restoreErr)
	}

	dumpErr := <-errCh
	var ctxErr error
	select {
	case ctxErr = <-ctxErrCh:
	default:
	}

	if ctxErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Mysqldump restic stream canceled: %s", ctxErr)
		return fmt.Errorf("restic dump reseed canceled: %w", ctxErr)
	}

	if dumpErr != nil && restoreErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Mysqldump restic stream failed: dump=%s restore=%s", dumpErr, restoreErr)
		return fmt.Errorf("restic dump failed: %v; mysql restore failed: %v", dumpErr, restoreErr)
	}
	if dumpErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Mysqldump restic dump failed: %s", dumpErr)
		return fmt.Errorf("restic dump failed: %w", dumpErr)
	}
	if restoreErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Mysql restore failed during restic stream: %s", restoreErr)
		return fmt.Errorf("mysql restore failed: %w", restoreErr)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Completed mysqldump stream from restic: snapshot=%s", snapshotID)

	return nil
}

func (server *ServerMonitor) reseedFromResticMount(ctx context.Context, snapshotID, method string, opts ResticReseedOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return fmt.Errorf("cluster not available")
	}
	if cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
	}
	if cluster.ResticManager.IsMountDisabled() {
		return fmt.Errorf("mount operations are disabled (FUSE not available)")
	}

	paths, err := server.prepareResticReseedPaths(snapshotID, method)
	if err != nil {
		return fmt.Errorf("failed to prepare paths: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	tempDir := opts.TempDir
	if tempDir == "" {
		if cluster.Conf.BackupResticReseedTempDir != "" {
			tempDir = cluster.Conf.BackupResticReseedTempDir
		} else {
			tempDir = os.TempDir()
		}
	}

	mountDir, err := resticMkdirTemp(tempDir, "restic-mount-*")
	if err != nil {
		return fmt.Errorf("failed to create mount dir: %w", err)
	}

	// Register cleanup immediately after resource allocation to prevent leaks
	paths.TempDir = mountDir
	paths.RequiresCleanup = true
	paths.IsMounted = true
	defer func() {
		shouldCleanup := true
		if opts.Cleanup != nil {
			shouldCleanup = *opts.Cleanup
		} else if cluster.Conf != nil {
			shouldCleanup = cluster.Conf.BackupResticReseedCleanup
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Cleanup after restic mount reseed: enabled=%t",
			shouldCleanup)
		if shouldCleanup {
			paths.RequiresCleanup = true
			if err := server.cleanupResticReseed(paths); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Cleanup failed: %s", err)
			}
		}
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Mounting restic repo at %s",
		mountDir)

	if err := resticMountRepo(ctx, cluster.ResticManager, mountDir); err != nil {
		return fmt.Errorf("failed to mount restic repo: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully mounted restic repo at %s",
		mountDir)

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	snapshotPath := filepath.Join(mountDir, "snapshots", snapshotID)
	paths.TargetPaths = make([]string, len(paths.SourcePaths))
	for i, sourcePath := range paths.SourcePaths {
		paths.TargetPaths[i] = filepath.Join(snapshotPath, sourcePath)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Using mounted snapshot path: %s (sources=%d)",
		snapshotPath, len(paths.SourcePaths))

	if err := server.verifyRestoredBackup(paths); err != nil {
		return fmt.Errorf("backup verification failed: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully verified mounted snapshot %s",
		snapshotID)
	if len(paths.TargetPaths) == 0 {
		return fmt.Errorf("no target paths available for reseed")
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	switch normalizedMethod {
	case "logical":
		return server.JobReseedLogicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	case "physical":
		return server.JobReseedPhysicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	default:
		return fmt.Errorf("invalid reseed method: %s", method)
	}
}
