//go:build clients
// +build clients

package clients

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	rmcrypto "github.com/signal18/replication-manager/utils/crypto"
)

func TestDecryptWithPassphraseOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	password := "passphrase-test"
	plaintext := []byte("backup payload content")

	cmd := exec.Command("openssl", "enc", "-aes-256-cbc", "-a", "-salt", "-pass", "pass:"+password)
	cmd.Stdin = bytes.NewReader(plaintext)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		t.Fatalf("failed to encrypt test payload: %s", errMsg)
	}

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "backup.enc")
	outputPath := filepath.Join(inputDir, "backup.dec")
	if err := os.WriteFile(inputPath, out.Bytes(), 0o600); err != nil {
		t.Fatalf("failed to write encrypted input: %s", err.Error())
	}

	if err := decryptWithPassphraseOpenSSL(inputPath, outputPath, password); err != nil {
		t.Fatalf("decryptWithPassphraseOpenSSL failed: %s", err.Error())
	}
	decrypted, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read decrypted output: %s", err.Error())
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted content mismatch")
	}
}

func TestDecryptWithLegacyKeyIVOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl binary not available")
	}

	password := "legacy-passphrase"
	plaintext := []byte("legacy backup payload")
	key := rmcrypto.GetSHA256Hash(password)
	iv := rmcrypto.GetMD5Hash(password)

	cmd := exec.Command("openssl", "aes-256-cbc", "-e", "-a", "-nosalt", "-K", key, "-iv", iv)
	cmd.Stdin = bytes.NewReader(plaintext)
	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg == "" {
			errMsg = err.Error()
		}
		t.Fatalf("failed to encrypt legacy payload: %s", errMsg)
	}

	inputDir := t.TempDir()
	inputPath := filepath.Join(inputDir, "legacy.enc")
	outputPath := filepath.Join(inputDir, "legacy.dec")
	passphraseOutputPath := filepath.Join(inputDir, "legacy-passphrase.dec")
	if err := os.WriteFile(inputPath, out.Bytes(), 0o600); err != nil {
		t.Fatalf("failed to write legacy encrypted input: %s", err.Error())
	}

	if err := decryptWithPassphraseOpenSSL(inputPath, passphraseOutputPath, password); err == nil {
		t.Fatalf("expected passphrase decrypt to fail for legacy payload")
	}

	if err := decryptWithLegacyKeyIVOpenSSL(inputPath, outputPath, key, iv); err != nil {
		t.Fatalf("decryptWithLegacyKeyIVOpenSSL failed: %s", err.Error())
	}
	decrypted, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read legacy decrypted output: %s", err.Error())
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("legacy decrypted content mismatch")
	}
}
