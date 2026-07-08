// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// SimulateHeartbeatFailure / RestoreHeartbeat are wired by the server package to the live
// ReplicationManager (regtest cannot import server — that would be a cycle).
// They drive the SERVER-level peer heartbeat cut so these cluster-scoped
// tests trigger a REAL network split brain (HeartbeatPeerSplitBrain times
// out → repman.SplitBrain → pushed down as IsSplitBrain), instead of faking
// the flag. Nil in plain unit tests.
var (
	SimulateHeartbeatFailure func(seconds int64)
	RestoreHeartbeat         func()
	// SimulatePeerFailure arms a split-brain-simulator cut on the ARBITRATION
	// PEER (repman2) via a direct authenticated API call — the remote half of
	// the minority-with-master orchestration. action is "simulate-heartbeat-
	// failure" or "simulate-master-failure". Wired by the server package; nil
	// in plain unit tests.
	SimulatePeerFailure func(clusterName, action string, seconds int64) error
	// SimulatePeerRestore clears the peer's cuts immediately on the happy path
	// (the peer's own bounded timer already guarantees crash-safe recovery).
	SimulatePeerRestore func(clusterName string) error
)

// Minority tests. A repman is in split brain because its infrastructure lost
// connection to BOTH its peer AND the arbitrator (DC3, which never fails): it
// is the minority node (1 of 3). The majority holds the truth, so the master
// this node sees is NOT authoritative. A minority node must do nothing: never
// flip active/passive, never fail over. The two tests differ only by where
// the master sits.

const minorityHold = 120 * time.Second

// TestSetMinorityWithMaster — master on the minority side, reachable. Must not
// keep sole authority (dual-master guard) and must not fail over.
func (regtest *RegTest) TestSetMinorityWithMaster(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled")
		return true
	}
	return minorityDoesNothing(cl, setMinorityWithMaster, "testSetMinorityWithMaster")
}

// TestSetMinorityWithoutMaster — master on the majority side, so this node holds
// only a slave and its master looks failed. It enters the failover path but
// as the minority must not promote.
func (regtest *RegTest) TestSetMinorityWithoutMaster(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cl.Conf.Arbitration {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping: arbitration not enabled")
		return true
	}
	return minorityDoesNothing(cl, setMinorityWithoutMaster, "testSetMinorityWithoutMaster")
}

// setMinorityWithMaster reproduces a real split brain for the case where the
// master is colocated on the isolated (minority) side. It does exactly 4
// operations — 2 LOCAL on this repman (repman1) and 2 REMOTE on the peer
// (repman2) — so both nodes independently fail to heartbeat each other, exactly
// like a cut cable where neither end can reach the other:
//
//	op 1/4 LOCAL  : cut repman1 → arbitrator link
//	op 2/4 LOCAL  : sever repman1's inbound heartbeat (repman2 → repman1 times out)
//	op 3/4 REMOTE : sever repman2's inbound heartbeat (repman1 → repman2 times out)
//	op 4/4 REMOTE : cut repman2 → master link (master lives on the minority side)
func setMinorityWithMaster(cl *cluster.Cluster) {
	secs := int64(minorityHold / time.Second)

	// op 1/4 LOCAL — repman1 loses the arbitrator (DC3).
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 1/4 LOCAL: cutting arbitrator link (repman1 -> arbitrator)")
	cl.SimulateArbitratorFailure(minorityHold)

	// op 2/4 LOCAL — darken repman1's inbound /api/heartbeat so repman2's
	// outbound heartbeat to repman1 times out (repman2 -> repman1 direction).
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 2/4 LOCAL: severing my inbound heartbeat (repman2 -> repman1 will time out)")
	if SimulateHeartbeatFailure != nil {
		SimulateHeartbeatFailure(secs)
	}

	if SimulatePeerFailure == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "no peer hook wired: skipping the 2 remote operations (single-node test)")
		return
	}

	// op 3/4 REMOTE — darken repman2's inbound /api/heartbeat so repman1's
	// outbound heartbeat to repman2 times out (repman1 -> repman2 direction).
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 3/4 REMOTE: severing peer inbound heartbeat on repman2 (repman1 -> repman2 will time out)")
	if err := SimulatePeerFailure(cl.Name, "simulate-heartbeat-failure", secs); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "op 3/4 REMOTE failed: %s", err)
	}

	// op 4/4 REMOTE — cut repman2's link to the master, which is colocated on
	// the minority side, so the majority loses the master but keeps its slave.
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 4/4 REMOTE: cutting peer -> master link on repman2 (master colocated on minority)")
	if err := SimulatePeerFailure(cl.Name, "simulate-master-failure", secs); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "op 4/4 REMOTE failed: %s", err)
	}
}

// setMinorityWithoutMaster is the mirror of setMinorityWithMaster for the case
// where the master lives on the MAJORITY side (repman2) and this node holds only
// a slave. The difference is which side loses the master: here repman1 (minority)
// is the one across the cut from the master, so the master cut is LOCAL — 3 LOCAL
// ops on repman1 and 1 REMOTE op on repman2:
//
//	op 1/4 LOCAL  : cut repman1 → arbitrator link
//	op 2/4 LOCAL  : sever repman1's inbound heartbeat (repman2 → repman1 times out)
//	op 3/4 REMOTE : sever repman2's inbound heartbeat (repman1 → repman2 times out)
//	op 4/4 LOCAL  : cut repman1 → master link (master lives on the majority side)
//
// repman1's master now looks failed and it enters the failover path — but as the
// minority it must not promote its local slave. Its own slaves stay reachable
// (SimulateMasterFailure cuts only the master link), so the failure it sees is
// precisely "lost the master", not "lost everything".
func setMinorityWithoutMaster(cl *cluster.Cluster) {
	secs := int64(minorityHold / time.Second)

	// op 1/4 LOCAL — repman1 loses the arbitrator (DC3).
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 1/4 LOCAL: cutting arbitrator link (repman1 -> arbitrator)")
	cl.SimulateArbitratorFailure(minorityHold)

	// op 2/4 LOCAL — darken repman1's inbound /api/heartbeat so repman2's
	// outbound heartbeat to repman1 times out (repman2 -> repman1 direction).
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 2/4 LOCAL: severing my inbound heartbeat (repman2 -> repman1 will time out)")
	if SimulateHeartbeatFailure != nil {
		SimulateHeartbeatFailure(secs)
	}

	// op 3/4 REMOTE — darken repman2's inbound /api/heartbeat so repman1's
	// outbound heartbeat to repman2 times out (repman1 -> repman2 direction).
	if SimulatePeerFailure != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 3/4 REMOTE: severing peer inbound heartbeat on repman2 (repman1 -> repman2 will time out)")
		if err := SimulatePeerFailure(cl.Name, "simulate-heartbeat-failure", secs); err != nil {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "op 3/4 REMOTE failed: %s", err)
		}
	} else {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "no peer hook wired: skipping op 3/4 (single-node test)")
	}

	// op 4/4 LOCAL — cut repman1's link to the master, which lives on the
	// majority side across the partition; repman1's local slaves stay reachable.
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "op 4/4 LOCAL: cutting my -> master link (master colocated on majority)")
	cl.SimulateMasterFailure(minorityHold)
}

// minorityDoesNothing applies a minority setup, first asserts the network
// split brain is actually DETECTED (peer heartbeat timed out), then asserts
// the node never moves active/passive and never fails over. Always restores.
func minorityDoesNothing(cl *cluster.Cluster, setup func(*cluster.Cluster), label string) bool {
	status0 := cl.Status
	failoverCtr := cl.FailoverCtr

	setup(cl)
	defer func() {
		// Clean up immediately on the happy path — local and peer both. Crash
		// safety is already guaranteed by each node's own bounded timer; these
		// calls just avoid the ~120s degraded tail when the test succeeds.
		if RestoreHeartbeat != nil {
			RestoreHeartbeat()
		}
		cl.RestoreSplitBrainSimulation()
		if SimulatePeerRestore != nil {
			if err := SimulatePeerRestore(cl.Name); err != nil {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: peer cleanup skipped (peer will self-heal on its own timer): %s", label, err)
			}
		}
	}()

	// First, the network split brain must be DETECTED via the real peer
	// heartbeat timeout — the first thing that must happen in the scenario.
	detected := false
	for i := 0; i < 20; i++ {
		time.Sleep(2 * time.Second)
		if cl.IsSplitBrain {
			detected = true
			break
		}
	}
	if !detected {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: network split brain never detected (peer heartbeat did not fail)", label)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: network split brain detected", label)

	// The minority must FAIL-SAFE, not freeze. It must:
	//   - never fail over (no promotion on a non-authoritative view), AND
	//   - yield: drop its cluster status to standby (and, where it owns the
	//     master, set it read-only so it can rejoin the majority's new master).
	// Assert no failover for the whole window and that it reaches standby.
	yielded := false
	for i := 0; i < 25; i++ {
		time.Sleep(3 * time.Second)
		if cl.FailoverCtr != failoverCtr {
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: minority node FAILED OVER on a non-authoritative master view", label)
			return false
		}
		if cl.Status == cluster.ConstMonitorStandby {
			yielded = true
		}
	}
	if !yielded {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: minority did NOT yield — still %s (started %s) — dual-active risk, master may keep writing", label, cl.Status, status0)
		return false
	}
	ro := "n/a"
	if m := cl.GetMaster(); m != nil {
		if m.IsReadOnly() {
			ro = "read-only"
		} else {
			ro = "READ-WRITE(!)"
		}
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: minority failed safe — yielded %s->standby, master %s, no failover", label, status0, ro)
	return true
}
