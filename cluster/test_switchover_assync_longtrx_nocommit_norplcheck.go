package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync(conf string, test string) bool {
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
	db, err := cluster.getClusterProxyConn()
	if err != nil {
		cluster.LogPrintf("INFO : Can't take proxy conn %s ", err)
		cluster.closeTestCluster(conf,test)
		return false
	}
	go dbhelper.InjectLongTrx(db, 20)
	time.Sleep(2 * time.Second)
	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

	cluster.switchoverChan <- true

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	err = cluster.enableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false

	}
	cluster.closeTestCluster(conf, test)
	return true
}
