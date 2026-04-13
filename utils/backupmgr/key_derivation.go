package backupmgr

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/hkdf"
)

const (
	BackupDerivedKeyLength = 32

	BackupKeyContextLegacyFileV1      = "repman/backup/legacy-file/v1"
	BackupKeyContextStreamContainerV1 = "repman/backup/stream/v1/container"
	BackupKeyContextStreamEntryV1Base = "repman/backup/stream/v1/entry"
)

// StreamEntryVersionContext returns the domain-separated stream entry context
// string for KDF operations.
func StreamEntryVersionContext(entryID string) (string, error) {
	trimmedEntryID := strings.TrimSpace(entryID)
	if trimmedEntryID == "" {
		return "", fmt.Errorf("stream entry id is required")
	}

	return fmt.Sprintf("%s/%s", BackupKeyContextStreamEntryV1Base, trimmedEntryID), nil
}

// DeriveStreamContainerKey derives a container-level key from root secret.
func DeriveStreamContainerKey(rootSecret []byte, clusterName string) ([]byte, error) {
	trimmedClusterName := strings.TrimSpace(clusterName)
	if len(rootSecret) == 0 {
		return nil, fmt.Errorf("root secret is required")
	}
	if trimmedClusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	return deriveHKDFSHA256Key(rootSecret, scopedContext(BackupKeyContextStreamContainerV1, trimmedClusterName))
}

// DeriveStreamEntryKey derives a per-entry key from a container key.
func DeriveStreamEntryKey(containerKey []byte, entryID string) ([]byte, error) {
	if len(containerKey) == 0 {
		return nil, fmt.Errorf("container key is required")
	}

	entryContext, err := StreamEntryVersionContext(entryID)
	if err != nil {
		return nil, err
	}

	return deriveHKDFSHA256Key(containerKey, entryContext)
}

// DeriveLegacyFileKey derives legacy-format key material from root secret.
func DeriveLegacyFileKey(rootSecret []byte, clusterName string) ([]byte, error) {
	trimmedClusterName := strings.TrimSpace(clusterName)
	if len(rootSecret) == 0 {
		return nil, fmt.Errorf("root secret is required")
	}
	if trimmedClusterName == "" {
		return nil, fmt.Errorf("cluster name is required")
	}

	return deriveHKDFSHA256Key(rootSecret, scopedContext(BackupKeyContextLegacyFileV1, trimmedClusterName))
}

// ResolveStreamRootSecretForReference resolves root secret bytes using the
// legacy sponsor -> admin priority and verifies that the selected source matches
// the provided key reference source.
func ResolveStreamRootSecretForReference(sponsorCredentials string, apiCredentials string, keyID string) ([]byte, string, error) {
	refSource, _, err := ParseBackupSecretKeyReference(keyID)
	if err != nil {
		return nil, "", err
	}

	secret, source, err := ResolveBackupEncryptionKeyMaterial(sponsorCredentials, apiCredentials)
	if err != nil {
		return nil, "", err
	}

	if source != refSource {
		return nil, "", fmt.Errorf("resolved backup secret source %q does not match key reference source %q", source, refSource)
	}

	return []byte(secret), source, nil
}

func deriveHKDFSHA256Key(inputKeyMaterial []byte, info string) ([]byte, error) {
	hkdfReader := hkdf.New(sha256.New, inputKeyMaterial, nil, []byte(info))
	key := make([]byte, BackupDerivedKeyLength)
	if _, err := io.ReadFull(hkdfReader, key); err != nil {
		return nil, fmt.Errorf("derive key with hkdf-sha256: %w", err)
	}

	return key, nil
}

func scopedContext(baseContext string, clusterName string) string {
	return fmt.Sprintf("%s|cluster=%s", baseContext, clusterName)
}
