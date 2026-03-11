//go:build clients
// +build clients

package clients

import "testing"

func TestDefaultDecryptedBackupPath(t *testing.T) {
	if got := defaultDecryptedBackupPath("/tmp/backup.sql.gz.enc"); got != "/tmp/backup.sql.gz" {
		t.Fatalf("expected .enc suffix removed, got %q", got)
	}
	if got := defaultDecryptedBackupPath("/tmp/backup.sql.gz"); got != "/tmp/backup.sql.gz.dec" {
		t.Fatalf("expected .dec suffix added, got %q", got)
	}
}
