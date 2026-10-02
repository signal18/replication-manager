// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newVersionedEncryptionTestCluster enables secret versioning with a Repman
// key, so secret_store.json records every db-servers-credential change.
func newVersionedEncryptionTestCluster(t *testing.T, pass string) *Cluster {
	t.Helper()
	c := newEncryptionTestCluster(t, pass)
	c.WorkingDir = filepath.Join(c.Conf.WorkingDir, c.Name)
	os.MkdirAll(c.WorkingDir, 0700)
	c.Conf.MonitoringSecretVersioning = true
	c.Conf.SecretKey = []byte("01234567890123456789012345678901")
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	c.recordPasswordVersion(t)
	return c
}

// recordPasswordVersion lets secret_store.json catch up with the current
// credential, as the monitor does after a change.
func (c *Cluster) recordPasswordVersion(t *testing.T) {
	t.Helper()
	c.MarkSecretVersionStoreDirty()
	c.ReconcileSecretVersionStore()
}

func encryptTestBackup(t *testing.T, c *Cluster, name, content string) string {
	t.Helper()
	server := &ServerMonitor{Host: "db1", Port: "3306", URL: "db1:3306", ClusterGroup: c}
	dest := filepath.Join(server.GetMyBackupDirectory(), name)
	staging := c.prepareBackupStaging(dest)
	os.WriteFile(staging, []byte(content), 0600)
	meta := &backupmgr.BackupMetadata{Id: time.Now().UnixNano(), Dest: staging, StartTime: time.Now()}
	if err := server.finalizeBackupEncryption(meta, "logical"); err != nil {
		t.Fatalf("finalizeBackupEncryption: %v", err)
	}
	c.BackupMetaMap.Set(meta.Id, meta)
	return meta.Dest
}

func TestBackupRestoresAfterRootPasswordChangeViaSecretStore(t *testing.T) {
	c := newVersionedEncryptionTestCluster(t, "pw1")
	old := encryptTestBackup(t, c, "mysqldump.sql.gz", "taken with pw1")

	// Root password rotates; the store records version 2.
	setTestDbPassword(c, "pw2")
	c.recordPasswordVersion(t)
	recent := encryptTestBackup(t, c, "mydumper.sql.gz", "taken with pw2")

	if got, err := restoreTestArtifact(c, old); err != nil || got != "taken with pw1" {
		t.Fatalf("pre-rotation backup not restorable from secret_store history: %q, %v", got, err)
	}
	if got, err := restoreTestArtifact(c, recent); err != nil || got != "taken with pw2" {
		t.Fatalf("post-rotation backup: %q, %v", got, err)
	}
}

func TestBackupRecordsSecretStoreVersion(t *testing.T) {
	c := newVersionedEncryptionTestCluster(t, "pw1")
	encryptTestBackup(t, c, "a.sql.gz", "x")
	setTestDbPassword(c, "pw2")
	c.recordPasswordVersion(t)
	encryptTestBackup(t, c, "b.sql.gz", "y")

	versions := map[string]int{}
	integrity := map[string]string{}
	c.BackupMetaMap.Callback(func(_ int64, m *backupmgr.BackupMetadata) bool {
		versions[filepath.Base(m.Dest)] = m.EncryptionKeyVersion
		integrity[filepath.Base(m.Dest)] = m.IntegrityAlgo
		return true
	})
	if versions["a.sql.gz.enc"] != 1 || versions["b.sql.gz.enc"] != 2 {
		t.Fatalf("recorded versions %v, want a=1 b=2", versions)
	}
	for k := range versions {
		if strings.Contains(k, "pw") {
			t.Fatalf("metadata must never hold a password")
		}
	}
	if integrity["a.sql.gz.enc"] != backupmgr.IntegrityFormatHMACSHA256V1 || integrity["b.sql.gz.enc"] != backupmgr.IntegrityFormatHMACSHA256V1 {
		t.Fatalf("integrity metadata %v, want HMAC-SHA256 v1", integrity)
	}
}

func TestLocalEncryptedRestoreValidatesDeclaredAlgorithms(t *testing.T) {
	c := newVersionedEncryptionTestCluster(t, "pw1")
	artifact := encryptTestBackup(t, c, "mysqldump.sql.gz", "authenticated backup")
	meta := encryptionMetaForArtifact(t, c, artifact)

	for _, tc := range []struct {
		name       string
		encryption string
		integrity  string
		want       string
	}{
		{"unsupported encryption", "other-encryption-v1", backupmgr.IntegrityFormatHMACSHA256V1, "unsupported encryption algorithm"},
		{"unsupported integrity", backupmgr.EncryptionFormatOpenSSL, "other-integrity-v1", "unsupported integrity algorithm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			meta.EncryptionAlgo = tc.encryption
			meta.IntegrityAlgo = tc.integrity
			reader, _, err := c.openRestoreArtifactStream(artifact)
			if reader != nil {
				reader.Close()
				t.Fatalf("restore stream opened before metadata validation")
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("restore error = %v, want %q", err, tc.want)
			}
		})
	}

	meta.EncryptionAlgo = backupmgr.EncryptionFormatOpenSSL
	meta.IntegrityAlgo = backupmgr.IntegrityFormatHMACSHA256V1
	if got, err := restoreTestArtifact(c, artifact); err != nil || got != "authenticated backup" {
		t.Fatalf("matching metadata restore = %q, %v", got, err)
	}

	// Binlogs and artifact-only recovery do not have BackupMetadata. The HMAC
	// sidecar remains sufficient for those local encrypted artifacts.
	c.BackupMetaMap.Clear()
	if got, err := restoreTestArtifact(c, artifact); err != nil || got != "authenticated backup" {
		t.Fatalf("metadata-less encrypted artifact restore = %q, %v", got, err)
	}
}

func encryptionMetaForArtifact(t *testing.T, c *Cluster, artifact string) *backupmgr.BackupMetadata {
	t.Helper()
	var found *backupmgr.BackupMetadata
	c.BackupMetaMap.Callback(func(_ int64, meta *backupmgr.BackupMetadata) bool {
		if meta != nil && filepath.Clean(meta.Dest) == filepath.Clean(artifact) {
			found = meta
			return false
		}
		return true
	})
	if found == nil {
		t.Fatalf("metadata not found for %s", artifact)
	}
	return found
}

func TestBackupWithoutRepmanKeyNeedsCurrentPassword(t *testing.T) {
	c := newEncryptionTestCluster(t, "pw1")
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	old := encryptTestBackup(t, c, "mysqldump.sql.gz", "taken with pw1")
	setTestDbPassword(c, "pw2")
	if _, err := restoreTestArtifact(c, old); err == nil {
		t.Fatalf("without a Repman key the store records nothing, so the old password is unknown; restore must fail")
	}
	setTestDbPassword(c, "pw1")
	if got, err := restoreTestArtifact(c, old); err != nil || got != "taken with pw1" {
		t.Fatalf("restore with the right current password failed: %q, %v", got, err)
	}
}

func TestBackupRestorePasswordsTimestampFirst(t *testing.T) {
	c := newVersionedEncryptionTestCluster(t, "pw1")
	old := encryptTestBackup(t, c, "binlog-like", "x")
	// Binlog copies have no metadata: only the file time is known.
	c.BackupMetaMap.Clear()

	time.Sleep(1100 * time.Millisecond) // rotated_at has one-second resolution
	setTestDbPassword(c, "pw2")
	c.recordPasswordVersion(t)
	setTestDbPassword(c, "pw3")
	c.recordPasswordVersion(t)

	past := time.Now().Add(-time.Hour)
	os.Chtimes(old, past, past)
	got := c.backupRestorePasswords(old)
	if len(got) != 3 || got[0] != "pw1" {
		t.Fatalf("candidates %v: the password active at the backup time must come first", got)
	}
	if got[1] != "pw3" {
		t.Fatalf("candidates %v: the current password must follow", got)
	}
}

func TestPasswordActiveAt(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	history := []backupPasswordVersion{ // newest first
		{version: 3, password: "c", rotatedAt: t0.Add(2 * time.Hour)},
		{version: 2, password: "b", rotatedAt: t0.Add(time.Hour)},
		{version: 1, password: "a", rotatedAt: t0},
	}
	cases := []struct {
		at   time.Time
		want string
	}{
		{t0.Add(-time.Hour), "a"},              // before the first record
		{t0, "a"},                              // exactly at a rotation
		{t0.Add(90 * time.Minute), "b"},        // between rotations
		{t0.Add(3 * time.Hour), "c"},           // after the last one
		{t0.Add(time.Hour - time.Second), "a"}, // just before a rotation
	}
	for _, tc := range cases {
		got, ok := passwordActiveAt(history, tc.at)
		if !ok || got.password != tc.want {
			t.Fatalf("at %s: got %q, want %q", tc.at, got.password, tc.want)
		}
	}
	if _, ok := passwordActiveAt(nil, t0); ok {
		t.Fatalf("empty history must not match")
	}
	if _, ok := passwordActiveAt(history, time.Time{}); ok {
		t.Fatalf("unknown backup time must not match")
	}
}

func setTestDbPassword(c *Cluster, pass string) {
	c.Conf.Secrets["db-servers-credential"] = config.Secret{Value: "root:" + pass}
}

// Encryption alone turns versioning on, and the first encrypted backup
// records the current password even if the store was never reconciled.
func TestEncryptionForcesSecretVersioningAndRecordsFirstPassword(t *testing.T) {
	c := newEncryptionTestCluster(t, "pw1")
	c.WorkingDir = filepath.Join(c.Conf.WorkingDir, c.Name)
	os.MkdirAll(c.WorkingDir, 0700)
	c.Conf.SecretKey = []byte("01234567890123456789012345678901")
	c.BackupMetaMap = backupmgr.NewBackupMetaMap()
	if c.Conf.MonitoringSecretVersioning {
		t.Fatalf("test must not enable monitoring-secret-versioning explicitly")
	}

	old := encryptTestBackup(t, c, "mysqldump.sql.gz", "taken with pw1")
	setTestDbPassword(c, "pw2")
	c.recordPasswordVersion(t)

	if got, err := restoreTestArtifact(c, old); err != nil || got != "taken with pw1" {
		t.Fatalf("backup taken before rotation not restorable: %q, %v", got, err)
	}
}
