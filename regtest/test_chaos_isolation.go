// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestSetMinority — a repman is in split brain because its infrastructure
// lost connection to BOTH its peer AND the arbitrator (DC3, which never
// fails): it is the minority node (1 of 3). The majority (peer + arbitrator)
// holds the truth about who the master is, so the master this node sees is
// NOT authoritative. A minority node must do nothing: never flip
// active/passive, never fail over.
//
// The virtual split brain is set at both levels at once — server: peer
// heartbeat check failed (IsSplitBrain); cluster: cannot join the arbitrator
// (arbitrator link cut). Two scenarios by where the master sits:
//   - setMinorityWithMaster: master on the minority side, reachable.
//   - setMinorityLostMaster: master on the majority side, unreachable.
//
// Requires arbitration configured; otherwise skipped.
func (regtest *RegTest) TestSetMinority(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled on this cluster")
		return true
	}

	status0 := cl.Status
	failoverCtr := cl.FailoverCtr

	if !minorityDoesNothing(cl, status0, failoverCtr, setMinorityWithMaster, "setMinorityWithMaster") {
		return false
	}
	if !minorityDoesNothing(cl, status0, failoverCtr, setMinorityLostMaster, "setMinorityLostMaster") {
		return false
	}

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Minority node never acted in either scenario: status held, no failover")
	return true
}

const minorityHold = 120 * time.Second

// setMinorityWithMaster puts the node in the minority with its master still
// reachable (master on the minority side): peer heartbeat failed + arbitrator
// unreachable, database left up.
func setMinorityWithMaster(cl *cluster.Cluster) {
	cl.IsSplitBrain = true
	cl.ChaosCutArbitrator(minorityHold)
}

// setMinorityLostMaster puts the node in the minority with its master gone
// (master on the majority side): peer heartbeat failed + arbitrator + master
// all unreachable.
func setMinorityLostMaster(cl *cluster.Cluster) {
	cl.IsSplitBrain = true
	cl.ChaosCutArbitrator(minorityHold)
	cl.ChaosCutDB(minorityHold)
}

// minorityDoesNothing applies a minority setup, holds it, and asserts the
// node never moves active/passive and never fails over. Always restores.
func minorityDoesNothing(cl *cluster.Cluster, status0 string, failoverCtr int, setup func(*cluster.Cluster), label string) bool {
	setup(cl)
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
