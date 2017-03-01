// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) testSwitchoverReadOnlyNoRplCheck(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}

	cluster.LogPrintf("TEST : Master is %s", cluster.master.URL)

	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.conf.ReadOnly = true

	for _, s := range cluster.slaves {
		_, err := s.Conn.Exec("set global read_only=1")
		if err != nil {
			cluster.LogPrintf("ERROR : %s", err)
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	cluster.switchoverWaitTest()
	cluster.LogPrintf("TEST : New Master is %s ", cluster.master.URL)
	for _, s := range cluster.slaves {
		cluster.LogPrintf("TEST : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly == "OFF" {
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	cluster.closeTestCluster(conf, test)
	return true
}
