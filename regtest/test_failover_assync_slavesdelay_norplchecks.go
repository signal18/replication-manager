// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/signal18/replication-manager/cluster"

func (regtest *RegTest) TestFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.LogPrintf("TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetFailoverCtr(0)
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(4)

	cluster.DelayAllSlaves()

	cluster.FailoverAndWait()

	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Old master %s ==  New master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		return false
	}

	return true
}
