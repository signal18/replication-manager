// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// WriteFileDurable replaces the file whole and leaves no temp file next to it (the config push
// stages <cluster>/*.toml: a temp file must never match). An existing file keeps its mode (a
// Chmod to the caller's mode would widen a 0644 toml to 0666, the umask not applying); a new file
// gets the caller's mode. The fsyncs are not observable without a crash: SyncDir is checked to
// fail on a missing directory.
func TestWriteFileDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c1.toml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileDurable(path, []byte("new = 1\n"), 0o640, ".c1.toml-*.tmp"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new = 1\n" {
		t.Fatalf("content %q, %v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("an existing file keeps its mode: got %v, want 0644", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temp file was left: %v", entries)
	}

	// a new file: the caller's mode, never wider
	fresh := filepath.Join(dir, "default.toml")
	if err := WriteFileDurable(fresh, []byte("a = 1\n"), 0o640, ".default.toml-*.tmp"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(fresh); fi.Mode().Perm() != 0o640 {
		t.Fatalf("a new file gets the caller's mode: got %v, want 0640", fi.Mode().Perm())
	}

	// a symlink is followed: its target changes, the link stays
	target := filepath.Join(dir, "real.toml")
	link := filepath.Join(dir, "link.toml")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileDurable(link, []byte("y"), 0o640, ".link.toml-*.tmp"); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if b, _ := os.ReadFile(target); string(b) != "y" {
		t.Fatalf("the target was not written: %q", b)
	}

	// a temp file a crash left behind is removed by the next write; a fresh one is not
	stale := filepath.Join(dir, ".c1.toml-111.tmp")
	if err := os.WriteFile(stale, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileDurable(path, []byte("new = 2\n"), 0o640, ".c1.toml-*.tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("a stale temp file was not removed")
	}

	if err := SyncDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("SyncDir of a missing directory must fail")
	}
}
