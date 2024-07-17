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

func (regtest *RegTest) TestMasterSuspect(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(true)
	cluster.SetRplChecks(true)
	cluster.GetMaster().FailCount = 1
	cluster.GetMaster().SetState("Suspect")
	cluster.GetMaster().PrevState = "Suspect"
	time.Sleep(10 * time.Second)
	if cluster.GetMaster().State == "Suspect" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", " Master state not refresh %s: %s  ", cluster.GetMaster().URL, cluster.GetMaster().State)
		return false
	}

	return true
}
