package misc

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileDurable replaces path with payload so that, after a crash or a power loss, the file
// is either the previous one or the new one, whole, and once it returns the new one stays: a
// temp file in the same directory (tempPattern, as os.CreateTemp), written and fsynced, renamed
// over path, then the directory fsynced (the rename is an entry of the directory: without its
// sync, a power loss can bring the previous file back). perm is the file's mode. The password
// rotation relies on it: it changes a database only once the new credential is on disk (#2269).
func WriteFileDurable(path string, payload []byte, perm os.FileMode, tempPattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()

	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmpFile.Write(payload); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Chmod(perm); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomic rename failed: %w", err)
	}
	return SyncDir(dir)
}

// SyncDir fsyncs a directory, making the entries renamed or created in it durable.
func SyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync of directory %s: %w", dir, err)
	}
	return nil
}
