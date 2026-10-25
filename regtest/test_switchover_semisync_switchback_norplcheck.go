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
	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (regtest *RegTest) TestSwitchover2TimesReplicationOkSemiSyncNoRplCheck(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)
		return false
	}
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s %s", err.Error(), result)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master  %s ", cluster.GetMaster().URL)
		SaveMasterURL := cluster.GetMaster().URL
		cluster.SwitchoverWaitTest()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master  %s ", cluster.GetMaster().URL)
		if SaveMasterURL == cluster.GetMaster().URL {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "same server URL after switchover")
			return false
		}
	}
	// The monitor sees the new topology a tick or two after the switchover ends: read the
	// replicas live (CheckSlavesRunning: SHOW SLAVE STATUS, threads and Master_Server_Id)
	// until they all replicate from the new master, for at most 30 seconds (#2169).
	for deadline := time.Now().Add(30 * time.Second); !cluster.CheckSlavesRunning(); {
		if time.Now().After(deadline) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replicas do not replicate from the new master %s 30s after the switchover", cluster.GetMaster().URL)
			return false
		}
	}
	return true
}
