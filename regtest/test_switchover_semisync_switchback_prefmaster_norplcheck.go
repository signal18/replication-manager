// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testSwitchoverBackPreferedMasterNoRplCheckSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR: %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.SetPrefMaster(cluster.GetMaster().URL)
	cluster.LogPrintf("TEST : Set cluster.conf.PrefMaster %s", "cluster.conf.PrefMaster")
	time.Sleep(2 * time.Second)
	SaveMasterURL := cluster.GetMaster().URL
	for i := 0; i < 2; i++ {
		cluster.LogPrintf("TEST : New Master  %s Failover counter %d", cluster.GetMaster().URL, i)
		cluster.SwitchoverWaitTest()
		cluster.LogPrintf("TEST : New Master  %s ", cluster.GetMaster().URL)
	}
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf("ERROR: Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
