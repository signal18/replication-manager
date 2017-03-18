// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testSlaReplAllSlavesDelayNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}

	cluster.SetRplMaxDelay(2)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	cluster.GetStateMachine().ResetUpTime()
	time.Sleep(3 * time.Second)
	sla1 := cluster.GetStateMachine().GetUptimeFailable()
	cluster.DelayAllSlaves()
	sla2 := cluster.GetStateMachine().GetUptimeFailable()
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	if sla2 == sla1 {
		cluster.CloseTestCluster(conf, test)
		return false
	} else {
		cluster.CloseTestCluster(conf, test)
		return true
	}
}
