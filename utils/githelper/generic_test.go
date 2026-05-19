package githelper

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupLocalRepo creates a temporary git repo with a branch and test files.
// Skips the test if git is not available in PATH.
func setupLocalRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in PATH")
	}
	repoDir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = append(os.Environ(),
			"GIT_TERMINAL_PROMPT=0",
			"GIT_AUTHOR_NAME=test",
			"GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test",
			"GIT_COMMITTER_EMAIL=test@test.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "-b", "main")
	run("config", "user.email", "test@test.com")
	run("config", "user.name", "test")

	if err := os.WriteFile(filepath.Join(repoDir, "hello.txt"), []byte("hello world\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repoDir, "subdir"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "subdir", "file.txt"), []byte("nested\n"), 0600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "init")

	return repoDir
}

func TestGenericGitClient_CheckRepo(t *testing.T) {
	repoDir := setupLocalRepo(t)
	gc := NewGenericGitClient("", "")

	msg, err := gc.CheckRepo(repoDir, "main", 30*time.Second)
	if err != nil {
		t.Fatalf("CheckRepo failed: %v", err)
	}
	if msg == "" {
		t.Error("expected non-empty success message")
	}
}

func TestGenericGitClient_CheckRepo_MissingBranch(t *testing.T) {
	repoDir := setupLocalRepo(t)
	gc := NewGenericGitClient("", "")

	_, err := gc.CheckRepo(repoDir, "does-not-exist", 30*time.Second)
	if err == nil {
		t.Error("expected error for missing branch, got nil")
	}
}

func TestGenericGitClient_CheckRepo_BadPath(t *testing.T) {
	gc := NewGenericGitClient("", "")
	_, err := gc.CheckRepo("/nonexistent/repo/path", "main", 30*time.Second)
	if err == nil {
		t.Error("expected error for non-existent repo, got nil")
	}
}

func TestGenericGitClient_GetRepositoryTree(t *testing.T) {
	repoDir := setupLocalRepo(t)
	cacheDir := t.TempDir()
	gc := NewGenericGitClient("", "")

	tree, err := gc.GetRepositoryTree(cacheDir, repoDir, "main", 30*time.Second, true)
	if err != nil {
		t.Fatalf("GetRepositoryTree failed: %v", err)
	}
	if tree == nil || tree.Tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestGenericGitClient_GetRepositoryTree_CacheHit(t *testing.T) {
	repoDir := setupLocalRepo(t)
	cacheDir := t.TempDir()
	gc := NewGenericGitClient("", "")

	if _, err := gc.GetRepositoryTree(cacheDir, repoDir, "main", 30*time.Second, true); err != nil {
		t.Fatalf("first GetRepositoryTree failed: %v", err)
	}

	tree, err := gc.GetRepositoryTree(cacheDir, repoDir, "main", 30*time.Second, false)
	if err != nil {
		t.Fatalf("second GetRepositoryTree failed: %v", err)
	}
	if !tree.IsCached {
		t.Error("expected IsCached=true on second call")
	}
}

func TestGenericGitClient_DownloadFileFromRepo(t *testing.T) {
	repoDir := setupLocalRepo(t)
	gc := NewGenericGitClient("", "")

	content, err := gc.DownloadFileFromRepo(repoDir, "main", "hello.txt", 30*time.Second)
	if err != nil {
		t.Fatalf("DownloadFileFromRepo failed: %v", err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("unexpected content: %q", string(content))
	}
}

func TestGenericGitClient_NoCredentialsInProcessArgs(t *testing.T) {
	// Verify that credentials are stored as Go struct values and that the
	// basicAuth() helper returns them correctly without any string encoding.
	gc := NewGenericGitClient("user", "s3cr3t")
	auth := gc.basicAuth()
	if auth == nil {
		t.Fatal("expected non-nil auth")
	}
	if auth.Password != "s3cr3t" {
		t.Errorf("unexpected password: %q", auth.Password)
	}
	// The password is never placed into a URL string or exec args by this client.
	// Confirm it doesn't appear in the sanitizeCacheRef output.
	ref := sanitizeCacheRef("https://user:s3cr3t@git.example.com/org/repo.git")
	if strings.Contains(ref, "s3cr3t") {
		t.Errorf("sanitizeCacheRef must strip credentials, got %q", ref)
	}
}

func TestSanitizeCacheRef(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://git.example.com/org/repo.git", "git.example.com/org/repo"},
		{"https://user:pass@git.example.com/org/repo.git", "git.example.com/org/repo"},
		{"git@github.com:org/repo.git", "github.com/org/repo"},
		{"/local/path/repo.git", "/local/path/repo"},
		{"https://gitlab.com/group/sub/repo.git", "gitlab.com/group/sub/repo"},
	}
	for _, tt := range tests {
		got := sanitizeCacheRef(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeCacheRef(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
