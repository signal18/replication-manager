// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestMisplacedReplicaNotRelay covers #2027 and #2028: a replica misplaced under another
// replica is the broken one; the replica it hangs under stays a plain, electable Slave
// (only a master fed from outside the cluster is a relay), and the never-relay repair
// moves the misplaced replica to the master although no crash elects the master.
//
// Scenario (orchestrator-agnostic, gated on a master with two GTID replicas):
//  1. With replication-master-slave-never-relay off, point replica B to replica A.
//  2. A must not be flagged relay, B must raise ERR00064; with B stopped, ERR00032 (no
//     candidate) and ERR00036 (relay skipped) must stay closed over three ticks.
//  3. Hide the crash records electing the master, turn never-relay on, restart B: B must
//     replicate from the master again.
func (regtest *RegTest) TestMisplacedReplicaNotRelay(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	master := cl.GetMaster()
	slaves := cl.GetSlaves()
	if master == nil || len(slaves) < 2 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: needs a master and at least two slaves")
		return true
	}
	upper, misplaced := slaves[0], slaves[1]
	if !upper.HasGTIDReplication() || !misplaced.HasGTIDReplication() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: needs GTID replication")
		return true
	}
	if cl.StateMachine.IsInState("ERR00032") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: no candidate (ERR00032) before the test")
		return true
	}
	fail := func(format string, args ...interface{}) bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: "+format, args...)
		return false
	}
	isUnder := func(s, parent *cluster.ServerMonitor) bool {
		src, _ := cl.GetMasterFromReplication(s)
		return src != nil && src.Id == parent.Id
	}

	noRelay := cl.Conf.ReplicationNoRelay
	saved := cl.Crashes
	defer func() {
		cl.Crashes = saved
		cl.SetReplicationNoRelay(noRelay)
		if m := cl.GetMaster(); m != nil && !isUnder(misplaced, m) {
			if _, err := misplaced.SetReplicationGTIDSlavePosFromServer(m); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cleanup: cannot point %s back to %s: %s", misplaced.URL, m.URL, err)
			}
		}
		if _, err := misplaced.StartSlave(); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cleanup: cannot start replication on %s: %s", misplaced.URL, err)
		}
	}()

	// 1. master <- upper <- misplaced
	cl.SetReplicationNoRelay(false)
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Pointing %s to replica %s", misplaced.URL, upper.URL)
	if _, err := misplaced.SetReplicationGTIDSlavePosFromServer(upper); err != nil {
		return fail("cannot point %s to %s: %s", misplaced.URL, upper.URL, err)
	}
	misplaced.StartSlave()
	if !waitFor(30*time.Second, func() bool { return isUnder(misplaced, upper) && cl.StateMachine.IsInState("ERR00064") }) {
		return fail("%s under %s not detected (ERR00064=%t)", misplaced.URL, upper.URL, cl.StateMachine.IsInState("ERR00064"))
	}

	// 2. upper stays a plain, electable Slave.
	misplaced.StopSlave()
	ticks := time.Duration(3*cl.Conf.MonitoringTicker) * time.Second
	if waitFor(ticks, func() bool {
		return upper.IsRelay || cl.StateMachine.IsInState("ERR00032") || cl.StateMachine.IsInState("ERR00036")
	}) {
		return fail("%s treated as a relay (IsRelay=%t state=%s ERR00032=%t ERR00036=%t)", upper.URL, upper.IsRelay, upper.State, cl.StateMachine.IsInState("ERR00032"), cl.StateMachine.IsInState("ERR00036"))
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s stays %s and electable", upper.URL, upper.State)

	// 3. No crash elects the master: the repair still moves the misplaced replica.
	kept := make([]*cluster.Crash, 0, len(saved))
	for _, cr := range saved {
		if cr != nil && cr.ElectedMasterURL != master.URL {
			kept = append(kept, cr)
		}
	}
	cl.Crashes = kept
	misplaced.StartSlave()
	cl.SetReplicationNoRelay(true)
	if !waitFor(60*time.Second, func() bool { return isUnder(misplaced, master) }) {
		return fail("%s still under %s without a crash electing %s (ERR00065=%t)", misplaced.URL, upper.URL, master.URL, cl.StateMachine.IsInState("ERR00065"))
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "PASS: %s plain Slave and electable, %s repaired to %s without a crash record", upper.URL, misplaced.URL, master.URL)
	return true
}

// waitFor polls cond every second until it holds or the timeout expires.
func waitFor(timeout time.Duration, cond func() bool) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(time.Second) {
		if cond() {
			return true
		}
	}
	return cond()
}
