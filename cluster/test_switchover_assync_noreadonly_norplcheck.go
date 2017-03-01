// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

func (cluster *Cluster) testSwitchoverNoReadOnlyNoRplCheck(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TEST : Master is %s", cluster.master.URL)
	cluster.conf.ReadOnly = false
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global read_only=0")
		if err != nil {
			cluster.LogPrintf("ERROR : %s", err.Error())
			cluster.closeTestCluster(conf, test)
		}
	}
	SaveMasterURL := cluster.master.URL
	cluster.switchoverWaitTest()
	cluster.LogPrintf("TEST : New Master is %s ", cluster.master.URL)
	if SaveMasterURL == cluster.master.URL {
		cluster.LogPrintf("ERROR : same server URL after switchover")
		cluster.closeTestCluster(conf, test)
		return false
	}
	for _, s := range cluster.slaves {
		cluster.LogPrintf("INFO : Server  %s is %s", s.URL, s.ReadOnly)
		s.refresh()
		if s.ReadOnly != "OFF" {
			cluster.LogPrintf("ERROR : READ ONLY on slave was set by switchover")
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	cluster.closeTestCluster(conf, test)
	return true
}
