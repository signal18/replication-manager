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
	"errors"
	"fmt"
	"hash/crc64"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) GetProcessList() []dbhelper.Processlist {
	return server.FullProcessList
}

func (server *ServerMonitor) GetSshEnv() string {
	/*
		REPLICATION_MANAGER_USER
		REPLICATION_MANAGER_PASSWORD
		REPLICATION_MANAGER_URL
		REPLICATION_MANAGER_CLUSTER_NAME
		REPLICATION_MANAGER_HOST_NAME
		REPLICATION_MANAGER_HOST_USER
		REPLICATION_MANAGER_HOST_PASSWORD
		REPLICATION_MANAGER_HOST_PORT

	*/
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	return "export REPLICATION_MANAGER_HOST_USER=\"" + server.User + "\";export REPLICATION_MANAGER_HOST_PASSWORD=\"" + server.Pass + "\";export MYSQL_ROOT_PASSWORD=\"" + server.Pass + "\";export REPLICATION_MANAGER_URL=\"https://" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + server.Host + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + server.Port + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + server.ClusterGroup.Name + "\"\n"
}

// Log Level will always be debug to prevent too verbose
func (server *ServerMonitor) GetSshLogEnv(task string) string {
	var module string = config.GetModuleNameForTask(task)

	return fmt.Sprintf("export JOB_NAME_ENV=\"%s\";export LOG_MODULE_ENV=\"%s\";export LOG_LEVEL_ENV=\"%s\";export LOG_BATCH_LINES_ENV=\"%d\"\n", task, module, config.LvlDbg, server.GetClusterConfig().JobLogBatchSize)
}

func (server *ServerMonitor) GetUniversalGtidServerID() uint64 {
	cluster := server.ClusterGroup
	if server.IsMariaDB() {
		return uint64(server.ServerID)
	}
	if server.DBVersion.IsMySQLOrPerconaGreater57() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", " %s %s", server.Variables["SERVER_UUID"], server.URL)
		return crc64.Checksum([]byte(strings.ToUpper(server.Variables["SERVER_UUID"])), server.GetCluster().GetCrcTable())

	}
	return 0
}

func (server *ServerMonitor) GetSourceClusterName() string {
	return server.SourceClusterName
}

func (server *ServerMonitor) GetProcessListReplicationLongQuery() string {
	if !server.ClusterGroup.Conf.MonitorProcessList {
		return ""
	}
	for _, q := range server.FullProcessList {
		if strings.HasPrefix(q.Command, "Slave_worker") && q.State.Valid && !strings.HasPrefix(q.State.String, "Waiting") {
			if q.Time.Valid && server.ClusterGroup.Conf.FailMaxDelay != -1 && q.Time.Float64 > float64(server.ClusterGroup.Conf.FailMaxDelay) {
				if q.Info.Valid {
					return q.Info.String
				}
			}
		}
	}
	return ""
}

func (server *ServerMonitor) GetSchemas() ([]string, string, error) {
	if server.Conn == nil {
		return nil, "", errors.New("No available connection")
	}
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

func (server *ServerMonitor) GetReplicationServerID() uint64 {
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

func (server *ServerMonitor) GetBindAddress() string {
	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.Host
	}
	return "0.0.0.0"
}

func (server *ServerMonitor) IsReplicationUsingGtidStrict() bool {
	if server.IsMariaDB() {
		if server.Variables["GTID_STRICT_MODE"] == "ON" {
			return true
		} else {
			return false
		}
	} else {
		return true
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

func (server *ServerMonitor) GetLastPseudoGTID() (string, string, error) {
	return dbhelper.GetLastPseudoGTID(server.Conn)
}

func (server *ServerMonitor) GetBinlogPosFromPseudoGTID(GTID string) (string, string, string, error) {
	return dbhelper.GetBinlogEventPseudoGTID(server.Conn, GTID, server.BinaryLogFile)
}

func (server *ServerMonitor) GetBinlogPosAfterSkipNumberOfEvents(file string, pos string, skip int) (string, string, string, error) {
	return dbhelper.GetBinlogPosAfterSkipNumberOfEvents(server.Conn, file, pos, skip)
}

func (server *ServerMonitor) GetNumberOfEventsAfterPos(file string, pos string) (int, string, error) {
	return dbhelper.GetNumberOfEventsAfterPos(server.Conn, file, pos)
}

func (server *ServerMonitor) GetTableFromDict(URI string) (v3.Table, error) {
	var emptyTable v3.Table
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

func (server *ServerMonitor) GetMetaDataLocks() []dbhelper.MetaDataLock {
	return server.MetaDataLocks
}

func (server *ServerMonitor) GetQueryResponseTime() []dbhelper.ResponseTime {
	var qrt []dbhelper.ResponseTime
	logs := ""
	var err error
	qrt, logs, err = dbhelper.GetQueryResponseTime(server.Conn, server.DBVersion)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Can't fetch Query Response Time ")
	return qrt
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

func (server *ServerMonitor) GetVariablesCaseSensitive() map[string]string {
	variables, _, _ := dbhelper.GetVariablesCase(server.Conn, server.DBVersion, "LOWER")
	return variables
}

func (server *ServerMonitor) GetQueryFromPFSDigest(digest string) (string, string, error) {
	for _, v := range server.PFSQueries {
		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status %s %s", digest, v.Digest)
		if v.Digest == digest {
			return v.Schema_name, v.Query, nil
		}
	}
	return "", "", errors.New("Query digest not found in PFS")
}

func (server *ServerMonitor) GetQueryFromSlowLogDigest(digest string) (string, string, error) {
	for _, v := range server.SlowLog.Buffer {
		if v.Digest == digest {
			return v.Db, v.Query, nil
		}
	}
	return "", "", errors.New("Query digest not found in PFS")
}

func (server *ServerMonitor) GetQueryExplain(schema string, query string) ([]dbhelper.Explain, error) {
	explainPlan, logs, err := dbhelper.GetQueryExplain(server.Conn, server.DBVersion, schema, query)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Can't get Explain %s %s ", server.URL, err)
	return explainPlan, err
}

func (server *ServerMonitor) GetQueryAnalyze(schema string, query string) (string, string, error) {
	return dbhelper.AnalyzeQuery(server.Conn, server.DBVersion, schema, query)
}

func (server *ServerMonitor) GetQueryExplainPFS(digest string) ([]dbhelper.Explain, error) {
	schema, query, err := server.GetQueryFromPFSDigest(digest)
	if err != nil {
		return nil, err
	}
	return server.GetQueryExplain(schema, query)
}

func (server *ServerMonitor) GetQueryAnalyzePFS(digest string) (string, string, error) {
	schema, query, err := server.GetQueryFromPFSDigest(digest)
	if err != nil {
		return "", "", err
	}
	return server.GetQueryAnalyze(schema, query)
}

func (server *ServerMonitor) GetQueryExplainSlowLog(digest string) ([]dbhelper.Explain, error) {
	schema, query, err := server.GetQueryFromSlowLogDigest(digest)
	if err != nil {
		return nil, err
	}
	return server.GetQueryExplain(schema, query)
}

func (server *ServerMonitor) GetQueryAnalyzeSlowLog(digest string) (string, string, error) {
	schema, query, err := server.GetQueryFromSlowLogDigest(digest)
	if err != nil {
		return "", "", err
	}
	return server.GetQueryAnalyze(schema, query)
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

func (server *ServerMonitor) GetServerConnections() int {
	res, _ := strconv.Atoi(server.Status["THREADS_RUNNING"])
	return res
}

func (server *ServerMonitor) GetStatusDelta() []dbhelper.Variable {
	var delta []dbhelper.Variable
	for k, v := range server.Status {
		//cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status %s %s", k, v)
		i1, err := strconv.ParseInt(v, 10, 64)
		if err == nil {
			i2, err2 := strconv.ParseInt(server.PrevStatus[k], 10, 64)
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status now %s %d", k, v)
			if err2 == nil && i2-i1 != 0 {
				//			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "Status prev %s %d", k, v)
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

func (server *ServerMonitor) GetPFSQueries() {
	if !(server.ClusterGroup.Conf.MonitorPFS && server.HavePFSSlowQueryLog && server.HavePFS) {
		return
	}
	if server.IsInPFSQueryCapture {
		return
	}
	server.IsInPFSQueryCapture = true
	defer func() { server.IsInPFSQueryCapture = false }()

	var err error
	logs := ""
	// GET PFS query digest
	server.PFSQueries, logs, err = dbhelper.GetQueries(server.Conn)
	server.ClusterGroup.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get queries %s %s", server.URL, err)
}

func (server *ServerMonitor) GetPFSStatements() []dbhelper.PFSQuery {
	var rows []dbhelper.PFSQuery
	for _, v := range server.PFSQueries {
		rows = append(rows, v)
	}
	sort.Sort(dbhelper.PFSQuerySorter(rows))
	return rows
}

func (server *ServerMonitor) GetPFSStatementsSlowLog() []dbhelper.PFSQuery {
	SlowPFSQueries := make(map[string]dbhelper.PFSQuery)
	for _, s := range server.SlowLog.Buffer {
		if s.Query != "" {
			if val, ok := SlowPFSQueries[s.Digest]; ok {
				val.Exec_count = val.Exec_count + 1
				sum, _ := strconv.ParseFloat(val.Exec_time_total, 64)
				val.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000+sum, 'g', 1, 64)
				avg, _ := strconv.ParseFloat(val.Exec_time_total, 64)
				avg = avg / float64(val.Exec_count)
				val.Exec_time_avg_ms.Float64 = avg
				if s.TimeMetrics["queryTime"] > val.Exec_time_max.Float64 {
					val.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
				}
				val.Value = val.Exec_time_total
				SlowPFSQueries[s.Digest] = val
			} else {
				var nval dbhelper.PFSQuery
				nval.Digest_text = dbhelper.GetQueryDigest(s.Query)
				nval.Digest = s.Digest
				nval.Query = s.Query
				nval.Last_seen = s.Timestamp
				nval.Exec_count = 1
				nval.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000, 'g', 1, 64)
				nval.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
				nval.Value = nval.Exec_time_total
				avg, _ := strconv.ParseFloat(nval.Exec_time_total, 64)
				avg = avg / float64(nval.Exec_count)
				nval.Exec_time_avg_ms.Float64 = avg
				nval.Rows_scanned = int64(s.NumberMetrics["rowsExamined"])
				nval.Rows_sent = int64(s.NumberMetrics["rowsSent"])
				SlowPFSQueries[s.Digest] = nval
				//	val.Plan_tmp_disk = s.BoolMetrics[""]
			}
		}
	}
	var rows []dbhelper.PFSQuery
	for _, v := range SlowPFSQueries {
		rows = append(rows, v)
	}
	sort.Sort(dbhelper.PFSQuerySorter(rows))
	var limits []dbhelper.PFSQuery
	i := 0
	for _, v := range rows {
		if i < 50 {
			limits = append(limits, v)

		}
		i = i + 1
	}
	return limits
}

func (server *ServerMonitor) GetSlowLog() []dbhelper.PFSQuery {
	var rows []dbhelper.PFSQuery
	for _, s := range server.SlowLog.Buffer {
		if s.Query != "" {

			var nval dbhelper.PFSQuery
			nval.Digest_text = dbhelper.GetQueryDigest(s.Query)
			nval.Digest = s.Digest
			nval.Query = s.Query
			nval.Last_seen = s.Timestamp
			nval.Exec_count = 1
			nval.Exec_time_total = strconv.FormatFloat(s.TimeMetrics["queryTime"]/1000, 'g', 1, 64)
			nval.Exec_time_max.Float64 = s.TimeMetrics["queryTime"]
			nval.Value = nval.Exec_time_total
			avg, _ := strconv.ParseFloat(nval.Exec_time_total, 64)
			avg = avg / float64(nval.Exec_count)
			nval.Exec_time_avg_ms.Float64 = avg
			nval.Rows_scanned = int64(s.NumberMetrics["rowsExamined"])
			nval.Rows_sent = int64(s.NumberMetrics["rowsSent"])
			nval.Schema_name = s.Db

			//	val.Plan_tmp_disk = s.BoolMetrics[""]

			rows = append(rows, nval)
		}
	}
	sort.Sort(dbhelper.PFSQuerySorter(rows))
	return rows
}

func (server *ServerMonitor) GetNewDBConn() (*sqlx.DB, error) {
	// get topology is call to late
	if server.ClusterGroup.Conf.MasterSlavePgStream || server.ClusterGroup.Conf.MasterSlavePgLogical {
		return sqlx.Connect("postgres", server.DSN)

	}
	conn, err := sqlx.Connect("mysql", server.DSN)
	if err != nil && server.ClusterGroup.HaveDBTLSCert {
		// Possible can't connect because of SSL key rotation try old key until server rebooted or key reloaded
		server.TLSConfigUsed = ConstTLSOldConfig
		server.SetDSN()
		conn, err := sqlx.Connect("mysql", server.DSN)
		if err == nil {
			server.ClusterGroup.SetState("ERR00080", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00080"], server.URL), ServerUrl: server.URL, ErrFrom: "MON"})
		} else {
			server.TLSConfigUsed = ConstTLSNoConfig
			server.SetDSN()
			conn, err := sqlx.Connect("mysql", server.DSN)
			if err == nil {
				// if not –require_secure_transport can still connect with no certificate MDEV-13362
				//server.ClusterGroup.SetState("ERR00081", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00081"], server.URL), ServerUrl: server.URL, ErrFrom: "MON"})
			}
			server.TLSConfigUsed = ConstTLSCurrentConfig
			server.SetDSN()
			return conn, err
		}
		//reset DNS in case the server is restarted
		server.TLSConfigUsed = ConstTLSCurrentConfig
		conn.SetConnMaxLifetime(3595 * time.Second)
		server.SetDSN()
		return conn, err
	}

	return conn, err

}

func (server *ServerMonitor) GetSlowLogTable() {
	cluster := server.ClusterGroup

	if cluster.IsInFailover() {
		return
	}
	if !server.HasLogsInSystemTables() {
		return
	}
	if server.IsDown() {
		return
	}
	if !cluster.GetConf().MonitorQueries {
		return
	}
	if server.IsInSlowQueryCapture {
		return
	}
	server.IsInSlowQueryCapture = true
	defer func() { server.IsInSlowQueryCapture = false }()

	f, err := os.OpenFile(server.Datadir+"/log/log_slow_query.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error writing slow queries %s", err)
		return
	}
	fi, _ := f.Stat()
	if fi.Size() > 100000000 {
		f.Truncate(0)
		f.Seek(0, 0)
	}
	defer f.Close()

	slowqueries := []dbhelper.LogSlow{}

	if server.DBVersion.IsMySQLOrPercona() {
		err = server.Conn.Select(&slowqueries, "SELECT FLOOR(UNIX_TIMESTAMP(start_time)) as start_time, user_host,TIME_TO_SEC(query_time) AS query_time,TIME_TO_SEC(lock_time) AS lock_time,rows_sent,rows_examined,db,last_insert_id,insert_id,server_id,sql_text,thread_id, 0 as rows_affected FROM  mysql.slow_log WHERE start_time > FROM_UNIXTIME("+strconv.FormatInt(server.MaxSlowQueryTimestamp+1, 10)+")")
	} else {
		err = server.Conn.Select(&slowqueries, "SELECT FLOOR(UNIX_TIMESTAMP(start_time)) as start_time, user_host,TIME_TO_SEC(query_time) AS query_time,TIME_TO_SEC(lock_time) AS lock_time,rows_sent,rows_examined,db,last_insert_id,insert_id,server_id,sql_text,thread_id,0 as rows_affected FROM  mysql.slow_log WHERE start_time > FROM_UNIXTIME("+strconv.FormatInt(server.MaxSlowQueryTimestamp+1, 10)+")")
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not get slow queries from table %s", err)
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
		server.MaxSlowQueryTimestamp = s.Start_time
	}
	//	server.ExecQueryNoBinLog("TRUNCATE mysql.slow_log")
}

func (server *ServerMonitor) GetTables() []v3.Table {
	return server.Tables
}

func (server *ServerMonitor) GetVTables() map[string]v3.Table {
	return server.DictTables
}

func (server *ServerMonitor) GetDictTables() []v3.Table {
	var tables []v3.Table
	if server.IsFailed() {
		return tables
	}
	for _, t := range server.DictTables {
		tables = append(tables, t)

	}
	sort.Sort(dbhelper.TableSizeSorter(tables))
	return tables
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
	cluster := server.ClusterGroup
	query := "SHOW CREATE TABLE `" + schema + "`.`" + table + "`"
	var tbl, ddl string

	err := server.Conn.QueryRowx(query).Scan(&tbl, &ddl)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed query %s %s", query, err)
		return "", err
	}
	return ddl, nil
}

func (server *ServerMonitor) GetDatabaseBasedir() string {

	if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorLocalhost {
		return server.Datadir

	} else if server.ClusterGroup.Conf.ProvOrchestrator == config.ConstOrchestratorSlapOS {
		return server.SlapOSDatadir
	}
	return server.Datadir
}

func (server *ServerMonitor) GetTablePK(schema string, table string) (string, error) {
	cluster := server.ClusterGroup
	query := "SELECT group_concat( distinct column_name) from information_schema.KEY_COLUMN_USAGE WHERE CONSTRAINT_NAME='PRIMARY' AND CONSTRAINT_SCHEMA='" + schema + "' AND TABLE_NAME='" + table + "'"
	var pk string
	err := server.Conn.QueryRowx(query).Scan(&pk)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed query %s %s", query, err)
		return "", nil
	}
	return pk, nil
}

func (server *ServerMonitor) GetVersion() *dbhelper.MySQLVersion {
	return server.DBVersion
}

func (server *ServerMonitor) GetCluster() *Cluster {
	return server.ClusterGroup
}

func (server *ServerMonitor) GetClusterConfig() config.Config {
	return server.GetCluster().Conf
}

func (server *ServerMonitor) GetGroupReplicationLocalAddress() string {
	strPort := "33061"
	return server.Host + ":" + strPort
}

func (server *ServerMonitor) GetWsrepNodeAddress() string {
	/*	strPort := "4567"*/
	return server.Host /*+ ":" + strPort*/
}

func (server *ServerMonitor) GetCPUUsageFromStats(t time.Time) (float64, error) {
	last_busy_time, _ := strconv.ParseFloat(server.WorkLoad["current"].BusyTime, 8)
	t_now := time.Now()
	elapsed := t_now.Sub(t).Seconds()
	if server.DBVersion.IsMariaDB() && last_busy_time != 0 {

		//if db using user_statistics, then we get cpu_usage from the user_statistics
		if server.HasUserStats() {
			res, _, err := dbhelper.GetCPUUsageFromUserStats(server.Conn)
			if err == nil {
				busy_time, _ := strconv.ParseFloat(res, 8)
				core, _ := strconv.ParseFloat(server.GetCluster().Conf.ProvCores, 8)
				return ((busy_time - last_busy_time) / (core * elapsed)) * 100, nil
			}
		}
		return 0, nil
	}
	return 0, errors.New("Not mariaDB version, cannot compute cpu usage")
}

func (server *ServerMonitor) GetBusyTimeFromStats() (string, error) {
	if server.DBVersion.IsMariaDB() {

		//if db using user_statistics, then we get cpu_usage from the user_statistics
		if server.HasUserStats() {
			res, _, err := dbhelper.GetCPUUsageFromUserStats(server.Conn)
			if err == nil {

				return res, nil
			}
		}
		return "", nil
	}
	return "", errors.New("Not mariaDB version, cannot compute cpu usage")
}

func (server *ServerMonitor) GetCPUUsageFromThreadsPool() float64 {
	if server.DBVersion.IsMariaDB() {
		//we compute it from status
		thread_idle, _ := strconv.ParseFloat(server.Status["THREADPOOL_IDLE_THREADS"], 8)
		thread, _ := strconv.ParseFloat(server.Status["THREADPOOL_THREADS"], 8)
		core, _ := strconv.ParseFloat(server.GetCluster().Conf.ProvCores, 8)
		return ((thread - thread_idle) / core) * 100
	}
	return -1
}
