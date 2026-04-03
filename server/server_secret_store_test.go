package server

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

func TestParseRestoreKeysDedupAndTrim(t *testing.T) {
	keys, err := parseRestoreKeys(" db-servers-credential, replication-credential,db-servers-credential ")
	if err != nil {
		t.Fatalf("parse keys failed: %v", err)
	}
	want := []string{"db-servers-credential", "replication-credential"}
	if !reflect.DeepEqual(keys, want) {
		t.Fatalf("unexpected keys: got=%v want=%v", keys, want)
	}
}

func TestRestoreSecretStoreForClusterConfigByVersion(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	storePath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seedStore := []byte(`{
	  "db-servers-credential": [
	    {"version": 1, "hash_value": "hash_old", "rotated_at": "2026-01-01T00:00:00Z"},
	    {"version": 2, "hash_value": "hash_new", "rotated_at": "2026-01-02T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir, ConfDir: confDir},
		},
	}

	summary, err := restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, 2, nil, false, false)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if summary.Mode != "version" {
		t.Fatalf("expected mode=version, got %s", summary.Mode)
	}

	configPath := filepath.Join(confDir, "cluster.d", clusterName+".toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read restored config failed: %v", err)
	}
	content := string(data)
	if content == "" || !containsAll(content, []string{"[cluster-a]", `db-servers-credential = "hash_new"`}) {
		t.Fatalf("restored config does not contain expected value: %s", content)
	}
}

func TestRestoreSecretStoreForClusterConfigByDateDryRun(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	storePath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seedStore := []byte(`{
	  "db-servers-credential": [
	    {"version": 1, "hash_value": "hash_old", "rotated_at": "2026-01-01T00:00:00Z"},
	    {"version": 2, "hash_value": "hash_new", "rotated_at": "2026-01-02T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir, ConfDir: confDir},
		},
	}

	at := time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC)
	summary, err := restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, 0, &at, true, false)
	if err != nil {
		t.Fatalf("dry-run restore failed: %v", err)
	}
	if !summary.DryRun {
		t.Fatalf("expected dry-run summary")
	}
	configPath := filepath.Join(confDir, "cluster.d", clusterName+".toml")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("expected no config write during dry-run")
	}
}

func containsAll(haystack string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(haystack, needle) {
			return false
		}
	}
	return true
}

func TestRestoreSecretStoreForClusterAllSecretsConfigOnly(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	storePath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seedStore := []byte(`{
	  "db-servers-credential": [
	    {"version": 1, "hash_value": "hash_db_v1", "rotated_at": "2026-01-01T00:00:00Z"},
	    {"version": 2, "hash_value": "hash_db_v2", "rotated_at": "2026-01-02T00:00:00Z"}
	  ],
	  "mail-smtp-password": [
	    {"version": 1, "hash_value": "hash_mail_v1", "rotated_at": "2026-01-01T00:00:00Z"},
	    {"version": 2, "hash_value": "hash_mail_v2", "rotated_at": "2026-01-02T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir, ConfDir: confDir},
		},
	}

	summary, err := restoreSecretStoreForCluster(repman, clusterName, nil, true, 2, nil, false, false)
	if err != nil {
		t.Fatalf("restore all-secrets failed: %v", err)
	}
	if summary.Selection != "all secrets from store" {
		t.Fatalf("unexpected selection mode: %s", summary.Selection)
	}

	configPath := filepath.Join(confDir, "cluster.d", clusterName+".toml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read restored config failed: %v", err)
	}
	content := string(data)
	if !containsAll(content, []string{`db-servers-credential = "hash_db_v2"`, `mail-smtp-password = "hash_mail_v2"`}) {
		t.Fatalf("restored config does not contain expected all-secrets values: %s", content)
	}
}

func TestRestoreSecretStoreForClusterApplyRuntimeRejectsUnsupportedKey(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	storePath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seedStore := []byte(`{
	  "mail-smtp-password": [
	    {"version": 1, "hash_value": "hash_mail_v1", "rotated_at": "2026-01-01T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{
		Confs: map[string]config.Config{
			clusterName: {WorkingDir: workingDir, ConfDir: confDir},
		},
	}

	_, err := restoreSecretStoreForCluster(repman, clusterName, []string{"mail-smtp-password"}, false, 1, nil, false, true)
	if err == nil {
		t.Fatalf("expected runtime-apply rejection for unsupported key")
	}
}
