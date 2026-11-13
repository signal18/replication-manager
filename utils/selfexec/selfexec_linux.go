// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Package selfexec starts a child copy of replication-manager's own binary: the backups folder
// mount server (s3fuse) and the /etc credential writer (etcwriter).
package selfexec

import "syscall"

// Path is the running binary, even once an upgrade replaced it on disk.
func Path() string { return "/proc/self/exe" }

// ChildAttr: the child gets SIGTERM when replication-manager dies, so it ends with it, never
// running unsupervised; in its own process group, a Ctrl-C to replication-manager's group (a
// foreground run) never stops it under the monitor.
func ChildAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM, Setpgid: true}
}
