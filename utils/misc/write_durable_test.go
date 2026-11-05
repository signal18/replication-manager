// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package misc

import (
	"os"
	"path/filepath"
	"testing"
)

// WriteFileDurable replaces the file whole, with its mode, and leaves no temp file next to it
// (the config push stages <cluster>/*.toml: a temp file must never match). The fsyncs are not
// observable without a crash: SyncDir is checked to fail on a missing directory.
func TestWriteFileDurable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c1.toml")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileDurable(path, []byte("new = 1\n"), 0640, ".c1.toml-*.tmp"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "new = 1\n" {
		t.Fatalf("content %q, %v", got, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0640 {
		t.Fatalf("mode %v, want 0640", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("a temp file was left: %v", entries)
	}
	if err := SyncDir(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("syncDir of a missing directory must fail")
	}
}
