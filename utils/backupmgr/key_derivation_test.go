package backupmgr

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestDeriveDomainSeparatedLegacyAndStreamKeys(t *testing.T) {
	t.Parallel()

	rootSecret := []byte("root-secret-material")
	clusterName := "cluster-a"

	legacyKey, err := DeriveLegacyFileKey(rootSecret, clusterName)
	if err != nil {
		t.Fatalf("derive legacy key: %v", err)
	}

	streamContainerKey, err := DeriveStreamContainerKey(rootSecret, clusterName)
	if err != nil {
		t.Fatalf("derive stream container key: %v", err)
	}

	if len(legacyKey) != BackupDerivedKeyLength {
		t.Fatalf("expected %d-byte legacy key, got %d", BackupDerivedKeyLength, len(legacyKey))
	}
	if len(streamContainerKey) != BackupDerivedKeyLength {
		t.Fatalf("expected %d-byte stream key, got %d", BackupDerivedKeyLength, len(streamContainerKey))
	}

	if bytes.Equal(legacyKey, streamContainerKey) {
		t.Fatalf("expected domain-separated keys to differ between legacy and stream contexts")
	}
}

func TestDeriveStreamContainerKeyDeterministic(t *testing.T) {
	t.Parallel()

	rootSecret := []byte("root-secret-material")
	clusterName := "cluster-a"

	key1, err := DeriveStreamContainerKey(rootSecret, clusterName)
	if err != nil {
		t.Fatalf("derive container key first call: %v", err)
	}

	key2, err := DeriveStreamContainerKey(rootSecret, clusterName)
	if err != nil {
		t.Fatalf("derive container key second call: %v", err)
	}

	if !bytes.Equal(key1, key2) {
		t.Fatalf("expected deterministic container key derivation for same input")
	}
}

func TestDeriveStreamEntryKeyUniquenessAndConsistency(t *testing.T) {
	t.Parallel()

	rootSecret := []byte("root-secret-material")
	containerKey, err := DeriveStreamContainerKey(rootSecret, "cluster-a")
	if err != nil {
		t.Fatalf("derive container key: %v", err)
	}

	entryKeyA1, err := DeriveStreamEntryKey(containerKey, "schema/db1.sql")
	if err != nil {
		t.Fatalf("derive entry key A1: %v", err)
	}

	entryKeyA2, err := DeriveStreamEntryKey(containerKey, "schema/db1.sql")
	if err != nil {
		t.Fatalf("derive entry key A2: %v", err)
	}

	entryKeyB, err := DeriveStreamEntryKey(containerKey, "data/db1.tbl1.0001.sql")
	if err != nil {
		t.Fatalf("derive entry key B: %v", err)
	}

	if !bytes.Equal(entryKeyA1, entryKeyA2) {
		t.Fatalf("expected same entry ID to derive same key")
	}

	if bytes.Equal(entryKeyA1, entryKeyB) {
		t.Fatalf("expected different entry IDs to derive different keys")
	}
}

func TestDeriveKeyValidationErrors(t *testing.T) {
	t.Parallel()

	if _, err := DeriveLegacyFileKey(nil, "cluster-a"); err == nil {
		t.Fatalf("expected missing root secret to fail for legacy key")
	}

	if _, err := DeriveLegacyFileKey([]byte("secret"), ""); err == nil {
		t.Fatalf("expected missing cluster name to fail for legacy key")
	}

	if _, err := DeriveStreamContainerKey(nil, "cluster-a"); err == nil {
		t.Fatalf("expected missing root secret to fail for container key")
	}

	if _, err := DeriveStreamContainerKey([]byte("secret"), " "); err == nil {
		t.Fatalf("expected missing cluster name to fail for container key")
	}

	if _, err := DeriveStreamEntryKey(nil, "entry-a"); err == nil {
		t.Fatalf("expected missing container key to fail for entry key")
	}

	if _, err := DeriveStreamEntryKey([]byte("container"), ""); err == nil {
		t.Fatalf("expected missing entry ID to fail for entry key")
	}
}

func TestStreamEntryVersionContext(t *testing.T) {
	t.Parallel()

	ctx, err := StreamEntryVersionContext("schema/db1.sql")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "repman/backup/stream/v1/entry/schema/db1.sql"
	if ctx != expected {
		t.Fatalf("expected %q, got %q", expected, ctx)
	}

	if _, err := StreamEntryVersionContext("  "); err == nil {
		t.Fatalf("expected empty entry ID to fail")
	}
}

func TestResolveStreamRootSecretForReference(t *testing.T) {
	t.Parallel()

	t.Run("prefers sponsor source when available", func(t *testing.T) {
		secret, source, err := ResolveStreamRootSecretForReference(
			"sponsor:sponsor-pass",
			"dba:dba-pass,admin:admin-pass",
			"cloud18-sponsor-user-credentials:v1",
		)
		if err != nil {
			t.Fatalf("resolve stream root secret: %v", err)
		}

		if source != BackupEncryptionSecretSourceSponsor {
			t.Fatalf("expected sponsor source, got %q", source)
		}

		if string(secret) != "sponsor-pass" {
			t.Fatalf("expected sponsor password, got %q", string(secret))
		}
	})

	t.Run("falls back to admin source when sponsor missing", func(t *testing.T) {
		secret, source, err := ResolveStreamRootSecretForReference(
			"",
			"dba:dba-pass,admin:admin-pass",
			"api-credentials/admin:v2",
		)
		if err != nil {
			t.Fatalf("resolve stream root secret: %v", err)
		}

		if source != BackupEncryptionSecretSourceAdmin {
			t.Fatalf("expected admin source, got %q", source)
		}

		if string(secret) != "admin-pass" {
			t.Fatalf("expected admin password, got %q", string(secret))
		}
	})

	t.Run("fails when key reference source does not match resolved source", func(t *testing.T) {
		_, _, err := ResolveStreamRootSecretForReference(
			"sponsor:sponsor-pass",
			"dba:dba-pass,admin:admin-pass",
			"api-credentials/admin:v1",
		)
		if err == nil {
			t.Fatalf("expected mismatch between resolved source and key reference source to fail")
		}
	})

	t.Run("fails when no secret source exists", func(t *testing.T) {
		_, _, err := ResolveStreamRootSecretForReference(
			"",
			"dba:dba-pass",
			"api-credentials/admin:v1",
		)
		if err == nil {
			t.Fatalf("expected missing secret source to fail")
		}
	})

	t.Run("fails when key reference is invalid", func(t *testing.T) {
		_, _, err := ResolveStreamRootSecretForReference(
			"sponsor:sponsor-pass",
			"dba:dba-pass,admin:admin-pass",
			"not-a-key-reference",
		)
		if err == nil {
			t.Fatalf("expected invalid key reference to fail")
		}
	})
}

// ---------------------------------------------------------------------------
// Story 3.9: Key sentinel errors and wrong-context key derivation
// ---------------------------------------------------------------------------

// TestErrKeyResolutionFailedAndDerivationFailedSentinelsExported verifies that
// the sentinel error variables added for structured log categorisation are
// exported and non-nil.
func TestErrKeyResolutionFailedAndDerivationFailedSentinelsExported(t *testing.T) {
	t.Parallel()

	if ErrKeyResolutionFailed == nil {
		t.Error("ErrKeyResolutionFailed must not be nil")
	}
	if ErrKeyDerivationFailed == nil {
		t.Error("ErrKeyDerivationFailed must not be nil")
	}
	if ErrKeyResolutionFailed == ErrKeyDerivationFailed {
		t.Error("ErrKeyResolutionFailed and ErrKeyDerivationFailed must be distinct sentinel values")
	}
}

// TestWrongClusterKeyDerivationContextCausesFrameAuthFailure validates that
// using a container key derived for a different cluster name produces an
// authentication failure during frame decryption. This verifies that the
// HKDF domain separation for cluster names is enforced end-to-end and that
// a wrong-context key derivation fails closed (ErrFrameAuthFailed) rather
// than producing silent garbage plaintext.
func TestWrongClusterKeyDerivationContextCausesFrameAuthFailure(t *testing.T) {
	t.Parallel()

	rootSecret := []byte("root-secret-material-for-context-test!!")

	// Derive keys for two different clusters from the same root secret.
	containerKeyA, err := DeriveStreamContainerKey(rootSecret, "cluster-a")
	if err != nil {
		t.Fatalf("derive container key cluster-a: %v", err)
	}
	containerKeyB, err := DeriveStreamContainerKey(rootSecret, "cluster-b")
	if err != nil {
		t.Fatalf("derive container key cluster-b: %v", err)
	}

	// Sanity: the two container keys must be distinct.
	if bytes.Equal(containerKeyA, containerKeyB) {
		t.Fatal("cluster-a and cluster-b container keys must differ (domain separation)")
	}

	entryPath := "backup.sql"
	entryKeyA, err := DeriveStreamEntryKey(containerKeyA, entryPath)
	if err != nil {
		t.Fatalf("derive entry key for cluster-a: %v", err)
	}
	entryKeyB, err := DeriveStreamEntryKey(containerKeyB, entryPath)
	if err != nil {
		t.Fatalf("derive entry key for cluster-b: %v", err)
	}

	plaintext := []byte("sensitive backup payload — must not leak on wrong-context decrypt")

	// Encrypt with cluster-a's entry key.
	var buf bytes.Buffer
	w, err := NewFrameWriter(&buf, entryKeyA, StreamCipherSuiteAES256GCMHKDFSHA256, 64*1024)
	if err != nil {
		t.Fatalf("NewFrameWriter: %v", err)
	}
	if _, err := w.Write(plaintext); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	ciphertext := buf.Bytes()

	// Decrypt with cluster-b's entry key — wrong derivation context.
	r, err := NewFrameReader(context.Background(), bytes.NewReader(ciphertext), entryKeyB, StreamCipherSuiteAES256GCMHKDFSHA256)
	if err != nil {
		t.Fatalf("NewFrameReader: %v", err)
	}

	_, readErr := r.Read(make([]byte, 4096))
	if !errors.Is(readErr, ErrFrameAuthFailed) {
		t.Errorf("expected ErrFrameAuthFailed when decrypting with wrong cluster key, got: %v", readErr)
	}
}
