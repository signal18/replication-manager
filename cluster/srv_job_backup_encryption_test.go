// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTightenBackupDirectoryPermissionsOnceMigratesExistingContent(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "sub")
	if err := os.MkdirAll(subdir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(dir, "mysqldump.sql.gz")
	if err := os.WriteFile(file, []byte("plaintext"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	nestedFile := filepath.Join(subdir, "shard.sql.gz")
	if err := os.WriteFile(nestedFile, []byte("plaintext"), 0644); err != nil {
		t.Fatalf("write nested file: %v", err)
	}

	cluster := &Cluster{}
	cluster.tightenBackupDirectoryPermissionsOnce(dir)

	assertMode := func(path string, want os.FileMode) {
		t.Helper()
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Fatalf("%s: got mode %o, want %o", path, got, want)
		}
	}

	assertMode(dir, 0700)
	assertMode(subdir, 0700)
	assertMode(file, 0600)
	assertMode(nestedFile, 0600)

	markerPath := filepath.Join(dir, backupPermissionsMigratedMarker)
	if _, err := os.Stat(markerPath); err != nil {
		t.Fatalf("expected migration marker at %s: %v", markerPath, err)
	}
}

func TestTightenBackupDirectoryPermissionsOnceIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "mysqldump.sql.gz")
	if err := os.WriteFile(file, []byte("plaintext"), 0644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cluster := &Cluster{}
	cluster.tightenBackupDirectoryPermissionsOnce(dir)

	// Simulate a later, unrelated file appearing with loose permissions
	// (e.g. a new backup written after the one-time migration already ran).
	newFile := filepath.Join(dir, "later.sql.gz")
	if err := os.WriteFile(newFile, []byte("plaintext"), 0644); err != nil {
		t.Fatalf("write later file: %v", err)
	}

	// A second call must be a no-op (marker present) -- it must not re-walk
	// and touch the newly-appeared file's permissions.
	cluster.tightenBackupDirectoryPermissionsOnce(dir)

	info, err := os.Stat(newFile)
	if err != nil {
		t.Fatalf("stat %s: %v", newFile, err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("expected the one-shot migration to skip re-walking, but %s mode changed to %o", newFile, got)
	}
}
