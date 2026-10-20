// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/shirou/gopsutil/disk"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/sirupsen/logrus"
)

// backup-archive-remove-original (#1987): once the archive snapshot of a logical or
// physical backup is verified to hold it, the original artifact is removed from the server
// backup directory. The meta file stays: it carries the snapshot a restore fetches it from.

// removeArchivedOriginal removes the original artifact of meta once snapshotID is verified
// to hold it. Any doubt keeps the original and is reported as WARN0238 until a later
// backup of the method is removed.
func (server *ServerMonitor) removeArchivedOriginal(backtype backupmgr.BackupMethod, meta *backupmgr.BackupMetadata, snapshotID string) {
	server.removeArchivedOriginalAsked(backtype, meta, snapshotID, false)
}

// removeArchivedOriginalAsked is removeArchivedOriginal; asked: the removal was asked with
// the archive of this backup (ArchiveBackup), its refusal reported whatever
// backup-archive-remove-original.
func (server *ServerMonitor) removeArchivedOriginalAsked(backtype backupmgr.BackupMethod, meta *backupmgr.BackupMetadata, snapshotID string, asked bool) {
	cluster := server.ClusterGroup
	if meta == nil || snapshotID == "" {
		return
	}
	cluster.waitBackupVerificationRead(meta.Id)

	server.backupMetaMutex.Lock()
	current := server.lastBackupMetaForMethod(backtype)
	dest := meta.Dest
	completed := meta.Completed
	removed := meta.OriginalRemoved
	encrypted := meta.Encrypted || isEncryptedBackupArtifact(dest)
	server.backupMetaMutex.Unlock()

	// A scheduled backup reuses its name on the next run: once a newer backup of the
	// method exists, Dest is no longer this backup's artifact.
	if meta != current && !meta.IsAdhoc() {
		return
	}
	if removed || dest == "" || !fileExists(dest) {
		return
	}
	if reason := server.archivedOriginalRefusal(dest, completed, encrypted, snapshotID); reason != "" {
		server.setArchiveRemoveOriginalRefusal(backtype, dest, reason, asked)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Keeping original backup %s of %s: %s", dest, server.URL, reason)
		return
	}

	var err error
	if encrypted {
		err = cluster.removeBackupArtifactWithSidecar(dest)
	} else {
		err = os.RemoveAll(dest)
	}
	if err != nil {
		reason := fmt.Sprintf("remove %s: %s", dest, err)
		server.setArchiveRemoveOriginalRefusal(backtype, dest, reason, asked)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Keeping original backup of %s: %s", server.URL, reason)
		return
	}
	server.setArchiveRemoveOriginalRefusal(backtype, "", "", false)

	server.backupMetaMutex.Lock()
	meta.OriginalRemoved = true
	if meta == server.lastBackupMetaForMethod(backtype) || meta.IsAdhoc() {
		if werr := server.writeBackupMetaFileLocked(meta); werr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Failed to write metadata of archived backup in %s: %s", server.URL, werr)
		}
	}
	server.backupMetaMutex.Unlock()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Removed original backup %s of %s, archived in snapshot %s", dest, server.URL, resticLogSnapshotID(cluster, snapshotID))
	cluster.UpdateDiskStat(server.GetMyBackupDirectoryPath())
}

// archivedOriginalRefusal returns why the original at dest must be kept, "" when the
// snapshot is verified to hold it.
func (server *ServerMonitor) archivedOriginalRefusal(dest string, completed, encrypted bool, snapshotID string) string {
	cluster := server.ClusterGroup
	if !completed {
		return "the backup is not completed"
	}
	if base := server.GetMyBackupDirectoryPath(); !isStrictlyWithinBase(base, dest) {
		return fmt.Sprintf("%s is not an entry of the backup directory %s", dest, base)
	}
	paths := []string{dest}
	if encrypted {
		paths = append(paths, backupmgr.IntegritySidecarPath(dest))
	}
	missing, err := cluster.snapshotMissingPath(snapshotID, paths)
	if err != nil {
		return fmt.Sprintf("cannot list snapshot %s: %s", resticLogSnapshotID(cluster, snapshotID), err)
	}
	if missing != "" {
		return fmt.Sprintf("snapshot %s does not hold %s", resticLogSnapshotID(cluster, snapshotID), missing)
	}
	return ""
}

// isStrictlyWithinBase reports whether target lies below baseDir, never baseDir itself:
// the original removed or fetched is one entry of the backup directory, and removing the
// directory would take every backup, binary log copy and meta file of the server with it.
func isStrictlyWithinBase(baseDir, target string) bool {
	if !isPathWithinBase(baseDir, target) {
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
	return filepath.Clean(baseAbs) != filepath.Clean(targetAbs)
}

// snapshotMissingPath lists paths in the snapshot with one restic ls and returns the first
// that it does not hold, "" when it holds them all.
func (cluster *Cluster) snapshotMissingPath(snapshotID string, paths []string) (string, error) {
	if cluster.ResticManager == nil {
		return "", fmt.Errorf("restic manager not initialized")
	}
	entries, err := cluster.ResticManager.ListSnapshotWithLogLevel(snapshotID, paths, true, logrus.DebugLevel)
	if err != nil {
		return "", err
	}
	for _, path := range paths {
		path = filepath.Clean(path)
		found := false
		for _, e := range entries {
			if e.Path == path || strings.HasPrefix(e.Path, path+"/") {
				found = true
				break
			}
		}
		if !found {
			return path, nil
		}
	}
	return "", nil
}

// archiveRemoveOriginalRefusal is why the original at Dest was kept after its archive push.
type archiveRemoveOriginalRefusal struct {
	Dest   string
	Reason string
	// Asked: the removal was asked with the archive of one backup (ArchiveBackup), not by
	// backup-archive-remove-original, and is reported whatever that switch
	Asked bool
}

func (server *ServerMonitor) setArchiveRemoveOriginalRefusal(backtype backupmgr.BackupMethod, dest, reason string, asked bool) {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	refusal := archiveRemoveOriginalRefusal{Dest: dest, Reason: reason, Asked: asked}
	switch backtype {
	case backupmgr.BackupMethodLogical:
		server.archiveRemoveOriginalRefusalLogical = refusal
	case backupmgr.BackupMethodPhysical:
		server.archiveRemoveOriginalRefusalPhysical = refusal
	}
}

// checkArchiveRemoveOriginalStates, every tick: WARN0238 for each server whose last
// original could not be removed after its archive push, while backup-archive-remove-original
// is on, or asked with the archive of that backup (ArchiveBackup). A refusal is dropped when
// the switch is off (one not asked), and once its original is no longer on disk (deleted,
// purged, or removed by a later run): the state follows what is observed.
func (cluster *Cluster) checkArchiveRemoveOriginalStates() {
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		s.backupMetaMutex.Lock()
		var reasons []string
		for _, r := range []struct {
			method  string
			refusal *archiveRemoveOriginalRefusal
		}{{"logical", &s.archiveRemoveOriginalRefusalLogical}, {"physical", &s.archiveRemoveOriginalRefusalPhysical}} {
			if r.refusal.Reason == "" {
				continue
			}
			if (!cluster.Conf.BackupArchiveRemoveOriginal && !r.refusal.Asked) || !fileExists(r.refusal.Dest) {
				*r.refusal = archiveRemoveOriginalRefusal{}
				continue
			}
			reasons = append(reasons, r.method+": "+r.refusal.Reason)
		}
		s.backupMetaMutex.Unlock()
		if len(reasons) > 0 {
			cluster.SetState("WARN0238", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0238"], s.URL, strings.Join(reasons, "; ")), ErrFrom: "BACKUP", ServerUrl: s.URL})
		}
	}
}

// restoreArchivedOriginal brings back the original of src's last backup of a method when
// backup-archive-remove-original removed it, so the restore paths find it where they look.
// tool, when set, must match the backup's tool. It returns whether that backup lives in the
// archive: the caller then keeps its backup cookie even when the fetch failed. The restored
// original stays until the next backup of the method replaces it and removes it after its
// own archive push, as it was before the removal. Only a restic archive is fetched from
// (archiveFetchable): a plugin archive's snapshot is no restic snapshot.
func (cluster *Cluster) restoreArchivedOriginal(src *ServerMonitor, backtype backupmgr.BackupMethod, tool string) bool {
	if src == nil || !cluster.archiveFetchable() {
		return false
	}
	src.backupMetaMutex.Lock()
	meta := src.lastBackupMetaForMethod(backtype)
	var dest, snapshotID, metaTool string
	var size int64
	var encrypted bool
	if meta != nil && meta.OriginalRemoved {
		dest, snapshotID, metaTool, size = meta.Dest, cluster.attachedSnapshot(meta), meta.BackupTool, meta.Size
		encrypted = meta.Encrypted || isEncryptedBackupArtifact(dest)
	}
	src.backupMetaMutex.Unlock()
	if dest == "" || snapshotID == "" || (tool != "" && metaTool != "" && tool != metaTool) {
		return false
	}

	// One fetch at a time per server: a second caller waits here, then finds dest present.
	// dest only ever appears whole (rename out of the work directory), so a present dest
	// is a complete artifact, never a leftover of an interrupted fetch.
	src.archiveRestoreMutex.Lock()
	defer src.archiveRestoreMutex.Unlock()
	if fileExists(dest) {
		return true
	}
	if err := src.fetchArchivedOriginal(dest, snapshotID, size, encrypted); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Cannot fetch backup %s of %s from snapshot %s: %s", dest, src.URL, resticLogSnapshotID(cluster, snapshotID), err)
		return true
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Fetched backup %s of %s from snapshot %s", dest, src.URL, resticLogSnapshotID(cluster, snapshotID))
	return true
}

// fetchArchivedOriginal restores dest (and the integrity sidecar of an encrypted artifact)
// from the snapshot into a work directory beside it, then moves it to its name: the backup
// directory itself is never the restore target, whose permissions restic rewrites.
func (server *ServerMonitor) fetchArchivedOriginal(dest, snapshotID string, size int64, encrypted bool) error {
	cluster := server.ClusterGroup
	base := server.GetMyBackupDirectory()
	if !isStrictlyWithinBase(base, dest) {
		return fmt.Errorf("%s is not an entry of the backup directory %s", dest, base)
	}
	// Measured now, not from the cached disk stats; no measure, no fetch (F3).
	if size > 0 {
		usage, err := disk.Usage(base)
		if err != nil {
			return fmt.Errorf("free space of %s: %w", base, err)
		}
		if usage.Free < uint64(size) {
			return fmt.Errorf("%d bytes free in %s, the backup needs %d", usage.Free, base, size)
		}
	}
	paths := []string{dest}
	if encrypted {
		paths = append(paths, backupmgr.IntegritySidecarPath(dest))
	}
	work, err := os.MkdirTemp(base, strings.TrimSuffix(archiveRestoreWorkDirPattern, "*"))
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	// restic reads --include as a pattern: escape the glob characters a path may hold.
	include := make([]string, len(paths))
	for i, path := range paths {
		include[i] = escapeResticPattern(path)
	}
	if err := cluster.ResticManager.RestoreSnapshotSyncWithOptions(backupmgr.ResticRestoreOption{SnapshotID: snapshotID, TargetDir: work, Include: include}); err != nil {
		return err
	}
	// Sidecar first: an artifact never appears without it.
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Rename(filepath.Join(work, paths[i]), paths[i]); err != nil {
			return err
		}
	}
	return nil
}

// escapeResticPattern makes path match itself only as a restic include/exclude pattern.
func escapeResticPattern(path string) string {
	var b strings.Builder
	for _, r := range path {
		switch r {
		case '\\', '*', '?', '[', ']':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// archiveRestoreWorkDirPattern excludes a fetch work directory from the scheduled push,
// which snapshots the whole backup directory.
const archiveRestoreWorkDirPattern = ".archive-restore-*"

// fetchPickedBackup fetches pick, a backup the restore selection chose that is only in the
// attached archive, back into its server's backup directory, at the path the reseed reads
// (pick.Path). An error says why it cannot be used (#2091).
func (cluster *Cluster) fetchPickedBackup(pick *BackupCatalogEntry, method backupmgr.BackupMethod, tool string) error {
	archive := cluster.Conf.AttachedArchive()
	if !cluster.archiveFetchable() {
		return fmt.Errorf("backup %d of %s is only in archive %s (snapshot %s): fetching a backup back from this archive is not supported yet", pick.ID, pick.Server, archive, pick.Snapshot)
	}
	src := cluster.GetServerFromURL(pick.Server)
	if src == nil {
		return fmt.Errorf("backup %d of %s is only in archive %s: its server is no longer monitored", pick.ID, pick.Server, archive)
	}
	cluster.restoreArchivedOriginal(src, method, tool)
	if !fileExists(pick.Path) {
		return fmt.Errorf("backup %d of %s could not be fetched back from archive %s (snapshot %s) to %s", pick.ID, pick.Server, archive, pick.Snapshot, pick.Path)
	}
	return nil
}
