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
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/s18log"
)

func (server *ServerMonitor) GetProcessList() []dbhelper.Processlist {
	return server.FullProcessList
}

func (server *ServerMonitor) GetSchemas() ([]string, error) {
	return dbhelper.GetSchemas(server.Conn)
}

func (server *ServerMonitor) GetPrometheusMetrics() string {
	metrics := server.GetDatabaseMetrics()
	var s string
	for _, m := range metrics {
		v := strings.Split(m.Name, ".")
		if v[2] == "pfs" {
			s = s + v[2] + "_" + v[3] + "{instance=\"" + v[1] + "\"} " + m.Value + "\n"
		} else {
			s = s + v[2] + "{instance=\"" + v[1] + "\"} " + m.Value + "\n"
		}
	}
	return s
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

func (server *ServerMonitor) GetVariables() []dbhelper.Variable {
	var variables []dbhelper.Variable
	for k, v := range server.Variables {
		var r dbhelper.Variable
		r.Variable_name = k
		r.Value = v
		variables = append(variables, r)
	}
	sort.Sort(dbhelper.VariableSorter(variables))
	return variables
}

func (server *ServerMonitor) GetPFSStatements() []dbhelper.PFSQuery {
	var rows []dbhelper.PFSQuery
	for _, v := range server.PFSQueries {
		rows = append(rows, v)
	}
	sort.Sort(dbhelper.PFSQuerySorter(rows))
	return rows
}

func (server *ServerMonitor) GetStatus() []dbhelper.Variable {
	var status []dbhelper.Variable
	for k, v := range server.Status {
		var r dbhelper.Variable
		r.Variable_name = k
		r.Value = v
		status = append(status, r)
	}
	sort.Sort(dbhelper.VariableSorter(status))
	return status
}

func (server *ServerMonitor) GetStatusDelta() []dbhelper.Variable {
	var delta []dbhelper.Variable
	for k, v := range server.Status {
		//server.ClusterGroup.LogPrintf(LvlInfo, "Status %s %s", k, v)
		i1, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			i2, err2 := strconv.ParseInt(server.PrevStatus[k], 10, 64)
			//	server.ClusterGroup.LogPrintf(LvlInfo, "Status now %s %d", k, v)
			if err2 == nil && i2-i1 != 0 {
				//			server.ClusterGroup.LogPrintf(LvlInfo, "Status prev %s %d", k, v)
				var r dbhelper.Variable
				r.Variable_name = k
				r.Value = strconv.FormatInt(i1-i2, 10)
				delta = append(delta, r)
			}
		}

	}
	return delta
}

func (server *ServerMonitor) GetErrorLog() s18log.HttpLog {
	return server.ErrorLog
}

func (server *ServerMonitor) GetSlowLog() s18log.SlowLog {
	return server.SlowLog
}

func (server *ServerMonitor) GetSlowLogTable() {

	f, err := os.OpenFile(server.ClusterGroup.Conf.WorkingDir+"/"+server.ClusterGroup.Name+"/"+server.Id+"_log_slow_query.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Error writing slow queries %s", err)
		return
	}
	fi, _ := f.Stat()
	if fi.Size() > 100000000 {
		f.Truncate(0)
		f.Seek(0, 0)
	}
	defer f.Close()

	slowqueries := []dbhelper.LogSlow{}
	err = server.Conn.Select(&slowqueries, "SELECT FLOOR(UNIX_TIMESTAMP(start_time)) as start_time, user_host,TIME_TO_SEC(query_time) AS query_time,TIME_TO_SEC(lock_time) AS lock_time,rows_sent,rows_examined,db,last_insert_id,insert_id,server_id,sql_text,thread_id,rows_affected FROM  mysql.slow_log")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Could not get slow queries from table %s", err)
	}
	for _, s := range slowqueries {

		fmt.Fprintf(f, "# User@Host: %s\n# Thread_id: %d  Schema: %s  QC_hit: No\n# Query_time: %s  Lock_time: %s  Rows_sent: %d  Rows_examined: %d\n# Rows_affected: %d\nSET timestamp=%d;\n%s;\n",
			s.User_host.String,
			s.Thread_id,
			s.Db.String,
			s.Query_time,
			s.Lock_time,
			s.Rows_sent,
			s.Rows_examined,
			s.Rows_affected,
			s.Start_time,
			strings.Replace(strings.Replace(s.Sql_text.String, "\r\n", " ", -1), "\n", " ", -1),
		)
	}
	_, err = server.Conn.Exec("set sql_log_bin=0")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Error cleaning slow queries table %s", err)
	}
	_, err = server.Conn.Exec("TRUNCATE mysql.slow_log")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "Error cleaning slow queries table %s", err)
	}
}

func (server *ServerMonitor) GetTables() []dbhelper.Table {
	return server.Tables
}
func (server *ServerMonitor) GetVTables() map[string]dbhelper.Table {
	return server.DictTables
}

func (server *ServerMonitor) GetInnoDBStatus() []dbhelper.Variable {
	var status []dbhelper.Variable
	for k, v := range server.EngineInnoDB {
		var r dbhelper.Variable
		r.Variable_name = k
		r.Value = v
		status = append(status, r)
	}
	sort.Sort(dbhelper.VariableSorter(status))
	return status

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
