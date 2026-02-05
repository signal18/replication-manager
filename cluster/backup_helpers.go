// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
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
// Line and retention settings influence default vs ad-hoc handling.
// ResticEnabled can force enable/disable restic for this run.
// BackupID is used for ad-hoc metadata naming.
type BackupRunOptions struct {
	Line              string
	RetentionDays     int
	RetentionDuration string
	ResticEnabled     *bool
	BackupID          int64
}

var adhocMetaFilePattern = regexp.MustCompile(`\.(\d+)\.meta\.json$`)

func normalizeBackupLine(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "")
	switch normalized {
	case backupmgr.BackupLineDefault:
		return backupmgr.BackupLineDefault
	case backupmgr.BackupLineAdhoc:
		return backupmgr.BackupLineAdhoc
	default:
		return ""
	}
}

// resolveBackupLine normalizes backup line selection and enforces ad-hoc
// behavior when retention days are specified or when the server is not the
// backup source or master.
func (server *ServerMonitor) resolveBackupLine(opts BackupRunOptions) string {
	line := normalizeBackupLine(opts.Line)
	if opts.RetentionDays > 0 || hasRetentionDurationOpt(opts) {
		line = backupmgr.BackupLineAdhoc
	}
	if line == "" {
		line = backupmgr.BackupLineDefault
	}

	cluster := server.ClusterGroup
	if cluster == nil {
		return line
	}

	if line == backupmgr.BackupLineDefault {
		backupServer := cluster.GetBackupServer()
		master := cluster.GetMaster()
		isAllowed := backupServer == server || master == server
		if !isAllowed {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup request on %s treated as ad-hoc (not primary backup source or master)", server.URL)
			line = backupmgr.BackupLineAdhoc
		}
	}

	return line
}

func hasRetentionDurationOpt(opts BackupRunOptions) bool {
	if strings.TrimSpace(opts.RetentionDuration) == "" {
		return false
	}
	if d, ok := backupmgr.ParseRetentionDuration(opts.RetentionDuration); ok {
		return d > 0
	}
	return false
}

// shouldRunRestic decides whether restic should be used for a backup run.
// It respects cluster config and an optional per-run override.
func (server *ServerMonitor) shouldRunRestic(opts BackupRunOptions) bool {
	cluster := server.ClusterGroup
	if cluster == nil || !cluster.Conf.BackupRestic {
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

func (server *ServerMonitor) generateBackupSessionID(method backupmgr.BackupMethod, line string, ts time.Time) string {
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
	line = normalizeBackupLine(line)
	if line == "" {
		line = backupmgr.BackupLineDefault
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
	return fmt.Sprintf("%s-%s-%s-%s-%d-%s",
		sanitizeSessionComponent(clusterName),
		sanitizeSessionComponent(host),
		sanitizeSessionComponent(port),
		sanitizeSessionComponent(methodLabel),
		ts.Unix(),
		sanitizeSessionComponent(line),
	)
}

func (server *ServerMonitor) ensureBackupSessionID(meta *backupmgr.BackupMetadata, method backupmgr.BackupMethod, ts time.Time, line string) string {
	if server == nil || meta == nil {
		return ""
	}
	if method == 0 && meta.BackupMethod != 0 {
		method = meta.BackupMethod
	}
	if strings.TrimSpace(line) == "" {
		line = meta.BackupLine
	}
	if ts.IsZero() {
		if !meta.StartTime.IsZero() {
			ts = meta.StartTime
		} else if meta.Id > 0 {
			ts = time.Unix(meta.Id, 0)
		}
	}
	if meta.BackupSessionID == "" {
		meta.BackupSessionID = server.generateBackupSessionID(method, line, ts)
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

func (server *ServerMonitor) buildBackupMetaFileName(backupTool string, backupID int64, line string) string {
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
	if normalizeBackupLine(line) == backupmgr.BackupLineAdhoc && backupID > 0 {
		return filepath.Join(dir, fmt.Sprintf("%s.%d.meta.json", backupTool, backupID))
	}
	return filepath.Join(dir, fmt.Sprintf("%s.meta.json", backupTool))
}

func (server *ServerMonitor) backupMetaFilePath(meta *backupmgr.BackupMetadata) string {
	if meta == nil {
		return ""
	}
	line := meta.BackupLine
	if line == "" && hasRetentionPolicy(meta) {
		line = backupmgr.BackupLineAdhoc
	}
	defaultPath := server.buildBackupMetaFileName(meta.BackupTool, meta.Id, line)
	if meta.MetaFile != "" {
		metaFile := meta.MetaFile
		if normalizeBackupLine(line) != backupmgr.BackupLineAdhoc {
			defaultBase := filepath.Base(defaultPath)
			if defaultBase != "" && filepath.Base(metaFile) != defaultBase {
				if cluster := server.ClusterGroup; cluster != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Ignoring custom metadata filename %s for non-adhoc backup", metaFile)
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

func parseAdhocMetaFileID(name string) (int64, bool) {
	matches := adhocMetaFilePattern.FindStringSubmatch(name)
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
	// For adhoc files like "mysqldump.1234567890.meta.json", return "mysqldump"
	// For default files like "mysqldump.meta.json", return "mysqldump"
	idx := strings.LastIndex(trimmed, ".")
	if idx < 0 {
		// No dot found, so the trimmed string is the tool name (default case)
		return trimmed
	}
	// Dot found, return everything before it (adhoc case)
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

func (server *ServerMonitor) LoadAdhocBackupMetadata() ([]*backupmgr.BackupMetadata, error) {
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
		if !adhocMetaFilePattern.MatchString(name) {
			continue
		}

		path := filepath.Join(dir, name)
		meta, err := readBackupMetadataFile(path)
		if err != nil {
			readErrors++
			lastErr = err
			if cluster != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Failed reading ad-hoc backup metadata %s: %s", path, err)
			}
			continue
		}
		if meta == nil {
			continue
		}

		meta.BackupLine = backupmgr.BackupLineAdhoc
		if meta.Id == 0 {
			if id, ok := parseAdhocMetaFileID(name); ok {
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
		server.ensureBackupSessionID(meta, method, meta.StartTime, meta.BackupLine)

		if cluster != nil {
			cluster.BackupMetaMap.Set(meta.Id, meta)
		}
		metas = append(metas, meta)
	}

	if readErrors > 0 {
		return metas, fmt.Errorf("failed reading %d ad-hoc backup metadata file(s): %w", readErrors, lastErr)
	}

	return metas, nil
}

func (server *ServerMonitor) GetLatestMetaForLine(method, line string) (int64, *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if cluster == nil {
		return 0, nil
	}

	normalizedLine := normalizeBackupLine(line)
	applyLineFilter := normalizedLine != ""

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

		if applyLineFilter {
			if normalizedLine == backupmgr.BackupLineDefault && m.IsAdhoc() {
				valid = false
			}
			if normalizedLine == backupmgr.BackupLineAdhoc && !m.IsAdhoc() {
				valid = false
			}
		}

		if valid && latest < m.Id {
			latest = m.Id
			meta = m
		}

		return true
	})

	return latest, meta
}

func (cluster *Cluster) PurgeExpiredAdhocBackups() {
	if cluster == nil {
		return
	}

	// BackupMetaMap is backed by sync.Map, so Range/Delete are safe for concurrent use.
	// Purge operations intentionally rely on that thread-safety to avoid additional locks.

	now := time.Now()
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}

		metas, err := server.LoadAdhocBackupMetadata()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to load ad-hoc metadata on %s: %s", server.URL, err)
			continue
		}

		for _, meta := range metas {
			if meta == nil || !meta.IsAdhoc() || !meta.Completed {
				continue
			}
			deadline, ok := retentionDeadline(meta)
			if !ok || now.Before(deadline) {
				continue
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Purging expired ad-hoc backup %d on %s", meta.Id, server.URL)

			if meta.ResticSnapshotID != "" {
				if cluster.Conf.BackupRestic {
					if err := cluster.AddPurgeTask(meta.ResticSnapshotID); err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to purge restic snapshot %s on %s: %s", meta.ResticSnapshotID, server.URL, err)
					}
				} else {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Restic disabled; skip purging snapshot %s on %s", meta.ResticSnapshotID, server.URL)
				}
			}

			if meta.Dest != "" {
				backupDir := server.GetMyBackupDirectory()
				if !isPathWithinBase(backupDir, meta.Dest) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Skip removing ad-hoc backup path %s on %s: outside backup directory", meta.Dest, server.URL)
				} else if err := os.RemoveAll(meta.Dest); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed removing ad-hoc backup path %s on %s: %s", meta.Dest, server.URL, err)
				}
			}

			metaPath := server.backupMetaFilePath(meta)
			if metaPath != "" {
				if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed removing ad-hoc metadata %s on %s: %s", metaPath, server.URL, err)
				}
			}

			cluster.BackupMetaMap.Delete(meta.Id)
		}
	}
}

func retentionDeadline(meta *backupmgr.BackupMetadata) (time.Time, bool) {
	if meta == nil {
		return time.Time{}, false
	}
	duration, ok := meta.RetentionWindow()
	if !ok || duration <= 0 {
		return time.Time{}, false
	}

	base := meta.EndTime
	if base.IsZero() {
		base = meta.StartTime
	}
	if base.IsZero() && meta.Id > 0 {
		if isLikelyUnixTimestamp(meta.Id) {
			base = time.Unix(meta.Id, 0)
		}
	}
	if base.IsZero() {
		return time.Time{}, false
	}

	return base.Add(duration), true
}

func hasRetentionPolicy(meta *backupmgr.BackupMetadata) bool {
	if meta == nil {
		return false
	}
	if _, ok := meta.RetentionWindow(); ok {
		return true
	}
	return false
}

func isLikelyUnixTimestamp(value int64) bool {
	if value <= 0 {
		return false
	}
	// Avoid treating small sequential IDs as timestamps.
	minTimestamp := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	maxTimestamp := time.Now().Add(24 * time.Hour).Unix()
	return value >= minTimestamp && value <= maxTimestamp
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
