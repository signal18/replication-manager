// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestSlaReplAllSlavesStopNoSemiSync(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s", err)

		return false
	}

	cluster.GetStateMachine().ResetUptime()
	time.Sleep(3 * time.Second)
	sla1 := cluster.GetStateMachine().GetUptimeFailable()
	err = cluster.StopSlaves()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s", err)

		return false
	}
	time.Sleep(recoverTime * time.Second)
	sla2 := cluster.GetStateMachine().GetUptimeFailable()
	err = cluster.StartSlaves()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s", err)

		return false
	}
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s", err)

		return false
	}
	if sla2 == sla1 {

		return false
	} else {

		return true
	}
}
