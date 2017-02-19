package cluster

import "time"

func (cluster *Cluster) testSwitchOverBackPreferedMasterNoRplCheckSemiSync(conf string) bool {
	if cluster.initTestCluster(conf) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverBackPreferedMasterNoRplCheckSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	cluster.conf.PrefMaster = cluster.master.URL
	cluster.LogPrintf("TESTING : Set cluster.conf.PrefMaster %s", "cluster.conf.PrefMaster")
	time.Sleep(2 * time.Second)
	SaveMasterURL := cluster.master.URL
	for i := 0; i < 2; i++ {

		cluster.LogPrintf("INFO : New Master  %s Failover counter %d", cluster.master.URL, i)

		switchoverChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf)
		return false
	}
	cluster.closeTestCluster(conf)
	return true
}
