// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testFailoverAssyncAutoRejoinNowrites(cluster *cluster.Cluster, conf string, test string) bool {

	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(false)
	cluster.SetRejoinDump(false)
	cluster.DisableSemisync()
	SaveMasterURL := cluster.GetMaster().URL
	SaveMaster := cluster.GetMaster()

	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(cluster.GetMaster())
	wg.Wait()
	/// give time to start the failover

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST", " Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
	//Wait for replication recovery
	time.Sleep(2 * time.Second)
	if cluster.CheckTableConsistency("test.sbtest") != true {
		cluster.LogPrintf("ERROR", "Inconsitant slave")
		cluster.CloseTestCluster(conf, test)
		return false
	}

	if cluster.CheckSlavesRunning() == false {
		cluster.LogPrintf("ERROR", "Replication issue")
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.CloseTestCluster(conf, test)

	return true
}
