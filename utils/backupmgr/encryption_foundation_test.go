package backupmgr

import "testing"

func TestValidateLockedBackupAlgorithms(t *testing.T) {
	t.Run("accepts locked algorithms", func(t *testing.T) {
		if err := ValidateLockedBackupAlgorithms("aes-256-cbc", "hmac-sha256"); err != nil {
			t.Fatalf("expected locked algorithm combination to validate, got error: %v", err)
		}
	})

	t.Run("rejects unsupported encryption algorithm", func(t *testing.T) {
		if err := ValidateLockedBackupAlgorithms("aes-128-cbc", "hmac-sha256"); err == nil {
			t.Fatalf("expected unsupported encryption algorithm to fail validation")
		}
	})

	t.Run("rejects unsupported integrity algorithm", func(t *testing.T) {
		if err := ValidateLockedBackupAlgorithms("aes-256-cbc", "sha256"); err == nil {
			t.Fatalf("expected unsupported integrity algorithm to fail validation")
		}
	})
}

func TestResolveBackupEncryptionKeyMaterial(t *testing.T) {
	t.Run("prefers sponsor key material", func(t *testing.T) {
		secret, source, err := ResolveBackupEncryptionKeyMaterial("sponsor-user:sponsor-pass", "admin:admin-pass")
		if err != nil {
			t.Fatalf("expected sponsor credentials to resolve, got error: %v", err)
		}

		if secret != "sponsor-pass" {
			t.Fatalf("expected sponsor password, got %q", secret)
		}

		if source != BackupEncryptionSecretSourceSponsor {
			t.Fatalf("expected sponsor source, got %q", source)
		}
	})

	t.Run("falls back to admin api credential", func(t *testing.T) {
		secret, source, err := ResolveBackupEncryptionKeyMaterial("", "dba:dba-pass,admin:admin-pass")
		if err != nil {
			t.Fatalf("expected admin credentials fallback, got error: %v", err)
		}

		if secret != "admin-pass" {
			t.Fatalf("expected admin password, got %q", secret)
		}

		if source != BackupEncryptionSecretSourceAdmin {
			t.Fatalf("expected admin source, got %q", source)
		}
	})

	t.Run("errors when both sources are unavailable", func(t *testing.T) {
		_, _, err := ResolveBackupEncryptionKeyMaterial("", "dba:dba-pass")
		if err == nil {
			t.Fatalf("expected missing sponsor/admin credentials to fail")
		}
	})
}

func TestBackupSecretKeyReferenceFormattingAndParsing(t *testing.T) {
	t.Run("formats sponsor reference", func(t *testing.T) {
		ref, err := FormatBackupSecretKeyReference(BackupEncryptionSecretSourceSponsor, 3)
		if err != nil {
			t.Fatalf("unexpected format error: %v", err)
		}

		expected := "cloud18-sponsor-user-credentials:v3"
		if ref != expected {
			t.Fatalf("expected %q, got %q", expected, ref)
		}
	})

	t.Run("parses admin reference", func(t *testing.T) {
		source, version, err := ParseBackupSecretKeyReference("api-credentials/admin:v12")
		if err != nil {
			t.Fatalf("unexpected parse error: %v", err)
		}

		if source != BackupEncryptionSecretSourceAdmin {
			t.Fatalf("expected admin source, got %q", source)
		}

		if version != 12 {
			t.Fatalf("expected version 12, got %d", version)
		}
	})

	t.Run("rejects invalid references", func(t *testing.T) {
		invalidRefs := []string{
			"",
			"api-credentials/admin",
			"api-credentials/admin:v0",
			"api-credentials/admin:v-1",
			"api-credentials/admin:12",
			"unknown-source:v1",
		}

		for _, ref := range invalidRefs {
			if err := ValidateBackupSecretKeyReference(ref); err == nil {
				t.Fatalf("expected ref %q to be invalid", ref)
			}
		}
	})
}

func TestEnsureNoPlaintextEncryptionKey(t *testing.T) {
	t.Run("clears plaintext key and returns error", func(t *testing.T) {
		meta := &BackupMetadata{EncryptionKey: "super-secret-passphrase"}

		err := meta.EnsureNoPlaintextEncryptionKey()
		if err == nil {
			t.Fatalf("expected plaintext key to be rejected")
		}

		if meta.EncryptionKey != "" {
			t.Fatalf("expected plaintext encryption key to be cleared, got %q", meta.EncryptionKey)
		}
	})

	t.Run("keeps validated key reference", func(t *testing.T) {
		meta := &BackupMetadata{EncryptionKey: "cloud18-sponsor-user-credentials:v5"}

		err := meta.EnsureNoPlaintextEncryptionKey()
		if err != nil {
			t.Fatalf("expected valid key reference to be accepted, got error: %v", err)
		}

		if meta.EncryptionKey != "cloud18-sponsor-user-credentials:v5" {
			t.Fatalf("expected key reference to be preserved, got %q", meta.EncryptionKey)
		}
	})
}
