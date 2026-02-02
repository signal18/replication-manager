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
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
)

// ResticReseedPaths tracks paths and temporary locations used during a restic reseed.
type ResticReseedPaths struct {
	// SnapshotID is the restic snapshot ID to restore from.
	SnapshotID string
	// BackupType is the backup type for the snapshot (mysqldump, mydumper, xtrabackup, mariabackup).
	BackupType string
	// IsDirectory reports whether the backup is directory-based (true for mydumper).
	IsDirectory bool
	// SourceBasePath is the base path inside the restic snapshot for the backup. If empty, paths are relative to root.
	SourceBasePath string
	// SourcePaths lists the paths inside the restic snapshot to restore (from SourceBasePath).
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
	// UseTempDir overrides temp dir usage; nil means use default selection.
	// Set to false to restore directly into the target directory.
	UseTempDir *bool
	// Cleanup overrides cleanup behavior; nil means use the configuration default.
	Cleanup *bool
	// Timeout is the reseed timeout in seconds; 0 means use the configuration default.
	Timeout int
	// Overwrite defines the restic overwrite policy (e.g. "", "always", "if-newer").
	Overwrite string
}

// ResticReseedCleanupEntry tracks deferred cleanup work for async reseed tasks.
type ResticReseedCleanupEntry struct {
	Paths *ResticReseedPaths
}

// ResticReseedRequest captures a queued restic reseed request for asynchronous processing.
type ResticReseedRequest struct {
	SnapshotID string
	Method     string
	Strategy   string
	Options    ResticReseedOptions
}

func resticLogSnapshotID(cluster *Cluster, snapshotID string) string {
	trimmed := strings.TrimSpace(snapshotID)
	if trimmed == "" {
		return ""
	}
	if trimmed == "latest" {
		return trimmed
	}
	if cluster != nil && cluster.ResticManager != nil {
		snap := cluster.ResticManager.GetSnapshot(trimmed)
		if snap != nil {
			shortID := strings.TrimSpace(snap.ShortId)
			if shortID != "" {
				return shortID
			}
		}
	}
	if len(trimmed) > 8 {
		return trimmed[:8]
	}
	return trimmed
}

// QueueResticReseed stores a pending restic reseed request for later processing.
func (server *ServerMonitor) QueueResticReseed(req ResticReseedRequest) error {
	if strings.TrimSpace(req.SnapshotID) == "" {
		return fmt.Errorf("snapshot ID is required")
	}
	server.resticReseedMutex.Lock()
	defer server.resticReseedMutex.Unlock()
	if server.pendingResticReseed != nil {
		return fmt.Errorf("restic reseed already queued")
	}
	copyReq := req
	server.pendingResticReseed = &copyReq
	return nil
}

// DequeueResticReseed retrieves and clears the pending restic reseed request.
func (server *ServerMonitor) DequeueResticReseed() (ResticReseedRequest, bool) {
	server.resticReseedMutex.Lock()
	defer server.resticReseedMutex.Unlock()
	if server.pendingResticReseed == nil {
		return ResticReseedRequest{}, false
	}
	request := *server.pendingResticReseed
	server.pendingResticReseed = nil
	return request, true
}

// resolveResticReseedStrategy determines the single best restic reseed strategy to use.
//
// It honors explicit user requests and otherwise auto-selects the optimal strategy
// based on snapshot metadata, reseed method, and FUSE availability. The auto
// selection now prefers mount whenever FUSE is available to avoid directory
// extraction, with a special-case for mysqldump single-file streams that can use
// dump efficiently.
// Any unknown or missing inputs fall back to the conservative "restore" strategy.
//
// This function returns a single strategy (no fallback chain) to avoid partial state
// corruption, resource leaks, and misleading error messages that can occur when
// multiple strategies are attempted sequentially.
func resolveResticReseedStrategy(requestedStrategy, method, snapshotID string, cluster *Cluster) string {
	normalizedRequested := strings.ToLower(strings.TrimSpace(requestedStrategy))
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))

	// Determine FUSE availability early for mount-based strategies.
	fuseAvailable := cluster != nil && cluster.ResticManager != nil && !cluster.ResticManager.IsMountDisabled()

	var strategy string

	// Honor explicit user requests first (no fallback).
	switch normalizedRequested {
	case "restore":
		strategy = "restore"
	case "dump":
		strategy = "dump"
	case "mount":
		if fuseAvailable {
			strategy = "mount"
		} else {
			// Mount requested but FUSE unavailable - fall back to restore
			// rather than failing, since this is still an explicit user request.
			strategy = "restore"
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Mount strategy requested but FUSE unavailable, using restore instead")
			}
		}
	case "", "auto":
		// Continue to auto-selection below.
	default:
		// Unknown strategy name; fall back to auto-selection instead of failing.
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Unknown strategy '%s', using auto-selection", normalizedRequested)
		}
	}

	// Auto-select optimal strategy based on snapshot metadata.
	if strategy == "" {
		metadata := getSnapshotMetadata(cluster, snapshotID)
		if metadata == nil {
			// Without metadata we choose the safest option.
			strategy = "restore"
		} else {
			backupTool := strings.ToLower(strings.TrimSpace(metadata.BackupTool))
			backupMethod := strings.ToLower(strings.TrimSpace(metadata.BackupMethod))

			// Prefer mysqldump streaming when it is a logical, single-file snapshot.
			if backupTool == config.ConstBackupLogicalTypeMysqldump {
				if normalizedMethod == "logical" || (normalizedMethod == "" && backupMethod == "logical") {
					strategy = "dump"
				}
			}

			// Prefer mount for all backup types when FUSE is available to avoid extraction.
			if strategy == "" {
				if fuseAvailable {
					strategy = "mount"
				} else {
					strategy = "restore"
				}
			}

			// Unknown or unsupported tool; default to the safest strategy.
			if strategy == "" {
				strategy = "restore"
			}
		}
	}

	if cluster != nil {
		logSnapshotID := resticLogSnapshotID(cluster, snapshotID)
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Resolved restic reseed strategy: requested=%s method=%s snapshot=%s strategy=%s",
			normalizedRequested, normalizedMethod, logSnapshotID, strategy)
	}

	return strategy
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

func getSnapshotMetadataForMethod(cluster *Cluster, snapshotID, method string) *SnapshotMetadataSummary {
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if normalizedMethod == "" {
		return getSnapshotMetadata(cluster, snapshotID)
	}
	if cluster == nil || strings.TrimSpace(snapshotID) == "" {
		return nil
	}
	selectBestSummary := func(candidates []*SnapshotMetadataSummary) *SnapshotMetadataSummary {
		var selected *SnapshotMetadataSummary
		for _, candidate := range candidates {
			if candidate == nil {
				continue
			}
			if strings.ToLower(strings.TrimSpace(candidate.BackupMethod)) != normalizedMethod {
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

// getSnapshotLogicalSplitUser returns split-user flag from backup metadata when available.
// Returns (splitUser, true) when matching metadata is found.
func getSnapshotLogicalSplitUser(cluster *Cluster, snapshotID string) (bool, bool) {
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
		if meta.BackupMethod != backupmgr.BackupMethodLogical {
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
	return selected.SplitUser, true
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
	metadata := getSnapshotMetadataForMethod(cluster, snapshotID, method)
	if metadata == nil {
		return nil, fmt.Errorf("snapshot metadata not available for %s (method %s)", snapshotID, strings.ToLower(strings.TrimSpace(method)))
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
		SourceBasePath:  strings.TrimSpace(metadata.ResticBasePath),
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
	logSnapshotID := resticLogSnapshotID(cluster, snapshotID)
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Prepared restic reseed paths: snapshot=%s tool=%s isDir=%t sources=%d source=%s compressed=%t (%s)",
		logSnapshotID, backupTool, isDirectory, len(sourcePaths), logSource, compressed, compressionSource)

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
			isEmpty, err := misc.IsDirEmpty(targetPath)
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
		logSnapshotID := resticLogSnapshotID(cluster, paths.SnapshotID)
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Verified restored backup: snapshot=%s targets=%d dir=%t",
			logSnapshotID, len(paths.TargetPaths), paths.IsDirectory)
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

	// NOTE: We no longer automatically unmount here because mounts are now persistent
	// and managed via ON/OFF toggle API. Mount references are released by the defer
	// in reseedFromResticMount(), which allows unmount to proceed when requested.
	// The IsMounted flag now just indicates that the reseed used a mount strategy,
	// but cleanup no longer unmounts automatically.
	if paths.IsMounted {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Reseed used mount strategy - mount reference has been released but mount remains active for reuse")
	}

	// Remove temporary directory if it exists.
	if paths.TempDir != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Cleaning up temp directory: %s", paths.TempDir)

		if err := os.RemoveAll(paths.TempDir); err != nil {
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

func resolveResticReseedCleanup(opts ResticReseedOptions, conf *config.Config) bool {
	if opts.Cleanup != nil {
		return *opts.Cleanup
	}
	if conf != nil {
		return conf.BackupResticReseedCleanup
	}
	return true
}

func (server *ServerMonitor) runResticReseedCleanup(paths *ResticReseedPaths, shouldCleanup bool, reason string) {
	cluster := server.ClusterGroup
	if !shouldCleanup {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Cleanup disabled for restic reseed (%s)", reason)
		}
		return
	}

	paths.RequiresCleanup = true
	if cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Running restic reseed cleanup (%s)", reason)
	}

	if err := server.cleanupResticReseed(paths); err != nil {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Cleanup failed: %s", err)
		}
	}
}

func (server *ServerMonitor) registerResticReseedCleanup(task string, paths *ResticReseedPaths, shouldCleanup bool) {
	if strings.TrimSpace(task) == "" || paths == nil {
		return
	}
	cluster := server.ClusterGroup
	if !shouldCleanup {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Cleanup disabled for async restic reseed task %s", task)
		}
		return
	}
	paths.RequiresCleanup = true
	server.resticReseedCleanupMutex.Lock()
	if server.resticReseedCleanup == nil {
		server.resticReseedCleanup = make(map[string]*ResticReseedCleanupEntry)
	}
	server.resticReseedCleanup[task] = &ResticReseedCleanupEntry{Paths: paths}
	server.resticReseedCleanupMutex.Unlock()
	if cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Registered restic reseed cleanup for task %s", task)
	}
}

func (server *ServerMonitor) cleanupResticReseedForTask(task, reason string) {
	if strings.TrimSpace(task) == "" {
		return
	}
	var entry *ResticReseedCleanupEntry
	server.resticReseedCleanupMutex.Lock()
	if server.resticReseedCleanup != nil {
		entry = server.resticReseedCleanup[task]
		if entry != nil {
			delete(server.resticReseedCleanup, task)
		}
	}
	server.resticReseedCleanupMutex.Unlock()
	if entry == nil || entry.Paths == nil {
		return
	}
	cluster := server.ClusterGroup
	if cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Cleaning up restic reseed resources for task %s (%s)", task, reason)
	}
	server.runResticReseedCleanup(entry.Paths, true, reason)
}

var resticReseedTimeout = func(opts ResticReseedOptions, conf *config.Config) time.Duration {
	if opts.Timeout > 0 {
		return time.Duration(opts.Timeout) * time.Second
	}
	if conf != nil && conf.BackupResticReseedTimeout > 0 {
		return time.Duration(conf.BackupResticReseedTimeout) * time.Second
	}
	return 1 * time.Hour
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

	if cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
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

	selectedStrategy := resolveResticReseedStrategy(strategy, normalizedMethod, snapshotID, cluster)
	logSnapshotID := resticLogSnapshotID(cluster, snapshotID)

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Starting restic reseed: snapshot=%s, method=%s, strategy=%s",
		logSnapshotID, normalizedMethod, selectedStrategy)

	timeout := resticReseedTimeout(opts, cluster.Conf)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic reseed timeout after %v", timeout)
	default:
	}

	var err error
	switch selectedStrategy {
	case "restore":
		err = server.reseedFromResticRestore(ctx, snapshotID, normalizedMethod, opts)
	case "dump":
		err = server.reseedFromResticDump(ctx, snapshotID, normalizedMethod)
	case "mount":
		err = server.reseedFromResticMount(ctx, snapshotID, normalizedMethod)
	default:
		return fmt.Errorf("unknown strategy: %s", selectedStrategy)
	}

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Restic reseed failed with strategy %s: %s",
			selectedStrategy, err)
		return fmt.Errorf("reseed strategy %s failed: %w", selectedStrategy, err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully completed restic reseed using strategy: %s",
		selectedStrategy)

	return nil
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
	resticVersion, found := cluster.GetToolsVersion("restic")
	if !found {
		return fmt.Errorf("restic version not found")
	}

	if resticVersion.Lower("0.17") && opts.Overwrite != "" {
		return fmt.Errorf("restic overwrite option requires restic version 0.17 or higher")
	}

	snap := cluster.ResticManager.GetSnapshot(snapshotID)
	if snap == nil {
		return fmt.Errorf("restic snapshot %s not found", snapshotID)
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	shortID := strings.TrimSpace(snap.ShortId)

	paths, err := server.prepareResticReseedPaths(snapshotID, method)
	if err != nil {
		return fmt.Errorf("failed to prepare paths: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	tempBaseDir := opts.TempDir
	if tempBaseDir == "" {
		if cluster.Conf.BackupResticReseedTempDir != "" {
			tempBaseDir = cluster.Conf.BackupResticReseedTempDir
		} else {
			tempBaseDir = filepath.Join(cluster.WorkingDir, "backup", "restic_temp")
		}
	}

	os.MkdirAll(tempBaseDir, os.ModePerm)

	restoreBaseDir := strings.TrimSpace(server.GetMyBackupDirectory())
	if restoreBaseDir == "" {
		return fmt.Errorf("restore base directory is empty")
	}

	useTempDir := true
	if opts.UseTempDir != nil {
		if !*opts.UseTempDir {
			useTempDir = false
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Temp dir disabled by request; restoring directly to %s",
				restoreBaseDir)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Temp dir explicitly requested; checking disk space for temp restore")
		}
	}

	requiredBytes, ok := getSnapshotSizeBytes(cluster, snapshotID)
	if !useTempDir {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Skipping temp dir disk space check for snapshot %s (temp dir disabled)",
			shortID)
	} else if !ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Skipping disk space check for snapshot %s: size metadata unavailable",
			shortID)
	} else {
		requiredWithMargin := requiredBytes + (requiredBytes / 10)
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Checking disk space for snapshot %s temp restore: required=%s (includes 10%% safety margin)",
			shortID, humanize.Bytes(requiredWithMargin))
		if err := misc.CheckDiskSpace(tempBaseDir, requiredWithMargin); err != nil {
			if misc.IsInsufficientDiskSpaceError(err) {
				useTempDir = false
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Insufficient disk space for temp dir %s (%s); restoring directly to %s",
					tempBaseDir, err, restoreBaseDir)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Disk space check failed for snapshot %s: %s", shortID, err)
				return err
			}
		}
	}

	var extractDir string
	if useTempDir {
		extractDir, err = os.MkdirTemp(tempBaseDir, snap.ShortId)
		if err != nil {
			return fmt.Errorf("failed to create temp dir: %w", err)
		}
		paths.TempDir = extractDir
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Created restic restore temp dir: %s",
			extractDir)
	} else {
		extractDir = restoreBaseDir
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Using direct restore target directory: %s",
			extractDir)
	}

	// Register cleanup immediately after resource allocation to prevent leaks
	shouldCleanup := resolveResticReseedCleanup(opts, cluster.Conf)
	if !useTempDir {
		shouldCleanup = false
	}
	paths.RequiresCleanup = shouldCleanup
	cleanupOnExit := false
	if normalizedMethod == "logical" {
		defer server.runResticReseedCleanup(paths, shouldCleanup, "restic restore reseed")
	} else if normalizedMethod == "physical" {
		cleanupOnExit = true
		defer func() {
			if cleanupOnExit {
				server.runResticReseedCleanup(paths, shouldCleanup, "restic restore reseed aborted")
			}
		}()
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Extracting snapshot %s to %s using restore strategy",
		resticLogSnapshotID(cluster, snapshotID), extractDir)

	overwrite := opts.Overwrite
	if overwrite == "" {
		overwrite = "if-newer"
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Restoring snapshot %s with overwrite policy %s (sources=%d)",
		resticLogSnapshotID(cluster, snapshotID), overwrite, len(paths.SourcePaths))

	if err := cluster.ResticManager.RestoreSnapshotSync(snapshotID, extractDir, paths.SourcePaths, overwrite); err != nil {
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
		resticLogSnapshotID(cluster, snapshotID))
	if len(paths.TargetPaths) == 0 {
		return fmt.Errorf("no target paths available for reseed")
	}

	switch normalizedMethod {
	case "logical":
		logicalOpts := JobReseedLogicalOptions{}
		if splitUser, ok := getSnapshotLogicalSplitUser(cluster, snapshotID); ok {
			logicalOpts.SplitUser = &splitUser
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Using restic metadata split-user=%t for snapshot %s",
				splitUser, resticLogSnapshotID(cluster, snapshotID))
		}
		return server.JobReseedLogicalBackupFromPathWithOptions(paths.BackupType, paths.TargetPaths[0], logicalOpts)
	case "physical":
		payload := map[string]string{
			"restic_snapshot_id": snapshotID,
		}
		sourceBase := strings.TrimSpace(paths.SourceBasePath)
		if sourceBase != "" {
			payload["restic_source_base_path"] = sourceBase
		}
		if len(paths.SourcePaths) > 0 {
			sourcePath := filepath.Join(sourceBase, paths.SourcePaths[0])
			if strings.TrimSpace(sourcePath) != "" {
				payload["restic_source_path"] = sourcePath
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Including restic metadata for physical reseed: snapshot=%s source=%s",
			resticLogSnapshotID(cluster, snapshotID), payload["restic_source_path"])
		err := server.JobReseedPhysicalBackupWithPayload(paths.BackupType, paths.TargetPaths[0], payload)
		if err != nil {
			return err
		}
		cleanupOnExit = false
		server.registerResticReseedCleanup("reseed"+paths.BackupType, paths, shouldCleanup)
		return nil
	default:
		return fmt.Errorf("invalid reseed method: %s", method)
	}
}

func (server *ServerMonitor) reseedFromResticDump(ctx context.Context, snapshotID, method string) error {
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
	snap := cluster.ResticManager.GetSnapshot(snapshotID)
	if snap == nil {
		return fmt.Errorf("restic snapshot %s not found", snapshotID)
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

	sourceFile := filepath.Join(paths.SourceBasePath, paths.SourcePaths[0])
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if normalizedMethod == "logical" && paths.BackupType == config.ConstBackupLogicalTypeMysqldump {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Dump strategy supported for backup type %s; starting stream",
			paths.BackupType)

		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Streaming snapshot %s using dump strategy (file: %s)",
			snap.ShortId, sourceFile)

		return server.reseedMysqldumpFromResticStream(ctx, snapshotID, sourceFile)
	}
	if normalizedMethod != "physical" {
		return fmt.Errorf("dump strategy not supported for backup type: %s", paths.BackupType)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Dump strategy supported for physical backup %s; preparing SST stream",
		paths.BackupType)

	payload := map[string]string{
		"restic_snapshot_id":      snapshotID,
		"restic_source_base_path": paths.SourceBasePath,
		"restic_source_path":      sourceFile,
	}
	if err := server.JobReseedPhysicalBackupWithPayload(paths.BackupType, "STREAM", payload); err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Queued SST stream for snapshot %s (file: %s)",
		resticLogSnapshotID(cluster, snapshotID), sourceFile)

	return nil
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

	snap := cluster.ResticManager.GetSnapshot(snapshotID)
	if snap == nil {
		return fmt.Errorf("snapshot %s not found in restic manager", snapshotID)
	}

	logSnapshotID := snap.ShortId
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Starting mysqldump stream from restic: snapshot=%s file=%s",
		logSnapshotID, filePath)

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
	var gzReader *pgzip.Reader
	if strings.HasSuffix(strings.ToLower(filePath), ".gz") {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Detected gzip-compressed mysqldump stream: %s",
			filePath)
		var err error
		gzReader, err = pgzip.NewReaderN(pr, cluster.Conf.SSTSendBuffer, cluster.Conf.CompressBackupsParallelBlocks)
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
		"Completed mysqldump stream from restic: snapshot=%s", logSnapshotID)

	return nil
}

func (server *ServerMonitor) reseedFromResticMount(ctx context.Context, snapshotID, method string) error {
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

	snap := cluster.ResticManager.GetSnapshot(snapshotID)
	if snap == nil {
		return fmt.Errorf("snapshot %s not found in restic manager", resticLogSnapshotID(cluster, snapshotID))
	}

	normalizedMethod := strings.ToLower(strings.TrimSpace(method))

	paths, err := server.prepareResticReseedPaths(snapshotID, method)
	if err != nil {
		return fmt.Errorf("failed to prepare paths: %w", err)
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	clusterName := strings.TrimSpace(cluster.GetClusterName())
	if clusterName == "" {
		return fmt.Errorf("cluster name is empty")
	}

	mountDir := filepath.Join("/mnt/restic", clusterName)
	if cluster.Conf.BackupResticMountDir != "" {
		mountDir = cluster.Conf.BackupResticMountDir
	}
	if err := os.MkdirAll(mountDir, 0o755); err != nil {
		return fmt.Errorf("failed to create mount dir: %w", err)
	}

	// Register cleanup immediately after resource allocation to prevent leaks
	paths.TempDir = mountDir
	paths.IsMounted = true

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	shortID := snap.ShortId
	// Generate unique user ID for this reseed operation
	userID := fmt.Sprintf("reseed-%s-%s-%d", server.Id, shortID, time.Now().UnixNano())

	mountOpt := backupmgr.NewResticMountOption(mountDir)
	mountOpt.PathTemplate = []string{"ids/%i"}
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Mounting restic repo with short-id path template 'ids/%i' (shortID: %s, userID: %s)",
		shortID, userID)

	// Mount the repo (will reuse existing mount if already mounted at same path)
	if err := cluster.ResticManager.MountRepoWithOptions(mountOpt); err != nil {
		return fmt.Errorf("failed to mount restic repo: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully mounted restic repo at %s",
		mountDir)

	// Acquire mount reference to prevent unmount while we're using it
	if err := cluster.ResticManager.AcquireMountRef(userID); err != nil {
		return fmt.Errorf("failed to acquire mount reference: %w", err)
	}

	// Ensure mount reference is released when we're done
	defer func() {
		cluster.ResticManager.ReleaseMountRef(userID)
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlDbg,
			"Released mount reference for userID: %s", userID)
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	snapshotPath := filepath.Join(mountDir, fmt.Sprintf("ids/%s", shortID))
	if err := waitForResticSnapshotPath(ctx, snapshotPath); err != nil {
		return fmt.Errorf("snapshot path not ready: %w", err)
	}

	if len(paths.SourcePaths) == 0 {
		return fmt.Errorf("no source paths available in snapshot %s", resticLogSnapshotID(cluster, shortID))
	}

	paths.TargetPaths = append(paths.TargetPaths, filepath.Join(snapshotPath, paths.SourceBasePath, paths.SourcePaths[0]))

	if err := server.verifyRestoredBackup(paths); err != nil {
		return fmt.Errorf("backup verification failed: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully verified mounted snapshot %s",
		shortID)
	if len(paths.TargetPaths) == 0 {
		return fmt.Errorf("no target paths available for reseed")
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	switch normalizedMethod {
	case "logical":
		return server.JobReseedLogicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	case "physical":
		return server.JobReseedPhysicalBackupFromPath(paths.BackupType, paths.TargetPaths[0])
	default:
		return fmt.Errorf("invalid reseed method: %s", method)
	}
}

func waitForResticSnapshotPath(ctx context.Context, snapshotPath string) error {
	if snapshotPath == "" {
		return fmt.Errorf("snapshot path is empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := os.Stat(snapshotPath); err == nil {
		return nil
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(snapshotPath); err == nil {
				return nil
			}
		}
	}
}
