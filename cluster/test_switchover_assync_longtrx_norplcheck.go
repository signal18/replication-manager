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
	_, err2 := db.Exec("start transaction")
	if err2 != nil {
		cluster.LogPrintf("ERROR : %s", err2)
		cluster.closeTestCluster(conf, test)
		return false
	}
	err = dbhelper.InjectLongTrx(db, 10)
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.LogPrintf("TEST : Wainting 12s in some trx")
	time.Sleep(12 * time.Second)

	cluster.LogPrintf("TEST :  Master is %s", cluster.master.URL)

	cluster.switchoverChan <- true

	cluster.waitFailoverEnd()
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
		cluster.closeTestCluster(conf,test)
		return false
	}
	cluster.closeTestCluster(conf,test)
	return true
}
