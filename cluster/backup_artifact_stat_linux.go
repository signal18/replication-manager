//go:build linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"syscall"
	"time"
)

// artifactStatIdentity returns a file's inode and its status change time (ctime): touch and a
// copy can set the modification time back, not the ctime, and a replaced file gets another
// inode, so a backup changed since it was verified does not match its fingerprint.
func artifactStatIdentity(fi os.FileInfo) (ino uint64, ctime time.Time) {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Ino, time.Unix(st.Ctim.Sec, st.Ctim.Nsec)
	}
	return 0, time.Time{}
}
