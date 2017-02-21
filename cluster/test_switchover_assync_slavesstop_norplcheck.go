package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck(conf string, test string) bool {
	if cluster.initTestCluster(conf,test) == false {
		return false
	}
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesStopNoRplCheck")
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
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO : Master is %s", cluster.master.URL)

		cluster.switchoverChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf,test)
		return false
	}
	cluster.closeTestCluster(conf,test)
	return true
}
