package cluster

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

// ---- helpers ----------------------------------------------------------------

func newDirectoryEncryptionCluster(t *testing.T, root, name string) *Cluster {
	t.Helper()
	cl := &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir: root,
			Verbose:    false,
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
				"api-credentials":                  {Value: "dba:dba-pass,admin:admin-pass"},
			},
		},
	}
	storePath := SecretVersionStorePath(root, name)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 4, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)
	return cl
}

// createBackupDir writes files to a temp dir and returns the dir path.
func createBackupDir(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "mydumper")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir subdir: %v", err)
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// ---- finalizeDirectoryEncryption --------------------------------------------

func TestFinalizeDirectoryEncryptionSetsMetadataAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	clusterName := "dir-enc-cluster"
	cl := newDirectoryEncryptionCluster(t, root, clusterName)

	origFiles := map[string][]byte{
		"db1.schema.sql.gz": []byte("schema-payload-1"),
		"db1.table.sql.gz":  []byte("data-payload-1"),
		"metadata":          []byte("myloader-metadata"),
	}
	outputDir := createBackupDir(t, origFiles)

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}

	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("finalizeDirectoryEncryption: %v", err)
	}

	// Metadata must be updated
	meta := server.LastBackupMeta.Logical
	if !meta.Encrypted {
		t.Fatal("expected Encrypted=true after directory encryption")
	}
	if meta.EncryptionAlgo != backupmgr.BackupEncryptionAlgorithm {
		t.Fatalf("expected algo %s, got %q", backupmgr.BackupEncryptionAlgorithm, meta.EncryptionAlgo)
	}
	if meta.EncryptionKey != "cloud18-sponsor-user-credentials:v4" {
		t.Fatalf("expected sponsor key v4, got %q", meta.EncryptionKey)
	}
	if meta.EncryptionKeyCluster != clusterName {
		t.Fatalf("expected cluster %q, got %q", clusterName, meta.EncryptionKeyCluster)
	}

	// Manifest must exist and contain 3 entries (one per file)
	manifest, err := backupmgr.ReadBackupEncryptionManifest(outputDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if manifest.Version != backupmgr.BackupEncryptionManifestVersion {
		t.Fatalf("expected manifest version %d, got %d", backupmgr.BackupEncryptionManifestVersion, manifest.Version)
	}
	if len(manifest.Entries) != len(origFiles) {
		t.Fatalf("expected %d manifest entries, got %d", len(origFiles), len(manifest.Entries))
	}

	// Every original file must now differ from its original content (encrypted)
	for name, original := range origFiles {
		current, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s after encrypt: %v", name, err)
		}
		if bytes.Equal(original, current) {
			t.Errorf("file %q was not encrypted (bytes unchanged)", name)
		}
	}
}

func TestFinalizeDirectoryEncryptionEachFileGetsUniqueIV(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "iv-unique-cluster")

	outputDir := createBackupDir(t, map[string][]byte{
		"a.sql.gz": []byte("payload-a"),
		"b.sql.gz": []byte("payload-b"),
	})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}

	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	manifest, err := backupmgr.ReadBackupEncryptionManifest(outputDir)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if len(manifest.Entries) < 2 {
		t.Fatalf("expected at least 2 entries, got %d", len(manifest.Entries))
	}

	ivs := make(map[string]bool)
	for _, e := range manifest.Entries {
		if ivs[e.IV] {
			t.Fatalf("duplicate IV %q found across entries", e.IV)
		}
		ivs[e.IV] = true
	}
}

// ---- runEncryptedDirectoryRestorePipeline -----------------------------------

func TestRunEncryptedDirectoryRestorePipelineSucceeds(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "restore-dir-cluster")

	origFiles := map[string][]byte{
		"db1.sql.gz": []byte("original-schema"),
		"db2.sql.gz": []byte("original-data"),
	}
	outputDir := createBackupDir(t, origFiles)

	// Encrypt
	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Restore pipeline
	if err := cl.runEncryptedDirectoryRestorePipeline(outputDir); err != nil {
		t.Fatalf("restore pipeline: %v", err)
	}

	// Files must be back to original content
	for name, original := range origFiles {
		current, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s after restore: %v", name, err)
		}
		if !bytes.Equal(original, current) {
			t.Errorf("file %q not restored to original content", name)
		}
	}
}

func TestRunEncryptedDirectoryRestorePipelinePassesThroughWithoutManifest(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "passthrough-cluster")

	// Directory without a manifest (non-encrypted backup)
	dir := filepath.Join(t.TempDir(), "plaindir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := cl.runEncryptedDirectoryRestorePipeline(dir); err != nil {
		t.Fatalf("expected pass-through for absent manifest, got: %v", err)
	}
}

func TestRunEncryptedDirectoryRestorePipelineFailsOnTamperedMAC(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "tamper-cluster")

	outputDir := createBackupDir(t, map[string][]byte{
		"file.sql.gz": []byte("payload"),
	})

	// Encrypt
	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Tamper with the manifest's MAC
	manifestPath := backupmgr.BackupEncryptionManifestPath(outputDir)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest backupmgr.BackupEncryptionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	manifest.Entries[0].MAC = "tampered-mac-value"
	tampered, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}

	err = cl.runEncryptedDirectoryRestorePipeline(outputDir)
	if err == nil {
		t.Fatal("expected tampered MAC to fail restore")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Fatalf("expected manifest validation error, got: %v", err)
	}
}

func TestRunEncryptedDirectoryRestorePipelineFailsOnMissingEntry(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "missing-entry-cluster")

	outputDir := createBackupDir(t, map[string][]byte{
		"file.sql.gz": []byte("payload"),
	})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Delete the backup file to simulate a missing artifact
	if err := os.Remove(filepath.Join(outputDir, "file.sql.gz")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	err := cl.runEncryptedDirectoryRestorePipeline(outputDir)
	if err == nil {
		t.Fatal("expected missing artifact to fail restore")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Fatalf("expected manifest validation error, got: %v", err)
	}
}

func TestRunEncryptedDirectoryRestorePipelineFailsOnWrongKeyVersion(t *testing.T) {
	root := t.TempDir()
	cl := newDirectoryEncryptionCluster(t, root, "wrong-ver-cluster")

	outputDir := createBackupDir(t, map[string][]byte{"f.sql": []byte("data")})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "mydumper"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Tamper: point manifest to a version that doesn't exist
	manifestPath := backupmgr.BackupEncryptionManifestPath(outputDir)
	data, _ := os.ReadFile(manifestPath)
	var manifest backupmgr.BackupEncryptionManifest
	json.Unmarshal(data, &manifest)
	manifest.KeyRef = "cloud18-sponsor-user-credentials:v99"
	tampered, _ := json.Marshal(manifest)
	os.WriteFile(manifestPath, tampered, 0o600)

	err := cl.runEncryptedDirectoryRestorePipeline(outputDir)
	if err == nil {
		t.Fatal("expected wrong key version to fail restore")
	}
	if !strings.Contains(err.Error(), "key resolution") {
		t.Fatalf("expected key resolution error, got: %v", err)
	}
}
