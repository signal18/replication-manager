package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestGetAppTemplatesFromLocal_ResolvesSharedPrefixAndRootLocalFiles(t *testing.T) {
	workingDir := t.TempDir()
	shareDir := t.TempDir()

	sharedDummyPath := filepath.Join(shareDir, "app", "templates", "dummy.toml")
	if err := os.MkdirAll(filepath.Dir(sharedDummyPath), 0o750); err != nil {
		t.Fatalf("mkdir shared dir failed: %v", err)
	}
	if err := os.WriteFile(sharedDummyPath, []byte("app-host='dummy'"), 0o600); err != nil {
		t.Fatalf("write shared dummy failed: %v", err)
	}

	clusterName := "cluster-a"
	clusterLocalRoot := filepath.Join(workingDir, clusterName, ".templates", "apps")
	if err := os.MkdirAll(clusterLocalRoot, 0o750); err != nil {
		t.Fatalf("mkdir cluster local root failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clusterLocalRoot, "root-local.toml"), []byte("app-host='cluster-local'"), 0o600); err != nil {
		t.Fatalf("write root-level cluster local template failed: %v", err)
	}

	repman := &ReplicationManager{
		Conf: &config.Config{
			WorkingDir: workingDir,
			ShareDir:   shareDir,
			WithEmbed:  "OFF",
		},
	}

	templates, err := repman.GetAppTemplatesFromLocal(clusterName)
	if err != nil {
		t.Fatalf("GetAppTemplatesFromLocal returned error: %v", err)
	}

	has := func(name string) bool {
		for _, item := range templates {
			if item == name {
				return true
			}
		}
		return false
	}

	if !has("shared/dummy") {
		t.Fatalf("expected shared template name to include shared/ prefix; got %v", templates)
	}
	if has("dummy") {
		t.Fatalf("unexpected bare shared template name 'dummy' in list: %v", templates)
	}
	if !has("root-local") {
		t.Fatalf("expected root-level local template to be listed; got %v", templates)
	}
}
