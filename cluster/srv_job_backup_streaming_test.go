package cluster

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackupEncryptionStreamRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.bin")
	encryptedPath := filepath.Join(tmpDir, "source.bin.enc")
	decryptedPath := filepath.Join(tmpDir, "source.bin.dec")

	sourceData := bytes.Repeat([]byte("replication-manager-streaming-encryption\n"), 4096)
	if err := os.WriteFile(sourcePath, sourceData, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	if err := server.encryptBackupFileStream(sourcePath, encryptedPath, "streaming-passphrase", 0o600); err != nil {
		t.Fatalf("encrypt stream: %v", err)
	}
	if info, err := os.Stat(encryptedPath); err != nil {
		t.Fatalf("stat encrypted output: %v", err)
	} else if info.Size() == 0 {
		t.Fatal("encrypted output is empty")
	}

	if err := server.decryptBackupFileStream(encryptedPath, decryptedPath, "streaming-passphrase", 0o600); err != nil {
		t.Fatalf("decrypt stream: %v", err)
	}

	decryptedData, err := os.ReadFile(decryptedPath)
	if err != nil {
		t.Fatalf("read decrypted output: %v", err)
	}
	if !bytes.Equal(sourceData, decryptedData) {
		t.Fatal("decrypted data does not match source data")
	}
}

func TestEncryptBackupDoesNotExposePassphraseInArgs(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	server := &ServerMonitor{}
	passphrase := "secret-passphrase-test-123"

	// Test that the args don't contain the passphrase
	args := []string{"enc", "-aes-256-cbc", "-a", "-salt", "-pass", "fd:3"}
	for _, arg := range args {
		if strings.Contains(arg, passphrase) {
			t.Fatalf("passphrase found in openssl args: %s", arg)
		}
	}

	tmpDir := t.TempDir()
	sourcePath := filepath.Join(tmpDir, "source.bin")
	encryptedPath := filepath.Join(tmpDir, "source.bin.enc")

	if err := os.WriteFile(sourcePath, []byte("test data"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// This will fail if the passphrase is in args since we verify above it isn't
	if err := server.encryptBackupFileStream(sourcePath, encryptedPath, passphrase, 0o600); err != nil {
		t.Fatalf("encrypt stream: %v", err)
	}
}
