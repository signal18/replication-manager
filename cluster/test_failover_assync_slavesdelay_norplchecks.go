package cluster

import (
	"sync"
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testFailOverAllSlavesDelayNoRplChecksNoSemiSync(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}

	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	err = cluster.stopSlaves()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err.Error())
		cluster.closeTestCluster(conf, test)
		return false
	}
	SaveMasterURL := cluster.master.URL
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("ERROR : %s %s", err.Error(), result)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.LogPrintf("BENCH : Write Concurrent Insert")

	err = dbhelper.InjectLongTrx(cluster.master.Conn, 10)
	if err != nil {
		cluster.LogPrintf("ERROR : %s %s", err.Error(), result)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.LogPrintf("BENCH : Inject Long Trx")
	time.Sleep(10 * time.Second)

	err = cluster.startSlaves()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err.Error())
		cluster.closeTestCluster(conf, test)
		return false
	}

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.master.State = stateFailed
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 4
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
