// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestFailoverSemisyncAutoRejoinFlashback(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)

	SaveMasterURL := cluster.GetMaster().URL
	SaveMaster := cluster.GetMaster()
	//clusteruster.DelayAllSlaves()
	cluster.PrepareBench()
	//go clusteruster.RunBench()
	go cluster.RunSysbench()
	time.Sleep(4 * time.Second)
	cluster.FailoverAndWait()
	/// give time to start the failover

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		return false
	}
	cluster.RunSysbench()
	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
	SaveMaster.ReadAllRelayLogs()
	if cluster.CheckTableConsistency("test.sbtest") != true {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "Inconsitant slave")

		return false
	}

	return true
}
