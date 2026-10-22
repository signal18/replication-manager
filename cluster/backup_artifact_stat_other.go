//go:build !linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"time"
)

// artifactStatIdentity: inode and ctime are read on Linux only, where the server runs.
// Elsewhere a fingerprint has none and matches nothing (artifactUnchangedBounded).
func artifactStatIdentity(fi os.FileInfo) (ino uint64, ctime time.Time) {
	return 0, time.Time{}
}

// artifactSyncBucket: the backups folder mount runs on Linux only.
func artifactSyncBucket(path string) error { return nil }

// onSameDevice: not known off Linux, so never counted on.
func onSameDevice(a, b string) bool { return false }
