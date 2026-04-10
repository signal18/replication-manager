package backupmgr

import (
	"errors"
	"fmt"
	"strings"
)

var (
	ErrEncryptedRestoreMetadataMissing           = errors.New("encrypted restore metadata is missing")
	ErrEncryptedRestoreMetadataModeMissing       = errors.New("encrypted restore metadata missing encryption mode")
	ErrEncryptedRestoreMetadataIVMissing         = errors.New("encrypted restore metadata missing encryption IV")
	ErrEncryptedRestoreMetadataMACMissing        = errors.New("encrypted restore metadata missing encryption MAC")
	ErrEncryptedRestoreMetadataKeyRefMissing     = errors.New("encrypted restore metadata missing encryption key reference")
	ErrEncryptedRestoreMetadataKeyClusterMissing = errors.New("encrypted restore metadata missing encryption key cluster")
)

// ValidateEncryptedRestorePreflight enforces fail-closed metadata checks before
// any encrypted restore/decrypt flow is allowed to continue.
func ValidateEncryptedRestorePreflight(meta *BackupMetadata) error {
	if meta == nil {
		return ErrEncryptedRestoreMetadataMissing
	}

	if !meta.Encrypted {
		return nil
	}

	mode := strings.TrimSpace(meta.EncryptionAlgo)
	if mode == "" {
		return ErrEncryptedRestoreMetadataModeMissing
	}

	if err := ValidateLockedBackupAlgorithms(mode, BackupIntegrityAlgorithm); err != nil {
		return fmt.Errorf("encrypted restore metadata has invalid encryption mode: %w", err)
	}

	if strings.TrimSpace(meta.EncryptionIV) == "" {
		return ErrEncryptedRestoreMetadataIVMissing
	}

	if strings.TrimSpace(meta.EncryptionMAC) == "" {
		return ErrEncryptedRestoreMetadataMACMissing
	}

	keyRef := strings.TrimSpace(meta.EncryptionKey)
	if keyRef == "" {
		return ErrEncryptedRestoreMetadataKeyRefMissing
	}

	if err := ValidateBackupSecretKeyReference(keyRef); err != nil {
		return fmt.Errorf("encrypted restore metadata has invalid key reference: %w", err)
	}

	if strings.TrimSpace(meta.EncryptionKeyCluster) == "" {
		return ErrEncryptedRestoreMetadataKeyClusterMissing
	}

	return nil
}
