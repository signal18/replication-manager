// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import "syscall"

// selfExecutable is the running binary, even once an upgrade replaced it on disk.
func selfExecutable() string { return "/proc/self/exe" }

// serverProcAttr: the server gets SIGTERM when replication-manager dies, so it unmounts and
// ends with it, never serving a mount nobody supervises.
func serverProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
}
