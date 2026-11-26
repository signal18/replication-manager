// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build unix

package cluster

import (
	"os/exec"
	"syscall"
)

// snapshotCmdOwnGroup runs cmd in a process group of its own, killed whole at its deadline:
// a zfs rollback or an lvconvert started by the helper stops with it, not only its sh.
func snapshotCmdOwnGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
