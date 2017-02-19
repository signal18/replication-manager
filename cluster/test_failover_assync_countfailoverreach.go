package cluster

func (cluster *Cluster) testNumberFailOverLimitReach(conf string) bool {
	if cluster.initTestCluster(conf) == false {
		return false
	}
	cluster.conf.MaxDelay = 0

	cluster.LogPrintf("TESTING : Starting Test %s", "testNumberFailOverLimitReach")
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
	SaveMaster := cluster.master
	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.conf.FailLimit = 3
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 3
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 20
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf)
		SaveMaster.FailCount = 0
		return false
	}
	SaveMaster.FailCount = 0
	cluster.closeTestCluster(conf)
	return true
}
