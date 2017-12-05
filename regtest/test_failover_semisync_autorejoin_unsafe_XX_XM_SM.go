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

func testFailoverSemisyncAutoRejoinUnsafeMSMXXXRXMSM(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

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
	SaveMaster := cluster.GetMaster()

	cluster.CleanupBench()
	cluster.PrepareBench()
	go cluster.RunBench()
	time.Sleep(4 * time.Second)
	SaveMaster2 := cluster.GetSlaves()[0]

	cluster.StopDatabaseService(cluster.GetSlaves()[0])
	time.Sleep(5 * time.Second)
	cluster.RunBench()

	cluster.StopDatabaseService(cluster.GetMaster())
	time.Sleep(15 * time.Second)

	cluster.ForgetTopology()

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster2)
	wg2.Wait()
	//Recovered as slave first wait that it trigger master failover
	time.Sleep(5 * time.Second)
	cluster.RunBench()

	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
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
	if len(cluster.GetServers()) == 2 && SaveMaster2.URL != cluster.GetMaster().URL {
		cluster.LogPrintf(LvlErr, "Unexpected master for 2 nodes cluster")
		return false
	}

	return true
}
