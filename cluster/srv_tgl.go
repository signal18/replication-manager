// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import "github.com/signal18/replication-manager/utils/dbhelper"

func (server *ServerMonitor) SwitchMaintenance() error {
	if server.ClusterGroup.GetTopology() == topoMultiMasterWsrep || server.ClusterGroup.GetTopology() == topoMultiMasterRing {
		if server.IsVirtualMaster && server.IsMaintenance == false {
			server.ClusterGroup.SwitchOver()
		}
	}
	if server.ClusterGroup.GetTopology() == topoMultiMasterRing {
		if server.IsMaintenance {
			server.ClusterGroup.CloseRing(server)
		} else {
			server.RejoinLoop()
		}
	}
	server.IsMaintenance = !server.IsMaintenance
	server.ClusterGroup.failoverProxies()

	return nil
}

func (server *ServerMonitor) SwitchSlowQuery() {
	if server.HasLogSlowQuery() {
		dbhelper.SetSlowQueryLogOff(server.Conn)
	} else {
		dbhelper.SetSlowQueryLogOn(server.Conn)
	}
}

func (server *ServerMonitor) SwitchMetaDataLocks() {
	if server.HaveMetaDataLocksLog {
		server.UnInstallPlugin("METADATA_LOCK_INFO")
		server.HaveMetaDataLocksLog = false
	} else {
		server.InstallPlugin("METADATA_LOCK_INFO")
		server.HaveMetaDataLocksLog = true
	}
}

func (server *ServerMonitor) SwitchQueryResponseTime() {
	if server.HaveQueryResponseTimeLog {
		server.UnInstallPlugin("QUERY_RESPONSE_TIME")
		server.HaveQueryResponseTimeLog = false
	} else {
		server.InstallPlugin("QUERY_RESPONSE_TIME")
		server.ExecQueryNoBinLog("set global query_response_time_stats=1")
		server.HaveQueryResponseTimeLog = true
	}
}

func (server *ServerMonitor) SwitchSqlErrorLog() {
	if server.HaveSQLErrorLog {
		server.UnInstallPlugin("SQL_ERROR_LOG")
	} else {
		server.InstallPlugin("SQL_ERROR_LOG")
	}
}

func (server *ServerMonitor) SwitchSlowQueryCapture() {
	if !server.SlowQueryCapture {
		server.LongQueryTimeSaved = server.Variables["LONG_QUERY_TIME"]
		server.SlowQueryCapture = true
		server.SetLongQueryTime("0")

	} else {
		server.SlowQueryCapture = false
		server.SetLongQueryTime(server.LongQueryTimeSaved)

	}
}

func (server *ServerMonitor) SwitchSlowQueryCapturePFS() {
	if !server.HavePFS {
		server.ClusterGroup.LogPrintf(LvlInfo, "Could not capture queries with performance schema disable")
		return
	}
	if !server.HavePFSSlowQueryLog {
		server.ExecQueryNoBinLog("update performance_schema.setup_consumers set ENABLED='YES' WHERE NAME IN('events_statements_history_long','events_stages_history')")
	} else {
		server.ExecQueryNoBinLog("update performance_schema.setup_consumers set ENABLED='NO' WHERE NAME IN('events_statements_history_long','events_stages_history')")
	}
}

func (server *ServerMonitor) SwitchSlowQueryCaptureMode() {
	if server.Variables["LOG_OUTPUT"] == "FILE" {
		dbhelper.SetQueryCaptureMode(server.Conn, "TABLE")
	} else {
		dbhelper.SetQueryCaptureMode(server.Conn, "FILE")
	}
}

func (server *ServerMonitor) SwitchReadOnly() {
	if server.IsReadOnly() {
		server.SetReadWrite()
	} else {
		server.SetReadOnly()
	}
}
