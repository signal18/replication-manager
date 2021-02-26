// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func (server *ServerMonitor) IsInDelayedHost() bool {
	delayedhosts := strings.Split(server.ClusterGroup.Conf.HostsDelayed, ",")
	for _, url := range delayedhosts {
		if server.URL == url || server.Name == url {
			return true
		}
	}
	return false
}

func (server *ServerMonitor) IsMysqlDumpUValidOption(option string) bool {
	if strings.Contains(strings.ToLower(option), "system") && strings.Contains(strings.ToLower(option), "all") {
		if server.IsMySQL() {
			return false
		}
		if server.IsMariaDB() {
			if server.DBVersion.Major == 10 && server.DBVersion.Minor == 2 && server.DBVersion.Release >= 36 {
				return true
			}
			if server.DBVersion.Major == 10 && server.DBVersion.Minor == 3 && server.DBVersion.Release >= 27 {
				return true
			}
			if server.DBVersion.Major == 10 && server.DBVersion.Minor == 4 && server.DBVersion.Release >= 17 {
				return true
			}
			if server.DBVersion.Major == 10 && server.DBVersion.Minor == 5 && server.DBVersion.Release >= 8 {
				return true
			}
			if server.DBVersion.Major > 10 || (server.DBVersion.Major == 10 && server.DBVersion.Minor > 5) {
				return true
			}
		}
		return false
	}
	return true
	//BackupMysqldumpOptions
}

func (server *ServerMonitor) IsSlaveOfReplicationSource(name string) bool {

	if server.Replications != nil {

		for _, ss := range server.Replications {
			server.ClusterGroup.LogPrintf(LvlDbg, "IsSlaveOfReplicationSource check %s drop unlinked server %s ", ss.ConnectionName.String, name)
			if ss.ConnectionName.String == name {
				return true
			}
		}
	}
	return false
}

func (server *ServerMonitor) HasProvisionCookie() bool {
	if server == nil {
		return false
	}
	if _, err := os.Stat(server.Datadir + "/@cookie_prov"); os.IsNotExist(err) {
		return false
	}
	return true
}

func (server *ServerMonitor) HasWaitStartCookie() bool {
	if server == nil {
		return false
	}
	if _, err := os.Stat(server.Datadir + "/@cookie_waitstart"); os.IsNotExist(err) {
		return false
	}
	return true
}

func (server *ServerMonitor) HasWaitStopCookie() bool {
	if server == nil {
		return false
	}
	if _, err := os.Stat(server.Datadir + "/@cookie_waitstop"); os.IsNotExist(err) {
		return false
	}
	return true
}

func (server *ServerMonitor) HasRestartCookie() bool {
	if server == nil {
		return false
	}
	if _, err := os.Stat(server.Datadir + "/@cookie_restart"); os.IsNotExist(err) {
		return false
	}
	return true
}

func (server *ServerMonitor) HasReprovCookie() bool {
	if server == nil {
		return false
	}
	if _, err := os.Stat(server.Datadir + "/@cookie_reprov"); os.IsNotExist(err) {
		return false
	}
	return true
}

func (server *ServerMonitor) HasReadOnly() bool {
	return server.Variables["READ_ONLY"] == "ON"
}

func (server *ServerMonitor) HasGtidStrictMode() bool {
	return server.Variables["GTID_STRICT_MODE"] == "ON"
}

func (server *ServerMonitor) HasBinlog() bool {
	return server.Variables["LOG_BIN"] == "ON"
}

func (server *ServerMonitor) HasBinlogCompress() bool {
	return server.Variables["LOG_BIN_COMPRESS"] == "ON"
}

func (server *ServerMonitor) HasBinlogSlaveUpdates() bool {
	return server.Variables["LOG_SLAVE_UPDATES"] == "ON"
}

func (server *ServerMonitor) HasBinlogRow() bool {
	return server.Variables["BINLOG_FORMAT"] == "ROW"
}

func (server *ServerMonitor) HasBinlogRowAnnotate() bool {
	return server.Variables["BINLOG_ANNOTATE_ROW_EVENTS"] == "ON"
}

func (server *ServerMonitor) HasBinlogSlowSlaveQueries() bool {
	return server.Variables["LOG_SLOW_SLAVE_STATEMENTS"] == "ON"
}

func (server *ServerMonitor) HasInnoDBRedoLogDurable() bool {
	return server.Variables["INNODB_FLUSH_LOG_AT_TRX_COMMIT"] == "1"
}

func (server *ServerMonitor) HasBinlogDurable() bool {
	return server.Variables["SYNC_BINLOG"] == "1"
}

func (server *ServerMonitor) HasInnoDBChecksum() bool {
	return server.Variables["INNODB_CHECKSUM"] != "NONE"
}

func (server *ServerMonitor) HasWsrep() bool {
	return server.Variables["WSREP_ON"] == "ON"
}

func (server *ServerMonitor) HasEventScheduler() bool {
	return server.Variables["EVENT_SCHEDULER"] == "ON"
}

func (server *ServerMonitor) HasLogSlowQuery() bool {
	return server.Variables["SLOW_QUERY_LOG"] == "ON"
}

func (server *ServerMonitor) HasLogPFS() bool {
	return server.Variables["PERFORMANCE_SCHEMA"] == "ON"
}

func (server *ServerMonitor) HasLogsInSystemTables() bool {
	return server.Variables["LOG_OUTPUT"] == "TABLE"
}

func (server *ServerMonitor) HasLogPFSSlowQuery() bool {
	ConsumerVariables, logs, err := dbhelper.GetPFSVariablesConsumer(server.Conn)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", LvlErr, "Could not get PFS consumer %s %s", server.URL, err)
	return ConsumerVariables["SLOW_QUERY_PFS"] == "ON"
}

func (server *ServerMonitor) HasLogGeneral() bool {
	return server.Variables["GENERAL_LOG"] == "ON"
}

func (server *ServerMonitor) HasMySQLGTID() bool {

	if !(server.DBVersion.IsMySQL() || server.DBVersion.IsPercona()) {
		return false
	}
	if server.ClusterGroup.Conf.ForceSlaveNoGtid {
		return false
	}
	val := server.Variables["ENFORCE_GTID_CONSISTENCY"]
	if val == "ON" {
		return true
	}
	val = server.Variables["GTID_MODE"]
	if val == "ON" {
		return true
	}

	return false
}

func (server *ServerMonitor) HasMariaDBGTID() bool {

	if !(server.DBVersion.IsMariaDB()) {
		return false
	}
	if server.DBVersion.Major < 10 {
		return false
	}
	if server.ClusterGroup.Conf.ForceSlaveNoGtid {
		return false
	}

	return true
}

func (server *ServerMonitor) HasInstallPlugin(name string) bool {
	val, ok := server.Plugins[name]
	if !ok {
		return false
	}
	if val.Status == "ACTIVE" {
		return true
	}
	return false
}

// check if node see same master as the passed list
func (server *ServerMonitor) HasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err != nil {
			return false
		}
		ssserver, err := server.GetSlaveStatus(server.ReplicationSourceName)
		if err != nil {
			return false
		}
		if sssib.MasterServerID != ssserver.MasterServerID {
			return false
		}
	}
	return true
}

func (server *ServerMonitor) HasReplicationSQLThreadRunning() bool {
	ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return false
	}
	return ss.SlaveSQLRunning.String == "yes"
}

func (server *ServerMonitor) HasReplicationIOThreadRunning() bool {
	ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return false
	}
	return ss.SlaveIORunning.String == "yes"
}

func (sl serverList) HasAllSlavesRunning() bool {
	if len(sl) == 0 {
		return false
	}
	for _, s := range sl {
		ss, sserr := s.GetSlaveStatus(s.ReplicationSourceName)
		if sserr != nil {
			return false
		}
		if ss.SlaveSQLRunning.String != "Yes" || ss.SlaveIORunning.String != "Yes" {
			return false
		}
	}
	return true
}

/* Check Consistency parameters on server */
func (server *ServerMonitor) IsAcid() bool {
	if server.DBVersion.IsPPostgreSQL() {
		if server.Variables["FSYNC"] == "ON" && server.Variables["SYNCHRONOUS_COMMIT"] == "ON" {
			return true
		}
	} else {
		syncBin := server.Variables["SYNC_BINLOG"]
		logFlush := server.Variables["INNODB_FLUSH_LOG_AT_TRX_COMMIT"]
		if syncBin == "1" && logFlush == "1" {
			return true
		}
	}

	return false
}

func (server *ServerMonitor) HasSlaves(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err == nil {
			if server.ServerID == sssib.MasterServerID && sl.ServerID != server.ServerID {
				return true
			}
		}
	}
	return false
}

func (server *ServerMonitor) HasCycling() bool {
	currentSlave := server
	searchServerID := server.ServerID

	for range server.ClusterGroup.Servers {
		currentMaster, _ := server.ClusterGroup.GetMasterFromReplication(currentSlave)
		if currentMaster != nil {
			//	server.ClusterGroup.LogPrintf("INFO", "Cycling my current master id :%d me id:%d", currentMaster.ServerID, currentSlave.ServerID)
			if currentMaster.ServerID == searchServerID {
				return true
			} else {
				currentSlave = currentMaster
			}
		} else {
			return false
		}
	}
	return false
}

func (server *ServerMonitor) HasHighNumberSlowQueries() bool {
	if server.Variables["LONG_QUERY_TIME"] == "0" || server.Variables["LONG_QUERY_TIME"] == "0.000010" {
		return false
	}
	slowquerynow, _ := strconv.ParseInt(server.Status["SLOW_QUERIES"], 10, 64)
	slowquerybefore, _ := strconv.ParseInt(server.PrevStatus["SLOW_QUERIES"], 10, 64)
	if server.MonitorTime-server.PrevMonitorTime > 0 {
		qpssecond := (slowquerynow - slowquerybefore) / (server.MonitorTime - server.PrevMonitorTime)
		if qpssecond > 20 {
			return true
		}
	}
	return false

}

// IsDown() returns true is the server is Failed or Suspect or or auth error
func (server *ServerMonitor) IsDown() bool {
	if server.State == stateFailed || server.State == stateSuspect || server.State == stateErrorAuth {
		return true
	}
	return false

}

func (server *ServerMonitor) IsRunning() bool {
	return !server.IsDown()
}

// IFailed() returns true is the server is Failed or auth error
func (server *ServerMonitor) IsFailed() bool {
	if server.State == stateFailed || server.State == stateErrorAuth {
		return true
	}
	return false
}

func (server *ServerMonitor) IsReplicationBroken() bool {
	if server.IsSQLThreadRunning() == false || server.IsIOThreadRunning() == false {
		return true
	}
	return false
}

func (server *ServerMonitor) HasGTIDReplication() bool {
	if server.DBVersion.IsMySQLOrPercona() && server.HaveMySQLGTID == false {
		return false
	} else if server.DBVersion.IsMariaDB() && server.DBVersion.Major == 5 {
		return false
	}
	return true
}

func (server *ServerMonitor) HasReplicationIssue() bool {
	ret := server.CheckReplication()
	if ret == "Running OK" || ((ret == "NOT OK, IO Connecting" || server.IsIOThreadRunning() == false) && server.ClusterGroup.GetMaster() == nil) {
		return false
	}
	return true
}

func (server *ServerMonitor) IsIgnored() bool {
	return server.Ignored
}

func (server *ServerMonitor) IsReadOnly() bool {
	return server.HaveReadOnly
}

func (server *ServerMonitor) IsReadWrite() bool {
	return !server.HaveReadOnly
}

func (server *ServerMonitor) IsIOThreadRunning() bool {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveIORunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) IsSQLThreadRunning() bool {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveSQLRunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) IsPrefered() bool {
	return server.Prefered
}

func (server *ServerMonitor) IsMaster() bool {
	master := server.ClusterGroup.GetMaster()
	if master == nil {
		return false
	}
	if master.Id == server.Id {
		return true
	}
	return false
}

func (server *ServerMonitor) IsMySQL() bool {
	return server.DBVersion.IsMySQL()
}

func (server *ServerMonitor) IsMariaDB() bool {
	if server.DBVersion == nil {
		return true
	}
	return server.DBVersion.IsMariaDB()
}

func (server *ServerMonitor) HasSuperReadOnlyCapability() bool {
	return server.DBVersion.IsMySQLOrPerconaGreater57()
}
