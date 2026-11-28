// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/go-sql-driver/mysql"
)

// A reseed and the replica's replication links (law T27, #2093). A restore replaces the
// data every channel applies to, so the reseed stops them all; it never deletes one
// (RESET SLAVE without ALL clears positions only) and never moves one to another source
// (the failover, switchover and rejoin's job). The channels it stopped are recorded and
// WARN0252 stays open, tick after tick, until each runs again: the GUI tells why a link
// is down instead of a log line.

// markReseedStoppedLinks records the channels the reseed stops, with the reason: they replace
// the reseed's previous record (WARN0252), a channel's other change is replaced by this one.
func (server *ServerMonitor) markReseedStoppedLinks(channels []string, reason string) {
	server.linkChangesMu.Lock()
	defer server.linkChangesMu.Unlock()
	kept := server.linkChanges[:0:0]
	for _, c := range server.linkChanges {
		if c.key != "WARN0252" && !slices.Contains(channels, c.channel) {
			kept = append(kept, c)
		}
	}
	now := time.Now()
	for _, ch := range channels {
		kept = append(kept, linkChange{channel: ch, op: linkOpStop, reason: reason, key: "WARN0252", at: now})
	}
	server.linkChanges = kept
}

// noReplicationChannel is error 1617 (no master connection by that name): the server has no
// such channel, a standalone server or a staging server detached by the refresh; nothing to stop
// or reset, not a failure of the reseed.
func noReplicationChannel(err error) bool {
	var e *mysql.MySQLError
	return errors.As(err, &e) && e.Number == 1617
}

// isReplicaMultipleChannelsError is error 3079 (ER_REPLICA_MULTIPLE_CHANNELS_CMD, ER_SLAVE_MULTIPLE_CHANNELS_CMD
// before 8.0.22): a statement without a channel name on a MySQL/Percona replica that has several
// channels. The statement is refused and changes nothing.
func isReplicaMultipleChannelsError(err error) bool {
	var e *mysql.MySQLError
	return errors.As(err, &e) && e.Number == 3079
}

// reseedStoppedLinkList is the channels a reseed stopped and are not known to run again.
func (server *ServerMonitor) reseedStoppedLinkList() []string {
	server.linkChangesMu.Lock()
	defer server.linkChangesMu.Unlock()
	var out []string
	for _, c := range server.linkChanges {
		if c.key == "WARN0252" {
			out = append(out, c.channel)
		}
	}
	return out
}

// replicationChannels lists the replica's channel names from its last status (the
// unnamed default channel is "").
func (server *ServerMonitor) replicationChannels() []string {
	out := make([]string, 0, len(server.NativeReplications()))
	for _, r := range server.NativeReplications().Rows() {
		out = append(out, r.ConnectionName.String)
	}
	return out
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
	for _, r := range server.NativeReplications().Rows() {
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
