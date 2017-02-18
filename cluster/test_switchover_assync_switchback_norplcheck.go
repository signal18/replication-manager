package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOver2TimesReplicationOk")
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
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
		if err != nil {
			cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
		}
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
		SaveMasterURL := cluster.master.URL
		switchoverChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

		if SaveMasterURL == cluster.master.URL {
			cluster.LogPrintf("INFO : same server URL after switchover")
			return false
		}
	}

	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("INFO : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("INFO :  Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}
