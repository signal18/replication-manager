// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strconv"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
)

// CheckMaxConnections Check 80% of max connection reach
func (server *ServerMonitor) CheckMaxConnections() {
	maxCx, _ := strconv.ParseInt(server.Variables["MAX_CONNECTIONS"], 10, 64)
	curCx, _ := strconv.ParseInt(server.Status["THREADS_CONNECTED"], 10, 64)
	if curCx > maxCx*80/100 {
		server.ClusterGroup.SetSugarState("ERR00076", "MON", server.URL, server.URL)
	}
}

func (server *ServerMonitor) CheckVersion() {

	if server.DBVersion.IsMariaDB() && ((server.DBVersion.Major == 10 && server.DBVersion.Minor == 4 && server.DBVersion.Release < 12) || (server.DBVersion.Major == 10 && server.DBVersion.Minor == 5 && server.DBVersion.Release < 1)) {
		server.ClusterGroup.SetSugarState("WARN0099", "MON", server.URL, server.URL)
	}

}

// CheckDisks check mariadb disk plugin ti see if it get free space
func (server *ServerMonitor) CheckDisks() {
	for _, d := range server.Disks {
		if d.Used/d.Total*100 > int32(server.ClusterGroup.Conf.MonitorDiskUsagePct) {
			server.ClusterGroup.SetSugarState("ERR00079", "MON", server.URL, server.URL)
		}
	}
}

// CheckReplication Check replication health and return status string
func (server *ServerMonitor) CheckReplication() string {
	if server.ClusterGroup.sme.IsInFailover() {
		return "In Failover"
	}
	if server.HaveWsrep && !server.IsFailed() {
		if server.IsWsrepSync {
			server.State = stateWsrep
			return "Galera OK"
		} else if server.IsWsrepDonor {
			server.State = stateWsrepDonor
			return "Galera OK"
		} else {
			server.State = stateWsrepLate
			return "Galera Late"
		}
	}
	if (server.IsDown()) && server.IsSlave == false {

		return "Master OK"
	}

	if server.ClusterGroup.master != nil {
		if server.ServerID == server.ClusterGroup.master.ServerID {
			return "Master OK"
		}
	}
	if server.IsMaintenance {
		server.State = stateMaintenance
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
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, IO Stopped (%s)", ss.LastIOErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "Yes" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, SQL Stopped (%s)", ss.LastSQLErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return "NOT OK, ALL Stopped"
		} else if ss.SlaveSQLRunning.String == "Connecting" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
			return "NOT OK, IO Connecting"
		}

		if server.IsRelay == false && server.IsMaxscale == false {
			server.State = stateSlave
		} else if server.IsRelay {
			server.State = stateRelay
		}
		return "Running OK"
	}

	if ss.SecondsBehindMaster.Int64 > 0 {
		if ss.SecondsBehindMaster.Int64 > server.ClusterGroup.Conf.FailMaxDelay && server.ClusterGroup.Conf.RplChecks == true {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveLate
			} else if server.IsRelay {
				server.State = stateRelayLate
			}

		} else {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
		}
		return "Behind master"
	}
	if server.IsRelay == false && server.IsMaxscale == false {
		server.State = stateSlave
	} else if server.IsRelay {
		server.State = stateRelayLate
	}
	return "Running OK"
}

// CheckSlaveSettings check slave variables & enforce if set
func (server *ServerMonitor) CheckSlaveSettings() {
	sl := server
	if server.ClusterGroup.Conf.ForceSlaveSemisync && sl.HaveSemiSync == false {
		server.ClusterGroup.LogPrintf("DEBUG", "Enforce semisync on slave %s", sl.URL)
		dbhelper.InstallSemiSync(sl.Conn)
	} else if sl.IsIgnored() == false && sl.HaveSemiSync == false {
		server.ClusterGroup.SetSugarState("WARN0048", "TOPO", sl.URL, sl.URL)
	}

	if server.ClusterGroup.Conf.ForceBinlogRow && sl.HaveBinlogRow == false {
		// In non-multimaster mode, enforce read-only flag if the option is set
		dbhelper.SetBinlogFormat(sl.Conn, "ROW")
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog format ROW on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogRow == false && server.ClusterGroup.Conf.AutorejoinFlashback == true {
		server.ClusterGroup.SetSugarState("WARN0049", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceSlaveReadOnly && sl.ReadOnly == "OFF" && !server.ClusterGroup.IsInIgnoredReadonly(server) {
		// In non-multimaster mode, enforce read-only flag if the option is set
		sl.SetReadOnly()
		server.ClusterGroup.LogPrintf("INFO", "Enforce read only on slave %s", sl.URL)
	}
	if server.ClusterGroup.Conf.ForceSlaveHeartbeat && sl.GetReplicationHearbeatPeriod() > 1 {
		dbhelper.SetSlaveHeartbeat(sl.Conn, "1", server.ClusterGroup.Conf.MasterConn, server.DBVersion)
		server.ClusterGroup.LogPrintf("INFO", "Enforce heartbeat to 1s on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationHearbeatPeriod() > 1 {
		server.ClusterGroup.SetSugarState("WARN0050", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceSlaveGtid && sl.GetReplicationUsingGtid() == "No" {
		dbhelper.SetSlaveGTIDMode(sl.Conn, "slave_pos", server.ClusterGroup.Conf.MasterConn, server.DBVersion)
		server.ClusterGroup.LogPrintf("INFO", "Enforce GTID replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.GetReplicationUsingGtid() == "No" {
		server.ClusterGroup.SetSugarState("WARN0051", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceSlaveGtidStrict && sl.IsReplicationUsingGtidStrict() == false {
		dbhelper.SetSlaveGTIDModeStrict(sl.Conn, server.DBVersion)
		server.ClusterGroup.LogPrintf("INFO", "Enforce GTID strict mode on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.IsReplicationUsingGtidStrict() == false {
		server.ClusterGroup.SetSugarState("WARN0058", "TOPO", sl.URL, sl.URL)
	}

	if server.ClusterGroup.Conf.ForceSyncInnoDB && sl.HaveInnodbTrxCommit == false {
		dbhelper.SetSyncInnodb(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce InnoDB durability on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveInnodbTrxCommit == false {
		server.ClusterGroup.SetSugarState("WARN0052", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogChecksum && sl.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce checksum on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveChecksum == false {
		server.ClusterGroup.SetSugarState("WARN0053", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogSlowqueries && sl.HaveBinlogSlowqueries == false {
		dbhelper.SetBinlogSlowqueries(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce log slow queries of replication on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogSlowqueries == false {
		server.ClusterGroup.SetSugarState("WARN0054", "TOPO", sl.URL, sl.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogAnnotate && sl.HaveBinlogAnnotate == false && server.IsMariaDB() {
		dbhelper.SetBinlogAnnotate(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce annotate on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogAnnotate == false && server.IsMariaDB() {
		server.ClusterGroup.SetSugarState("WARN0055", "TOPO", sl.URL, sl.URL)
	}

	if server.ClusterGroup.Conf.ForceBinlogCompress && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(sl.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog compression on slave %s", sl.URL)
	} else if sl.IsIgnored() == false && sl.HaveBinlogCompress == false && sl.DBVersion.IsMariaDB() && sl.DBVersion.Major >= 10 && sl.DBVersion.Minor >= 2 {
		server.ClusterGroup.SetSugarState("WARN0056", "TOPO", sl.URL, sl.URL)
	}
	if sl.IsIgnored() == false && sl.HaveBinlogSlaveUpdates == false {
		server.ClusterGroup.SetSugarState("WARN0057", "TOPO", sl.URL, sl.URL)
	}

	if server.IsAcid() == false && server.ClusterGroup.IsDiscovered() {
		server.ClusterGroup.SetSugarState("WARN0007", "CONF", sl.URL)
	}

}

// CheckMasterSettings check master variables & enforce if set
func (server *ServerMonitor) CheckMasterSettings() {
	if server.ClusterGroup.Conf.ForceSlaveSemisync && server.HaveSemiSync == false {
		server.ClusterGroup.LogPrintf("INFO", "Enforce semisync on Master %s", server.URL)
		dbhelper.InstallSemiSync(server.Conn)
	} else if server.HaveSemiSync == false {
		server.ClusterGroup.SetSugarState("WARN0060", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogRow && server.HaveBinlogRow == false {
		dbhelper.SetBinlogFormat(server.Conn, "ROW")
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog format ROW on Master %s", server.URL)
	} else if server.HaveBinlogRow == false && server.ClusterGroup.Conf.AutorejoinFlashback == true {
		server.ClusterGroup.SetSugarState("WARN0061", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceSyncBinlog && server.HaveBinlogSync == false {
		dbhelper.SetSyncBinlog(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce sync binlog on Master %s", server.URL)
	} else if server.HaveBinlogSync == false {
		server.ClusterGroup.SetSugarState("WARN0062", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceSyncInnoDB && server.HaveBinlogSync == false {
		dbhelper.SetSyncInnodb(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce innodb durability on Master %s", server.URL)
	} else if server.HaveBinlogSync == false {
		server.ClusterGroup.SetSugarState("WARN0064", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogAnnotate && server.HaveBinlogAnnotate == false && server.IsMariaDB() {
		dbhelper.SetBinlogAnnotate(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog annotate on master %s", server.URL)
	} else if server.HaveBinlogAnnotate == false && server.IsMariaDB() {
		server.ClusterGroup.SetSugarState("WARN0067", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogChecksum && server.HaveChecksum == false {
		dbhelper.SetBinlogChecksum(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce ckecksum annotate on master %s", server.URL)
	} else if server.HaveChecksum == false {
		server.ClusterGroup.SetSugarState("WARN0065", "TOPO", server.URL, server.URL)
	}
	if server.ClusterGroup.Conf.ForceBinlogCompress && server.HaveBinlogCompress == false && server.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		dbhelper.SetBinlogCompress(server.Conn)
		server.ClusterGroup.LogPrintf("INFO", "Enforce binlog compression on master %s", server.URL)
	} else if server.HaveBinlogCompress == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 2 {
		server.ClusterGroup.SetSugarState("WARN0068", "TOPO", server.URL, server.URL)
	}
	if server.HaveBinlogSlaveUpdates == false {
		server.ClusterGroup.SetSugarState("WARN0069", "TOPO", server.URL, server.URL)
	}

	if server.HaveGtidStrictMode == false && server.DBVersion.Flavor == "MariaDB" {
		server.ClusterGroup.SetSugarState("WARN0070", "TOPO", server.URL, server.URL)
	}
	if server.IsAcid() == false && server.ClusterGroup.IsDiscovered() {
		server.ClusterGroup.SetSugarState("WARN0007", "CONF", server.URL, server.URL)
	}
}

// CheckSlaveSameMasterGrants check same serers grants as the master
func (server *ServerMonitor) CheckSlaveSameMasterGrants() bool {
	if server.ClusterGroup.GetMaster() == nil || server.IsIgnored() || server.ClusterGroup.Conf.CheckGrants == false {
		return true
	}
	for _, user := range server.ClusterGroup.GetMaster().Users {
		if _, ok := server.Users["'"+user.User+"'@'"+user.Host+"'"]; !ok {
			server.ClusterGroup.SetSugarState("ERR00056", "TOPO", server.URL, fmt.Sprintf("'%s'@'%s'", user.User, user.Host), server.URL)
			return false
		}
	}
	return true
}

// CheckPrivileges replication manager user privileges on live servers
func (server *ServerMonitor) CheckPrivileges() {
	if server.ClusterGroup.Conf.LogLevel > 2 {
		server.ClusterGroup.LogPrintf(LvlDbg, "Privilege check on %s", server.URL)
	}
	if server.State != "" && !server.IsDown() && server.IsRelay == false {
		myhost, logs, err := dbhelper.GetHostFromConnection(server.Conn, server.ClusterGroup.dbUser, server.DBVersion)
		server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", LvlErr, "Check Privileges can't get hostname from server %s connection on %s: %s", server.State, server.URL, err)
		myip, err := misc.GetIPSafe(misc.Unbracket(myhost))
		if server.ClusterGroup.Conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf(LvlDbg, "Client connection found on server %s with IP %s for host %s", server.URL, myip, myhost)
		}
		if err != nil {
			server.ClusterGroup.SetState("ERR00078", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], server.ClusterGroup.dbUser, server.URL, myhost, err), ErrFrom: "CONF", ServerUrl: server.URL})
		} else {
			priv, logs, err := dbhelper.GetPrivileges(server.Conn, server.ClusterGroup.dbUser, server.ClusterGroup.repmgrHostname, myip, server.DBVersion)
			server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", LvlDbg, fmt.Sprintf(clusterError["ERR00005"], server.ClusterGroup.dbUser, server.ClusterGroup.repmgrHostname, err))
			if err != nil {
				server.ClusterGroup.SetState("ERR00005", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00005"], server.ClusterGroup.dbUser, server.ClusterGroup.repmgrHostname, err), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Repl_client_priv == "N" {
				server.ClusterGroup.SetState("ERR00006", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00006"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Super_priv == "N" {
				server.ClusterGroup.SetState("ERR00008", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00008"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
			if priv.Reload_priv == "N" {
				server.ClusterGroup.SetState("ERR00009", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00009"], server.URL), ErrFrom: "CONF", ServerUrl: server.URL})
			}
		}
		// Check replication user has correct privs.
		for _, sv2 := range server.ClusterGroup.Servers {
			if sv2.URL != server.URL && sv2.IsRelay == false && !sv2.IsDown() {
				rplhost, _ := misc.GetIPSafe(misc.Unbracket(sv2.Host))
				rpriv, logs, err := dbhelper.GetPrivileges(server.Conn, server.ClusterGroup.rplUser, sv2.Host, rplhost, server.DBVersion)
				server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", LvlDbg, fmt.Sprintf(clusterError["ERR00015"], server.ClusterGroup.rplUser, sv2.URL, err))
				if err != nil {
					server.ClusterGroup.SetState("ERR00015", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00015"], server.ClusterGroup.rplUser, sv2.URL, err), ErrFrom: "CONF", ServerUrl: sv2.URL})
				}
				if rpriv.Repl_slave_priv == "N" {
					server.ClusterGroup.SetState("ERR00007", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00007"], sv2.URL), ErrFrom: "CONF", ServerUrl: sv2.URL})
				}
			}
		}
	}
}
