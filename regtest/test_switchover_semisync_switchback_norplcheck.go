// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/dbhelper"
)

func testSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster *cluster.Cluster, conf string, test string) bool {
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
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR: %s %s", err.Error(), result)
		}
		cluster.LogPrintf("TEST : New Master  %s ", cluster.GetMaster().URL)
		SaveMasterURL := cluster.GetMaster().URL
		cluster.SwitchoverWaitTest()
		cluster.LogPrintf("TEST : New Master  %s ", cluster.GetMaster().URL)

		if SaveMasterURL == cluster.GetMaster().URL {
			cluster.LogPrintf("ERROR: same server URL after switchover")
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	time.Sleep(2 * time.Second)
	for _, s := range cluster.GetSlaves() {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("ERROR: Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			cluster.CloseTestCluster(conf, test)
			return false
		}
		if s.MasterServerID != cluster.GetMaster().ServerID {
			cluster.LogPrintf("ERROR:  Replication is  pointing to wrong master %s ", cluster.GetMaster().ServerID)
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
