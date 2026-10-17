// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build !linux

package cluster

import "os/exec"

// helperDiesWithParent: only Linux tells a child its parent died; elsewhere a leftover mount is
// detached at the next start (RefreshBackupMount).
func helperDiesWithParent(cmd *exec.Cmd) {}
