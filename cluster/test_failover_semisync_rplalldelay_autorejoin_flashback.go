package cluster

import "sync"

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
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.waitFailover(wg)
	cluster.killMariaDB(cluster.master)
	wg.Wait()
	/// give time to start the failover

	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("TEST : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf, test)
		return false
	}

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.waitRejoin(wg2)
	cluster.startMariaDB(SaveMaster)
	wg2.Wait()
	cluster.closeTestCluster(conf, test)

	return true
}
