// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file finds the logical backups placed in a server's backup folder without metadata
// (copied there by hand or by another tool) and checks them: they are listed as candidates,
// with what the cheap checks found, and registered only by the operator, only when they are
// proper backups (backup_discovery_register.go). A registered one is never picked by an
// automatic restore: a copy's modification time is not the time of its data.

package cluster

import (
	"archive/tar"
	"bufio"
	stdgzip "compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/splitdump"
)

var (
	// the position lines of a dump header, from MariaDB, MySQL and Percona clients
	discoveryBinlogRegex = regexp.MustCompile(`CHANGE (?:REPLICATION SOURCE|MASTER) (?:'[^']*' )?TO (?:SOURCE|MASTER)_LOG_FILE='([^']+)', (?:SOURCE|MASTER)_LOG_POS=(\d+)`)
	discoveryGtidMariaDB = regexp.MustCompile(`gtid_slave_pos='([^']*)'`)
	discoveryGtidMySQL   = regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'\+'\*/\s*)?(?:/\*![0-9]*\s*'([^'+][^']*)'\*/|'([^'+][^']*)')`)
	// a continuation line of a GTID_PURGED statement: a GTID set part, the last one closing it
	discoveryGtidContinuation = regexp.MustCompile(`^[0-9A-Za-z_:,-]+(?:'\s*;)?$`)
	discoveryDumpComplete     = regexp.MustCompile(`^-- Dump completed(?: on (\d{4}-\d{2}-\d{2} +\d{1,2}:\d{2}:\d{2}))?`)
	// a table of a dump: the client writes its CREATE TABLE at the start of a line
	discoveryTableLine = regexp.MustCompile(`(?i)^CREATE TABLE\b`)
	// an account line: what an accounts or grants file holds (mysqldump --system=user, SHOW
	// GRANTS), the version comment of a client included
	discoveryAccountLine = regexp.MustCompile(`(?i)^(?:/\*[!M]?[0-9]*\s*)?(?:CREATE USER|CREATE ROLE|GRANT|ALTER USER|SET PASSWORD|SET DEFAULT ROLE)\b`)
	// the table file of a splitdump or mydumper folder: <db>.<table>-schema.sql[.gz|.zst]
	// (a database's own file is <db>-schema-create.sql, a view's <db>.<view>-schema-view.sql)
	discoveryTableFile = regexp.MustCompile(`^[^/]+\.[^/]+-schema\.sql(?:\.gz|\.zst)?$`)
	// mydumper's start and end lines in its metadata, "# " before them since 0.14.1
	discoveryMydumperTime = regexp.MustCompile(`(?m)^#?\s*(Started|Finished) dump at:\s*(\d{4}-\d{2}-\d{2} +\d{1,2}:\d{2}:\d{2})`)
)

// discoveryGtidStatementMax bounds a joined GTID_PURGED statement: 10000 sources with
// several intervals each are about 650 KB.
const discoveryGtidStatementMax = 1 << 20

// discoveryLinePrefixMax bounds what is kept of a dump line: the header and footer lines
// are short, a data line (an INSERT of a whole table) is not kept whole.
const discoveryLinePrefixMax = 4096

// backupCandidateReadMax bounds the cheap check of a dump file, decompressed: its first
// CREATE TABLE comes before its data. A package variable for the tests, which lower it.
var backupCandidateReadMax int64 = 64 << 20

// ErrBackupDiscoveryNotLogical: a reseed from a chosen backup takes a logical backup only.
var ErrBackupDiscoveryNotLogical = errors.New("reseed from a chosen backup takes a logical backup (mysqldump, splitdump or mydumper)")

// backupDiscoverySettle is how long an artifact must be left unchanged before it is checked
// or registered: one still being copied in is not read half written. A package variable for
// the tests, which shorten it; they restore it and do not run in parallel.
var backupDiscoverySettle = time.Minute

// backupDiscoveryMaxPerRun bounds the artifacts one discovery or one change check reads the
// content of (decrypted, decompressed): the others are left to the next one. A package
// variable for the tests, which shorten it; they restore it and do not run in parallel.
var backupDiscoveryMaxPerRun = 8

// ErrBackupDiscoveryRunning: a discovery, a Register or an Unregister of the server's
// folder already runs.
var ErrBackupDiscoveryRunning = errors.New("a discovery or a registration of the backup folder already runs")

// errBackupDiscoveryStopping: replication-manager stops, a read of a backup ends.
var errBackupDiscoveryStopping = errors.New("replication-manager is stopping")

// logDiscoveryPanic logs a panic the read of an artifact recovered, with its stack (4 KiB at
// most).
func (cluster *Cluster) logDiscoveryPanic(path string, r any) {
	stack := debug.Stack()
	if len(stack) > 4096 {
		stack = stack[:4096]
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Backup discovery: the read of %s panicked: %v\n%s", path, r, stack)
}

// ErrBackupDiscoveryInBackup: a backup of the cluster runs, its artifact is not complete yet.
var ErrBackupDiscoveryInBackup = errors.New("a backup of the cluster is running: discover its folder once it ends")

// The status of a candidate, what its cheap checks found.
const (
	BackupCandidateBackup     = "backup"     // holds a table: registerable
	BackupCandidateNotBackup  = "not-backup" // no table (accounts only, empty...): never registered
	BackupCandidateIncomplete = "incomplete" // a dump folder that did not finish: not registered
	BackupCandidateUnchecked  = "unchecked"  // the cheap checks can not tell: Register reads it
	BackupCandidateUnreadable = "unreadable" // can not be read (a key, a permission): Register retries
	BackupCandidateSettling   = "settling"   // changed in the last backupDiscoverySettle: still copied in?
)

// BackupCandidate is an artifact of a backup folder no metadata describes, as the last
// discovery checked it.
type BackupCandidate struct {
	Server    string    `json:"server"`
	Path      string    `json:"path"`
	Name      string    `json:"name"`
	Tool      string    `json:"tool"`
	SplitDump bool      `json:"splitDump"`
	Encrypted bool      `json:"encrypted"`
	Size      int64     `json:"size"`
	Files     int       `json:"files"`
	ModTime   time.Time `json:"modTime"`
	Status    string    `json:"status"`
	Reason    string    `json:"reason,omitempty"`
	// Registering and RegisterError: the last Register of this artifact, running or refused
	Registering   bool   `json:"registering,omitempty"`
	RegisterError string `json:"registerError,omitempty"`
	// Scheduled: a scheduled backup file (mysqldump.sql.gz, splitdump...): registered, it is
	// the scheduled backup, used as any scheduled backup
	Scheduled bool `json:"scheduled,omitempty"`
	// deferred: not checked, left to the next discovery by backupDiscoveryMaxPerRun
	deferred bool
}

// Registerable tells whether Register may be asked for the candidate: the full read decides.
func (c *BackupCandidate) Registerable() bool {
	switch c.Status {
	case BackupCandidateBackup, BackupCandidateUnchecked, BackupCandidateUnreadable:
		return true
	}
	return false
}

// backupCandidateSet is the candidates of a server's last discovery, replaced whole.
type backupCandidateSet struct {
	Server     string            `json:"server"`
	Candidates []BackupCandidate `json:"candidates"`
	// Dropped: the artifacts the last discovery did not list, beyond
	// backup-discovery-candidate-max (WARN0250)
	Dropped   int       `json:"dropped"`
	ScannedAt time.Time `json:"scannedAt"`
}

// backupDiscoveryCandidateMax is backup-discovery-candidate-max, its default when unset.
func (cluster *Cluster) backupDiscoveryCandidateMax() int {
	if n := cluster.Conf.BackupDiscoveryCandidateMax; n > 0 {
		return n
	}
	return config.ConstBackupDiscoveryCandidateMax
}

// DiscoverBackups lists the logical backups of the server's backup folder that no metadata
// describes as candidates, with their cheap checks, and loads the ad-hoc metadata files of
// the folder. It registers nothing. A discovered backup whose artifact has gone is
// unregistered. One discovery runs at a time per server.
func (server *ServerMonitor) DiscoverBackups() ([]BackupCandidate, error) {
	return server.discoverBackups(false)
}

// discoverBackups is DiscoverBackups; retry checks again the candidates whose artifact did not
// change too (the discovery the operator requests: a key installed since can read one now).
func (server *ServerMonitor) discoverBackups(retry bool) ([]BackupCandidate, error) {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return nil, nil
	}
	dir := server.GetMyBackupDirectory()
	// ad-hoc backups (and the backups registered before) have one metadata file each: read
	// even while a backup runs (a live entry is kept), so a server monitor created then (a
	// config reload) lists them at once
	if _, err := server.LoadAdhocBackupMetadata(); err != nil && !os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: ad-hoc metadata not all read: %s", server.URL, err)
	}
	// a backup running writes its artifact: not checked half written (an archive push only
	// reads the artifacts with metadata, which a discovery passes over)
	if cluster.IsInBackupJob() {
		return nil, ErrBackupDiscoveryInBackup
	}
	if _, running := cluster.backupDiscoveries.LoadOrStore(server.URL, struct{}{}); running {
		return nil, ErrBackupDiscoveryRunning
	}
	defer cluster.backupDiscoveries.Delete(server.URL)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			cluster.backupCandidates.Delete(server.URL)
			return nil, nil
		}
		return nil, fmt.Errorf("read backup folder %s: %w", dir, err)
	}
	server.unregisterGoneDiscoveredBackups()
	known := server.knownBackupArtifactsOnDisk(entries)
	previous := map[string]BackupCandidate{}
	if set := cluster.backupCandidateSet(server.URL); set != nil {
		for _, c := range set.Candidates {
			previous[c.Path] = c
		}
	}
	budget := backupDiscoveryMaxPerRun
	settled := time.Now().Add(-backupDiscoverySettle)
	var candidates []BackupCandidate
	left := 0
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if known[path] {
			continue
		}
		tool, split, ok := classifyBackupArtifact(path, entry)
		if !ok {
			continue
		}
		c := BackupCandidate{Server: server.URL, Path: path, Name: entry.Name(), Tool: tool, SplitDump: split, Encrypted: isEncryptedBackupArtifact(path)}
		fp, err := restoreArtifactFingerprintBounded(path, backupProofWalkMax)
		switch {
		case errors.Is(err, errArtifactWalkBudget):
			c.Status, c.Reason = BackupCandidateUnchecked, fmt.Sprintf("more than %d files: its changes can not be tracked, it can not be registered", backupProofWalkMax)
		case err != nil:
			c.Status, c.Reason = BackupCandidateUnreadable, fmt.Sprintf("can not be read: %s", err)
		}
		if fp != nil {
			c.Size, c.Files, c.ModTime = fp.Size, fp.Files, fp.ModTime
		}
		// a scheduled backup file no record describes (replaced by hand, or left by an
		// earlier mode): registered, it becomes the scheduled backup
		c.Scheduled = server.isScheduledBackupArtifact(path)
		if c.Status == "" {
			// a dump file's check reads its content (bounded by budget); a folder's, its file
			// names and small files; an encrypted folder archive's, nothing
			readsContent := !split && tool != config.ConstBackupLogicalTypeMydumper
			p, seen := previous[c.Path]
			switch {
			case c.ModTime.After(settled):
				c.Status, c.Reason = BackupCandidateSettling, fmt.Sprintf("changed in the last %s: still being copied in? Checked once it settles", backupDiscoverySettle)
			case seen && !retry && !p.deferred && p.Status != BackupCandidateSettling && p.Size == c.Size && p.Files == c.Files && p.ModTime.Equal(c.ModTime):
				c.Status, c.Reason, c.Tool, c.SplitDump = p.Status, p.Reason, p.Tool, p.SplitDump
			case budget <= 0 && readsContent:
				c.Status, c.Reason, c.deferred = BackupCandidateUnchecked, "not checked yet: left to the next discovery", true
				left++
			default:
				if readsContent {
					budget--
				}
				c.Status, c.Reason, c.Tool, c.SplitDump = server.ClusterGroup.checkBackupCandidate(path, tool, split)
			}
		}
		candidates = append(candidates, c)
	}
	if left > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: %d more artifact(s) left to the next discovery (%d read per discovery)", server.URL, left, backupDiscoveryMaxPerRun)
	}
	// the newest first: beyond the bound the oldest are dropped, into a fresh array
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].ModTime.After(candidates[j].ModTime) })
	dropped := 0
	if max := cluster.backupDiscoveryCandidateMax(); len(candidates) > max {
		dropped = len(candidates) - max
		candidates = append([]BackupCandidate(nil), candidates[:max]...)
	}
	cluster.backupCandidates.Store(server.URL, &backupCandidateSet{Server: server.URL, Candidates: candidates, Dropped: dropped, ScannedAt: time.Now()})
	cluster.pruneBackupRegisterResults()
	return candidates, nil
}

// IsBackupDiscoveryRunning tells whether a discovery, a Register or an Unregister of the
// server's backup folder runs.
func (cluster *Cluster) IsBackupDiscoveryRunning(serverURL string) bool {
	_, running := cluster.backupDiscoveries.Load(serverURL)
	return running
}

// backupCandidateSet returns the server's candidates of its last discovery, nil before one.
func (cluster *Cluster) backupCandidateSet(serverURL string) *backupCandidateSet {
	if v, ok := cluster.backupCandidates.Load(serverURL); ok {
		return v.(*backupCandidateSet)
	}
	return nil
}

// findBackupCandidate returns the candidate of the server's last discovery at path.
func (cluster *Cluster) findBackupCandidate(serverURL, path string) (BackupCandidate, bool) {
	if set := cluster.backupCandidateSet(serverURL); set != nil {
		for _, c := range set.Candidates {
			if c.Path == path {
				return c, true
			}
		}
	}
	return BackupCandidate{}, false
}

// dropBackupCandidate removes path from the server's candidates: it was just registered.
func (cluster *Cluster) dropBackupCandidate(serverURL, path string) {
	set := cluster.backupCandidateSet(serverURL)
	if set == nil {
		return
	}
	kept := make([]BackupCandidate, 0, len(set.Candidates))
	for _, c := range set.Candidates {
		if c.Path != path {
			kept = append(kept, c)
		}
	}
	cluster.backupCandidates.Store(serverURL, &backupCandidateSet{Server: set.Server, Candidates: kept, Dropped: set.Dropped, ScannedAt: set.ScannedAt})
}

// unregisterGoneDiscoveredBackups unregisters the server's discovered backups whose artifact
// has gone: their metadata file (replication-manager's own) is removed, the discovered entries
// never outlive their artifacts. A discovered record of a scheduled backup file is removed too
// (removeScheduledBackupRecord).
func (server *ServerMonitor) unregisterGoneDiscoveredBackups() {
	cluster := server.ClusterGroup
	var discovered []*backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Discovered && meta.Source == server.URL && !meta.OriginalRemoved {
			discovered = append(discovered, meta)
		}
		return true
	})
	for _, meta := range discovered {
		if cluster.isDuplicateScheduledBackup(meta) {
			server.removeScheduledBackupRecord(meta)
			continue
		}
		if _, err := os.Stat(meta.Dest); !os.IsNotExist(err) {
			continue
		}
		if err := os.Remove(server.backupMetaFilePath(meta)); err != nil && !os.IsNotExist(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: metadata of backup %d not removed: %s", server.URL, meta.Id, err)
			continue
		}
		cluster.BackupMetaMap.Delete(meta.Id)
		cluster.backupChanges.Delete(meta.Id)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: discovered backup %d unregistered, its artifact %s is gone", server.URL, meta.Id, meta.Dest)
	}
}

// knownBackupArtifactsOnDisk returns the artifacts the registered backups use, and those the
// metadata files of the folder describe: a metadata file not loaded (a scheduled backup's
// without its cookie) still describes its artifact, which is not a candidate.
func (server *ServerMonitor) knownBackupArtifactsOnDisk(entries []os.DirEntry) map[string]bool {
	known := server.ClusterGroup.knownBackupArtifacts()
	dir := server.GetMyBackupDirectory()
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		if meta, err := readBackupMetadataFile(filepath.Join(dir, entry.Name())); err == nil && meta != nil {
			addKnownBackupArtifact(known, meta.Dest)
		}
	}
	return known
}

// StartBackupDiscovery runs the discovery on every server of the cluster in the background,
// not while a backup of the cluster runs. It registers nothing: the candidates are listed.
func (cluster *Cluster) StartBackupDiscovery() error {
	if cluster.IsInBackupJob() {
		return ErrBackupDiscoveryInBackup
	}
	servers := append([]*ServerMonitor(nil), cluster.Servers...)
	go func() {
		found := 0
		for _, server := range servers {
			if server == nil {
				continue
			}
			candidates, err := server.discoverBackups(true)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: %s", server.URL, err)
			}
			found += len(candidates)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery of cluster %s ended: %d artifact(s) found in the backup folders", cluster.Name, found)
	}()
	return nil
}

// knownBackupArtifacts returns the paths the registered backups use: their artifact and its
// encrypted forms, which a discovery skips.
func (cluster *Cluster) knownBackupArtifacts() map[string]bool {
	known := map[string]bool{}
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil {
			addKnownBackupArtifact(known, meta.Dest)
		}
		return true
	})
	return known
}

// addKnownBackupArtifact records dest, a backup's artifact, and its encrypted forms.
func addKnownBackupArtifact(known map[string]bool, dest string) {
	if strings.TrimSpace(dest) == "" {
		return
	}
	dest = filepath.Clean(dest)
	for _, suffix := range []string{".tar.enc", ".enc"} {
		dest = strings.TrimSuffix(dest, suffix)
	}
	for _, p := range []string{dest, dest + ".enc", dest + ".tar.enc"} {
		known[p] = true
	}
}

// classifyBackupArtifact tells whether an entry of a backup folder may be a logical backup,
// and which: a mysqldump file (.sql, .sql.gz, encrypted .enc), a splitdump folder (or its
// encrypted .tar.enc archive), a mydumper folder. A splitdump folder has its manifest, or a
// metadata file without mydumper's "Started dump at" line; splitdump writes mydumper's file
// names, so its name or contents decide, not its file names. Whether it is a backup is the
// checks' to tell.
func classifyBackupArtifact(path string, entry os.DirEntry) (tool string, split, ok bool) {
	name := entry.Name()
	// a symbolic link may point outside the backup folder: never read
	if entry.Type()&os.ModeSymlink != 0 {
		return "", false, false
	}
	// replication-manager's own working folder of the direct reseeds (srv_job_reseed.go),
	// and the users file a backup with backup-split-mysql-user writes next to its dump
	userSidecar := filepath.Base(mysqldumpUserSidecarPath(path))
	if name == directReseedSystemArtifactSubdir || name == userSidecar || name == userSidecar+".enc" {
		return "", false, false
	}
	// a backup being written or kept aside by replication-manager: staging, previous copy
	for _, suffix := range []string{".partial", ".tmp", ".new", ".old", ".err"} {
		if strings.HasSuffix(name, suffix) {
			return "", false, false
		}
	}
	if !entry.IsDir() {
		switch {
		case strings.HasSuffix(name, ".tar.enc"):
			if strings.HasPrefix(name, config.ConstBackupLogicalTypeMydumper) {
				return config.ConstBackupLogicalTypeMydumper, false, true
			}
			return config.ConstBackupLogicalTypeMysqldump, true, true
		case strings.HasSuffix(name, ".sql"), strings.HasSuffix(name, ".sql.gz"),
			strings.HasSuffix(name, ".sql.enc"), strings.HasSuffix(name, ".sql.gz.enc"):
			return config.ConstBackupLogicalTypeMysqldump, false, true
		}
		return "", false, false
	}
	if fileExists(filepath.Join(path, "manifest")) {
		return config.ConstBackupLogicalTypeMysqldump, true, true
	}
	if data, err := readDiscoverySmallFile(filepath.Join(path, "metadata")); err == nil {
		if strings.Contains(string(data), "Started dump at") {
			return config.ConstBackupLogicalTypeMydumper, false, true
		}
		return config.ConstBackupLogicalTypeMysqldump, true, true
	}
	if isSplit, err := splitdump.Detect(path); err == nil && isSplit && isSplitDumpName(path) {
		return config.ConstBackupLogicalTypeMysqldump, true, true
	}
	return "", false, false
}

// checkBackupCandidate runs the cheap checks of an artifact: a dump file is read up to its
// first CREATE TABLE (backupCandidateReadMax at most), a folder's file names and its small
// manifest or metadata files are read. An encrypted folder archive is not read: Register
// reads it to its end. It returns the candidate's status and why, and its tool, which a
// folder's content may correct.
func (cluster *Cluster) checkBackupCandidate(path, tool string, split bool) (status, reason, toolOut string, splitOut bool) {
	// the check parses untrusted content: a panic marks the candidate unreadable, never the
	// monitor
	defer func() {
		if r := recover(); r != nil {
			status, reason, toolOut, splitOut = BackupCandidateUnreadable, fmt.Sprintf("can not be read: internal error: %v", r), tool, split
			cluster.logDiscoveryPanic(path, r)
		}
	}()
	if strings.HasSuffix(path, ".tar.enc") {
		return BackupCandidateUnchecked, "an encrypted dump folder: Register reads it to its end", tool, split
	}
	if !split && tool != config.ConstBackupLogicalTypeMydumper {
		facts := &discoveredDumpFacts{stopAtTable: true, readMax: backupCandidateReadMax}
		if err := cluster.readDiscoveredStream(path, facts); err != nil {
			return BackupCandidateUnreadable, fmt.Sprintf("can not be read: %s", err), tool, split
		}
		if facts.table {
			return BackupCandidateBackup, "", tool, split
		}
		if facts.truncated {
			return BackupCandidateUnchecked, fmt.Sprintf("no CREATE TABLE in its first %s: Register reads it to its end", humanBytes(backupCandidateReadMax)), tool, split
		}
		return BackupCandidateNotBackup, facts.notABackupReason(), tool, split
	}
	names, err := discoveryFolderNames(path)
	if err != nil {
		return BackupCandidateUnreadable, fmt.Sprintf("can not be read: %s", err), tool, split
	}
	read := func(name string) ([]byte, error) { return readDiscoverySmallFile(filepath.Join(path, name)) }
	meta := &backupmgr.BackupMetadata{BackupTool: tool, SplitDump: split, Completed: true}
	var why string
	inspectDiscoveredFolder(meta, read, names, func(format string, args ...any) {
		meta.Completed = false
		why = fmt.Sprintf(format, args...)
	})
	if !meta.Completed {
		if meta.NotABackup {
			return BackupCandidateNotBackup, why, tool, split
		}
		return BackupCandidateIncomplete, why, tool, split
	}
	return BackupCandidateBackup, "", tool, split
}

// discoveryFolderNames returns the names of the files of a dump folder: a splitdump or
// mydumper folder is flat.
func discoveryFolderNames(dir string) (map[string]bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		// a symbolic link may point outside the backup folder: the folder is not read
		if e.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("it holds a symbolic link (%s): not read", e.Name())
		}
		if !e.IsDir() {
			names[e.Name()] = true
		}
	}
	return names, nil
}

func describeSplit(split bool) string {
	if split {
		return "splitdump"
	}
	return "single artifact"
}

// inspectDiscoveredBackup reads the artifact to its end as a restore would and records what
// it found: its position, its dump time and where it comes from, whether it is a backup
// (a table) and complete. One that is not, or can not be read (damaged, or encrypted with a
// key this replication-manager does not hold), is left not completed, the reason in its
// DiscoveryNote: Register refuses it.
func (server *ServerMonitor) inspectDiscoveredBackup(meta *backupmgr.BackupMetadata) {
	fail := func(format string, args ...any) {
		meta.Completed = false
		meta.DiscoveryNote = fmt.Sprintf(format, args...)
	}
	// the read parses untrusted content: a panic fails this registration, never the monitor
	defer func() {
		if r := recover(); r != nil {
			fail("can not be read: internal error: %v", r)
			server.ClusterGroup.logDiscoveryPanic(meta.Dest, r)
		}
	}()
	switch {
	case strings.HasSuffix(meta.Dest, ".tar.enc"):
		// an encrypted folder: its archive is read to its end (its integrity code is
		// checked, which a foreign key fails), its members listed and its manifest and
		// metadata kept in memory, then the folder's own checks run on them
		archive, err := server.ClusterGroup.readDiscoveredArchive(meta.Dest)
		if err != nil {
			fail("can not be read: %s", err)
			return
		}
		archive.classify(meta)
		inspectDiscoveredFolder(meta, archive.read, archive.names, fail)
	case meta.SplitDump || meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		dir := meta.Dest
		names, err := discoveryFolderNames(dir)
		if err != nil {
			fail("can not be read: %s", err)
			return
		}
		read := func(name string) ([]byte, error) { return readDiscoverySmallFile(filepath.Join(dir, name)) }
		inspectDiscoveredFolder(meta, read, names, fail)
		// a plain splitdump folder is read to its end as a backup verification reads it:
		// every file the manifest lists, none unlisted
		if meta.Completed && meta.SplitDump {
			if err := validateSplitDumpBackup(dir); err != nil {
				fail("%s", err)
			}
		}
	default:
		server.inspectDiscoveredDump(meta, fail)
	}
	// without a position a reseed would start replication at the primary's current position,
	// and the replica would miss what was written since the dump: listed, refused by a reseed
	if meta.Completed && !hasReplicationPosition(meta) {
		note := "no replication position: a replica can not be reseeded from it"
		if meta.DiscoveryNote != "" {
			note = meta.DiscoveryNote + "; " + note
		}
		meta.DiscoveryNote = note
	}
}

// hasReplicationPosition reports whether meta holds the position of its dump: a binary log
// file or a GTID set.
func hasReplicationPosition(meta *backupmgr.BackupMetadata) bool {
	return strings.TrimSpace(meta.BinLogFileName) != "" || strings.TrimSpace(meta.BinLogGtid) != ""
}

// inspectDiscoveredFolder checks a splitdump or mydumper folder through read (a file's
// content) and names (its files): a plain folder, or what an encrypted archive held
// (readDiscoveredArchive). A folder without a table file is not a backup (NotABackup set).
func inspectDiscoveredFolder(meta *backupmgr.BackupMetadata, read func(string) ([]byte, error), names map[string]bool, fail func(string, ...any)) {
	switch {
	case meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		data, err := read("metadata")
		if err != nil {
			fail("no mydumper metadata: %s", err)
			return
		}
		if !strings.Contains(string(data), "Finished dump at") {
			fail("mydumper metadata has no \"Finished dump at\" line: the dump did not finish")
			return
		}
		meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = parseDiscoveredMyDumperPosition(data)
		if start, end := parseDiscoveredMyDumperTimes(data); !end.IsZero() {
			if start.IsZero() {
				start = end
			}
			meta.StartTime, meta.EndTime, meta.TimeSource = start, end, backupmgr.BackupTimeSourceMydumperMetadata
		}
		if !hasDiscoveryTableFile(namesOf(names)) {
			meta.NotABackup = true
			fail("no table file (<db>.<table>-schema.sql): its dump excluded every table, not a backup")
		}
	case meta.SplitDump:
		if data, err := read("metadata"); err == nil {
			if m, err := splitdump.ParseMetadata(data); err == nil && m != nil {
				meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = m.File, m.Position, m.GTID
			}
		}
		// splitdump writes its manifest last (every splitdump since 3.1.24 has one): without
		// it, the dump or its copy may not have ended
		data, err := read("manifest")
		if errors.Is(err, os.ErrNotExist) {
			fail("no manifest: an incomplete dump or copy, or a splitdump written before 3.1.24; its files can not be checked")
			return
		}
		var m *splitdump.Manifest
		if err == nil {
			if m, err = splitdump.ParseManifest(data); err == nil {
				err = splitdump.ValidateManifest(m)
			}
		}
		if err != nil {
			fail("manifest: %s", err)
			return
		}
		listed := map[string]bool{"manifest": true, "metadata": true}
		for _, files := range [][]string{m.Schema, m.Data, m.Post} {
			for _, f := range files {
				listed[f] = true
				if !names[f] {
					fail("the manifest lists %s, missing", f)
					return
				}
			}
		}
		// a dump file the manifest does not list: a shard splitdump did not record, or a
		// file added since
		for _, name := range namesOf(names) {
			if !listed[name] && (strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".sql.gz")) {
				fail("%s is not in the manifest", name)
				return
			}
		}
		if !hasDiscoveryTableFile(m.Schema) {
			meta.NotABackup = true
			fail("no table file in the manifest (<db>.<table>-schema.sql): its dump excluded every table, not a backup")
		}
	}
}

// namesOf returns the names of a set, sorted.
func namesOf(names map[string]bool) []string {
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// hasDiscoveryTableFile tells whether names hold the file of a table.
func hasDiscoveryTableFile(names []string) bool {
	for _, n := range names {
		if discoveryTableFile.MatchString(n) {
			return true
		}
	}
	return false
}

// Both forms of mydumper's metadata, as the reseed reads them (JobParseMyDumperMetaNew,
// JobParseMyDumperMetaOld): "File = " / "SOURCE_LOG_FILE = " and their position and GTID
// lines since 0.14.1, an indented "Log: " / "Pos: " / "GTID:" before.
var (
	reMyDumperFile = regexp.MustCompile(`(?i)^(?:File|SOURCE_LOG_FILE)\s*=\s*"?(.+?)"?$`)
	reMyDumperPos  = regexp.MustCompile(`(?i)^(?:Position|SOURCE_LOG_POS)\s*=\s*(\d+)`)
	reMyDumperGtid = regexp.MustCompile(`(?i)^executed_gtid_set\s*=\s*"?(.+?)"?$`)
	reMyDumperOld  = regexp.MustCompile(`^(Log|Pos|GTID):\s*(.*)$`)
)

// parseDiscoveredMyDumperPosition returns the first binary log file, position and GTID set
// mydumper's metadata holds.
func parseDiscoveredMyDumperPosition(data []byte) (file string, pos uint64, gtid string) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if m := reMyDumperOld.FindStringSubmatch(line); m != nil {
			switch {
			case m[1] == "Log" && file == "":
				file = m[2]
			case m[1] == "Pos" && pos == 0:
				pos, _ = strconv.ParseUint(m[2], 10, 64)
			case m[1] == "GTID" && gtid == "":
				gtid = m[2]
			}
		} else if m := reMyDumperFile.FindStringSubmatch(line); m != nil && file == "" {
			file = m[1]
		} else if m := reMyDumperPos.FindStringSubmatch(line); m != nil && pos == 0 {
			pos, _ = strconv.ParseUint(m[1], 10, 64)
		} else if m := reMyDumperGtid.FindStringSubmatch(line); m != nil && gtid == "" {
			gtid = m[1]
		}
	}
	return file, pos, gtid
}

// parseDiscoveredMyDumperTimes returns the start and end of the dump mydumper's metadata
// records, zero when it does not. The times have no zone: the dump server's local time,
// read in replication-manager's.
func parseDiscoveredMyDumperTimes(data []byte) (start, end time.Time) {
	for _, m := range discoveryMydumperTime.FindAllStringSubmatch(string(data), -1) {
		t, err := parseDiscoveryTime(m[2])
		if err != nil {
			continue
		}
		if m[1] == "Started" && start.IsZero() {
			start = t
		} else if m[1] == "Finished" && end.IsZero() {
			end = t
		}
	}
	return start, end
}

// parseDiscoveryTime reads a dump's "2006-01-02 15:04:05" time, in the local time zone.
func parseDiscoveryTime(s string) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", strings.Join(strings.Fields(s), " "), time.Local)
}

// discoveryArchiveSmallFile bounds the manifest and metadata files read from an archive or a
// folder.
const discoveryArchiveSmallFile = 16 << 20

// readDiscoverySmallFile reads a manifest or metadata file of a folder, discoveryArchiveSmallFile
// at most: a larger one is no backup's.
func readDiscoverySmallFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, discoveryArchiveSmallFile+1))
	if err != nil {
		return nil, err
	}
	if len(data) > discoveryArchiveSmallFile {
		return nil, fmt.Errorf("%s is larger than %d bytes", filepath.Base(path), discoveryArchiveSmallFile)
	}
	return data, nil
}

// discoveredArchive is what the read of an encrypted folder archive kept, in memory: its
// members' names and its manifest and metadata files. A splitdump or mydumper folder is flat:
// a member is known by its base name (a nested one would share it, harmless for the checks).
type discoveredArchive struct {
	names map[string]bool
	small map[string][]byte
}

func (a *discoveredArchive) read(name string) ([]byte, error) {
	if data, ok := a.small[name]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func (a *discoveredArchive) exists(name string) bool { return a.names[name] }

// classify sets what the archive holds from its content, as classifyBackupArtifact does for a
// folder: a splitdump with its manifest, mydumper with mydumper's metadata ("Started dump at"),
// a splitdump with another metadata. Its name, which decided before it was read, is kept for
// an archive with neither.
func (a *discoveredArchive) classify(meta *backupmgr.BackupMetadata) {
	switch data, err := a.read("metadata"); {
	case a.exists("manifest"):
		meta.BackupTool, meta.SplitDump = config.ConstBackupLogicalTypeMysqldump, true
	case err == nil && strings.Contains(string(data), "Started dump at"):
		meta.BackupTool, meta.SplitDump = config.ConstBackupLogicalTypeMydumper, false
	case err == nil:
		meta.BackupTool, meta.SplitDump = config.ConstBackupLogicalTypeMysqldump, true
	}
}

// discoveryArchiveMaxMembers bounds the member names an archive's read keeps: an archive with
// more is not completed. A package variable for the tests, which lower it.
var discoveryArchiveMaxMembers = 500000

// readDiscoveredArchive reads the encrypted folder archive at path to its end (its integrity
// is checked, a foreign key fails it) and keeps its members' names (discoveryArchiveMaxMembers
// at most) and its manifest and metadata files: nothing is written to disk.
func (cluster *Cluster) readDiscoveredArchive(path string) (*discoveredArchive, error) {
	rc, _, err := cluster.openRestoreArtifactStream(path)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	a := &discoveredArchive{names: map[string]bool{}, small: map[string][]byte{}}
	tr := tar.NewReader(rc)
	for {
		if cluster.exit.Load() {
			return nil, errBackupDiscoveryStopping
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			return a, nil
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(hdr.Name)
		if !a.names[name] && len(a.names) >= discoveryArchiveMaxMembers {
			return nil, fmt.Errorf("more than %d files", discoveryArchiveMaxMembers)
		}
		a.names[name] = true
		if name == "manifest" || name == "metadata" {
			data, err := io.ReadAll(io.LimitReader(tr, discoveryArchiveSmallFile))
			if err != nil {
				return nil, err
			}
			a.small[name] = data
		}
		if _, err := io.Copy(io.Discard, tr); err != nil { // the rest is read: its integrity
			return nil, err
		}
	}
}

// inspectDiscoveredDump checks a mysqldump file: it holds a table, and its footer when it
// has the client's header.
func (server *ServerMonitor) inspectDiscoveredDump(meta *backupmgr.BackupMetadata, fail func(string, ...any)) {
	facts := &discoveredDumpFacts{}
	if err := server.ClusterGroup.readDiscoveredStream(meta.Dest, facts); err != nil {
		fail("can not be read: %s", err)
		return
	}
	meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = facts.file, facts.pos, facts.gtid
	if !facts.completedAt.IsZero() {
		meta.StartTime, meta.EndTime, meta.TimeSource = facts.completedAt, facts.completedAt, backupmgr.BackupTimeSourceDumpFooter
	}
	if !facts.table {
		meta.NotABackup = true
		fail("%s", facts.notABackupReason())
		return
	}
	if !facts.header {
		meta.DiscoveryNote = "no \"-- MySQL dump\" / \"-- MariaDB dump\" header (not a dump client's file, or written with --skip-comments): its completion is not checked"
	}
	if facts.header && !facts.completed {
		fail("no \"-- Dump completed\" line: the dump did not finish")
	}
}

// discoveredDumpFacts is what the read of a mysqldump file found.
type discoveredDumpFacts struct {
	// stopAtTable ends the read at the first CREATE TABLE, readMax (if not 0) after that many
	// bytes decompressed (truncated): the cheap check of a candidate
	stopAtTable bool
	readMax     int64
	truncated   bool

	header      bool // the client's "-- MySQL dump" / "-- MariaDB dump" header
	completed   bool // its last line is the client's "-- Dump completed" footer
	completedAt time.Time
	table       bool // a CREATE TABLE
	accounts    bool // an account line: CREATE USER, GRANT...
	lines       bool // a line other than blank
	file        string
	pos         uint64
	gtid        string
}

// notABackupReason says why a dump without a table is not a backup.
func (f *discoveredDumpFacts) notABackupReason() string {
	switch {
	case !f.lines:
		return "empty: not a backup"
	case f.accounts:
		return "accounts only (CREATE USER / GRANT, no table): not a backup"
	}
	return "no CREATE TABLE: not a backup"
}

// discoveryCountingReader counts the bytes read through it.
type discoveryCountingReader struct {
	r io.Reader
	n int64
}

func (c *discoveryCountingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// readDiscoveredStream reads the mysqldump file at path as a restore reads it (decrypted,
// decompressed), to its end unless facts asks to stop earlier, and fills facts.
func (cluster *Cluster) readDiscoveredStream(path string, facts *discoveredDumpFacts) error {
	rc, _, err := cluster.openRestoreArtifactStream(path)
	if err != nil {
		return err
	}
	defer rc.Close()
	br := bufio.NewReaderSize(rc, 64*1024)
	var r io.Reader = br
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := stdgzip.NewReader(br)
		if err != nil {
			return err
		}
		defer zr.Close()
		r = zr
	}
	counted := &discoveryCountingReader{r: r}
	lr := bufio.NewReaderSize(counted, 64*1024)
	var last string
	// a GTID_PURGED statement the client wrote over several lines (one source per line): its
	// lines joined up to the end of the statement, as the backup's own reader joins them
	var gtidStmt strings.Builder
	for {
		if cluster.exit.Load() {
			return errBackupDiscoveryStopping
		}
		line, err := readDiscoveryLine(lr)
		if line != "" {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				last = trimmed
				facts.lines = true
			}
			if gtidStmt.Len() > 0 {
				if !discoveryGtidContinuation.MatchString(trimmed) || gtidStmt.Len()+len(trimmed) > discoveryGtidStatementMax {
					gtidStmt.Reset() // not a GTID set part: the statement is dropped
				} else {
					gtidStmt.WriteString(trimmed)
					if strings.HasSuffix(trimmed, ";") {
						if m := discoveryGtidMySQL.FindStringSubmatch(gtidStmt.String()); m != nil && facts.gtid == "" {
							facts.gtid = m[1] + m[2]
						}
						gtidStmt.Reset()
					}
					continue
				}
			}
			// the client's own lines only: a data line can hold any text. MariaDB writes
			// its gtid_slave_pos after the data, the other position lines come first
			stmt := strings.TrimPrefix(trimmed, "-- ")
			switch {
			case strings.HasPrefix(trimmed, "-- MySQL dump"), strings.HasPrefix(trimmed, "-- MariaDB dump"):
				facts.header = true
			case discoveryTableLine.MatchString(trimmed):
				facts.table = true
				if facts.stopAtTable {
					return nil
				}
			case discoveryAccountLine.MatchString(trimmed):
				facts.accounts = true
			case strings.HasPrefix(stmt, "CHANGE MASTER"), strings.HasPrefix(stmt, "CHANGE REPLICATION SOURCE"):
				if m := discoveryBinlogRegex.FindStringSubmatch(stmt); m != nil && facts.file == "" {
					facts.file = m[1]
					facts.pos, _ = strconv.ParseUint(m[2], 10, 64)
				}
			case strings.HasPrefix(stmt, "SET GLOBAL gtid_slave_pos"):
				if m := discoveryGtidMariaDB.FindStringSubmatch(stmt); m != nil && facts.gtid == "" {
					facts.gtid = m[1]
				}
			case strings.HasPrefix(stmt, "SET @@GLOBAL.GTID_PURGED"), strings.HasPrefix(stmt, "/*!") && strings.Contains(stmt, "GTID_PURGED"):
				if !strings.HasSuffix(stmt, ";") && facts.gtid == "" {
					gtidStmt.WriteString(stmt) // continued on the next lines
					break
				}
				if m := discoveryGtidMySQL.FindStringSubmatch(stmt); m != nil && facts.gtid == "" {
					facts.gtid = m[1] + m[2]
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if facts.readMax > 0 && counted.n > facts.readMax {
			facts.truncated = true
			return nil
		}
	}
	if m := discoveryDumpComplete.FindStringSubmatch(last); m != nil {
		facts.completed = true
		if m[1] != "" {
			if t, err := parseDiscoveryTime(m[1]); err == nil {
				facts.completedAt = t
			}
		}
	}
	return nil
}

// readDiscoveryLine reads one line, keeping its first discoveryLinePrefixMax bytes.
func readDiscoveryLine(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if room := discoveryLinePrefixMax - sb.Len(); room > 0 {
			if len(chunk) > room {
				sb.Write(chunk[:room])
			} else {
				sb.Write(chunk)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return sb.String(), err
	}
}

// ReseedFromBackupID reseeds server from the logical backup id, a backup the operator
// chose (a registered discovered one included): its artifact is restored and replication
// restarts from the position the artifact holds.
func (cluster *Cluster) ReseedFromBackupID(server *ServerMonitor, id int64) error {
	if server == nil {
		return errors.New("no server")
	}
	meta := cluster.BackupMetaMap.Get(id)
	if meta == nil {
		return fmt.Errorf("%w: %d", ErrBackupNotFound, id)
	}
	if meta.BackupMethod == backupmgr.BackupMethodPhysical || (meta.BackupTool != config.ConstBackupLogicalTypeMysqldump && meta.BackupTool != config.ConstBackupLogicalTypeMydumper) {
		return fmt.Errorf("%w: backup %d is %s", ErrBackupDiscoveryNotLogical, id, meta.BackupTool)
	}
	if err := cluster.backupUseRefusal(meta); err != nil {
		return fmt.Errorf("backup %d can not be restored: %w", id, err)
	}
	// the file as it is now: the change check runs every 30 heartbeats
	if err := cluster.backupFileLocked(meta.Dest); err != nil {
		return fmt.Errorf("backup %d can not be restored: %w", id, err)
	}
	if !meta.Completed {
		reason := meta.DiscoveryNote
		if reason == "" {
			reason = "not completed"
		}
		return fmt.Errorf("backup %d can not be restored: %s", id, reason)
	}
	if !hasReplicationPosition(meta) {
		return fmt.Errorf("backup %d can not be restored on a replica: it holds no replication position (binary log file or GTID set)", id)
	}
	if meta.OriginalRemoved {
		return fmt.Errorf("backup %d is kept only in its archive: restore it from its snapshot", id)
	}
	if !cluster.backupArtifactExists(meta.Dest) {
		return fmt.Errorf("backup %d: %s is missing", id, meta.Dest)
	}
	if master := cluster.GetMaster(); master != nil && master.URL == server.URL {
		return fmt.Errorf("%s is the primary: it is not reseeded", server.URL)
	}
	// a backup running may rewrite the artifact this reseed restores (a scheduled backup
	// reuses its file): refused, retried once it ends. A second reseed of the server is
	// refused by the reseed itself (TrySetInReseedBackup).
	if cluster.IsInBackup() {
		return fmt.Errorf("%w: a backup of the cluster is running, reseed %s from backup %d once it ends", ErrBackupInProgress, server.URL, id)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed of %s from backup %d (%s, %s) requested", server.URL, id, meta.BackupTool, meta.Dest)
	return server.JobReseedLogicalBackupFromPathWithOptions(nil, meta.BackupTool, meta.Dest, JobReseedLogicalOptions{})
}

// discoveredBackupPaths returns the artifacts of the discovered backups, and their encrypted
// forms: an automatic restore never takes one (its time may be a copy's). Built once per
// lookup (findExistingBackupPath checks several candidates against it).
func (cluster *Cluster) discoveredBackupPaths() map[string]bool {
	discovered := map[string]bool{}
	if cluster == nil || cluster.BackupMetaMap == nil {
		return discovered
	}
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Discovered && !cluster.isDuplicateScheduledBackup(meta) {
			addKnownBackupArtifact(discovered, meta.Dest)
		}
		return true
	})
	return discovered
}

// isDiscoveredBackupPath tells whether path, or its encrypted form, is the artifact of a
// discovered backup.
func (cluster *Cluster) isDiscoveredBackupPath(path string) bool {
	return cluster.discoveredBackupPaths()[filepath.Clean(path)]
}
