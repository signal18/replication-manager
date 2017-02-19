package cluster

import "time"

func (cluster *Cluster) testFailOverTimeNotReach(conf string) bool {
	if cluster.initTestCluster(conf) == false {
		return false
	}
	cluster.conf.MaxDelay = 0

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverTimeNotReach")
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

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.failoverTs = time.Now().Unix()
	cluster.conf.FailLimit = 3
	cluster.conf.FailTime = 20
	cluster.failoverCtr = 1
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 20
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf)
		return false
	}
	cluster.closeTestCluster(conf)
	return true
}

func (cluster *Cluster) getTestResultLabel(res bool) string {
	if res == false {
		return "FAILED"
	} else {
		return "PASS"
	}
}
