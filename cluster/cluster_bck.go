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

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

const (
	snapshotMetadataExtractorConcurrency    = 2
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
	if cluster.resticSnapshotLsCache == nil {
		cluster.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	cluster.resticMetadataDir = filepath.Join(cluster.WorkingDir, "backup", "restic_metadata")
	if cluster.resticMetadataDir == "" {
		return
	}
	if err := os.MkdirAll(cluster.resticMetadataDir, os.ModePerm); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to prepare restic metadata directory %s: %v", cluster.resticMetadataDir, err)
		return
	}
	if cluster.snapshotMetadataCache == nil {
		cluster.snapshotMetadataCache = newSnapshotMetadataCache()
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
		cluster.snapshotMetadataCache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
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
	if cluster == nil || cluster.resticMetadataDir == "" {
		return "", fmt.Errorf("restic metadata directory is not initialized")
	}
	sanitized, err := sanitizeSnapshotMetadataID(snapshotID)
	if err != nil {
		return "", err
	}
	return filepath.Join(cluster.resticMetadataDir, sanitized+snapshotMetadataFileExtension), nil
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
	if cluster == nil || cluster.resticMetadataDir == "" {
		return results, nil
	}
	pattern := filepath.Join(cluster.resticMetadataDir, "*"+snapshotMetadataFileExtension)
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
	if cluster == nil || cluster.resticMetadataDir == "" {
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
	if cluster == nil || cluster.ResticManager == nil {
		return nil, fmt.Errorf("restic manager not initialized")
	}
	snapshotID = strings.TrimSpace(snapshotID)
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshot id is empty")
	}
	cluster.resticSnapshotLsCacheMu.Lock()
	if cluster.resticSnapshotLsCache == nil {
		cluster.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	if cached, ok := cluster.resticSnapshotLsCache[snapshotID]; ok {
		cluster.resticSnapshotLsCacheMu.Unlock()
		return cached, nil
	}
	cluster.resticSnapshotLsCacheMu.Unlock()

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
	cluster.resticSnapshotLsCacheMu.Lock()
	if cluster.resticSnapshotLsCache == nil {
		cluster.resticSnapshotLsCache = make(map[string]map[string]bool)
	}
	cluster.resticSnapshotLsCache[snapshotID] = paths
	cluster.resticSnapshotLsCacheMu.Unlock()
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
	if cluster == nil || strings.TrimSpace(snapshotID) == "" {
		return
	}
	cluster.resticSnapshotLsCacheMu.Lock()
	defer cluster.resticSnapshotLsCacheMu.Unlock()
	if cluster.resticSnapshotLsCache == nil {
		return
	}
	delete(cluster.resticSnapshotLsCache, snapshotID)
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
		cluster.snapshotMetadataCache.Delete(snapshotID)
		cluster.clearResticSnapshotLsCache(snapshotID)
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

func (cluster *Cluster) ResticGetEnv() []string {
	newEnv := append(os.Environ(), "RESTIC_PASSWORD="+cluster.Conf.GetDecryptedValue("backup-restic-password"))
	newEnv = append(newEnv, "RESTIC_CACHE_DIR="+cluster.Conf.WorkingDir+"/"+cluster.Name+"/.cache/restic")

	if cluster.Conf.BackupResticAws {
		newEnv = append(newEnv, "AWS_ACCESS_KEY_ID="+cluster.Conf.BackupResticAwsAccessKeyId)
		newEnv = append(newEnv, "AWS_SECRET_ACCESS_KEY="+cluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret"))
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.Conf.BackupResticRepository+"/"+cluster.Name)
	} else {
		if _, err := os.Stat(cluster.GetResticLocalDir()); os.IsNotExist(err) {
			err := os.MkdirAll(cluster.GetResticLocalDir(), os.ModePerm)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Create archive directory failed: %s,%s", cluster.GetResticLocalDir(), err)
			}
		}
		newEnv = append(newEnv, "RESTIC_REPOSITORY="+cluster.GetResticLocalDir())
	}
	return newEnv
}

func (cluster *Cluster) ReloadResticEnv() {
	if cluster.ResticManager != nil {
		cluster.ResticManager.SetEnv(cluster.ResticGetEnv())
	}
}

func (cluster *Cluster) CheckResticInstallation() {
	if cluster.Conf.BackupRestic && cluster.VersionsMap.Get("restic") == nil {
		if err := cluster.RefreshResticVersion(); err != nil {
			cluster.SetState("WARN0121", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0121"], err), ErrFrom: "CLUSTER"})
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic version: %s", cluster.VersionsMap.Get("restic").ToString())
		}
	}
}

func (cluster *Cluster) CheckResticErrors() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// If repo cannot be initialized, all other errors are not relevant. So we just fetch the init repo errors
	if !cluster.ResticManager.CanInitRepo && cluster.ResticManager.HasAnyError() {
		err := cluster.ResticManager.FetchAndClearError(backupmgr.InitTask)
		cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		return
	}

	for task, err := range cluster.ResticManager.FetchAndClearErrors() {
		switch task {
		case backupmgr.FetchTask:
			cluster.SetState("WARN0093", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0093"], err), ErrFrom: "BACKUP"})
		case backupmgr.PurgeTask:
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
		case backupmgr.UnlockTask:
			cluster.SetState("WARN0095", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0095"], err), ErrFrom: "BACKUP"})
		default:
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unknown restic task error: %s", err)
		}
	}

}

func (cluster *Cluster) CheckResticConfigBackup() {
	if !cluster.Conf.BackupRestic {
		return
	}

	if err := cluster.BackupResticConfig(); err != nil {
		cluster.SetState("WARN0145", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0145"], err), ErrFrom: "BACKUP"})
	}
}

func (cluster *Cluster) StartResticManager() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	resticManager := backupmgr.NewResticRepo(cluster.Conf.BackupResticBinaryPath, cluster.MessageChan, config.ConstLogModRestic)
	if err := cluster.Conf.ValidateResticPermissions(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Invalid restic permission config: %s", err)
	}
	resticManager.OnPurgeComplete = cluster.handleResticPurgeComplete
	resticManager.SetPermissions(cluster.Conf.GetResticDirMode(), cluster.Conf.GetResticFileMode())
	resticManager.SetOperationTimeout(cluster.Conf.GetResticTimeout())
	resticManager.AutoDetectAndDisableMount()
	cluster.ResticManager = resticManager
	cluster.ReloadResticEnv()
	go cluster.ResticFetchRepo()
	return nil
}

func (cluster *Cluster) ResticInitRepo(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	err := cluster.ResticManager.InitRepo(force)
	if err != nil {
		cluster.SetState("WARN0092", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0092"], err), ErrFrom: "BACKUP"})
	}

	return err
}

func (cluster *Cluster) AddPurgeTask(snapshotID string) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	if snapshotID == "" {
		return fmt.Errorf("Unable to purge single snapshot: snapshot ID is empty")
	}

	cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
		SnapshotID: snapshotID,
	}, true)
	return nil
}

func (cluster *Cluster) ResticPurgeRepo(now bool) error {
	if cluster.Conf.BackupRestic {
		err := cluster.Conf.CheckKeepWithin() // Check if backup-keep-within is valid
		if err != nil {
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		if cluster.ResticManager == nil {
			cluster.StartResticManager()
		}

		hasKeepN := cluster.Conf.BackupKeepLast > 0 ||
			cluster.Conf.BackupKeepHourly > 0 ||
			cluster.Conf.BackupKeepDaily > 0 ||
			cluster.Conf.BackupKeepWeekly > 0 ||
			cluster.Conf.BackupKeepMonthly > 0 ||
			cluster.Conf.BackupKeepYearly > 0
		hasKeepWithin := strings.TrimSpace(cluster.Conf.BackupKeepWithin) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinHourly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinDaily) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinWeekly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinMonthly) != "" ||
			strings.TrimSpace(cluster.Conf.BackupKeepWithinYearly) != ""
		if !hasKeepN && !hasKeepWithin {
			err := fmt.Errorf("restic purge skipped: no keep-last/keep-within policy configured")
			cluster.SetState("WARN0094", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0094"], err), ErrFrom: "BACKUP"})
			return err
		}

		groupBy := strings.TrimSpace(cluster.Conf.BackupResticPurgeGroupBy)
		if groupBy == "" || strings.EqualFold(groupBy, "default") {
			groupBy = ""
		} else if strings.EqualFold(groupBy, "none") {
			groupBy = "none"
		}

		keepTemplates := parseResticKeepTagTemplates(cluster.Conf.BackupResticPurgeKeepTag, cluster)
		keepValues := map[string]string{
			"tenant":  cluster.Conf.Cloud18GitUser,
			"cluster": cluster.Name,
		}
		keepTags := make([]string, 0, len(keepTemplates))
		for _, template := range keepTemplates {
			rendered, ok := renderResticKeepTagTemplate(template, keepValues, cluster)
			if !ok {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to render restic keep-tag template %q", template)
				continue
			}
			if strings.TrimSpace(rendered) == "" {
				continue
			}
			keepTags = append(keepTags, rendered)
		}
		cluster.ResticManager.AddPurgeTask(backupmgr.ResticPurgeOption{
			KeepLast:          cluster.Conf.BackupKeepLast,
			KeepHourly:        cluster.Conf.BackupKeepHourly,
			KeepDaily:         cluster.Conf.BackupKeepDaily,
			KeepWeekly:        cluster.Conf.BackupKeepWeekly,
			KeepMonthly:       cluster.Conf.BackupKeepMonthly,
			KeepYearly:        cluster.Conf.BackupKeepYearly,
			KeepWithin:        cluster.Conf.BackupKeepWithin,
			KeepWithinHourly:  cluster.Conf.BackupKeepWithinHourly,
			KeepWithinDaily:   cluster.Conf.BackupKeepWithinDaily,
			KeepWithinWeekly:  cluster.Conf.BackupKeepWithinWeekly,
			KeepWithinMonthly: cluster.Conf.BackupKeepWithinMonthly,
			KeepWithinYearly:  cluster.Conf.BackupKeepWithinYearly,
			GroupBy:           groupBy,
			KeepTag:           keepTags,
		}, now)
	}
	return nil
}

func (cluster *Cluster) ResticFetchRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	// Check if no other fetch task queued
	if !cluster.ResticManager.HasFetchQueue() {
		cluster.ResticManager.AddFetchTask()
	}
}

func (cluster *Cluster) BackupResticConfig() error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if _, err := os.Stat(filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")); err == nil {
		// Backup already exists
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	dest := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")
	src := filepath.Join(repopath, "config")

	err := misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file backed up to %s", dest)
	return nil
}

func (cluster *Cluster) RestoreResticConfig(force bool) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	repopath := cluster.ResticManager.GetRepoPath()
	if repopath == "" {
		return fmt.Errorf("restic repo path is empty")
	}

	_, err := os.Stat(filepath.Join(repopath, "config"))
	if !os.IsNotExist(err) && !force {
		return fmt.Errorf("restic config file already exists in repo path %s", repopath)
	}

	dest := filepath.Join(repopath, "config")
	src := filepath.Join(cluster.Conf.WorkingDir, cluster.Name, "restic.config.bak")

	err = misc.CopyFile(src, dest)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restic config file restored from %s", src)
	return nil
}

func (cluster *Cluster) ResticUnlockRepo() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.ResticManager.AddUnlockTask()

}

func (cluster *Cluster) ResticGetQueue() ([]*backupmgr.ResticTask, error) {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil, nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.TaskQueue, nil
}

var resticTagTemplateKeySet = map[string]struct{}{
	"tenant":      {},
	"cluster":     {},
	"engine":      {},
	"version":     {},
	"backup-type": {},
	"backup-tool": {},
	"line":        {},
	"method":      {},
}

var resticKeepTagTemplateKeySet = map[string]struct{}{
	"tenant":  {},
	"cluster": {},
}

var resticTagTemplatePattern = regexp.MustCompile(`\{([^}]+)\}`)

func normalizeResticTagCategory(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	return normalized
}

func parseResticTagTemplates(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts := splitResticTagTemplates(value)
	templates := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		template := strings.TrimSpace(part)
		if template == "" {
			continue
		}
		if _, ok := seen[template]; ok {
			continue
		}
		seen[template] = struct{}{}
		templates = append(templates, template)
	}
	return templates
}

func parseResticKeepTagTemplates(value string, cluster *Cluster) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	parts, hadUnmatched := splitResticKeepTagTemplates(value)
	if hadUnmatched && cluster != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring restic keep-tag with unmatched quotes in %q", value)
	}
	templates := make([]string, 0, len(parts))
	seen := make(map[string]struct{})
	for _, part := range parts {
		template := strings.TrimSpace(part)
		if template == "" {
			continue
		}
		if _, ok := seen[template]; ok {
			continue
		}
		seen[template] = struct{}{}
		templates = append(templates, template)
	}
	return templates
}

func splitResticTagTemplates(value string) []string {
	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false

	for _, r := range value {
		if quote != 0 {
			if quote == '"' && !escaped && r == '\\' {
				escaped = true
				current.WriteRune(r)
				continue
			}
			if quote == '"' && escaped {
				current.WriteRune(r)
				escaped = false
				continue
			}
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '"', '\'':
			quote = r
			current.WriteRune(r)
		case ',':
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 || strings.HasSuffix(value, ",") {
		parts = append(parts, current.String())
	}

	return parts
}

func splitResticKeepTagTemplates(value string) ([]string, bool) {
	parts := make([]string, 0)
	var current strings.Builder
	var quote rune
	escaped := false
	hadUnmatched := false

	for _, r := range value {
		if quote != 0 {
			if quote == '"' && !escaped && r == '\\' {
				escaped = true
				current.WriteRune(r)
				continue
			}
			if quote == '"' && escaped {
				current.WriteRune(r)
				escaped = false
				continue
			}
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
			continue
		}

		switch r {
		case '"', '\'':
			quote = r
			current.WriteRune(r)
		case ',', ' ', '\t', '\n', '\r':
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if quote != 0 {
		hadUnmatched = true
	} else if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts, hadUnmatched
}

func validateResticKeepTagTemplatesStrict(value string) error {
	if strings.TrimSpace(value) == "" {
		return nil
	}

	_, hadUnmatched := splitResticKeepTagTemplates(value)
	if hadUnmatched {
		return fmt.Errorf("restic keep-tag has unmatched quotes")
	}
	return nil
}

func isQuotedResticTagLiteral(value string) bool {
	if len(value) < 2 {
		return false
	}
	first := value[0]
	last := value[len(value)-1]
	return (first == '"' && last == '"') || (first == '\'' && last == '\'')
}

func unquoteResticTagLiteral(value string) (string, bool) {
	if !isQuotedResticTagLiteral(value) {
		return value, false
	}

	quote := value[0]
	raw := value[1 : len(value)-1]
	if quote == '\'' {
		// Single quotes preserve content literally (no escape processing).
		return raw, true
	}

	var b strings.Builder
	b.Grow(len(raw))
	escaped := false
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	// Double quotes allow \\ and \" escapes (backslash only affects the next rune).
	return b.String(), true
}

func renderResticTagTemplate(template string, values map[string]string, cluster *Cluster) (string, bool) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", false
	}

	if literal, ok := unquoteResticTagLiteral(trimmed); ok {
		literal = strings.TrimSpace(literal)
		if literal == "" {
			return "", false
		}
		return literal, true
	}

	matches := resticTagTemplatePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		if strings.Contains(trimmed, ":") {
			return trimmed, true
		}
		key := normalizeResticTagCategory(trimmed)
		if _, ok := resticTagTemplateKeySet[key]; ok {
			value := strings.TrimSpace(values[key])
			if value == "" {
				return "", false
			}
			return fmt.Sprintf("%s:%s", key, value), true
		}
		if cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unknown restic tag template %q", trimmed)
		}
		return trimmed, true
	}

	rendered := trimmed
	for _, match := range matches {
		raw := match[1]
		key := normalizeResticTagCategory(raw)
		if key == "" {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid restic tag template %q", template)
			}
			return "", false
		}
		if _, ok := resticTagTemplateKeySet[key]; !ok {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unknown restic tag template key %q in %q", raw, template)
			}
			return "", false
		}
		value := strings.TrimSpace(values[key])
		if value == "" {
			return "", false
		}
		rendered = strings.ReplaceAll(rendered, "{"+raw+"}", value)
	}

	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return "", false
	}
	return rendered, true
}

func renderResticKeepTagTemplate(template string, values map[string]string, cluster *Cluster) (string, bool) {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" {
		return "", false
	}

	if literal, ok := unquoteResticTagLiteral(trimmed); ok {
		literal = strings.TrimSpace(literal)
		if literal == "" {
			return "", false
		}
		return literal, true
	}

	matches := resticTagTemplatePattern.FindAllStringSubmatch(trimmed, -1)
	if len(matches) == 0 {
		return trimmed, true
	}

	rendered := trimmed
	for _, match := range matches {
		raw := match[1]
		key := normalizeResticTagCategory(raw)
		if key == "" {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid restic keep-tag template %q", template)
			}
			return "", false
		}
		if _, ok := resticKeepTagTemplateKeySet[key]; !ok {
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Unsupported restic keep-tag template key %q in %q", raw, template)
			}
			return "", false
		}
		value := strings.TrimSpace(values[key])
		if value == "" {
			return "", false
		}
		rendered = strings.ReplaceAll(rendered, "{"+raw+"}", value)
	}

	rendered = strings.TrimSpace(rendered)
	if rendered == "" {
		return "", false
	}
	return rendered, true
}

func (server *ServerMonitor) BuildResticTags(backupType, backupTool, backupLine string, meta *backupmgr.BackupMetadata) []string {
	cluster := server.ClusterGroup
	lineValue := normalizeBackupLine(backupLine)
	if lineValue == "" {
		lineValue = backupmgr.BackupLineDefault
	}
	tagValues := map[string]string{
		"tenant":      cluster.Conf.Cloud18GitUser,
		"cluster":     cluster.Name,
		"engine":      server.DBVersion.Flavor,
		"version":     server.DBVersion.ToString(),
		"backup-type": backupType,
		"backup-tool": backupTool,
		"line":        lineValue,
		"method":      strings.TrimSpace(backupType),
	}

	templates := parseResticTagTemplates(cluster.Conf.BackupResticTags)
	tagSet := make(map[string]struct{})
	tags := make([]string, 0, len(templates)+3)
	for _, template := range templates {
		rendered, ok := renderResticTagTemplate(template, tagValues, cluster)
		if !ok || strings.TrimSpace(rendered) == "" {
			continue
		}
		if _, exists := tagSet[rendered]; exists {
			continue
		}
		tagSet[rendered] = struct{}{}
		tags = append(tags, rendered)
	}
	required := []string{}
	for _, tag := range required {
		if strings.HasSuffix(tag, ":") {
			continue
		}
		if _, exists := tagSet[tag]; exists {
			continue
		}
		tagSet[tag] = struct{}{}
		tags = append(tags, tag)
	}
	return tags
}

func getSnapshotSessionIDFromMetadata(cluster *Cluster, snapshot *backupmgr.BackupSnapshot) string {
	if cluster == nil || snapshot == nil {
		return ""
	}
	if cluster.snapshotMetadataCache != nil {
		if entry, ok := cluster.snapshotMetadataCache.Get(snapshot.Id); ok && entry != nil {
			for _, summary := range entry.Summaries {
				if summary != nil && strings.TrimSpace(summary.BackupSessionID) != "" {
					return strings.TrimSpace(summary.BackupSessionID)
				}
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

func (cluster *Cluster) ResticModifyQueue(moveType string, taskID, cmpID int) error {
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	return cluster.ResticManager.MoveTask(moveType, taskID, cmpID)
}

func (cluster *Cluster) ResticCancelTask(taskId int) error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancelling restic task ID %d", taskId)

	cluster.ResticManager.CancelTask(taskId)

	return nil
}

func (cluster *Cluster) ResticClearQueue() error {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return nil
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Clearing pending restic tasks from queue. Total tasks: %d", len(cluster.ResticManager.TaskQueue))

	cluster.ResticManager.ClearQueue()

	return nil
}

// ResticRunQueue starts processing the restic task queue
func (cluster *Cluster) ResticRunQueue() {

	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting restic task queue processing. Total tasks: %d", len(cluster.ResticManager.TaskQueue))
	cluster.ResticManager.ResumeWorker()
	cluster.IsResticQueuePaused = false
}

// ResticPauseQueue pauses the next restic task queue processing
func (cluster *Cluster) ResticPauseQueue() {
	// No need to add wait since it will be checked each monitor loop
	if !cluster.Conf.BackupRestic {
		return
	}

	if cluster.ResticManager == nil {
		cluster.StartResticManager()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pausing restic task queue processing")
	cluster.ResticManager.PauseWorker()
	cluster.IsResticQueuePaused = true
}

func (cluster *Cluster) UpdateDiskStat(dirpath string) error {
	diskstat, err := disk.Usage(dirpath)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	if diskstat == nil {
		err := fmt.Errorf("disk usage is nil for %s", dirpath)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Error getting disk usage for dir %s: %s", dirpath, err)
		return err
	}

	cluster.DiskStatManager.UpdateStat(dirpath, diskstat)

	return nil
}

// TODO: Restic password change
func (cluster *Cluster) ChangeResticRepoPassword(newpass string) error {
	if !cluster.Conf.BackupRestic {
		return fmt.Errorf("Restic backup is not enabled")
	}

	if newpass == "" {
		return fmt.Errorf("New password is empty")
	}

	if newpass == cluster.Conf.GetDecryptedValue("backup-restic-password") {
		return fmt.Errorf("New password is the same as the current one")
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Changing restic password for cluster %s", cluster.Name)

	cluster.ReloadResticEnv()

	keylist, err := cluster.ResticManager.GetRepoKeyList()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to list restic keys: %s", err)
		return err
	}

	keylen := len(keylist)
	if keylen == 0 {
		return fmt.Errorf("No keys found in the restic repository")
	}

	oldkeyid := ""
	for _, key := range keylist {
		if key.Current {
			oldkeyid = key.Id
			break
		}
	}

	if _, err := os.Stat(cluster.ResticManager.GetCacheDirPath()); os.IsNotExist(err) {
		err := os.MkdirAll(cluster.ResticManager.GetCacheDirPath(), os.ModePerm)
		if err != nil {
			return fmt.Errorf("Error creating restic cache directory: %s", err)
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic cache directory created: %s", cluster.ResticManager.GetCacheDirPath())
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Adding new key to restic repository")

	newpassfile := filepath.Join(cluster.ResticManager.GetCacheDirPath(), "newpass.txt")
	err = os.WriteFile(newpassfile, []byte(newpass), 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to write new password file: %s", err)
		return fmt.Errorf("failed to write new password file: %w", err)
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Temporary password file created: %s", newpassfile)

	defer func() {
		if _, err := os.Stat(newpassfile); err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Removing temporary password file")
			err := os.Remove(newpassfile)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove temporary password file: %s", err)
			}
		}
	}()

	err = cluster.ResticManager.AddRepoKey(newpassfile)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to add new key to restic repository: %s", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New key added to restic repository successfully. Saving new password.")

	// Save new password in configuration
	cluster.SetResticPassword(newpass)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "New restic password saved in configuration successfully. Removing old key from repository using new password.")

	// Reload env with new password
	cluster.ReloadResticEnv()

	// Remove old key using new password
	err = cluster.ResticManager.RemoveRepoKey(oldkeyid)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to remove old key from restic repository: %s", err)
		return nil
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Restic password changed successfully. New key added and old key removed.")

	return nil
}

func (cluster *Cluster) CheckBackupToolVersions() {
	bcksrv := cluster.GetBackupServer()
	if bcksrv == nil {
		bcksrv = cluster.GetMaster()
		if bcksrv == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "No backup server or master server found for cluster %s", cluster.Name)
			return
		}
	}

	cluster.CheckLogicalBackupToolVersion(bcksrv)
	cluster.CheckPhysicalBackupToolVersion(bcksrv)
}

func (cluster *Cluster) CheckLogicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, logical := server.GetLatestMeta("logical")
	if logical != nil {
		v, _ := cluster.GetToolsVersion(logical.BackupTool)
		if v != nil && logical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(logical.BackupTool, logical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0156", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0156"], v.ToString(), logical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not compatible with restore version", server.URL)
			} else if cluster.IsInErrorState("WARN0156", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0156@%s", server.URL))
			}
		}
	}
	return nil
}

func (cluster *Cluster) CheckPhysicalBackupToolVersion(server *ServerMonitor) error {
	if server == nil {
		return fmt.Errorf("Server is nil")
	}

	_, physical := server.GetLatestMeta("physical")
	if physical != nil {
		v, _ := cluster.GetToolsVersion(physical.BackupTool)
		if v != nil && physical.BackupToolVersion != "" {
			backupv, _ := version.NewVersionFromString(physical.BackupTool, physical.BackupToolVersion)
			if v.ToInt(2) != backupv.ToInt(2) { // Major and minor version must match
				cluster.SetState("WARN0157", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0157"], v.ToString(), physical.BackupToolVersion), ErrFrom: "CHECK", ServerUrl: server.URL})
				return fmt.Errorf("Node %s backup tool version is not same with restore version", server.URL)
			} else if cluster.IsInErrorState("WARN0157", server.URL) {
				// Remove state if version is now correct
				cluster.GetStateMachine().DeleteState(fmt.Sprintf("WARN0157@%s", server.URL))
			}
		}
	}
	return nil
}

// getSanitizedCompressionLevel validates and returns a safe compression level (1-9).
// If the configured value is out of range, it logs a warning and returns the default (6).
func (cluster *Cluster) getSanitizedCompressionLevel(logModule int) int {
	level := cluster.Conf.CompressBackupsCompressionLevel
	if level < 1 || level > 9 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlWarn,
			"compress-backups-compression-level value %d is out of range (1-9), using default 6", level)
		return 6 // Default to standard compression
	}
	return level
}

// getSanitizedParallelBlocks validates and returns safe parallel blocks (1-32).
// If the configured value is <= 0, it returns the default (16) for performance.
// If the configured value is > 32, it logs a warning and caps to 32.
func (cluster *Cluster) getSanitizedParallelBlocks(logModule int) int {
	blocks := cluster.Conf.CompressBackupsParallelBlocks
	if blocks <= 0 {
		return 16 // Default for SST/restore performance
	}
	if blocks > 32 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, logModule, config.LvlWarn,
			"compress-backups-parallel-blocks value %d exceeds maximum 32, capping to 32", blocks)
		return 32 // Cap at maximum safe value
	}
	return blocks
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
	return &SnapshotMetadataSummary{
		Dest:             strings.TrimSpace(meta.Dest),
		BackupMethod:     backupMethodToString(method),
		BackupTool:       meta.BackupTool,
		BackupLine:       meta.BackupLine,
		StartTime:        meta.StartTime,
		EndTime:          meta.EndTime,
		BackupSessionID:  meta.BackupSessionID,
		ResticSnapshotID: meta.ResticSnapshotID,
		ResticBasePath:   strings.TrimSpace(basepath),
	}
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
				key := fmt.Sprintf("%d|%s", method, strings.ToLower(strings.TrimSpace(meta.BackupLine)))
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
	var cacheEntry *snapshotMetadataCacheEntry
	if cluster.snapshotMetadataCache != nil {
		cacheEntry, _ = cluster.snapshotMetadataCache.Get(snapshot.Id)
	}
	if cacheEntry != nil && len(cacheEntry.Summaries) > 0 {
		for key, summary := range cacheEntry.Summaries {
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
	if cluster == nil || cluster.snapshotMetadataCache == nil || strings.TrimSpace(snapshotID) == "" {
		return nil, false
	}
	return cluster.snapshotMetadataCache.Get(snapshotID)
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
	if !cluster.Conf.BackupRestic || cluster.ResticManager == nil || cluster.snapshotMetadataCache == nil {
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
	if cluster == nil || cluster.snapshotMetadataCache == nil || strings.TrimSpace(snapshotID) == "" {
		return nil, false
	}
	now := time.Now()
	shouldStart := false
	entry := cluster.snapshotMetadataCache.Update(snapshotID, func(entry *snapshotMetadataCacheEntry) {
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
	sem := cluster.snapshotMetadataExtractorSem
	if sem != nil {
		sem <- struct{}{}
		defer func() { <-sem }()
	}
	summaries, err := cluster.extractMetadataFromResticSnapshot(snapshot)
	now := time.Now()
	if err != nil {
		updatedEntry := cluster.snapshotMetadataCache.Update(snapshot.Id, func(entry *snapshotMetadataCacheEntry) {
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
	updatedEntry := cluster.snapshotMetadataCache.Update(snapshot.Id, func(entry *snapshotMetadataCacheEntry) {
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
			key := fmt.Sprintf("%d|%s", candidate.Method, strings.ToLower(strings.TrimSpace(meta.BackupLine)))
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
	return ""
}

// ReconcileSnapshotMetadata detects drift between metadata files and restic snapshots
func (cluster *Cluster) ReconcileSnapshotMetadata() (*ReconciliationReport, error) {
	report := &ReconciliationReport{
		OrphanedMetadata: make([]string, 0),
		MissingMetadata:  make([]string, 0),
		Timestamp:        time.Now(),
		CleanedUp:        false,
	}

	// Get all restic snapshots
	snapshots := cluster.GetSnapshots()

	// Build map of snapshot IDs for quick lookup
	snapshotIDs := make(map[string]bool)
	for _, snap := range snapshots {
		snapshotIDs[snap.Id] = true
	}

	// Get all metadata files
	backupDir := cluster.WorkingDir + "/backup"
	metadataFiles, err := filepath.Glob(backupDir + "/*.json")
	if err != nil {
		return report, fmt.Errorf("failed to list metadata files: %w", err)
	}

	// Track metadata snapshot IDs to detect missing metadata later
	metadataSnapshotIDs := make(map[string]bool)
	if cluster.snapshotMetadataCache != nil {
		for _, snapshotID := range cluster.snapshotMetadataCache.SnapshotIDs() {
			if snapshotID == "" {
				continue
			}
			metadataSnapshotIDs[snapshotID] = true
			if !snapshotIDs[snapshotID] {
				cluster.snapshotMetadataCache.Delete(snapshotID)
				cluster.clearResticSnapshotLsCache(snapshotID)
			}
		}
	}
	var persisted map[string]*snapshotMetadataCacheEntry
	if cluster.resticMetadataDir != "" {
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
		data, err := os.ReadFile(metaFile)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to read metadata %s: %v", metaFile, err)
			continue
		}

		var meta backupmgr.BackupMetadata
		if err := json.Unmarshal(data, &meta); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Failed to parse metadata %s: %v", metaFile, err)
			continue
		}

		if !meta.ResticEnabled && strings.TrimSpace(meta.ResticSnapshotID) == "" {
			continue
		}
		if strings.TrimSpace(meta.ResticSnapshotID) == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Metadata missing restic snapshot ID: %s", filepath.Base(metaFile))
			continue
		}
		metadataSnapshotIDs[meta.ResticSnapshotID] = true

		// Check if snapshot exists
		if !snapshotIDs[meta.ResticSnapshotID] {
			report.OrphanedMetadata = append(report.OrphanedMetadata, metaFile)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlWarn, "Orphaned metadata: %s references deleted snapshot %s", filepath.Base(metaFile), meta.ResticSnapshotID)

			// Auto-cleanup if enabled
			if cluster.Conf.BackupReconcileAutoCleanup {
				if err := os.Remove(metaFile); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlErr, "Failed to cleanup orphaned metadata %s: %v", metaFile, err)
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModRestic, config.LvlInfo, "Cleaned up orphaned metadata: %s", filepath.Base(metaFile))
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

	if cluster.resticMetadataDir != "" && len(persisted) > 0 {
		for snapshotID := range persisted {
			if snapshotID == "" {
				continue
			}
			if snapshotIDs[snapshotID] {
				continue
			}
			cluster.snapshotMetadataCache.Delete(snapshotID)
			cluster.clearResticSnapshotLsCache(snapshotID)
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
