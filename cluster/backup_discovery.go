// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file discovers the logical backups placed in a server's backup folder without
// metadata (copied there by hand or by another tool) and registers them: they are listed,
// and a server can be reseeded from one on request. They are never picked by an automatic
// restore: a copy's modification time is not the time of its data.

package cluster

import (
	"archive/tar"
	"bufio"
	stdgzip "compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
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
)

// discoveryGtidStatementMax bounds a joined GTID_PURGED statement: 10000 sources with
// several intervals each are about 650 KB.
const discoveryGtidStatementMax = 1 << 20

// discoveryLinePrefixMax bounds what is kept of a dump line: the header and footer lines
// are short, a data line (an INSERT of a whole table) is not kept whole.
const discoveryLinePrefixMax = 4096

// ErrBackupDiscoveryNotLogical: a reseed from a chosen backup takes a logical backup only.
var ErrBackupDiscoveryNotLogical = errors.New("reseed from a chosen backup takes a logical backup (mysqldump, splitdump or mydumper)")

// backupDiscoverySettle is how long an artifact must be left unchanged before it is
// discovered: one still being copied in is not registered half written. A package variable
// for the tests, which shorten it; they restore it and do not run in parallel.
var backupDiscoverySettle = time.Minute

// backupDiscoveryMaxPerRun bounds the artifacts one discovery reads (each to its end:
// decrypted, decompressed): new ones and the ones inspected again. The others are left to the
// next discovery, so a folder of many large dumps is never read in one go. A package variable
// for the tests, which shorten it; they restore it and do not run in parallel.
var backupDiscoveryMaxPerRun = 8

// errBackupDiscoveryRunning: a discovery of the server's folder already runs.
var errBackupDiscoveryRunning = errors.New("a discovery of the backup folder already runs")

// DiscoverBackups registers the logical backups of the server's backup folder that no
// metadata describes yet, and loads the ad-hoc metadata files of the folder. A discovered
// backup whose artifact has gone is unregistered; one not completed whose artifact changed
// since (a copy that was still running) is inspected again. It returns the backups it
// registered. One discovery runs at a time per server.
func (server *ServerMonitor) DiscoverBackups() ([]*backupmgr.BackupMetadata, error) {
	return server.discoverBackups(false)
}

// discoverBackups is DiscoverBackups; retry inspects again the discovered backups not
// completed whose artifact did not change too (the discovery the operator requests: a backup
// encrypted with a key installed since can be read now).
func (server *ServerMonitor) discoverBackups(retry bool) ([]*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return nil, nil
	}
	dir := server.GetMyBackupDirectory()
	// ad-hoc backups (and the backups discovered before) have one metadata file each: read
	// even while a backup runs (a live entry is kept), so a server monitor created then (a
	// config reload) lists them at once
	if _, err := server.LoadAdhocBackupMetadata(); err != nil && !os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: ad-hoc metadata not all read: %s", server.URL, err)
	}
	// a backup running writes its artifact: not discovered half written (the startup
	// discovery too, not only the API's)
	if cluster.IsInBackup() {
		return nil, ErrBackupDiscoveryInBackup
	}
	if _, running := cluster.backupDiscoveries.LoadOrStore(server.URL, struct{}{}); running {
		return nil, errBackupDiscoveryRunning
	}
	defer cluster.backupDiscoveries.Delete(server.URL)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup folder %s: %w", dir, err)
	}
	var found []*backupmgr.BackupMetadata
	budget := backupDiscoveryMaxPerRun
	found = append(found, server.refreshDiscoveredBackups(&budget, retry)...)
	known := cluster.knownBackupArtifacts()
	// a metadata file not loaded (a scheduled backup's without its cookie) still describes
	// its artifact: it is not discovered again
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".meta.json") {
			continue
		}
		if meta, err := readBackupMetadataFile(filepath.Join(dir, entry.Name())); err == nil && meta != nil {
			addKnownBackupArtifact(known, meta.Dest)
		}
	}
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
		if changed, err := artifactChangedSince(path, time.Now().Add(-backupDiscoverySettle)); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: %s can not be checked, left to the next discovery: %s", server.URL, path, err)
			continue
		} else if changed {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: %s changed in the last %s (still being written?), left to the next discovery", server.URL, path, backupDiscoverySettle)
			continue
		}
		if budget <= 0 {
			left++
			continue
		}
		budget--
		meta, err := server.registerDiscoveredBackup(path, tool, split, 0)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: %s not registered: %s", server.URL, path, err)
			continue
		}
		found = append(found, meta)
	}
	if left > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: %d more artifact(s) left to the next discovery (%d read per discovery)", server.URL, left, backupDiscoveryMaxPerRun)
	}
	return found, nil
}

// refreshDiscoveredBackups goes over the server's discovered backups: one whose artifact has
// gone is unregistered (its metadata file removed: the discovered entries never outlive their
// artifacts); one not completed whose artifact changed since its inspection (its size or file
// count), or any one not completed with retry, is inspected again once it has settled, within
// budget (the artifacts the discovery may still read). It returns the backups inspected again.
func (server *ServerMonitor) refreshDiscoveredBackups(budget *int, retry bool) []*backupmgr.BackupMetadata {
	cluster := server.ClusterGroup
	var discovered []*backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Discovered && meta.Source == server.URL {
			discovered = append(discovered, meta)
		}
		return true
	})
	var refreshed []*backupmgr.BackupMetadata
	for _, meta := range discovered {
		if _, err := os.Stat(meta.Dest); os.IsNotExist(err) {
			if err := os.Remove(server.backupMetaFilePath(meta)); err != nil && !os.IsNotExist(err) {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: metadata of backup %d not removed: %s", server.URL, meta.Id, err)
				continue
			}
			cluster.BackupMetaMap.Delete(meta.Id)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: discovered backup %d unregistered, its artifact %s is gone", server.URL, meta.Id, meta.Dest)
			continue
		}
		if meta.Completed {
			continue
		}
		now := backupmgr.BackupMetadata{Dest: meta.Dest}
		if err := now.GetSizeAndFileCount(); err != nil || (!retry && now.Size == meta.Size && now.FileCount == meta.FileCount) {
			continue
		}
		if changed, err := artifactChangedSince(meta.Dest, time.Now().Add(-backupDiscoverySettle)); err != nil || changed {
			continue
		}
		if *budget <= 0 {
			continue // inspected again at the next discovery
		}
		*budget--
		again, err := server.registerDiscoveredBackup(meta.Dest, meta.BackupTool, meta.SplitDump, meta.Id)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: backup %d not inspected again: %s", server.URL, meta.Id, err)
			continue
		}
		refreshed = append(refreshed, again)
	}
	return refreshed
}

// artifactChangedSince tells whether the artifact at path (a file, or a folder's files) was
// modified after since.
func artifactChangedSince(path string, since time.Time) (bool, error) {
	changed := false
	err := filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(since) {
			changed = true
			return filepath.SkipAll
		}
		return nil
	})
	return changed, err
}

// ErrBackupDiscoveryInBackup: a backup of the cluster runs, its artifact is not complete yet.
var ErrBackupDiscoveryInBackup = errors.New("a backup of the cluster is running: discover its folder once it ends")

// StartBackupDiscovery runs the discovery on every server of the cluster in the background
// (it reads each new artifact to its end: never in a request or the monitor), not while a
// backup of the cluster runs. The backups it registers are logged and listed.
func (cluster *Cluster) StartBackupDiscovery() error {
	if cluster.IsInBackup() {
		return ErrBackupDiscoveryInBackup
	}
	servers := append([]*ServerMonitor(nil), cluster.Servers...)
	go func() {
		found := 0
		for _, server := range servers {
			if server == nil {
				continue
			}
			metas, err := server.discoverBackups(true)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: %s", server.URL, err)
			}
			found += len(metas)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery of cluster %s ended: %d backup(s) registered or inspected again", cluster.Name, found)
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

// classifyBackupArtifact tells whether an entry of a backup folder is a logical backup, and
// which: a mysqldump file (.sql, .sql.gz, encrypted .enc), a splitdump folder (or its
// encrypted .tar.enc archive), a mydumper folder. A splitdump folder has its manifest, or a
// metadata file without mydumper's "Started dump at" line; splitdump writes mydumper's file
// names, so its name or contents decide, not its file names.
func classifyBackupArtifact(path string, entry os.DirEntry) (tool string, split, ok bool) {
	name := entry.Name()
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

// registerDiscoveredBackup describes the artifact at path, writes its metadata file and
// registers it.
// id 0 registers a new backup; another id inspects that discovered backup again.
func (server *ServerMonitor) registerDiscoveredBackup(path, tool string, split bool, id int64) (*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if id == 0 {
		// the id of an ad-hoc backup, a unix time: the artifact's mtime, bumped while taken
		// (the map or a meta file). Two artifacts with the same mtime get their ids in the
		// folder's sorted order (os.ReadDir), so which one gets the bump can change after a
		// deletion; once its meta file is written, a backup keeps its id for good.
		id = info.ModTime().Unix()
		for cluster.BackupMetaMap.Get(id) != nil || fileExists(server.buildBackupMetaFileName(tool, id, backupmgr.BackupLineAdhoc)) {
			id++
		}
	}
	meta := &backupmgr.BackupMetadata{
		Id:           id,
		StartTime:    info.ModTime(),
		EndTime:      info.ModTime(),
		BackupMethod: backupmgr.BackupMethodLogical,
		BackupTool:   tool,
		Source:       server.URL,
		Dest:         path,
		SplitDump:    split,
		Encrypted:    isEncryptedBackupArtifact(path),
		BackupLine:   backupmgr.BackupLineAdhoc,
		Completed:    true,
		Discovered:   true,
	}
	if err := meta.GetSizeAndFileCount(); err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	if !meta.Encrypted {
		meta.Compressed, _ = backupmgr.DetectCompressionFromDest(path)
	}
	server.inspectDiscoveredBackup(meta)
	server.ensureBackupSessionID(meta, meta.BackupMethod, meta.StartTime, meta.BackupLine)

	metaPath := server.backupMetaFilePath(meta)
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return nil, fmt.Errorf("write %s: %w", metaPath, err)
	}
	meta.MetaFile = metaPath
	cluster.BackupMetaMap.Set(meta.Id, meta)
	state := "completed"
	if !meta.Completed {
		state = "not completed"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s registered %s backup %d (%s, %s): %s %s", server.URL, meta.BackupTool, meta.Id, describeSplit(meta.SplitDump), state, path, meta.DiscoveryNote)
	return meta, nil
}

func describeSplit(split bool) string {
	if split {
		return "splitdump"
	}
	return "single artifact"
}

// inspectDiscoveredBackup reads the artifact as a restore would and records what it found:
// its position, its dump time, whether it is complete. An artifact that can not be read
// (damaged, or encrypted with a key this replication-manager does not hold) is registered
// as not completed: listed, refused by a reseed.
func (server *ServerMonitor) inspectDiscoveredBackup(meta *backupmgr.BackupMetadata) {
	fail := func(format string, args ...any) {
		meta.Completed = false
		meta.DiscoveryNote = fmt.Sprintf(format, args...)
	}
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
		inspectDiscoveredFolder(meta, archive.read, archive.exists, fail)
	case meta.SplitDump || meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		dir := meta.Dest
		read := func(name string) ([]byte, error) { return readDiscoverySmallFile(filepath.Join(dir, name)) }
		exists := func(name string) bool { return fileExists(filepath.Join(dir, name)) }
		inspectDiscoveredFolder(meta, read, exists, fail)
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
// content) and exists: a plain folder, or what an encrypted archive held
// (readDiscoveredArchive).
func inspectDiscoveredFolder(meta *backupmgr.BackupMetadata, read func(string) ([]byte, error), exists func(string) bool, fail func(string, ...any)) {
	switch {
	case meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		data, err := read("metadata")
		if err != nil {
			fail("no mydumper metadata: %s", err)
		} else if !strings.Contains(string(data), "Finished dump at") {
			fail("mydumper metadata has no \"Finished dump at\" line: the dump did not finish")
		} else {
			meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = parseDiscoveredMyDumperPosition(data)
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
		for _, files := range [][]string{m.Schema, m.Data, m.Post} {
			for _, f := range files {
				if !exists(f) {
					fail("the manifest lists %s, missing", f)
					return
				}
			}
		}
	}
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

// inspectDiscoveredDump checks a mysqldump file.
func (server *ServerMonitor) inspectDiscoveredDump(meta *backupmgr.BackupMetadata, fail func(string, ...any)) {
	facts := &discoveredDumpFacts{}
	if err := server.ClusterGroup.readDiscoveredStream(meta.Dest, facts); err != nil {
		fail("can not be read: %s", err)
		return
	}
	meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = facts.file, facts.pos, facts.gtid
	if !facts.completedAt.IsZero() {
		meta.StartTime, meta.EndTime = facts.completedAt, facts.completedAt
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
	header      bool // the client's "-- MySQL dump" / "-- MariaDB dump" header
	completed   bool // its last line is the client's "-- Dump completed" footer
	completedAt time.Time
	file        string
	pos         uint64
	gtid        string
}

// readDiscoveredStream reads the mysqldump file at path to its end as a restore reads it
// (decrypted, decompressed), and fills facts.
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
	lr := bufio.NewReaderSize(r, 64*1024)
	var last string
	// a GTID_PURGED statement the client wrote over several lines (one source per line): its
	// lines joined up to the end of the statement, as the backup's own reader joins them
	var gtidStmt strings.Builder
	for {
		line, err := readDiscoveryLine(lr)
		if line != "" {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				last = trimmed
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
	}
	if m := discoveryDumpComplete.FindStringSubmatch(last); m != nil {
		facts.completed = true
		if m[1] != "" {
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", strings.Join(strings.Fields(m[1]), " "), time.Local); err == nil {
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
// chose (a discovered one included): its artifact is restored and replication restarts from
// the position the artifact holds.
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
		if meta != nil && meta.Discovered {
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
