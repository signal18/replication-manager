package backupmgr

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func deriveBackupEncryptionKey(secret string) []byte {
	sum := sha256.Sum256([]byte("enc:" + secret))
	return sum[:]
}

// EncryptBackupFileAES256CBC encrypts a backup file in-place using AES-256-CBC.
// It returns the IV as a hex-prefixed metadata token (hex:<iv-bytes>).
func EncryptBackupFileAES256CBC(path string, secret string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("backup encryption path is empty")
	}
	if strings.TrimSpace(secret) == "" {
		return "", fmt.Errorf("backup encryption secret is empty")
	}

	iv := make([]byte, 16)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	ivHex := hex.EncodeToString(iv)
	keyHex := hex.EncodeToString(deriveBackupEncryptionKey(secret))

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "mysqldump-encrypted-*.tmp")
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("openssl", "aes-256-cbc", "-e", "-nosalt", "-K", keyHex, "-iv", ivHex, "-in", path, "-out", tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("openssl encryption failed: %v (%s)", err, msg)
		}
		return "", fmt.Errorf("openssl encryption failed: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return "", err
	}

	return "hex:" + ivHex, nil
}

// DecryptBackupFileAES256CBC decrypts a backup file in-place using AES-256-CBC.
// ivToken must have the "hex:" prefix as returned by EncryptBackupFileAES256CBC.
func DecryptBackupFileAES256CBC(path string, secret string, ivToken string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("backup decryption path is empty")
	}
	if strings.TrimSpace(secret) == "" {
		return fmt.Errorf("backup decryption secret is empty")
	}

	ivToken = strings.TrimSpace(ivToken)
	if !strings.HasPrefix(ivToken, "hex:") {
		return fmt.Errorf("backup decryption IV must have hex: prefix, got: %q", ivToken)
	}
	ivHex := strings.TrimPrefix(ivToken, "hex:")
	if ivHex == "" {
		return fmt.Errorf("backup decryption IV hex value is empty")
	}

	keyHex := hex.EncodeToString(deriveBackupEncryptionKey(secret))

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "backup-decrypted-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("openssl", "aes-256-cbc", "-d", "-nosalt", "-K", keyHex, "-iv", ivHex, "-in", path, "-out", tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return fmt.Errorf("openssl decryption failed: %v (%s)", err, msg)
		}
		return fmt.Errorf("openssl decryption failed: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}

	return nil
}
