// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/tanji/replication-manager/cluster"

func testSwitchoverNoReadOnlyNoRplCheck(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	cluster.SetRplMaxDelay(0)
	cluster.SetRplChecks(false)
	cluster.SetReadOnly(false)

	cluster.LogPrintf("TEST", "Master is %s", cluster.GetMaster().URL)

	for _, s := range cluster.GetServers() {
		_, err := s.Conn.Exec("set global read_only=0")
		if err != nil {
			cluster.LogPrintf("ERROR", "%s", err.Error())
			cluster.CloseTestCluster(conf, test)
		}
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST", "New Master is %s ", cluster.GetMaster().URL)
	if SaveMasterURL == cluster.GetMaster().URL {
		cluster.LogPrintf("ERROR", "same server URL after switchover")
		cluster.CloseTestCluster(conf, test)
		return false
	}
	for _, s := range cluster.GetSlaves() {
		cluster.LogPrintf("TEST", "Server  %s is %s", s.URL, s.ReadOnly)
		s.Refresh()
		if s.ReadOnly != "OFF" {
			cluster.LogPrintf("ERROR", "READ ONLY on slave was set by switchover")
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
