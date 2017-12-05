// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/signal18/replication-manager/cluster"

func testFailoverNoRplChecksNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(0)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR:", "%s", err)
		return false
	}
	SaveMasterURL := cluster.GetMaster().URL

	cluster.LogPrintf("TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.FailoverAndWait()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf(LvlErr, "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
