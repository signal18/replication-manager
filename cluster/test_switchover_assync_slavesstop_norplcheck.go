package cluster

import "time"

func (cluster *Cluster) testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck(conf string, test string) bool {
	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	err = cluster.stopSlaves()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}

	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO : Master is %s", cluster.master.URL)
	cluster.switchoverWaitTest()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)
	return true
}
