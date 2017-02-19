package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync(conf string) bool {
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
	db, err := cluster.getClusterProxyConn()
	if err != nil {
		cluster.LogPrintf("INFO : Can't take proxy conn %s ", err)
		cluster.closeTestCluster(conf)
		return false
	}
	go dbhelper.InjectLongTrx(db, 20)
	time.Sleep(2 * time.Second)
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
