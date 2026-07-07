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
// end to end: arm the simulated isolation (database connections and peer
// visibility cut on this instance — cluster_chaos.go), then assert that the
// isolated side loses the arbitration and refuses to fail over, and that
// everything recovers once the isolation is lifted. Requires arbitration to
// be configured (arbitrator + peer): without it the scenario is meaningless
// and is skipped as passed.
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

	cluster.ChaosIsolateStart(180 * time.Second)
	defer cluster.ChaosIsolateStop()

	// Phase 1: the isolated side must detect the (simulated) partition and
	// lose the election — split brain engaged, cluster standby.
	lost := false
	for i := 0; i < 30; i++ {
		time.Sleep(4 * time.Second)
		if cluster.IsSplitBrain && !cluster.IsActive() {
			lost = true
			break
		}
	}
	if !lost {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Isolated instance never lost arbitration (splitbrain=%t active=%t)", cluster.IsSplitBrain, cluster.IsActive())
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Isolated instance correctly lost arbitration and went standby")

	// The whole point: the isolated side must NOT have failed over.
	if cluster.FailoverCtr != failoverCtr {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAILOVER HAPPENED DURING ISOLATION — protection failed")
		return false
	}

	// Phase 2: lift the isolation, everything must converge back: databases
	// reachable, split brain resolved, same master, still no failover.
	cluster.ChaosIsolateStop()
	recovered := false
	for i := 0; i < 30; i++ {
		time.Sleep(4 * time.Second)
		if !cluster.IsSplitBrain && cluster.GetMaster() != nil && cluster.GetMaster().State != "Failed" {
			recovered = true
			break
		}
	}
	if !recovered {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Cluster did not recover after isolation lifted (splitbrain=%t)", cluster.IsSplitBrain)
		return false
	}
	if cluster.GetMaster().URL != masterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Master changed across isolation: %s -> %s", masterURL, cluster.GetMaster().URL)
		return false
	}
	if cluster.FailoverCtr != failoverCtr {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Failover counter moved across isolation")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Recovery complete: same master, no failover, split brain resolved")

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
