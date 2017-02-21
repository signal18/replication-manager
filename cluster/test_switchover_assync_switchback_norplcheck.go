package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck(conf string, test string) bool {
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

	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR : %s %s", err.Error(), result)
			cluster.closeTestCluster(conf, test)
			return false
		}
		cluster.LogPrintf("TEST : New Master  %s ", cluster.master.URL)
		SaveMasterURL := cluster.master.URL
		cluster.switchoverChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("TEST : New Master  %s ", cluster.master.URL)

		if SaveMasterURL == cluster.master.URL {
			cluster.LogPrintf("TEST : same server URL after switchover")
			cluster.closeTestCluster(conf, test)
			return false
		}
	}

	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("TEST : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			cluster.closeTestCluster(conf, test)
			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("TEST :  Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			cluster.closeTestCluster(conf, test)
			return false
		}
	}
	cluster.closeTestCluster(conf, test)
	return true
}
