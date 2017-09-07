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

func testFailoverSemisyncSlavekilledAutoRejoin(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)

	SaveMaster := cluster.GetMaster()
	SaveMasterURL := SaveMaster.URL
	//clusteruster.DelayAllSlaves()
	killedSlave := cluster.GetSlaves()[0]
	cluster.StopDatabaseService(killedSlave)

	time.Sleep(5 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(cluster.GetMaster())
	wg.Wait()
	/// give time to start the failover

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST", "Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)

		return false
	}
	cluster.PrepareBench()

	cluster.StartDatabaseService(killedSlave)
	time.Sleep(12 * time.Second)
	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()
	SaveMaster.ReadAllRelayLogs()

	if killedSlave.HasSiblings(cluster.GetSlaves()) == false {
		cluster.LogPrintf("ERROR", "Not all slaves pointing to master")

		return false
	}

	return true
}
