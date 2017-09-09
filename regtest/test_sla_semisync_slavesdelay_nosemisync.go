// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
)

func testSlaReplAllSlavesDelayNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}

	cluster.SetRplMaxDelay(2)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
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
		cluster.LogPrintf("ERROR", "%s", err)
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
