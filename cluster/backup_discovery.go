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
	discoveryBinlogRegex  = regexp.MustCompile(`CHANGE (?:REPLICATION SOURCE|MASTER) (?:'[^']*' )?TO (?:SOURCE|MASTER)_LOG_FILE='([^']+)', (?:SOURCE|MASTER)_LOG_POS=(\d+)`)
	discoveryGtidMariaDB  = regexp.MustCompile(`gtid_slave_pos='([^']*)'`)
	discoveryGtidMySQL    = regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'\+'\*/\s*)?(?:/\*![0-9]*\s*'([^'+][^']*)'\*/|'([^'+][^']*)')`)
	discoveryDumpComplete = regexp.MustCompile(`^-- Dump completed(?: on (\d{4}-\d{2}-\d{2} +\d{1,2}:\d{2}:\d{2}))?`)
)

// discoveryLinePrefixMax bounds what is kept of a dump line: the header and footer lines
// are short, a data line (an INSERT of a whole table) is not kept whole.
const discoveryLinePrefixMax = 4096

// ErrBackupDiscoveryNotLogical: a reseed from a chosen backup takes a logical backup only.
var ErrBackupDiscoveryNotLogical = errors.New("reseed from a chosen backup takes a logical backup (mysqldump, splitdump or mydumper)")

// DiscoverBackups registers the logical backups of the server's backup folder that no
// metadata describes yet, and loads the ad-hoc metadata files of the folder. It returns the
// backups it registered.
func (server *ServerMonitor) DiscoverBackups() ([]*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.BackupMetaMap == nil {
		return nil, nil
	}
	dir := server.GetMyBackupDirectory()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup folder %s: %w", dir, err)
	}
	// ad-hoc backups (and the backups discovered before) have one metadata file each
	if _, err := server.LoadAdhocBackupMetadata(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: ad-hoc metadata not all read: %s", server.URL, err)
	}
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
	var found []*backupmgr.BackupMetadata
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if known[path] {
			continue
		}
		tool, split, ok := classifyBackupArtifact(path, entry)
		if !ok {
			continue
		}
		meta, err := server.registerDiscoveredBackup(path, tool, split)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: %s not registered: %s", server.URL, path, err)
			continue
		}
		found = append(found, meta)
	}
	return found, nil
}

// ErrBackupDiscoveryInBackup: a backup of the cluster runs, its artifact is not complete yet.
var ErrBackupDiscoveryInBackup = errors.New("a backup of the cluster is running: discover its folder once it ends")

// DiscoverBackups runs the discovery on every server of the cluster, not while a backup of
// the cluster runs.
func (cluster *Cluster) DiscoverBackups() ([]*backupmgr.BackupMetadata, error) {
	if cluster.IsInBackup() {
		return nil, ErrBackupDiscoveryInBackup
	}
	var found []*backupmgr.BackupMetadata
	var errs []error
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		metas, err := server.DiscoverBackups()
		if err != nil {
			errs = append(errs, err)
		}
		found = append(found, metas...)
	}
	return found, errors.Join(errs...)
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
	if data, err := os.ReadFile(filepath.Join(path, "metadata")); err == nil {
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
func (server *ServerMonitor) registerDiscoveredBackup(path, tool string, split bool) (*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	// the id of an ad-hoc backup, a unix time: the artifact's, unique in the cluster
	id := info.ModTime().Unix()
	for cluster.BackupMetaMap.Get(id) != nil || fileExists(server.buildBackupMetaFileName(tool, id, backupmgr.BackupLineAdhoc)) {
		id++
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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s registered %s backup %d (%s, %s): %s %s", server.URL, tool, meta.Id, describeSplit(split), state, path, meta.DiscoveryNote)
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
		// checked, which a foreign key fails), its manifest and metadata kept, then the
		// folder's own checks run on them
		dir, cleanup, err := server.ClusterGroup.readDiscoveredArchive(meta.Dest)
		if err != nil {
			fail("can not be read: %s", err)
			return
		}
		defer cleanup()
		inspectDiscoveredFolder(meta, dir, fail)
	case meta.SplitDump || meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		inspectDiscoveredFolder(meta, meta.Dest, fail)
	default:
		server.inspectDiscoveredDump(meta, fail)
	}
}

// inspectDiscoveredFolder checks a splitdump or mydumper folder at dir: a plain one, or the
// files an encrypted archive listed (readDiscoveredArchive).
func inspectDiscoveredFolder(meta *backupmgr.BackupMetadata, dir string, fail func(string, ...any)) {
	switch {
	case meta.BackupTool == config.ConstBackupLogicalTypeMydumper:
		data, err := os.ReadFile(filepath.Join(dir, "metadata"))
		if err != nil {
			fail("no mydumper metadata: %s", err)
		} else if !strings.Contains(string(data), "Finished dump at") {
			fail("mydumper metadata has no \"Finished dump at\" line: the dump did not finish")
		}
	case meta.SplitDump:
		if m, err := splitdump.ReadMetadata(dir); err == nil && m != nil {
			meta.BinLogFileName, meta.BinLogFilePos, meta.BinLogGtid = m.File, m.Position, m.GTID
		}
		m, err := splitdump.ReadManifest(dir)
		if errors.Is(err, os.ErrNotExist) {
			meta.DiscoveryNote = "no manifest: its files are not checked against one"
			return
		}
		if err == nil {
			err = splitdump.ValidateManifest(m)
		}
		if err != nil {
			fail("manifest: %s", err)
			return
		}
		for _, files := range [][]string{m.Schema, m.Data, m.Post} {
			for _, f := range files {
				if !fileExists(filepath.Join(dir, f)) {
					fail("the manifest lists %s, missing", f)
					return
				}
			}
		}
	}
}

// discoveryArchiveSmallFile bounds the manifest and metadata files read from an archive.
const discoveryArchiveSmallFile = 16 << 20

// readDiscoveredArchive reads the encrypted folder archive at path to its end and returns a
// temporary folder holding, for its checks, its manifest and metadata files as they are and
// every other file it holds as an empty placeholder; cleanup removes it.
func (cluster *Cluster) readDiscoveredArchive(path string) (string, func(), error) {
	rc, _, err := cluster.openRestoreArtifactStream(path)
	if err != nil {
		return "", func() {}, err
	}
	defer rc.Close()
	dir, err := os.MkdirTemp("", "repman-discovery-")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return dir, cleanup, nil
		}
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		name := filepath.Base(hdr.Name)
		if hdr.Typeflag != tar.TypeReg || name == "." || name == string(filepath.Separator) {
			continue
		}
		var data []byte
		if name == "manifest" || name == "metadata" {
			if data, err = io.ReadAll(io.LimitReader(tr, discoveryArchiveSmallFile)); err != nil {
				cleanup()
				return "", func() {}, err
			}
		}
		if _, err := io.Copy(io.Discard, tr); err != nil { // the rest is read: its integrity
			cleanup()
			return "", func() {}, err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			cleanup()
			return "", func() {}, err
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
	for {
		line, err := readDiscoveryLine(lr)
		if line != "" {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				last = trimmed
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
	if meta.OriginalRemoved {
		return fmt.Errorf("backup %d is kept only in its archive: restore it from its snapshot", id)
	}
	if !cluster.backupArtifactExists(meta.Dest) {
		return fmt.Errorf("backup %d: %s is missing", id, meta.Dest)
	}
	if master := cluster.GetMaster(); master != nil && master.URL == server.URL {
		return fmt.Errorf("%s is the primary: it is not reseeded", server.URL)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Reseed of %s from backup %d (%s, %s) requested", server.URL, id, meta.BackupTool, meta.Dest)
	return server.JobReseedLogicalBackupFromPathWithOptions(nil, meta.BackupTool, meta.Dest, JobReseedLogicalOptions{})
}

// isDiscoveredBackupPath tells whether path, or its encrypted form, is the artifact of a
// discovered backup: an automatic restore never takes it (its time may be a copy's).
func (cluster *Cluster) isDiscoveredBackupPath(path string) bool {
	if cluster == nil || cluster.BackupMetaMap == nil {
		return false
	}
	known := map[string]bool{}
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Discovered {
			addKnownBackupArtifact(known, meta.Dest)
		}
		return true
	})
	return known[filepath.Clean(path)]
}
