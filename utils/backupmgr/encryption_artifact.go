package backupmgr

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StreamContainerDefaultFrameSize is the per-frame plaintext chunk size used when
// creating new stream container backups.
const StreamContainerDefaultFrameSize = 64 * 1024 // 64 KiB

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

// EncryptFileAsStreamContainer encrypts a file in-place using the RMSC stream
// container format (AEAD per-frame, AES-256-GCM + HKDF-SHA256). The encrypted
// file replaces the original atomically via a temp-file rename.
//
// Parameters:
//   - path: path of the plaintext file to encrypt
//   - rootSecret: resolved root secret bytes (from ResolveBackupEncryptionKeyMaterial)
//   - clusterName: cluster identifier used in key derivation
//   - entryPath: logical name of the entry inside the container (e.g. "backup.sql")
//   - keyID: formatted key reference produced by FormatBackupSecretKeyReference
//
// The resulting file can be decoded with ReadPreflight → DeriveStreamContainerKey
// → DeriveStreamEntryKey → NewFrameReader.
func EncryptFileAsStreamContainer(path string, rootSecret []byte, clusterName, entryPath, keyID string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("stream container encryption: path is empty")
	}
	if len(rootSecret) == 0 {
		return fmt.Errorf("stream container encryption: root secret is empty")
	}
	if strings.TrimSpace(clusterName) == "" {
		return fmt.Errorf("stream container encryption: cluster name is empty")
	}
	if strings.TrimSpace(entryPath) == "" {
		return fmt.Errorf("stream container encryption: entry path is empty")
	}
	if err := ValidateBackupSecretKeyReference(keyID); err != nil {
		return fmt.Errorf("stream container encryption: invalid key ID: %w", err)
	}

	containerKey, err := DeriveStreamContainerKey(rootSecret, clusterName)
	if err != nil {
		return fmt.Errorf("stream container encryption: container key derivation: %w", err)
	}
	entryKey, err := DeriveStreamEntryKey(containerKey, entryPath)
	if err != nil {
		return fmt.Errorf("stream container encryption: entry key derivation: %w", err)
	}

	origInfo, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stream container encryption: stat %s: %w", path, err)
	}

	preflight := &StreamPreflight{
		Magic:       StreamContainerMagic,
		Version:     StreamContainerVersionV1,
		Mode:        StreamModeSingleFile,
		CipherSuite: StreamCipherSuiteAES256GCMHKDFSHA256,
		FrameSize:   StreamContainerDefaultFrameSize,
		KeyRef: StreamKeyReference{
			KeyID:          keyID,
			KeyCluster:     strings.TrimSpace(clusterName),
			VersionContext: BackupKeyContextStreamContainerV1,
		},
		Entries: []StreamEntryIndex{
			{
				Path:      strings.TrimSpace(entryPath),
				Class:     StreamEntryClassData,
				SizeBytes: uint64(origInfo.Size()),
				OrderHint: 1,
				GroupHint: "full",
			},
		},
	}

	header, err := EncodePreflight(preflight)
	if err != nil {
		return fmt.Errorf("stream container encryption: encode preflight: %w", err)
	}

	tmpFile, err := os.CreateTemp(filepath.Dir(path), "stream-container-*.tmp")
	if err != nil {
		return fmt.Errorf("stream container encryption: create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	committed := false
	defer func() {
		if !committed {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmpFile.Write(header); err != nil {
		tmpFile.Close()
		return fmt.Errorf("stream container encryption: write preflight: %w", err)
	}

	src, err := os.Open(path)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("stream container encryption: open source %s: %w", path, err)
	}
	defer src.Close()

	fw, err := NewFrameWriter(tmpFile, entryKey, StreamCipherSuiteAES256GCMHKDFSHA256, StreamContainerDefaultFrameSize)
	if err != nil {
		tmpFile.Close()
		return fmt.Errorf("stream container encryption: create frame writer: %w", err)
	}
	if _, err := io.Copy(fw, src); err != nil {
		tmpFile.Close()
		return fmt.Errorf("stream container encryption: encrypt frames: %w", err)
	}
	if err := fw.Close(); err != nil {
		tmpFile.Close()
		return fmt.Errorf("stream container encryption: finalize frames: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("stream container encryption: close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("stream container encryption: rename to %s: %w", path, err)
	}
	committed = true
	return nil
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
