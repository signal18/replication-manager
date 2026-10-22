// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/dbhelper"
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

	// the binlog copies and the archive (#2091): a copy beyond the keep window no snapshot holds
	// is kept while a binlog-only snapshot is queued or running, then removed once it holds it.
	// When that snapshot is not taken (binlogSnapshotRefusal) or fails, the copy is removed
	// anyway: the purge policy and the space always win, the loss is tracked (BinlogGap).
	inSnapshot     string    // the newest binlog copy the last snapshot held, "" unknown
	held           int       // the copies beyond the keep window kept for the snapshot
	heldOldest     string    // the oldest of them
	heldSince      time.Time // since when copies are held
	needSnapshot   bool      // the keep removal held copies: a snapshot is to take
	snapshotting   bool      // the binlog-only snapshot is queued or running
	snapshotFailed string    // why the last one failed, until the keep removal took it in
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
	var lost []string
	q.mu.Lock()
	if binlog != "" && !slices.Contains(q.pending, binlog) {
		q.pending = append(q.pending, binlog)
		if max := cluster.binlogCopyQueueMax(); len(q.pending) > max {
			dropped := q.pending[:len(q.pending)-max]
			q.pending = append([]string(nil), q.pending[len(q.pending)-max:]...)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Binlog copy of %s: %v dropped from the copies to make, beyond backup-binlogs-keep (%d)", server.URL, dropped, max)
			lost = append(lost, dropped...)
		}
	}
	if binlog != "" {
		q.catchUp = binlog
	}
	start := !q.running
	q.running = true
	q.mu.Unlock()
	for _, b := range lost {
		cluster.noteBinlogGap(server, b, fmt.Sprintf("not copied: beyond backup-binlogs-keep (%d) while the copies waited", cluster.binlogCopyQueueMax()))
	}
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
	idle := false
	defer func() {
		if idle {
			return // ended under the lock it found the queue empty with
		}
		q.mu.Lock()
		q.running, q.waiting = false, ""
		q.mu.Unlock()
	}()
	defer cluster.recoverBackupPanic("Binlog copies of "+server.URL, nil)
	for {
		q.mu.Lock()
		// idle and ended at once: a copy queued or a catch-up requeued (the binlog-only
		// snapshot ended) after it starts a new worker, never waits for the next rotation
		if idle = len(q.pending) == 0 && q.catchUp == ""; idle {
			q.running, q.waiting = false, ""
			q.mu.Unlock()
			return
		}
		q.mu.Unlock()
		if !server.waitBinlogCopySlot() {
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
				// gone from the server before its copy (its own size or time purge): no copy can
				// be made any more, the archive misses it (F12); any other failure is no loss
				if server.binlogPurgedOnServer(binlog) {
					cluster.noteBinlogGap(server, binlog, "not copied: purged on the server before its copy")
				}
			}
			q.mu.Lock()
			q.pending = slices.DeleteFunc(q.pending, func(b string) bool { return b == binlog })
			q.mu.Unlock()
			continue
		}
		if err := server.JobBackupBinlogPurge(catchUp); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlDbg, "Binlog catch-up of %s: %s", server.URL, err)
		}
		// the copies the keep removal held: the binlog-only snapshot, which runs it again once ended
		server.snapshotHeldBinlogCopies(catchUp)
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

// newestBinlogCopy is the newest copy of the server's binlogs in its backup directory, "" none.
func (server *ServerMonitor) newestBinlogCopy() string {
	prefix := binlogPrefix(server.BinaryLogFile)
	if prefix == "" {
		return ""
	}
	files, err := os.ReadDir(server.GetMyBackupDirectory())
	if err != nil {
		return ""
	}
	newest := ""
	for _, f := range files {
		name := logicalArtifactName(f.Name())
		if f.IsDir() || isBackupIntegritySidecar(f.Name()) || binlogPrefix(name) != prefix || binlogNumber(name) < 0 {
			continue
		}
		if newest == "" || binlogNumber(name) > binlogNumber(newest) {
			newest = name
		}
	}
	return newest
}

// binlogPrefix is the name of a binlog without its number (mysql-bin.000042: mysql-bin).
// binlogPurgedOnServer tells whether binlog is no longer on the server: older than its oldest
// binlog, read now (the list the monitor keeps can be a tick old).
func (server *ServerMonitor) binlogPurgedOnServer(binlog string) bool {
	if server.Conn == nil {
		return false
	}
	_, oldest, _, _, err := dbhelper.GetBinaryLogs(server.Conn, server.DBVersion, dbhelper.NewBinaryLogMetaMap())
	if err != nil {
		return false
	}
	return binlogOlderThan(binlog, oldest)
}

// binlogOlderThan tells whether binlog comes before oldest in the same binlog sequence.
func binlogOlderThan(binlog, oldest string) bool {
	if oldest == "" || binlogPrefix(binlog) != binlogPrefix(oldest) {
		return false
	}
	n, o := binlogNumber(binlog), binlogNumber(oldest)
	return n >= 0 && o >= 0 && n < o
}

func binlogPrefix(name string) string {
	if i := strings.LastIndex(name, "."); i > 0 {
		return name[:i]
	}
	return ""
}

// recordBinlogsInSnapshot: a snapshot of the backup directory holds its binlog copies up to
// newest.
func (server *ServerMonitor) recordBinlogsInSnapshot(newest string) {
	if newest == "" {
		return
	}
	q := &server.binlogCopies
	q.mu.Lock()
	if binlogNumber(newest) > binlogNumber(q.inSnapshot) || binlogPrefix(newest) != binlogPrefix(q.inSnapshot) {
		q.inSnapshot = newest
	}
	q.mu.Unlock()
}

// binlogCopyInSnapshot: a snapshot holds the copy of binlog; an unknown snapshot (a restart)
// holds none.
func (server *ServerMonitor) binlogCopyInSnapshot(binlog string) bool {
	q := &server.binlogCopies
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.inSnapshot != "" && binlogPrefix(binlog) == binlogPrefix(q.inSnapshot) && binlogNumber(binlog) <= binlogNumber(q.inSnapshot)
}

// holdBinlogCopies records the copies beyond the keep window kept for the binlog-only
// snapshot, the oldest first; none ends the hold.
func (server *ServerMonitor) holdBinlogCopies(held []string) {
	q := &server.binlogCopies
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(held) == 0 {
		q.held, q.heldOldest, q.heldSince, q.needSnapshot = 0, "", time.Time{}, false
		return
	}
	if q.held == 0 {
		q.heldSince = time.Now()
	}
	q.held, q.heldOldest, q.needSnapshot = len(held), held[0], true
}

// binlogSnapshotRefusal is why a binlog copy beyond the keep window no snapshot holds is not
// kept for a binlog-only snapshot, "" it is: the copy is then removed, and the loss tracked.
// Better lose binlogs than backups (F12): the binlog-only snapshot never costs space nor a
// backup, the purge policy always applies.
func (server *ServerMonitor) binlogSnapshotRefusal() string {
	cluster := server.ClusterGroup
	q := &server.binlogCopies
	q.mu.Lock()
	failed, snapshotting := q.snapshotFailed, q.snapshotting
	q.snapshotFailed = "" // taken in once: the next copy beyond the window tries again
	q.mu.Unlock()
	if snapshotting {
		return ""
	}
	if failed != "" {
		return "the binlog-only snapshot failed: " + failed
	}
	if cluster.Conf.BackupArchiveRemoveOriginal {
		return "backup-archive-remove-original: a binlog-only snapshot would hold no backup"
	}
	if warn := cluster.Conf.BackupDiskTresholdWarn; warn > 0 && cluster.DiskStatManager != nil {
		if stat, err := cluster.GetDiskStat(server.GetMyBackupDirectory()); err == nil && stat.UsedPercent >= float64(warn) {
			return fmt.Sprintf("the disk of the backup directory is %.0f%% used, over backup-disk-treshold-warn (%d%%)", stat.UsedPercent, warn)
		}
	}
	return ""
}

// snapshotHeldBinlogCopies, after the keep removal: when it kept copies no snapshot holds, a
// binlog-only snapshot of the backup directory (trigger binlogs), one at a time; once it holds
// them, the keep removal runs again and removes them. A snapshot is so taken every
// backup-binlogs-keep binlogs at most while nothing refuses it (binlogSnapshotRefusal).
func (server *ServerMonitor) snapshotHeldBinlogCopies(catchUp string) {
	cluster := server.ClusterGroup
	q := &server.binlogCopies
	q.mu.Lock()
	if !q.needSnapshot || q.snapshotting {
		q.mu.Unlock()
		return
	}
	q.snapshotting, q.needSnapshot = true, false
	held, oldest := q.held, q.heldOldest
	q.mu.Unlock()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Binlog copies of %s: binlog-only snapshot of the backup directory queued, %d copies beyond backup-binlogs-keep (%d) in no snapshot, the oldest %s", server.URL, held, cluster.Conf.BackupBinlogsKeep, oldest)
	ended := func(err error) {
		q.mu.Lock()
		q.snapshotting = false
		if err != nil {
			q.snapshotFailed = err.Error()
		}
		q.mu.Unlock()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn, "Binlog copies of %s: binlog-only snapshot failed, the copies beyond backup-binlogs-keep are removed: %s", server.URL, err)
		}
		// the keep removal again: the copies the snapshot holds go, or all of them when it failed
		server.requeueBinlogCatchUp(catchUp)
	}
	// its own goroutine: an archive plugin pushes before it returns, the copies go on meanwhile
	go func() {
		var err error
		defer func() {
			if err != nil {
				ended(err) // a push that panics never holds the copies
			}
		}()
		defer cluster.recoverBackupPanic("Binlog-only snapshot of "+server.URL, &err)
		server.backupResticWith(backupmgr.BackupMethodLogical, nil, server.GetMyBackupDirectory(), resticPushOptions{
			removeOriginal: func() bool { return false },
			noFlag:         true,
			trigger:        snapshotTriggerBinlogs,
			done:           ended,
		}, server.BuildResticTags("binlog", "", nil)...)
	}()
}

// requeueBinlogCatchUp runs the catch-up and keep removal again, with no copy.
func (server *ServerMonitor) requeueBinlogCatchUp(binlog string) {
	q := &server.binlogCopies
	q.mu.Lock()
	if q.catchUp == "" {
		q.catchUp = binlog
	}
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		go server.runBinlogCopies()
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
	cluster.checkHeldBinlogCopyStates()
}

// checkHeldBinlogCopyStates, every tick: WARN0258 for each server whose backup directory keeps
// binlog copies beyond backup-binlogs-keep for the binlog-only snapshot, WARN0259 for each
// server whose binlog continuity in the archive is lost.
func (cluster *Cluster) checkHeldBinlogCopyStates() {
	for _, server := range cluster.Servers {
		if server == nil {
			continue
		}
		q := &server.binlogCopies
		q.mu.Lock()
		held, oldest, since := q.held, q.heldOldest, q.heldSince
		q.mu.Unlock()
		if held > 0 {
			cluster.SetState("WARN0258", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0258"], server.URL, held, cluster.Conf.BackupBinlogsKeep, oldest, since.Format(time.RFC3339)), ErrFrom: "JOB", ServerUrl: server.URL})
		}
	}
	for _, g := range cluster.BinlogGaps() {
		cluster.SetState("WARN0259", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0259"], g.Server, g.From, g.To, g.Reason, g.Since.Format(time.RFC3339)), ErrFrom: "JOB", ServerUrl: g.Server})
	}
}
