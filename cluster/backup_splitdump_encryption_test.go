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

// newSplitdumpEncryptionCluster creates a minimal cluster fixture for splitdump encryption tests.
func newSplitdumpEncryptionCluster(t *testing.T, root, name string) *Cluster {
	t.Helper()
	cl := &Cluster{
		Name: name,
		Conf: &config.Config{
			WorkingDir: root,
			Verbose:    false,
			Secrets: map[string]config.Secret{
				"cloud18-sponsor-user-credentials": {Value: "sponsor:sponsor-pass"},
			},
		},
	}
	storePath := SecretVersionStorePath(root, name)
	writeSecretStoreFixture(t, storePath, `{
  "cloud18-sponsor-user-credentials": [
    {"version": 2, "hash_value": "sponsor:sponsor-pass", "rotated_at": "2026-01-01T00:00:00Z"}
  ]
}`)
	return cl
}

// createSplitdumpDir creates a temp directory simulating splitdump chunk output.
func createSplitdumpDir(t *testing.T, chunks map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "splitdump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir splitdump dir: %v", err)
	}
	for name, content := range chunks {
		if err := os.WriteFile(filepath.Join(dir, name), content, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// ---- finalizeDirectoryEncryption for splitdump --------------------------------

func TestSplitdumpEncryptionSetsMetadataAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-meta-cluster")

	chunks := map[string][]byte{
		"db.table.00000.sql.gz": []byte("chunk-0-payload"),
		"db.table.00001.sql.gz": []byte("chunk-1-payload"),
		"metadata":              []byte("splitdump-metadata"),
	}
	outputDir := createSplitdumpDir(t, chunks)

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}

	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("finalizeDirectoryEncryption: %v", err)
	}

	meta := server.LastBackupMeta.Logical
	if !meta.Encrypted {
		t.Fatal("expected Encrypted=true after splitdump encryption")
	}
	if meta.EncryptionAlgo != backupmgr.BackupEncryptionAlgorithm {
		t.Fatalf("expected algo %s, got %q", backupmgr.BackupEncryptionAlgorithm, meta.EncryptionAlgo)
	}
	if meta.EncryptionKey != "cloud18-sponsor-user-credentials:v2" {
		t.Fatalf("expected sponsor key v2, got %q", meta.EncryptionKey)
	}

	manifest, err := backupmgr.ReadBackupEncryptionManifest(outputDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if len(manifest.Entries) != len(chunks) {
		t.Fatalf("expected %d manifest entries, got %d", len(chunks), len(manifest.Entries))
	}

	// All chunk files must be encrypted (differ from original)
	for name, original := range chunks {
		current, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s after encrypt: %v", name, err)
		}
		if bytes.Equal(original, current) {
			t.Errorf("chunk %q was not encrypted", name)
		}
	}
}

func TestSplitdumpEncryptionEachChunkGetsUniqueIV(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-iv-cluster")

	outputDir := createSplitdumpDir(t, map[string][]byte{
		"db.t.00000.sql.gz": []byte("chunk-payload-0"),
		"db.t.00001.sql.gz": []byte("chunk-payload-1"),
		"db.t.00002.sql.gz": []byte("chunk-payload-2"),
	})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}

	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	manifest, err := backupmgr.ReadBackupEncryptionManifest(outputDir)
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}

	ivs := make(map[string]bool)
	for _, e := range manifest.Entries {
		if ivs[e.IV] {
			t.Fatalf("duplicate IV %q found across chunk entries", e.IV)
		}
		ivs[e.IV] = true
	}
}

// ---- restore pipeline for splitdump -----------------------------------------

func TestSplitdumpRestorePipelineDecryptsAllChunks(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-restore-cluster")

	origChunks := map[string][]byte{
		"db.t.00000.sql.gz": []byte("original-chunk-0"),
		"db.t.00001.sql.gz": []byte("original-chunk-1"),
	}
	outputDir := createSplitdumpDir(t, origChunks)

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	if err := cl.runEncryptedDirectoryRestorePipeline(outputDir); err != nil {
		t.Fatalf("restore pipeline: %v", err)
	}

	for name, original := range origChunks {
		current, err := os.ReadFile(filepath.Join(outputDir, name))
		if err != nil {
			t.Fatalf("read %s after restore: %v", name, err)
		}
		if !bytes.Equal(original, current) {
			t.Errorf("chunk %q not restored to original content", name)
		}
	}
}

func TestSplitdumpRestorePipelinePassesThroughWithoutManifest(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-passthrough-cluster")

	dir := filepath.Join(t.TempDir(), "splitdump")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := cl.runEncryptedDirectoryRestorePipeline(dir); err != nil {
		t.Fatalf("expected pass-through for absent manifest, got: %v", err)
	}
}

func TestSplitdumpRestorePipelineFailsOnCorruptChunk(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-corrupt-cluster")

	outputDir := createSplitdumpDir(t, map[string][]byte{
		"db.t.00000.sql.gz": []byte("chunk-payload"),
	})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Tamper the manifest MAC to simulate a corrupted chunk
	manifestPath := backupmgr.BackupEncryptionManifestPath(outputDir)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest backupmgr.BackupEncryptionManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	manifest.Entries[0].MAC = "corrupted-mac"
	tampered, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, tampered, 0o600); err != nil {
		t.Fatalf("write tampered manifest: %v", err)
	}

	err = cl.runEncryptedDirectoryRestorePipeline(outputDir)
	if err == nil {
		t.Fatal("expected corrupted chunk MAC to fail restore")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Fatalf("expected manifest validation error, got: %v", err)
	}
}

func TestSplitdumpRestorePipelineFailsOnMissingChunk(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-missing-chunk-cluster")

	outputDir := createSplitdumpDir(t, map[string][]byte{
		"db.t.00000.sql.gz": []byte("chunk-payload"),
	})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Remove the chunk to simulate a missing artifact
	if err := os.Remove(filepath.Join(outputDir, "db.t.00000.sql.gz")); err != nil {
		t.Fatalf("remove: %v", err)
	}

	err := cl.runEncryptedDirectoryRestorePipeline(outputDir)
	if err == nil {
		t.Fatal("expected missing chunk to fail restore")
	}
	if !strings.Contains(err.Error(), "manifest validation failed") {
		t.Fatalf("expected manifest validation error, got: %v", err)
	}
}

func TestSplitdumpRestorePipelineFailsOnWrongKeyVersion(t *testing.T) {
	root := t.TempDir()
	cl := newSplitdumpEncryptionCluster(t, root, "splitdump-wrongkey-cluster")

	outputDir := createSplitdumpDir(t, map[string][]byte{"db.t.00000.sql.gz": []byte("data")})

	server := &ServerMonitor{ClusterGroup: cl}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{Dest: outputDir}
	if err := server.finalizeDirectoryEncryption(outputDir, "splitdump"); err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	// Tamper: point manifest to a key version that doesn't exist
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
