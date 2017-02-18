package cluster

import (
	"time"

	"github.com/tanji/mariadb-tools/dbhelper"
)

func (cluster *Cluster) testSlaReplAllSlavesStopNoSemiSync() bool {
	cluster.LogPrintf("TESTING : Starting Test %s", "testSlaReplAllSlavesStopNoSemySync")
	cluster.conf.MaxDelay = 0
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

	cluster.sme.ResetUpTime()
	time.Sleep(3 * time.Second)
	sla1 := cluster.sme.GetUptimeFailable()
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(recover_time * time.Second)
	sla2 := cluster.sme.GetUptimeFailable()
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
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
	if sla2 == sla1 {
		return false
	} else {
		return true
	}
}
