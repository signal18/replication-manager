package cluster

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestFinalizeMysqldumpSingleFileEncryptionSetsMetadata(t *testing.T) {
	root := t.TempDir()
	clusterName := "enc-cluster"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir:             root,
			BackupEncryption: true,
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
				"api-credentials":                  {Value: "dba:dba-pass,admin:admin-pass"},
			},
		},
	}
	server := &ServerMonitor{ClusterGroup: cl}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 3, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ],
  "api-credentials": [
    {"version": 7, "hash_value": "dba:dba-pass,admin:admin-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	original := []byte("plain-mysqldump-payload")
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		t.Fatalf("failed to write backup fixture: %v", err)
	}

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: backupPath}
	if err := server.finalizeMysqldumpSingleFileEncryption(backupPath); err != nil {
		t.Fatalf("unexpected encryption error: %v", err)
	}

	meta := server.LastBackupMeta.Logical
	if meta == nil {
		t.Fatalf("expected logical metadata")
	}
	if !meta.Encrypted {
		t.Fatalf("expected encrypted metadata flag")
	}
	if meta.EncryptionAlgo != backupmgr.BackupEncryptionAlgorithm {
		t.Fatalf("expected encryption algo %s, got %s", backupmgr.BackupEncryptionAlgorithm, meta.EncryptionAlgo)
	}
	if meta.EncryptionIV == "" {
		t.Fatalf("expected encryption IV in metadata")
	}
	if meta.EncryptionMAC == "" {
		t.Fatalf("expected encryption MAC in metadata")
	}
	if meta.EncryptionKey != "cloud18-sponsor-user-credentials:v3" {
		t.Fatalf("expected sponsor key reference v3, got %q", meta.EncryptionKey)
	}
	if meta.EncryptionKeyCluster != clusterName {
		t.Fatalf("expected key cluster %q, got %q", clusterName, meta.EncryptionKeyCluster)
	}

	encrypted, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed reading encrypted artifact: %v", err)
	}
	if bytes.Equal(original, encrypted) {
		t.Fatalf("expected encrypted artifact bytes to differ from plaintext")
	}
}

func TestFinalizeMysqldumpSingleFileEncryptionDisabledUnchanged(t *testing.T) {
	cl := &Cluster{
		Name: "plain-cluster",
		Conf: &config.Config{BackupEncryption: false},
	}
	server := &ServerMonitor{ClusterGroup: cl}

	backupPath := filepath.Join(t.TempDir(), "mysqldump.sql.gz")
	original := []byte("plain-mysqldump-payload")
	if err := os.WriteFile(backupPath, original, 0o644); err != nil {
		t.Fatalf("failed to write backup fixture: %v", err)
	}

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: backupPath}
	if err := server.finalizeMysqldumpSingleFileEncryption(backupPath); err != nil {
		t.Fatalf("expected disabled mode to no-op, got error: %v", err)
	}

	after, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("failed reading artifact: %v", err)
	}
	if !bytes.Equal(original, after) {
		t.Fatalf("expected artifact bytes unchanged when encryption disabled")
	}

	meta := server.LastBackupMeta.Logical
	if meta == nil {
		t.Fatalf("expected logical metadata")
	}
	if meta.Encrypted {
		t.Fatalf("expected metadata.Encrypted=false in disabled mode")
	}
	if meta.EncryptionIV != "" || meta.EncryptionMAC != "" || meta.EncryptionKey != "" || meta.EncryptionKeyCluster != "" {
		t.Fatalf("expected encryption metadata fields to stay empty in disabled mode")
	}
}

func TestFinalizeMysqldumpSingleFileEncryptionUsesDifferentIVPerRun(t *testing.T) {
	root := t.TempDir()
	clusterName := "iv-cluster"
	cl := &Cluster{
		Name: clusterName,
		Conf: &config.Config{
			WorkingDir:             root,
			BackupEncryption: true,
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
			},
		},
	}
	server := &ServerMonitor{ClusterGroup: cl}

	storePath := SecretVersionStorePath(root, clusterName)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 2, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)

	pathA := filepath.Join(t.TempDir(), "a.sql.gz")
	pathB := filepath.Join(t.TempDir(), "b.sql.gz")
	if err := os.WriteFile(pathA, []byte("payload-a"), 0o644); err != nil {
		t.Fatalf("failed writing pathA fixture: %v", err)
	}
	if err := os.WriteFile(pathB, []byte("payload-b"), 0o644); err != nil {
		t.Fatalf("failed writing pathB fixture: %v", err)
	}

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: pathA}
	if err := server.finalizeMysqldumpSingleFileEncryption(pathA); err != nil {
		t.Fatalf("unexpected encryption error for pathA: %v", err)
	}
	ivA := server.LastBackupMeta.Logical.EncryptionIV

	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: pathB}
	if err := server.finalizeMysqldumpSingleFileEncryption(pathB); err != nil {
		t.Fatalf("unexpected encryption error for pathB: %v", err)
	}
	ivB := server.LastBackupMeta.Logical.EncryptionIV

	if ivA == "" || ivB == "" {
		t.Fatalf("expected non-empty IVs")
	}
	if ivA == ivB {
		t.Fatalf("expected unique IV per encryption run, got identical IV %q", ivA)
	}
}
