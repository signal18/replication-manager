package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverLongTransactionNoRplCheckNoSemiSync(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}

	SaveMasterURL := cluster.master.URL
	masterTest, _ := cluster.newServerMonitor(cluster.master.URL)
	defer masterTest.Conn.Close()
	db, err := cluster.getClusterProxyConn()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}

	go dbhelper.InjectTrxWithoutCommit(db, 10)
	cluster.LogPrintf("TEST : Wainting in some trx 12s more wait-trx  default 10 ")
	time.Sleep(12 * time.Second)

	cluster.LogPrintf("TEST :  Master is %s", cluster.master.URL)
	cluster.switchoverWaitTest()
	cluster.LogPrintf("TEST : New Master  %s ", cluster.master.URL)
	err = cluster.enableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("TEST : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)
	return true
}
