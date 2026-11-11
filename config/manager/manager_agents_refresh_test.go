package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v5"
	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

// After a metadata refresh (a fresh clone with no checkout, so an empty index) the next
// push re-stages every file. agents.json, throttled to once an hour, must be re-staged
// too: otherwise that commit deletes it from the remote, and it stays deleted until its
// content changes (an unchanged file is never re-staged).
func TestPushAfterRefreshKeepsAgentsJSON(t *testing.T) {
	remoteBare := filepath.Join(t.TempDir(), "remote.git")
	if _, err := git.PlainInit(remoteBare, true); err != nil {
		t.Fatalf("init bare remote: %v", err)
	}
	workDir := t.TempDir()
	cluster := "cluster1"
	if err := os.MkdirAll(filepath.Join(workDir, cluster), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		cluster + ".toml": "[cluster1]\ntitle = \"cluster1\"\n",
		"agents.json":     "[{\"hostName\": \"node1\"}]\n",
	} {
		if err := os.WriteFile(filepath.Join(workDir, cluster, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      remoteBare,
		GitUsername: "local",
		Secrets:     config.NewSecrets(map[string]config.Secret{"git-acces-token": {Value: ""}}),
	}
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logrus.New()))
	t.Cleanup(cm.Stop)

	if err := cm.PushConfigToGit(conf, []string{cluster}); err != nil {
		t.Fatalf("first push: %v", err)
	}
	if !remoteHasFile(t, remoteBare, cluster+"/agents.json") {
		t.Fatalf("first push must publish agents.json")
	}

	// The periodic refresh (after 10 commits), then a push within the agents.json hour.
	if err := cm.RefreshGitMetadata(conf); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if err := cm.PushConfigToGit(conf, []string{cluster}); err != nil {
		t.Fatalf("push after refresh: %v", err)
	}
	if !remoteHasFile(t, remoteBare, cluster+"/agents.json") {
		t.Fatalf("the push after a metadata refresh deleted agents.json from the remote")
	}
	if !remoteHasFile(t, remoteBare, cluster+"/"+cluster+".toml") {
		t.Fatalf("the push after a metadata refresh deleted the cluster toml from the remote")
	}
}

// remoteHasFile reports whether the remote's HEAD tree holds path.
func remoteHasFile(t *testing.T, remoteBare, path string) bool {
	t.Helper()
	r, err := git.PlainOpen(remoteBare)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	head, err := r.Head()
	if err != nil {
		t.Fatalf("remote head: %v", err)
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		t.Fatalf("remote commit: %v", err)
	}
	_, err = commit.File(path)
	return err == nil
}
