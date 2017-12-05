// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/signal18/replication-manager/cluster"

func testFailoverNoRplChecksNoSemiSyncMasterHeartbeat(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL

	cluster.LogPrintf(LvlInfo, "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetFailLimit(5)
	cluster.SetFailTime(0)
	cluster.SetFailoverCtr(0)
	cluster.SetCheckFalsePositiveHeartbeat(true)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)
	cluster.CheckFailed()
	cluster.FailoverNow()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf("TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR:", "%s", err)

		return false
	}
	return true
}
