// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testFailoverSemisyncAutoRejoinMSSXMSXXMSXMSSM(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	//testFailoverCascadingSemisyncAutoRejoinFlashback
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
	cluster.FailoverAndWait()
	SaveMaster2 := cluster.GetMaster()
	cluster.RunBench()
	cluster.FailoverAndWait()
	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	cluster.StartDatabaseWaitRejoin(SaveMaster)
	time.Sleep(5 * time.Second)
	cluster.RunBench()
	cluster.StartDatabaseWaitRejoin(SaveMaster2)

	for _, s := range cluster.GetSlaves() {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("ERROR", "Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
	}
	time.Sleep(5 * time.Second)
	if cluster.ChecksumBench() != true {
		cluster.LogPrintf("ERROR", "Inconsitant slave")
		return false
	}

	return true
}
