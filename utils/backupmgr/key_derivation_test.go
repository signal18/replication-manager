package backupmgr

import (
	"bytes"
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
