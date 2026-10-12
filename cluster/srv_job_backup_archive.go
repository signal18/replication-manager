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

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

// backup-archive-remove-original (#1987): once the archive snapshot of a logical or
// physical backup is verified to hold it, the original artifact is removed from the server
// backup directory. The meta file stays: it carries the snapshot a restore fetches it from.

// removeArchivedOriginal removes the original artifact of meta once snapshotID is verified
// to hold it. Any doubt keeps the original and is reported as WARN0238 until a later
// backup of the method is removed.
func (server *ServerMonitor) removeArchivedOriginal(backtype backupmgr.BackupMethod, meta *backupmgr.BackupMetadata, snapshotID string) {
	cluster := server.ClusterGroup
	if meta == nil || snapshotID == "" {
		return
	}

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
		server.setArchiveRemoveOriginalRefusal(backtype, reason)
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
		server.setArchiveRemoveOriginalRefusal(backtype, reason)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlErr, "Keeping original backup of %s: %s", server.URL, reason)
		return
	}
	server.setArchiveRemoveOriginalRefusal(backtype, "")

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
	if base := server.GetMyBackupDirectoryPath(); !isPathWithinBase(base, dest) {
		return fmt.Sprintf("%s is outside the backup directory %s", dest, base)
	}
	paths := []string{dest}
	if encrypted {
		paths = append(paths, backupmgr.IntegritySidecarPath(dest))
	}
	for _, path := range paths {
		found, err := cluster.snapshotPathExists(snapshotID, path)
		if err != nil {
			return fmt.Sprintf("cannot list snapshot %s: %s", resticLogSnapshotID(cluster, snapshotID), err)
		}
		if !found {
			return fmt.Sprintf("snapshot %s does not hold %s", resticLogSnapshotID(cluster, snapshotID), path)
		}
	}
	return ""
}

func (server *ServerMonitor) setArchiveRemoveOriginalRefusal(backtype backupmgr.BackupMethod, reason string) {
	server.backupMetaMutex.Lock()
	defer server.backupMetaMutex.Unlock()
	switch backtype {
	case backupmgr.BackupMethodLogical:
		server.archiveRemoveOriginalRefusalLogical = reason
	case backupmgr.BackupMethodPhysical:
		server.archiveRemoveOriginalRefusalPhysical = reason
	}
}

// checkArchiveRemoveOriginalStates, every tick: WARN0238 for each server whose last
// original could not be removed after its archive push, while backup-archive-remove-original
// is on.
func (cluster *Cluster) checkArchiveRemoveOriginalStates() {
	if !cluster.Conf.BackupArchiveRemoveOriginal {
		return
	}
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		s.backupMetaMutex.Lock()
		var reasons []string
		if r := s.archiveRemoveOriginalRefusalLogical; r != "" {
			reasons = append(reasons, "logical: "+r)
		}
		if r := s.archiveRemoveOriginalRefusalPhysical; r != "" {
			reasons = append(reasons, "physical: "+r)
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
// own archive push, as it was before the removal.
func (cluster *Cluster) restoreArchivedOriginal(src *ServerMonitor, backtype backupmgr.BackupMethod, tool string) bool {
	if src == nil || cluster.ResticManager == nil {
		return false
	}
	src.backupMetaMutex.Lock()
	meta := src.lastBackupMetaForMethod(backtype)
	var dest, snapshotID, metaTool string
	var size int64
	var encrypted bool
	if meta != nil && meta.OriginalRemoved {
		dest, snapshotID, metaTool, size = meta.Dest, meta.ResticSnapshotID, meta.BackupTool, meta.Size
		encrypted = meta.Encrypted || isEncryptedBackupArtifact(dest)
	}
	src.backupMetaMutex.Unlock()
	if dest == "" || snapshotID == "" || (tool != "" && metaTool != "" && tool != metaTool) {
		return false
	}

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
	if !isPathWithinBase(base, dest) {
		return fmt.Errorf("%s is outside the backup directory %s", dest, base)
	}
	if size > 0 {
		if stat, err := cluster.GetDiskStat(base); err == nil && stat.Free < uint64(size) {
			return fmt.Errorf("%d bytes free in %s, the backup needs %d", stat.Free, base, size)
		}
	}
	paths := []string{dest}
	if encrypted {
		paths = append(paths, backupmgr.IntegritySidecarPath(dest))
	}
	work, err := os.MkdirTemp(base, ".archive-restore-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if err := cluster.ResticManager.RestoreSnapshotSyncWithOptions(backupmgr.ResticRestoreOption{SnapshotID: snapshotID, TargetDir: work, Include: paths}); err != nil {
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
