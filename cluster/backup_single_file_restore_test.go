package cluster

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// encryptFixtureWithMeta encrypts path in-place and returns populated BackupMetadata.
// Registers cleanup via t.Cleanup so callers don't need to manage temp files.
func encryptFixtureWithMeta(t *testing.T, path string, secret string, cluster *Cluster) *backupmgr.BackupMetadata {
	t.Helper()

	ivToken, err := backupmgr.EncryptBackupFileAES256CBC(path, secret)
	if err != nil {
		t.Fatalf("fixture encrypt: %v", err)
	}

	mac, err := backupmgr.ComputeBackupFileHMACSHA256(path, secret)
	if err != nil {
		t.Fatalf("fixture hmac: %v", err)
	}

	return &backupmgr.BackupMetadata{
		Dest:                 path,
		Encrypted:            true,
		EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
		EncryptionIV:         ivToken,
		EncryptionMAC:        mac,
		EncryptionKey:        "cloud18-sponsor-user-credentials:v3",
		EncryptionKeyCluster: cluster.Name,
	}
}

// newEncryptionTestCluster creates a minimal cluster with a secret store fixture
// containing cloud18-sponsor-user-credentials:v3 → "sponsor-pass".
func newEncryptionTestCluster(t *testing.T, root, name string) *Cluster {
	t.Helper()
	cl := &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir: root,
			Verbose:    false,
		},
	}
	storePath := SecretVersionStorePath(root, name)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 3, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ],
  "api-credentials": [
    {"version": 7, "hash_value": "dba:dba-pass,admin:admin-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)
	return cl
}

func TestRunEncryptedSingleFileRestorePipelineSucceeds(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "restore-cluster")

	original := []byte("original-backup-payload")
	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	meta := encryptFixtureWithMeta(t, backupPath, "sponsor-pass", cl)

	if err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta); err != nil {
		t.Fatalf("expected pipeline to succeed, got: %v", err)
	}

	restored, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read restored: %v", err)
	}
	if !bytes.Equal(original, restored) {
		t.Fatal("expected restored bytes to match original after pipeline decryption")
	}
}

func TestRunEncryptedSingleFileRestorePipelineFailsOnMACMismatch(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "mac-mismatch-cluster")

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	meta := encryptFixtureWithMeta(t, backupPath, "sponsor-pass", cl)
	// Tamper with the MAC — pipeline must abort before decrypting.
	meta.EncryptionMAC = "deadbeefdeadbeefdeadbeefdeadbeef"

	err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
	if err == nil {
		t.Fatal("expected MAC mismatch to fail restore")
	}
	if !strings.Contains(err.Error(), "HMAC") {
		t.Fatalf("expected HMAC error context, got: %v", err)
	}
}

func TestRunEncryptedSingleFileRestorePipelineFailsOnWrongKeyVersion(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "wrong-version-cluster")

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	meta := encryptFixtureWithMeta(t, backupPath, "sponsor-pass", cl)
	// Point to a version that does not exist in the store.
	meta.EncryptionKey = "cloud18-sponsor-user-credentials:v99"

	err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
	if err == nil {
		t.Fatal("expected non-existent key version to fail restore")
	}
	if !strings.Contains(err.Error(), "key resolution") {
		t.Fatalf("expected key resolution error context, got: %v", err)
	}
}

func TestRunEncryptedSingleFileRestorePipelineFailsOnMissingSecretStore(t *testing.T) {
	root := t.TempDir()
	// Create cluster without writing any secret store file.
	cl := &Cluster{
		Name: "no-store-cluster",
		Conf: &config.Config{WorkingDir: root},
	}

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	meta := &backupmgr.BackupMetadata{
		Dest:                 backupPath,
		Encrypted:            true,
		EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
		EncryptionIV:         "hex:aabbccdd",
		EncryptionMAC:        "aabbccdd",
		EncryptionKey:        "cloud18-sponsor-user-credentials:v1",
		EncryptionKeyCluster: "no-store-cluster",
	}

	err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
	if err == nil {
		t.Fatal("expected missing secret_store to fail restore")
	}
	if !strings.Contains(err.Error(), "key resolution") {
		t.Fatalf("expected key resolution error context, got: %v", err)
	}
}

func TestRunEncryptedSingleFileRestorePipelineFailsOnMissingMetadataFields(t *testing.T) {
	root := t.TempDir()
	cl := newEncryptionTestCluster(t, root, "missing-fields-cluster")

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	if err := os.WriteFile(backupPath, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	t.Run("missing IV", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{
			Encrypted:            true,
			EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
			EncryptionIV:         "",
			EncryptionMAC:        "mac",
			EncryptionKey:        "cloud18-sponsor-user-credentials:v3",
			EncryptionKeyCluster: cl.Name,
		}
		err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
		if err == nil {
			t.Fatal("expected missing IV to fail preflight")
		}
		if !strings.Contains(err.Error(), "preflight") {
			t.Fatalf("expected preflight error context, got: %v", err)
		}
	})

	t.Run("missing MAC", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{
			Encrypted:            true,
			EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
			EncryptionIV:         "hex:aabb",
			EncryptionMAC:        "",
			EncryptionKey:        "cloud18-sponsor-user-credentials:v3",
			EncryptionKeyCluster: cl.Name,
		}
		err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
		if err == nil {
			t.Fatal("expected missing MAC to fail preflight")
		}
		if !strings.Contains(err.Error(), "preflight") {
			t.Fatalf("expected preflight error context, got: %v", err)
		}
	})

	t.Run("missing key reference", func(t *testing.T) {
		meta := &backupmgr.BackupMetadata{
			Encrypted:            true,
			EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
			EncryptionIV:         "hex:aabb",
			EncryptionMAC:        "mac",
			EncryptionKey:        "",
			EncryptionKeyCluster: cl.Name,
		}
		err := cl.runEncryptedSingleFileRestorePipeline(backupPath, meta)
		if err == nil {
			t.Fatal("expected missing key reference to fail preflight")
		}
		if !strings.Contains(err.Error(), "preflight") {
			t.Fatalf("expected preflight error context, got: %v", err)
		}
	})
}

func TestResolvePhysicalBackupMetaFromPath(t *testing.T) {
	t.Run("returns nil when meta file is absent", func(t *testing.T) {
		dir := t.TempDir()
		backupfile := filepath.Join(dir, "xtrabackup.xbtream")
		if resolvePhysicalBackupMetaFromPath(backupfile, "xtrabackup") != nil {
			t.Fatal("expected nil for absent meta file")
		}
	})

	t.Run("returns metadata when sidecar file exists", func(t *testing.T) {
		dir := t.TempDir()
		backupfile := filepath.Join(dir, "xtrabackup.xbtream")
		metaPath := filepath.Join(dir, "xtrabackup.meta.json")
		metaJSON := `{"encrypted":true,"encryptionAlgo":"aes-256-cbc","encryptionIV":"hex:aa","encryptionMAC":"bb","encryptionKey":"cloud18-sponsor-user-credentials:v1","encryptionKeyCluster":"cl"}`
		if err := os.WriteFile(metaPath, []byte(metaJSON), 0o644); err != nil {
			t.Fatalf("write meta: %v", err)
		}

		meta := resolvePhysicalBackupMetaFromPath(backupfile, "xtrabackup")
		if meta == nil {
			t.Fatal("expected non-nil metadata")
		}
		if !meta.Encrypted {
			t.Fatal("expected encrypted=true in loaded metadata")
		}
	})
}
