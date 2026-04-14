package backupmgr

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func deriveBackupIntegrityKey(secret string) []byte {
	sum := sha256.Sum256([]byte("mac:" + secret))
	return sum[:]
}

func computeBackupFileHMACSHA256(path string, secret string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	h := hmac.New(sha256.New, deriveBackupIntegrityKey(secret))
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// ComputeBackupFileHMACSHA256 computes hex-encoded HMAC-SHA256 for a backup
// artifact using the locked backup integrity key derivation contract.
func ComputeBackupFileHMACSHA256(path string, secret string) (string, error) {
	return computeBackupFileHMACSHA256(path, secret)
}

// VerifyBackupFileHMACSHA256 validates the encrypted artifact MAC before any
// decrypt flow is allowed to proceed.
func VerifyBackupFileHMACSHA256(path string, secret string, expectedMAC string) error {
	expected := strings.ToLower(strings.TrimSpace(expectedMAC))
	if strings.HasPrefix(expected, "hex:") {
		expected = strings.TrimPrefix(expected, "hex:")
	}
	if expected == "" {
		return fmt.Errorf("expected backup hmac is empty")
	}

	actual, err := computeBackupFileHMACSHA256(path, secret)
	if err != nil {
		return err
	}

	if !hmac.Equal([]byte(actual), []byte(expected)) {
		return fmt.Errorf("backup hmac verification failed")
	}

	return nil
}
