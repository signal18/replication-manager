// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
)

func testFailoverSemisyncAutoRejoinUnsafeMSXMSM(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailoverCtr(0)
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(true)
	cluster.EnableSemisync()
	cluster.SetFailTime(0)
	cluster.SetFailRestartUnsafe(true)
	cluster.SetBenchMethod("table")
	SaveMasterURL := cluster.GetMaster().URL
	SaveMaster := cluster.GetMaster()
	//clusteruster.DelayAllSlaves()
	cluster.CleanupBench()
	cluster.PrepareBench()
	go cluster.RunBench()
	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(cluster.GetMaster())
	wg.Wait()

	cluster.RunBench()

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		return false
	}
	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
	//Recovered as slave first wait that it trigger master failover
	time.Sleep(5 * time.Second)

	for _, s := range cluster.GetSlaves() {
		if s.IsReplicationBroken() {
			cluster.LogPrintf(LvlErr, "Slave  %s issue on replication", s.URL)

			return false
		}
	}
	time.Sleep(10 * time.Second)
	if cluster.ChecksumBench() != true {
		cluster.LogPrintf(LvlErr, "Inconsitant slave")

		return false
	}
	if len(cluster.GetServers()) == 2 && SaveMasterURL == cluster.GetMaster().URL {
		cluster.LogPrintf(LvlErr, "Unexpected master for 2 nodes cluster")
		return false
	}

	return true
}
