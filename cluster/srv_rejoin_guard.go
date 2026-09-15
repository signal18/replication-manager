// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "fmt"

// rejoinWouldDemoteMaster reports whether a rejoin of server would re-slave the
// cluster's CURRENT master on stale information: server is the master and no crash
// record newer than its promotion (MasterChangeTs) names it as the loser. Such a
// rejoin must be skipped -- it is how the 2026-09-14 belair replication ring was
// built (#1793). A crash newer than the promotion is a genuine later event (e.g. the
// split-brain colocated old master whose pointer was just re-designated) and passes.
func (cluster *Cluster) rejoinWouldDemoteMaster(server *ServerMonitor) bool {
	if server == nil {
		return false
	}
	m := cluster.GetMaster()
	if m == nil || m.URL != server.URL {
		return false
	}
	cr := cluster.getCrashFromJoiner(server.URL)
	return cr == nil || cr.UnixTimestamp <= cluster.MasterChangeTs
}

// peerCrashStaleReason returns why a crash entry fetched from the arbitration peer
// must NOT be materialized, or "" when it is a usable verdict. During a LIVE split
// brain (IsSplitBrain) the caller already anchors on SplitBrainStartTs; outside one
// this anchors on the last master change of this cluster (MasterChangeTs) and on the
// live master itself. SplitBrainStartTs is sticky (never reset once a split resolves)
// so it must not be used to decide whether a split is in progress.
func (cluster *Cluster) peerCrashStaleReason(last *Crash) string {
	if last == nil {
		return "no entry"
	}
	if cluster.IsSplitBrain {
		return "" // live split: the split-brain guard (SplitBrainStartTs) applies instead
	}
	if cluster.MasterChangeTs > 0 && last.UnixTimestamp < cluster.MasterChangeTs {
		return fmt.Sprintf("predates the current master change (%d)", cluster.MasterChangeTs)
	}
	if m := cluster.GetMaster(); m != nil && last.URL == m.URL {
		return "names the live master as the loser outside a split brain"
	}
	return ""
}
