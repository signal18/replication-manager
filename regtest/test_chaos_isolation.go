// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestChaosIsolationArbitration exercises the split-brain protection path
// end to end. It cuts this node's arbitrator link (cluster_chaos.go) — the
// constant across both co-located-master scenarios — leaving the master
// reachable, then asserts the isolated node fail-safes to standby and does
// not fail over; that it recovers when the cut is lifted; and finally that a
// REAL master failure still fails over automatically and the old master
// rejoins. Requires arbitration configured; otherwise skipped as passed.
// chaosWaitStandbyNoFailover waits for the isolated node to fail-safe to
// standby and asserts it did not fail over.
func chaosWaitStandbyNoFailover(cl *cluster.Cluster, failoverCtr int, label string) bool {
	for i := 0; i < 30; i++ {
		time.Sleep(4 * time.Second)
		if !cl.IsActive() {
			break
		}
	}
	if cl.IsActive() {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: isolated node never fail-safed to standby", label)
		return false
	}
	if cl.FailoverCtr != failoverCtr {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: FAILOVER HAPPENED WHILE ISOLATED — protection failed", label)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: isolated node fail-safed to standby, no failover", label)
	return true
}

// chaosWaitRecover waits for the cluster to converge back after the cut is
// lifted: same master, no failover.
func chaosWaitRecover(cl *cluster.Cluster, masterURL string, failoverCtr int, label string) bool {
	for i := 0; i < 30; i++ {
		time.Sleep(4 * time.Second)
		if cl.GetMaster() != nil && cl.GetMaster().State != "Failed" {
			break
		}
	}
	if cl.GetMaster() == nil || cl.GetMaster().State == "Failed" {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: cluster did not recover after cut lifted", label)
		return false
	}
	if cl.GetMaster().URL != masterURL {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: master changed across isolation: %s -> %s", label, masterURL, cl.GetMaster().URL)
		return false
	}
	if cl.FailoverCtr != failoverCtr {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: failover counter moved across isolation", label)
		return false
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "%s: recovered — same master, no failover", label)
	return true
}

func (regtest *RegTest) TestChaosIsolationArbitration(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if !cluster.Conf.Arbitration {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Skipping chaos isolation: arbitration not enabled on this cluster")
		return true
	}
	if cluster.GetMaster() == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "No master discovered, cannot run chaos isolation")
		return false
	}
	masterURL := cluster.GetMaster().URL
	failoverCtr := cluster.FailoverCtr

	// The assertion is only meaningful in automatic failover mode: in
	// manual/interactive mode no failover would happen regardless, and the
	// test would pass without arbitration being what blocked it. Force auto
	// for the duration and restore the previous mode afterwards.
	prevInteractive := cluster.Conf.Interactive
	cluster.SetInteractive(false)
	defer cluster.SetInteractive(prevInteractive)

	// Partition 1 — isolated active, MASTER OK: cut arbitrator + peer, leave
	// the database reachable. The node has a live master but cannot confirm
	// authority, so it must fail-safe (go standby, not keep acting) — the
	// surviving peer takes over; this side must not stay a second master.
	cluster.ChaosCutArbitrator(180 * time.Second)
	if !chaosWaitStandbyNoFailover(cluster, failoverCtr, "P1 (master ok)") {
		return false
	}
	cluster.ChaosStop()
	if !chaosWaitRecover(cluster, masterURL, failoverCtr, "P1") {
		return false
	}

	// Partition 2 — isolated active, MASTER FAILED: also cut the database
	// (db). The node wants to fail over but cannot reach the arbitrator, so
	// it must NOT promote a new master.
	cluster.ChaosCutArbitrator(180 * time.Second)
	cluster.ChaosCutDB(180 * time.Second)
	if !chaosWaitStandbyNoFailover(cluster, failoverCtr, "P2 (master failed)") {
		return false
	}
	cluster.ChaosStop()
	if !chaosWaitRecover(cluster, masterURL, failoverCtr, "P2") {
		return false
	}

	// Phase 3 — the hard part. Phases 1-2 pass whether or not failover is
	// automatic, so alone they cannot prove arbitration is what blocked a
	// promotion. Now, with arbitration active and mode auto, a REAL master
	// failure must still fail over (the protection must never block the
	// legitimate authority) and the old master must come back as a slave.
	if !cluster.IsActive() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Peer holds authority after recovery; active-side failover phase must run on the active instance — skipping")
		return true
	}
	saveMaster := cluster.GetMaster()
	saveMasterURL := saveMaster.URL
	cluster.SetFailSync(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(cluster.GetMaster())
	wg.Wait()

	if cluster.GetMaster() == nil || cluster.GetMaster().URL == saveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "No automatic failover under active arbitration — the protection wrongly blocked the legitimate authority")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Automatic failover under arbitration: %s -> %s", saveMasterURL, cluster.GetMaster().URL)

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(saveMaster)
	wg2.Wait()
	time.Sleep(2 * time.Second)

	if !cluster.CheckSlavesRunning() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master did not rejoin as running slave")
		return false
	}
	rejoined := false
	for _, sl := range cluster.GetSlaves() {
		if sl.URL == saveMasterURL {
			rejoined = true
		}
	}
	if !rejoined {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s is not back among the slaves", saveMasterURL)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s rejoined as slave — full arbitration lifecycle validated", saveMasterURL)
	return true
}
