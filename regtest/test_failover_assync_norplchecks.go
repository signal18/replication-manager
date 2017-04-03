// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/tanji/replication-manager/cluster"

func testFailoverNoRplChecksNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetRplMaxDelay(0)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR: %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	SaveMasterURL := cluster.GetMaster().URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.GetMaster().URL)
	cluster.SetMasterStateFailed()
	cluster.SetInteractive(false)
	cluster.GetMaster().FailCount = cluster.GetMaxFail()
	cluster.SetFailLimit(5)
	cluster.SetFailTime(0)
	cluster.SetFailoverCtr(0)
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)
	cluster.CheckFailed()

	cluster.WaitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR: %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
