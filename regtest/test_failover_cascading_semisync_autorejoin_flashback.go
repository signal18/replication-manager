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

func testFailoverCascadingSemisyncAutoRejoinFlashback(cluster *cluster.Cluster, conf string, test string) bool {

	if cluster.InitTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)
	cluster.EnableSemisync()
	SaveMasterURL := cluster.GetMaster().URL
	SaveMaster := cluster.GetMaster()
	//clusteruster.DelayAllSlaves()
	cluster.PrepareBench()
	//go clusteruster.RunBench()
	go cluster.RunSysbench()
	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.KillMariaDB(cluster.GetMaster())
	wg.Wait()
	SaveMaster2 := cluster.GetMaster()
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.KillMariaDB(cluster.GetMaster())
	wg.Wait()

	if cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogPrintf("TEST : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		cluster.CloseTestCluster(conf, test)
		return false
	}

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartMariaDB(SaveMaster)
	wg2.Wait()
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartMariaDB(SaveMaster2)
	wg2.Wait()

	for _, s := range cluster.GetSlaves() {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("ERROR: Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			cluster.CloseTestCluster(conf, test)
			return false
		}
	}
	if cluster.CheckTableConsistency("test.sbtest") != true {
		cluster.LogPrintf("ERROR: Inconsitant slave")
		cluster.CloseTestCluster(conf, test)
		return false
	}
	cluster.CloseTestCluster(conf, test)

	return true
}
