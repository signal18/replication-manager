// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

// A reseed and the replica's replication links (law T27, #2093). A restore replaces the
// data every channel applies to, so the reseed stops them all; it never deletes one
// (RESET SLAVE without ALL clears positions only) and never moves one to another source
// (the failover, switchover and rejoin's job). The channels it stopped are recorded and
// WARN0252 stays open, tick after tick, until each runs again: the GUI tells why a link
// is down instead of a log line.

// reseedStoppedLinks is what WARN0252 reports: the channels a reseed stopped, why, and
// when it recorded them.
type reseedStoppedLinks struct {
	channels []string
	reason   string
	at       time.Time
}

// markReseedStoppedLinks records the channels the reseed stops, with the reason.
func (server *ServerMonitor) markReseedStoppedLinks(channels []string, reason string) {
	server.reseedLinksMu.Lock()
	defer server.reseedLinksMu.Unlock()
	server.reseedLinks = reseedStoppedLinks{channels: append([]string(nil), channels...), reason: reason, at: time.Now()}
}

// reseedStoppedLinkList is the channels a reseed stopped and are not known to run again.
func (server *ServerMonitor) reseedStoppedLinkList() []string {
	server.reseedLinksMu.Lock()
	defer server.reseedLinksMu.Unlock()
	return append([]string(nil), server.reseedLinks.channels...)
}

// replicationChannels lists the replica's channel names from its last status (the
// unnamed default channel is "").
func (server *ServerMonitor) replicationChannels() []string {
	out := make([]string, 0, len(server.Replications))
	for _, r := range server.Replications {
		out = append(out, r.ConnectionName.String)
	}
	return out
}

// checkReseedStoppedLinks runs with the server refresh, every tick: a channel the reseed
// stopped leaves the list once it runs again (whoever started it) or once it is gone from
// the replica's status while the server answers (removed by the operator: nothing to wait
// for, F3/F4), WARN0252 while any remains. A server that does not answer keeps the list.
func (server *ServerMonitor) checkReseedStoppedLinks() {
	cluster := server.ClusterGroup
	// Under the lock end to end: a reseed recording between the read and the write would
	// otherwise be overwritten (#2093 review). Within two ticks of a recording a running
	// status may predate the STOP: the channel stays.
	server.reseedLinksMu.Lock()
	links := server.reseedLinks
	if len(links.channels) == 0 {
		server.reseedLinksMu.Unlock()
		return
	}
	fresh := time.Since(links.at) < 2*time.Duration(cluster.Conf.MonitoringTicker)*time.Second
	reachable := server.Conn != nil && !server.IsDown()
	still := make([]string, 0, len(links.channels))
	for _, channel := range links.channels {
		present, running := false, false
		for _, r := range server.Replications {
			if r.ConnectionName.String == channel {
				present = true
				running = r.SlaveIORunning.String == "Yes" && r.SlaveSQLRunning.String == "Yes"
				break
			}
		}
		if !fresh && (running || (reachable && !present)) {
			continue
		}
		still = append(still, channel)
	}
	server.reseedLinks.channels = still
	server.reseedLinksMu.Unlock()
	if len(still) == 0 {
		return
	}
	names := make([]string, 0, len(still))
	for _, c := range still {
		if c == "" {
			c = "default"
		}
		names = append(names, "'"+c+"'")
	}
	cluster.SetState("WARN0252", state.State{ErrType: "WARNING", ErrFrom: "TOPO", ServerUrl: server.URL,
		ErrDesc: fmt.Sprintf(clusterError["WARN0252"], server.URL, strings.Join(names, ", "), links.reason)})
}

// reseedLinkPointsToPrimary reports whether the reseed may position the managed channel:
// it points to the cluster's primary, or the replica has no such channel yet (a new
// replica, no link of the client's to keep). A channel pointing elsewhere is left as it
// is: moving it is the rejoin's job.
func (server *ServerMonitor) reseedLinkPointsToPrimary() (bool, string) {
	cluster := server.ClusterGroup
	if cluster.master == nil {
		return false, "no primary known: start replication once a primary is elected"
	}
	for _, r := range server.Replications {
		if r.ConnectionName.String != server.ReplicationSourceName {
			continue
		}
		host, port := r.MasterHost.String, r.MasterPort.String
		if host == "" {
			return true, ""
		}
		if slices.Contains([]string{cluster.master.Host, cluster.master.IP}, host) && (port == "" || port == cluster.master.Port) {
			return true, ""
		}
		return false, fmt.Sprintf("the link points to %s:%s, not to the primary %s", host, port, cluster.master.URL)
	}
	return true, ""
}
