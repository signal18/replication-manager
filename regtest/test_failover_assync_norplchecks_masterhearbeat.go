// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestFailoverNoRplChecksNoSemiSyncMasterHeartbeat(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	cluster.SetRplMaxDelay(0)
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err)

		return false
	}
	SaveMasterURL := cluster.GetMaster().URL

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master is %s", cluster.GetMaster().URL)
	cluster.SetInteractive(false)
	cluster.SetFailLimit(5)
	cluster.SetFailTime(0)
	cluster.SetFailoverCtr(0)
	cluster.SetCheckFalsePositiveHeartbeat(true)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(20)
	cluster.CheckFailed()
	cluster.FailoverNow()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master  %s ", cluster.GetMaster().URL)
	if cluster.GetMaster() != nil && cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}
	err = cluster.EnableSemisync()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR:", "%s", err)

		return false
	}
	return true
}
