// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"time"
)

func (cluster *Cluster) testFailoverSemisyncSlavekilledAutoRejoin(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)

	SaveMasterURL := cluster.master.URL
	SaveMaster := cluster.master
	//clusteruster.DelayAllSlaves()
	killedSlave := cluster.slaves[0]
	cluster.killMariaDB(killedSlave)

	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.waitFailover(wg)
	cluster.killMariaDB(cluster.master)
	wg.Wait()
	/// give time to start the failover

	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("TEST : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.RunSysbench()

	cluster.startMariaDB(killedSlave)

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.waitRejoin(wg2)
	cluster.startMariaDB(SaveMaster)
	wg2.Wait()
	SaveMaster.readAllRelayLogs()

	if cluster.master.hasSiblings(cluster.slaves) == false {
		cluster.LogPrintf("ERROR: Not all slaves pointing to master")
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)

	return true
}
