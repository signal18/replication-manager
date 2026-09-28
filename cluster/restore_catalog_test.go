// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/backupmgr"
)

func hasCap(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

func TestBackupMetaToCatalogEncryptCapability(t *testing.T) {
	base := func() *backupmgr.BackupMetadata {
		return &backupmgr.BackupMetadata{
			BackupMethod: backupmgr.BackupMethodLogical,
			BackupTool:   "mysqldump",
			Dest:         "/var/lib/replication-manager/cluster/backup/mysqldump.sql.gz",
			Completed:    true,
			EndTime:      time.Now(),
		}
	}

	t.Run("plaintext backup has no encrypt capability", func(t *testing.T) {
		m := base()
		entry := backupMetaToCatalog("10.0.0.1:3306", m)
		if hasCap(entry.Caps, "encrypt") {
			t.Fatalf("plaintext metadata should not carry the encrypt capability, got %v", entry.Caps)
		}
		if entry.Path != m.Dest {
			t.Fatalf("expected catalog path %q, got %q", m.Dest, entry.Path)
		}
	})

	t.Run("encrypted and completed backup has encrypt capability", func(t *testing.T) {
		m := base()
		m.Dest = m.Dest + ".enc"
		m.Encrypted = true
		m.EncryptionAlgo = backupmgr.EncryptionFormatOpenSSL
		entry := backupMetaToCatalog("10.0.0.1:3306", m)
		if !hasCap(entry.Caps, "encrypt") {
			t.Fatalf("encrypted+completed metadata should carry the encrypt capability, got %v", entry.Caps)
		}
		if entry.Path != m.Dest {
			t.Fatalf("expected catalog path %q, got %q", m.Dest, entry.Path)
		}
	})

	t.Run("existing metadata with unset encryption fields remains a valid plaintext entry", func(t *testing.T) {
		// Simulates a pre-existing backup record written before this feature
		// existed: Encrypted/EncryptionAlgo are simply absent (zero values).
		m := base()
		entry := backupMetaToCatalog("10.0.0.1:3306", m)
		if hasCap(entry.Caps, "encrypt") {
			t.Fatalf("legacy unset-encryption metadata must not be reported as encrypted, got %v", entry.Caps)
		}
	})
}

func TestBuildBackupCatalogExcludesIncompleteEncryptionFailure(t *testing.T) {
	// A backup that failed at the encryption step is flipped to
	// Completed=false (see reportBackupEncryptionFailure); it must not
	// surface in the catalog at all, encrypted-looking or otherwise, since
	// finalizeBackupEncryption never mutated Dest/Encrypted on that failure.
	cluster := &Cluster{}
	sv := &ServerMonitor{URL: "10.0.0.1:3306"}
	sv.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BackupMethod: backupmgr.BackupMethodLogical,
		Dest:         "/var/lib/replication-manager/cluster/backup/mysqldump.sql.gz",
		Completed:    false,
		Encrypted:    false,
	}
	cluster.Servers = []*ServerMonitor{sv}

	cat := cluster.buildBackupCatalog()
	for _, entry := range cat {
		if entry.Server == sv.URL {
			t.Fatalf("incomplete (failed-encryption) backup must be excluded from the catalog, got entry %+v", entry)
		}
	}
}
