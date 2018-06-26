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
	"errors"

	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/httplog"
	"github.com/signal18/replication-manager/slowlog"
)

func (server *ServerMonitor) GetProcessList() []dbhelper.Processlist {
	return server.FullProcessList
}

func (server *ServerMonitor) GetSchemas() ([]string, error) {
	return dbhelper.GetSchemas(server.Conn)
}

func (server *ServerMonitor) GetReplicationServerID() uint {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.MasterServerID
}

func (server *ServerMonitor) GetReplicationDelay() int64 {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	if ss.SecondsBehindMaster.Valid == false {
		return 0
	}
	return ss.SecondsBehindMaster.Int64
}

func (server *ServerMonitor) GetReplicationHearbeatPeriod() float64 {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.SlaveHeartbeatPeriod
}

func (server *ServerMonitor) GetReplicationUsingGtid() string {
	if server.IsMariaDB() {
		ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
		if sserr != nil {
			return "No"
		}
		return ss.UsingGtid.String
	} else {
		if server.HaveMySQLGTID {
			return "Yes"
		}
		return "No"
	}
}

func (server *ServerMonitor) GetReplicationMasterHost() string {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return ""
	}
	return ss.MasterHost.String
}

func (server *ServerMonitor) GetReplicationMasterPort() string {
	ss, sserr := server.GetSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return "3306"
	}
	return ss.MasterPort.String
}

func (server *ServerMonitor) GetSibling() *ServerMonitor {
	ssserver, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return nil
	}
	for _, sl := range server.ClusterGroup.Servers {
		sssib, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err != nil {
			continue
		}
		if sssib.MasterServerID == ssserver.MasterServerID && sl.ServerID != server.ServerID {
			return sl
		}
	}
	return nil
}

func (server *ServerMonitor) GetSlaveStatus(name string) (*dbhelper.SlaveStatus, error) {
	if server.Replications != nil {
		for _, ss := range server.Replications {
			if ss.ConnectionName.String == name {
				return &ss, nil
			}
		}
	}
	return nil, errors.New("Empty replications channels")
}

func (server *ServerMonitor) GetSlaveStatusLastSeen(name string) (*dbhelper.SlaveStatus, error) {
	if server.LastSeenReplications != nil {
		for _, ss := range server.LastSeenReplications {
			if ss.ConnectionName.String == name {
				return &ss, nil
			}
		}
	} else {
		return server.GetSlaveStatus(name)
	}
	return nil, errors.New("Empty replications channels")
}

func (server *ServerMonitor) GetAllSlavesStatus() []dbhelper.SlaveStatus {
	return server.Replications
}

func (server *ServerMonitor) GetMasterStatus() dbhelper.MasterStatus {
	return server.MasterStatus
}

func (server *ServerMonitor) GetLastPseudoGTID() (string, error) {
	return dbhelper.GetLastPseudoGTID(server.Conn)
}

func (server *ServerMonitor) GetBinlogPosFromPseudoGTID(GTID string) (string, string, error) {
	return dbhelper.GetBinlogEventPseudoGTID(server.Conn, GTID, server.BinaryLogFile)
}

func (server *ServerMonitor) GetBinlogPosAfterSkipNumberOfEvents(file string, pos string, skip int) (string, string, error) {
	return dbhelper.GetBinlogPosAfterSkipNumberOfEvents(server.Conn, file, pos, skip)
}

func (server *ServerMonitor) GetNumberOfEventsAfterPos(file string, pos string) (int, error) {
	return dbhelper.GetNumberOfEventsAfterPos(server.Conn, file, pos)
}

func (server *ServerMonitor) GetTableFromDict(URI string) (dbhelper.Table, error) {
	var emptyTable dbhelper.Table
	val, ok := server.DictTables[URI]
	if !ok {
		if len(server.DictTables) == 0 {
			return emptyTable, errors.New("Empty")
		} else {
			return emptyTable, errors.New("Not found")
		}
	} else {
		return val, nil
	}
}

func (server *ServerMonitor) GetVariables() map[string]string {
	return server.Variables
}

func (server *ServerMonitor) GetStatus() map[string]string {
	return server.Status
}

func (server *ServerMonitor) GetErrorLog() httplog.HttpLog {
	return server.ErrorLog
}

func (server *ServerMonitor) GetSlowLog() slowlog.SlowLog {
	return server.SlowLog
}

func (server *ServerMonitor) GetTables() map[string]dbhelper.Table {
	return server.DictTables
}

func (server *ServerMonitor) GetInnoDBStatus() map[string]string {
	return server.EngineInnoDB
}

func (server *ServerMonitor) GetTableDefinition(schema string, table string) (string, error) {
	query := "SHOW CREATE TABLE `" + schema + "`.`" + table + "`"
	var tbl, ddl string

	err := server.Conn.QueryRowx(query).Scan(&tbl, &ddl)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Failed query %s %s", query, err)
		return "", err
	}
	return ddl, nil
}
