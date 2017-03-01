// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"time"
)

func (cluster *Cluster) testFailoverAssyncAutoRejoinNowrites(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(false)
	cluster.SetRejoinDump(false)
	cluster.disableSemisync()
	SaveMasterURL := cluster.master.URL
	SaveMaster := cluster.master

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

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.waitRejoin(wg2)
	cluster.startMariaDB(SaveMaster)
	wg2.Wait()
	//Wait for replication recovery
	time.Sleep(2 * time.Second)
	if cluster.checkTableConsistency("test.sbtest") != true {
		cluster.LogPrintf("ERROR: Inconsitant slave")
		cluster.closeTestCluster(conf, test)
		return false
	}

	if cluster.checkSlavesRunning() == false {
		cluster.LogPrintf("ERROR: replication issue")
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)

	return true
}
