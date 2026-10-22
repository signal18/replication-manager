// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build !linux

package s3fuse

import "time"

// AbortStuckMounts: a FUSE connection left stuck by a previous run is aborted through Linux's fusectl
func AbortStuckMounts(root string, interval time.Duration) ([]string, error) { return nil, nil }

// AbortMount: aborting a FUSE connection goes through Linux's fusectl
func AbortMount(point string) error { return nil }
