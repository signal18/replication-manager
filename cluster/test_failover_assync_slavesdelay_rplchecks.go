package cluster

import "time"

func (cluster *Cluster) testFailoverAllSlavesDelayRplChecksNoSemiSync(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}

	err := cluster.disableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}

	SaveMasterURL := cluster.master.URL

	cluster.DelayAllSlaves()
	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = true
	cluster.conf.MaxDelay = 4
	cluster.conf.CheckFalsePositiveHeartbeat = false
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	err = cluster.enableSemisync()
	if err != nil {
		cluster.LogPrintf("ERROR : %s", err)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.closeTestCluster(conf, test)
	return true
}
