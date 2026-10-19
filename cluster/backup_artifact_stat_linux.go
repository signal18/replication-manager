//go:build linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// artifactStatIdentity returns a file's inode and its status change time (ctime): touch and a
// copy can set the modification time back, not the ctime, and a replaced file gets another
// inode, so a backup changed since it was verified does not match its fingerprint.
func artifactStatIdentity(fi os.FileInfo) (ino uint64, ctime time.Time) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		// int64: Timespec fields are int32 on 32-bit Linux (arm, 386)
		return uint64(st.Ino), time.Unix(int64(st.Ctim.Sec), int64(st.Ctim.Nsec))
	}
	return 0, time.Time{}
}

// artifactETag returns the S3 ETag of a file of the backups folder mount, its s3.etag
// attribute: ENODATA while the file is not uploaded yet (written, still buffered).
func artifactETag(path string) (string, error) {
	buf := make([]byte, 256)
	n, err := unix.Lgetxattr(path, "s3.etag", buf)
	if err != nil {
		return "", err
	}
	return strings.Trim(string(buf[:n]), `"`), nil
}

// artifactSyncBucket waits until the backup at path, a file or a folder of the backups folder
// mount, is uploaded: GeeseFS keeps a file written in memory and uploads it in the background,
// its s3.etag meanwhile the one of its previous upload. An fsync uploads what it holds, the
// whole tree for a folder, and returns at once when nothing is pending.
func artifactSyncBucket(path string) error {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	return unix.Fsync(fd)
}
