// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newRegisteredBackupTest registers a dump of the server's backup folder (#2108).
func newRegisteredBackupTest(t *testing.T) (*Cluster, *ServerMonitor, *backupmgr.BackupMetadata) {
	t.Helper()
	c, server, dir := newDiscoveryTest(t)
	path := filepath.Join(dir, "copy.sql.gz")
	writeDiscoveryGzip(t, path, discoveryDump+discoveryDumpFooter)
	m, err := registerAndWait(t, c, server, path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return c, server, m
}

// setVerification records v as the last verification of m, as a run does.
func setVerification(server *ServerMonitor, m *backupmgr.BackupMetadata, v *backupmgr.BackupVerification) {
	server.backupMetaMutex.Lock()
	m.Verification = v
	server.backupMetaMutex.Unlock()
}

// The manual reseed of a registered backup: refused while not proven with
// backup-verification-registered-proven, and whenever the last verification of its current
// file failed; a failure before its file was registered does not count. The primary refusal
// below comes after these: reaching it means the verifications let the reseed through.
func TestRegisteredBackupReseedGate(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	c.master = server
	registered := m.Registration.RegisteredAt
	artifact := m.Registration.Artifact

	c.Conf.BackupVerificationRegisteredProven = true
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupNotProven) {
		t.Fatalf("never verified: %v", err)
	}
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelIntegrity, Status: backupmgr.BackupVerificationPassed, EndTime: registered.Add(time.Minute), Artifact: &artifact})
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupNotProven) {
		t.Fatalf("integrity only: %v", err)
	}
	other := artifact
	other.Size++
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationPassed, EndTime: registered.Add(time.Minute), Artifact: &other})
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupNotProven) {
		t.Fatalf("test restore of another file: %v", err)
	}
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationPassed, EndTime: registered.Add(time.Minute), Artifact: &artifact})
	if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("proven: %v", err)
	}

	c.Conf.BackupVerificationRegisteredProven = false
	setVerification(server, m, nil)
	if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("switch off, never verified: %v", err)
	}
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationFailed, EndTime: registered.Add(time.Minute), Error: "Error 1062"})
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupVerificationFailed) || !strings.Contains(err.Error(), "1062") {
		t.Fatalf("switch off, failed: %v", err)
	}
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationFailed, EndTime: registered.Add(-time.Minute), Error: "Error 1062"})
	if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("failed before the file was registered: %v", err)
	}

	// a backup replication-manager took is not gated
	c.Conf.BackupVerificationRegisteredProven = true
	m.Discovered = false
	if err := c.ReseedFromBackupID(server, m.Id); err == nil || !strings.Contains(err.Error(), "primary") {
		t.Fatalf("own backup: %v", err)
	}
}

// WARN0253 while the last verification of a registered backup's current file failed; not for
// a failure before the file was registered, nor once a verification passed.
func TestRegisteredVerificationState(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	registered := m.Registration.RegisteredAt
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationFailed, EndTime: registered.Add(time.Minute), Error: "Error 1062"})
	c.checkRegisteredVerificationStates()
	if !hasOpenState(c, "WARN0253") {
		t.Fatal("WARN0253 not raised for a failed verification")
	}
	c.StateMachine.Init()
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationFailed, EndTime: registered.Add(-time.Minute), Error: "Error 1062"})
	c.checkRegisteredVerificationStates()
	if hasOpenState(c, "WARN0253") {
		t.Fatal("WARN0253 raised for a failure before the file was registered")
	}
	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelIntegrity, Status: backupmgr.BackupVerificationPassed, EndTime: registered.Add(time.Minute)})
	c.checkRegisteredVerificationStates()
	if hasOpenState(c, "WARN0253") {
		t.Fatal("WARN0253 raised after a passed verification")
	}
}

// ArchiveBackup refuses what it can not push now, and keeps one archive of a backup at a
// time; a refusal takes its reservation back.
func TestArchiveBackupRefusals(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	if err := c.ArchiveBackup(999, false, false); !errors.Is(err, ErrBackupNotFound) {
		t.Fatalf("unknown backup: %v", err)
	}
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupArchiveUnavailable) {
		t.Fatalf("restic off: %v", err)
	}
	c.Conf.BackupRestic = true
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupArchiveUnavailable) {
		t.Fatalf("no restic manager: %v", err)
	}
	c.ResticManager = &backupmgr.ResticManager{}
	if err := c.ArchiveBackup(m.Id, false, true); !errors.Is(err, ErrBackupNotVerifiable) {
		t.Fatalf("test restore without a verification server: %v", err)
	}

	setVerification(server, m, &backupmgr.BackupVerification{Level: config.ConstBackupVerificationLevelRestore, Status: backupmgr.BackupVerificationFailed, EndTime: m.Registration.RegisteredAt.Add(time.Minute), Error: "Error 1062"})
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupVerificationFailed) {
		t.Fatalf("failed verification: %v", err)
	}
	setVerification(server, m, nil)

	c.backupVerifying.Store(m.Id, config.ConstBackupVerificationLevelIntegrity)
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupVerificationRunning) {
		t.Fatalf("being verified: %v", err)
	}
	c.backupVerifying.Delete(m.Id)
	if _, held := c.backupArchiving.Load(m.Id); held {
		t.Fatal("a refused archive kept its reservation")
	}

	c.backupArchiving.Store(m.Id, struct{}{})
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupArchiving) {
		t.Fatalf("already being archived: %v", err)
	}
	c.backupArchiving.Delete(m.Id)

	server.backupMetaMutex.Lock()
	m.ResticSnapshotID = "0123456789abcdef"
	server.backupMetaMutex.Unlock()
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupArchived) {
		t.Fatalf("already archived: %v", err)
	}

	m.Discovered = false
	if err := c.ArchiveBackup(m.Id, false, false); !errors.Is(err, ErrBackupNotDiscovered) {
		t.Fatalf("own backup: %v", err)
	}
}

// The step after an on-demand verification (an archive tested first) runs once the run ended
// and released the backup: no longer verifying nor read, its result recorded. It relies on the
// order of the run's defers, which this pins.
func TestVerificationThenRunsAfterRelease(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	type seen struct {
		verifying, reading bool
		status             string
	}
	got := make(chan seen, 1)
	if _, err := c.verifyBackupByIDRunThen(m.Id, config.ConstBackupVerificationLevelIntegrity, func() {
		_, verifying := c.backupVerifying.Load(m.Id)
		_, reading := c.backupVerificationReads.Load(m.Id)
		s := seen{verifying: verifying, reading: reading}
		if v := server.GetBackupVerification(m); v != nil {
			s.status = v.Status
		}
		got <- s
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-got:
		if s.verifying || s.reading || s.status == "" || s.status == backupmgr.BackupVerificationRunning {
			t.Fatalf("the step ran before the run released the backup: %+v", s)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the step did not run")
	}
}

// An archive push running alone (restic task queue) reads the artifacts: the discovery and a
// registration go on; the change check still waits for it (a push with
// backup-archive-remove-original removes its backup's file), and so does a reseed.
func TestDiscoveryAndRegisterDuringArchivePush(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	dir := server.GetMyBackupDirectory()
	other := filepath.Join(dir, "other.sql.gz")
	writeDiscoveryGzip(t, other, discoveryDump+discoveryDumpFooter)
	c.SetInResticLogicalBackupState(true)
	defer c.SetInResticLogicalBackupState(false)
	if cands, err := server.DiscoverBackups(); err != nil || len(cands) != 1 {
		t.Fatalf("discovery during a push: %d, %v", len(cands), err)
	}
	if _, err := registerAndWait(t, c, server, other, time.Time{}); err != nil {
		t.Fatalf("registration during a push: %v", err)
	}
	if !c.IsInBackup() {
		t.Fatal("a push no longer counts as a backup for the other guards")
	}
	if err := c.ReseedFromBackupID(server, m.Id); !errors.Is(err, ErrBackupInProgress) {
		t.Fatalf("reseed during a push: %v", err)
	}
}

// A push without the in-archive flag (an archive of a registered backup) leaves the flag of
// the pushes before it as it is, and reports its end.
func TestBackupResticWithoutFlag(t *testing.T) {
	c, server, m := newRegisteredBackupTest(t)
	c.SetInResticLogicalBackupState(true)
	var got error
	server.backupResticWith(backupmgr.BackupMethodLogical, m, m.Dest, resticPushOptions{removeOriginal: func() bool { return false }, noFlag: true, done: func(err error) { got = err }})
	if !errors.Is(got, ErrBackupArchiveUnavailable) {
		t.Fatalf("restic off: done(%v)", got)
	}
	if !c.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
		t.Fatal("the in-archive flag of another push was cleared")
	}
	server.backupResticWith(backupmgr.BackupMethodLogical, m, m.Dest, resticPushOptions{removeOriginal: func() bool { return false }})
	if c.IsInResticBackupForMethod(backupmgr.BackupMethodLogical) {
		t.Fatal("a push with the flag did not clear it")
	}
}

// A removal refused in an archive asked for one backup is WARN0238 whatever
// backup-archive-remove-original.
func TestArchiveRemoveOriginalAskedRefusalKept(t *testing.T) {
	cl, server, meta, _ := newArchiveRemoveOriginalTest(t)
	cl.Conf.BackupArchiveRemoveOriginal = false
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, meta.Dest, "snapshot does not hold it", true)
	cl.checkArchiveRemoveOriginalStates()
	if !hasOpenState(cl, "WARN0238") {
		t.Fatal("WARN0238 not raised for an asked removal kept")
	}
	cl.StateMachine.Init()
	server.setArchiveRemoveOriginalRefusal(backupmgr.BackupMethodLogical, meta.Dest, "snapshot does not hold it", false)
	cl.checkArchiveRemoveOriginalStates()
	if hasOpenState(cl, "WARN0238") {
		t.Fatal("WARN0238 raised for a refusal of the switch, switch off")
	}
}
