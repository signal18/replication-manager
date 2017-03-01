// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"time"
)

func (cluster *Cluster) testFailoverAllSlavesDelayNoRplChecksNoSemiSync(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}

	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	SaveMasterURL := cluster.master.URL
	cluster.DelayAllSlaves()

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.master.State = stateFailed
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 4
	cluster.conf.CheckFalsePositiveHeartbeat = false
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.waitFailover(wg)
	cluster.killMariaDB(cluster.master)
	wg.Wait()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}

	cluster.closeTestCluster(conf, test)
	return true
}
