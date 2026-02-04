// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 Cloud SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/sirupsen/logrus"
)

const (
	snapshotMetadataExtractorConcurrency    = 2 // Default; configurable via backup-restic-metadata-extractor-concurrency.
	snapshotMetadataExtractionRetryInterval = 5 * time.Minute
	snapshotMetadataFileExtension           = ".json"
)

var snapshotMetadataIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

type snapshotMetadataStatus int

const (
	snapshotMetadataStatusUnknown snapshotMetadataStatus = iota
	snapshotMetadataStatusPending
	snapshotMetadataStatusReady
	snapshotMetadataStatusFailed
)

type snapshotMetadataCache struct {
	mu      sync.RWMutex
	entries map[string]*snapshotMetadataCacheEntry
}

type snapshotMetadataCacheEntry struct {
	Summaries   map[string]*SnapshotMetadataSummary
	Status      snapshotMetadataStatus
	LastAttempt time.Time
	LastError   string
}

type snapshotMetadataDiskEntry struct {
	Summaries   map[string]*SnapshotMetadataSummary `json:"summaries"`
	Status      snapshotMetadataStatus              `json:"status"`
	LastAttempt time.Time                           `json:"lastAttempt"`
	LastError   string                              `json:"lastError"`
}

func newSnapshotMetadataCache() *snapshotMetadataCache {
	return &snapshotMetadataCache{entries: make(map[string]*snapshotMetadataCacheEntry)}
}

func cloneSnapshotMetadataMap(src map[string]*SnapshotMetadataSummary) map[string]*SnapshotMetadataSummary {
	if len(src) == 0 {
		return make(map[string]*SnapshotMetadataSummary)
	}
	dst := make(map[string]*SnapshotMetadataSummary, len(src))
	for key, summary := range src {
		if summary == nil {
			continue
		}
		copySummary := *summary
		dst[key] = &copySummary
	}
	return dst
}

func snapshotMetadataKey(method backupmgr.BackupMethod, line string) string {
	return fmt.Sprintf("%d|%s", method, strings.ToLower(strings.TrimSpace(line)))
}

func (cluster *Cluster) snapshotMetadataCacheForID(snapshotID string) (*snapshotMetadataCache, string, bool) {
	if cluster == nil {
		return nil, "", false
	}
	manager := cluster.getSnapshotMetadataManager()
	if manager == nil || manager.cache == nil {
		return nil, "", false
	}
	id := strings.TrimSpace(snapshotID)
	if id == "" {
		return nil, "", false
	}
	return manager.cache, id, true
}

func (cluster *Cluster) dropSnapshotMetadataCaches(snapshotID string) {
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil {
		return
	}
	if manager.cache != nil {
		manager.cache.Delete(snapshotID)
	}
	cluster.clearResticSnapshotLsCache(snapshotID)
}

func (c *snapshotMetadataCache) Get(snapshotID string) (*snapshotMetadataCacheEntry, bool) {
	if c == nil || snapshotID == "" {
		return nil, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[snapshotID]
	if !ok || entry == nil {
		return nil, false
	}
	return entry.clone(), true
}

func (c *snapshotMetadataCache) GetSummaries(snapshotID string) map[string]*SnapshotMetadataSummary {
	if c == nil || snapshotID == "" {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[snapshotID]
	if !ok || entry == nil || len(entry.Summaries) == 0 {
		return nil
	}
	return cloneSnapshotMetadataMap(entry.Summaries)
}

func (c *snapshotMetadataCache) Update(snapshotID string, fn func(entry *snapshotMetadataCacheEntry)) *snapshotMetadataCacheEntry {
	if c == nil || snapshotID == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[snapshotID]
	if !ok || entry == nil {
		entry = &snapshotMetadataCacheEntry{
			Summaries: make(map[string]*SnapshotMetadataSummary),
			Status:    snapshotMetadataStatusUnknown,
		}
		c.entries[snapshotID] = entry
	}
	if fn != nil {
		fn(entry)
	}
	return entry.clone()
}

func (c *snapshotMetadataCache) Delete(snapshotID string) {
	if c == nil || snapshotID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, snapshotID)
}

func (c *snapshotMetadataCache) SnapshotIDs() []string {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.entries))
	for id := range c.entries {
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func (entry *snapshotMetadataCacheEntry) clone() *snapshotMetadataCacheEntry {
	if entry == nil {
		return nil
	}
	clone := &snapshotMetadataCacheEntry{
		Status:      entry.Status,
		LastAttempt: entry.LastAttempt,
		LastError:   entry.LastError,
	}
	clone.Summaries = cloneSnapshotMetadataMap(entry.Summaries)
	return clone
}

func (cluster *Cluster) initSnapshotMetadataPersistence() {
	if cluster == nil {
		return
	}
	manager := cluster.getSnapshotMetadataManager()
	if manager == nil {
		return
	}
	if manager.resticSnapshotLsCache == nil {
		manager.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	manager.resticMetadataDir = filepath.Join(cluster.WorkingDir, "backup", "restic_metadata")
	if manager.resticMetadataDir == "" {
		return
	}
	if err := os.MkdirAll(manager.resticMetadataDir, os.ModePerm); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to prepare restic metadata directory %s: %v", manager.resticMetadataDir, err)
		return
	}
	if manager.cache == nil {
		manager.cache = newSnapshotMetadataCache()
	}
	entries, err := cluster.loadSnapshotMetadataEntriesFromDisk()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to load snapshot metadata cache from disk: %v", err)
		return
	}
	if len(entries) == 0 {
		return
	}
	for snapshotID, persisted := range entries {
		if persisted == nil {
			continue
		}
		manager.cache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
			entry.Status = persisted.Status
			entry.LastAttempt = persisted.LastAttempt
			entry.LastError = persisted.LastError
			entry.Summaries = cloneSnapshotMetadataMap(persisted.Summaries)
		})
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlDbg, "Loaded %d restic snapshot metadata entries from disk", len(entries))
}

func sanitizeSnapshotMetadataID(snapshotID string) (string, error) {
	id := strings.TrimSpace(snapshotID)
	if id == "" {
		return "", fmt.Errorf("empty snapshot id")
	}
	if !snapshotMetadataIDPattern.MatchString(id) {
		return "", fmt.Errorf("snapshot id %q contains invalid characters", snapshotID)
	}
	return id, nil
}

func (cluster *Cluster) snapshotMetadataFilePath(snapshotID string) (string, error) {
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil || manager.resticMetadataDir == "" {
		return "", fmt.Errorf("restic metadata directory is not initialized")
	}
	sanitized, err := sanitizeSnapshotMetadataID(snapshotID)
	if err != nil {
		return "", err
	}
	return filepath.Join(manager.resticMetadataDir, sanitized+snapshotMetadataFileExtension), nil
}

func (cluster *Cluster) persistSnapshotMetadataEntry(snapshotID string, entry *snapshotMetadataCacheEntry) error {
	if cluster == nil {
		return fmt.Errorf("cluster is not initialized")
	}
	if entry == nil {
		return cluster.deleteSnapshotMetadataEntry(snapshotID)
	}
	path, err := cluster.snapshotMetadataFilePath(snapshotID)
	if err != nil {
		return err
	}
	diskEntry := snapshotMetadataDiskEntry{
		Summaries:   cloneSnapshotMetadataMap(entry.Summaries),
		Status:      entry.Status,
		LastAttempt: entry.LastAttempt,
		LastError:   entry.LastError,
	}
	data, err := json.MarshalIndent(diskEntry, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(filepath.Dir(path), fmt.Sprintf(".%s.tmp", filepath.Base(path)))
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func (cluster *Cluster) loadSnapshotMetadataEntriesFromDisk() (map[string]*snapshotMetadataCacheEntry, error) {
	results := make(map[string]*snapshotMetadataCacheEntry)
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil || manager.resticMetadataDir == "" {
		return results, nil
	}
	pattern := filepath.Join(manager.resticMetadataDir, "*"+snapshotMetadataFileExtension)
	files, err := filepath.Glob(pattern)
	if err != nil {
		return nil, err
	}
	for _, filePath := range files {
		base := filepath.Base(filePath)
		if !strings.HasSuffix(base, snapshotMetadataFileExtension) {
			continue
		}
		snapshotID := strings.TrimSuffix(base, snapshotMetadataFileExtension)
		if snapshotID == "" {
			continue
		}
		entry, err := cluster.loadSnapshotMetadataFromDisk(snapshotID)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to load snapshot metadata %s: %v", base, err)
			continue
		}
		results[snapshotID] = entry
	}
	return results, nil
}

func (cluster *Cluster) loadSnapshotMetadataFromDisk(snapshotID string) (*snapshotMetadataCacheEntry, error) {
	path, err := cluster.snapshotMetadataFilePath(snapshotID)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var diskEntry snapshotMetadataDiskEntry
	if err := json.Unmarshal(data, &diskEntry); err != nil {
		return nil, err
	}
	entry := &snapshotMetadataCacheEntry{
		Status:      diskEntry.Status,
		LastAttempt: diskEntry.LastAttempt,
		LastError:   diskEntry.LastError,
	}
	entry.Summaries = cloneSnapshotMetadataMap(diskEntry.Summaries)
	return entry, nil
}

func (cluster *Cluster) deleteSnapshotMetadataEntry(snapshotID string) error {
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil || manager.resticMetadataDir == "" {
		return nil
	}
	path, err := cluster.snapshotMetadataFilePath(snapshotID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (cluster *Cluster) logSnapshotMetadataPersistenceError(snapshot *backupmgr.BackupSnapshot, phase string, err error) {
	if cluster == nil || err == nil {
		return
	}
	label := "unknown"
	if snapshot != nil {
		label = snapshot.ShortId
		if label == "" {
			label = snapshot.Id
		}
		if label == "" {
			label = "unknown"
		}
	}
	context := ""
	if phase != "" {
		context = fmt.Sprintf(" (%s)", phase)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to persist snapshot metadata%s for snapshot %s: %v", context, label, err)
}

func (cluster *Cluster) getResticSnapshotLs(snapshotID string) (map[string]bool, error) {
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil || cluster.ResticManager == nil {
		return nil, fmt.Errorf("restic manager not initialized")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshot id is empty")
	}
	manager.resticSnapshotLsCacheMu.Lock()
	if manager.resticSnapshotLsCache == nil {
		manager.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	if cached, ok := manager.resticSnapshotLsCache[snapshotID]; ok {
		manager.resticSnapshotLsCacheMu.Unlock()
		return cached, nil
	}
	manager.resticSnapshotLsCacheMu.Unlock()

	entries, err := cluster.ResticManager.ListSnapshotWithLogLevel(snapshotID, nil, true, logrus.DebugLevel)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool)
	for _, entry := range entries {
		if strings.TrimSpace(entry.Path) == "" {
			continue
		}
		if strings.EqualFold(entry.Type, "file") {
			if !isSnapshotMetadataCandidatePath(entry.Path, nil) {
				continue
			}
			paths[entry.Path] = true
		}
	}
	manager.resticSnapshotLsCacheMu.Lock()
	if manager.resticSnapshotLsCache == nil {
		manager.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	manager.resticSnapshotLsCache[snapshotID] = paths
	manager.resticSnapshotLsCacheMu.Unlock()
	return paths, nil
}

func isSnapshotMetadataCandidatePath(path string, allowedTools map[string]bool) bool {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(trimmed))
	for _, candidate := range snapshotMetadataCandidates {
		if len(allowedTools) > 0 {
			if _, ok := allowedTools[strings.ToLower(candidate.Tool)]; !ok {
				continue
			}
		}
		if base == strings.ToLower(candidate.File) {
			return true
		}
	}
	return false
}

func (cluster *Cluster) clearResticSnapshotLsCache(snapshotID string) {
	manager := cluster.getSnapshotMetadataManager()
	if cluster == nil || manager == nil || strings.TrimSpace(snapshotID) == "" {
		return
	}
	manager.resticSnapshotLsCacheMu.Lock()
	defer manager.resticSnapshotLsCacheMu.Unlock()
	if manager.resticSnapshotLsCache == nil {
		return
	}
	delete(manager.resticSnapshotLsCache, snapshotID)
}

func (cluster *Cluster) handleResticPurgeComplete(opt backupmgr.ResticPurgeOption) {
	if cluster == nil {
		return
	}
	if opt.DryRun {
		return
	}
	if strings.TrimSpace(opt.SnapshotID) != "" {
		snapshotID := strings.TrimSpace(opt.SnapshotID)
		cluster.dropSnapshotMetadataCaches(snapshotID)
		if err := cluster.deleteSnapshotMetadataEntry(snapshotID); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to delete persisted metadata for snapshot %s: %v", snapshotID, err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Removed persisted metadata for pruned snapshot %s", snapshotID)
		}
		return
	}
	if _, err := cluster.ReconcileSnapshotMetadata(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to reconcile snapshot metadata after purge: %v", err)
	}
}

func getSnapshotSessionIDFromMetadata(cluster *Cluster, snapshot *backupmgr.BackupSnapshot) string {
	if cluster == nil || snapshot == nil {
		return ""
	}
	if summaries := cluster.getSnapshotMetadataSummaries(snapshot.Id); len(summaries) > 0 {
		for _, summary := range summaries {
			if summary != nil && strings.TrimSpace(summary.BackupSessionID) != "" {
				return strings.TrimSpace(summary.BackupSessionID)
			}
		}
	}
	summaries := cluster.SummarizeSnapshotMetadata(snapshot)
	for _, summary := range summaries {
		if summary != nil && strings.TrimSpace(summary.BackupSessionID) != "" {
			return strings.TrimSpace(summary.BackupSessionID)
		}
	}
	return ""
}

// FilterMostRecentSnapshotsPerSession returns only the newest snapshot per backup session.
// Snapshots without session IDs are treated as individual sessions (no grouping).
func FilterMostRecentSnapshotsPerSession(cluster *Cluster, snapshots []backupmgr.BackupSnapshot) []backupmgr.BackupSnapshot {
	return FilterMostRecentSnapshotsPerSessionWithIndex(cluster, snapshots, nil)
}

func FilterMostRecentSnapshotsPerSessionWithIndex(cluster *Cluster, snapshots []backupmgr.BackupSnapshot, index SnapshotMetadataIndex) []backupmgr.BackupSnapshot {
	if len(snapshots) == 0 {
		return snapshots
	}

	sessionMap := make(map[string]*backupmgr.BackupSnapshot)

	for i := range snapshots {
		snapshot := &snapshots[i]
		var sessionID string
		if len(index) > 0 {
			sessionID = getSnapshotSessionIDFromIndex(index, snapshot.Id)
		} else {
			sessionID = getSnapshotSessionIDFromMetadata(cluster, snapshot)
		}

		if sessionID == "" {
			// Snapshot without metadata session: Use snapshot ID as unique key (no grouping)
			sessionMap[snapshot.Id] = snapshot
			continue
		}

		// Modern snapshot: Group by session, keep most recent
		existing, found := sessionMap[sessionID]
		if !found {
			sessionMap[sessionID] = snapshot
			continue
		}

		// Compare timestamps - keep newer snapshot
		snapshotTime, err1 := time.Parse(time.RFC3339, snapshot.Time)
		existingTime, err2 := time.Parse(time.RFC3339, existing.Time)

		if err1 == nil && err2 == nil {
			if snapshotTime.After(existingTime) {
				sessionMap[sessionID] = snapshot
			}
		} else {
			// Fallback to string comparison if parsing fails
			if snapshot.Time > existing.Time {
				sessionMap[sessionID] = snapshot
			}
		}
	}

	// Convert map to slice
	result := make([]backupmgr.BackupSnapshot, 0, len(sessionMap))
	for _, snapshot := range sessionMap {
		result = append(result, *snapshot)
	}

	// Sort by time descending (newest first)
	sort.Slice(result, func(i, j int) bool {
		timeI, errI := time.Parse(time.RFC3339, result[i].Time)
		timeJ, errJ := time.Parse(time.RFC3339, result[j].Time)
		if errI == nil && errJ == nil {
			return timeI.After(timeJ)
		}
		// Fallback to string comparison
		return result[i].Time > result[j].Time
	})

	return result
}

// ReconciliationReport tracks drift between metadata and restic snapshots
type ReconciliationReport struct {
	OrphanedMetadata []string  `json:"orphanedMetadata"` // Metadata files referencing deleted snapshots
	MissingMetadata  []string  `json:"missingMetadata"`  // Snapshots without metadata files
	Timestamp        time.Time `json:"timestamp"`
	CleanedUp        bool      `json:"cleanedUp"`
}

// SnapshotMetadataSummary captures lightweight backup metadata associated with a restic snapshot.
type SnapshotMetadataSummary struct {
	Dest             string    `json:"dest,omitempty"`
	BackupMethod     string    `json:"backupMethod"`
	BackupTool       string    `json:"backupTool"`
	BackupLine       string    `json:"backupLine"`
	StartTime        time.Time `json:"startTime"`
	EndTime          time.Time `json:"endTime"`
	Compressed       bool      `json:"compressed,omitempty"`
	BackupSessionID  string    `json:"backupSessionID,omitempty"`
	ResticSnapshotID string    `json:"resticSnapshotID,omitempty"`
	ResticBasePath   string    `json:"resticBasePath,omitempty"`
}

type snapshotMetadataCandidate struct {
	File   string
	Method backupmgr.BackupMethod
	Tool   string
}

var snapshotMetadataCandidates = []snapshotMetadataCandidate{
	{File: "mysqldump.meta.json", Method: backupmgr.BackupMethodLogical, Tool: config.ConstBackupLogicalTypeMysqldump},
	{File: "mydumper.meta.json", Method: backupmgr.BackupMethodLogical, Tool: config.ConstBackupLogicalTypeMydumper},
	{File: "dumpling.meta.json", Method: backupmgr.BackupMethodLogical, Tool: config.ConstBackupLogicalTypeDumpling},
	{File: "mysqlpump.meta.json", Method: backupmgr.BackupMethodLogical, Tool: "mysqlpump"},
	{File: "mariabackup.meta.json", Method: backupmgr.BackupMethodPhysical, Tool: config.ConstBackupPhysicalTypeMariaBackup},
	{File: "xtrabackup.meta.json", Method: backupmgr.BackupMethodPhysical, Tool: config.ConstBackupPhysicalTypeXtrabackup},
}

func backupMethodToString(method backupmgr.BackupMethod) string {
	switch method {
	case backupmgr.BackupMethodPhysical:
		return "physical"
	case backupmgr.BackupMethodLogical:
		return "logical"
	default:
		return "unknown"
	}
}

func buildSnapshotMetadataSummary(meta *backupmgr.BackupMetadata, method backupmgr.BackupMethod, basepath string) *SnapshotMetadataSummary {
	if meta == nil {
		return nil
	}
	compressed := meta.Compressed
	if !compressed && isCompressedDest(meta.Dest) {
		compressed = true
	}
	return &SnapshotMetadataSummary{
		Dest:             strings.TrimSpace(meta.Dest),
		BackupMethod:     backupMethodToString(method),
		BackupTool:       meta.BackupTool,
		BackupLine:       meta.BackupLine,
		StartTime:        meta.StartTime,
		EndTime:          meta.EndTime,
		Compressed:       compressed,
		BackupSessionID:  meta.BackupSessionID,
		ResticSnapshotID: meta.ResticSnapshotID,
		ResticBasePath:   strings.TrimSpace(basepath),
	}
}

func isCompressedDest(dest string) bool {
	lower := strings.ToLower(strings.TrimSpace(dest))
	if lower == "" {
		return false
	}
	suffixes := []string{".gz", ".tgz", ".zst", ".xz", ".bz2", ".lz4", ".zip"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	return false
}

// SummarizeSnapshotMetadata returns lightweight metadata associated with the given snapshot paths.
type metadataSelection struct {
	exact    *SnapshotMetadataSummary
	fallback *SnapshotMetadataSummary
}

type SnapshotMetadataIndex map[string][]*SnapshotMetadataSummary

func isBetterFallback(candidate, current *SnapshotMetadataSummary, snapshotTime time.Time) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if snapshotTime.IsZero() {
		return candidate.StartTime.After(current.StartTime)
	}
	candidateDiff := candidate.StartTime.Sub(snapshotTime)
	currentDiff := current.StartTime.Sub(snapshotTime)
	if candidateDiff < 0 {
		candidateDiff = -candidateDiff
	}
	if currentDiff < 0 {
		currentDiff = -currentDiff
	}
	if candidateDiff == currentDiff {
		return candidate.StartTime.After(current.StartTime)
	}
	return candidateDiff < currentDiff
}

func (cluster *Cluster) SummarizeSnapshotMetadata(snapshot *backupmgr.BackupSnapshot) []*SnapshotMetadataSummary {
	if cluster == nil || snapshot == nil || len(snapshot.Paths) == 0 {
		return nil
	}
	// Deduplicate summaries by backup method + line. Prefer metadata rows referencing this snapshot ID, otherwise use the closest fallback.
	summaryMap := make(map[string]*metadataSelection)
	snapshotTime, _ := time.Parse(time.RFC3339Nano, snapshot.Time)
	cluster.BackupMetaMap.Range(func(_, value any) bool {
		meta, ok := value.(*backupmgr.BackupMetadata)
		if !ok || meta == nil {
			return true
		}
		dest := strings.TrimSpace(meta.Dest)
		if dest == "" {
			return true
		}
		resticID := strings.TrimSpace(meta.ResticSnapshotID)
		for _, base := range snapshot.Paths {
			if base == "" {
				continue
			}
			if strings.HasPrefix(dest, base) {
				method := inferBackupMethod(meta)
				key := snapshotMetadataKey(method, meta.BackupLine)
				summary := buildSnapshotMetadataSummary(meta, method, base)
				if summary == nil {
					break
				}
				selection := summaryMap[key]
				if selection == nil {
					selection = &metadataSelection{}
					summaryMap[key] = selection
				}
				if resticID != "" && resticID == snapshot.Id {
					if selection.exact == nil || summary.StartTime.After(selection.exact.StartTime) {
						selection.exact = summary
					}
				} else if isBetterFallback(summary, selection.fallback, snapshotTime) {
					selection.fallback = summary
				}
				break
			}
		}
		return true
	})
	if cacheSummaries := cluster.getSnapshotMetadataSummaries(snapshot.Id); len(cacheSummaries) > 0 {
		for key, summary := range cacheSummaries {
			if summary == nil {
				continue
			}
			selection := summaryMap[key]
			if selection == nil {
				selection = &metadataSelection{}
				summaryMap[key] = selection
			}
			if summary.ResticSnapshotID == snapshot.Id {
				if selection.exact == nil || summary.StartTime.After(selection.exact.StartTime) {
					selection.exact = summary
				}
			} else if isBetterFallback(summary, selection.fallback, snapshotTime) {
				selection.fallback = summary
			}
		}
	}
	if len(summaryMap) == 0 {
		cluster.scheduleSnapshotMetadataExtraction(snapshot)
		return nil
	}
	summaries := make([]*SnapshotMetadataSummary, 0, len(summaryMap))
	exactFound := false
	for _, selection := range summaryMap {
		if selection == nil {
			continue
		}
		if selection.exact != nil {
			summaries = append(summaries, selection.exact)
			exactFound = true
		} else if selection.fallback != nil {
			summaries = append(summaries, selection.fallback)
		}
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].StartTime.After(summaries[j].StartTime)
	})
	if !exactFound {
		cluster.scheduleSnapshotMetadataExtraction(snapshot)
	}
	return summaries
}

func (cluster *Cluster) BuildSnapshotMetadataIndex(snapshots []backupmgr.BackupSnapshot) SnapshotMetadataIndex {
	index := make(SnapshotMetadataIndex)
	if cluster == nil || len(snapshots) == 0 {
		return index
	}
	for i := range snapshots {
		snapshot := &snapshots[i]
		if snapshot == nil || snapshot.Id == "" {
			continue
		}
		if summaries := cluster.SummarizeSnapshotMetadata(snapshot); len(summaries) > 0 {
			index[snapshot.Id] = summaries
		}
	}
	return index
}

func getSnapshotSessionIDFromIndex(index SnapshotMetadataIndex, snapshotID string) string {
	if len(index) == 0 || strings.TrimSpace(snapshotID) == "" {
		return ""
	}
	for _, summary := range index[snapshotID] {
		if summary != nil && strings.TrimSpace(summary.BackupSessionID) != "" {
			return strings.TrimSpace(summary.BackupSessionID)
		}
	}
	return ""
}

func (cluster *Cluster) getSnapshotMetadataCacheEntry(snapshotID string) (*snapshotMetadataCacheEntry, bool) {
	cache, id, ok := cluster.snapshotMetadataCacheForID(snapshotID)
	if !ok {
		return nil, false
	}
	return cache.Get(id)
}

func (cluster *Cluster) getSnapshotMetadataSummaries(snapshotID string) map[string]*SnapshotMetadataSummary {
	cache, id, ok := cluster.snapshotMetadataCacheForID(snapshotID)
	if !ok {
		return nil
	}
	return cache.GetSummaries(id)
}

func (cluster *Cluster) GetSnapshotMetadataStatus(snapshotID string) snapshotMetadataStatus {
	if entry, ok := cluster.getSnapshotMetadataCacheEntry(snapshotID); ok && entry != nil {
		return entry.Status
	}
	return snapshotMetadataStatusUnknown
}

func (cluster *Cluster) GetSnapshotMetadataStatusString(snapshotID string) string {
	switch cluster.GetSnapshotMetadataStatus(snapshotID) {
	case snapshotMetadataStatusReady:
		return "ready"
	case snapshotMetadataStatusPending:
		return "pending"
	case snapshotMetadataStatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func (cluster *Cluster) GetSnapshotMetadataError(snapshotID string) string {
	if entry, ok := cluster.getSnapshotMetadataCacheEntry(snapshotID); ok && entry != nil {
		return entry.LastError
	}
	return ""
}

func (cluster *Cluster) IsSnapshotMetadataReady(snapshotID string) bool {
	return cluster.GetSnapshotMetadataStatus(snapshotID) == snapshotMetadataStatusReady
}

func (cluster *Cluster) RequireSnapshotMetadataReady(snapshotID string) error {
	switch cluster.GetSnapshotMetadataStatus(snapshotID) {
	case snapshotMetadataStatusReady:
		return nil
	case snapshotMetadataStatusPending:
		return fmt.Errorf("metadata extraction in progress for snapshot %s", snapshotID)
	case snapshotMetadataStatusFailed:
		return fmt.Errorf("metadata extraction failed for snapshot %s: %s", snapshotID, cluster.GetSnapshotMetadataError(snapshotID))
	default:
		return fmt.Errorf("metadata not available for snapshot %s", snapshotID)
	}
}

func (cluster *Cluster) scheduleSnapshotMetadataExtraction(snapshot *backupmgr.BackupSnapshot) {
	if cluster == nil || snapshot == nil || snapshot.Id == "" || len(snapshot.Paths) == 0 {
		return
	}
	manager := cluster.getSnapshotMetadataManager()
	if !cluster.Conf.BackupRestic || cluster.ResticManager == nil || manager == nil || manager.cache == nil {
		return
	}
	updatedEntry, shouldStart := cluster.markSnapshotMetadataPending(snapshot.Id)
	if !shouldStart {
		return
	}
	if updatedEntry != nil {
		if err := cluster.persistSnapshotMetadataEntry(snapshot.Id, updatedEntry); err != nil {
			cluster.logSnapshotMetadataPersistenceError(snapshot, "pending", err)
		}
	}
	go cluster.runSnapshotMetadataExtraction(snapshot)
}

func (cluster *Cluster) markSnapshotMetadataPending(snapshotID string) (*snapshotMetadataCacheEntry, bool) {
	cache, id, ok := cluster.snapshotMetadataCacheForID(snapshotID)
	if !ok {
		return nil, false
	}
	now := time.Now()
	shouldStart := false
	entry := cache.Update(id, func(entry *snapshotMetadataCacheEntry) {
		switch entry.Status {
		case snapshotMetadataStatusPending, snapshotMetadataStatusReady:
			return
		case snapshotMetadataStatusFailed:
			if now.Sub(entry.LastAttempt) < snapshotMetadataExtractionRetryInterval {
				return
			}
		}
		entry.Status = snapshotMetadataStatusPending
		entry.LastAttempt = now
		entry.LastError = ""
		shouldStart = true
	})
	return entry, shouldStart
}

func (cluster *Cluster) runSnapshotMetadataExtraction(snapshot *backupmgr.BackupSnapshot) {
	if cluster == nil || snapshot == nil || snapshot.Id == "" {
		return
	}
	var sem chan struct{}
	manager := cluster.getSnapshotMetadataManager()
	if manager == nil {
		return
	}
	manager.extractorSemMu.Lock()
	sem = manager.extractorSem
	manager.extractorSemMu.Unlock()
	if sem != nil {
		sem <- struct{}{}
		defer func() {
			<-sem
			cluster.tryApplySnapshotMetadataExtractorConcurrency()
		}()
	}
	summaries, err := cluster.extractMetadataFromResticSnapshot(snapshot)
	now := time.Now()
	if err != nil {
		updatedEntry := manager.cache.Update(snapshot.Id, func(entry *snapshotMetadataCacheEntry) {
			entry.Status = snapshotMetadataStatusFailed
			entry.LastError = err.Error()
			entry.LastAttempt = now
		})
		if updatedEntry != nil {
			if persistErr := cluster.persistSnapshotMetadataEntry(snapshot.Id, updatedEntry); persistErr != nil {
				cluster.logSnapshotMetadataPersistenceError(snapshot, "failed", persistErr)
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to extract metadata for snapshot %s: %v", snapshot.ShortId, err)
		return
	}
	updatedEntry := manager.cache.Update(snapshot.Id, func(entry *snapshotMetadataCacheEntry) {
		entry.Status = snapshotMetadataStatusReady
		entry.LastError = ""
		entry.LastAttempt = now
		entry.Summaries = cloneSnapshotMetadataMap(summaries)
	})
	if updatedEntry != nil {
		if err := cluster.persistSnapshotMetadataEntry(snapshot.Id, updatedEntry); err != nil {
			cluster.logSnapshotMetadataPersistenceError(snapshot, "ready", err)
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlDbg, "Extracted metadata for snapshot %s", snapshot.ShortId)
}

func (cluster *Cluster) extractMetadataFromResticSnapshot(snapshot *backupmgr.BackupSnapshot) (map[string]*SnapshotMetadataSummary, error) {
	if cluster == nil || snapshot == nil || snapshot.Id == "" {
		return nil, fmt.Errorf("invalid snapshot reference")
	}
	if cluster.ResticManager == nil {
		return nil, fmt.Errorf("restic manager not initialized")
	}
	results := make(map[string]*SnapshotMetadataSummary)
	destExistsCache := make(map[string]bool)
	var lastErr error
	lsCache, lsErr := cluster.getResticSnapshotLs(snapshot.Id)
	if lsErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to list snapshot %s before metadata extraction: %v", snapshot.ShortId, lsErr)
		lsCache = nil
	}
	var dumpAttempted int
	var dumpSucceeded int
	var dumpSkipped int
	allowedTools := snapshotAllowedTools(snapshot)
	for _, base := range snapshot.Paths {
		base = strings.TrimSpace(base)
		if base == "" {
			continue
		}
		for _, candidate := range snapshotMetadataCandidates {
			if len(allowedTools) > 0 {
				if _, ok := allowedTools[strings.ToLower(candidate.Tool)]; !ok {
					continue
				}
			}
			filePath := filepath.Join(base, candidate.File)
			if lsCache != nil {
				if _, ok := lsCache[filePath]; !ok {
					dumpSkipped++
					continue
				}
			}
			dumpAttempted++
			var buf bytes.Buffer
			if err := cluster.ResticManager.DumpSnapshotWithLogLevel(snapshot.Id, filePath, &buf, logrus.DebugLevel); err != nil {
				lastErr = err
				continue
			}
			var meta backupmgr.BackupMetadata
			if err := json.Unmarshal(buf.Bytes(), &meta); err != nil {
				lastErr = err
				continue
			}
			if strings.TrimSpace(meta.ResticSnapshotID) == "" {
				meta.ResticSnapshotID = snapshot.Id
			}
			if strings.TrimSpace(meta.BackupTool) == "" {
				meta.BackupTool = candidate.Tool
			}
			if strings.TrimSpace(meta.BackupLine) == "" {
				meta.BackupLine = backupmgr.BackupLineDefault
			}
			if destPath, ok := resolveSnapshotDestPath(base, meta.Dest); ok {
				exists, ok := destExistsCache[destPath]
				if !ok {
					var err error
					exists, err = cluster.snapshotPathExists(snapshot.Id, destPath)
					if err != nil {
						lastErr = err
						continue
					}
					destExistsCache[destPath] = exists
				}
				if !exists {
					lastErr = fmt.Errorf("metadata dest not found in snapshot %s: %s", snapshot.ShortId, destPath)
					continue
				}
			}
			summary := buildSnapshotMetadataSummary(&meta, candidate.Method, base)
			if summary == nil {
				continue
			}
			key := snapshotMetadataKey(candidate.Method, meta.BackupLine)
			if existing, ok := results[key]; ok {
				if summary.StartTime.After(existing.StartTime) {
					results[key] = summary
				}
			} else {
				results[key] = summary
			}
			dumpSucceeded++
		}
	}
	if dumpAttempted > 0 || dumpSkipped > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlDbg, "Snapshot %s metadata extraction summary: attempted=%d succeeded=%d skipped=%d", snapshot.ShortId, dumpAttempted, dumpSucceeded, dumpSkipped)
	}
	if len(results) == 0 {
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no metadata files found in snapshot %s", snapshot.ShortId)
	}
	return results, nil
}

func resolveSnapshotDestPath(base, dest string) (string, bool) {
	base = strings.TrimSpace(base)
	dest = strings.TrimSpace(dest)
	if base == "" || dest == "" {
		return "", false
	}
	if strings.HasPrefix(dest, base) {
		return dest, true
	}
	if filepath.IsAbs(dest) {
		return "", false
	}
	candidate := filepath.Join(base, dest)
	if !isPathWithinBase(base, candidate) {
		return "", false
	}
	return candidate, true
}

func (cluster *Cluster) snapshotPathExists(snapshotID, path string) (bool, error) {
	if cluster == nil || cluster.ResticManager == nil {
		return false, fmt.Errorf("restic manager not initialized")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return false, nil
	}
	entries, err := cluster.ResticManager.ListSnapshotWithLogLevel(snapshotID, []string{path}, true, logrus.DebugLevel)
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func snapshotAllowedTools(snapshot *backupmgr.BackupSnapshot) map[string]bool {
	if snapshot == nil || len(snapshot.Tags) == 0 {
		return nil
	}
	line := strings.ToLower(strings.TrimSpace(getSnapshotTagValue(snapshot.Tags, "line")))
	if line != strings.ToLower(backupmgr.BackupLineAdhoc) {
		return nil
	}
	tool := strings.ToLower(strings.TrimSpace(getSnapshotTagValue(snapshot.Tags, "backup-tool")))
	if tool == "" {
		return nil
	}
	return map[string]bool{tool: true}
}

func getSnapshotTagValue(tags []string, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	prefix := key + ":"
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if strings.HasPrefix(strings.ToLower(tag), strings.ToLower(prefix)) {
			return strings.TrimSpace(tag[len(prefix):])
		}
	}
	return getSnapshotTagFallbackValue(tags, key)
}

func getSnapshotTagFallbackValue(tags []string, key string) string {
	if len(tags) == 0 {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "line":
		return getSnapshotTagLegacyLine(tags)
	case "backup-tool":
		return getSnapshotTagLegacyTool(tags)
	default:
		return ""
	}
}

func getSnapshotTagLegacyLine(tags []string) string {
	var line string
	for _, tag := range tags {
		normalized := normalizeBackupLine(tag)
		if normalized == "" {
			continue
		}
		if line == "" {
			line = normalized
			continue
		}
		if line != normalized {
			return ""
		}
	}
	return line
}

func getSnapshotTagLegacyTool(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	toolSet := make(map[string]bool, len(snapshotMetadataCandidates))
	for _, candidate := range snapshotMetadataCandidates {
		tool := strings.ToLower(strings.TrimSpace(candidate.Tool))
		if tool == "" {
			continue
		}
		toolSet[tool] = true
	}
	var tool string
	for _, tag := range tags {
		trimmed := strings.ToLower(strings.TrimSpace(tag))
		if trimmed == "" {
			continue
		}
		if !toolSet[trimmed] {
			continue
		}
		if tool == "" {
			tool = trimmed
			continue
		}
		if tool != trimmed {
			return ""
		}
	}
	return tool
}

// ReconcileSnapshotMetadata detects drift between metadata files and restic snapshots
func (cluster *Cluster) ReconcileSnapshotMetadata() (*ReconciliationReport, error) {
	report := &ReconciliationReport{
		OrphanedMetadata: make([]string, 0),
		MissingMetadata:  make([]string, 0),
		Timestamp:        time.Now(),
		CleanedUp:        false,
	}
	manager := cluster.getSnapshotMetadataManager()

	// Get all restic snapshots
	snapshots := cluster.GetSnapshots()

	// Build map of snapshot IDs for quick lookup
	snapshotIDs := make(map[string]bool)
	for _, snap := range snapshots {
		snapshotIDs[snap.Id] = true
	}

	// Get all metadata files
	var err error
	metadataFiles := []string{}
	if manager != nil && manager.resticMetadataDir != "" {
		pattern := filepath.Join(manager.resticMetadataDir, "*"+snapshotMetadataFileExtension)
		metadataFiles, err = filepath.Glob(pattern)
		if err != nil {
			return report, fmt.Errorf("failed to list restic metadata files: %w", err)
		}
	}

	// Track metadata snapshot IDs to detect missing metadata later
	metadataSnapshotIDs := make(map[string]bool)
	if manager != nil && manager.cache != nil {
		for _, snapshotID := range manager.cache.SnapshotIDs() {
			if snapshotID == "" {
				continue
			}
			metadataSnapshotIDs[snapshotID] = true
			if !snapshotIDs[snapshotID] {
				cluster.dropSnapshotMetadataCaches(snapshotID)
			}
		}
	}
	var persisted map[string]*snapshotMetadataCacheEntry
	if manager != nil && manager.resticMetadataDir != "" {
		persisted, err = cluster.loadSnapshotMetadataEntriesFromDisk()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to scan persisted snapshot metadata: %v", err)
		} else {
			for snapshotID := range persisted {
				if snapshotID == "" {
					continue
				}
				metadataSnapshotIDs[snapshotID] = true
			}
		}
	}

	// Check for orphaned metadata
	for _, metaFile := range metadataFiles {
		base := filepath.Base(metaFile)
		if !strings.HasSuffix(base, snapshotMetadataFileExtension) {
			continue
		}
		snapshotID := strings.TrimSuffix(base, snapshotMetadataFileExtension)
		if snapshotID == "" {
			continue
		}
		if !snapshotMetadataIDPattern.MatchString(snapshotID) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Invalid restic metadata filename: %s", base)
			continue
		}
		metadataSnapshotIDs[snapshotID] = true

		// Check if snapshot exists
		if !snapshotIDs[snapshotID] {
			report.OrphanedMetadata = append(report.OrphanedMetadata, metaFile)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Orphaned metadata: %s references deleted snapshot %s", base, snapshotID)

			// Auto-cleanup if enabled
			if cluster.Conf.BackupReconcileAutoCleanup {
				if err := os.Remove(metaFile); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to cleanup orphaned metadata %s: %v", metaFile, err)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Cleaned up orphaned metadata: %s", base)
					report.CleanedUp = true
				}
			}
		}
	}

	// Check for missing metadata (snapshots without metadata)
	for _, snap := range snapshots {
		if !metadataSnapshotIDs[snap.Id] {
			report.MissingMetadata = append(report.MissingMetadata, snap.Id)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Missing metadata for snapshot: %s (created %s)", snap.ShortId, snap.Time)
		}
	}

	if manager != nil && manager.resticMetadataDir != "" && len(persisted) > 0 {
		for snapshotID := range persisted {
			if snapshotID == "" {
				continue
			}
			if snapshotIDs[snapshotID] {
				continue
			}
			cluster.dropSnapshotMetadataCaches(snapshotID)
			if cluster.Conf.BackupReconcileAutoCleanup {
				if err := cluster.deleteSnapshotMetadataEntry(snapshotID); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to delete persisted snapshot metadata %s: %v", snapshotID, err)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Removed persisted snapshot metadata for pruned snapshot %s", snapshotID)
					report.CleanedUp = true
				}
			}
		}
	}

	return report, nil
}
