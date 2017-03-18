// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/tanji/replication-manager/cluster"
)

func testFailoverAllSlavesDelayNoRplChecksNoSemiSync(cluster *cluster.Cluster, conf string, test string) bool {

	if cluster.InitTestCluster(conf, test) == false {
		return false
	}

	err := cluster.DisableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.CloseTestCluster(conf, test)
		return false
	}
	SaveMasterURL := cluster.GetMaster().URL
	cluster.DelayAllSlaves()

	cluster.LogPrintf("INFO :  Master is %s", cluster.GetMaster().URL)
	cluster.SetMasterStateFailed()
	cluster.SetInteractive(false)
	cluster.GetMaster().FailCount = cluster.GetMaxFail()
	cluster.SetFailLimit(5)
	cluster.SetFailTime(0)
	cluster.SetFailoverCtr(0)
	cluster.SetCheckFalsePositiveHeartbeat(false)
	cluster.SetRplChecks(false)
	cluster.SetRplMaxDelay(4)
	cluster.CheckFailed()

	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.KillMariaDB(cluster.GetMaster())
	wg.Wait()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.GetMaster().URL)

	time.Sleep(2 * time.Second)
	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	cluster.CloseTestCluster(conf, test)
	return true
}
