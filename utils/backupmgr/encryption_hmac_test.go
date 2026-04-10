package backupmgr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyBackupFileHMACSHA256(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "backup.enc")
	if err := os.WriteFile(filePath, []byte("encrypted-backup-payload"), 0o600); err != nil {
		t.Fatalf("failed to write fixture file: %v", err)
	}

	secret := "test-secret"
	mac, err := computeBackupFileHMACSHA256(filePath, secret)
	if err != nil {
		t.Fatalf("failed to compute hmac fixture: %v", err)
	}

	t.Run("accepts valid mac", func(t *testing.T) {
		if err := VerifyBackupFileHMACSHA256(filePath, secret, mac); err != nil {
			t.Fatalf("expected valid mac to pass, got: %v", err)
		}
	})

	t.Run("accepts hex prefixed mac", func(t *testing.T) {
		if err := VerifyBackupFileHMACSHA256(filePath, secret, "hex:"+mac); err != nil {
			t.Fatalf("expected prefixed valid mac to pass, got: %v", err)
		}
	})

	t.Run("fails for wrong secret", func(t *testing.T) {
		if err := VerifyBackupFileHMACSHA256(filePath, "wrong-secret", mac); err == nil {
			t.Fatalf("expected wrong secret to fail hmac verification")
		}
	})

	t.Run("fails for wrong mac", func(t *testing.T) {
		if err := VerifyBackupFileHMACSHA256(filePath, secret, "deadbeef"); err == nil {
			t.Fatalf("expected wrong mac to fail verification")
		}
	})
}
