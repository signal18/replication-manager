package cluster

import (
	"time"

	"github.com/tanji/replication-manager/dbhelper"
)

func (cluster *Cluster) checkfailed() {
	// Don't trigger a failover if a switchover is happening
	if cluster.sme.IsInFailover() {
		cluster.LogPrintf("DEBUG: In Failover skip checking failed master")
		return
	}
	//  LogPrintf("WARN : Constraint is blocking master state %s stateFailed %s conf.Interactive %b cluster.master.FailCount %d >= maxfail %d" ,cluster.master.State,stateFailed,interactive, master.FailCount , maxfail )
	if cluster.master != nil {
		if cluster.master.State == stateFailed && cluster.conf.Interactive == false && cluster.isMaxMasterFailedCountReach() {
			if cluster.isBeetwenFailoverTimeTooShort() == false && cluster.isMaxClusterFailoverCountReach() == false && cluster.isOneSlaveHeartbeatIncreasing() == false {
				cluster.MasterFailover(true)
				cluster.failoverCond.Send <- true
			} else {
				cluster.LogPrintf("WARN : Constraint is blocking for failover")
			}
		}
	} else {
		if cluster.conf.LogLevel > 1 {
			cluster.LogPrintf("WARN : No master skip failover check")
		}
	}
}

func (cluster *Cluster) isMaxMasterFailedCountReach() bool {
	// illimited failed count
	if cluster.master.FailCount >= cluster.conf.MaxFail {
		cluster.LogPrintf("DEBUG: Need failover, maximum number of master failure detection reached")
		return true
	}
	return false
}

func (cluster *Cluster) isMaxClusterFailoverCountReach() bool {
	// illimited failed count
	if cluster.conf.FailLimit == 0 {
		return false
	}
	if cluster.failoverCtr == cluster.conf.FailLimit {
		cluster.LogPrintf("ERROR: Can't failover, maximum number of cluster failover reached")
		return true
	}
	return false
}

func (cluster *Cluster) isBeetwenFailoverTimeTooShort() bool {
	// illimited failed count
	rem := (cluster.failoverTs + cluster.conf.FailTime) - time.Now().Unix()
	if cluster.conf.FailTime == 0 {
		return false
	}
	if rem > 0 {
		cluster.LogPrintf("ERROR: Can failover, time between failover to short ")
		return true
	}
	return false
}

func (cluster *Cluster) isOneSlaveHeartbeatIncreasing() bool {
	if cluster.conf.CheckFalsePositiveHeartbeat == false {
		return false
	}

	for _, s := range cluster.slaves {

		status, _ := dbhelper.GetStatusAsInt(s.Conn)
		saveheartbeats := status["SLAVE_RECEIVED_HEARTBEATS"]
		cluster.LogPrintf("SLAVE_RECEIVED_HEARTBEATS %d", saveheartbeats)
		time.Sleep(3 * time.Second)
		status2, _ := dbhelper.GetStatusAsInt(s.Conn)
		cluster.LogPrintf("SLAVE_RECEIVED_HEARTBEATS %d", status2["SLAVE_RECEIVED_HEARTBEATS"])
		if status2["SLAVE_RECEIVED_HEARTBEATS"] > saveheartbeats {
			cluster.LogPrintf("ERROR: Can't failover,  slave %s still see the master ", s.DSN)
			return true
		}
	}
	return false
}
