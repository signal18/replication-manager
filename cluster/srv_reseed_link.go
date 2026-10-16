// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"slices"
	"strings"

	"github.com/signal18/replication-manager/utils/state"
)

// A reseed and the replica's replication links (law T27, #2093). A restore replaces the
// data every channel applies to, so the reseed stops them all; it never deletes one
// (RESET SLAVE without ALL clears positions only) and never moves one to another source
// (the failover, switchover and rejoin's job). The channels it stopped are recorded and
// WARN0252 stays open, tick after tick, until each runs again: the GUI tells why a link
// is down instead of a log line.

// markReseedStoppedLinks records the channels the reseed stops, each with the reason.
func (server *ServerMonitor) markReseedStoppedLinks(channels []string, reason string) {
	server.reseedStoppedLinks.Store(append([]string(nil), channels...))
	server.reseedStoppedLinkReason.Store(reason)
}

// reseedStoppedLinkList is the channels a reseed stopped and are not known to run again.
func (server *ServerMonitor) reseedStoppedLinkList() []string {
	if l, ok := server.reseedStoppedLinks.Load().([]string); ok {
		return l
	}
	return nil
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
// stopped leaves the list once it runs again (whoever started it), WARN0252 while any
// remains.
func (server *ServerMonitor) checkReseedStoppedLinks() {
	list := server.reseedStoppedLinkList()
	if len(list) == 0 {
		return
	}
	still := make([]string, 0, len(list))
	for _, channel := range list {
		running := false
		for _, r := range server.Replications {
			if r.ConnectionName.String == channel {
				running = r.SlaveIORunning.String == "Yes" && r.SlaveSQLRunning.String == "Yes"
				break
			}
		}
		if !running {
			still = append(still, channel)
		}
	}
	if len(still) != len(list) {
		server.reseedStoppedLinks.Store(still)
	}
	if len(still) == 0 {
		return
	}
	cluster := server.ClusterGroup
	reason, _ := server.reseedStoppedLinkReason.Load().(string)
	names := make([]string, 0, len(still))
	for _, c := range still {
		if c == "" {
			c = "default"
		}
		names = append(names, "'"+c+"'")
	}
	cluster.SetState("WARN0252", state.State{ErrType: "WARNING", ErrFrom: "TOPO", ServerUrl: server.URL,
		ErrDesc: fmt.Sprintf(clusterError["WARN0252"], server.URL, strings.Join(names, ", "), reason)})
}

// reseedLinkPointsToPrimary reports whether the reseed may position the managed channel:
// it points to the cluster's primary, or the replica has no such channel yet (a new
// replica, no link of the client's to keep). A channel pointing elsewhere is left as it
// is: moving it is the rejoin's job.
func (server *ServerMonitor) reseedLinkPointsToPrimary() (bool, string) {
	cluster := server.ClusterGroup
	if cluster.master == nil {
		return false, "no primary known"
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
