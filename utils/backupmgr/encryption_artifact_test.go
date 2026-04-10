package backupmgr

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	original := []byte("backup-payload-round-trip-test")
	path := filepath.Join(t.TempDir(), "artifact.sql.gz")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	secret := "test-secret-key"
	ivToken, err := EncryptBackupFileAES256CBC(path, secret)
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read encrypted: %v", err)
	}
	if bytes.Equal(original, encrypted) {
		t.Fatal("expected encrypted bytes to differ from original")
	}

	if err := DecryptBackupFileAES256CBC(path, secret, ivToken); err != nil {
		t.Fatalf("decrypt failed: %v", err)
	}

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(original, restored) {
		t.Fatalf("expected restored bytes to match original: got %q, want %q", restored, original)
	}
}

func TestDecryptBackupFileAES256CBCRejectsEmptyPath(t *testing.T) {
	err := DecryptBackupFileAES256CBC("", "secret", "hex:aabbccdd")
	if err == nil {
		t.Fatal("expected empty path to fail")
	}
}

func TestDecryptBackupFileAES256CBCRejectsEmptySecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.sql.gz")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	err := DecryptBackupFileAES256CBC(path, "", "hex:aabbccdd")
	if err == nil {
		t.Fatal("expected empty secret to fail")
	}
}

func TestDecryptBackupFileAES256CBCRejectsMissingHexPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.sql.gz")
	if err := os.WriteFile(path, []byte("data"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	err := DecryptBackupFileAES256CBC(path, "secret", "aabbccdd")
	if err == nil {
		t.Fatal("expected missing hex: prefix to fail")
	}
	if !strings.Contains(err.Error(), "hex:") {
		t.Fatalf("expected error to mention hex: prefix, got: %v", err)
	}
}

func TestDecryptBackupFileAES256CBCFailsWithWrongSecret(t *testing.T) {
	original := []byte("backup-payload")
	path := filepath.Join(t.TempDir(), "artifact.sql.gz")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	ivToken, err := EncryptBackupFileAES256CBC(path, "correct-secret")
	if err != nil {
		t.Fatalf("encrypt failed: %v", err)
	}

	// Decrypt with wrong secret succeeds at the OpenSSL level (no auth),
	// but the resulting plaintext will differ from the original.
	// The HMAC layer is what catches wrong-key at the pipeline level;
	// here we verify the raw decrypt produces garbage rather than the original.
	_ = DecryptBackupFileAES256CBC(path, "wrong-secret", ivToken)
	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after wrong-key decrypt: %v", err)
	}
	if bytes.Equal(original, restored) {
		t.Fatal("expected wrong-key decrypt to produce different bytes than original")
	}
}
