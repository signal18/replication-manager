package cluster

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func writeSecretStoreFixture(t *testing.T, storePath string, payload string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		t.Fatalf("failed to create store dir: %v", err)
	}
	if err := os.WriteFile(storePath, []byte(payload), 0o600); err != nil {
		t.Fatalf("failed to write store fixture: %v", err)
	}
}

func TestResolveEncryptedRestoreSecretFromReference(t *testing.T) {
	root := t.TempDir()
	clusterName := "clusterA"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{WorkingDir: root},
	}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 3, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ],
  "api-credentials": [
    {"version": 7, "hash_value": "dba:dba-pass,admin:admin-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	t.Run("resolves sponsor password by versioned reference", func(t *testing.T) {
		secret, err := cl.resolveEncryptedRestoreSecretFromReference("cloud18-sponsor-user-credentials:v3", clusterName)
		if err != nil {
			t.Fatalf("unexpected resolve error: %v", err)
		}
		if secret != "sponsor-pass" {
			t.Fatalf("expected sponsor-pass, got %q", secret)
		}
	})

	t.Run("resolves admin password by versioned reference", func(t *testing.T) {
		secret, err := cl.resolveEncryptedRestoreSecretFromReference("api-credentials/admin:v7", clusterName)
		if err != nil {
			t.Fatalf("unexpected resolve error: %v", err)
		}
		if secret != "admin-pass" {
			t.Fatalf("expected admin-pass, got %q", secret)
		}
	})

	t.Run("fails when referenced version missing", func(t *testing.T) {
		_, err := cl.resolveEncryptedRestoreSecretFromReference("api-credentials/admin:v9", clusterName)
		if err == nil {
			t.Fatalf("expected missing version to fail")
		}
	})
}

func TestResolveEncryptedRestoreSecretFromReferenceMissingStore(t *testing.T) {
	root := t.TempDir()
	cl := &Cluster{
		Name: "clusterB",
		Conf: &config.Config{WorkingDir: root},
	}

	_, err := cl.resolveEncryptedRestoreSecretFromReference("api-credentials/admin:v1", "clusterB")
	if err == nil {
		t.Fatalf("expected missing secret_store history to fail")
	}
	if !strings.Contains(err.Error(), "secret_store") {
		t.Fatalf("expected secret_store error context, got: %v", err)
	}
}

func TestResolveEncryptedRestoreSecretFromReferenceAdminMissing(t *testing.T) {
	root := t.TempDir()
	clusterName := "clusterC"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{WorkingDir: root},
	}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "api-credentials": [
    {"version": 2, "hash_value": "dba:dba-pass,readonly:ro-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	_, err := cl.resolveEncryptedRestoreSecretFromReference("api-credentials/admin:v2", clusterName)
	if err == nil {
		t.Fatalf("expected missing admin in versioned credentials to fail")
	}
	if !strings.Contains(err.Error(), "does not contain admin") {
		t.Fatalf("expected missing admin message, got: %v", err)
	}
}

func TestReseedMysqldumpWithMetadataFailsClosedOnEncryptedPathMismatch(t *testing.T) {
	cl := &Cluster{
		Name: "clusterD",
		Conf: &config.Config{Verbose: false},
	}
	server := &ServerMonitor{ClusterGroup: cl}

	meta := &backupmgr.BackupMetadata{
		Encrypted:            true,
		Dest:                 filepath.Join(t.TempDir(), "one.sql.gz.enc"),
		EncryptionAlgo:       backupmgr.BackupEncryptionAlgorithm,
		EncryptionIV:         "iv",
		EncryptionMAC:        "mac",
		EncryptionKey:        "cloud18-sponsor-user-credentials:v1",
		EncryptionKeyCluster: "clusterD",
	}

	err := server.reseedMysqldumpWithMetadata(context.Background(), filepath.Join(t.TempDir(), "two.sql.gz.enc"), false, meta)
	if err == nil {
		t.Fatalf("expected encrypted path mismatch to fail closed")
	}
	if !strings.Contains(err.Error(), "path mismatch") {
		t.Fatalf("expected path mismatch error, got: %v", err)
	}
}

func TestReseedMysqldumpWithMetadataEncryptedSplitdumpSkipsSingleFileIVPreflight(t *testing.T) {
	backupDir := filepath.Join(t.TempDir(), "splitdump")
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatalf("mkdir splitdump dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(backupDir, "db.table.sql"), []byte("SELECT 1;\n"), 0o600); err != nil {
		t.Fatalf("write splitdump chunk: %v", err)
	}

	cl := &Cluster{
		Name: "clusterE",
		Conf: &config.Config{Verbose: false},
	}
	server := &ServerMonitor{ClusterGroup: cl}

	meta := &backupmgr.BackupMetadata{
		Encrypted:      true,
		SplitDump:      true,
		Dest:           backupDir,
		EncryptionAlgo: backupmgr.BackupEncryptionAlgorithm,
		// No IV/MAC on purpose: splitdump directory encryption stores per-file IV/MAC
		// in the manifest, and must not be validated as single-file metadata.
	}

	err := server.reseedMysqldumpWithMetadata(context.Background(), backupDir, false, meta)
	if err == nil {
		t.Fatalf("expected restore to fail later in splitdump path due missing master fixture")
	}
	if strings.Contains(err.Error(), "encrypted restore metadata missing encryption IV") {
		t.Fatalf("unexpected single-file encrypted preflight error for splitdump path: %v", err)
	}
	if !strings.Contains(err.Error(), "No master found") {
		t.Fatalf("expected splitdump restore path error context, got: %v", err)
	}
}
