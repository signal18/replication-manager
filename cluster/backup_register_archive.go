// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file archives a registered backup on the operator's request (#2108): a registered
// backup never goes through a backup job, whose end pushes the backup to the archive. The
// push is queued in the restic task queue; the removal of the original and a test restore
// first are opt-in, asked with the archive. It also holds what a registered backup's
// verifications decide: the manual reseed from it requires it proven
// (backup-verification-registered-proven), and a failed verification of its current file is
// WARN0253.

package cluster

import (
	"errors"
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

var (
	// ErrBackupNotProven: a registered backup is reseeded from once its test restore passed
	// (backup-verification-registered-proven).
	ErrBackupNotProven = errors.New("not proven: a registered backup is reseeded from once a test restore of its file passed (backup-verification-registered-proven)")
	// ErrBackupVerificationFailed: the last verification of a registered backup's current
	// file failed.
	ErrBackupVerificationFailed = errors.New("its last verification failed")
	// ErrBackupArchived: the backup already has its archive snapshot.
	ErrBackupArchived = errors.New("already archived")
	// ErrBackupArchiveUnavailable: no archive to push to (backup-restic off, or its manager
	// not started).
	ErrBackupArchiveUnavailable = errors.New("no archive: backup-restic is off or restic is not started")
	// ErrBackupArchiving: an archive of the backup already runs (its test restore or its push).
	ErrBackupArchiving = errors.New("the backup is already being archived")
)

// backupVerificationOf returns a copy of the last verification of meta, read under its
// server's lock; nil when never verified.
func (cluster *Cluster) backupVerificationOf(meta *backupmgr.BackupMetadata) *backupmgr.BackupVerification {
	if server := cluster.GetServerFromURL(meta.Source); server != nil {
		return server.GetBackupVerification(meta)
	}
	return nil
}

// registeredVerificationFailed tells whether v, the last verification of meta, a registered
// backup, failed on its current file: ended after the file was registered (a registered
// change of the file registers it again).
func registeredVerificationFailed(meta *backupmgr.BackupMetadata, v *backupmgr.BackupVerification) bool {
	return meta.Discovered && meta.Registration != nil && v != nil && v.Status == backupmgr.BackupVerificationFailed && !v.EndTime.Before(meta.Registration.RegisteredAt)
}

// registeredBackupProven tells whether v, the last verification of meta, a registered
// backup, is a passed test restore of its current file: Proven, as the restore selector
// means it for the automatic rejoin (backupProven).
func registeredBackupProven(meta *backupmgr.BackupMetadata, v *backupmgr.BackupVerification) bool {
	return meta.Discovered && meta.Registration != nil && v != nil && backupProven(&backupmgr.BackupMetadata{Verification: v}) && artifactFingerprintsEqual(v.Artifact, &meta.Registration.Artifact)
}

// registeredReseedRefusal tells why the manual reseed from meta is refused by its
// verifications: a registered backup whose last verification of its current file failed,
// or not proven while backup-verification-registered-proven. nil for the backups
// replication-manager took.
func (cluster *Cluster) registeredReseedRefusal(meta *backupmgr.BackupMetadata) error {
	if meta == nil || !meta.Discovered {
		return nil
	}
	v := cluster.backupVerificationOf(meta)
	if registeredVerificationFailed(meta, v) {
		return fmt.Errorf("%w (%s level: %s): verify it again, register a fixed file or unregister it", ErrBackupVerificationFailed, v.Level, v.Error)
	}
	if cluster.Conf.BackupVerificationRegisteredProven && !registeredBackupProven(meta, v) {
		return ErrBackupNotProven
	}
	return nil
}

// backupArchived tells whether meta has its archive snapshot.
func (cluster *Cluster) backupArchived(meta *backupmgr.BackupMetadata) bool {
	if server := cluster.GetServerFromURL(meta.Source); server != nil {
		server.backupMetaMutex.Lock()
		defer server.backupMetaMutex.Unlock()
	}
	return meta.ResticSnapshotID != "" || meta.OriginalRemoved
}

// archiveRefusal tells why meta, a registered backup, can not be pushed to the archive now;
// nil when it can. testFirst: a test restore runs before the push, a failed verification
// does not refuse it.
func (cluster *Cluster) archiveRefusal(meta *backupmgr.BackupMetadata, testFirst bool) error {
	if !meta.Discovered {
		return fmt.Errorf("%w: %d", ErrBackupNotDiscovered, meta.Id)
	}
	if !cluster.Conf.BackupRestic || cluster.ResticManager == nil {
		return ErrBackupArchiveUnavailable
	}
	if cluster.backupArchived(meta) {
		return fmt.Errorf("%w: backup %d", ErrBackupArchived, meta.Id)
	}
	if err := cluster.backupUseRefusal(meta); err != nil {
		return err
	}
	if err := cluster.backupFileLocked(meta.Dest); err != nil {
		return err
	}
	if _, verifying := cluster.backupVerifying.Load(meta.Id); verifying {
		return fmt.Errorf("%w: %d", ErrBackupVerificationRunning, meta.Id)
	}
	if _, running := cluster.backupDiscoveries.Load(meta.Source); running {
		return ErrBackupDiscoveryRunning
	}
	if !testFirst && registeredVerificationFailed(meta, cluster.backupVerificationOf(meta)) {
		return fmt.Errorf("%w: test it again with the archive, register a fixed file or unregister it", ErrBackupVerificationFailed)
	}
	return nil
}

// ArchiveBackup queues the push of a registered backup's file in the restic task queue, its
// snapshot recorded in the backup. removeOriginal: the original is removed once the snapshot
// holds it (removeArchivedOriginal, its checks), whatever backup-archive-remove-original.
// testFirst: a test restore of the backup runs first, on backup-verification-host, and the
// push is queued only when it passes. It returns at once: refused when the backup can not
// be archived now.
func (cluster *Cluster) ArchiveBackup(id int64, removeOriginal, testFirst bool) error {
	meta := cluster.BackupMetaMap.Get(id)
	if meta == nil {
		return fmt.Errorf("%w: %d", ErrBackupNotFound, id)
	}
	server := cluster.GetServerFromURL(meta.Source)
	if server == nil {
		return fmt.Errorf("%w: server %s not found", ErrBackupNotFound, meta.Source)
	}
	if testFirst {
		if strings.TrimSpace(cluster.Conf.BackupVerificationHost) == "" {
			return fmt.Errorf("%w: set backup-verification-host for a test restore", ErrBackupNotVerifiable)
		}
		if err := cluster.verifiableLogicalBackup(meta); err != nil {
			return err
		}
	}
	// one archive of a backup at a time, from the request to the end of its push
	if _, running := cluster.backupArchiving.LoadOrStore(id, struct{}{}); running {
		return fmt.Errorf("%w: %d", ErrBackupArchiving, id)
	}
	if err := cluster.archiveRefusal(meta, testFirst); err != nil {
		cluster.backupArchiving.Delete(id)
		return err
	}
	if !testFirst {
		cluster.pushRegisteredBackup(server, meta, removeOriginal)
		return nil
	}
	start, err := cluster.verifyBackupByIDRunThen(id, config.ConstBackupVerificationLevelRestore, func() {
		v := server.GetBackupVerification(meta)
		if v == nil || v.Status != backupmgr.BackupVerificationPassed {
			cluster.backupArchiving.Delete(id)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Archive of backup %d on %s not queued: its test restore did not pass", id, server.URL)
			return
		}
		// the test restore took time: what it allowed then is checked again
		if err := cluster.archiveRefusal(meta, false); err != nil {
			cluster.backupArchiving.Delete(id)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Archive of backup %d on %s not queued after its test restore: %s", id, server.URL, err)
			return
		}
		cluster.pushRegisteredBackup(server, meta, removeOriginal)
	})
	if err != nil {
		cluster.backupArchiving.Delete(id)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive of backup %d on %s: test restore (run %s) first", id, server.URL, start.RunID)
	return nil
}

// pushRegisteredBackup queues the push of meta's file in the restic task queue. A
// registered file has its own name, no backup rewrites it: the push does not hold the
// in-archive flag a backup waits for, the queue orders it after the pushes before it.
func (cluster *Cluster) pushRegisteredBackup(server *ServerMonitor, meta *backupmgr.BackupMetadata, removeOriginal bool) {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Archive of backup %d on %s: push of %s queued (remove original: %t)", meta.Id, server.URL, meta.Dest, removeOriginal)
	tags := server.BuildResticTags("logical", meta.BackupTool, meta.BackupLine, meta)
	server.backupResticWith(backupmgr.BackupMethodLogical, meta, meta.Dest, resticPushOptions{
		removeOriginal: func() bool { return removeOriginal },
		asked:          true,
		noFlag:         true,
		done:           func(error) { cluster.backupArchiving.Delete(meta.Id) },
	}, tags...)
}

// checkRegisteredVerificationStates, every tick: WARN0253 for each registered backup whose
// last verification of its current file failed. It goes with a passed verification, a
// registered change of the file, or an unregister.
func (cluster *Cluster) checkRegisteredVerificationStates() {
	if cluster.BackupMetaMap == nil {
		return
	}
	var metas []*backupmgr.BackupMetadata
	cluster.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && meta.Discovered {
			metas = append(metas, meta)
		}
		return true
	})
	for _, meta := range metas {
		if v := cluster.backupVerificationOf(meta); registeredVerificationFailed(meta, v) {
			cluster.SetState("WARN0253", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0253"], meta.Id, meta.Source, v.Level, v.Error), ErrFrom: "BACKUP", ServerUrl: meta.Source})
		}
	}
}
