// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "time"

func (cluster *Cluster) testFailoverTimeNotReach(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.conf.MaxDelay = 0
	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.failoverTs = time.Now().Unix()
	cluster.conf.FailLimit = 3
	cluster.conf.FailTime = 20
	cluster.failoverCtr = 1
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 20
	cluster.conf.CheckFalsePositiveHeartbeat = false
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	err = cluster.enableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}

	cluster.closeTestCluster(conf, test)
	return true
}
