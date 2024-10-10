package misc

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func ChownR(path string, uid, gid int) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err == nil {
			err = os.Chown(name, uid, gid)
		}
		return err
	})
}

func ChmodR(path string, perm os.FileMode) error {
	return filepath.Walk(path, func(name string, info os.FileInfo, err error) error {
		if err == nil {
			err = os.Chmod(name, perm)
		}
		return err
	})
}

func ReadFile(src string) (string, error) {
	filerc, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer filerc.Close()

	buf := new(bytes.Buffer)
	buf.ReadFrom(filerc)
	return buf.String(), nil
}

// CopyFile copies the contents of the file named src to the file named
// by dst. The file will be created if it does not already exist. If the
// destination file exists, all it's contents will be replaced by the contents
// of the source file. The file mode will be copied from the source and
// the copied data is synced/flushed to stable storage.
func CopyFile(src string, dst string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return
	}
	defer func() {
		if e := out.Close(); e != nil {
			err = e
		}
	}()

	_, err = io.Copy(out, in)
	if err != nil {
		return
	}

	err = out.Sync()
	if err != nil {
		return
	}

	si, err := os.Stat(src)
	if err != nil {
		return
	}
	err = os.Chmod(dst, si.Mode())
	if err != nil {
		return
	}

	return
}

func CopyFileClose(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}
	return out.Close()
}

// CopyDir recursively copies a directory tree, attempting to preserve permissions.
// Source directory must exist, destination directory must *not* exist.
// Symlinks are ignored and skipped.
func CopyDir(src string, dst string) (err error) {
	src = filepath.Clean(src)
	dst = filepath.Clean(dst)

	si, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !si.IsDir() {
		return fmt.Errorf("source is not a directory")
	}

	_, err = os.Stat(dst)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	if err == nil {
		return fmt.Errorf("destination already exists")
	}

	err = os.MkdirAll(dst, si.Mode())
	if err != nil {
		return
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			err = CopyDir(srcPath, dstPath)
			if err != nil {
				return
			}
		} else {
			var eInfo fs.FileInfo
			if eInfo, err = entry.Info(); err != nil {
				return
			} else {
				// Skip symlinks.
				if eInfo.Mode()&os.ModeSymlink != 0 {
					continue
				}
			}

			err = CopyFile(srcPath, dstPath)
			if err != nil {
				return
			}
		}
	}

	return
}

// RemoveOldLogFiles removes log files older than a specified number of days
// from the given directory, based on a specified prefix and timestamp format.
func RemoveOldLogFiles(dir, prefix string, daysOld int, timestampFormat string) error {
	// Get the current time and define the threshold time based on the specified number of days
	currentTime := time.Now()
	threshold := currentTime.Add(-time.Duration(daysOld) * 24 * time.Hour)

	// Create a pattern for log files
	pattern := filepath.Join(dir, fmt.Sprintf("%s*.log", prefix))

	// Use Glob to find files matching the pattern
	files, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	// Track any errors during file deletion
	var deletionErrors []error

	// Iterate over the matched files
	for _, filePath := range files {
		info, err := os.Stat(filePath)
		if err != nil {
			deletionErrors = append(deletionErrors, err)
			continue
		}

		// Check if the file is a regular file
		if !info.Mode().IsRegular() {
			continue
		}

		// Extract the timestamp from the filename
		fileDateString := strings.TrimSuffix(strings.TrimPrefix(info.Name(), prefix), ".log")
		fileTime, err := time.Parse(timestampFormat, fileDateString)
		if err != nil {
			deletionErrors = append(deletionErrors, err)
			continue // Skip files with invalid timestamp format
		}

		// Check if the file is older than the threshold
		if fileTime.Before(threshold) {
			// Remove the log file
			err := os.Remove(filePath)
			if err != nil {
				deletionErrors = append(deletionErrors, err)
				continue
			}
			fmt.Printf("Deleted: %s\n", filePath)
		}
	}

	// Report any errors encountered during deletion
	if len(deletionErrors) > 0 {
		return fmt.Errorf("errors encountered during deletion: %v", deletionErrors)
	}
	return nil
}

func TryOpenFile(path string, flag int, perm fs.FileMode, remove bool) error {
	// If it's a file, try opening it with write permissions
	file, err := os.OpenFile(path, flag, 0666)
	if err != nil {
		return err
	}
	file.Close()

	if remove {
		os.Remove(path)
	}

	return nil
}
