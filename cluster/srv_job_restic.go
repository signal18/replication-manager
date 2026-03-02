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
	"io"
	"os"
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
	Paths       *ResticReseedPaths
	MountUserID string
}

// ResticReseedRequest captures a queued restic reseed request for asynchronous processing.
type ResticReseedRequest struct {
	SnapshotID string
	Method     string
	Strategy   string
	Options    ResticReseedOptions
}

func (server *ServerMonitor) buildResticReseedPayload(summary *SnapshotMetadataSummary, sourceBase, strategy string) map[string]string {
	payload := map[string]string{}
	if summary == nil {
		return payload
	}
	if strings.TrimSpace(summary.ResticSnapshotID) != "" {
		payload["restic_snapshot_id"] = strings.TrimSpace(summary.ResticSnapshotID)
	}
	if strings.TrimSpace(strategy) != "" {
		payload["restic_reseed_strategy"] = strings.TrimSpace(strategy)
	}
	base := strings.TrimSpace(sourceBase)
	if base == "" {
		base = strings.TrimSpace(summary.ResticBasePath)
	}
	if base != "" {
		payload["restic_source_base_path"] = base
	}
	if strings.TrimSpace(summary.Dest) != "" {
		payload["restic_source_path"] = strings.TrimSpace(summary.Dest)
	}
	return payload
}

// QueueResticReseed stores a pending restic reseed request for later processing.
func (server *ServerMonitor) QueueResticReseed(req ResticReseedRequest) error {
	if strings.TrimSpace(req.SnapshotID) == "" {
		return fmt.Errorf("snapshot ID is required")
	}
	server.resticReseedMutex.Lock()
	defer server.resticReseedMutex.Unlock()
	if server.HasWaitResticReseedCookie() {
		return fmt.Errorf("restic reseed already in progress")
	}
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
		metadata := getSnapshotMetadata(cluster, snapshotID, nil)
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
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Resolved restic reseed strategy: requested=%s method=%s snapshot=%s strategy=%s",
			normalizedRequested, normalizedMethod, resticLogSnapshotID(cluster, snapshotID), strategy)
	}

	return strategy
}

// getSnapshotMetadata selects the best available metadata summary for a restic snapshot.
// It prefers cached metadata derived from snapshot metadata files, with a fallback to
// backup metadata summaries when no extracted metadata is available.
func getSnapshotMetadata(cluster *Cluster, snapshotID string, index SnapshotMetadataIndex) *SnapshotMetadataSummary {
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
	if len(index) > 0 {
		if selected := selectBestSummary(index[snapshotID]); selected != nil {
			return selected
		}
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

func getSnapshotMetadataForMethod(cluster *Cluster, snapshotID, method string, index SnapshotMetadataIndex) *SnapshotMetadataSummary {
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))
	if normalizedMethod == "" {
		return getSnapshotMetadata(cluster, snapshotID, index)
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
	if len(index) > 0 {
		if selected := selectBestSummary(index[snapshotID]); selected != nil {
			return selected
		}
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
	metadata := getSnapshotMetadataForMethod(cluster, snapshotID, method, nil)
	if metadata == nil {
		return nil, fmt.Errorf("snapshot metadata not available for %s (method %s)", snapshotID, strings.ToLower(strings.TrimSpace(method)))
	}
	backupTool := strings.ToLower(strings.TrimSpace(metadata.BackupTool))
	if backupTool == "" {
		return nil, fmt.Errorf("snapshot metadata missing backup tool for %s", snapshotID)
	}
	normalizedMethod := strings.ToLower(strings.TrimSpace(method))

	compressed, compressionSource := resolveResticReseedCompression(cluster, metadata, snapshotID)

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

	sourceBasePath := strings.TrimSpace(metadata.ResticBasePath)
	if override := resolveResticSourcePaths(metadata, &sourceBasePath, sourcePaths); len(override) > 0 {
		sourcePaths = override
	}

	paths := &ResticReseedPaths{
		SnapshotID:      snapshotID,
		BackupType:      backupTool,
		IsDirectory:     isDirectory,
		SourceBasePath:  sourceBasePath,
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
		resticLogSnapshotID(cluster, snapshotID), backupTool, isDirectory, len(sourcePaths), logSource, compressed, compressionSource)

	return paths, nil
}

func resolveResticReseedCompression(cluster *Cluster, metadata *SnapshotMetadataSummary, snapshotID string) (bool, string) {
	if cluster == nil {
		return false, "default"
	}
	if compressed, ok := compressionFromSummary(metadata); ok {
		return compressed, "snapshot-metadata"
	}
	if metaCompressed, ok := getSnapshotCompression(cluster, snapshotID); ok {
		return metaCompressed, "snapshot-metadata"
	}
	method := backupMethodFromSummary(metadata)
	return cluster.resolveBackupCompression(method, metadata.BackupTool), "config"
}

func backupMethodFromSummary(metadata *SnapshotMetadataSummary) backupmgr.BackupMethod {
	if metadata == nil {
		return backupmgr.BackupMethodLogical
	}
	switch strings.ToLower(strings.TrimSpace(metadata.BackupMethod)) {
	case "physical":
		return backupmgr.BackupMethodPhysical
	case "logical":
		return backupmgr.BackupMethodLogical
	default:
		return backupmgr.BackupMethodLogical
	}
}

func resolveResticSourcePaths(metadata *SnapshotMetadataSummary, sourceBasePath *string, defaultPaths []string) []string {
	if metadata == nil || sourceBasePath == nil {
		return defaultPaths
	}
	base := strings.TrimSpace(*sourceBasePath)
	dest := strings.TrimSpace(metadata.Dest)
	if dest == "" {
		return defaultPaths
	}
	if base == "" {
		if filepath.IsAbs(dest) {
			*sourceBasePath = filepath.Dir(dest)
			return []string{filepath.Base(dest)}
		}
		return defaultPaths
	}
	resolvedPath, ok := resolveSnapshotDestPath(base, dest)
	if !ok {
		return defaultPaths
	}
	rel, err := filepath.Rel(base, resolvedPath)
	if err != nil {
		return defaultPaths
	}
	if rel == "." || strings.HasPrefix(rel, "..") || rel == "" {
		return defaultPaths
	}
	return []string{rel}
}

func compressionFromSummary(metadata *SnapshotMetadataSummary) (bool, bool) {
	if metadata == nil {
		return false, false
	}
	if isCompressedDest(metadata.Dest) {
		return true, true
	}
	if metadata.Compressed {
		return true, true
	}
	return false, false
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
				if !paths.IsDirectory {
					if altPath := alternateCompressionPath(targetPath); altPath != "" {
						if _, altErr := os.Stat(altPath); altErr == nil {
							paths.TargetPaths[i] = altPath
							if len(paths.SourcePaths) > i {
								paths.SourcePaths[i] = filepath.Base(altPath)
							}
							continue
						}
					}
				} else {
					// Try to create the directory if missing.
					if mkErr := os.MkdirAll(targetPath, 0o755); mkErr == nil {
						continue
					}
				}
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
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Verified restored backup: snapshot=%s targets=%d dir=%t",
			resticLogSnapshotID(cluster, paths.SnapshotID), len(paths.TargetPaths), paths.IsDirectory)
	}

	return nil
}

func verifyMountedSnapshotPaths(cluster *Cluster, snapshotRoot string, targetPaths []string) error {
	trimmedRoot := strings.TrimSpace(snapshotRoot)
	if trimmedRoot == "" {
		return fmt.Errorf("snapshot root is empty")
	}
	if len(targetPaths) == 0 {
		return fmt.Errorf("no target paths to verify")
	}
	resolvedRoot, err := filepath.EvalSymlinks(trimmedRoot)
	if err != nil {
		return fmt.Errorf("failed to resolve snapshot root %s: %w", trimmedRoot, err)
	}
	for i, targetPath := range targetPaths {
		trimmedTarget := strings.TrimSpace(targetPath)
		if trimmedTarget == "" {
			return fmt.Errorf("target path %d is empty", i)
		}
		info, err := os.Lstat(trimmedTarget)
		if err != nil {
			return fmt.Errorf("failed to lstat mounted path %s: %w", trimmedTarget, err)
		}
		resolvedTarget, err := filepath.EvalSymlinks(trimmedTarget)
		if err != nil {
			return fmt.Errorf("failed to resolve symlinks for mounted path %s: %w", trimmedTarget, err)
		}
		rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
		if err != nil {
			return fmt.Errorf("failed to resolve mount path %s under %s: %w", resolvedTarget, resolvedRoot, err)
		}
		if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("mounted path %s escapes snapshot root %s", resolvedTarget, resolvedRoot)
		}
		if info.Mode()&os.ModeSymlink != 0 && cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlDbg,
				"Mounted path %s is symlink resolved to %s",
				trimmedTarget, resolvedTarget)
		}
	}
	return nil
}

func alternateCompressionPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if strings.HasSuffix(lower, ".gz") {
		return strings.TrimSuffix(trimmed, filepath.Ext(trimmed))
	}
	return trimmed + ".gz"
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
func (server *ServerMonitor) cleanupResticReseed(paths *ResticReseedPaths, mountUserID string) error {
	if paths == nil {
		return fmt.Errorf("restic reseed paths are nil")
	}

	cluster := server.ClusterGroup
	if paths.IsMounted {
		if strings.TrimSpace(mountUserID) != "" && cluster != nil && cluster.ResticManager != nil {
			if err := cluster.ResticManager.ReleaseMountRef(mountUserID); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Failed to release mount reference for userID %s: %s", mountUserID, err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlDbg,
					"Released mount reference for userID: %s", mountUserID)
			}
		}
	}

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

	if paths.IsMounted {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Reseed used mount strategy - mount reference released; mount remains active for reuse")
		return nil
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

func (server *ServerMonitor) runResticReseedCleanup(paths *ResticReseedPaths, shouldCleanup bool, reason string, mountUserID string) {
	cluster := server.ClusterGroup
	if server.HasWaitResticReseedCookie() {
		if err := server.DelWaitResticReseedCookie(); err != nil {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to clear restic reseed cookie: %s", err)
			}
		}
	}
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

	if err := server.cleanupResticReseed(paths, mountUserID); err != nil {
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Cleanup failed: %s", err)
		}
	}
}

func (server *ServerMonitor) registerResticReseedCleanup(task string, paths *ResticReseedPaths, shouldCleanup bool, mountUserID string) {
	if strings.TrimSpace(task) == "" || paths == nil {
		return
	}
	cluster := server.ClusterGroup
	if !shouldCleanup && strings.TrimSpace(mountUserID) == "" {
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
	paths.RequiresCleanup = shouldCleanup
	server.resticReseedCleanup[task] = &ResticReseedCleanupEntry{Paths: paths, MountUserID: strings.TrimSpace(mountUserID)}
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
	if server.HasWaitResticReseedCookie() {
		if err := server.DelWaitResticReseedCookie(); err != nil {
			cluster := server.ClusterGroup
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to clear restic reseed cookie: %s", err)
			}
		}
	}
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
	server.runResticReseedCleanup(entry.Paths, entry.Paths.RequiresCleanup, reason, entry.MountUserID)
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

func resticReseedTaskName(cluster *Cluster, snapshotID, method string) string {
	if cluster == nil {
		return ""
	}
	backupTool := ""
	if summary := getSnapshotMetadataForMethod(cluster, snapshotID, method, nil); summary != nil {
		backupTool = strings.TrimSpace(summary.BackupTool)
	}
	if backupTool == "" {
		normalizedMethod := strings.ToLower(strings.TrimSpace(method))
		switch normalizedMethod {
		case "physical":
			backupTool = cluster.Conf.BackupPhysicalType
		case "logical":
			backupTool = cluster.Conf.BackupLogicalType
		}
	}
	backupTool = strings.TrimSpace(backupTool)
	if backupTool == "" {
		return ""
	}
	return "reseed" + backupTool
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
	if server.HasWaitResticReseedCookie() {
		return fmt.Errorf("restic reseed already in progress")
	}
	if err := server.SetWaitResticReseedCookie(); err != nil {
		return fmt.Errorf("failed to set restic reseed cookie: %w", err)
	}
	handoffToJob := false
	defer func() {
		if handoffToJob {
			return
		}
		if server.HasWaitResticReseedCookie() {
			if err := server.DelWaitResticReseedCookie(); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to clear restic reseed cookie: %s", err)
			}
		}
	}()

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

	var ctx context.Context
	var cancel context.CancelFunc
	if normalizedMethod == "logical" {
		ctx, cancel = context.WithCancel(context.Background())
		task := resticReseedTaskName(cluster, snapshotID, normalizedMethod)
		if task != "" {
			server.registerJobCancel(task, cancel)
			defer server.clearJobCancel(task)
		}
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	defer cancel()

	if normalizedMethod == "physical" {
		select {
		case <-ctx.Done():
			return fmt.Errorf("restic reseed timeout after %v", timeout)
		default:
		}
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
		server.updateResticReseedJobError(snapshotID, normalizedMethod, err)
		return fmt.Errorf("reseed strategy %s failed: %w", selectedStrategy, err)
	}
	if normalizedMethod == "physical" {
		handoffToJob = true
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Successfully completed restic reseed using strategy: %s",
		selectedStrategy)

	return nil
}

func (server *ServerMonitor) updateResticReseedJobError(snapshotID, method string, err error) {
	if server == nil || err == nil {
		return
	}
	cluster := server.ClusterGroup
	if cluster == nil {
		return
	}
	task := resticReseedTaskName(cluster, snapshotID, method)
	if task == "" {
		return
	}
	if err := server.JobsUpdateState(task, err.Error(), 5, 1); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Failed to update restic reseed job state for %s: %s", task, err)
	}
	if server.HasReseedingState(task) {
		server.SetInReseedBackup("")
	}
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

	// Use snapshotID:/path style for restic >= 0.16
	useSubfolder := resticVersion.GreaterEqual("0.16")

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

	logSnapshotID := resticLogSnapshotID(cluster, snapshotID)
	metadataPathMatches := func(path string) (bool, error) {
		summary := getSnapshotMetadataForMethod(cluster, snapshotID, method, nil)
		if summary == nil {
			return false, fmt.Errorf("snapshot metadata not available")
		}
		path = filepath.Clean(strings.TrimSpace(path))
		dest := strings.TrimSpace(summary.Dest)
		if dest == "" {
			return false, fmt.Errorf("snapshot metadata missing dest path")
		}
		base := strings.TrimSpace(summary.ResticBasePath)
		if base == "" {
			return filepath.Clean(dest) == path, nil
		}
		resolved, ok := resolveSnapshotDestPath(base, dest)
		if !ok {
			return false, fmt.Errorf("failed to resolve snapshot dest path from metadata")
		}
		return filepath.Clean(resolved) == path, nil
	}
	checkSnapshotPath := func(path string) (bool, error) {
		if cluster.ResticManager != nil {
			exists, err := cluster.snapshotPathExists(snapshotID, path)
			if err == nil {
				return exists, nil
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to list restic snapshot %s for path %s: %s; falling back to metadata",
				logSnapshotID, path, err)
		}
		return metadataPathMatches(path)
	}

	for i, sourcePath := range paths.SourcePaths {
		trimmed := strings.TrimSpace(sourcePath)
		if trimmed == "" {
			return fmt.Errorf("source path %d is empty", i)
		}
		fullPath := filepath.Join(paths.SourceBasePath, trimmed)
		exists, err := checkSnapshotPath(fullPath)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlErr,
				"Restic snapshot path check failed: snapshot=%s path=%s err=%s",
				logSnapshotID, fullPath, err)
			return fmt.Errorf("failed to verify restic snapshot path %s: %w", fullPath, err)
		}
		if !exists && !paths.IsDirectory {
			alt := alternateCompressionPath(trimmed)
			if alt != "" && alt != trimmed {
				altPath := filepath.Join(paths.SourceBasePath, alt)
				altExists, altErr := checkSnapshotPath(altPath)
				if altErr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlErr,
						"Restic snapshot alternate path check failed: snapshot=%s path=%s err=%s",
						logSnapshotID, altPath, altErr)
					return fmt.Errorf("failed to verify restic snapshot alternate path %s: %w", altPath, altErr)
				}
				if altExists {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlWarn,
						"Restic snapshot path %s not found; using alternate %s",
						fullPath, altPath)
					paths.SourcePaths[i] = alt
					exists = true
				}
			}
		}
		if !exists {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlErr,
				"Restic snapshot missing path: snapshot=%s path=%s",
				logSnapshotID, fullPath)
			return fmt.Errorf("restic snapshot %s missing path %s", logSnapshotID, fullPath)
		}
	}

	restorePaths := paths.SourcePaths
	// For restic < 0.16, we need to provide full paths (base + source) due to lack of subfolder support.
	if !useSubfolder {
		restorePaths = make([]string, len(paths.SourcePaths))
		for i, sourcePath := range paths.SourcePaths {
			restorePaths[i] = filepath.Join(paths.SourceBasePath, sourcePath)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic version %s uses full restore paths (base + source)",
			resticVersion.ToFullString())
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic version %s uses relative restore paths",
			resticVersion.ToFullString())
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
		defer server.runResticReseedCleanup(paths, shouldCleanup, "restic restore reseed", "")
	} else if normalizedMethod == "physical" {
		defer func() {
			if cleanupOnExit {
				server.runResticReseedCleanup(paths, shouldCleanup, "restic restore reseed aborted", "")
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
		logSnapshotID, extractDir)

	overwrite := opts.Overwrite

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Restoring snapshot %s with overwrite policy %s (sources=%d)",
		logSnapshotID, overwrite, len(paths.SourcePaths))

	if useSubfolder {
		snapshotID = fmt.Sprintf("%s:%s", snapshotID, paths.SourceBasePath)
	}

	if err := server.restoreSnapshotWithFallback(cluster, snapshotID, extractDir, paths, restorePaths, overwrite); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic restore reseed canceled: %w", ctx.Err())
	default:
	}

	paths.TargetPaths = make([]string, len(paths.SourcePaths))
	for i, sourcePath := range paths.SourcePaths {
		paths.TargetPaths[i] = filepath.Join(extractDir, sourcePath)

		if !useSubfolder {
			oldPath := filepath.Join(extractDir, paths.SourceBasePath, sourcePath)
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Moving restored path from temp to final target with restic before 0.16: %s -> %s",
				oldPath, paths.TargetPaths[i])
			if err := os.Rename(oldPath, paths.TargetPaths[i]); err != nil {
				return fmt.Errorf("failed to move restored path to final target %s: %w", paths.TargetPaths[i], err)
			}
		}
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
		logSnapshotID)
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
				splitUser, logSnapshotID)
		}
		logicalOpts.SkipMetadata = true
		return server.JobReseedLogicalBackupFromPathWithOptions(ctx, paths.BackupType, paths.TargetPaths[0], logicalOpts)
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
			logSnapshotID, payload["restic_source_path"])
		cleanupOnExit = false
		err := server.JobReseedPhysicalBackupWithPayload(paths.BackupType, paths.TargetPaths[0], payload)
		if err != nil {
			server.runResticReseedCleanup(paths, shouldCleanup, "restic restore reseed aborted", "")
			return err
		}
		server.registerResticReseedCleanup("reseed"+paths.BackupType, paths, shouldCleanup, "")
		return nil
	default:
		return fmt.Errorf("invalid reseed method: %s", method)
	}
}

func (server *ServerMonitor) restoreSnapshotWithFallback(cluster *Cluster, snapshotID, extractDir string, paths *ResticReseedPaths, restorePaths []string, overwrite string) error {
	if cluster == nil || cluster.ResticManager == nil {
		return fmt.Errorf("restic manager not available")
	}
	if paths == nil || len(restorePaths) == 0 {
		return fmt.Errorf("no source paths available for restore")
	}
	if err := cluster.ResticManager.RestoreSnapshotSync(snapshotID, extractDir, restorePaths, overwrite); err == nil {
		return nil
	} else if len(restorePaths) == 1 {
		alt := alternateCompressionPath(restorePaths[0])
		if alt != "" && alt != restorePaths[0] {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Restore failed for %s; retrying with alternate path %s",
				restorePaths[0], alt)
			if err := cluster.ResticManager.RestoreSnapshotSync(snapshotID, extractDir, []string{alt}, overwrite); err == nil {
				if !filepath.IsAbs(alt) {
					paths.SourcePaths = []string{alt}
					return nil
				}
				if strings.TrimSpace(paths.SourceBasePath) != "" {
					if rel, relErr := filepath.Rel(paths.SourceBasePath, alt); relErr == nil && rel != "." && rel != "" && !strings.HasPrefix(rel, "..") {
						paths.SourcePaths = []string{rel}
						return nil
					}
				}
				paths.SourcePaths = []string{filepath.Base(alt)}
				return nil
			}
		}
		return fmt.Errorf("failed to restore from restic: %w", err)
	} else {
		return fmt.Errorf("failed to restore from restic: %w", err)
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
	logSnapshotID := resticLogSnapshotID(cluster, snapshotID)
	metadataPathMatches := func(path string) (bool, error) {
		summary := getSnapshotMetadataForMethod(cluster, snapshotID, method, nil)
		if summary == nil {
			return false, fmt.Errorf("snapshot metadata not available")
		}
		path = filepath.Clean(strings.TrimSpace(path))
		dest := strings.TrimSpace(summary.Dest)
		if dest == "" {
			return false, fmt.Errorf("snapshot metadata missing dest path")
		}
		base := strings.TrimSpace(summary.ResticBasePath)
		if base == "" {
			return filepath.Clean(dest) == path, nil
		}
		resolved, ok := resolveSnapshotDestPath(base, dest)
		if !ok {
			return false, fmt.Errorf("failed to resolve snapshot dest path from metadata")
		}
		return filepath.Clean(resolved) == path, nil
	}
	checkSnapshotPath := func(path string) (bool, error) {
		if cluster.ResticManager != nil {
			exists, err := cluster.snapshotPathExists(snapshotID, path)
			if err == nil {
				return exists, nil
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to list restic snapshot %s for file %s: %s; falling back to metadata",
				logSnapshotID, path, err)
		}
		return metadataPathMatches(path)
	}

	sourceExists, err := checkSnapshotPath(sourceFile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Restic snapshot file check failed: snapshot=%s file=%s err=%s",
			logSnapshotID, sourceFile, err)
		return fmt.Errorf("failed to verify restic snapshot file %s: %w", sourceFile, err)
	}
	if !sourceExists {
		alt := alternateCompressionPath(paths.SourcePaths[0])
		if alt != "" && alt != paths.SourcePaths[0] {
			altSource := filepath.Join(paths.SourceBasePath, alt)
			altExists, altErr := checkSnapshotPath(altSource)
			if altErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlErr,
					"Restic snapshot alternate file check failed: snapshot=%s file=%s err=%s",
					logSnapshotID, altSource, altErr)
				return fmt.Errorf("failed to verify restic snapshot alternate file %s: %w", altSource, altErr)
			}
			if altExists {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Restic snapshot file %s not found; using alternate %s",
					sourceFile, altSource)
				paths.SourcePaths[0] = alt
				sourceFile = altSource
				sourceExists = true
			}
		}
	}
	if !sourceExists {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Restic snapshot missing file: snapshot=%s file=%s",
			logSnapshotID, sourceFile)
		return fmt.Errorf("restic snapshot %s missing file %s", logSnapshotID, sourceFile)
	}

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

	if err := server.JobReseedPhysicalBackupWithPayload(paths.BackupType, "STREAM", map[string]string{
		"restic_snapshot_id":      snapshotID,
		"restic_source_base_path": paths.SourceBasePath,
		"restic_source_path":      sourceFile,
	}); err != nil {
		if len(paths.SourcePaths) == 1 {
			alt := alternateCompressionPath(paths.SourcePaths[0])
			if alt != "" && alt != paths.SourcePaths[0] {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Dump reseed failed for %s; retrying with alternate path %s",
					paths.SourcePaths[0], alt)
				return server.JobReseedPhysicalBackupWithPayload(paths.BackupType, "STREAM", map[string]string{
					"restic_snapshot_id":      snapshotID,
					"restic_source_base_path": paths.SourceBasePath,
					"restic_source_path":      filepath.Join(paths.SourceBasePath, alt),
				})
			}
		}
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Queued SST stream for snapshot %s (file: %s)",
		resticLogSnapshotID(cluster, snapshotID), sourceFile)

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
		bufferSize := cluster.getSanitizedDecompressBufferSize(config.ConstLogModRestic)
		parallelBlocks := cluster.getSanitizedParallelBlocks(config.ConstLogModRestic)
		gzReader, err = pgzip.NewReaderN(pr, bufferSize, parallelBlocks)
		if err != nil {
			_ = pr.CloseWithError(err)
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	restoreErr := server.executeMysqlRestore(reader, false)
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

	mountOpt, meta, err := cluster.parseResticMountOptionsFromConfig()
	if err != nil {
		return err
	}
	if err := cluster.sanitizeAndValidateResticMountOptions(&mountOpt, meta); err != nil {
		return err
	}
	mountDir := strings.TrimSpace(mountOpt.TargetDir)

	// Register cleanup immediately after resource allocation to prevent leaks
	paths.TempDir = mountDir
	paths.IsMounted = true

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	shortID := snap.ShortId
	fullID := strings.TrimSpace(snap.Id)
	if shortID == "" {
		shortID = fullID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
	}
	// Generate unique user ID for this reseed operation
	userID := fmt.Sprintf("reseed-%s-%s-%d", server.Id, shortID, time.Now().UnixNano())
	allowOtherSource := "config"
	if mountOpt.AllowOther {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Restic mount allow-other enabled (%s, default=true): requires user_allow_other in /etc/fuse.conf; any local user can access mounted snapshot data",
			allowOtherSource)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic mount allow-other disabled (%s): mount access restricted to the mounting user",
			allowOtherSource)
	}
	if mountOpt.NoDefaultPermissions {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Restic mount no-default-permissions enabled: bypasses Unix permission checks and can expose data; use only if necessary")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Mounting restic repo with path templates %v (shortID: %s, userID: %s)",
		mountOpt.PathTemplate, shortID, userID)

	mountPathBefore := strings.TrimSpace(cluster.ResticManager.GetMountPath())
	mountWasActive := cluster.ResticManager.IsMounted()
	mountWasActiveAtPath := mountWasActive && mountPathBefore != "" && filepath.Clean(mountPathBefore) == filepath.Clean(mountDir)

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
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Failed to acquire mount reference for userID %s: %s", userID, err)
		if !mountWasActiveAtPath {
			if unmountErr := cluster.ResticManager.UnmountRepo(); unmountErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Failed to rollback restic mount at %s after ref acquisition error: %s",
					mountDir, unmountErr)
			}
		}
		return fmt.Errorf("failed to acquire mount reference: %w", err)
	}
	cluster.ResticManager.RequestUnmountWhenIdle()
	refOwned := true

	// Release mount reference immediately only for logical reseed (physical job continues async)
	if normalizedMethod != "physical" {
		defer func() {
			if refOwned {
				if err := cluster.ResticManager.ReleaseMountRef(userID); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlWarn,
						"Failed to release mount reference for userID %s: %s", userID, err)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlDbg,
						"Released mount reference for userID: %s", userID)
				}
				refOwned = false
			}
		}()
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("restic mount reseed canceled: %w", ctx.Err())
	default:
	}

	timeValue := ""
	if snap != nil {
		snapTime, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(snap.Time))
		if parseErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to parse restic snapshot time %s: %s", snap.Time, parseErr)
		} else if mountOpt.TimeTemplate != "" {
			timeValue = snapTime.Format(mountOpt.TimeTemplate)
		} else {
			timeValue = snapTime.Format(time.RFC3339)
		}
	}
	username := ""
	hostname := ""
	tagValue := ""
	if snap != nil {
		username = strings.TrimSpace(snap.Username)
		hostname = strings.TrimSpace(snap.Hostname)
		if len(snap.Tags) > 0 {
			tagValue = strings.Join(snap.Tags, ",")
		}
	}

	seenCandidates := make(map[string]struct{})
	candidates := buildResticMountSnapshotCandidates(mountDir, mountOpt.PathTemplate, shortID, fullID, username, hostname, tagValue, timeValue, "configured", seenCandidates)
	fallbackTemplates := []string{"ids/%i", "ids/%I", "snapshots/%T", "hosts/%h/%T", "tags/%t/%T"}
	candidates = append(candidates, buildResticMountSnapshotCandidates(mountDir, fallbackTemplates, shortID, fullID, username, hostname, tagValue, timeValue, "fallback", seenCandidates)...)
	if len(candidates) == 0 {
		err := fmt.Errorf("no usable restic mount path templates available")
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Restic mount path resolution failed: %s", err)
		logResticMountTopLevel(cluster, mountDir)
		return err
	}

	candidate, err := waitForResticSnapshotPaths(ctx, candidates)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Restic mount snapshot paths not found before timeout; logging mount directory layout")
			logResticMountTopLevel(cluster, mountDir)
		}
		if len(mountOpt.Host) > 0 || len(mountOpt.Tag) > 0 || len(mountOpt.Path) > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Restic mount filters may hide the snapshot (host=%v tag=%v path=%v)",
				mountOpt.Host, mountOpt.Tag, mountOpt.Path)
		}
		if normalizedMethod == "physical" && refOwned {
			if relErr := cluster.ResticManager.ReleaseMountRef(userID); relErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Failed to release mount reference for userID %s: %s", userID, relErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlDbg,
					"Released mount reference for userID: %s", userID)
			}
			refOwned = false
		}
		return fmt.Errorf("snapshot path not ready: %w", err)
	}
	snapshotPath := candidate.Path
	if candidate.Source != "configured" {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Restic mount path template mismatch detected; using path %s (template=%s, id=%s, source=%s)",
			snapshotPath, candidate.Template, candidate.ID, candidate.Source)
	}

	if len(paths.SourcePaths) == 0 {
		return fmt.Errorf("no source paths available in snapshot %s", resticLogSnapshotID(cluster, shortID))
	}

	normalizeRelPath := func(label, value string, allowEmpty bool) (string, error) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			if allowEmpty {
				return "", nil
			}
			return "", fmt.Errorf("%s is empty", label)
		}
		if hasDotDotComponent(trimmed) {
			return "", fmt.Errorf("%s contains '..' component: %s", label, trimmed)
		}
		cleaned := filepath.Clean(trimmed)
		if filepath.IsAbs(cleaned) {
			cleaned = strings.TrimLeft(cleaned, string(filepath.Separator))
		}
		if cleaned == "." || cleaned == "" {
			if allowEmpty {
				return "", nil
			}
			return "", fmt.Errorf("%s resolves to empty path", label)
		}
		if filepath.IsAbs(cleaned) {
			return "", fmt.Errorf("%s resolves to absolute path %s", label, cleaned)
		}
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || hasDotDotComponent(cleaned) {
			return "", fmt.Errorf("%s resolves outside mount root: %s", label, cleaned)
		}
		return cleaned, nil
	}

	normalizedBase, err := normalizeRelPath("source base path", paths.SourceBasePath, true)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Invalid restic mount base path: %s", err)
		return err
	}
	paths.SourceBasePath = normalizedBase

	normalizedSources := make([]string, len(paths.SourcePaths))
	for i, sourcePath := range paths.SourcePaths {
		normalizedSource, err := normalizeRelPath(fmt.Sprintf("source path %d", i), sourcePath, false)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlErr,
				"Invalid restic mount source path: %s", err)
			return err
		}
		normalizedSources[i] = normalizedSource
	}
	paths.SourcePaths = normalizedSources

	joinParts := []string{snapshotPath}
	if paths.SourceBasePath != "" {
		joinParts = append(joinParts, paths.SourceBasePath)
	}
	joinParts = append(joinParts, paths.SourcePaths[0])
	resolvedTarget := filepath.Join(joinParts...)
	relTarget, relErr := filepath.Rel(snapshotPath, resolvedTarget)
	if relErr != nil || relTarget == "" || relTarget == "." || relTarget == ".." || strings.HasPrefix(relTarget, ".."+string(filepath.Separator)) {
		err := fmt.Errorf("resolved mount path %s escapes snapshot root %s", resolvedTarget, snapshotPath)
		if relErr != nil {
			err = fmt.Errorf("failed to resolve mount path %s under %s: %w", resolvedTarget, snapshotPath, relErr)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Invalid restic mount target: %s", err)
		return err
	}

	paths.TargetPaths = append(paths.TargetPaths, resolvedTarget)
	if err := verifyMountedSnapshotPaths(cluster, snapshotPath, paths.TargetPaths); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlErr,
			"Mounted snapshot path verification failed: %s",
			err)
		if normalizedMethod == "physical" && refOwned {
			if relErr := cluster.ResticManager.ReleaseMountRef(userID); relErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Failed to release mount reference for userID %s: %s", userID, relErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlDbg,
					"Released mount reference for userID: %s", userID)
			}
			refOwned = false
		}
		return fmt.Errorf("mounted snapshot path verification failed: %w", err)
	}

	if err := server.verifyRestoredBackup(paths); err != nil {
		if normalizedMethod == "physical" && refOwned {
			if relErr := cluster.ResticManager.ReleaseMountRef(userID); relErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlWarn,
					"Failed to release mount reference for userID %s: %s", userID, relErr)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlDbg,
					"Released mount reference for userID: %s", userID)
			}
			refOwned = false
		}
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
		logicalOpts := JobReseedLogicalOptions{SkipMetadata: true}
		if splitUser, ok := getSnapshotLogicalSplitUser(cluster, snapshotID); ok {
			logicalOpts.SplitUser = &splitUser
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Using restic metadata split-user=%t for snapshot %s",
				splitUser, resticLogSnapshotID(cluster, snapshotID))
		}
		return server.JobReseedLogicalBackupFromPathWithOptions(ctx, paths.BackupType, paths.TargetPaths[0], logicalOpts)
	case "physical":
		task := "reseed" + paths.BackupType
		if summary := getSnapshotMetadataForMethod(cluster, snapshotID, method, nil); summary != nil {
			payload := server.buildResticReseedPayload(summary, paths.SourceBasePath, "mount")
			server.registerResticReseedCleanup(task, paths, true, userID)
			if err := server.JobReseedPhysicalBackupWithPayload(paths.BackupType, paths.TargetPaths[0], payload); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlErr,
					"Failed to enqueue physical reseed from mount for snapshot %s: %s",
					resticLogSnapshotID(cluster, snapshotID), err)
				if cluster.ResticManager != nil {
					if relErr := cluster.ResticManager.ReleaseMountRef(userID); relErr != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose,
							config.ConstLogModRestic,
							config.LvlWarn,
							"Failed to release mount reference for userID %s: %s", userID, relErr)
					} else {
						cluster.LogModulePrintf(cluster.Conf.Verbose,
							config.ConstLogModRestic,
							config.LvlDbg,
							"Released mount reference for userID: %s", userID)
					}
					refOwned = false
				}
				server.cleanupResticReseedForTask(task, "mount reseed enqueue failure")
				return err
			}
			refOwned = false
			return nil
		}
		server.registerResticReseedCleanup(task, paths, true, userID)
		if err := server.JobReseedPhysicalBackupFromPath(paths.BackupType, paths.TargetPaths[0]); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlErr,
				"Failed to enqueue physical reseed from mount for snapshot %s: %s",
				resticLogSnapshotID(cluster, snapshotID), err)
			if cluster.ResticManager != nil {
				if relErr := cluster.ResticManager.ReleaseMountRef(userID); relErr != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlWarn,
						"Failed to release mount reference for userID %s: %s", userID, relErr)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose,
						config.ConstLogModRestic,
						config.LvlDbg,
						"Released mount reference for userID: %s", userID)
				}
				refOwned = false
			}
			server.cleanupResticReseedForTask(task, "mount reseed enqueue failure")
			return err
		}
		refOwned = false
		return nil
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

type resticSnapshotPathCandidate struct {
	Path     string
	Template string
	ID       string
	Source   string
}

func logResticMountTopLevel(cluster *Cluster, mountDir string) {
	if cluster == nil {
		return
	}
	trimmed := strings.TrimSpace(mountDir)
	if trimmed == "" {
		return
	}
	entries, err := os.ReadDir(trimmed)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlWarn,
			"Failed to read restic mount dir %s: %s", trimmed, err)
		return
	}
	if len(entries) == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic mount dir %s is empty", trimmed)
		return
	}
	const maxTopEntries = 50
	const maxChildEntries = 20
	cluster.LogModulePrintf(cluster.Conf.Verbose,
		config.ConstLogModRestic,
		config.LvlInfo,
		"Restic mount dir %s top-level entries: %d", trimmed, len(entries))
	for i, entry := range entries {
		if i >= maxTopEntries {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Restic mount dir %s listing truncated at %d entries", trimmed, maxTopEntries)
			break
		}
		name := entry.Name()
		if !entry.IsDir() {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Restic mount entry: %s", name)
			continue
		}
		childPath := filepath.Join(trimmed, name)
		children, childErr := os.ReadDir(childPath)
		if childErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Failed to read restic mount entry %s: %s", childPath, childErr)
			continue
		}
		childNames := make([]string, 0, len(children))
		for j, child := range children {
			if j >= maxChildEntries {
				remaining := len(children) - maxChildEntries
				if remaining > 0 {
					childNames = append(childNames, fmt.Sprintf("...%d more", remaining))
				}
				break
			}
			childNames = append(childNames, child.Name())
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Restic mount entry %s/ children=%d [%s]", name, len(children), strings.Join(childNames, ", "))
	}
}

func buildResticMountSnapshotCandidates(mountDir string, templates []string, shortID, fullID, username, hostname, tagValue, timeValue, source string, seen map[string]struct{}) []resticSnapshotPathCandidate {
	if mountDir == "" {
		return nil
	}
	candidates := make([]resticSnapshotPathCandidate, 0, len(templates))
	for _, template := range templates {
		trimmed := strings.TrimSpace(template)
		if trimmed == "" {
			continue
		}
		if relPath, idUsed, ok := expandResticMountTemplate(trimmed, shortID, fullID, username, hostname, tagValue, timeValue); ok {
			path := filepath.Join(mountDir, relPath)
			if seen != nil {
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					candidates = append(candidates, resticSnapshotPathCandidate{
						Path:     path,
						Template: trimmed,
						ID:       idUsed,
						Source:   source,
					})
				}
			} else {
				candidates = append(candidates, resticSnapshotPathCandidate{
					Path:     path,
					Template: trimmed,
					ID:       idUsed,
					Source:   source,
				})
			}
		}
	}
	return candidates
}

func expandResticMountTemplate(template, shortID, fullID, username, hostname, tagValue, timeValue string) (string, string, bool) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", "", false
	}
	hasShort := strings.Contains(trimmed, "%i")
	hasFull := strings.Contains(trimmed, "%I")
	hasUser := strings.Contains(trimmed, "%u")
	hasHost := strings.Contains(trimmed, "%h")
	hasTags := strings.Contains(trimmed, "%t")
	hasTime := strings.Contains(trimmed, "%T")
	if !hasShort && !hasFull && !hasUser && !hasHost && !hasTags && !hasTime {
		if strings.Contains(trimmed, "%") {
			return "", "", false
		}
	}
	if hasShort && strings.TrimSpace(shortID) == "" {
		return "", "", false
	}
	if hasFull && strings.TrimSpace(fullID) == "" {
		return "", "", false
	}
	if hasUser && strings.TrimSpace(username) == "" {
		return "", "", false
	}
	if hasHost && strings.TrimSpace(hostname) == "" {
		return "", "", false
	}
	if hasTags && strings.TrimSpace(tagValue) == "" {
		return "", "", false
	}
	if hasTime && strings.TrimSpace(timeValue) == "" {
		return "", "", false
	}
	replaced := strings.ReplaceAll(trimmed, "%i", shortID)
	replaced = strings.ReplaceAll(replaced, "%I", fullID)
	replaced = strings.ReplaceAll(replaced, "%u", username)
	replaced = strings.ReplaceAll(replaced, "%h", hostname)
	replaced = strings.ReplaceAll(replaced, "%t", tagValue)
	replaced = strings.ReplaceAll(replaced, "%T", timeValue)
	if strings.Contains(replaced, "%") {
		return "", "", false
	}
	cleaned := filepath.Clean(replaced)
	if filepath.IsAbs(cleaned) {
		return "", "", false
	}
	if cleaned == "." || cleaned == "" || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", false
	}
	idUsed := ""
	if hasShort && hasFull {
		idUsed = "mixed"
	} else if hasShort {
		idUsed = shortID
	} else if hasFull {
		idUsed = fullID
	}
	return cleaned, idUsed, true
}

func waitForResticSnapshotPaths(ctx context.Context, candidates []resticSnapshotPathCandidate) (resticSnapshotPathCandidate, error) {
	if len(candidates) == 0 {
		return resticSnapshotPathCandidate{}, fmt.Errorf("no snapshot path candidates")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkPaths := func() (resticSnapshotPathCandidate, bool) {
		for _, candidate := range candidates {
			if candidate.Path == "" {
				continue
			}
			if _, err := os.Stat(candidate.Path); err == nil {
				return candidate, true
			}
		}
		return resticSnapshotPathCandidate{}, false
	}
	if candidate, ok := checkPaths(); ok {
		return candidate, nil
	}
	if ctx.Err() != nil {
		return resticSnapshotPathCandidate{}, ctx.Err()
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return resticSnapshotPathCandidate{}, ctx.Err()
		case <-ticker.C:
			if candidate, ok := checkPaths(); ok {
				return candidate, nil
			}
		}
	}
}
