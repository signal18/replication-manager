package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestPruneSecretStoreForCluster(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	storePath := filepath.Join(root, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}

	seed := []byte(`{
	  "db-servers-credential": [
	    {"version": 1, "hash_value": "h1", "rotated_at": "2026-01-01T00:00:00Z"},
	    {"version": 2, "hash_value": "h2", "rotated_at": "2026-01-02T00:00:00Z"},
	    {"version": 3, "hash_value": "h3", "rotated_at": "2026-01-03T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seed, 0o644); err != nil {
		t.Fatalf("write seed failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: root},
		},
	}

	summary, err := pruneSecretStoreForCluster(repman, clusterName, 2, false)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}
	if !summary.Changed {
		t.Fatalf("expected changed summary")
	}
	if summary.VersionsRemoved != 1 {
		t.Fatalf("expected 1 removed version, got %d", summary.VersionsRemoved)
	}
}

func TestPruneSecretStoreForClusterMissingCluster(t *testing.T) {
	repman := &ReplicationManager{Confs: map[string]config.Config{}}
	_, err := pruneSecretStoreForCluster(repman, "does-not-exist", 2, true)
	if err == nil {
		t.Fatalf("expected missing cluster error")
	}
}

func TestCopySecretStoreForCluster(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	srcPath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seed := []byte(`{"db-servers-credential":[{"version":1,"hash_value":"h1","rotated_at":"2026-01-01T00:00:00Z"}]}`)
	if err := os.WriteFile(srcPath, seed, 0o644); err != nil {
		t.Fatalf("write source failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir, ConfDir: confDir},
		},
	}

	summary, err := copySecretStoreForCluster(repman, clusterName, false, false)
	if err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	if !summary.Copied {
		t.Fatalf("expected copied=true")
	}
	expectedDestination := filepath.Join(confDir, "cluster.d", clusterName+"_secret_store.json")
	if summary.DestinationPath != expectedDestination {
		t.Fatalf("unexpected destination path: got %s want %s", summary.DestinationPath, expectedDestination)
	}
}

func TestCopySecretStoreForClusterUsesGlobalConfDirFallback(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	srcPath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seed := []byte(`{"db-servers-credential":[{"version":1,"hash_value":"h1","rotated_at":"2026-01-01T00:00:00Z"}]}`)
	if err := os.WriteFile(srcPath, seed, 0o644); err != nil {
		t.Fatalf("write source failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir},
		},
		Conf: &config.Config{ConfDir: confDir},
	}

	summary, err := copySecretStoreForCluster(repman, clusterName, false, false)
	if err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	if !summary.Copied {
		t.Fatalf("expected copied=true")
	}
}
