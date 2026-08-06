package server

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestNormalizePullPluginSymlinks verifies the pre-symlink-upgrade self-heal:
// a legacy real .pull/<cluster>/plugins directory (which blocks go-git from
// checking out the ../plugins symlink the -pull repo ships) is converted to the
// symlink, while an already-correct symlink and .git are left untouched.
func TestNormalizePullPluginSymlinks(t *testing.T) {
	tmp := t.TempDir()
	pullDir := filepath.Join(tmp, ".pull")

	// shared, git-authoritative plugins dir
	if err := os.MkdirAll(filepath.Join(pullDir, "plugins"), 0755); err != nil {
		t.Fatal(err)
	}
	// legacy real per-cluster plugins dir (the conflict) with a stale binary
	benchPlugins := filepath.Join(pullDir, "bench", "plugins")
	if err := os.MkdirAll(benchPlugins, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchPlugins, "plugin-stale"), []byte("x"), 0755); err != nil {
		t.Fatal(err)
	}
	// a cluster already correct (symlink) — must be left untouched
	if err := os.MkdirAll(filepath.Join(pullDir, "evaluo"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "plugins"), filepath.Join(pullDir, "evaluo", "plugins")); err != nil {
		t.Fatal(err)
	}
	// .git must be skipped
	if err := os.MkdirAll(filepath.Join(pullDir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.normalizePullPluginSymlinks(pullDir)

	// bench/plugins is now a symlink to ../plugins
	fi, err := os.Lstat(benchPlugins)
	if err != nil {
		t.Fatalf("bench/plugins missing after normalize: %v", err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("bench/plugins should be a symlink, got mode %v", fi.Mode())
	}
	if tgt, _ := os.Readlink(benchPlugins); tgt != filepath.Join("..", "plugins") {
		t.Errorf("bench/plugins -> %q, want %q", tgt, filepath.Join("..", "plugins"))
	}

	// evaluo/plugins remains the symlink (unchanged)
	if fi2, err := os.Lstat(filepath.Join(pullDir, "evaluo", "plugins")); err != nil || fi2.Mode()&os.ModeSymlink == 0 {
		t.Errorf("evaluo/plugins should remain a symlink (err=%v)", err)
	}

	// .git untouched (still a real dir)
	if fi3, err := os.Lstat(filepath.Join(pullDir, ".git")); err != nil || !fi3.IsDir() {
		t.Errorf(".git should be untouched")
	}
}

// TestNormalizePullPluginSymlinks_NoPullDir ensures a missing .pull (first clone)
// is a safe no-op and does not panic.
func TestNormalizePullPluginSymlinks_NoPullDir(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.normalizePullPluginSymlinks(filepath.Join(t.TempDir(), "does-not-exist"))
}
