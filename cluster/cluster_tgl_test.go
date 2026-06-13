// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/version"
)

// newArchiveModeTestCluster builds a minimal Cluster sufficient to exercise
// SetBackupArchiveMode's side effects (CheckResticInstallation,
// StartResticManager/ClearSnapshotList, ReloadResticEnv) without touching a
// real restic binary. The "restic" entry in VersionsMap makes
// CheckResticInstallation skip RefreshResticVersion, which would otherwise
// shell out to backup-restic-binary-path.
func newArchiveModeTestCluster(t *testing.T) *Cluster {
	cluster := &Cluster{
		Name: "cluster1",
		Conf: &config.Config{
			WorkingDir: t.TempDir(),
		},
		VersionsMap: config.NewVersionsMap(),
	}
	v, _ := version.NewVersion("restic", 0, 17, 0)
	cluster.VersionsMap.Set("restic", v)
	return cluster
}

// TestSetBackupArchiveModeNoneToResticSftp covers the none -> restic-sftp
// transition, which flips backup-restic on while ResticManager is still nil,
// so SetBackupArchiveMode must take the StartResticManager branch.
func TestSetBackupArchiveModeNoneToResticSftp(t *testing.T) {
	cluster := newArchiveModeTestCluster(t)
	cluster.Conf.BackupArchiveMode = config.ConstBackupArchiveModeNone
	cluster.Conf.BackupResticLocalRepository = "sftp:backup@10.0.0.1:/srv/restic-repo"

	if err := cluster.SetBackupArchiveMode(config.ConstBackupArchiveModeResticSftp); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cluster.Conf.BackupArchiveMode != config.ConstBackupArchiveModeResticSftp {
		t.Fatalf("BackupArchiveMode = %q, want %q", cluster.Conf.BackupArchiveMode, config.ConstBackupArchiveModeResticSftp)
	}
	if !cluster.Conf.BackupRestic {
		t.Fatalf("expected BackupRestic=true")
	}
	if cluster.Conf.BackupResticAws {
		t.Fatalf("expected BackupResticAws=false")
	}
	if cluster.ResticManager == nil {
		t.Fatalf("expected StartResticManager to set ResticManager")
	}
}

// TestSetBackupArchiveModeResticLocalToNone covers the restic-local -> none
// transition, which flips backup-restic off while a ResticManager already
// exists, so SetBackupArchiveMode must take the ClearSnapshotList branch
// rather than replacing the manager via StartResticManager.
func TestSetBackupArchiveModeResticLocalToNone(t *testing.T) {
	cluster := newArchiveModeTestCluster(t)
	cluster.Conf.BackupArchiveMode = config.ConstBackupArchiveModeResticLocal
	cluster.Conf.BackupRestic = true

	existingManager := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	existingManager.UpdateSnapshotList([]backupmgr.BackupSnapshot{{Id: "snap-1"}})
	cluster.ResticManager = existingManager

	if err := cluster.SetBackupArchiveMode(config.ConstBackupArchiveModeNone); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cluster.Conf.BackupArchiveMode != config.ConstBackupArchiveModeNone {
		t.Fatalf("BackupArchiveMode = %q, want %q", cluster.Conf.BackupArchiveMode, config.ConstBackupArchiveModeNone)
	}
	if cluster.Conf.BackupRestic {
		t.Fatalf("expected BackupRestic=false")
	}
	if cluster.Conf.BackupResticAws {
		t.Fatalf("expected BackupResticAws=false")
	}
	if cluster.ResticManager != existingManager {
		t.Fatalf("expected existing ResticManager to be reused, not replaced")
	}
	if len(cluster.ResticManager.Backups) != 0 {
		t.Fatalf("expected ClearSnapshotList to clear snapshots, got %d", len(cluster.ResticManager.Backups))
	}
}
