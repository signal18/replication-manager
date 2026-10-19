//go:build !linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"os"
	"time"
)

// artifactStatIdentity: inode and ctime are read on Linux only, where the server runs.
// Elsewhere a fingerprint has none and matches nothing (artifactUnchangedBounded).
func artifactStatIdentity(fi os.FileInfo) (ino uint64, ctime time.Time) {
	return 0, time.Time{}
}

// artifactETag: the backups folder mount runs on Linux only.
func artifactETag(path string) (string, error) {
	return "", errors.New("no S3 ETag off Linux")
}

// artifactSyncBucket: the backups folder mount runs on Linux only.
func artifactSyncBucket(path string) error { return nil }
