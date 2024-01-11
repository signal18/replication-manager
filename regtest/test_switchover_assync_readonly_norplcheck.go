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

func (regtest *RegTest) TestSwitchoverReadOnlyNoRplCheck(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Master is %s", cluster.GetMaster().URL)
	cluster.SetRplMaxDelay(0)
	cluster.SetRplChecks(false)
	cluster.SetReadOnly(true)

	for _, s := range cluster.GetSlaves() {
		_, err := s.Conn.Exec("set global read_only=1")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, LvlErr, "%s", err)
			return false
		}
	}
	cluster.SwitchoverWaitTest()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New Master is %s ", cluster.GetMaster().URL)
	for _, s := range cluster.GetSlaves() {
		s.Refresh()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly == "OFF" {
			return false
		}
	}
	return true
}
