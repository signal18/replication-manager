// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/dbhelper"
)

func testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		return false
	}

	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR", "%s %s", err.Error(), result)
			return false
		}
		cluster.LogPrintf("TEST", "Master  %s ", cluster.GetMaster().URL)
		SaveMasterURL := cluster.GetMaster().URL
		cluster.SwitchoverWaitTest()
		cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)

		if SaveMasterURL == cluster.GetMaster().URL {
			cluster.LogPrintf("ERROR", "Same server URL after switchover")
			return false
		}
	}
	if cluster.CheckSlavesRunning() == false {
		return false
	}
	return true
}
