// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ownSeqInBinlogState returns the highest sequence the given server id holds in a MariaDB
// gtid_binlog_state ("0-1-5,4-53070043-2269383"), 0 when absent.
func ownSeqInBinlogState(state string, serverID uint64) uint64 {
	var best uint64
	for _, g := range strings.Split(state, ",") {
		p := strings.Split(strings.TrimSpace(g), "-")
		if len(p) != 3 {
			continue
		}
		if sid, err := strconv.ParseUint(p[1], 10, 64); err != nil || sid != serverID {
			continue
		}
		if seq, err := strconv.ParseUint(p[2], 10, 64); err == nil && seq > best {
			best = seq
		}
	}
	return best
}

// TestSwitchoverNoDivergenceOnOldMaster (GH-1847): after a switchover the demoted master
// must never binlog a transaction of its own again. The traffic marker is injected several
// times through the proxies right after the switch; the old master's own-origin sequence in
// gtid_binlog_state must not move and every replica must keep its SQL thread running.
func (regtest *RegTest) TestSwitchoverNoDivergenceOnOldMaster(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {
	if cluster.Conf.ActivePassive {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Test not applicable in active-passive mode")
		return false
	}
	old := cluster.GetMaster()
	if old == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No master")
		return false
	}
	if !old.HaveMariaDBGTID {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "SKIPPED: %s is not a MariaDB GTID master, gtid_binlog_state has no own-origin sequence to check", old.URL)
		return true
	}
	cluster.SetRplMaxDelay(0)
	cluster.SetRplChecks(false)
	oldID := old.ServerID
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Master is %s (server_id %d)", old.URL, oldID)
	cluster.SwitchoverWaitTest()
	if cluster.GetMaster() == nil || cluster.GetMaster().URL == old.URL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover did not move the master")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "New master is %s", cluster.GetMaster().URL)
	var stateAfter string
	if err := old.Conn.QueryRowx("SELECT @@gtid_binlog_state").Scan(&stateAfter); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "gtid_binlog_state on old master: %s", err)
		return false
	}
	seqAtDemotion := ownSeqInBinlogState(stateAfter, oldID)
	for i := 0; i < 5; i++ {
		cluster.InjectProxiesTraffic()
		time.Sleep(2 * time.Second)
	}
	var stateLater string
	if err := old.Conn.QueryRowx("SELECT @@gtid_binlog_state").Scan(&stateLater); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "gtid_binlog_state on old master: %s", err)
		return false
	}
	if seqLater := ownSeqInBinlogState(stateLater, oldID); seqLater > seqAtDemotion {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Old master %s binlogged its own transactions after demotion: own seq %d -> %d (state %s): divergence", old.URL, seqAtDemotion, seqLater, stateLater)
		return false
	}
	for _, s := range cluster.GetSlaves() {
		s.Refresh()
		if !s.IsSQLThreadRunning() {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Replica %s SQL thread stopped after switchover", s.URL)
			return false
		}
	}
	return true
}
