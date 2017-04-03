// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import "github.com/tanji/replication-manager/cluster"

func testSwitchoverReadOnlyNoRplCheck(cluster *cluster.Cluster, conf string, test string) bool {
	if cluster.InitTestCluster(conf, test) == false {
		return false
	}

	cluster.LogPrintf("TEST : Master is %s", cluster.GetMaster().URL)
	cluster.SetRplMaxDelay(0)
	cluster.SetRplChecks(false)
	cluster.SetReadOnly(true)

	for _, s := range cluster.GetSlaves() {
		_, err := s.Conn.Exec("set global read_only=1")
		if err != nil {
			cluster.LogPrintf("ERROR: %s", err)
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	cluster.SwitchoverWaitTest()
	cluster.LogPrintf("TEST : New Master is %s ", cluster.GetMaster().URL)
	for _, s := range cluster.GetSlaves() {
		cluster.LogPrintf("TEST : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly == "OFF" {
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	cluster.CloseTestCluster(conf, test)
	return true
}
