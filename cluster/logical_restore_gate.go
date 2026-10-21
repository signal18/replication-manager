// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster/configurator"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// A restore with the binary log on keeps its binlogs bounded (#2124). A restore on a master (a child
// or staging cluster's master, at its creation or by staging-reseed-from-parent) writes the whole
// backup to its binlog for its replicas, and each replica writes it again to its own
// (log_slave_updates): the disks filled and MariaDB died (preprod belair-staging-test, 17 GB of
// binlog on db1 and 14 GB on db3 of a 20 GB plan). While it runs, on MariaDB 11.4+, the master and
// every replica keep max_binlog_total_size at 2G and MariaDB purges by itself (a replica, with no
// replica of its own, gets slave_connections_needed_for_purge 0, or it never purges); at the end
// the plan's value (prov-db-binlog-total-size-pct, dynamic config) or the one found comes back.
// The binlog monitoring sees each rotation (CheckBinaryLogs); the restore then pauses before its
// next statement until every replica applied the rotated binlog (its SQL thread is on the current
// one, Relay_Master_Log_File) and has no delay (Seconds_Behind_Master 0): MariaDB never purges a
// binlog a replica still reads. No timer, WARN0254 names the replica holding it. A stopped replica
// (IO or SQL thread) or a delayed one (hosts-delayed, MASTER_DELAY) does not take part. Before
// 11.4 there is no max_binlog_total_size: the gate purges the rotated binlogs itself.

// logicalRestoreGate is the pause of a logical restore's statement stream on a binlog rotation.
type logicalRestoreGate struct {
	mu      sync.Mutex
	open    chan struct{} // closed while the stream may flow
	reason  string        // why it is paused, "" while it flows
	handled string        // the last binlog it paused on: a rotation is handled once
}

// firstHandling records binlog as handled, false when it already was: the binlog checks run
// detached, two can see the same rotation, and the second paused the restore again.
func (g *logicalRestoreGate) firstHandling(binlog string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if binlog == "" || binlog == g.handled {
		return false
	}
	g.handled = binlog
	return true
}

func newLogicalRestoreGate() *logicalRestoreGate {
	g := &logicalRestoreGate{open: make(chan struct{})}
	close(g.open)
	return g
}

func (g *logicalRestoreGate) pause(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.open:
		g.open = make(chan struct{})
	default:
	}
	g.reason = reason
}

func (g *logicalRestoreGate) resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.open:
	default:
		close(g.open)
	}
	g.reason = ""
}

// wait blocks while the logical restore is paused.
func (g *logicalRestoreGate) wait() {
	g.mu.Lock()
	ch := g.open
	g.mu.Unlock()
	<-ch
}

func (g *logicalRestoreGate) pausedReason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reason
}

// logicalRestoreGateReader holds a logical restore's stream before each read while it is paused on a binlog rotation.
type logicalRestoreGateReader struct {
	r    io.Reader
	gate *logicalRestoreGate
}

func (g *logicalRestoreGateReader) Read(p []byte) (int, error) {
	g.gate.wait()
	return g.r.Read(p)
}

// waitLogicalRestoreGate blocks the logical restore's next statement while it is paused on a binlog
// rotation; nothing when none runs (a restore with the binary log off).
func (server *ServerMonitor) waitLogicalRestoreGate() {
	if g := server.logicalRestoreGate.Load(); g != nil {
		g.wait()
	}
}

// startLogicalRestoreGate starts the rotation pause of a logical restore writing to server's binlog (sql_log_bin=1)
// on MariaDB and holds the binlogs of server and its replicas at 2G; stop ends both and lets the stream flow.
func (server *ServerMonitor) startLogicalRestoreGate() (stop func()) {
	cluster := server.ClusterGroup
	if server.BinaryLogFile == "" {
		return func() {}
	}
	// every flavor: semi-sync paused (MariaDB, MySQL, Percona); the gate and the binlog hold
	// are MariaDB's
	semiSync := server.pauseSemiSyncForRestore()
	if !server.IsMariaDB() {
		return semiSync
	}
	gate := newLogicalRestoreGate()
	server.logicalRestoreGate.Store(gate)
	held := server.holdBinlogTotalSizeForRestore()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Restore on %s with the binary log on: binlogs kept to 2G, the restore pauses on each rotation until the replicas apply it", server.URL)
	return func() {
		// the last transactions land after the last rotation: the restore is done only once
		// every replica taking part applied them (preprod belair-staging-test: db3 detached
		// 660 MB short, no rotation came after the last commits)
		server.waitReplicasAppliedAll(gate)
		server.logicalRestoreGate.Store(nil)
		gate.resume()
		held()
		semiSync()
	}
}

// waitReplicasAppliedAll waits, with no timer, for every replica of server taking part to reach
// server's position read once when the restore wrote its last transaction (WaitSyncToTarget,
// 30 s turns); WARN0254 names the replica it waits for. Not server's current position: a child or
// staging master keeps writing its parent's transactions, a moving target is never reached. An
// SQL error of the wait, or no progress of the replica's applied position for
// replicaNoProgressTurns turns, ends it for that replica, logged.
func (server *ServerMonitor) waitReplicasAppliedAll(gate *logicalRestoreGate) {
	cluster := server.ClusterGroup
	target, err := server.ReadReplicationTarget()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Restore on %s written: could not read its position to wait for the replicas: %s", server.URL, err)
		return
	}
	for _, sl := range cluster.GetSlaves() {
		lastApplied, still := "", 0
		for sl != nil && sl != server && sl.Conn != nil && replicaTakesPart(sl) {
			reached, err := sl.WaitSyncToTarget(target, 30)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Restore on %s written: stopped waiting for replica %s to reach %s: %s", server.URL, sl.URL, target, err)
				break
			}
			if reached {
				break
			}
			if applied := sl.AppliedPosition(); applied != lastApplied {
				lastApplied, still = applied, 0
			} else if still++; still >= replicaNoProgressTurns {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Restore on %s written: stopped waiting for replica %s, no progress (still at %s for %d turns of 30 s)", server.URL, sl.URL, applied, still)
				break
			}
			gate.pause(fmt.Sprintf("restore written, waiting for replica %s to reach %s", sl.URL, target))
			sl = cluster.GetServerFromURL(sl.URL) // a reload rebuilds the ServerMonitor
		}
	}
}

// replicaTakesPart is false for a stopped replica (IO or SQL thread) or a delayed one
// (hosts-delayed, MASTER_DELAY): a restore never waits for it.
func replicaTakesPart(sl *ServerMonitor) bool {
	return sl.SlaveStatus != nil && !sl.IsReplicationBroken() && !sl.IsInDelayedHost() && sl.SlaveStatus.SQLDelay.Int64 <= 0
}

// pauseSemiSyncForRestore switches semi-sync off on the master while the restore runs: every
// commit waited for a replica's ack (preprod belair-staging-test: 189189 commits, 3.3 ms each),
// and the gate already holds the restore until the replicas applied each binlog. The returned
// func switches it back on, only when it was on.
func (server *ServerMonitor) pauseSemiSyncForRestore() (putBack func()) {
	cluster := server.ClusterGroup
	if server.Conn == nil || !server.IsSemiSyncMaster() {
		return func() {}
	}
	if _, err := dbhelper.SetSemiSyncMasterEnabled(server.Conn, server.DBVersion, false); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
			"Restore on %s: could not pause semi-sync: %s", server.URL, err)
		return func() {}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Restore on %s: semi-sync paused while the restore runs", server.URL)
	return func() {
		if _, err := dbhelper.SetSemiSyncMasterEnabled(server.Conn, server.DBVersion, true); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Restore on %s ended: could not put back semi-sync: %s", server.URL, err)
			return
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Restore on %s ended: semi-sync back on", server.URL)
	}
}

// holdBinlogTotalSizeForRestore sets max_binlog_total_size to 2G on server and its replicas
// (MariaDB 11.4+), slave_connections_needed_for_purge to 0 on the replicas; the returned func puts
// back the plan's max_binlog_total_size under the dynamic config, the value found otherwise, and
// the slave_connections_needed_for_purge found.
func (server *ServerMonitor) holdBinlogTotalSizeForRestore() (putBack func()) {
	cluster := server.ClusterGroup
	type found struct {
		sv          *ServerMonitor
		totalSize   int
		connections int
	}
	var held []found
	for _, sv := range append([]*ServerMonitor{server}, cluster.GetSlaves()...) {
		if sv == nil || sv.Conn == nil || !sv.IsMariaDB() || !sv.DBVersion.GreaterEqual("11.4") {
			continue
		}
		f := found{sv: sv, connections: -1}
		if v, ok := sv.Variables.CheckAndGet("MAX_BINLOG_TOTAL_SIZE"); ok {
			f.totalSize, _ = strconv.Atoi(v)
		}
		if _, err := dbhelper.SetMaxBinlogTotalSize(sv.Conn, int(configurator.MinBinlogTotalSize)); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
				"Restore on %s: could not keep the binlogs of %s to 2G: %s", server.URL, sv.URL, err)
			continue
		}
		sv.restoreHoldsBinlogTotalSize.Store(true)
		if sv != server {
			// a replica has no replica of its own: with the default 1 MariaDB never purges it
			if v, ok := sv.Variables.CheckAndGet("SLAVE_CONNECTIONS_NEEDED_FOR_PURGE"); ok {
				if n, err := strconv.Atoi(v); err == nil && n > 0 {
					if _, err := dbhelper.SetSlaveConnectionsNeededForPurge(sv.Conn, 0); err == nil {
						f.connections = n
					}
				}
			}
		}
		held = append(held, f)
	}
	return func() {
		for _, f := range held {
			size := f.totalSize
			if cluster.Conf.ProvDBApplyDynamicConfig {
				size, _ = strconv.Atoi(cluster.Configurator.GetConfigMaxBinlogTotalSize())
			}
			if _, err := dbhelper.SetMaxBinlogTotalSize(f.sv.Conn, size); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
					"Restore on %s ended: could not put back max_binlog_total_size %d on %s: %s", server.URL, size, f.sv.URL, err)
			}
			if f.connections > 0 {
				if _, err := dbhelper.SetSlaveConnectionsNeededForPurge(f.sv.Conn, f.connections); err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlWarn,
						"Restore on %s ended: could not put back slave_connections_needed_for_purge %d on %s: %s", server.URL, f.connections, f.sv.URL, err)
				}
			}
			f.sv.restoreHoldsBinlogTotalSize.Store(false)
		}
	}
}

// pauseLogicalRestoreOnBinlogRotation is the binlog monitoring's call on a rotation of server's binlog: while
// a restore with the binary log on runs, it pauses it until the replicas apply the rotated binlog,
// then resumes. One handling at a time; a later rotation is caught up by it.
func (server *ServerMonitor) pauseLogicalRestoreOnBinlogRotation() {
	gate := server.logicalRestoreGate.Load()
	if gate == nil || !server.logicalRestoreGatePurging.CompareAndSwap(false, true) {
		return
	}
	if !gate.firstHandling(server.BinaryLogFile) {
		server.logicalRestoreGatePurging.Store(false)
		return
	}
	gate.pause(fmt.Sprintf("binlog rotated to %s, waiting for the replicas to apply it", server.BinaryLogFile))
	go func() {
		defer server.logicalRestoreGatePurging.Store(false)
		server.purgeBinlogsAppliedByReplicasThenResume(gate)
	}()
}

// purgeBinlogsAppliedByReplicasThenResume waits, with no timer, for every replica taking part to apply
// server's rotated binlog with no delay, and for binlog archiving to end, then resumes the restore,
// purging first the binlogs before the current one where MariaDB does not (before 11.4); nothing
// once the restore ended.
func (server *ServerMonitor) purgeBinlogsAppliedByReplicasThenResume(gate *logicalRestoreGate) {
	cluster := server.ClusterGroup
	for server.logicalRestoreGate.Load() == gate {
		current := server.BinaryLogFile
		holding := server.logicalRestoreGateHolder(current)
		if holding == "" && server.IsBackingUpBinaryLog {
			holding = "binlog archiving running"
		}
		if holding == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Restore on %s: the replicas applied up to %s, resuming", server.URL, current)
			// MariaDB 11.4+ purges by itself within max_binlog_total_size; before, purge here,
			// a replica too: it writes what it applies to its own binlog (log_slave_updates)
			for _, sv := range append([]*ServerMonitor{server}, cluster.GetSlaves()...) {
				if sv != nil && sv.BinaryLogFile != "" && !sv.restoreHoldsBinlogTotalSize.Load() && !sv.IsBackingUpBinaryLog {
					sv.WaitForRefresh() // PurgeBinlogTo purges only a binlog it lists
					sv.PurgeBinlogTo(sv.BinaryLogFile)
					sv.RefreshBinaryLogs()
				}
			}
			gate.resume()
			return
		}
		gate.pause(fmt.Sprintf("binlog rotated to %s, %s", current, holding))
		time.Sleep(time.Second)
	}
}

// logicalRestoreGateHolder is why a replica keeps server's restore paused on the binlog current,
// "" when none does: every replica taking part applied the binlogs before it (its SQL thread is on
// current) and has no delay (Seconds_Behind_Master 0). A stopped replica (IO or SQL thread) or a delayed one does not take part.
func (server *ServerMonitor) logicalRestoreGateHolder(current string) string {
	for _, sl := range server.ClusterGroup.GetSlaves() {
		if sl == nil || sl == server || !replicaTakesPart(sl) {
			continue
		}
		if applying := sl.SlaveStatus.RelayMasterLogFile.String; applying != current {
			return fmt.Sprintf("waiting for replica %s to apply it (applying %s)", sl.URL, applying)
		}
		if delay := sl.SlaveStatus.SecondsBehindMaster; !delay.Valid || delay.Int64 > 0 {
			return fmt.Sprintf("waiting for replica %s to have no delay (Seconds_Behind_Master %d)", sl.URL, delay.Int64)
		}
	}
	return ""
}

// checkLogicalRestoreGate raises WARN0254 tick after tick while server's restore is paused.
func (server *ServerMonitor) checkLogicalRestoreGate() {
	g := server.logicalRestoreGate.Load()
	if g == nil {
		return
	}
	if reason := g.pausedReason(); reason != "" {
		cluster := server.ClusterGroup
		cluster.SetState("WARN0254", state.State{ErrType: "WARNING", ErrFrom: "JOB", ServerUrl: server.URL,
			ErrDesc: fmt.Sprintf(clusterError["WARN0254"], server.URL, reason)})
	}
}
