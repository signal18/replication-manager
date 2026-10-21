// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// binlogCopyQueue holds the binlogs of a server to copy into its backup directory (#2091). A
// rotation queues the binlog it closed and returns at once; one worker per server copies them in
// order, then runs the catch-up and keep removal once, when no backup, snapshot or failover
// runs. A long snapshot holds the copies back without one goroutine per monitor tick, and the
// binlogs it holds back are never purged on the server (PurgeBinlogTo).
type binlogCopyQueue struct {
	mu      sync.Mutex
	pending []string  // the binlogs to copy, oldest first, each once
	catchUp string    // the last binlog rotated: the catch-up and keep removal after the copies
	running bool      // the worker runs
	waiting string    // what the worker waits for, "" while it does not wait
	since   time.Time // since when it waits
}

// binlogCopyQueueMax bounds the binlogs a server holds to copy: the keep window, beyond which a
// copy would be removed at once (backup-binlogs-keep).
func (cluster *Cluster) binlogCopyQueueMax() int {
	if cluster.Conf.BackupBinlogsKeep > 0 {
		return cluster.Conf.BackupBinlogsKeep
	}
	return 1
}

// queueBinlogCopy queues the copy of binlog, closed by a rotation, and starts the server's
// worker when none runs. It never waits.
func (server *ServerMonitor) queueBinlogCopy(binlog string) {
	cluster := server.ClusterGroup
	q := &server.binlogCopies
	q.mu.Lock()
	if binlog != "" && !slices.Contains(q.pending, binlog) {
		q.pending = append(q.pending, binlog)
		if max := cluster.binlogCopyQueueMax(); len(q.pending) > max {
			dropped := q.pending[:len(q.pending)-max]
			q.pending = append([]string(nil), q.pending[len(q.pending)-max:]...)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Binlog copy of %s: %v dropped from the copies to make, beyond backup-binlogs-keep (%d)", server.URL, dropped, max)
		}
	}
	if binlog != "" {
		q.catchUp = binlog
	}
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		go server.runBinlogCopies()
	}
}

// pendingBinlogCopies is a copy of the binlogs waiting to be copied, oldest first.
func (server *ServerMonitor) pendingBinlogCopies() []string {
	q := &server.binlogCopies
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]string(nil), q.pending...)
}

// oldestPendingBinlogCopy is the oldest binlog waiting to be copied, "" when none.
func (server *ServerMonitor) oldestPendingBinlogCopy() string {
	if p := server.pendingBinlogCopies(); len(p) > 0 {
		return p[0]
	}
	return ""
}

// runBinlogCopies is the server's worker: the queued copies in order, then the catch-up once.
func (server *ServerMonitor) runBinlogCopies() {
	cluster := server.ClusterGroup
	q := &server.binlogCopies
	defer func() {
		q.mu.Lock()
		q.running, q.waiting = false, ""
		q.mu.Unlock()
	}()
	defer cluster.recoverBackupPanic("Binlog copies of "+server.URL, nil)
	for {
		q.mu.Lock()
		idle := len(q.pending) == 0 && q.catchUp == ""
		q.mu.Unlock()
		if idle || !server.waitBinlogCopySlot() {
			return
		}
		q.mu.Lock()
		var binlog, catchUp string
		if len(q.pending) > 0 {
			binlog = q.pending[0]
		} else {
			catchUp, q.catchUp = q.catchUp, ""
		}
		q.mu.Unlock()
		if binlog != "" {
			if err := server.InitiateJobBackupBinlog(binlog, false); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Binlog copy of %s: %s not copied: %s", server.URL, binlog, err)
			}
			q.mu.Lock()
			q.pending = slices.DeleteFunc(q.pending, func(b string) bool { return b == binlog })
			q.mu.Unlock()
			continue
		}
		if err := server.JobBackupBinlogPurge(catchUp); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Binlog catch-up of %s: %s", server.URL, err)
		}
	}
}

// waitBinlogCopySlot waits until no backup, snapshot of the server's backup directory or
// failover runs, recording what it waits for (WARN0257); false when the cluster stops.
func (server *ServerMonitor) waitBinlogCopySlot() bool {
	cluster := server.ClusterGroup
	q := &server.binlogCopies
	for {
		var waiting string
		switch {
		case cluster.IsInFailover():
			waiting = "the failover"
		case cluster.IsInBackupJob():
			waiting = "the backup in progress"
		case cluster.IsInResticBackup() || server.snapshotsRunning.Load() > 0:
			waiting = "the snapshot in progress"
		}
		q.mu.Lock()
		if waiting == "" {
			q.waiting = ""
		} else if q.waiting != waiting {
			q.waiting, q.since = waiting, time.Now()
		}
		q.mu.Unlock()
		if waiting == "" {
			return true
		}
		if cluster.exit.Load() {
			return false
		}
		time.Sleep(time.Second)
	}
}

// checkBinlogCopyStates, every tick: WARN0257 for each server whose binlog copies wait.
func (cluster *Cluster) checkBinlogCopyStates() {
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		q := &server.binlogCopies
		q.mu.Lock()
		waiting, since, pending := q.waiting, q.since, append([]string(nil), q.pending...)
		q.mu.Unlock()
		if waiting == "" || len(pending) == 0 {
			continue
		}
		oldest := pending[0]
		if server.BinaryLogFileOldest != "" && server.BinaryLogFileOldest >= oldest {
			oldest += " (the server's oldest binlog)"
		}
		cluster.SetState("WARN0257", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0257"], server.URL, waiting, since.Format(time.RFC3339), len(pending), oldest), ErrFrom: "JOB", ServerUrl: server.URL})
	}
}
