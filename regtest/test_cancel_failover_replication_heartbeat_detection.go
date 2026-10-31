// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestCancelFailoverReplicationHeartbeatDetection forces a live master to failed and expects
// no failover: its replicas still receive from it, so the false-positive heartbeat check
// (failover-falsepositive-heartbeat) cancels the failover with ERR00028 (#2208). Named
// testFailoverManual until #2208: it never tested a manual failover.
func (regtest *RegTest) TestCancelFailoverReplicationHeartbeatDetection(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(true)
	cluster.SetRplChecks(true)
	SaveMaster := cluster.GetMaster()
	SaveMasterURL := SaveMaster.URL
	cluster.FailoverNow()
	if cluster.GetMaster() != nil && cluster.GetMaster().URL != SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", " Old master %s !=  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	return true
}
