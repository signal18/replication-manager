// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestRelayElectableAndRepaired covers #2027 and #2028: a replica feeding another
// replica (a relay) stays a candidate and can be promoted, and the no-relay repair
// moves the replica under it back to the master even when no crash names the master.
//
// Scenario (orchestrator-agnostic, gated on a master with two GTID replicas):
//  1. With replication-master-slave-never-relay off, chain replica B under replica A:
//     master <- A <- B. A becomes a relay.
//  2. B is stopped, so A is the only possible candidate: over three monitor ticks
//     ERR00032 (no candidate) and ERR00036 (relay skipped) must stay closed.
//  3. Hide the crash records electing the master, turn the never-relay option on
//     and restart B: the repair must point B back to the master.
//  4. Chain B under A again, stop B and switch over: A must be elected, lose its
//     relay flag, keep B under it, and the old master must replicate from A.
func (regtest *RegTest) TestRelayElectableAndRepaired(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	master := cl.GetMaster()
	slaves := cl.GetSlaves()
	if master == nil || len(slaves) < 2 {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: needs a master and at least two slaves")
		return true
	}
	relay, leaf := slaves[0], slaves[1]
	if !relay.HasGTIDReplication() || !leaf.HasGTIDReplication() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: needs GTID replication to chain the replicas")
		return true
	}
	if cl.StateMachine.IsInState("ERR00032") {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: no candidate (ERR00032) before the test")
		return true
	}

	sourceOf := func(s *cluster.ServerMonitor) *cluster.ServerMonitor {
		src, _ := cl.GetMasterFromReplication(s)
		return src
	}
	isUnder := func(s, parent *cluster.ServerMonitor) bool {
		src := sourceOf(s)
		return src != nil && parent != nil && src.Id == parent.Id
	}
	chain := func() bool {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Chaining %s under %s", leaf.URL, relay.URL)
		if _, err := leaf.SetReplicationGTIDSlavePosFromServer(relay); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: cannot chain %s under %s: %s", leaf.URL, relay.URL, err)
			return false
		}
		leaf.StartSlave()
		if !waitFor(30*time.Second, func() bool { return relay.IsRelay }) {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: %s never seen as a relay", relay.URL)
			return false
		}
		return true
	}

	noRelay := cl.Conf.ReplicationNoRelay
	saved := cl.Crashes
	defer func() {
		cl.Crashes = saved
		cl.SetReplicationNoRelay(noRelay)
		// Leave the leaf directly under the current master whatever the outcome.
		if m := cl.GetMaster(); m != nil && m.Id != leaf.Id && !isUnder(leaf, m) {
			if _, err := leaf.SetReplicationGTIDSlavePosFromServer(m); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cleanup: cannot point %s back to %s: %s", leaf.URL, m.URL, err)
			}
		}
		if _, err := leaf.StartSlave(); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cleanup: cannot start replication on %s: %s", leaf.URL, err)
		}
	}()

	// 1. master <- relay <- leaf
	cl.SetReplicationNoRelay(false)
	if !chain() {
		return false
	}

	// 2. The relay is the only candidate left once the leaf is stopped: neither
	// state may open during three monitor ticks.
	leaf.StopSlave()
	ticks := time.Duration(3*cl.Conf.MonitoringTicker) * time.Second
	if waitFor(ticks, func() bool { return cl.StateMachine.IsInState("ERR00032") || cl.StateMachine.IsInState("ERR00036") }) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: relay %s is not a candidate (ERR00032=%t ERR00036=%t)", relay.URL, cl.StateMachine.IsInState("ERR00032"), cl.StateMachine.IsInState("ERR00036"))
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Relay %s is a failover candidate", relay.URL)

	// 3. No crash elects the master: the repair must still move the leaf up.
	kept := make([]*cluster.Crash, 0, len(saved))
	for _, cr := range saved {
		if cr != nil && cr.ElectedMasterURL != master.URL {
			kept = append(kept, cr)
		}
	}
	cl.Crashes = kept
	leaf.StartSlave()
	cl.SetReplicationNoRelay(true)
	if !waitFor(60*time.Second, func() bool { return isUnder(leaf, master) }) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: %s still under relay %s without a crash naming %s (ERR00065=%t)", leaf.URL, relay.URL, master.URL, cl.StateMachine.IsInState("ERR00065"))
		return false
	}
	cl.Crashes = saved
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s repaired to %s without a crash record", leaf.URL, master.URL)

	// 4. Promote the relay for real.
	cl.SetReplicationNoRelay(false)
	if !chain() {
		return false
	}
	leaf.StopSlave()
	cl.SwitchoverWaitTest()
	newMaster := cl.GetMaster()
	if newMaster == nil || newMaster.Id != relay.Id {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: switchover did not elect relay %s (master %v)", relay.URL, newMaster)
		return false
	}
	if relay.IsRelay {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: promoted %s still flagged relay", relay.URL)
		return false
	}
	if !isUnder(leaf, relay) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: %s left the promoted relay %s (source %v)", leaf.URL, relay.URL, sourceOf(leaf))
		return false
	}
	if !waitFor(60*time.Second, func() bool { return isUnder(master, relay) }) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: old master %s does not replicate from %s", master.URL, relay.URL)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "PASS: relay %s electable and promoted with %s attached, repair works without a crash record", relay.URL, leaf.URL)
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
