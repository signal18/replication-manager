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

func (regtest *RegTest) TestFailoverSemisyncSlavekilledAutoRejoin(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)

	SaveMaster := cluster.GetMaster()
	SaveMasterURL := SaveMaster.URL
	//clusteruster.DelayAllSlaves()
	killedSlave := cluster.GetSlaves()[0]
	cluster.StopDatabaseService(killedSlave)

	time.Sleep(5 * time.Second)
	cluster.FailoverAndWait()

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		return false
	}
	cluster.PrepareBench()

	cluster.StartDatabaseService(killedSlave)
	time.Sleep(12 * time.Second)
	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
	SaveMaster.ReadAllRelayLogs()

	if killedSlave.HasSiblings(cluster.GetSlaves()) == false {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Not all slaves pointing to master")

		return false
	}

	return true
}
