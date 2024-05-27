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

func (regtest *RegTest) TestFailoverTimeNotReach(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetFailLimit(3)
	cluster.SetFailTime(60)
	// Give longer failtime than the failover wait loop 30s
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.SetFailoverTs(time.Now().Unix())
	//Giving time for state dicovery
	time.Sleep(4 * time.Second)
	cluster.FailoverAndWait()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
