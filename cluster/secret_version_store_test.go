package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestReconcileSecretVersionStoreBootstrapReconcileAndDedupe(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "cluster-a")
	if err := os.MkdirAll(workingDir, os.ModePerm); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}

	cl := &Cluster{
		Name:       "cluster-a",
		WorkingDir: workingDir,
		Conf: &config.Config{
			WorkingDir: root,
			Secrets: map[string]config.Secret{
				"db-servers-credential": {Value: "dbuser:hash_pass_1"},
			},
		},
	}

	cl.ReconcileSecretVersionStore()
	store := readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "db-servers-credential", 1)

	cl.ReconcileSecretVersionStore()
	store = readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "db-servers-credential", 1)

	cl.Conf.Secrets["db-servers-credential"] = config.Secret{Value: "dbuser:hash_pass_2"}
	cl.ReconcileSecretVersionStore()
	store = readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "db-servers-credential", 2)

	cl.ReconcileSecretVersionStore()
	store = readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "db-servers-credential", 2)

	cl.Conf.Secrets["proxysql-password"] = config.Secret{Value: "hash_proxy_pass"}
	cl.ReconcileSecretVersionStore()
	store = readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "proxysql-password", 1)
}

func TestReconcileSecretVersionStoreDisabledWhenVaultConfigured(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "cluster-b")
	if err := os.MkdirAll(workingDir, os.ModePerm); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}

	cl := &Cluster{
		Name:       "cluster-b",
		WorkingDir: workingDir,
		Conf: &config.Config{
			WorkingDir:                 root,
			VaultServerAddr:            "https://vault.example",
			MonitoringSecretVersioning: false,
			Secrets: map[string]config.Secret{
				"db-servers-credential": {Value: "dbuser:hash_pass_1"},
			},
		},
	}

	cl.ReconcileSecretVersionStore()
	if _, err := os.Stat(filepath.Join(workingDir, secretVersionStoreFilename)); !os.IsNotExist(err) {
		t.Fatalf("expected no secret store file when feature is disabled, got err=%v", err)
	}
}

func TestWriteSecretVersionStoreAtomic(t *testing.T) {
	root := t.TempDir()
	storePath := filepath.Join(root, secretVersionStoreFilename)

	store := secretVersionStore{
		"k1": {
			{Version: 1, HashValue: "h1", RotatedAt: "2026-01-01T00:00:00Z"},
		},
	}

	if err := writeSecretVersionStoreAtomic(storePath, store); err != nil {
		t.Fatalf("first atomic write failed: %v", err)
	}

	store["k1"] = append(store["k1"], secretVersion{Version: 2, HashValue: "h2", RotatedAt: "2026-01-02T00:00:00Z"})
	if err := writeSecretVersionStoreAtomic(storePath, store); err != nil {
		t.Fatalf("second atomic write failed: %v", err)
	}

	loaded := readSecretStoreForTest(t, storePath)
	assertSecretVersionCount(t, loaded, "k1", 2)

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("readdir failed: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "secret_store-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("unexpected temp file left behind: %s", entry.Name())
		}
	}
}

func TestReconcileSecretVersionStoreSkipsNonEncryptedValues(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "cluster-c")
	if err := os.MkdirAll(workingDir, os.ModePerm); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}

	cl := &Cluster{
		Name:       "cluster-c",
		WorkingDir: workingDir,
		Conf: &config.Config{
			WorkingDir: root,
			Secrets: map[string]config.Secret{
				"custom-secret": {Value: "plain-text-value"},
			},
		},
	}

	cl.ReconcileSecretVersionStore()
	if _, err := os.Stat(filepath.Join(workingDir, secretVersionStoreFilename)); !os.IsNotExist(err) {
		t.Fatalf("expected no store file when values are not encrypted hash format, got err=%v", err)
	}
}

func TestReconcileSecretVersionStoreDoesNotAppendForEquivalentEncryptedValues(t *testing.T) {
	root := t.TempDir()
	workingDir := filepath.Join(root, "cluster-d")
	if err := os.MkdirAll(workingDir, os.ModePerm); err != nil {
		t.Fatalf("mkdir working dir: %v", err)
	}

	cl := &Cluster{
		Name:       "cluster-d",
		WorkingDir: workingDir,
		Conf: &config.Config{
			WorkingDir: root,
			SecretKey:  []byte("01234567890123456789012345678901"),
			Secrets: map[string]config.Secret{
				"proxysql-password": {Value: "same-secret"},
			},
		},
	}

	cl.ReconcileSecretVersionStore()
	store := readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "proxysql-password", 1)

	// GetEncryptedString may generate different encrypted hash tokens for same plaintext.
	// Reconciliation must compare semantic decrypted value and avoid false version bumps.
	cl.ReconcileSecretVersionStore()
	store = readSecretStoreForTest(t, filepath.Join(workingDir, secretVersionStoreFilename))
	assertSecretVersionCount(t, store, "proxysql-password", 1)
}

func readSecretStoreForTest(t *testing.T, path string) secretVersionStore {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read store failed: %v", err)
	}
	store := make(secretVersionStore)
	if err := json.Unmarshal(data, &store); err != nil {
		t.Fatalf("unmarshal store failed: %v", err)
	}
	return store
}

func assertSecretVersionCount(t *testing.T, store secretVersionStore, key string, want int) {
	t.Helper()
	versions := store[key]
	if len(versions) != want {
		t.Fatalf("unexpected versions count for %s: got=%d want=%d", key, len(versions), want)
	}
	if want > 0 && versions[len(versions)-1].Version != want {
		t.Fatalf("unexpected latest version number for %s: got=%d want=%d", key, versions[len(versions)-1].Version, want)
	}
}
