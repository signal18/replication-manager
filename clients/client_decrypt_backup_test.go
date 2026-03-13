//go:build clients
// +build clients

package clients

import (
	"bytes"
	"os/exec"
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

	decrypted, err := decryptWithPassphraseOpenSSL(out.Bytes(), password)
	if err != nil {
		t.Fatalf("decryptWithPassphraseOpenSSL failed: %s", err.Error())
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

	if _, err := decryptWithPassphraseOpenSSL(out.Bytes(), password); err == nil {
		t.Fatalf("expected passphrase decrypt to fail for legacy payload")
	}

	decrypted, err := decryptWithLegacyKeyIVOpenSSL(out.Bytes(), key, iv)
	if err != nil {
		t.Fatalf("decryptWithLegacyKeyIVOpenSSL failed: %s", err.Error())
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("legacy decrypted content mismatch")
	}
}
