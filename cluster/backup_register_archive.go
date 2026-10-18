// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file holds what a registered backup's verifications decide (#2108): the manual reseed
// from it requires it proven (backup-verification-registered-proven), and a failed
// verification of its current file is WARN0253.

package cluster

import (
	"errors"
	"fmt"

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
