// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestFailoverSemisyncAutoRejoinMSSXMSXXMXSMSSM(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

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
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	cluster.StartDatabaseWaitRejoin(SaveMaster2)
	time.Sleep(5 * time.Second)
	cluster.RunBench()
	cluster.StartDatabaseWaitRejoin(SaveMaster)

	for _, s := range cluster.GetSlaves() {
		if s.IsReplicationBroken() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Slave  %s issue on replication", s.URL)
			return false
		}
	}
	time.Sleep(5 * time.Second)
	if cluster.ChecksumBench() != true {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Inconsitant slave")
		return false
	}

	return true
}
