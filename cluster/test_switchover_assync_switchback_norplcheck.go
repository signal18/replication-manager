// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchover2TimesReplicationOkNoSemiSyncNoRplCheck(conf string, test string) bool {
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

	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR : %s %s", err.Error(), result)
			cluster.closeTestCluster(conf, test)
			return false
		}
		cluster.LogPrintf("TEST :  Master  %s ", cluster.master.URL)
		SaveMasterURL := cluster.master.URL
		cluster.switchoverWaitTest()
		cluster.LogPrintf("TEST : New Master  %s ", cluster.master.URL)

		if SaveMasterURL == cluster.master.URL {
			cluster.LogPrintf("TEST : same server URL after switchover")
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	if cluster.checkSlavesRunning() == false {
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)
	return true
}
