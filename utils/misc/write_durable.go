package misc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// staleTempAge: a temp file of WriteFileDurable older than this was left by a crash between its
// write and its rename; the next write of the same file removes it (it may hold secrets, F4).
const staleTempAge = 10 * time.Minute

// WriteFileDurable replaces path with payload so that, after a crash or a power loss, the file
// is either the previous one or the new one, whole, and once it returns the new one stays: a
// temp file in the same directory (tempPattern, as os.CreateTemp), written and fsynced, renamed
// over path, then the directory fsynced (the rename is an entry of the directory: without its
// sync, a power loss can bring the previous file back). The password rotation relies on it: it
// changes a database only once the new credential is on disk (#2269).
//
//   - An existing file keeps its mode and owner; newPerm is the mode of a file created here (the
//     temp file's Chmod ignores the umask, so the caller's mode is exactly what the file gets).
//   - A symlink is followed: its target is replaced, the link stays. A dangling link cannot be
//     followed: it is replaced by a regular file.
//   - A file that cannot be replaced by a rename (a single-file bind mount: EBUSY, EXDEV) is
//     written in place and fsynced instead: durable, NOT atomic (a crash between the truncate
//     and the fsync leaves a short or empty file).
//   - tempPattern must contain a "*" (".name-*.tmp"): the leftover cleanup globs it, and without
//     one os.CreateTemp appends its random part at the end, where the glob would miss it.
func WriteFileDurable(path string, payload []byte, newPerm os.FileMode, tempPattern string) error {
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	removeStaleTemps(dir, tempPattern)

	perm := newPerm
	uid, gid := -1, -1
	if fi, err := os.Stat(path); err == nil {
		perm = fi.Mode().Perm()
		if st, ok := fi.Sys().(*syscall.Stat_t); ok {
			uid, gid = int(st.Uid), int(st.Gid)
		}
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
	if uid >= 0 && (uid != os.Getuid() || gid != os.Getgid()) {
		// keep the owner of the file it replaces; refused when not privileged: the file then
		// belongs to this process, as a file written in place would not
		_ = tmpFile.Chown(uid, gid)
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		if errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV) {
			return writeInPlaceDurable(path, payload)
		}
		return fmt.Errorf("atomic rename failed: %w", err)
	}
	return SyncDir(dir)
}

// writeInPlaceDurable writes payload over path in place and fsyncs it: for a file no rename can
// replace (a single-file bind mount).
func writeInPlaceDurable(path string, payload []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(payload); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// removeStaleTemps removes the temp files of tempPattern a crash left in dir.
func removeStaleTemps(dir, tempPattern string) {
	matches, err := filepath.Glob(filepath.Join(dir, tempPattern))
	if err != nil {
		return
	}
	for _, m := range matches {
		if fi, err := os.Stat(m); err == nil && time.Since(fi.ModTime()) > staleTempAge {
			_ = os.Remove(m)
		}
	}
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
