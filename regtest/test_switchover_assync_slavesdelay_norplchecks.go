// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testSwitchoverAllSlavesDelayNoRplChecksNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetRplMaxDelay(8)
	cluster.SetRplChecks(false)

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	err = cluster.StopSlaves()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	time.Sleep(15 * time.Second)

	SaveMasterURL := cluster.GetMaster().URL
	cluster.LogPrintf("TEST :  Master is %s", cluster.GetMaster().URL)

	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST : New Master  %s ", cluster.GetMaster().URL)

	err = cluster.StartSlaves()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
