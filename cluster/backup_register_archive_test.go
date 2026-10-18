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
