// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package selfexec starts a child copy of replication-manager's own binary: the backups folder
// mount server (s3fuse) and the /etc credential writer (etcwriter).
package selfexec

import (
	"os/exec"
	"runtime"
)

// StartLocked starts cmd from a goroutine locked to its thread for the child's whole life: the
// child's parent-death signal (ChildAttr) follows the thread that started it, not the process,
// and Go would otherwise end or reuse that thread. waited gets cmd.Wait's result.
func StartLocked(cmd *exec.Cmd, waited chan<- error) error {
	started := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // never unlocked: the thread ends with this goroutine
		if err := cmd.Start(); err != nil {
			started <- err
			return
		}
		started <- nil
		waited <- cmd.Wait()
	}()
	return <-started
}
