// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build linux

package cluster

import (
	"os/exec"
	"syscall"
)

// helperDiesWithParent: the kernel sends the helper SIGTERM when replication-manager dies, even
// killed, so the helper unmounts and never serves a mount nobody supervises (#2091).
func helperDiesWithParent(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
