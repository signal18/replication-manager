// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/signal18/replication-manager/cluster"

func (regtest *RegTest) TestFailoverNumberFailureLimitReach(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)

		return false
	}

	SaveMaster := cluster.GetMaster()
	SaveMasterURL := cluster.GetMaster().URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.GetMaster().URL)
	cluster.SetMasterStateFailed()
	cluster.SetInteractive(false)
	cluster.GetMaster().FailCount = cluster.GetMaxFail()
	cluster.SetFailLimit(3)
	cluster.SetFailTime(0)
	cluster.SetFailoverCtr(3)
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)
	cluster.CheckFailed()

	cluster.WaitFailoverEnd()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		SaveMaster.FailCount = 0
		return false
	}
	SaveMaster.FailCount = 0
	return true
}
