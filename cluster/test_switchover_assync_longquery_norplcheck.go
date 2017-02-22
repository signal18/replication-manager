package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverLongQueryNoRplCheckNoSemiSync(conf string, test string) bool {
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
	go dbhelper.InjectLongTrx(cluster.master.Conn, 20)

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.switchoverWaitTest()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(20 * time.Second)
	err = cluster.enableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}

	cluster.closeTestCluster(conf, test)
	return true
}
