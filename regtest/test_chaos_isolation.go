// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestChaosIsolationArbitration — minority test.
//
// A repman is in split brain because its infrastructure lost connection to
// BOTH its peer AND the arbitrator: it is the minority node (1 of 3). The
// majority (peer + arbitrator) holds the truth about who the master is, so
// the master this node sees is NOT authoritative. A minority node must do
// nothing: never flip active/passive, never fail over — whatever its local
// master appears to be.
//
// A virtual split brain is set up at both levels at once:
//   - server level : the peer heartbeat check has failed (IsSplitBrain, as
//     the server heartbeat would push down to the cluster).
//   - cluster level: the cluster can no longer join the arbitrator (its
//     arbitrator link is cut).
//
// The database link is cut in the second case (the local master appears
// failed) — the node must still not act, because in the minority its view is
// not the truth. Requires arbitration configured; otherwise skipped.
func (regtest *RegTest) TestChaosIsolationArbitration(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled on this cluster")
		return true
	}

	status0 := cl.Status
	failoverCtr := cl.FailoverCtr

	// Case 1 — local master appears OK.
	if !chaosMinorityNoAction(cl, status0, failoverCtr, false, "case1 (local master ok)") {
		return false
	}
	// Case 2 — local master appears FAILED (db link cut too).
	if !chaosMinorityNoAction(cl, status0, failoverCtr, true, "case2 (local master failed)") {
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Minority node never acted: status held, no failover, in both master states")
	return true
}

// chaosMinorityNoAction puts the cluster in the minority condition — server
// peer check failed (IsSplitBrain) and the cluster can't join the arbitrator
// (arbitrator cut), optionally with the local master also unreachable — and
// asserts it never moves active/passive and never fails over. Always restores.
func chaosMinorityNoAction(cl *cluster.Cluster, status0 string, failoverCtr int, cutMaster bool, label string) bool {
	cl.IsSplitBrain = true                   // server level: peer heartbeat check failed
	cl.ChaosCutArbitrator(120 * time.Second) // cluster level: cannot join the arbitrator
	if cutMaster {
		cl.ChaosCutDB(120 * time.Second)
	}
	defer func() {
		cl.IsSplitBrain = false
		cl.ChaosStop()
	}()

	for i := 0; i < 20; i++ {
		time.Sleep(3 * time.Second)
		if cl.Status != status0 {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: minority node FLIPPED active/passive %s -> %s", label, status0, cl.Status)
			return false
		}
		if cl.FailoverCtr != failoverCtr {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: minority node FAILED OVER on a non-authoritative master view", label)
			return false
		}
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: status held at %s, no failover — minority correctly did nothing", label, status0)
	return true
}
