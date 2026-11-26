// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

//go:build !unix

package cluster

import "os/exec"

// snapshotCmdOwnGroup: no process group here, the deadline kills the program only.
func snapshotCmdOwnGroup(cmd *exec.Cmd) {}
