//go:build !linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package s3fuse

import (
	"os"
	"syscall"
)

func selfExecutable() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

func serverProcAttr() *syscall.SysProcAttr { return nil }
