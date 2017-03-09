// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sync"
	"time"
)

func (cluster *Cluster) testFailoverCascadingSemisyncAutoRejoinFlashback(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)
	cluster.enableSemisync()
	SaveMasterURL := cluster.master.URL
	SaveMaster := cluster.master
	//clusteruster.DelayAllSlaves()
	cluster.PrepareBench()
	//go clusteruster.RunBench()
	go cluster.RunSysbench()
	time.Sleep(4 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.waitFailover(wg)
	cluster.killMariaDB(cluster.master)
	wg.Wait()
	SaveMaster2 := cluster.master
	wg.Add(1)
	go cluster.waitFailover(wg)
	cluster.killMariaDB(cluster.master)
	wg.Wait()

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
	wg2.Add(1)
	go cluster.waitRejoin(wg2)
	cluster.startMariaDB(SaveMaster2)
	wg2.Wait()

	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("ERROR : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	if cluster.checkTableConsistency("test.sbtest") != true {
		cluster.LogPrintf("ERROR: Inconsitant slave")
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)

	return true
}
