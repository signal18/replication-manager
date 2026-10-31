package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	s18crypto "github.com/signal18/replication-manager/utils/crypto"
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

	summary, err := restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, "2", nil, "", false, false)
	if err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if summary.Mode != "version" {
		t.Fatalf("expected mode=version, got %s", summary.Mode)
	}

	// #2227 rule 1: a versioned secret is restored as a new version, the config keeps its
	// starting value (no config file written for it)
	configPath := filepath.Join(confDir, "cluster.d", clusterName+".toml")
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("the config was written: %v", err)
	}
	if v, n := lastStoreVersion(t, storePath, "db-servers-credential"); v != "hash_new" || n != 3 {
		t.Fatalf("restored version %q (%d versions)", v, n)
	}
}

// lastStoreVersion is the last value of key in the store file and how many versions it has.
func lastStoreVersion(t *testing.T, storePath, key string) (string, int) {
	t.Helper()
	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	var store map[string][]struct {
		HashValue string `json:"hash_value"`
	}
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatal(err)
	}
	vs := store[key]
	if len(vs) == 0 {
		return "", 0
	}
	return vs[len(vs)-1].HashValue, len(vs)
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
	summary, err := restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, "", &at, "", true, false)
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

	summary, err := restoreSecretStoreForCluster(repman, clusterName, nil, true, "2", nil, "", false, false)
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
	// #2227 rule 1: the versioned db-servers-credential is a new version, the other secret
	// goes to the config as before
	if !containsAll(content, []string{`mail-smtp-password = "hash_mail_v2"`}) || strings.Contains(content, "db-servers-credential") {
		t.Fatalf("restored config: %s", content)
	}
	if v, n := lastStoreVersion(t, storePath, "db-servers-credential"); v != "hash_db_v2" || n != 3 {
		t.Fatalf("restored version %q (%d versions)", v, n)
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

	_, err := restoreSecretStoreForCluster(repman, clusterName, []string{"mail-smtp-password"}, false, "1", nil, "", false, true)
	if err == nil {
		t.Fatalf("expected runtime-apply rejection for unsupported key")
	}
}

func TestRestoreSecretStoreForClusterLatestVersion(t *testing.T) {
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
	    {"version": 3, "hash_value": "hash_db_v3", "rotated_at": "2026-01-03T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{Confs: map[string]config.Config{clusterName: {WorkingDir: workingDir, ConfDir: confDir}}}

	summary, err := restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, "latest", nil, "", false, false)
	if err != nil {
		t.Fatalf("restore latest failed: %v", err)
	}
	if summary.Mode != "version" || summary.RequestedVersion != "latest" {
		t.Fatalf("unexpected summary mode/version: %+v", summary)
	}
	if len(summary.Entries) != 1 || summary.Entries[0].Version != 3 {
		t.Fatalf("expected resolved latest version 3, got %+v", summary.Entries)
	}
}

func TestRestoreSecretStoreForClusterWithLegacyKeyPath(t *testing.T) {
	root := t.TempDir()
	clusterName := "cluster-a"
	workingDir := filepath.Join(root, "data")
	confDir := filepath.Join(root, "etc", "replication-manager")

	legacyKeyPath := filepath.Join(root, "legacy.key")
	currentKeyPath := filepath.Join(root, "current.key")

	legacyKey, err := s18crypto.Keygen()
	if err != nil {
		t.Fatalf("legacy keygen failed: %v", err)
	}
	currentKey, err := s18crypto.Keygen()
	if err != nil {
		t.Fatalf("current keygen failed: %v", err)
	}
	if err := s18crypto.WriteKey(legacyKey, legacyKeyPath, false); err != nil {
		t.Fatalf("write legacy key failed: %v", err)
	}
	if err := s18crypto.WriteKey(currentKey, currentKeyPath, false); err != nil {
		t.Fatalf("write current key failed: %v", err)
	}

	legacyCipher := s18crypto.Password{Key: legacyKey, PlainText: "secret-pass"}
	legacyCipher.Encrypt()
	legacyStoredValue := "dbuser:" + "hash_" + legacyCipher.CipherText

	storePath := filepath.Join(workingDir, clusterName, "secret_store.json")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		t.Fatalf("mkdir source dir failed: %v", err)
	}
	seedStore := []byte(`{
	  "db-servers-credential": [
	    {"version": 1, "hash_value": "` + legacyStoredValue + `", "rotated_at": "2026-01-01T00:00:00Z"}
	  ]
	}`)
	if err := os.WriteFile(storePath, seedStore, 0o644); err != nil {
		t.Fatalf("write store failed: %v", err)
	}

	repman := &ReplicationManager{Confs: map[string]config.Config{clusterName: {WorkingDir: workingDir, ConfDir: confDir, MonitoringKeyPath: currentKeyPath}}}

	_, err = restoreSecretStoreForCluster(repman, clusterName, []string{"db-servers-credential"}, false, "1", nil, legacyKeyPath, false, false)
	if err != nil {
		t.Fatalf("restore with legacy key-path failed: %v", err)
	}

	// #2227 rule 1: restored as a new version of the store, re-encrypted with the current key
	restoredValue, _ := lastStoreVersion(t, storePath, "db-servers-credential")
	if restoredValue == legacyStoredValue {
		t.Fatalf("expected transformed value with current key, got unchanged value")
	}

	verifyConf := &config.Config{MonitoringKeyPath: currentKeyPath}
	if _, err := verifyConf.LoadEncrytionKey(); err != nil {
		t.Fatalf("load current key failed: %v", err)
	}
	plaintext := verifyConf.DecryptSecretValue("db-servers-credential", restoredValue)
	if plaintext != "dbuser:secret-pass" {
		t.Fatalf("unexpected decrypted restored value: %s", plaintext)
	}
}
