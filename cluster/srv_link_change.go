// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// The changes repman made to a server's replication links (law T27): every stop, reset of
// positions or repoint through a Replication is recorded with the reason the caller gave,
// and stays a state, tick after tick, until the link runs again: the GUI tells why a link is
// down instead of a log line. A reseed's stops are WARN0252 (their own text, srv_reseed_link.go),
// every other change WARN0352. A Start through the link that succeeds drops the record at once.

// linkChange is one recorded change: the channel ("" the default one, "cdc:<name>" a feed), the
// action (linkOp*), the reason, the state that reports it, and when.
type linkChange struct {
	channel string
	op      string
	reason  string
	key     string
	at      time.Time
}

// recordLinkChange records a change of the channel, replacing its previous one; a reseed's
// record (WARN0252) stays: the reseed's own steps (its repoint, its reset) do not hide why the
// link is down, only a start or a removal ends it.
func (server *ServerMonitor) recordLinkChange(channel, op, reason string) {
	server.linkChangesMu.Lock()
	defer server.linkChangesMu.Unlock()
	kept := server.linkChanges[:0:0]
	for _, c := range server.linkChanges {
		if c.channel == channel && c.key == "WARN0252" {
			return
		}
		if c.channel != channel {
			kept = append(kept, c)
		}
	}
	server.linkChanges = append(kept, linkChange{channel: channel, op: op, reason: reason, key: "WARN0352", at: time.Now()})
}

// clearLinkChange drops the channel's record: it was started again.
func (server *ServerMonitor) clearLinkChange(channel string) {
	server.linkChangesMu.Lock()
	defer server.linkChangesMu.Unlock()
	kept := server.linkChanges[:0:0]
	for _, c := range server.linkChanges {
		if c.channel != channel {
			kept = append(kept, c)
		}
	}
	server.linkChanges = kept
}

// linkChangeList is the recorded changes, a copy.
func (server *ServerMonitor) linkChangeList() []linkChange {
	server.linkChangesMu.Lock()
	defer server.linkChangesMu.Unlock()
	return append([]linkChange(nil), server.linkChanges...)
}

// linkRow is the channel's row in the server's last status, native or cdc.
func (server *ServerMonitor) linkRow(channel string) (dbhelper.SlaveStatus, bool) {
	if r := server.Replications.Named(channel); r != nil {
		return r.Row(), true
	}
	return dbhelper.SlaveStatus{}, false
}

// checkLinkChanges runs with the server refresh, every tick: a recorded channel leaves the
// list once it runs again (whoever started it) or once it is gone from the server's status
// while the server answers (removed by the operator: nothing to wait for, F3/F4); the states
// stay while any remains. A server that does not answer keeps the list.
func (server *ServerMonitor) checkLinkChanges() {
	cluster := server.ClusterGroup
	// Under the lock end to end: a record between the read and the write would otherwise be
	// overwritten (#2093 review). Within two ticks of a record a running status may predate the
	// STOP: the channel stays.
	server.linkChangesMu.Lock()
	if len(server.linkChanges) == 0 {
		server.linkChangesMu.Unlock()
		return
	}
	reachable := server.Conn != nil && !server.IsDown()
	still := server.linkChanges[:0:0]
	for _, c := range server.linkChanges {
		fresh := time.Since(c.at) < 2*time.Duration(cluster.Conf.MonitoringTicker)*time.Second
		r, present := server.linkRow(c.channel)
		running := present && r.SlaveIORunning.String == "Yes" && r.SlaveSQLRunning.String == "Yes"
		if !fresh && (running || (reachable && !present)) {
			continue
		}
		still = append(still, c)
	}
	server.linkChanges = still
	server.linkChangesMu.Unlock()

	var reseed, other []string
	reseedReason := ""
	for _, c := range still {
		name := c.channel
		if name == "" {
			name = "default"
		}
		if c.key == "WARN0252" {
			reseed = append(reseed, "'"+name+"'")
			reseedReason = c.reason
			continue
		}
		other = append(other, fmt.Sprintf("'%s' %s by %s", name, c.op, c.reason))
	}
	if len(reseed) > 0 {
		cluster.SetState("WARN0252", state.State{ErrType: "WARNING", ErrFrom: "TOPO", ServerUrl: server.URL,
			ErrDesc: fmt.Sprintf(clusterError["WARN0252"], server.URL, strings.Join(reseed, ", "), reseedReason)})
	}
	if len(other) > 0 {
		cluster.SetState("WARN0352", state.State{ErrType: "WARNING", ErrFrom: "TOPO", ServerUrl: server.URL,
			ErrDesc: fmt.Sprintf(clusterError["WARN0352"], server.URL, strings.Join(other, ", "))})
	}
}
