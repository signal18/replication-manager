// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestRejoinStaleCrashKeepsMaster reproduces #1793 (belair 2026-09-14): a crash record
// that predates the current master's promotion and names it as the LOSER must never
// make RejoinMaster re-slave the live master (which built a replication ring, both
// sides writable, after a rolling-restart switchover).
//
// Scenario (orchestrator-agnostic, gated on a real master-slave topology):
//  1. Seed the cluster's crash working set with a STALE entry: URL = master,
//     ElectedMasterURL = a slave, timestamp one day before the master's promotion.
//  2. Fire RejoinMaster on the master (the same call the Failed->up / Maintenance->up
//     edge and the topology extra-master path make).
//  3. The master must still be the master, must not have a replication channel,
//     and the slave must still replicate from it.
func (regtest *RegTest) TestRejoinStaleCrashKeepsMaster(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	master := cl.GetMaster()
	if master == nil || len(cl.GetSlaves()) == 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: needs a master and at least one slave")
		return true
	}
	slave := cl.GetSlaves()[0]
	if cl.MasterChangeTs == 0 {
		cl.MasterChangeTs = time.Now().Unix()
	}
	stale := &cluster.Crash{
		URL:              master.URL,
		ElectedMasterURL: slave.URL,
		UnixTimestamp:    cl.MasterChangeTs - 86400,
	}
	cl.Crashes = append(cl.Crashes, stale)
	defer func() {
		kept := cl.Crashes[:0]
		for _, cr := range cl.Crashes {
			if cr != stale {
				kept = append(kept, cr)
			}
		}
		cl.Crashes = kept
	}()

	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Firing RejoinMaster on the live master %s with a stale crash naming it loser of %s", master.URL, slave.URL)
	if err := master.RejoinMaster(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "RejoinMaster returned %s (a skip returns nil)", err)
	}
	// Let the monitor loop observe the outcome (a rejoin that did run would have
	// issued CHANGE MASTER on the master within this window).
	time.Sleep(5 * time.Second)

	if m := cl.GetMaster(); m == nil || m.URL != master.URL {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: master changed after a stale-crash rejoin (now %v)", m)
		return false
	}
	if master.IsSlave || len(master.Replications) > 0 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: the master acquired a replication channel (ring): IsSlave=%v channels=%d", master.IsSlave, len(master.Replications))
		return false
	}
	if !slave.IsSlave {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: slave %s stopped replicating", slave.URL)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "PASS: master %s untouched by the stale crash, %s still its slave", master.URL, slave.URL)
	return true
}
