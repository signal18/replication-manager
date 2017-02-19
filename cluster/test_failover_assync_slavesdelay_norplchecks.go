package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) testFailOverAllSlavesDelayNoRplChecksNoSemiSync(conf string) bool {

	if cluster.initTestCluster(conf) == false {
		return false
	}

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverAllSlavesDelayNoRplChecksNoSemiSync")
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
	cluster.LogPrintf("BENCH: Stopping replication")
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
	}
	cluster.LogPrintf("BENCH : Write Concurrent Insert")

	dbhelper.InjectLongTrx(cluster.master.Conn, 10)
	cluster.LogPrintf("BENCH : Inject Long Trx")
	time.Sleep(10 * time.Second)
	cluster.LogPrintf("BENCH : Sarting replication")
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.master.State = stateFailed
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 4
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.master.URL)
		cluster.closeTestCluster(conf)
		return false
	}
	cluster.closeTestCluster(conf)
	return true
}
