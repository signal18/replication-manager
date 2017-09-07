// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/tanji/replication-manager/cluster"

func testSwitchoverNoReadOnlyNoRplCheck(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR", "%s", err)
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
		}
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST", "New Master is %s ", cluster.GetMaster().URL)
	if SaveMasterURL == cluster.GetMaster().URL {
		cluster.LogPrintf("ERROR", "same server URL after switchover")
		return false
	}
	for _, s := range cluster.GetSlaves() {
		cluster.LogPrintf("TEST", "Server  %s is %s", s.URL, s.ReadOnly)
		s.Refresh()
		if s.ReadOnly != "OFF" {
			cluster.LogPrintf("ERROR", "READ ONLY on slave was set by switchover")
			return false
		}
	}
	return true
}
