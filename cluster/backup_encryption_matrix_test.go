package cluster

// Story 2.9: Regression, Negative Testing, and Operational Readiness
//
// This file implements the test matrix required by Story 2.9:
//   Task 1 — Encrypted / non-encrypted roundtrip matrix for all backup types
//   Task 2 — Negative tests: MAC mismatch, wrong key version, missing store history,
//             missing metadata fields (IV / MAC / key reference)
//   Task 3 — Explicit no-fallback verification: resolver must NOT use current
//             Secrets config when the referenced version is absent from the store
//
// Restic-level encryption coverage is in backup_restic_encryption_test.go.
// Single-file restore pipeline negative tests are in backup_single_file_restore_test.go.
// Directory and splitdump per-type negative tests are in their own test files.
// This file adds the cross-type matrix view and the explicit no-fallback tests.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// ============================================================
// Task 1: Roundtrip matrix — all backup types × both modes
// ============================================================

// TestEncryptionRoundtripMatrixDirectoryTools exercises every directory-backed
// tool (mydumper, dumpling, splitdump) in both encrypted and non-encrypted mode.
func TestEncryptionRoundtripMatrixDirectoryTools(t *testing.T) {
	tools := []string{"mydumper", "dumpling", "splitdump"}

	origFiles := map[string][]byte{
		"table1.sql.gz": []byte("payload-table1"),
		"table2.sql.gz": []byte("payload-table2"),
	}

	for _, tool := range tools {
		tool := tool

		t.Run(tool+"/non-encrypted-passthrough", func(t *testing.T) {
			root := t.TempDir()
			cl := &Cluster{
				Name: "matrix-plain-" + tool,
				Conf: &config.Config{WorkingDir: root},
			}
			dir := createBackupDir(t, origFiles)

			if err := cl.runEncryptedDirectoryRestorePipeline(dir); err != nil {
				t.Fatalf("non-encrypted passthrough failed for %s: %v", tool, err)
			}

			for name, original := range origFiles {
				current, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("read %s: %v", name, err)
				}
				if !bytes.Equal(original, current) {
					t.Errorf("%s: file %q changed in non-encrypted passthrough", tool, name)
				}
			}
		})

		t.Run(tool+"/encrypted-roundtrip", func(t *testing.T) {
			root := t.TempDir()
			clusterName := "matrix-enc-" + tool
			cl := &Cluster{
				Name: clusterName,
				Conf: &config.Config{
					WorkingDir: root,
					Secrets: map[string]config.Secret{
						"cloud18-sponsor-user-credentials": {Value: "sponsor:matrix-pass"},
					},
				},
			}
			storePath := SecretVersionStorePath(root, clusterName)
			writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 1, "hash_value": "sponsor:matrix-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)
			dir := createBackupDir(t, origFiles)
			server := &ServerMonitor{ClusterGroup: cl}
			server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: dir}

			if err := server.finalizeDirectoryEncryption(dir, tool); err != nil {
				t.Fatalf("%s: encrypt failed: %v", tool, err)
			}
			if err := cl.runEncryptedDirectoryRestorePipeline(dir); err != nil {
				t.Fatalf("%s: restore pipeline failed: %v", tool, err)
			}

			for name, original := range origFiles {
				current, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("read %s after restore: %v", name, err)
				}
				if !bytes.Equal(original, current) {
					t.Errorf("%s: file %q not restored to original plaintext", tool, name)
				}
			}
		})
	}
}

// TestEncryptionRoundtripMatrixSingleFile covers mysqldump single-file
// backup in both encrypted and non-encrypted mode.
func TestEncryptionRoundtripMatrixSingleFile(t *testing.T) {
	t.Run("non-encrypted/file-unchanged", func(t *testing.T) {
		cl := &Cluster{
			Name: "singlefile-plain-matrix",
			Conf: &config.Config{BackupEncryption: false},
		}
		server := &ServerMonitor{ClusterGroup: cl}
		path := filepath.Join(t.TempDir(), "dump.sql.gz")
		original := []byte("non-encrypted-matrix-payload")
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: path}

		if err := server.finalizeMysqldumpSingleFileEncryption(path); err != nil {
			t.Fatalf("expected no-op in disabled mode: %v", err)
		}
		meta := server.LastBackupMeta.Logical
		if meta.Encrypted {
			t.Fatal("expected metadata.Encrypted=false in non-encrypted mode")
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(original, current) {
			t.Fatal("expected file unchanged in non-encrypted mode")
		}
	})

	t.Run("encrypted/roundtrip", func(t *testing.T) {
		root := t.TempDir()
		clusterName := "singlefile-enc-matrix"
		cl := &Cluster{
			Name: clusterName,
			Conf: &config.Config{
				WorkingDir:             root,
				BackupEncryption: true,
				Secrets: map[string]config.Secret{
					"cloud18-sponsor-user-credentials": {Value: "sponsor:roundtrip-matrix-pass"},
				},
			},
		}
		storePath := SecretVersionStorePath(root, clusterName)
		writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 1, "hash_value": "sponsor:roundtrip-matrix-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)
		path := filepath.Join(t.TempDir(), "dump.sql.gz")
		original := []byte("roundtrip-matrix-payload")
		if err := os.WriteFile(path, original, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		server := &ServerMonitor{ClusterGroup: cl}
		server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: path}

		if err := server.finalizeMysqldumpSingleFileEncryption(path); err != nil {
			t.Fatalf("encrypt: %v", err)
		}
		meta := server.LastBackupMeta.Logical
		if !meta.Encrypted {
			t.Fatal("expected metadata.Encrypted=true after single-file encryption")
		}
		if err := cl.runEncryptedSingleFileRestorePipeline(path, meta); err != nil {
			t.Fatalf("restore: %v", err)
		}
		current, _ := os.ReadFile(path)
		if !bytes.Equal(original, current) {
			t.Fatal("expected restored bytes to match original plaintext")
		}
	})
}

// ============================================================
// Task 2: Negative tests
// ============================================================

// TestNegativeMACMismatchMatrix verifies MAC mismatch fails closed for both
// the single-file restore pipeline and the directory restore pipeline.
func TestNegativeMACMismatchMatrix(t *testing.T) {
	t.Run("single-file/mac-mismatch-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		cl := newEncryptionTestCluster(t, root, "mac-matrix-single-cluster")
		path := filepath.Join(t.TempDir(), "dump.sql.gz")
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		meta := encryptFixtureWithMeta(t, path, "sponsor-pass", cl)
		meta.EncryptionMAC = "0000000000000000000000000000000000000000000000000000000000000000"

		err := cl.runEncryptedSingleFileRestorePipeline(path, meta)
		if err == nil {
			t.Fatal("expected MAC mismatch to fail restore closed")
		}
		if !strings.Contains(err.Error(), "HMAC") {
			t.Fatalf("expected HMAC error, got: %v", err)
		}
	})

	t.Run("directory/mac-mismatch-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		cl := newDirectoryEncryptionCluster(t, root, "mac-matrix-dir-cluster")
		dir := createBackupDir(t, map[string][]byte{"f.sql.gz": []byte("payload")})
		server := &ServerMonitor{ClusterGroup: cl}
		server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: dir}
		if err := server.finalizeDirectoryEncryption(dir, "mydumper"); err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		manifestPath := backupmgr.BackupEncryptionManifestPath(dir)
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read manifest: %v", err)
		}
		var manifest backupmgr.BackupEncryptionManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
		manifest.Entries[0].MAC = "0000000000000000000000000000000000000000000000000000000000000000"
		tampered, _ := json.Marshal(manifest)
		if err := os.WriteFile(manifestPath, tampered, 0o600); err != nil {
			t.Fatalf("write tampered manifest: %v", err)
		}

		if err := cl.runEncryptedDirectoryRestorePipeline(dir); err == nil {
			t.Fatal("expected MAC mismatch in directory manifest to fail restore closed")
		}
	})
}

// TestNegativeWrongKeyVersionMatrix verifies that referencing a key version that
// does not exist in the store fails closed for both single-file and directory.
func TestNegativeWrongKeyVersionMatrix(t *testing.T) {
	t.Run("single-file/wrong-version-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		cl := newEncryptionTestCluster(t, root, "wrong-ver-single-matrix")
		path := filepath.Join(t.TempDir(), "dump.sql.gz")
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		meta := encryptFixtureWithMeta(t, path, "sponsor-pass", cl)
		meta.EncryptionKey = "cloud18-sponsor-user-credentials:v99"

		err := cl.runEncryptedSingleFileRestorePipeline(path, meta)
		if err == nil {
			t.Fatal("expected non-existent key version to fail restore")
		}
		if !strings.Contains(err.Error(), "key resolution") {
			t.Fatalf("expected key resolution error, got: %v", err)
		}
	})

	t.Run("directory/wrong-version-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		cl := newDirectoryEncryptionCluster(t, root, "wrong-ver-dir-matrix")
		dir := createBackupDir(t, map[string][]byte{"f.sql": []byte("data")})
		server := &ServerMonitor{ClusterGroup: cl}
		server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: dir}
		if err := server.finalizeDirectoryEncryption(dir, "mydumper"); err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		manifestPath := backupmgr.BackupEncryptionManifestPath(dir)
		data, _ := os.ReadFile(manifestPath)
		var manifest backupmgr.BackupEncryptionManifest
		json.Unmarshal(data, &manifest)
		manifest.KeyRef = "cloud18-sponsor-user-credentials:v99"
		tampered, _ := json.Marshal(manifest)
		os.WriteFile(manifestPath, tampered, 0o600)

		err := cl.runEncryptedDirectoryRestorePipeline(dir)
		if err == nil {
			t.Fatal("expected wrong key version to fail directory restore")
		}
		if !strings.Contains(err.Error(), "key resolution") {
			t.Fatalf("expected key resolution error, got: %v", err)
		}
	})
}

// TestNegativeMissingSecretHistoryMatrix verifies that a missing secret_store.json
// fails closed for both single-file and directory restore pipelines.
func TestNegativeMissingSecretHistoryMatrix(t *testing.T) {
	t.Run("single-file/missing-store-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		// No store file written — only Secrets config exists.
		cl := &Cluster{
			Name: "no-store-single-matrix",
			Conf: &config.Config{
				WorkingDir: root,
				Secrets:    map[string]config.Secret{
					"cloud18-sponsor-user-credentials": {Value: "sponsor:some-pass"},
				},
			},
		}
		path := filepath.Join(t.TempDir(), "dump.sql.gz")
		if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		meta := &backupmgr.BackupMetadata{
			Dest:                 path,
			Encrypted:            true,
			EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
			EncryptionIV:         "hex:aabbccdd00112233aabbccdd00112233",
			EncryptionMAC:        "aabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd",
			EncryptionKey:        "cloud18-sponsor-user-credentials:v1",
			EncryptionKeyCluster: "no-store-single-matrix",
		}

		err := cl.runEncryptedSingleFileRestorePipeline(path, meta)
		if err == nil {
			t.Fatal("expected missing secret_store to fail restore closed")
		}
		if !strings.Contains(err.Error(), "key resolution") {
			t.Fatalf("expected key resolution error (wrapping secret_store failure), got: %v", err)
		}
	})

	t.Run("resolver/missing-store-fails-closed", func(t *testing.T) {
		root := t.TempDir()
		cl := &Cluster{
			Name: "no-store-resolver-matrix",
			Conf: &config.Config{
				WorkingDir: root,
				Secrets:    map[string]config.Secret{
					"cloud18-sponsor-user-credentials": {Value: "sponsor:some-pass"},
				},
			},
		}
		// No store file — resolution must fail.
		_, err := cl.resolveEncryptedRestoreSecretFromReference("cloud18-sponsor-user-credentials:v1", "no-store-resolver-matrix")
		if err == nil {
			t.Fatal("expected missing store to fail resolution")
		}
		if !strings.Contains(err.Error(), "secret_store") {
			t.Fatalf("expected secret_store context in error, got: %v", err)
		}
	})
}

// TestNegativeMissingMetadataFieldsMatrix verifies that missing IV, MAC, and key
// reference each fail closed at the preflight stage before any decryption attempt.
func TestNegativeMissingMetadataFieldsMatrix(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "missing-fields-matrix-cluster")
	path := filepath.Join(t.TempDir(), "dump.sql.gz")
	if err := os.WriteFile(path, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cases := []struct {
		name    string
		iv      string
		mac     string
		keyRef  string
	}{
		{"missing-IV", "", "aabbccdd", "cloud18-sponsor-user-credentials:v3"},
		{"missing-MAC", "hex:aabb", "", "cloud18-sponsor-user-credentials:v3"},
		{"missing-key-reference", "hex:aabb", "aabbccdd", ""},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			meta := &backupmgr.BackupMetadata{
				Dest:                 path,
				Encrypted:            true,
				EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
				EncryptionIV:         tc.iv,
				EncryptionMAC:        tc.mac,
				EncryptionKey:        tc.keyRef,
				EncryptionKeyCluster: cl.Name,
			}
			err := cl.runEncryptedSingleFileRestorePipeline(path, meta)
			if err == nil {
				t.Fatalf("%s: expected preflight failure, got no error", tc.name)
			}
			if !strings.Contains(err.Error(), "preflight") {
				t.Fatalf("%s: expected preflight error, got: %v", tc.name, err)
			}
		})
	}
}

// ============================================================
// Task 3: No-fallback to current Secrets config
// ============================================================

// TestNoFallbackToCurrentSecretWhenVersionMissing verifies that when the
// secret_store.json exists but does not contain the referenced version, the
// resolver fails closed without falling back to the current Secrets config value.
//
// This is the key invariant: secret history is required for restore; the current
// live credential in Conf.Secrets is NOT used as a substitute.
func TestNoFallbackToCurrentSecretWhenVersionMissing(t *testing.T) {
	root := t.TempDir()
	clusterName := "no-fallback-missing-ver"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir: root,
			// Secrets config HAS the current sponsor value.
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:current-live-pass"},
			},
		},
	}
	// Store exists but only has v1 — backup references v5.
	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 1, "hash_value": "sponsor:old-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	_, err := cl.resolveEncryptedRestoreSecretFromReference("cloud18-sponsor-user-credentials:v5", clusterName)
	if err == nil {
		t.Fatal("expected failure when version v5 is not in store history — Secrets config must not be used as fallback")
	}
	// The error must NOT be the "secret_store missing" message — the store exists;
	// the version is simply absent from it.
	if strings.Contains(err.Error(), "is missing") {
		t.Fatalf("unexpected 'store missing' error path — store exists but version is absent: %v", err)
	}
}

// TestNoFallbackToCurrentSecretWhenStoreMissing verifies that when Secrets config
// is populated but secret_store.json does not exist, the resolver fails closed.
// The current live credential in Conf.Secrets is NOT used as a substitute.
func TestNoFallbackToCurrentSecretWhenStoreMissing(t *testing.T) {
	root := t.TempDir()
	clusterName := "no-fallback-missing-store"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir: root,
			// Secrets config HAS the current value — must NOT be used.
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:current-live-pass"},
			},
		},
	}
	// Deliberately write NO store file.

	_, err := cl.resolveEncryptedRestoreSecretFromReference("cloud18-sponsor-user-credentials:v1", clusterName)
	if err == nil {
		t.Fatal("expected failure when secret_store.json is absent even when Secrets config is populated")
	}
	if !strings.Contains(err.Error(), "secret_store") {
		t.Fatalf("expected secret_store context in error, got: %v", err)
	}
}

// TestNoFallbackAdminCredentialsVersionMissing verifies no-fallback for the
// api-credentials/admin key source (the secondary key source).
func TestNoFallbackAdminCredentialsVersionMissing(t *testing.T) {
	root := t.TempDir()
	clusterName := "no-fallback-admin"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir: root,
			Secrets: map[string]config.Secret{
				"api-credentials": {Value: "dba:dba-pass,admin:current-admin-pass"},
			},
		},
	}
	// Store has v1, backup references v3.
	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "api-credentials": [
    {"version": 1, "hash_value": "dba:dba-pass,admin:old-admin-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	_, err := cl.resolveEncryptedRestoreSecretFromReference("api-credentials/admin:v3", clusterName)
	if err == nil {
		t.Fatal("expected failure for missing version v3 in api-credentials history — must not fall back to current Secrets config")
	}
	if strings.Contains(err.Error(), "is missing") {
		t.Fatalf("unexpected 'store missing' error — store exists but version is absent: %v", err)
	}
}

// ============================================================
// IV mismatch behavior
// ============================================================

// TestIVMismatchBehaviorSingleFile documents the behavior when only the IV in
// the stored metadata is tampered (without changing the ciphertext or its MAC).
//
// Since HMAC is computed over the ciphertext bytes, a tampered IV alone does not
// invalidate the MAC — the HMAC check passes and AES-CBC decryption proceeds with
// the wrong IV. The resulting plaintext will be corrupt for the first cipher block
// (16 bytes) due to the CBC XOR operation.
//
// This test asserts the invariant: a wrong IV must NEVER silently produce correct
// plaintext. If the implementation ever allowed that, it would mean the IV has no
// effect, which is a cryptographic regression.
func TestIVMismatchBehaviorSingleFile(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "iv-mismatch-cluster")

	original := []byte("iv-mismatch-test-payload-longer-than-16-bytes")
	path := filepath.Join(t.TempDir(), "dump.sql.gz")
	if err := os.WriteFile(path, original, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	meta := encryptFixtureWithMeta(t, path, "sponsor-pass", cl)
	originalIV := meta.EncryptionIV

	// Use a different all-F IV — MAC stays valid (ciphertext unchanged).
	meta.EncryptionIV = "hex:ffffffffffffffffffffffffffffffff"
	if meta.EncryptionIV == originalIV {
		t.Skip("tampered IV matches original; skipping")
	}

	// Pipeline may return an error (e.g., decryption padding error) or succeed
	// with corrupt output — both outcomes are acceptable. What is NOT acceptable
	// is producing the original plaintext from the wrong IV.
	err := cl.runEncryptedSingleFileRestorePipeline(path, meta)
	if err != nil {
		// Decrypt failure is a valid fail-closed outcome for a wrong IV.
		return
	}
	restored, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read restored file: %v", readErr)
	}
	if bytes.Equal(original, restored) {
		t.Fatal("wrong IV must not silently produce correct plaintext — cryptographic invariant violated")
	}
}
