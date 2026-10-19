// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build !linux

package s3fuse

// disablePoll: the poll request a file of the mount sends to this process is Linux's
func disablePoll(point string) error { return nil }
