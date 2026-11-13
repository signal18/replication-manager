//go:build !linux

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package selfexec

import (
	"os"
	"syscall"
)

// Path is the running binary.
func Path() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// ChildAttr: none outside Linux.
func ChildAttr() *syscall.SysProcAttr { return nil }
