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

func (regtest *RegTest) TestMasterNil(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(true)
	cluster.SetRplChecks(true)
	SaveMaster := cluster.GetMaster()
	cluster.GetMaster().FailCount = 1
	cluster.GetMaster().State = "Suspect"
	cluster.GetMaster().PrevState = "Suspect"
	cluster.SetMasterNil()
	time.Sleep(10 * time.Second)
	if SaveMaster.State == "Suspect" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", " Old Master state not refresh %s: %s. Current Master:   ", SaveMaster.URL, SaveMaster.State, cluster.GetMaster().URL)
		return false
	}

	return true
}
