// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "time"

func (cluster *Cluster) testSwitchoverBackPreferedMasterNoRplCheckSemiSync(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.conf.PrefMaster = cluster.master.URL
	cluster.LogPrintf("TEST : Set cluster.conf.PrefMaster %s", "cluster.conf.PrefMaster")
	time.Sleep(2 * time.Second)
	SaveMasterURL := cluster.master.URL
	for i := 0; i < 2; i++ {

		cluster.LogPrintf("TEST : New Master  %s Failover counter %d", cluster.master.URL, i)
		cluster.switchoverWaitTest()
		cluster.LogPrintf("TEST : New Master  %s ", cluster.master.URL)

	}
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("ERROR : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)
	return true
}
