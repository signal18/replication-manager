// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// A restore with the binary log on keeps one binlog (#2124). A restore on a master (a child or
// staging cluster's master, at its creation or by staging-reseed-from-parent) writes the whole
// backup to its binlog for its replicas: the binlog of a former seed still there, the disk
// filled and MariaDB crashed (preprod belair-staging-test, 15 GB of binlog on a 20 GB disk).
// The binlog retention policy gives way while it runs. The binlog monitoring sees each rotation
// (CheckBinaryLogs); the restore then pauses before its next statement until every replica read
// the rotated binlog (SlavesOldestMasterFile, the purge's own measure), the rotated binlogs are
// purged (PurgeBinlogTo, after binlog archiving: IsBackingUpBinaryLog), the replicas' own too,
// and it goes on. No timer: a replica that does not replicate keeps it paused, WARN0254 saying
// so; a binlog is never purged before every replica read it.

// restoreBinlogGate is the pause of a restore's statement stream.
type restoreBinlogGate struct {
	mu     sync.Mutex
	open   chan struct{} // closed while the stream may flow
	reason string        // why it is paused, "" while it flows
}

func newRestoreBinlogGate() *restoreBinlogGate {
	g := &restoreBinlogGate{open: make(chan struct{})}
	close(g.open)
	return g
}

func (g *restoreBinlogGate) pause(reason string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.open:
		g.open = make(chan struct{})
	default:
	}
	g.reason = reason
}

func (g *restoreBinlogGate) resume() {
	g.mu.Lock()
	defer g.mu.Unlock()
	select {
	case <-g.open:
	default:
		close(g.open)
	}
	g.reason = ""
}

// wait blocks while the gate is paused.
func (g *restoreBinlogGate) wait() {
	g.mu.Lock()
	ch := g.open
	g.mu.Unlock()
	<-ch
}

func (g *restoreBinlogGate) pausedReason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reason
}

// gatedReader pauses a restore stream at the gate before each read.
type gatedReader struct {
	r    io.Reader
	gate *restoreBinlogGate
}

func (g *gatedReader) Read(p []byte) (int, error) {
	g.gate.wait()
	return g.r.Read(p)
}

// restoreGateWait blocks the restore's next statement while its gate is paused; nothing when no
// gate runs (a restore with the binary log off).
func (server *ServerMonitor) restoreGateWait() {
	if g := server.restoreGate.Load(); g != nil {
		g.wait()
	}
}

// startRestoreBinlogGate starts the gate of a restore writing to server's binlog (sql_log_bin=1)
// on MariaDB; stop ends it and lets the stream flow.
func (server *ServerMonitor) startRestoreBinlogGate() (stop func()) {
	cluster := server.ClusterGroup
	if !server.IsMariaDB() || server.BinaryLogFile == "" {
		return func() {}
	}
	gate := newRestoreBinlogGate()
	server.restoreGate.Store(gate)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Restore on %s with the binary log on: one binlog kept, the restore pauses on each rotation until the replicas read it", server.URL)
	return func() {
		server.restoreGate.Store(nil)
		gate.resume()
	}
}

// onRestoreBinlogRotation is the binlog monitoring's call on a rotation of server's binlog: while
// a restore with the binary log on runs, it pauses it until the replicas read the rotated binlog,
// purges it, and resumes. One handling at a time; a later rotation is caught up by it.
func (server *ServerMonitor) onRestoreBinlogRotation() {
	gate := server.restoreGate.Load()
	if gate == nil || !server.restoreRotating.CompareAndSwap(false, true) {
		return
	}
	gate.pause(fmt.Sprintf("binlog rotated to %s, waiting for the replicas to read it", server.BinaryLogFile))
	go func() {
		defer server.restoreRotating.Store(false)
		server.purgeRestoreRotatedBinlogs(gate)
	}()
}

// purgeRestoreRotatedBinlogs waits, with no timer, for every replica to read server's current
// binlog and for binlog archiving to end, then purges the binlogs before it, the replicas' own,
// and resumes the restore; nothing once the restore ended.
func (server *ServerMonitor) purgeRestoreRotatedBinlogs(gate *restoreBinlogGate) {
	cluster := server.ClusterGroup
	for server.restoreGate.Load() == gate {
		cluster.refreshSlavesOldestMasterFile()
		current := server.BinaryLogFile
		oldest := fmt.Sprintf("%s.%06d", cluster.SlavesOldestMasterFile.Prefix, cluster.SlavesOldestMasterFile.Suffix)
		if oldest == current && !server.IsBackingUpBinaryLog {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
				"Restore on %s: the replicas read up to %s, purging the binlogs before it", server.URL, current)
			server.PurgeBinlogTo(current)
			server.RefreshBinaryLogs()
			for _, sl := range cluster.GetSlaves() {
				// a replica writes what it applies to its own binlog (log_slave_updates)
				if sl != nil && sl.BinaryLogFile != "" && !sl.IsBackingUpBinaryLog {
					sl.PurgeBinlogTo(sl.BinaryLogFile)
					sl.RefreshBinaryLogs()
				}
			}
			gate.resume()
			return
		}
		gate.pause(fmt.Sprintf("binlog rotated to %s, waiting for the replicas to read it (oldest read %s)", current, oldest))
		time.Sleep(time.Second)
	}
}

// checkRestoreBinlogPause raises WARN0254 tick after tick while server's restore is paused.
func (server *ServerMonitor) checkRestoreBinlogPause() {
	g := server.restoreGate.Load()
	if g == nil {
		return
	}
	if reason := g.pausedReason(); reason != "" {
		cluster := server.ClusterGroup
		cluster.SetState("WARN0254", state.State{ErrType: "WARNING", ErrFrom: "JOB", ServerUrl: server.URL,
			ErrDesc: fmt.Sprintf(clusterError["WARN0254"], server.URL, reason)})
	}
}
