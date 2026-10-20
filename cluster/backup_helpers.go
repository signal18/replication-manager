// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// BackupRunOptions defines runtime overrides for backup execution behavior.
// ResticEnabled can force enable/disable restic for this run.
type BackupRunOptions struct {
	ResticEnabled *bool
	BackupID      int64
}

// registeredMetaFilePattern is the record of a registered backup, named by its id
// (<tool>.<id>.meta.json): its file keeps its own name, no new backup replaces it.
var registeredMetaFilePattern = regexp.MustCompile(`\.(\d+)\.meta\.json$`)
var (
	ErrBackupInProgress         = errors.New("backup is still running")
	ErrBackupInvalidID          = errors.New("invalid backup id")
	ErrBackupNotFound           = errors.New("backup not found")
	ErrBackupDiscovered         = errors.New("discovered backup: unregister it instead")
	ErrBackupDuplicateScheduled = errors.New("a discovered record of a scheduled backup file: it is removed at the next check, use the scheduled backup")
)

// retireReplacedBackup handles the record of the backup a new one of its type replaces in its
// server's backup directory (#2091): held by a snapshot of an archive, it stays in the
// catalogue as only in the archive; held by none, it exists no more and leaves the catalogue.
func (server *ServerMonitor) retireReplacedBackup(id int64) {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	server.retireReplacedBackupLocked(id)
}

// retireReplacedBackupLocked is retireReplacedBackup with backupMetaMutex held.
func (server *ServerMonitor) retireReplacedBackupLocked(id int64) {
	cluster := server.ClusterGroup
	if cluster == nil || id == 0 {
		return
	}
	m := cluster.BackupMetaMap.Get(id)
	if m == nil {
		return
	}
	if len(m.ArchiveRefs) > 0 || strings.TrimSpace(m.ResticSnapshotID) != "" {
		m.OriginalRemoved = true
		return
	}
	cluster.BackupMetaMap.Delete(id)
}

// shouldRunRestic decides whether restic should be used for a backup run.
// It respects cluster config and an optional per-run override.
func (server *ServerMonitor) shouldRunRestic(opts BackupRunOptions) bool {
	cluster := server.ClusterGroup
	// an attached external archive plugin archives the backups as restic does (#2091)
	external := false
	if cluster != nil {
		_, external = cluster.attachedExternalArchive()
	}
	if cluster == nil || (!cluster.Conf.BackupRestic && !external) {
		if opts.ResticEnabled != nil && *opts.ResticEnabled && cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Restic requested but disabled for %s", server.URL)
		}
		return false
	}

	if opts.ResticEnabled == nil {
		return true
	}
	return *opts.ResticEnabled
}

func sanitizeSessionComponent(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "na"
	}
	var b strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	cleaned := strings.Trim(b.String(), "-")
	if cleaned == "" {
		return "na"
	}
	return cleaned
}

func (server *ServerMonitor) generateBackupSessionID(method backupmgr.BackupMethod, ts time.Time) string {
	clusterName := "cluster"
	if server.ClusterGroup != nil && strings.TrimSpace(server.ClusterGroup.Name) != "" {
		clusterName = server.ClusterGroup.Name
	}
	host := server.Host
	if host == "" {
		host = server.Name
	}
	port := server.Port
	if port == "" {
		if parts := strings.Split(server.URL, ":"); len(parts) >= 2 {
			host = parts[0]
			port = parts[len(parts)-1]
		}
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	methodLabel := "custom"
	switch method {
	case backupmgr.BackupMethodLogical:
		methodLabel = "logical"
	case backupmgr.BackupMethodPhysical:
		methodLabel = "physical"
	}
	return fmt.Sprintf("%s-%s-%s-%s-%d",
		sanitizeSessionComponent(clusterName),
		sanitizeSessionComponent(host),
		sanitizeSessionComponent(port),
		sanitizeSessionComponent(methodLabel),
		ts.Unix(),
	)
}

func (server *ServerMonitor) ensureBackupSessionID(meta *backupmgr.BackupMetadata, method backupmgr.BackupMethod, ts time.Time) string {
	if server == nil || meta == nil {
		return ""
	}
	if method == 0 && meta.BackupMethod != 0 {
		method = meta.BackupMethod
	}
	if ts.IsZero() {
		if !meta.StartTime.IsZero() {
			ts = meta.StartTime
		} else if meta.Id > 0 {
			ts = time.Unix(meta.Id, 0)
		}
	}
	if meta.BackupSessionID == "" {
		meta.BackupSessionID = server.generateBackupSessionID(method, ts)
	}
	return meta.BackupSessionID
}

func inferBackupMethod(meta *backupmgr.BackupMetadata) backupmgr.BackupMethod {
	if meta == nil {
		return backupmgr.BackupMethodLogical
	}
	if meta.BackupMethod == backupmgr.BackupMethodLogical || meta.BackupMethod == backupmgr.BackupMethodPhysical {
		return meta.BackupMethod
	}
	switch meta.BackupTool {
	case config.ConstBackupPhysicalTypeXtrabackup, config.ConstBackupPhysicalTypeMariaBackup:
		return backupmgr.BackupMethodPhysical
	default:
		return backupmgr.BackupMethodLogical
	}
}

// buildBackupMetaFileName is the record file of a backup: <tool>.meta.json for the backup of
// its type the next one replaces, <tool>.<id>.meta.json for a registered one (byID).
func (server *ServerMonitor) buildBackupMetaFileName(backupTool string, backupID int64, byID bool) string {
	if backupTool == "" {
		return ""
	}
	if !isSafeBackupToolName(backupTool) {
		if cluster := server.ClusterGroup; cluster != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Invalid backup tool name %q for metadata file", backupTool)
		}
		return ""
	}

	dir := server.GetMyBackupDirectory()
	if byID && backupID > 0 {
		return filepath.Join(dir, fmt.Sprintf("%s.%d.meta.json", backupTool, backupID))
	}
	return filepath.Join(dir, fmt.Sprintf("%s.meta.json", backupTool))
}

func (server *ServerMonitor) backupMetaFilePath(meta *backupmgr.BackupMetadata) string {
	if meta == nil {
		return ""
	}
	defaultPath := server.buildBackupMetaFileName(meta.BackupTool, meta.Id, meta.Discovered)
	if meta.MetaFile != "" {
		metaFile := meta.MetaFile
		if !meta.Discovered {
			defaultBase := filepath.Base(defaultPath)
			if defaultBase != "" && filepath.Base(metaFile) != defaultBase {
				if cluster := server.ClusterGroup; cluster != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring custom metadata filename %s: the backup of its type is recorded as %s", metaFile, defaultBase)
				}
				return defaultPath
			}
		}
		if filepath.IsAbs(metaFile) {
			return metaFile
		}
		return filepath.Join(server.GetMyBackupDirectory(), metaFile)
	}
	return defaultPath
}

func (server *ServerMonitor) resolveMysqldumpDest(splitDump bool) (string, string, string, bool) {
	dir := server.GetMyBackupDirectory()
	filename := dir + "mysqldump.sql.gz"
	outputdir := ""
	if splitDump {
		outputdir = filepath.Join(dir, "splitdump")
	}
	if splitDump {
		return filename, outputdir, outputdir, false
	}
	return filename, outputdir, filename, true
}

func parseRegisteredMetaFileID(name string) (int64, bool) {
	matches := registeredMetaFilePattern.FindStringSubmatch(name)
	if len(matches) != 2 {
		return 0, false
	}
	id, err := strconv.ParseInt(matches[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func parseBackupToolFromMetaFilename(name string) string {
	trimmed := strings.TrimSuffix(name, ".meta.json")
	if trimmed == name {
		return ""
	}
	// For a registered backup's record like "mysqldump.1234567890.meta.json", return "mysqldump"
	// For default files like "mysqldump.meta.json", return "mysqldump"
	idx := strings.LastIndex(trimmed, ".")
	if idx < 0 {
		// No dot found, so the trimmed string is the tool name (default case)
		return trimmed
	}
	// Dot found, return everything before it (a registered backup's record)
	return trimmed[:idx]
}

func readBackupMetadataFile(path string) (*backupmgr.BackupMetadata, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	meta := new(backupmgr.BackupMetadata)
	if err := json.NewDecoder(file).Decode(meta); err != nil {
		return nil, err
	}
	// a discovered logical dump is a full backup; a release before #2073 left its strategy unset
	if meta != nil && meta.Discovered && meta.BackupStrategy == 0 {
		meta.BackupStrategy = backupmgr.BackupStrategyFull
	}
	if meta != nil && strings.TrimSpace(meta.Dest) != "" {
		if compressed, err := backupmgr.DetectCompressionFromDest(meta.Dest); err == nil {
			meta.Compressed = compressed
		}
	}
	return meta, nil
}

func parseCompressionOverride(value string) (bool, bool) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" || trimmed == "auto" {
		return false, false
	}
	switch trimmed {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}

func (cluster *Cluster) resolveBackupCompression(method backupmgr.BackupMethod, tool string) bool {
	if cluster == nil || cluster.Conf == nil {
		return false
	}
	_ = tool
	conf := cluster.Conf
	switch method {
	case backupmgr.BackupMethodLogical:
		if val, ok := parseCompressionOverride(conf.CompressBackupsLogical); ok {
			return val
		}
	case backupmgr.BackupMethodPhysical:
		if val, ok := parseCompressionOverride(conf.CompressBackupsPhysical); ok {
			return val
		}
	}
	return conf.CompressBackups
}

func (cluster *Cluster) shouldUncompressOnSenderForReseed() bool {
	if cluster == nil || cluster.Conf == nil {
		return true
	}
	return !cluster.Conf.BackupReseedRemoteDecompress
}

// LoadRegisteredBackupMetadata reads the records of the server's registered backups
// (<tool>.<id>.meta.json). A record of that name a previous release wrote for a backup it took
// under its id is read as registered too: its file stays as it is, no new backup replaces it.
func (server *ServerMonitor) LoadRegisteredBackupMetadata() ([]*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	dir := server.GetMyBackupDirectory()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	metas := make([]*backupmgr.BackupMetadata, 0)
	var lastErr error
	readErrors := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !registeredMetaFilePattern.MatchString(name) {
			continue
		}

		path := filepath.Join(dir, name)
		meta, err := readBackupMetadataFile(path)
		if err != nil {
			readErrors++
			lastErr = err
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Failed reading registered backup metadata %s: %s", path, err)
			}
			continue
		}
		if meta == nil {
			continue
		}

		meta.Discovered = true
		if meta.Id == 0 {
			if id, ok := parseRegisteredMetaFileID(name); ok {
				meta.Id = id
			}
		}
		if meta.Id == 0 {
			continue
		}
		if meta.BackupTool == "" {
			meta.BackupTool = parseBackupToolFromMetaFilename(name)
		}
		if meta.BackupTool == "" {
			continue
		}
		if meta.MetaFile == "" {
			meta.MetaFile = path
		}
		if meta.Source == "" {
			meta.Source = server.URL
		}
		method := inferBackupMethod(meta)
		meta.BackupMethod = method
		server.ensureBackupSessionID(meta, method, meta.StartTime)

		if cluster != nil {
			// a backup already registered keeps its live entry: a running backup or
			// verification updates it, the copy just read from its meta file may be older;
			// a copy registered is first closed when its verification was interrupted
			if live := cluster.BackupMetaMap.Get(meta.Id); live != nil {
				meta = live
			} else {
				server.closeInterruptedVerification(meta)
				server.glueLegacyArchiveRef(meta)
				cluster.BackupMetaMap.Set(meta.Id, meta)
			}
		}
		metas = append(metas, meta)
	}

	if readErrors > 0 {
		return metas, fmt.Errorf("failed reading %d registered backup metadata file(s): %w", readErrors, lastErr)
	}

	return metas, nil
}

// GetLatestBackupMeta is the server's latest backup of a method (logical, physical) or tool;
// withRegistered false leaves out the registered ones.
func (server *ServerMonitor) GetLatestBackupMeta(method string, withRegistered bool) (int64, *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if cluster == nil {
		return 0, nil
	}

	var latest int64
	var meta *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Range(func(k, v any) bool {
		m := v.(*backupmgr.BackupMetadata)
		valid := false
		switch method {
		case "logical":
			if m.BackupMethod == backupmgr.BackupMethodLogical {
				valid = true
			}
		case "physical":
			if m.BackupMethod == backupmgr.BackupMethodPhysical {
				valid = true
			}
		default:
			if m.BackupTool == method {
				valid = true
			}
		}

		if m.Source != server.URL {
			valid = false
		}

		if !withRegistered && m.Discovered {
			valid = false
		}

		if valid && latest < m.Id {
			latest = m.Id
			meta = m
		}

		return true
	})

	return latest, meta
}

func isActiveBackupMeta(target, active *backupmgr.BackupMetadata) bool {
	if target == nil || active == nil {
		return false
	}
	if target.BackupSessionID != "" && active.BackupSessionID != "" {
		if target.BackupSessionID == active.BackupSessionID {
			return true
		}
		return false
	}
	if target.Id > 0 && active.Id > 0 && target.Id == active.Id {
		return true
	}
	if target.Dest != "" && active.Dest != "" {
		return filepath.Clean(target.Dest) == filepath.Clean(active.Dest)
	}
	return false
}

func (cluster *Cluster) isBackupMetaInProgress(meta *backupmgr.BackupMetadata, server *ServerMonitor) bool {
	if cluster == nil || server == nil || meta == nil {
		return false
	}

	server.backupMetaMutex.Lock()
	var logicalCopy *backupmgr.BackupMetadata
	var physicalCopy *backupmgr.BackupMetadata
	if server.LastBackupMeta.Logical != nil {
		metaCopy := *server.LastBackupMeta.Logical
		logicalCopy = &metaCopy
	}
	if server.LastBackupMeta.Physical != nil {
		metaCopy := *server.LastBackupMeta.Physical
		physicalCopy = &metaCopy
	}
	server.backupMetaMutex.Unlock()

	knownLogical := false
	knownPhysical := false
	switch meta.BackupTool {
	case config.ConstBackupLogicalTypeMysqldump,
		config.ConstBackupLogicalTypeMydumper,
		config.ConstBackupLogicalTypeDumpling,
		"script":
		knownLogical = true
	case config.ConstBackupPhysicalTypeXtrabackup,
		config.ConstBackupPhysicalTypeMariaBackup:
		knownPhysical = true
	}

	method := inferBackupMethod(meta)
	if knownLogical {
		method = backupmgr.BackupMethodLogical
	} else if knownPhysical {
		method = backupmgr.BackupMethodPhysical
	}
	if !knownLogical && !knownPhysical {
		if !cluster.IsInBackup() {
			return false
		}
		return isActiveBackupMeta(meta, logicalCopy) || isActiveBackupMeta(meta, physicalCopy)
	}

	switch method {
	case backupmgr.BackupMethodLogical:
		if !cluster.InLogicalBackup && !cluster.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
			return false
		}
		return isActiveBackupMeta(meta, logicalCopy)
	case backupmgr.BackupMethodPhysical:
		if !cluster.InPhysicalBackup && !cluster.IsInResticBackupForMethod(backupmgr.BackupMethodPhysical) {
			return false
		}
		return isActiveBackupMeta(meta, physicalCopy)
	default:
		if !cluster.IsInBackup() {
			return false
		}
		return isActiveBackupMeta(meta, logicalCopy) || isActiveBackupMeta(meta, physicalCopy)
	}
}

func (cluster *Cluster) DeleteBackupByID(backupID int64) (*backupmgr.BackupMetadata, error) {
	if cluster == nil || cluster.BackupMetaMap == nil {
		return nil, fmt.Errorf("cluster backups not available")
	}
	// Keep verification reservation and deletion mutually exclusive. Holding this
	// only spans the user action; restore work never holds it.
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if backupID <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrBackupInvalidID, backupID)
	}

	meta := cluster.BackupMetaMap.Get(backupID)
	if meta == nil {
		return nil, fmt.Errorf("%w: %d", ErrBackupNotFound, backupID)
	}
	if meta.Discovered {
		return nil, fmt.Errorf("%w: %d", ErrBackupDiscovered, backupID)
	}
	if meta.Source == "" {
		return nil, fmt.Errorf("backup %d has no source server", backupID)
	}

	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return nil, fmt.Errorf("source server %s not found", meta.Source)
	}
	if cluster.isBackupMetaInProgress(meta, server) {
		return nil, fmt.Errorf("%w: %d", ErrBackupInProgress, backupID)
	}
	if _, verifying := cluster.backupVerifying.Load(backupID); verifying {
		return nil, fmt.Errorf("%w: %d", ErrBackupVerificationRunning, backupID)
	}

	if meta.Dest != "" {
		backupDir := server.GetMyBackupDirectory()
		if !isPathWithinBase(backupDir, meta.Dest) {
			return nil, fmt.Errorf("backup path %s is outside %s", meta.Dest, backupDir)
		}
		if cluster.isBackupMetaInProgress(meta, server) {
			return nil, fmt.Errorf("%w: %d", ErrBackupInProgress, backupID)
		}
		var err error
		if isEncryptedBackupArtifact(meta.Dest) {
			err = cluster.removeBackupArtifactWithSidecar(meta.Dest)
		} else {
			err = os.RemoveAll(meta.Dest)
		}
		if err != nil {
			return nil, err
		}
	}
	metaPath := server.backupMetaFilePath(meta)
	if metaPath != "" {
		if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	cluster.BackupMetaMap.Delete(meta.Id)
	return meta, nil
}

func isSafeBackupToolName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return filepath.Base(name) == name
}

func isPathWithinBase(baseDir, target string) bool {
	if baseDir == "" || target == "" {
		return false
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}
