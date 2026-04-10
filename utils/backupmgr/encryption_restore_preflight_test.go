package backupmgr

import (
	"errors"
	"testing"
)

func validEncryptedRestoreMeta() *BackupMetadata {
	return &BackupMetadata{
		Encrypted:            true,
		EncryptionAlgo:       BackupEncryptionAlgorithm,
		EncryptionIV:         "b64:0123456789abcdef",
		EncryptionMAC:        "b64:0123456789abcdef0123456789abcdef",
		EncryptionKey:        "cloud18-sponsor-user-credentials:v3",
		EncryptionKeyCluster: "clusterA",
	}
}

func TestValidateEncryptedRestorePreflight(t *testing.T) {
	t.Run("allows non encrypted metadata", func(t *testing.T) {
		meta := &BackupMetadata{Encrypted: false}
		if err := ValidateEncryptedRestorePreflight(meta); err != nil {
			t.Fatalf("expected non-encrypted metadata to pass, got: %v", err)
		}
	})

	t.Run("accepts complete encrypted metadata", func(t *testing.T) {
		if err := ValidateEncryptedRestorePreflight(validEncryptedRestoreMeta()); err != nil {
			t.Fatalf("expected valid encrypted metadata to pass, got: %v", err)
		}
	})

	t.Run("fails when metadata is missing", func(t *testing.T) {
		err := ValidateEncryptedRestorePreflight(nil)
		if !errors.Is(err, ErrEncryptedRestoreMetadataMissing) {
			t.Fatalf("expected metadata missing error, got: %v", err)
		}
	})

	t.Run("fails when encryption mode missing", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionAlgo = ""
		err := ValidateEncryptedRestorePreflight(meta)
		if !errors.Is(err, ErrEncryptedRestoreMetadataModeMissing) {
			t.Fatalf("expected mode missing error, got: %v", err)
		}
	})

	t.Run("fails when encryption mode is unsupported", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionAlgo = "aes-128-cbc"
		err := ValidateEncryptedRestorePreflight(meta)
		if err == nil {
			t.Fatalf("expected unsupported mode to fail")
		}
	})

	t.Run("fails when iv missing", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionIV = ""
		err := ValidateEncryptedRestorePreflight(meta)
		if !errors.Is(err, ErrEncryptedRestoreMetadataIVMissing) {
			t.Fatalf("expected iv missing error, got: %v", err)
		}
	})

	t.Run("fails when mac missing", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionMAC = ""
		err := ValidateEncryptedRestorePreflight(meta)
		if !errors.Is(err, ErrEncryptedRestoreMetadataMACMissing) {
			t.Fatalf("expected mac missing error, got: %v", err)
		}
	})

	t.Run("fails when key reference missing", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionKey = ""
		err := ValidateEncryptedRestorePreflight(meta)
		if !errors.Is(err, ErrEncryptedRestoreMetadataKeyRefMissing) {
			t.Fatalf("expected key reference missing error, got: %v", err)
		}
	})

	t.Run("fails when key reference format invalid", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionKey = "api-credentials/admin"
		err := ValidateEncryptedRestorePreflight(meta)
		if err == nil {
			t.Fatalf("expected invalid key reference format to fail")
		}
	})

	t.Run("fails when key cluster missing", func(t *testing.T) {
		meta := validEncryptedRestoreMeta()
		meta.EncryptionKeyCluster = ""
		err := ValidateEncryptedRestorePreflight(meta)
		if !errors.Is(err, ErrEncryptedRestoreMetadataKeyClusterMissing) {
			t.Fatalf("expected key cluster missing error, got: %v", err)
		}
	})
}
