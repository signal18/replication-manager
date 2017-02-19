package cluster

func (cluster *Cluster) testFailoverReplAllDelayAutoRejoinFlashback(conf string, test string) bool {

	if cluster.initTestCluster(conf, test) == false {
		return false
	}
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)

	SaveMasterURL := cluster.master.URL
	SaveMaster := cluster.master
	//clusteruster.DelayAllSlaves()
	cluster.PrepareBench()
	//go clusteruster.RunBench()
	cluster.killMariaDB(cluster.master)
	/// give time to start the failover
	err := cluster.waitFailoverStart()
	if err != nil {
		cluster.LogPrintf("TEST : Abording test, Failover Timeout")
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.waitFailoverEnd()

	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("TEST : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}
	cluster.startMariaDB(SaveMaster)
	cluster.waitRejoinEnd()
	cluster.closeTestCluster(conf, test)

	return true
}
