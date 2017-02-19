package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverLongTransactionNoRplCheckNoSemiSync(conf string) bool {
	if cluster.initTestCluster(conf) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverLongTransactionNoRplCheckNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}

	SaveMasterURL := cluster.master.URL
	masterTest, _ := cluster.newServerMonitor(cluster.master.URL)
	defer masterTest.Conn.Close()
	db, err := cluster.getClusterProxyConn()
	if err != nil {
		cluster.LogPrintf("TESTING : %s", err)
		cluster.closeTestCluster(conf)
		return false
	}
	_, err2 := db.Exec("start transaction")
	if err2 != nil {
		cluster.LogPrintf("TESTING : %s", err2)
		cluster.closeTestCluster(conf)
		return false
	}
	err = dbhelper.InjectLongTrx(db, 10)
	if err != nil {
		cluster.LogPrintf("TESTING : %s", err)
		cluster.closeTestCluster(conf)
		return false
	}
	cluster.LogPrintf("TESTING : Wainting 12s in some trx")
	time.Sleep(12 * time.Second)

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

	switchoverChan <- true

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf)
		return false
	}
	cluster.closeTestCluster(conf)
	return true
}
