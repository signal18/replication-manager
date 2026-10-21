// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// A restore with the binary log on keeps one binlog (#2124). A restore on a master (a child or
// staging cluster's master, at its creation or by staging-reseed-from-parent) writes the whole
// backup to its binlog for its replicas: the binlog of a former seed still there, the disk
// filled and MariaDB crashed (preprod belair-staging-test, 15 GB of binlog on a 20 GB disk).
// The binlog retention policy gives way while it runs: on each rotation of the master's
// binlog the restore pauses before its next statement, waits for every replica to have
// applied the master's position, purges the rotated binlogs (the replicas' own too), then goes
// on. No timer: a replica that does not replicate keeps it paused, WARN0254 saying so; the
// binlogs are never purged before a replica applied them.

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
// on MariaDB; stop ends it and lets the stream flow. Without a binlog nothing is started.
func (server *ServerMonitor) startRestoreBinlogGate() (stop func()) {
	cluster := server.ClusterGroup
	if !server.IsMariaDB() {
		return func() {}
	}
	ms, _, err := dbhelper.GetMasterStatus(server.Conn, server.DBVersion)
	if err != nil || ms.File == "" {
		return func() {}
	}
	gate := newRestoreBinlogGate()
	server.restoreGate.Store(gate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.runRestoreBinlogGate(ctx, gate, ms.File)
	}()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
		"Restore on %s with the binary log on: one binlog kept, the restore pauses on each rotation until the replicas applied it", server.URL)
	return func() {
		cancel()
		<-done
		gate.resume()
		server.restoreGate.Store(nil)
	}
}

// runRestoreBinlogGate watches server's binlog file once a second; on a rotation it pauses the
// restore, waits for the replicas, purges the rotated binlogs and resumes.
func (server *ServerMonitor) runRestoreBinlogGate(ctx context.Context, gate *restoreBinlogGate, current string) {
	cluster := server.ClusterGroup
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
		ms, _, err := dbhelper.GetMasterStatus(server.Conn, server.DBVersion)
		if err != nil || ms.File == "" || ms.File == current {
			continue
		}
		gate.pause(fmt.Sprintf("binlog rotated to %s, waiting for the replicas to apply it", ms.File))
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Restore on %s paused: binlog rotated to %s, waiting for the replicas", server.URL, ms.File)
		if !server.waitRestoreReplicasApplied(ctx, gate) {
			return
		}
		if logs, err := dbhelper.PurgeBinlogTo(server.Conn, ms.File); err != nil {
			cluster.LogSQL(logs, err, server.URL, "Restore", config.LvlWarn, "Restore on %s: purge of the binlogs before %s: %s", server.URL, ms.File, err)
		}
		for _, url := range server.restoreReplicaURLs() {
			// a replica writes what it applies to its own binlog (log_slave_updates): one kept too
			if replica := cluster.GetServerFromURL(url); replica != nil && replica.Conn != nil {
				if rms, _, err := dbhelper.GetMasterStatus(replica.Conn, replica.DBVersion); err == nil && rms.File != "" {
					if logs, err := dbhelper.PurgeBinlogTo(replica.Conn, rms.File); err != nil {
						cluster.LogSQL(logs, err, replica.URL, "Restore", config.LvlWarn, "Restore on %s: purge of the binlogs of replica %s before %s: %s", server.URL, replica.URL, rms.File, err)
					}
				}
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo,
			"Restore on %s resumed: the replicas applied the binlogs before %s, purged", server.URL, ms.File)
		current = ms.File
		gate.resume()
	}
}

// waitRestoreReplicasApplied waits, with no timer, for every replica of server to have applied
// server's binlog position; false when the restore ended meanwhile.
func (server *ServerMonitor) waitRestoreReplicasApplied(ctx context.Context, gate *restoreBinlogGate) bool {
	cluster := server.ClusterGroup
	pos, _, err := dbhelper.GetVariableByName(server.Conn, "GTID_BINLOG_POS", server.DBVersion)
	if err != nil || pos == "" {
		return true
	}
	for _, url := range server.restoreReplicaURLs() {
		for {
			select {
			case <-ctx.Done():
				return false
			default:
			}
			// read by URL each time: a configuration reload rebuilds the ServerMonitor
			replica := cluster.GetServerFromURL(url)
			if replica == nil || replica.Conn == nil {
				break // no longer a replica of this cluster: nothing to wait for
			}
			applied, _, err := dbhelper.MasterWaitGTIDApplied(replica.Conn, pos, 5)
			if err == nil && applied {
				break
			}
			if err != nil {
				time.Sleep(time.Second) // the wait returned at once: not a busy loop
			}
			gate.pause(fmt.Sprintf("waiting for replica %s to apply %s before the rotated binlog is purged", url, pos))
		}
	}
	return true
}

// restoreReplicaURLs are the replicas of server in its cluster: the servers whose replication
// source is server (a detached staging server replicates from nothing).
func (server *ServerMonitor) restoreReplicaURLs() []string {
	var urls []string
	for _, s := range server.ClusterGroup.GetSlaves() {
		if s == nil || s.URL == server.URL {
			continue
		}
		host := s.GetReplicationMasterHost()
		if (host == server.Host || host == server.IP) && s.GetReplicationMasterPort() == server.Port {
			urls = append(urls, s.URL)
		}
	}
	return urls
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
