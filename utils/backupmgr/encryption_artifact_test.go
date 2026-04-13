package backupmgr

import (
	"bytes"
	"context"
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

// ---------------------------------------------------------------------------
// EncryptFileAsStreamContainer
// ---------------------------------------------------------------------------

// makeTestStreamKey builds a root secret from a sponsor credential string for
// tests that need a consistent key source.
func makeTestRootSecret(t *testing.T, sponsorCreds string) []byte {
	t.Helper()
	secret, _, err := ResolveBackupEncryptionKeyMaterial(sponsorCreds, "")
	if err != nil {
		t.Fatalf("resolve key material: %v", err)
	}
	return []byte(secret)
}

func TestEncryptFileAsStreamContainerRoundTrip(t *testing.T) {
	plaintext := []byte("stream container round-trip test payload")
	path := filepath.Join(t.TempDir(), "backup.sql")
	if err := os.WriteFile(path, plaintext, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	sponsorCreds := "cloud18-sponsor-user-credentials:v1"
	rootSecret := makeTestRootSecret(t, "sponsor:s3cr3t")
	keyID := "cloud18-sponsor-user-credentials:v1"
	clusterName := "test-cluster"
	entryPath := "backup.sql"

	if err := EncryptFileAsStreamContainer(path, rootSecret, clusterName, entryPath, keyID); err != nil {
		t.Fatalf("EncryptFileAsStreamContainer: %v", err)
	}

	// Verify the file is no longer plaintext and starts with the RMSC magic
	encrypted, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read encrypted: %v", err)
	}
	if bytes.Equal(plaintext, encrypted) {
		t.Fatal("expected encrypted bytes to differ from original")
	}
	if !bytes.HasPrefix(encrypted, []byte(StreamContainerMagic)) {
		t.Fatalf("expected RMSC magic prefix, got: %q", encrypted[:4])
	}

	// Decrypt by reading the stream container back
	containerKey, err := DeriveStreamContainerKey(rootSecret, clusterName)
	if err != nil {
		t.Fatalf("derive container key: %v", err)
	}
	entryKey, err := DeriveStreamEntryKey(containerKey, entryPath)
	if err != nil {
		t.Fatalf("derive entry key: %v", err)
	}

	r := bytes.NewReader(encrypted)
	preflight, err := ReadPreflight(r)
	if err != nil {
		t.Fatalf("ReadPreflight: %v", err)
	}
	if preflight.Mode != StreamModeSingleFile {
		t.Errorf("expected single-file mode, got %d", preflight.Mode)
	}
	if len(preflight.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(preflight.Entries))
	}
	if preflight.Entries[0].Path != entryPath {
		t.Errorf("expected entry path %q, got %q", entryPath, preflight.Entries[0].Path)
	}

	fr, err := NewFrameReader(context.Background(), r, entryKey, preflight.CipherSuite)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}
	var out bytes.Buffer
	buf := make([]byte, 1024)
	for {
		n, readErr := fr.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
		}
		if readErr != nil {
			break
		}
	}
	if !bytes.Equal(plaintext, out.Bytes()) {
		t.Fatalf("decrypted content mismatch: got %q, want %q", out.Bytes(), plaintext)
	}
	_ = sponsorCreds
}

func TestEncryptFileAsStreamContainerRejectsEmptyPath(t *testing.T) {
	err := EncryptFileAsStreamContainer("", []byte("secret"), "cluster", "entry.sql", "cloud18-sponsor-user-credentials:v1")
	if err == nil || !strings.Contains(err.Error(), "path is empty") {
		t.Fatalf("expected empty-path error, got: %v", err)
	}
}

func TestEncryptFileAsStreamContainerRejectsEmptySecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.sql")
	os.WriteFile(path, []byte("data"), 0o644)
	err := EncryptFileAsStreamContainer(path, nil, "cluster", "entry.sql", "cloud18-sponsor-user-credentials:v1")
	if err == nil || !strings.Contains(err.Error(), "root secret is empty") {
		t.Fatalf("expected empty-secret error, got: %v", err)
	}
}

func TestEncryptFileAsStreamContainerRejectsEmptyCluster(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.sql")
	os.WriteFile(path, []byte("data"), 0o644)
	err := EncryptFileAsStreamContainer(path, []byte("secret"), "", "entry.sql", "cloud18-sponsor-user-credentials:v1")
	if err == nil || !strings.Contains(err.Error(), "cluster name is empty") {
		t.Fatalf("expected empty-cluster error, got: %v", err)
	}
}

func TestEncryptFileAsStreamContainerRejectsInvalidKeyID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup.sql")
	os.WriteFile(path, []byte("data"), 0o644)
	err := EncryptFileAsStreamContainer(path, []byte("secret"), "cluster", "entry.sql", "bad-key-id")
	if err == nil || !strings.Contains(err.Error(), "invalid key ID") {
		t.Fatalf("expected invalid-key-id error, got: %v", err)
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
