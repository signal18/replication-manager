// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync/atomic"
	"time"
)

// A snapshot of a server's backup directory and a binlog copy into it never run together
// (#2091): a snapshot never holds a binlog being copied, a copy never lands in a directory being
// snapshotted. Each side counts itself in first, then looks at the other: a snapshot waits for
// the copies in progress to end, a copy that finds a snapshot steps back and waits for it.

// backupDirExclusionPoll is how often a waiting snapshot or binlog copy looks again.
var backupDirExclusionPoll = 200 * time.Millisecond

// beginSnapshot counts a snapshot of the server's backup directory in and waits for the binlog
// copies in progress to end. The release it returns counts it out, once however often called.
func (server *ServerMonitor) beginSnapshot() (release func()) {
	server.snapshotsRunning.Add(1)
	for server.binlogCopyRunning.Load() > 0 {
		time.Sleep(backupDirExclusionPoll)
	}
	var released atomic.Bool
	return func() {
		if released.CompareAndSwap(false, true) {
			server.snapshotsRunning.Add(-1)
		}
	}
}

// beginBinlogCopy counts a binlog copy into the server's backup directory in, once no snapshot
// of it runs. The caller calls endBinlogCopy when the copy is published or discarded. A copy
// started inside another (the catch-up of JobBackupBinlogPurge) is not counted again.
func (server *ServerMonitor) beginBinlogCopy() {
	for {
		server.binlogCopyRunning.Add(1)
		if server.snapshotsRunning.Load() == 0 {
			return
		}
		server.binlogCopyRunning.Add(-1)
		time.Sleep(backupDirExclusionPoll)
	}
}

// endBinlogCopy counts a binlog copy out (beginBinlogCopy).
func (server *ServerMonitor) endBinlogCopy() {
	server.binlogCopyRunning.Add(-1)
}
