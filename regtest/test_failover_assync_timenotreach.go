// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
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
		cluster.LogPrintf("ERROR", "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.SetFailoverTs(time.Now().Unix())
	//Giving time for state dicovery
	time.Sleep(4 * time.Second)
	cluster.FailoverAndWait()
	cluster.LogPrintf("TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogPrintf("ERROR", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
