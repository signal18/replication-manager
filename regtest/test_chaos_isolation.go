// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// Minority tests. A repman is in split brain because its infrastructure lost
// connection to BOTH its peer AND the arbitrator (DC3, which never fails): it
// is the minority node (1 of 3). The majority (peer + arbitrator) holds the
// truth about who the master is, so the master this node sees is NOT
// authoritative. A minority node must do nothing: never flip active/passive,
// never fail over.
//
// The virtual split brain is set at both levels at once — server: peer
// heartbeat check failed (IsSplitBrain); cluster: cannot join the arbitrator
// (arbitrator link cut). The two tests differ only by where the master sits.

const minorityHold = 120 * time.Second

// TestSetMinorityWithMaster — master on the minority side, reachable. The
// node sees a live master but its view is not authoritative; it must not keep
// acting as the sole authority (dual-master risk) and must not fail over.
func (regtest *RegTest) TestSetMinorityWithMaster(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled")
		return true
	}
	return minorityDoesNothing(cl, setMinorityWithMaster, "testSetMinorityWithMaster")
}

// TestSetMinorityLostMaster — master on the majority side, unreachable. The
// node's master looks failed and it enters the failover path, but as the
// minority it cannot confirm authority and must not promote a slave.
func (regtest *RegTest) TestSetMinorityLostMaster(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled")
		return true
	}
	return minorityDoesNothing(cl, setMinorityLostMaster, "testSetMinorityLostMaster")
}

// setMinorityWithMaster: peer heartbeat failed + arbitrator unreachable,
// database left up (master on the minority side).
func setMinorityWithMaster(cl *cluster.Cluster) {
	cl.IsSplitBrain = true
	cl.ChaosCutArbitrator(minorityHold)
}

// setMinorityLostMaster: peer heartbeat failed + arbitrator + master all
// unreachable (master on the majority side).
func setMinorityLostMaster(cl *cluster.Cluster) {
	cl.IsSplitBrain = true
	cl.ChaosCutArbitrator(minorityHold)
	cl.ChaosCutDB(minorityHold)
}

// minorityDoesNothing applies a minority setup, holds it, and asserts the
// node never moves active/passive and never fails over. Always restores.
func minorityDoesNothing(cl *cluster.Cluster, setup func(*cluster.Cluster), label string) bool {
	status0 := cl.Status
	failoverCtr := cl.FailoverCtr

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
