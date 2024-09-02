// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// CheckMaxConnections Check 80% of max connection reach
func (server *ServerMonitor) CheckMaxConnections() {
	cluster := server.ClusterGroup
	maxCx, _ := strconv.ParseInt(server.Variables.Get("MAX_CONNECTIONS"), 10, 64)
	curCx, _ := strconv.ParseInt(server.Status.Get("THREADS_CONNECTED"), 10, 64)
	if curCx > maxCx*80/100 {
		cluster.SetState("ERR00076", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00076"], server.URL), ErrFrom: "MON", ServerUrl: server.URL})
	}
}

func (server *ServerMonitor) CheckVersion() {
	cluster := server.ClusterGroup
	if server.DBVersion.IsMariaDB() && server.DBVersion.LowerReleaseList("10.4.12", "10.5.1") {
		cluster.SetState("MDEV20821", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["MDEV20821"], server.URL), ErrFrom: "MON", ServerUrl: server.URL})
	}

	if server.DBVersion.IsMariaDB() && !server.HasBinlogRow() && server.DBVersion.LowerReleaseList("10.2.44", "10.3.35", "10.4.25", "10.5.16", "10.6.8", "10.7.4", "10.8.3", "10.9.1") {
		cluster.SetState("MDEV28310", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["MDEV28310"], server.URL), ErrFrom: "MON", ServerUrl: server.URL})
	}

	if server.DBVersion.IsMariaDB() && !server.HasBinlogRow() && server.Variables.Get("INNODB_AUTOINC_LOCK_MODE") == "2" && (server.DBVersion.GreaterEqualRelease("10.2") || server.DBVersion.LowerReleaseList("10.3.35", " 10.4.25", " 10.5.16", " 10.6.8", " 10.7.4", " 10.8.3")) {
		cluster.SetState("MDEV19577", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["MDEV19577"], server.URL), ErrFrom: "MON", ServerUrl: server.URL})
	}

}

// CheckDisks check mariadb disk plugin ti see if it get free space
func (server *ServerMonitor) CheckDisks() {
	cluster := server.ClusterGroup
	for _, d := range server.Disks {
		if d.Used/d.Total*100 > int32(cluster.Conf.MonitorDiskUsagePct) {
			cluster.SetState("ERR00079", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00079"], server.URL), ErrFrom: "MON", ServerUrl: server.URL})
		}
	}
}

// CheckReplication Check replication health and return status string
func (server *ServerMonitor) CheckReplication() string {
	cluster := server.ClusterGroup
	if server.HaveWsrep {
		if server.IsWsrepSync {
			server.SetState(stateWsrep)
			return "Galera OK"
		} else if server.IsWsrepDonor {
			server.SetState(stateWsrepDonor)
			return "Galera OK"
		} else {
			server.SetState(stateWsrepLate)
			return "Galera Late"
		}
	}
	if cluster.StateMachine.IsInFailover() {
		return "In Failover"
	}
	if (server.IsDown()) && server.IsSlave == false {
		return "Master OK"
	}

	if cluster.master != nil {
		if server.ServerID == cluster.master.ServerID {
			return "Master OK"
		}
	}

	//Prevent maintenance label for topology active passive
	if cluster.Topology == topoActivePassive {
		return "Master OK"
	}

	if server.IsMaintenance {
		server.SetState(stateMaintenance)
		return "Maintenance"
	}
	// when replication stopped Valid is null
	ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return "Not a slave"
	}
	if ss.SecondsBehindMaster.Valid == false {

		//	log.Printf("replicationCheck %s %s", server.SQLThread, server.IOThread)
		if ss.SlaveSQLRunning.String == "Yes" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlaveErr)
			} else if server.IsRelay {
				server.SetState(stateRelayErr)
			}
			return fmt.Sprintf("NOT OK, IO Stopped (%s)", ss.LastIOErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "Yes" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlaveErr)
			} else if server.IsRelay {
				server.SetState(stateRelayErr)
			}
			return fmt.Sprintf("NOT OK, SQL Stopped (%s)", ss.LastSQLErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlaveErr)
			} else if server.IsRelay {
				server.SetState(stateRelayErr)
			}
			return "NOT OK, ALL Stopped"
		} else if ss.SlaveSQLRunning.String == "Connecting" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlave)
			} else if server.IsRelay {
				server.SetState(stateRelay)
			}
			return "NOT OK, IO Connecting"
		}

		if server.IsRelay == false && server.IsMaxscale == false {
			server.SetState(stateSlave)
		} else if server.IsRelay {
			server.SetState(stateRelay)
		}
		return "Running OK"
	}

	if ss.SecondsBehindMaster.Int64 > 0 {
		if ss.SecondsBehindMaster.Int64 > cluster.Conf.FailMaxDelay && cluster.Conf.RplChecks == true {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlaveLate)
			} else if server.IsRelay {
				server.SetState(stateRelayLate)
			}

		} else {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.SetState(stateSlave)
			} else if server.IsRelay {
				server.SetState(stateRelay)
			}
		}

		if cluster.Conf.DelayStatCapture {
			server.DelayStat.UpdateDelayStat(ss.SecondsBehindMaster.Int64, cluster.Conf.DelayStatRotate) // Capture Delay Stat
		}
		return "Behind master"
	}
	if server.IsRelay == false && server.IsMaxscale == false {
		server.SetState(stateSlave)
	} else if server.IsRelay {
		server.SetState(stateRelay)
	}

	if cluster.Conf.DelayStatCapture {
		server.DelayStat.UpdateDelayStat(ss.SecondsBehindMaster.Int64, cluster.Conf.DelayStatRotate) // Capture Delay Stat with 0 seconds behind master
	}
	return "Running OK"
}

// CheckSlaveSettings check slave variables & enforce if set
func (server *ServerMonitor) CheckSlaveSettings() {
	sl := server
	cluster := server.ClusterGroup
	if cluster.Conf.ForceSlaveSemisync && sl.HaveSemiSync == false && cluster.GetTopology() != topoMultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "DEBUG", "Enforce semisync on slave %s", sl.URL)
		dbhelper.InstallSemiSync(sl.Conn, server.DBVersion)
	} else if sl.IsIgnored() == false && sl.HaveSemiSync == false && cluster.GetTopology() != topoMultiMasterWsrep {
		cluster.SetState("WARN0048", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0048"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}

	if cluster.Conf.ForceBinlogRow && sl.HaveBinlogRow == false {
		// In non-multimaster mode, enforce read-only flag if the option is set
		dbhelper.SetBinlogFormat(sl.Conn, "ROW")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce binlog format ROW on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogRow == false && (cluster.Conf.AutorejoinFlashback == true || cluster.GetTopology() == topoMultiMasterWsrep) {
		//galera or binlog flashback need row based binlog
		cluster.SetState("WARN0049", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0049"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceSlaveReadOnly && sl.ReadOnly == "OFF" && !server.IsIgnoredReadonly() && !cluster.IsMultiMaster() {
		// In non-multimaster mode, enforce read-only flag if the option is set
		sl.SetReadOnly()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce read only on slave %s, ReadOnly:%s, InIgnored:%t MultiMaster:%t", sl.URL, sl.ReadOnly, server.IsIgnoredReadonly(), cluster.IsMultiMaster())
	}
	if cluster.Conf.ForceSlaveHeartbeat && sl.GetReplicationHearbeatPeriod() > 1 {
		dbhelper.SetSlaveHeartbeat(sl.Conn, "1", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce heartbeat to 1s on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationHearbeatPeriod() > 1 {
		cluster.SetState("WARN0050", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0050"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceSlaveGtid && sl.GetReplicationUsingGtid() == "No" {
		dbhelper.SetSlaveGTIDMode(sl.Conn, "slave_pos", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce GTID replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationUsingGtid() == "No" && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		cluster.SetState("WARN0051", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0051"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceSlaveGtidStrict && !sl.IsReplicationUsingGtidStrict() && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveGTIDModeStrict(sl.Conn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce GTID strict mode on slave %s", sl.URL)
	} else if !sl.IsIgnored() && !sl.IsReplicationUsingGtidStrict() && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		cluster.SetState("WARN0058", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0058"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}

	if cluster.Conf.ForceSlaveIdempotent && !sl.HaveSlaveIdempotent && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveExecMode(sl.Conn, "IDEMPOTENT", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication mode idempotent on slave %s", sl.URL)
	} /* else if !sl.IsIgnored() && cluster.Conf.ForceSlaveIdempotent && sl.HaveSlaveIdempotent && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		cluster.SetState("WARN0103", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0103"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}*/
	if cluster.Conf.ForceSlaveStrict && sl.HaveSlaveIdempotent && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveExecMode(sl.Conn, "STRICT", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication mode strict on slave %s", sl.URL)
	} /*else if !sl.IsIgnored() && cluster.Conf.ForceSlaveStrict &&  && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		cluster.SetState("WARN0104", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0103"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	} */
	if strings.ToUpper(cluster.Conf.ForceSlaveParallelMode) == "OPTIMISTIC" && !sl.HaveSlaveOptimistic && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveParallelMode(sl.Conn, "OPTIMISTIC", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication parallel mode optimistic on slave %s", sl.URL)
	}
	if strings.ToUpper(cluster.Conf.ForceSlaveParallelMode) == "SERIALIZED" && !sl.HaveSlaveSerialized && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveParallelMode(sl.Conn, "NONE", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication parallel mode serialized on slave %s", sl.URL)
	}
	if strings.ToUpper(cluster.Conf.ForceSlaveParallelMode) == "AGGRESSIVE" && !sl.HaveSlaveAggressive && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveParallelMode(sl.Conn, "AGGRESSIVE", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication parallel mode aggressive on slave %s", sl.URL)
	}
	if strings.ToUpper(cluster.Conf.ForceSlaveParallelMode) == "MINIMAL" && !sl.HaveSlaveMinimal && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveParallelMode(sl.Conn, "MINIMAL", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication parallel mode minimal on slave %s", sl.URL)
	}
	if strings.ToUpper(cluster.Conf.ForceSlaveParallelMode) == "CONSERVATIVE" && !sl.HaveSlaveConservative && cluster.GetTopology() != topoMultiMasterWsrep && server.IsMariaDB() {
		dbhelper.SetSlaveParallelMode(sl.Conn, "CONSERVATIVE", cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce replication parallel mode conservative on slave %s", sl.URL)
	}
	if cluster.Conf.ForceSyncInnoDB && sl.HaveInnodbTrxCommit == false {
		dbhelper.SetSyncInnodb(sl.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce InnoDB durability on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveInnodbTrxCommit == false {
		cluster.SetState("WARN0052", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0052"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceBinlogChecksum && sl.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(sl.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce checksum on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveChecksum == false {
		cluster.SetState("WARN0053", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0053"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceBinlogSlowqueries && sl.HaveBinlogSlowqueries == false {
		dbhelper.SetBinlogSlowqueries(sl.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce log slow queries of replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogSlowqueries == false {
		cluster.SetState("WARN0054", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0054"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if cluster.Conf.ForceBinlogAnnotate && sl.HaveBinlogAnnotate == false && server.IsMariaDB() {
		dbhelper.SetBinlogAnnotate(sl.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce annotate on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogAnnotate == false && server.IsMariaDB() {
		cluster.SetState("WARN0055", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0055"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}

	if cluster.Conf.ForceBinlogCompress && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(sl.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce binlog compression on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		cluster.SetState("WARN0056", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0056"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}
	if sl.IsIgnored() == false && sl.HaveBinlogSlaveUpdates == false {
		cluster.SetState("WARN0057", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0057"], sl.URL), ErrFrom: "TOPO", ServerUrl: sl.URL})
	}

	if server.IsAcid() == false && cluster.IsDiscovered() {
		cluster.SetState("WARN0007", state.State{ErrType: config.LvlWarn, ErrDesc: "At least one server is not ACID-compliant. Please make sure that sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF", ServerUrl: sl.URL})
	}

}

// CheckMasterSettings check master variables & enforce if set
func (server *ServerMonitor) CheckMasterSettings() {
	cluster := server.ClusterGroup
	if cluster.Conf.ForceSlaveSemisync && server.HaveSemiSync == false {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce semisync on Master %s", server.URL)
		dbhelper.InstallSemiSync(server.Conn, server.DBVersion)
	} else if server.HaveSemiSync == false && cluster.GetTopology() != topoMultiMasterWsrep && cluster.GetTopology() != topoMultiMasterGrouprep {
		cluster.SetState("WARN0060", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0060"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceBinlogRow && server.HaveBinlogRow == false {
		dbhelper.SetBinlogFormat(server.Conn, "ROW")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce binlog format ROW on Master %s", server.URL)
	} else if server.HaveBinlogRow == false && cluster.Conf.AutorejoinFlashback == true {
		cluster.SetState("WARN0061", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0061"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceSyncBinlog && server.HaveBinlogSync == false {
		dbhelper.SetSyncBinlog(server.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce sync binlog on Master %s", server.URL)
	} else if server.HaveBinlogSync == false {
		cluster.SetState("WARN0062", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0062"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceSyncInnoDB && server.HaveBinlogSync == false {
		dbhelper.SetSyncInnodb(server.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce innodb durability on Master %s", server.URL)
	} else if server.HaveBinlogSync == false {
		cluster.SetState("WARN0064", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0064"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceBinlogAnnotate && server.HaveBinlogAnnotate == false && server.IsMariaDB() {
		dbhelper.SetBinlogAnnotate(server.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce binlog annotate on master %s", server.URL)
	} else if server.HaveBinlogAnnotate == false && server.IsMariaDB() {
		cluster.SetState("WARN0067", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0067"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceBinlogChecksum && server.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(server.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce ckecksum annotate on master %s", server.URL)
	} else if server.HaveChecksum == false {
		cluster.SetState("WARN0065", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0065"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if cluster.Conf.ForceBinlogCompress && server.HaveBinlogCompress == false && server.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(server.Conn)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enforce binlog compression on master %s", server.URL)
	} else if server.HaveBinlogCompress == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		cluster.SetState("WARN0068", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0068"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if server.HaveBinlogSlaveUpdates == false {
		cluster.SetState("WARN0069", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0069"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if server.HaveGtidStrictMode == false && server.DBVersion.Flavor == "MariaDB" && cluster.GetTopology() != topoMultiMasterWsrep && cluster.GetTopology() != topoMultiMasterGrouprep {
		cluster.SetState("WARN0070", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0070"], server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
	}
	if server.IsAcid() == false && cluster.IsDiscovered() {
		cluster.SetState("WARN0007", state.State{ErrType: "WARNING", ErrDesc: "At least one server is not ACID-compliant. Please make sure that sync_binlog and innodb_flush_log_at_trx_commit are set to 1", ErrFrom: "CONF", ServerUrl: server.URL})
	}
}

// CheckSlaveSameMasterGrants check same serers grants as the master
func (server *ServerMonitor) CheckSlaveSameMasterGrants() bool {
	cluster := server.ClusterGroup
	if cluster.GetMaster() == nil || server.IsIgnored() || cluster.Conf.CheckGrants == false {
		return true
	}
	for _, user := range cluster.GetMaster().Users.ToNewMap() {
		if _, ok := server.Users.CheckAndGet("'" + user.User + "'@'" + user.Host + "'"); !ok {
			cluster.SetState("ERR00056", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00056"], fmt.Sprintf("'%s'@'%s'", user.User, user.Host), server.URL), ErrFrom: "TOPO", ServerUrl: server.URL})
			return false
		}
	}
	return true
}

// CheckPrivileges replication manager user privileges on live servers
func (server *ServerMonitor) CheckPrivileges() {
	cluster := server.ClusterGroup
	if !cluster.Conf.MonitorCheckGrants {
		return
	}
	// if cluster.Conf.LogLevel > 2 {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Privilege check on %s", server.URL)
	// }
	if server.State != "" && !server.IsDown() && server.IsRelay == false {
		myhost, logs, err := dbhelper.GetHostFromConnection(server.Conn, cluster.GetDbUser(), server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlErr, "Check Privileges can't get hostname from server %s connection on %s: %s", server.State, server.URL, err)
		myip, err := misc.GetIPSafe(misc.Unbracket(myhost))
		// if cluster.Conf.LogLevel > 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Client connection found on server %s with IP %s for host %s", server.URL, myip, myhost)
		// }
		if err != nil {
			cluster.SetState("ERR00078", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], cluster.GetDbUser(), server.URL, myhost, err), ErrFrom: "CONF", ServerUrl: server.URL})
		} else {
			priv, logs, err := dbhelper.GetPrivileges(server.Conn, cluster.GetDbUser(), cluster.RepMgrHostname, myip, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, fmt.Sprintf(clusterError["ERR00005"], cluster.GetDbUser(), cluster.RepMgrHostname, err))
			if err != nil {
				cluster.SetState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], cluster.GetDbUser(), cluster.RepMgrHostname, err), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Repl_client_priv == "N" {
				cluster.SetState("ERR00006", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00006"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Super_priv == "N" {
				cluster.SetState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00008"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Reload_priv == "N" {
				cluster.SetState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00009"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
		}
		// Check replication user has correct privs.
		for _, sv2 := range cluster.Servers {
			if sv2.URL != server.URL && sv2.IsRelay == false && !sv2.IsDown() {
				rplhost, _ := misc.GetIPSafe(misc.Unbracket(sv2.Host))
				rpriv, logs, err := dbhelper.GetPrivileges(server.Conn, cluster.GetDbUser(), sv2.Host, rplhost, server.DBVersion)
				cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, fmt.Sprintf(clusterError["ERR00015"], cluster.GetRplUser(), sv2.URL, err))
				if err != nil {
					cluster.SetState("ERR00015", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00015"], cluster.GetRplUser(), sv2.URL, err), ErrFrom: "CONF", ServerUrl: sv2.URL})
				}
				if rpriv.Repl_slave_priv == "N" {
					cluster.SetState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00007"], sv2.URL), ErrFrom: "CONF", ServerUrl: sv2.URL})
				}
			}
		}
	}
}

func (server *ServerMonitor) CheckMonitoringCredentialsRotation() {
	cluster := server.GetCluster()
	if cluster.Conf.IsVaultUsed() {
		client, err := cluster.GetVaultConnection()
		if err != nil {
			//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Fail Vault connection: %v", err)
			return
		}
		//if opensvc and shard proxy clusterhead
		if server.IsCompute && cluster.Conf.ClusterHead == "" {
			_, newpass, err := cluster.GetVaultShardProxyCredentials(client)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Vault shard proxy check rotation %s , %s , %s", server.Pass, newpass, err)
			if newpass != server.Pass && err == nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Vault shard proxy is Shard proxy and clusterhead")

				cluster.SetClusterProxyCredentialsFromConfig()
				cluster.SetClusterMonitorCredentialsFromConfig()
				server.SetCredential(server.URL, cluster.GetShardUser(), cluster.GetShardPass())
				for _, u := range server.Users.ToNewMap() {
					if u.User == cluster.GetShardUser() {
						dbhelper.SetUserPassword(server.Conn, server.DBVersion, u.Host, u.User, cluster.GetShardPass())
					}

				}
			}
		} else {
			//if is database
			_, newpass, err := cluster.GetVaultMonitorCredentials(client)
			if newpass != server.Pass && err == nil {

				cluster.SetClusterMonitorCredentialsFromConfig()
				server.SetCredential(server.URL, cluster.GetDbUser(), cluster.GetDbPass())
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Vault monitoring user password rotation")
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Ping function User: %s, Pass: %s", server.User, server.Pass)

				for _, pri := range cluster.Proxies {
					if prx, ok := pri.(*ProxySQLProxy); ok {
						prx.RotateMonitoringPasswords(newpass)
					}
				}
				//upgrade openSVC secret
				err = cluster.ProvisionRotatePasswords(newpass)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Fail of ProvisionRotatePasswords during rotation password ", err)
				}
			}
		}
	}
}
