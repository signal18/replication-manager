// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testSlaReplAllSlavesDelayNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetRplMaxDelay(2)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		return false
	}
	cluster.GetStateMachine().ResetUpTime()
	time.Sleep(3 * time.Second)
	sla1 := cluster.GetStateMachine().GetUptimeFailable()
	cluster.DelayAllSlaves()
	sla2 := cluster.GetStateMachine().GetUptimeFailable()
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		return false
	}
	if sla2 == sla1 {
		return false
	} else {
		return true
	}
}
