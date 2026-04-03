package manager

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/signal18/replication-manager/config"
	"github.com/sirupsen/logrus"
)

func testWorkDir(t *testing.T) string {
	t.Helper()

	if wd := os.Getenv("REPMAN_GIT_TEST_WORKDIR"); wd != "" {
		if err := os.RemoveAll(wd); err != nil {
			t.Fatalf("failed resetting static test workdir %s: %v", wd, err)
		}
		if err := os.MkdirAll(wd, 0o755); err != nil {
			t.Fatalf("failed creating static test workdir %s: %v", wd, err)
		}
		t.Logf("using static test workdir: %s", wd)
		return wd
	}

	return t.TempDir()
}

func snapshotLocalFiles(t *testing.T, root string) map[string][]byte {
	t.Helper()

	files := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if d.IsDir() {
			if rel == ".git" || rel == ".tmp" {
				return filepath.SkipDir
			}
			return nil
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = raw
		return nil
	})
	if err != nil {
		t.Fatalf("failed to snapshot local files: %v", err)
	}

	return files
}

func testGitCredentialsFromEnv(t *testing.T) (string, string, string) {
	t.Helper()

	gitURL := os.Getenv("REPMAN_GIT_TEST_URL")
	gitToken := os.Getenv("REPMAN_GIT_TEST_TOKEN")
	gitUser := os.Getenv("REPMAN_GIT_TEST_USERNAME")

	if gitURL == "" || gitToken == "" || gitUser == "" {
		t.Skip("set REPMAN_GIT_TEST_URL, REPMAN_GIT_TEST_TOKEN, and REPMAN_GIT_TEST_USERNAME to run this integration test")
	}

	return gitURL, gitToken, gitUser
}

func TestPushConfigToGit_WithEnvCredentials(t *testing.T) {
	gitURL, gitToken, gitUser := testGitCredentialsFromEnv(t)

	workDir := testWorkDir(t)
	clusterName := "cluster1"

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      gitURL,
		GitUsername: gitUser,
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: gitToken},
		},
	}

	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	// First call: clone/open repository into empty temp working dir.
	if err := cm.PushConfigToGit(conf, nil); err != nil {
		t.Fatalf("initial PushConfigToGit (clone/open) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workDir, ".git")); err != nil {
		t.Fatalf("expected .git directory after initial push call: %v", err)
	}

	// Second phase: create files so we produce an actual commit and push.
	if err := os.MkdirAll(filepath.Join(workDir, clusterName), 0o755); err != nil {
		t.Fatalf("failed creating cluster directory: %v", err)
	}

	defaultToml := []byte("# repman integration test\nupdated-at = \"" + time.Now().UTC().Format(time.RFC3339Nano) + "\"\n")
	if err := os.WriteFile(filepath.Join(workDir, "default.toml"), defaultToml, 0o644); err != nil {
		t.Fatalf("failed writing default.toml: %v", err)
	}

	clusterState := []byte("{\"servers\":\"\",\"crashes\":[],\"sla\":{},\"slaHistory\":[],\"provisioned\":false,\"repmgrVersion\":\"test\"}\n")
	if err := os.WriteFile(filepath.Join(workDir, clusterName, "clusterstate.json"), clusterState, 0o644); err != nil {
		t.Fatalf("failed writing clusterstate.json: %v", err)
	}

	if err := cm.PushConfigToGit(conf, []string{clusterName}); err != nil {
		t.Fatalf("second PushConfigToGit (commit+push) failed: %v", err)
	}
}

func TestPushConfigToGit_ShallowClonePreservesLocalFilesThenPushMaster(t *testing.T) {
	gitURL, gitToken, gitUser := testGitCredentialsFromEnv(t)

	workDir := testWorkDir(t)
	clusterName := "cluster1"

	conf := &config.Config{
		WorkingDir:  workDir,
		GitUrl:      gitURL,
		GitUsername: gitUser,
		Secrets: map[string]config.Secret{
			"git-acces-token": {Value: gitToken},
		},
	}

	logger := logrus.New()
	cm := NewConfigManager(config.NewLogrusWrapper(conf, logger))
	defer cm.Stop()

	preservedFile := filepath.Join(workDir, "local-preserved.txt")
	preservedContent := "preserve-me"
	if err := os.WriteFile(preservedFile, []byte(preservedContent), 0o644); err != nil {
		t.Fatalf("failed writing local preserved file: %v", err)
	}

	preservedNestedDir := filepath.Join(workDir, "local-dir")
	if err := os.MkdirAll(preservedNestedDir, 0o755); err != nil {
		t.Fatalf("failed creating nested preserved dir: %v", err)
	}
	preservedNestedFile := filepath.Join(preservedNestedDir, "keep.txt")
	preservedNestedContent := "keep-nested"
	if err := os.WriteFile(preservedNestedFile, []byte(preservedNestedContent), 0o644); err != nil {
		t.Fatalf("failed writing nested preserved file: %v", err)
	}

	t.Logf("snapshotting pre-clone local files")
	beforeClone := snapshotLocalFiles(t, workDir)

	// 1) Shallow clone into non-empty directory.
	t.Logf("step 1: shallow clone (non-empty workdir)")
	if err := cm.PushConfigToGit(conf, nil); err != nil {
		t.Fatalf("initial PushConfigToGit (shallow clone) failed: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workDir, ".git", "shallow")); err != nil {
		t.Fatalf("expected shallow clone metadata in .git/shallow: %v", err)
	}

	// 2) Pre-existing local files are preserved.
	t.Logf("step 2: verify pre-existing files remain intact")
	afterClone := snapshotLocalFiles(t, workDir)
	if len(beforeClone) != len(afterClone) {
		t.Fatalf("file count changed after clone, before=%d after=%d", len(beforeClone), len(afterClone))
	}
	for name, beforeRaw := range beforeClone {
		afterRaw, ok := afterClone[name]
		if !ok {
			t.Fatalf("file missing after clone: %s", name)
		}
		if !bytes.Equal(beforeRaw, afterRaw) {
			t.Fatalf("file content changed after clone: %s", name)
		}
	}

	raw, err := os.ReadFile(preservedFile)
	if err != nil {
		t.Fatalf("failed reading preserved file after clone: %v", err)
	}
	if string(raw) != preservedContent {
		t.Fatalf("preserved file content changed after clone, got=%q want=%q", string(raw), preservedContent)
	}
	rawNested, err := os.ReadFile(preservedNestedFile)
	if err != nil {
		t.Fatalf("failed reading nested preserved file after clone: %v", err)
	}
	if string(rawNested) != preservedNestedContent {
		t.Fatalf("nested preserved file content changed after clone, got=%q want=%q", string(rawNested), preservedNestedContent)
	}

	// 3) Commit local files.
	t.Logf("step 3: create tracked files and commit+push")
	if err := os.MkdirAll(filepath.Join(workDir, clusterName), 0o755); err != nil {
		t.Fatalf("failed creating cluster directory: %v", err)
	}

	marker := "integration-marker=" + time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(workDir, "default.toml"), []byte("# repman integration test\n"+marker+"\n"), 0o644); err != nil {
		t.Fatalf("failed writing default.toml: %v", err)
	}

	clusterState := "{\"integrationMarker\":\"" + marker + "\"}\n"
	if err := os.WriteFile(filepath.Join(workDir, clusterName, "clusterstate.json"), []byte(clusterState), 0o644); err != nil {
		t.Fatalf("failed writing clusterstate.json: %v", err)
	}

	if err := cm.PushConfigToGit(conf, []string{clusterName}); err != nil {
		t.Fatalf("second PushConfigToGit (commit+push) failed: %v", err)
	}

	// 4) Push succeeded: verify remote master contains marker.
	t.Logf("step 4: verify marker exists on remote master")
	verifyDir := t.TempDir()
	_, err = git.PlainClone(verifyDir, false, &git.CloneOptions{
		URL:           gitURL,
		Depth:         1,
		SingleBranch:  true,
		ReferenceName: plumbing.NewBranchReferenceName("master"),
		Auth: &git_https.BasicAuth{
			Username: gitUser,
			Password: gitToken,
		},
	})
	if err != nil {
		t.Fatalf("failed to clone remote master for verification: %v", err)
	}

	remoteState, err := os.ReadFile(filepath.Join(verifyDir, clusterName, "clusterstate.json"))
	if err != nil {
		t.Fatalf("failed reading clusterstate.json from remote clone: %v", err)
	}
	if !strings.Contains(string(remoteState), marker) {
		t.Fatalf("remote clusterstate.json does not contain marker %q", marker)
	}
}
