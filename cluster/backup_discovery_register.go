// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file registers a discovered backup on the operator's request, only a proper backup
// (it holds a table and is complete), unregisters one, and tracks a change to a registered
// one's file: until the change is registered, the backup is not used (#2073). Register and
// Unregister write or remove replication-manager's own metadata file only, never the backup.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

var (
	// ErrBackupChangedSinceRegistered: a registered discovered backup's file changed, the
	// change is not registered yet.
	ErrBackupChangedSinceRegistered = errors.New("changed since registered: register the change first")
	// ErrBackupNotABackup: the artifact holds no table, or a discovered entry was flagged so.
	ErrBackupNotABackup = errors.New("not a backup")
	// ErrBackupNotDiscovered: Register's change and Unregister take a discovered backup only.
	ErrBackupNotDiscovered = errors.New("not a discovered backup: only a backup found in a backup folder is registered or unregistered")
	// ErrBackupAlreadyRegistered: a backup's metadata already describes the artifact.
	ErrBackupAlreadyRegistered = errors.New("already registered")
	// ErrBackupNotRegisterable: the artifact can not be registered (not in the backup folder,
	// not a logical backup, still being copied in...).
	ErrBackupNotRegisterable = errors.New("can not be registered")
	// ErrBackupUnchanged: Register's change on a backup whose file did not change.
	ErrBackupUnchanged = errors.New("unchanged since registered: nothing to register")
)

// BackupRegistrationChange is a registered discovered backup whose file is not the one
// registered: what changed, as the last change check found it.
type BackupRegistrationChange struct {
	Server   string                         `json:"server"`
	ID       int64                          `json:"id"`
	Dest     string                         `json:"dest"`
	Was      backupmgr.ArtifactFingerprint  `json:"was"`
	Now      *backupmgr.ArtifactFingerprint `json:"now,omitempty"` // nil: missing, or can not be read
	Error    string                         `json:"error,omitempty"`
	Checked  time.Time                      `json:"checked"`
	Describe string                         `json:"describe"`
	// RegisterError: the last Register of the change, refused
	RegisterError string `json:"registerError,omitempty"`
}

// backupRegisterResult is the outcome of a Register still kept for the list: running, or
// refused with why.
type backupRegisterResult struct {
	server  string
	id      int64 // 0: a new backup
	running bool
	err     string
}

// BackupDiscoveryView is what the backup list shows of the discovery: the candidates of each
// server's last discovery and the registered backups changed since.
type BackupDiscoveryView struct {
	Servers []backupCandidateSet       `json:"servers"`
	Changes []BackupRegistrationChange `json:"changes"`
}

// GetBackupDiscovery returns the candidates of each server's last discovery, with the
// outcome of their last Register, and the registered backups changed since registered.
func (cluster *Cluster) GetBackupDiscovery() BackupDiscoveryView {
	view := BackupDiscoveryView{Servers: []backupCandidateSet{}, Changes: []BackupRegistrationChange{}}
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		set := cluster.backupCandidateSet(s.URL)
		if set == nil {
			continue
		}
		out := backupCandidateSet{Server: set.Server, Dropped: set.Dropped, ScannedAt: set.ScannedAt, Candidates: make([]BackupCandidate, len(set.Candidates))}
		copy(out.Candidates, set.Candidates)
		for i := range out.Candidates {
			if v, ok := cluster.backupRegisterResults.Load(out.Candidates[i].Path); ok {
				r := v.(backupRegisterResult)
				out.Candidates[i].Registering, out.Candidates[i].RegisterError = r.running, r.err
			}
		}
		view.Servers = append(view.Servers, out)
	}
	cluster.backupChanges.Range(func(_, v any) bool {
		c := *v.(*BackupRegistrationChange)
		if r, ok := cluster.backupRegisterResults.Load(c.Dest); ok {
			c.RegisterError = r.(backupRegisterResult).err
		}
		view.Changes = append(view.Changes, c)
		return true
	})
	sort.Slice(view.Changes, func(i, j int) bool { return view.Changes[i].ID < view.Changes[j].ID })
	return view
}

// pruneBackupRegisterResults drops the outcomes of the Registers whose artifact is neither a
// candidate nor a changed backup any more: bounded by the candidates and the backups.
func (cluster *Cluster) pruneBackupRegisterResults() {
	live := map[string]bool{}
	cluster.backupCandidates.Range(func(_, v any) bool {
		for _, c := range v.(*backupCandidateSet).Candidates {
			live[c.Path] = true
		}
		return true
	})
	cluster.backupChanges.Range(func(_, v any) bool {
		live[v.(*BackupRegistrationChange).Dest] = true
		return true
	})
	cluster.backupRegisterResults.Range(func(k, v any) bool {
		if !live[k.(string)] && !v.(backupRegisterResult).running {
			cluster.backupRegisterResults.Delete(k)
		}
		return true
	})
}

// backupChange returns the change of a registered discovered backup, nil when its file is
// the one registered (or it was never checked).
func (cluster *Cluster) backupChange(id int64) *BackupRegistrationChange {
	if v, ok := cluster.backupChanges.Load(id); ok {
		return v.(*BackupRegistrationChange)
	}
	return nil
}

// backupUseRefusal tells why a backup is not used by a reseed, a restore or the restore
// catalog: a discovered record of a scheduled backup file, not a backup, or any backup whose
// file changed since it was written or registered (the change check's record, from memory).
// nil otherwise.
func (cluster *Cluster) backupUseRefusal(meta *backupmgr.BackupMetadata) error {
	if meta == nil {
		return nil
	}
	if c := cluster.backupChange(meta.Id); c != nil {
		return fmt.Errorf("%w (%s)", ErrBackupChangedSinceRegistered, c.Describe)
	}
	if !meta.Discovered {
		return nil
	}
	if cluster.isDuplicateScheduledBackup(meta) {
		return fmt.Errorf("%w: %s", ErrBackupDuplicateScheduled, meta.DiscoveryNote)
	}
	if meta.NotABackup {
		return fmt.Errorf("%w: %s", ErrBackupNotABackup, meta.DiscoveryNote)
	}
	return nil
}

// recordBackupRegistration records the fingerprint of meta's file as written, for a completed
// logical backup on disk: a file changed since is not used until registered (the change
// check, backupFileLocked). The caller holds backupMetaMutex. A file whose fingerprint can not
// be taken (a folder beyond the walk bound, no inode off Linux) is recorded without one, used
// as before.
func (server *ServerMonitor) recordBackupRegistration(meta *backupmgr.BackupMetadata) {
	if meta == nil || !meta.Completed || meta.OriginalRemoved || meta.BackupMethod != backupmgr.BackupMethodLogical || strings.TrimSpace(meta.Dest) == "" {
		return
	}
	fp, err := restoreArtifactFingerprintBounded(meta.Dest, backupProofWalkMax)
	if err != nil || fp.ChangeTime.IsZero() {
		return
	}
	meta.Registration = &backupmgr.BackupRegistration{Artifact: *fp, RegisteredAt: time.Now()}
}

// backupRecordFor returns the newest record of the logical backup whose file is path: the one
// that owns it (a scheduled backup rewrites its file and its record).
func (cluster *Cluster) backupRecordFor(path string) *backupmgr.BackupMetadata {
	if cluster == nil || cluster.BackupMetaMap == nil || strings.TrimSpace(path) == "" {
		return nil
	}
	var found *backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, m *backupmgr.BackupMetadata) bool {
		if m != nil && m.BackupMethod == backupmgr.BackupMethodLogical && backupArtifactsMatch(m.Dest, path) && (found == nil || m.Id > found.Id) {
			found = m
		}
		return true
	})
	return found
}

// isSupersededBackupRecord tells whether a newer record owns meta's file.
func (cluster *Cluster) isSupersededBackupRecord(meta *backupmgr.BackupMetadata) bool {
	newest := cluster.backupRecordFor(meta.Dest)
	return newest != nil && newest.Id != meta.Id
}

// backupFileLocked tells, when an automatic reseed is about to restore path, whether its file
// is not the one its newest record was written or registered with: a replacement put there
// by hand, an edit, a truncation. It stats the file (walks a folder, bounded) now, the change
// check runs every 30 heartbeats. A backup kept in its archive and fetched back by
// replication-manager, or without a fingerprint, is not locked.
func (cluster *Cluster) backupFileLocked(path string) error {
	meta := cluster.backupRecordFor(path)
	if meta == nil {
		return nil
	}
	if err := cluster.backupUseRefusal(meta); err != nil {
		return fmt.Errorf("backup %d (%s): %w", meta.Id, path, err)
	}
	server := cluster.GetServerFromURL(meta.Source)
	var rec *backupmgr.ArtifactFingerprint
	removed := false
	if server != nil {
		server.backupMetaMutex.Lock()
	}
	if meta.Registration != nil {
		r := meta.Registration.Artifact
		rec = &r
	}
	removed = meta.OriginalRemoved
	if server != nil {
		server.backupMetaMutex.Unlock()
	}
	if rec == nil || removed || artifactUnchangedBounded(path, rec, backupProofWalkMax) {
		return nil
	}
	return fmt.Errorf("backup %d (%s): %w", meta.Id, path, ErrBackupChangedSinceRegistered)
}

// RegisterBackup registers the artifact at path of the server's backup folder, a candidate of
// a discovery: in the background, it is read to its end as a restore would read it, and
// registered only if it is a proper backup (a table, complete); the outcome is listed with the
// candidates. takenAt, when not zero, is the time of a backup that records none (the
// operator's). It returns at once: refused when the cheap checks already tell it is not a
// backup.
func (cluster *Cluster) RegisterBackup(serverURL, path string, takenAt time.Time) error {
	server := cluster.GetServerFromURL(serverURL)
	if server == nil {
		return fmt.Errorf("%w: server %s not found", ErrBackupNotRegisterable, serverURL)
	}
	path = filepath.Clean(path)
	dir := filepath.Clean(server.GetMyBackupDirectory())
	if filepath.Dir(path) != dir {
		return fmt.Errorf("%w: %s is not an entry of %s", ErrBackupNotRegisterable, path, dir)
	}
	if !takenAt.IsZero() && takenAt.After(time.Now()) {
		return fmt.Errorf("%w: its time %s is in the future", ErrBackupNotRegisterable, takenAt.Format(time.RFC3339))
	}
	// the entry itself, not what a symbolic link points to: one may point outside the folder
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrBackupNotRegisterable, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s is a symbolic link, not read", ErrBackupNotRegisterable, filepath.Base(path))
	}
	tool, split, ok := classifyBackupArtifact(path, fs.FileInfoToDirEntry(info))
	if !ok {
		return fmt.Errorf("%w: %s is not a logical backup (mysqldump, splitdump or mydumper)", ErrBackupNotRegisterable, path)
	}
	// a scheduled backup file (mysqldump.sql.gz, splitdump...) replaced by hand is registered
	// under its name as the scheduled backup: its record is replaced, never doubled
	scheduled := server.isScheduledBackupArtifact(path)
	if entries, err := os.ReadDir(dir); err == nil && !scheduled && server.knownBackupArtifactsOnDisk(entries)[path] {
		return fmt.Errorf("%w: %s", ErrBackupAlreadyRegistered, path)
	}
	if c, ok := cluster.findBackupCandidate(server.URL, path); ok && !c.Registerable() && c.Status != BackupCandidateSettling {
		if c.Status == BackupCandidateNotBackup {
			return fmt.Errorf("%w: %s", ErrBackupNotABackup, c.Reason)
		}
		return fmt.Errorf("%w: %s", ErrBackupNotRegisterable, c.Reason)
	}
	return cluster.startBackupRegistration(server, path, tool, split, nil, takenAt)
}

// RegisterBackupChange registers the change of a registered discovered backup's file: the
// file as it is now is read and checked as a new one would be, and on success its new
// fingerprint, position and size are recorded and the change cleared. In the background,
// as RegisterBackup.
func (cluster *Cluster) RegisterBackupChange(id int64, takenAt time.Time) error {
	meta := cluster.BackupMetaMap.Get(id)
	if meta == nil {
		return fmt.Errorf("%w: %d", ErrBackupNotFound, id)
	}
	if meta.BackupMethod != backupmgr.BackupMethodLogical {
		return fmt.Errorf("%w: backup %d is %s, only a logical backup is read and registered", ErrBackupNotRegisterable, id, meta.BackupTool)
	}
	if !takenAt.IsZero() && takenAt.After(time.Now()) {
		return fmt.Errorf("%w: its time %s is in the future", ErrBackupNotRegisterable, takenAt.Format(time.RFC3339))
	}
	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return fmt.Errorf("%w: server %s not found", ErrBackupNotRegisterable, meta.Source)
	}
	if meta.Registration != nil && cluster.backupChange(id) == nil && artifactUnchangedBounded(meta.Dest, &meta.Registration.Artifact, backupProofWalkMax) {
		return fmt.Errorf("%w: backup %d", ErrBackupUnchanged, id)
	}
	tool, split := meta.BackupTool, meta.SplitDump
	return cluster.startBackupRegistration(server, meta.Dest, tool, split, meta, takenAt)
}

// startBackupRegistration starts the read and registration of path in the background, one
// per server folder at a time (with the discoveries and the change check).
func (cluster *Cluster) startBackupRegistration(server *ServerMonitor, path, tool string, split bool, prev *backupmgr.BackupMetadata, takenAt time.Time) error {
	if cluster.IsInBackup() {
		return ErrBackupDiscoveryInBackup
	}
	if prev != nil {
		if _, verifying := cluster.backupVerifying.Load(prev.Id); verifying {
			return fmt.Errorf("%w: %d", ErrBackupVerificationRunning, prev.Id)
		}
	}
	if changed, err := artifactChangedSince(path, time.Now().Add(-backupDiscoverySettle)); err != nil {
		return fmt.Errorf("%w: %s", ErrBackupNotRegisterable, err)
	} else if changed {
		return fmt.Errorf("%w: changed in the last %s, still being copied in? Register it once it settles", ErrBackupNotRegisterable, backupDiscoverySettle)
	}
	if _, running := cluster.backupDiscoveries.LoadOrStore(server.URL, struct{}{}); running {
		return ErrBackupDiscoveryRunning
	}
	var id int64
	if prev != nil {
		id = prev.Id
	}
	cluster.backupRegisterResults.Store(path, backupRegisterResult{server: server.URL, id: id, running: true})
	go func() {
		defer cluster.backupDiscoveries.Delete(server.URL)
		// the reads recover their own panics (inspectDiscoveredBackup): this one keeps any
		// other from ending the process, the registration refused
		defer func() {
			if r := recover(); r != nil {
				cluster.backupRegisterResults.Store(path, backupRegisterResult{server: server.URL, id: id, err: fmt.Sprintf("internal error: %v", r)})
				cluster.logDiscoveryPanic(path, r)
			}
		}()
		meta, err := server.registerDiscoveredBackup(path, tool, split, prev, takenAt)
		if err != nil {
			cluster.backupRegisterResults.Store(path, backupRegisterResult{server: server.URL, id: id, err: err.Error()})
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup register on %s: %s refused: %s", server.URL, path, err)
			return
		}
		cluster.backupRegisterResults.Delete(path)
		cluster.backupChanges.Delete(meta.Id)
		cluster.backupChanges.Delete(id) // a scheduled backup's registered change gets a new id
		cluster.dropBackupCandidate(server.URL, path)
	}()
	return nil
}

// artifactChangedSince tells whether the artifact at path (a file, or a folder's files) was
// modified after since.
func artifactChangedSince(path string, since time.Time) (bool, error) {
	fp, err := restoreArtifactFingerprintBounded(path, backupProofWalkMax)
	if err != nil {
		return false, err
	}
	return fp.ModTime.After(since), nil
}

// registerDiscoveredBackup reads the artifact at path to its end and, when it is a proper
// backup, writes its metadata file and registers it as discovered, its fingerprint recorded.
// prev nil registers a new backup; else the change of prev, which keeps its id and its
// verification results (taken before the change). The artifact must not change during the
// read. Nothing is written when it is refused.
func (server *ServerMonitor) registerDiscoveredBackup(path, tool string, split bool, prev *backupmgr.BackupMetadata, takenAt time.Time) (*backupmgr.BackupMetadata, error) {
	cluster := server.ClusterGroup
	before, err := restoreArtifactFingerprintBounded(path, backupProofWalkMax)
	if errors.Is(err, errArtifactWalkBudget) {
		return nil, fmt.Errorf("more than %d files: its changes can not be tracked", backupProofWalkMax)
	}
	if err != nil {
		return nil, err
	}
	if before.ChangeTime.IsZero() {
		return nil, errors.New("its file identity (inode, change time) can not be read here: its changes can not be tracked")
	}
	// a scheduled backup file replaced by hand becomes the scheduled backup: its record
	// (<tool>.meta.json) is replaced, it is used as any scheduled backup
	scheduled := (prev == nil && server.isScheduledBackupArtifact(path)) || (prev != nil && !prev.Discovered && !prev.IsAdhoc())
	var id int64
	switch {
	case prev != nil && !scheduled:
		id = prev.Id
	case scheduled:
		id = time.Now().Unix()
		for cluster.BackupMetaMap.Get(id) != nil {
			id++
		}
	default:
		// the id of an ad-hoc backup, a unix time: the artifact's mtime, bumped while taken
		// (the map or a meta file); once its meta file is written, a backup keeps its id
		id = before.ModTime.Unix()
		for cluster.BackupMetaMap.Get(id) != nil || fileExists(server.buildBackupMetaFileName(tool, id, backupmgr.BackupLineAdhoc)) {
			id++
		}
	}
	meta := &backupmgr.BackupMetadata{
		Id:             id,
		StartTime:      before.ModTime,
		EndTime:        before.ModTime,
		TimeSource:     backupmgr.BackupTimeSourceFileTime,
		BackupMethod:   backupmgr.BackupMethodLogical,
		BackupTool:     tool,
		BackupStrategy: backupmgr.BackupStrategyFull,
		Source:         server.URL,
		Dest:           path,
		SplitDump:      split,
		Encrypted:      isEncryptedBackupArtifact(path),
		BackupLine:     backupmgr.BackupLineAdhoc,
		Completed:      true,
		Discovered:     true,
	}
	if err := meta.GetSizeAndFileCount(); err != nil {
		return nil, fmt.Errorf("size: %w", err)
	}
	if !meta.Encrypted {
		meta.Compressed, _ = backupmgr.DetectCompressionFromDest(path)
	}
	server.inspectDiscoveredBackup(meta)
	if meta.NotABackup {
		return nil, fmt.Errorf("%w: %s", ErrBackupNotABackup, meta.DiscoveryNote)
	}
	if !meta.Completed {
		return nil, fmt.Errorf("not a complete backup: %s", meta.DiscoveryNote)
	}
	if !takenAt.IsZero() {
		if meta.TimeSource != backupmgr.BackupTimeSourceFileTime {
			return nil, fmt.Errorf("it records its own time (%s, %s): the time is not set by the operator", meta.TimeSource, meta.EndTime.Format("2006-01-02 15:04:05"))
		}
		meta.StartTime, meta.EndTime, meta.TimeSource = takenAt, takenAt, backupmgr.BackupTimeSourceOperator
	}
	after, err := restoreArtifactFingerprintBounded(path, backupProofWalkMax)
	if err != nil || !artifactFingerprintsEqual(before, after) {
		return nil, errors.New("changed while it was read (a copy still running, or a backup that started meanwhile): register it again once it ends")
	}
	meta.Registration = &backupmgr.BackupRegistration{Artifact: *after, RegisteredAt: time.Now()}
	if prev != nil {
		// the change of a registered backup keeps what is not read from its file: its
		// verification results (taken before the change, marked so in the list), its
		// archive snapshot, its session
		// (a verification writes them under backupMetaMutex)
		server.backupMetaMutex.Lock()
		meta.Verification, meta.VerificationRuns, meta.VerificationRunSequence = prev.Verification, prev.VerificationRuns, prev.VerificationRunSequence
		meta.ResticEnabled, meta.ResticSnapshotID, meta.ResticFilePath = prev.ResticEnabled, prev.ResticSnapshotID, prev.ResticFilePath
		meta.BackupSessionID = prev.BackupSessionID
		// a backup replication-manager took keeps being one; an ad hoc one keeps its line
		meta.Discovered, meta.BackupLine, meta.RetentionDays, meta.RetentionDuration = prev.Discovered, prev.BackupLine, prev.RetentionDays, prev.RetentionDuration
		server.backupMetaMutex.Unlock()
	}
	what := "registered"
	if scheduled {
		meta.Discovered, meta.BackupLine = false, backupmgr.BackupLineDefault
		server.ensureBackupSessionID(meta, meta.BackupMethod, meta.StartTime, meta.BackupLine)
		if err := server.writeScheduledBackupMeta(meta); err != nil {
			return nil, err
		}
		what = "registered, as the scheduled backup,"
	} else {
		server.ensureBackupSessionID(meta, meta.BackupMethod, meta.StartTime, meta.BackupLine)
		if err := server.writeDiscoveredBackupMeta(meta, prev); err != nil {
			return nil, err
		}
	}
	if prev != nil {
		what = "registered the change of"
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup register on %s %s %s backup %d (%s, %s, %s): %s %s", server.URL, what, meta.BackupTool, meta.Id, describeSplit(meta.SplitDump), humanBytes(meta.Size), meta.TimeSource, path, meta.DiscoveryNote)
	return meta, nil
}

// writeDiscoveredBackupMeta writes meta's metadata file, replication-manager's own, and puts
// it in the backup list in place of prev (whose file is removed if its name differs).
func (server *ServerMonitor) writeDiscoveredBackupMeta(meta, prev *backupmgr.BackupMetadata) error {
	cluster := server.ClusterGroup
	metaPath := server.backupMetaFilePath(meta)
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return err
	}
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", metaPath, err)
	}
	meta.MetaFile = metaPath
	if prev != nil {
		if old := server.backupMetaFilePath(prev); old != "" && old != metaPath {
			os.Remove(old)
		}
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	return nil
}

// writeScheduledBackupMeta makes meta, a scheduled backup file replaced by hand and read by
// Register, the server's scheduled backup of its tool: its record (<tool>.meta.json) written
// in place of the one the scheduled backup wrote, the previous entries of that record dropped
// from the list, the server's last logical backup and its backup cookie set, as a scheduled
// backup sets them.
func (server *ServerMonitor) writeScheduledBackupMeta(meta *backupmgr.BackupMetadata) error {
	cluster := server.ClusterGroup
	metaPath := server.buildBackupMetaFileName(meta.BackupTool, meta.Id, backupmgr.BackupLineDefault)
	if metaPath == "" {
		return fmt.Errorf("no metadata file for tool %q", meta.BackupTool)
	}
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return err
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write %s: %w", metaPath, err)
	}
	var replaced []int64
	cluster.BackupMetaMap.Callback(func(id int64, other *backupmgr.BackupMetadata) bool {
		if other != nil && id != meta.Id && !other.Discovered && !other.IsAdhoc() && other.Source == server.URL && other.BackupTool == meta.BackupTool && other.BackupMethod == backupmgr.BackupMethodLogical {
			replaced = append(replaced, id)
		}
		return true
	})
	for _, id := range replaced {
		cluster.BackupMetaMap.Delete(id)
	}
	cluster.BackupMetaMap.Set(meta.Id, meta)
	server.LastBackupMeta.Logical = meta
	if err := server.SetBackupLogicalCookie(meta.BackupTool); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup register on %s: backup cookie of %s not set: %s", server.URL, meta.BackupTool, err)
	}
	return nil
}

// artifactFingerprintsEqual compares two fingerprints of the same read.
func artifactFingerprintsEqual(a, b *backupmgr.ArtifactFingerprint) bool {
	return a != nil && b != nil && a.Files == b.Files && a.Size == b.Size && a.ModTime.Equal(b.ModTime) && a.Inode == b.Inode && a.ChangeTime.Equal(b.ChangeTime)
}

// UnregisterBackup unregisters a discovered backup: its metadata file (replication-manager's
// own) is removed, the backup file is left as it is and becomes a candidate again at the next
// discovery, started at once.
func (cluster *Cluster) UnregisterBackup(id int64) error {
	meta := cluster.BackupMetaMap.Get(id)
	if meta == nil {
		return fmt.Errorf("%w: %d", ErrBackupNotFound, id)
	}
	if !meta.Discovered {
		return fmt.Errorf("%w: backup %d", ErrBackupNotDiscovered, id)
	}
	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return fmt.Errorf("source server %s not found", meta.Source)
	}
	cluster.backupVerificationMu.Lock()
	defer cluster.backupVerificationMu.Unlock()
	if _, verifying := cluster.backupVerifying.Load(id); verifying {
		return fmt.Errorf("%w: %d", ErrBackupVerificationRunning, id)
	}
	if _, running := cluster.backupDiscoveries.LoadOrStore(server.URL, struct{}{}); running {
		return ErrBackupDiscoveryRunning
	}
	if err := os.Remove(server.backupMetaFilePath(meta)); err != nil && !os.IsNotExist(err) {
		cluster.backupDiscoveries.Delete(server.URL)
		return err
	}
	cluster.BackupMetaMap.Delete(id)
	cluster.backupChanges.Delete(id)
	cluster.backupRegisterResults.Delete(meta.Dest)
	cluster.backupDiscoveries.Delete(server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup unregister on %s: discovered backup %d unregistered, its file %s left as it is", server.URL, id, meta.Dest)
	go func() {
		if _, err := server.discoverBackups(false); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: %s", server.URL, err)
		}
	}()
	return nil
}

// checkRegisteredBackups compares each logical backup's file, a registered discovered one or
// one replication-manager took, with its recorded fingerprint (a stat, a bounded walk for a
// folder) and records the changes, which checkBackupDiscoveryStates raises and
// backupUseRefusal refuses: off the monitor tick (Run, every 30 heartbeats), one run at a
// time, not while a backup runs, a server skipped while its folder is discovered or
// registered. A backup without a fingerprint (registered by 3.1.44, or taken before backups
// recorded one) is adopted: its fingerprint recorded as it is now, a discovered one flagged if
// it is not a backup (backupDiscoveryMaxPerRun of them per run).
func (cluster *Cluster) checkRegisteredBackups() {
	if cluster.BackupMetaMap == nil || !cluster.backupRegistrationChecking.CompareAndSwap(false, true) {
		return
	}
	// a backup running rewrites, renames (backup-keep-until-valid) or encrypts its files: the
	// last check's changes are kept until it ends
	if cluster.IsInBackup() {
		cluster.backupRegistrationChecking.Store(false)
		return
	}
	defer cluster.backupRegistrationChecking.Store(false)
	budget := backupDiscoveryMaxPerRun
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		if _, running := cluster.backupDiscoveries.LoadOrStore(s.URL, struct{}{}); running {
			continue
		}
		s.checkRegisteredBackups(&budget)
		cluster.backupDiscoveries.Delete(s.URL)
	}
	// the Register outcomes are bounded by the candidates and the changes, also when no
	// discovery runs (backup-discovery off)
	cluster.pruneBackupRegisterResults()
}

// checkRegisteredBackups is Cluster.checkRegisteredBackups for the server's logical backups:
// the discovered ones and replication-manager's own, scheduled or ad hoc.
func (server *ServerMonitor) checkRegisteredBackups(budget *int) {
	cluster := server.ClusterGroup
	var metas []*backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Source == server.URL && meta.BackupMethod == backupmgr.BackupMethodLogical {
			metas = append(metas, meta)
		}
		return true
	})
	for _, meta := range metas {
		if cluster.isDuplicateScheduledBackup(meta) {
			server.removeScheduledBackupRecord(meta)
			continue
		}
		server.backupMetaMutex.Lock()
		removed, completed := meta.OriginalRemoved, meta.Completed
		var rec *backupmgr.ArtifactFingerprint
		if meta.Registration != nil {
			r := meta.Registration.Artifact
			rec = &r
		}
		server.backupMetaMutex.Unlock()
		// kept only in its archive (nothing on disk to compare), or a file a newer record owns
		// (a scheduled backup rewrote it with its own record)
		if removed || cluster.isSupersededBackupRecord(meta) {
			cluster.backupChanges.Delete(meta.Id)
			continue
		}
		if rec == nil {
			if *budget > 0 && (meta.Discovered || completed) {
				*budget--
				if meta.Discovered {
					server.adoptDiscoveredBackup(meta)
				} else {
					server.adoptOwnBackup(meta)
				}
			}
			continue
		}
		if artifactUnchangedBounded(meta.Dest, rec, backupProofWalkMax) {
			cluster.backupChanges.Delete(meta.Id)
			continue
		}
		change := &BackupRegistrationChange{Server: server.URL, ID: meta.Id, Dest: meta.Dest, Was: *rec, Checked: time.Now()}
		if now, err := restoreArtifactFingerprintBounded(meta.Dest, backupProofWalkMax); err != nil {
			change.Error = err.Error()
		} else {
			change.Now = now
		}
		change.Describe = describeBackupChange(meta.Id, change)
		cluster.backupChanges.Store(meta.Id, change)
	}
}

// adoptOwnBackup records the fingerprint of a backup replication-manager took before backups
// recorded one, as its file is now, in place, and rewrites its metadata file. Its file absent
// (taken on a DB node, kept elsewhere), still settling or verified: left to a later check.
func (server *ServerMonitor) adoptOwnBackup(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if _, verifying := cluster.backupVerifying.Load(meta.Id); verifying || !fileExists(meta.Dest) {
		return
	}
	if changed, err := artifactChangedSince(meta.Dest, time.Now().Add(-backupDiscoverySettle)); err != nil || changed {
		return
	}
	fp, err := restoreArtifactFingerprintBounded(meta.Dest, backupProofWalkMax)
	if err != nil || fp.ChangeTime.IsZero() {
		return
	}
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	meta.Registration = &backupmgr.BackupRegistration{Artifact: *fp, RegisteredAt: time.Now(), Adopted: true}
	metaPath := server.backupMetaFilePath(meta)
	data, err := json.MarshalIndent(meta, "", "\t")
	if err != nil || metaPath == "" {
		return
	}
	tmp := metaPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup %d of %s: fingerprint recorded, metadata file not written: %s", meta.Id, server.URL, err)
		return
	}
	if err := os.Rename(tmp, metaPath); err != nil {
		os.Remove(tmp)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup %d of %s taken before fingerprints were recorded: its fingerprint recorded as its file is now (%s)", meta.Id, server.URL, humanBytes(fp.Size))
}

// adoptDiscoveredBackup records the fingerprint of a backup registered by a release that
// registered every dump it found, as its file is now, and loads it as Full; one that is not a
// backup (the cheap checks) is flagged, listed with Unregister, never used. Not while it is
// verified: the next check adopts it.
func (server *ServerMonitor) adoptDiscoveredBackup(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if _, verifying := cluster.backupVerifying.Load(meta.Id); verifying {
		return
	}
	if changed, err := artifactChangedSince(meta.Dest, time.Now().Add(-backupDiscoverySettle)); err != nil || changed {
		return
	}
	fp, err := restoreArtifactFingerprintBounded(meta.Dest, backupProofWalkMax)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: backup %d not adopted: %s", server.URL, meta.Id, err)
		return
	}
	server.backupMetaMutex.Lock() // a verification writes its fields under it
	adopted := *meta
	server.backupMetaMutex.Unlock()
	adopted.BackupStrategy = backupmgr.BackupStrategyFull
	adopted.Registration = &backupmgr.BackupRegistration{Artifact: *fp, RegisteredAt: time.Now(), Adopted: true}
	if adopted.TimeSource == "" && adopted.StartTime.Equal(adopted.EndTime) {
		adopted.TimeSource = backupmgr.BackupTimeSourceFileTime
	}
	if status, reason, _, _ := cluster.checkBackupCandidate(meta.Dest, meta.BackupTool, meta.SplitDump); status == BackupCandidateNotBackup {
		adopted.NotABackup = true
		adopted.Completed = false
		adopted.DiscoveryNote = reason
	}
	if err := server.writeDiscoveredBackupMeta(&adopted, meta); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: backup %d not adopted: %s", server.URL, meta.Id, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: backup %d registered by a previous release adopted, its fingerprint recorded (%s)%s", server.URL, meta.Id, humanBytes(fp.Size), map[bool]string{true: ": not a backup, " + adopted.DiscoveryNote, false: ""}[adopted.NotABackup])
}

// isDuplicateScheduledBackup reports whether a discovered record describes one of
// replication-manager's scheduled backup files (scheduledLogicalBackupNames in its server's
// backup folder): a release that registered every dump it found (3.1.44) may have registered
// one, and the scheduled backups rewrite it. Such a record describes a file that is not a
// discovered backup: it is never used, and removeScheduledBackupRecord removes it. By name, so
// it holds whichever tool or mode wrote the file last; no file is read.
func (cluster *Cluster) isDuplicateScheduledBackup(meta *backupmgr.BackupMetadata) bool {
	if cluster == nil || meta == nil || !meta.Discovered {
		return false
	}
	server := cluster.GetServerFromURL(meta.Source)
	return server != nil && server.isScheduledBackupArtifact(meta.Dest)
}

// scheduledLogicalBackupNames are the files the scheduled logical backups write in a
// server's backup folder (their encrypted forms too), whatever the tool or the mode: the
// fixed destinations of the logical reseed (JobReseedLogicalBackup) and of
// resolveMysqldumpDest.
var scheduledLogicalBackupNames = map[string]bool{"mysqldump.sql.gz": true, "splitdump": true, "mydumper": true, "dumpling": true}

// isScheduledBackupArtifact tells whether path is one of the server's scheduled backup files.
func (server *ServerMonitor) isScheduledBackupArtifact(path string) bool {
	if server == nil || server.ClusterGroup == nil || strings.TrimSpace(path) == "" {
		return false
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) != filepath.Clean(server.GetMyBackupDirectoryPath()) {
		return false
	}
	return isScheduledBackupName(filepath.Base(path))
}

// isScheduledBackupName tells whether name, an entry of a backup folder, is a scheduled
// backup file or one of its encrypted forms.
func isScheduledBackupName(name string) bool {
	for _, suffix := range []string{".tar.enc", ".enc"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return scheduledLogicalBackupNames[name]
}

// removeScheduledBackupRecord removes a discovered record of a scheduled backup file
// (isDuplicateScheduledBackup): its metadata file, replication-manager's own, and its entry.
// The file is the scheduled backup's and is left as it is. Not while it is verified: the next
// check removes it. The caller holds the server's backupDiscoveries guard.
func (server *ServerMonitor) removeScheduledBackupRecord(meta *backupmgr.BackupMetadata) {
	cluster := server.ClusterGroup
	if _, verifying := cluster.backupVerifying.Load(meta.Id); verifying {
		return
	}
	if err := os.Remove(server.backupMetaFilePath(meta)); err != nil && !os.IsNotExist(err) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Backup discovery on %s: record %d of the scheduled backup file %s not removed: %s", server.URL, meta.Id, meta.Dest, err)
		return
	}
	cluster.BackupMetaMap.Delete(meta.Id)
	cluster.backupChanges.Delete(meta.Id)
	cluster.backupRegisterResults.Delete(meta.Dest)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Backup discovery on %s: discovered record %d removed, %s is a scheduled backup file (registered by a previous release); the file is left as it is", server.URL, meta.Id, meta.Dest)
}

func backupArtifactsMatch(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return false
	}
	known := map[string]bool{}
	addKnownBackupArtifact(known, left)
	return known[filepath.Clean(right)]
}

// describeBackupChange says what changed in a registered backup's file: its size, its files,
// its modification time, or the file itself (replaced, its size and time put back).
func describeBackupChange(id int64, c *BackupRegistrationChange) string {
	if c.Now == nil {
		return fmt.Sprintf("backup %d %s: %s", id, filepath.Base(c.Dest), c.Error)
	}
	was, now := c.Was, c.Now
	var parts []string
	if was.Size != now.Size {
		parts = append(parts, fmt.Sprintf("%s -> %s", humanBytes(was.Size), humanBytes(now.Size)))
	}
	if was.Files != now.Files {
		parts = append(parts, fmt.Sprintf("files %d -> %d", was.Files, now.Files))
	}
	if !was.ModTime.Equal(now.ModTime) {
		parts = append(parts, fmt.Sprintf("modified %s", now.ModTime.Local().Format("2006-01-02 15:04")))
	}
	if len(parts) == 0 {
		if was.ChangeTime.IsZero() {
			parts = append(parts, "its registration does not identify the file (no inode, change time)")
		} else {
			parts = append(parts, fmt.Sprintf("replaced or rewritten, same size and modification time (changed %s)", now.ChangeTime.Local().Format("2006-01-02 15:04")))
		}
	}
	return fmt.Sprintf("backup %d %s changed since registered: %s", id, filepath.Base(c.Dest), strings.Join(parts, ", "))
}

// checkBackupDiscoveryStates, every tick, from memory only: WARN0249 for each server with a
// registered backup changed since registered, WARN0250 for each server whose last discovery
// dropped candidates beyond backup-discovery-candidate-max.
func (cluster *Cluster) checkBackupDiscoveryStates() {
	changed := map[string][]string{}
	cluster.backupChanges.Range(func(k, v any) bool {
		c := v.(*BackupRegistrationChange)
		if cluster.BackupMetaMap == nil || cluster.BackupMetaMap.Get(c.ID) == nil {
			cluster.backupChanges.Delete(k) // the backup was deleted since
			return true
		}
		changed[c.Server] = append(changed[c.Server], c.Describe)
		return true
	})
	for server, descs := range changed {
		sort.Strings(descs)
		cluster.SetState("WARN0249", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0249"], server, strings.Join(descs, "; ")), ErrFrom: "BACKUP", ServerUrl: server})
	}
	cluster.backupCandidates.Range(func(_, v any) bool {
		if set := v.(*backupCandidateSet); set.Dropped > 0 {
			cluster.SetState("WARN0250", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0250"], set.Server, set.Dropped, cluster.backupDiscoveryCandidateMax()), ErrFrom: "BACKUP", ServerUrl: set.Server})
		}
		return true
	})
}
