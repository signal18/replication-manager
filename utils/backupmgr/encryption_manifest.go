package backupmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// BackupEncryptionManifestVersion is the current manifest schema version.
	BackupEncryptionManifestVersion = 1

	// BackupEncryptionManifestFileSuffix is appended to the backup directory path
	// to derive the manifest file location.
	BackupEncryptionManifestFileSuffix = ".enc.manifest.json"
)

var (
	ErrEncryptedManifestNil              = errors.New("encryption manifest is nil")
	ErrEncryptedManifestVersionUnknown   = errors.New("encryption manifest version is unsupported")
	ErrEncryptedManifestKeyRefMissing    = errors.New("encryption manifest missing key reference")
	ErrEncryptedManifestKeyClusterMissing = errors.New("encryption manifest missing key cluster")
	ErrEncryptedManifestNoEntries        = errors.New("encryption manifest has no entries")
	ErrEncryptedManifestEntryPathMissing = errors.New("encryption manifest entry missing path")
	ErrEncryptedManifestEntryIVMissing   = errors.New("encryption manifest entry missing IV")
	ErrEncryptedManifestEntryMACMissing  = errors.New("encryption manifest entry missing MAC")
)

// BackupEncryptionManifestEntry holds the per-artifact crypto metadata for one file
// within a directory or chunked backup.
type BackupEncryptionManifestEntry struct {
	// Path is the path of the artifact relative to the backup directory root.
	Path string `json:"path"`
	// IV is the AES-256-CBC IV token in "hex:<hex-bytes>" format.
	IV string `json:"iv"`
	// MAC is the HMAC-SHA256 hex digest of the encrypted artifact.
	MAC string `json:"mac"`
}

// BackupEncryptionManifest holds the crypto metadata for all artifacts in a
// directory or chunked backup. It is written alongside the backup directory and
// consumed during restore preflight to verify each artifact before decryption.
type BackupEncryptionManifest struct {
	// Version identifies the manifest schema for compatibility checking.
	Version int `json:"version"`
	// KeyRef is the versioned secret reference used to encrypt all artifacts
	// in this manifest (e.g. "cloud18-sponsor-user-credentials:v3").
	KeyRef string `json:"keyRef"`
	// KeyCluster is the name of the cluster whose secret_store.json history
	// holds the versioned key material.
	KeyCluster string `json:"keyCluster"`
	// Entries contains one record per encrypted artifact.
	Entries []BackupEncryptionManifestEntry `json:"entries"`
}

// BackupEncryptionManifestPath returns the conventional manifest file path for
// a backup directory: {backupDir} + ".enc.manifest.json".
func BackupEncryptionManifestPath(backupDir string) string {
	return strings.TrimRight(backupDir, "/\\") + BackupEncryptionManifestFileSuffix
}

// WriteBackupEncryptionManifest serializes manifest to JSON and writes it to
// the conventional path alongside backupDir.
func WriteBackupEncryptionManifest(backupDir string, manifest *BackupEncryptionManifest) error {
	if manifest == nil {
		return ErrEncryptedManifestNil
	}
	manifest.Version = BackupEncryptionManifestVersion

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal encryption manifest: %w", err)
	}

	manifestPath := BackupEncryptionManifestPath(backupDir)
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		return fmt.Errorf("failed to write encryption manifest %s: %w", manifestPath, err)
	}

	return nil
}

// ReadBackupEncryptionManifest reads and deserializes the manifest from the
// conventional path alongside backupDir.
func ReadBackupEncryptionManifest(backupDir string) (*BackupEncryptionManifest, error) {
	manifestPath := BackupEncryptionManifestPath(backupDir)

	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("encryption manifest not found at %s: %w", manifestPath, err)
		}
		return nil, fmt.Errorf("failed to read encryption manifest %s: %w", manifestPath, err)
	}

	var manifest BackupEncryptionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse encryption manifest %s: %w", manifestPath, err)
	}

	return &manifest, nil
}

// ValidateBackupEncryptionManifest validates the manifest schema without any I/O.
// It checks that the version is supported, required fields are present, and each
// entry has the minimum required fields.
func ValidateBackupEncryptionManifest(manifest *BackupEncryptionManifest) error {
	if manifest == nil {
		return ErrEncryptedManifestNil
	}

	if manifest.Version < 1 || manifest.Version > BackupEncryptionManifestVersion {
		return fmt.Errorf("%w: %d", ErrEncryptedManifestVersionUnknown, manifest.Version)
	}

	if err := ValidateBackupSecretKeyReference(manifest.KeyRef); err != nil {
		return fmt.Errorf("%w: %v", ErrEncryptedManifestKeyRefMissing, err)
	}

	if strings.TrimSpace(manifest.KeyCluster) == "" {
		return ErrEncryptedManifestKeyClusterMissing
	}

	if len(manifest.Entries) == 0 {
		return ErrEncryptedManifestNoEntries
	}

	for i, entry := range manifest.Entries {
		if strings.TrimSpace(entry.Path) == "" {
			return fmt.Errorf("entry[%d]: %w", i, ErrEncryptedManifestEntryPathMissing)
		}
		if strings.TrimSpace(entry.IV) == "" {
			return fmt.Errorf("entry[%d] %q: %w", i, entry.Path, ErrEncryptedManifestEntryIVMissing)
		}
		if strings.TrimSpace(entry.MAC) == "" {
			return fmt.Errorf("entry[%d] %q: %w", i, entry.Path, ErrEncryptedManifestEntryMACMissing)
		}
	}

	return nil
}

// ValidateBackupEncryptionManifestEntries validates each manifest entry against
// the on-disk encrypted artifacts under baseDir.
//
// For each entry it checks:
//  1. The artifact file exists at filepath.Join(baseDir, entry.Path)
//  2. The HMAC-SHA256 of the on-disk file matches entry.MAC
//
// Restore fails closed: returns an error immediately on the first missing or
// tampered entry, without processing any remaining entries.
func ValidateBackupEncryptionManifestEntries(manifest *BackupEncryptionManifest, baseDir string, secret string) error {
	if err := ValidateBackupEncryptionManifest(manifest); err != nil {
		return fmt.Errorf("manifest structure invalid: %w", err)
	}

	for i, entry := range manifest.Entries {
		artifactPath := filepath.Join(strings.TrimRight(baseDir, "/\\"), entry.Path)

		if _, err := os.Stat(artifactPath); err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("manifest entry[%d] %q: artifact missing from disk (fail-closed)", i, entry.Path)
			}
			return fmt.Errorf("manifest entry[%d] %q: cannot access artifact: %w", i, entry.Path, err)
		}

		if err := VerifyBackupFileHMACSHA256(artifactPath, secret, entry.MAC); err != nil {
			return fmt.Errorf("manifest entry[%d] %q: HMAC verification failed (fail-closed): %w", i, entry.Path, err)
		}
	}

	return nil
}
