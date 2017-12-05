// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
)

func testFailoverTimeNotReach(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.LogPrintf("TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetFailLimit(3)
	cluster.SetFailTime(60)
	// Give longer failtime than the failover wait loop 30s
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.SetFailoverTs(time.Now().Unix())
	//Giving time for state dicovery
	time.Sleep(4 * time.Second)
	cluster.FailoverAndWait()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
