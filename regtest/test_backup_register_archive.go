// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// archiveWait bounds the wait for an archive push of a registered backup.
const archiveWait = 15 * time.Minute

// TestBackupRegisterArchive checks the archive of a registered backup and the verifications
// its manual reseed requires (#2108) on real servers, with backup-verification-registered-proven
// on and backup-archive-remove-original off:
//
//   - A1: a copy of a backup of the primary, registered, never verified: its reseed is refused
//     (not proven);
//   - A2: its test restore passes: the replica is reseeded from it, checked as
//     TestLogicalReseed checks a reseed;
//   - A3: it is archived without removing its file: its snapshot is recorded, its file kept
//     whatever backup-archive-remove-original; a second archive is refused;
//   - A4: a second copy is archived with a test restore first and the removal of its file:
//     the test restore passes, its snapshot is recorded, its file removed;
//   - A5: a copy holding a duplicated primary key row is registered (registration and
//     integrity do not load it), then archived with a test restore first: the test restore
//     fails with error 1062, nothing is pushed, its file is kept, WARN0253 is raised; its
//     reseed is refused, also with backup-verification-registered-proven off, and so is its
//     archive without a test restore; its unregister clears WARN0253.
//
// The test's copies are unregistered and removed, its snapshots purged. It reseeds the
// cluster's first replica, needs backup-logical-type mysqldump, backup-restic and
// backup-verification-host. Run it by name:
// /api/clusters/<cluster>/tests/actions/run/testBackupRegisterArchive
func (regtest *RegTest) TestBackupRegisterArchive(cl *clusterpkg.Cluster, conf string, test *clusterpkg.Test) bool {
	e := newEdgeRun(cl)
	if e == nil {
		return false
	}
	e.name = "backup register archive"
	if !cl.Conf.BackupRestic || cl.ResticManager == nil || strings.TrimSpace(cl.Conf.BackupVerificationHost) == "" {
		e.logf(config.LvlErr, "needs backup-restic (restic started) and backup-verification-host")
		return false
	}
	defer saveConf(cl)()
	proven, removeOriginal := cl.Conf.BackupVerificationRegisteredProven, cl.Conf.BackupArchiveRemoveOriginal
	cl.Conf.BackupVerificationRegisteredProven, cl.Conf.BackupArchiveRemoveOriginal = true, false
	defer func() {
		cl.Conf.BackupVerificationRegisteredProven, cl.Conf.BackupArchiveRemoveOriginal = proven, removeOriginal
	}()
	cl.Conf.SchedulerJobsExecRemote = ""
	cl.Conf.BackupMysqldumpSplitDump = false
	cl.Conf.ParseJobsExecOverrides()
	gtid := e.r.HasGTIDReplication()

	var copies []string
	var snapshots []string
	defer func() {
		for _, path := range copies {
			if m := e.registered(path); m != nil {
				if err := cl.UnregisterBackup(m.Id); err != nil {
					e.logf(config.LvlWarn, "unregister %s: %s", path, err)
				}
			}
			os.RemoveAll(path)
		}
		for _, id := range snapshots {
			if err := cl.ResticPurgeSnapshot(id, true); err != nil {
				e.logf(config.LvlWarn, "purge snapshot %s: %s", id, err)
			}
		}
	}()

	e.seed()
	if err := e.m.JobBackupLogical(context.Background()); err != nil {
		e.check("backup", false, err.Error())
		return e.result()
	}
	orig := e.m.LastBackupMeta.Logical
	if orig == nil || !orig.Completed {
		e.check("backup", false, fmt.Sprintf("metadata %+v", orig))
		return e.result()
	}
	// register copies the backup under name, edit (when not empty) inserted before its
	// footer, and registers it as an operator would
	register := func(name, edit string) *backupmgr.BackupMetadata {
		path := filepath.Join(e.m.GetMyBackupDirectory(), name)
		copies = append(copies, path)
		if err := copyBackupArtifact(orig.Dest, path, time.Now().Add(-2*time.Hour)); err != nil {
			e.check(name+" copy", false, err.Error())
			return nil
		}
		if edit != "" {
			if err := editDump(path, edit); err != nil {
				e.check(name+" edit", false, err.Error())
				return nil
			}
		}
		meta, err := e.register(path)
		if !e.check(name+" registered", err == nil && meta != nil && meta.Discovered && meta.Completed, fmt.Sprintf("%+v %v", meta, err)) {
			return nil
		}
		return meta
	}

	good := register("archive-good.sql.gz", "")
	if good == nil {
		return e.result()
	}
	err := cl.ReseedFromBackupID(e.r, good.Id)
	e.check("A1 reseed refused while not proven", errors.Is(err, clusterpkg.ErrBackupNotProven), fmt.Sprint(err))

	if err := cl.VerifyBackupByID(good.Id, config.ConstBackupVerificationLevelRestore); err != nil {
		e.check("A2 test restore", false, err.Error())
		return e.result()
	}
	v := e.waitVerification(good)
	if e.check("A2 test restore passed", v != nil && v.Status == backupmgr.BackupVerificationPassed, fmt.Sprintf("%+v", v)) {
		id := good.Id
		e.reseedWithAndCheck("A2 reseed from the proven copy", gtid, config.ConstBackupLogicalTypeMysqldump, func() error {
			// a reseed waits for the archive pushes (one may remove the file it restores):
			// retried as the operator would, the backup's own push ending
			deadline := time.Now().Add(archiveWait)
			for {
				err := cl.ReseedFromBackupID(e.r, id)
				if !errors.Is(err, clusterpkg.ErrBackupInProgress) || time.Now().After(deadline) {
					return err
				}
				time.Sleep(5 * time.Second)
			}
		})
	}

	if err := cl.ArchiveBackup(good.Id, false, false); err != nil {
		e.check("A3 archive", false, err.Error())
	} else {
		snap := e.waitArchived(good, false)
		if snap != "" {
			snapshots = append(snapshots, snap)
		}
		_, statErr := os.Stat(good.Dest)
		e.check("A3 archived, file kept", snap != "" && statErr == nil && !good.OriginalRemoved, fmt.Sprintf("snapshot %q, file %v, removed %v", snap, statErr, good.OriginalRemoved))
		err := cl.ArchiveBackup(good.Id, false, false)
		e.check("A3 second archive refused", errors.Is(err, clusterpkg.ErrBackupArchived), fmt.Sprint(err))
	}

	if removed := register("archive-remove.sql.gz", ""); removed != nil {
		if err := cl.ArchiveBackup(removed.Id, true, true); err != nil {
			e.check("A4 archive with a test restore and the removal", false, err.Error())
		} else {
			v := e.waitVerification(removed)
			snap := e.waitArchived(removed, true)
			if snap != "" {
				snapshots = append(snapshots, snap)
			}
			_, statErr := os.Stat(removed.Dest)
			e.check("A4 tested, archived, file removed", v != nil && v.Status == backupmgr.BackupVerificationPassed && snap != "" && os.IsNotExist(statErr), fmt.Sprintf("verification %+v, snapshot %q, file %v", v, snap, statErr))
		}
	}

	bad := register("archive-dupkey.sql.gz", "INSERT INTO "+edgeTable+" VALUES (1,'dup-2108');\n")
	if bad == nil {
		return e.result()
	}
	if err := cl.ArchiveBackup(bad.Id, true, true); err != nil {
		e.check("A5 archive with a test restore", false, err.Error())
		return e.result()
	}
	v = e.waitVerification(bad)
	e.check("A5 test restore failed", v != nil && v.Status == backupmgr.BackupVerificationFailed && strings.Contains(v.Error, "1062"), fmt.Sprintf("%+v", v))
	// the archive ends with its test restore: given a moment, nothing is pushed
	time.Sleep(10 * time.Second)
	_, statErr := os.Stat(bad.Dest)
	e.check("A5 not archived, file kept", bad.ResticSnapshotID == "" && statErr == nil, fmt.Sprintf("snapshot %q, file %v", bad.ResticSnapshotID, statErr))
	e.check("A5 WARN0253 raised", e.waitState("WARN0253", true), "not raised")
	cl.Conf.BackupVerificationRegisteredProven = false
	err = cl.ReseedFromBackupID(e.r, bad.Id)
	e.check("A5 reseed refused, proof not required", errors.Is(err, clusterpkg.ErrBackupVerificationFailed), fmt.Sprint(err))
	cl.Conf.BackupVerificationRegisteredProven = true
	err = cl.ArchiveBackup(bad.Id, false, false)
	e.check("A5 archive without a test restore refused", errors.Is(err, clusterpkg.ErrBackupVerificationFailed), fmt.Sprint(err))
	err = cl.UnregisterBackup(bad.Id)
	e.check("A5 unregister clears WARN0253", err == nil && e.waitState("WARN0253", false), fmt.Sprint(err))

	return e.result()
}

// waitArchived waits for the archive push of meta to record its snapshot (and, removed, the
// removal of its file); its snapshot id, empty when it did not end in archiveWait.
func (e *edgeRun) waitArchived(meta *backupmgr.BackupMetadata, removed bool) string {
	deadline := time.Now().Add(archiveWait)
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if meta.ResticSnapshotID != "" && (!removed || meta.OriginalRemoved) {
			return meta.ResticSnapshotID
		}
	}
	return ""
}
