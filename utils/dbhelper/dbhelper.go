// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package dbhelper

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/crc64"
	"log"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/percona/go-mysql/query"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/misc"
)

const debug = false
const (
	DDMMYYYYhhmmss = "2006-01-02 15:04:05"
)

type Plugin struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Type    string         `json:"type"`
	Library sql.NullString `json:"library"`
	License string         `json:"license"`
}

type chunk struct {
	ChunkId       uint64 `json:"chunkId"`
	ChunkMinKey   string `json:"chunkMinKey"`
	ChunkMaxKey   string `json:"chunkMaxKey"`
	ChunkCheckSum uint64 `json:"chunkCheckSum"`
}

type MetaDataLock struct {
	Thread_id     uint64         `json:"threadId" db:"THREAD_ID"`
	Lock_mode     sql.NullString `json:"lockMode" db:"LOCK_MODE"`
	Lock_duration sql.NullString `json:"lockDuration" db:"LOCK_DURATION"`
	Lock_type     sql.NullString `json:"lockType" db:"LOCK_TYPE"`
	Lock_schema   sql.NullString `json:"lockSchema" db:"TABLE_SCHEMA"`
	Lock_name     sql.NullString `json:"lockName" db:"TABLE_NAME"`
}

type ResponseTime struct {
	Time  string `json:"time" db:"TIME"`
	Count uint64 `json:"count" db:"COUNT"`
	Total string `json:"total" db:"TOTAL"`
}

type PFSQuery struct {
	Digest           string          `json:"digest"`
	Query            string          `json:"query"`
	Digest_text      string          `json:"digestText"`
	Schema_name      string          `json:"shemaName"`
	Last_seen        string          `json:"lastSeen"`
	Plan_full_scan   string          `json:"planFullScan"`
	Plan_tmp_disk    int64           `json:"planTmpDisk"`
	Plan_tmp_mem     int64           `json:"planTmpMem"`
	Exec_count       int64           `json:"execCount"`
	Err_count        int64           `json:"errCount"`
	Warn_count       int64           `json:"warnCount"`
	Exec_time_total  string          `json:"execTimeTotal"`
	Exec_time_max    sql.NullFloat64 `json:"execTimeMax"`
	Exec_time_avg_ms sql.NullFloat64 `json:"execTimeAvgMs"`
	Rows_sent        int64           `json:"rowsSent"`
	Rows_sent_avg    int64           `json:"rowsSentAvg"`
	Rows_scanned     int64           `json:"rowsScanned"`
	Value            string          `json:"value"`
}

type PFSQuerySorter []PFSQuery

func (a PFSQuerySorter) Len() int      { return len(a) }
func (a PFSQuerySorter) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a PFSQuerySorter) Less(i, j int) bool {
	l, _ := strconv.ParseFloat(a[i].Value, 64)
	r, _ := strconv.ParseFloat(a[j].Value, 64)
	return l > r
}

type TableSizeSorter []v3.Table

func (a TableSizeSorter) Len() int      { return len(a) }
func (a TableSizeSorter) Swap(i, j int) { a[i], a[j] = a[j], a[i] }
func (a TableSizeSorter) Less(i, j int) bool {
	return a[i].DataLength+a[i].IndexLength > a[j].DataLength+a[j].IndexLength
}

type Disk struct {
	Disk      string
	Path      string
	Total     int32
	Used      int32
	Available int32
}

/* replaced by v3.Table
type Table struct {
	Table_schema   string `json:"tableSchema"`
	Table_name     string `json:"tableName"`
	Engine         string `json:"engine"`
	Table_rows     int64  `json:"tableRows"`
	Data_length    int64  `json:"dataLength"`
	Index_length   int64  `json:"indexLength"`
	Table_crc      uint64 `json:"tableCrc"`
	Table_clusters string `json:"tableClusters"`
	Table_sync     string `json:"tableSync"`
}
*/

type Grant struct {
	User     string `json:"user"`
	Host     string `json:"host"`
	Password string `json:"-"`
	Hash     uint64 `json:"hash"`
}

type Event struct {
	Db      string `json:"db"`
	Name    string `json:"name"`
	Definer string `json:"definer"`
	Status  int64  `json:"status"`
}

type Processlist struct {
	Id           uint64          `json:"id" db:"Id"`
	User         string          `json:"user" db:"User"`
	Host         string          `json:"host" db:"Host"`
	Db           sql.NullString  `json:"db" db:"db"`
	Command      string          `json:"command" db:"Command"`
	Time         sql.NullFloat64 `json:"time" db:"Time"`
	TimeMs       sql.NullFloat64 `json:"timeMs" db:"Time_ms"`
	State        sql.NullString  `json:"state" db:"State"`
	Info         sql.NullString  `json:"info" db:"Info"`
	Progress     sql.NullFloat64 `json:"progress" db:"Progress"`
	RowsSent     uint64          `json:"rowsSent" db:"Rows_sent"`
	RowsExamined uint64          `json:"rowsExamined" db:"Rows_examined"`
}

type LogSlow struct {
	Start_time     int64          `db:"start_time"`
	User_host      sql.NullString `db:"user_host"`
	Query_time     string         `db:"query_time"`
	Lock_time      string         `db:"lock_time"`
	Rows_sent      int            `db:"rows_sent"`
	Rows_examined  int            `db:"rows_examined"`
	Db             sql.NullString `db:"db"`
	Last_insert_id int            `db:"last_insert_id"`
	Insert_id      int            `db:"insert_id"`
	Server_id      int            `db:"server_id"`
	Sql_text       sql.NullString `db:"sql_text"`
	Thread_id      int64          `db:"thread_id"`
	Rows_affected  int            `db:"rows_affected"`
	Digest         string
}

type SlaveHosts struct {
	Server_id uint64 `json:"serverId"`
	Host      string `json:"host"`
	Port      uint   `json:"port"`
	Master_id uint64 `json:"masterId"`
}

type MasterStatus struct {
	File             string `json:"file"`
	Position         uint   `json:"position"`
	Binlog_Do_DB     string `json:"binlogDoDB"`
	Binlog_Ignore_DB string `json:"binlogIgnoreDB"`
}

type SlaveStatus struct {
	ConnectionName       sql.NullString `db:"Connection_name" json:"connectionName"`
	ChannelName          sql.NullString `db:"Channel_Name" json:"channelName"`
	MasterHost           sql.NullString `db:"Master_Host" json:"masterHost"`
	MasterUser           sql.NullString `db:"Master_User" json:"masterUser"`
	MasterPort           sql.NullString `db:"Master_Port" json:"masterPort"`
	MasterLogFile        sql.NullString `db:"Master_Log_File" json:"masterLogFile"`
	ReadMasterLogPos     sql.NullString `db:"Read_Master_Log_Pos" json:"readMasterLogPos"`
	RelayMasterLogFile   sql.NullString `db:"Relay_Master_Log_File" json:"relayMasterLogFile"`
	SlaveIORunning       sql.NullString `db:"Slave_IO_Running" json:"slaveIoRunning"`
	SlaveSQLRunning      sql.NullString `db:"Slave_SQL_Running" json:"slaveSqlRunning"`
	ExecMasterLogPos     sql.NullString `db:"Exec_Master_Log_Pos" json:"execMasterLogPos"`
	SecondsBehindMaster  sql.NullInt64  `db:"Seconds_Behind_Master" json:"secondsBehindMaster"`
	LastIOErrno          sql.NullString `db:"Last_IO_Errno" json:"lastIoErrno"`
	LastIOError          sql.NullString `db:"Last_IO_Error" json:"lastIoError"`
	LastSQLErrno         sql.NullString `db:"Last_SQL_Errno" json:"lastSqlErrno"`
	LastSQLError         sql.NullString `db:"Last_SQL_Error" json:"lastSqlError"`
	MasterServerID       uint64         `db:"Master_Server_Id" json:"masterServerId"`
	UsingGtid            sql.NullString `db:"Using_Gtid" json:"usingGtid"`
	GtidIOPos            sql.NullString `db:"Gtid_IO_Pos" json:"gtidIoPos"`
	GtidSlavePos         sql.NullString `db:"Gtid_Slave_Pos" json:"gtidSlavePos"`
	SlaveHeartbeatPeriod float64        `db:"Slave_Heartbeat_Period" json:"slaveHeartbeatPeriod"`
	ExecutedGtidSet      sql.NullString `db:"Executed_Gtid_Set" json:"executedGtidSet"`
	RetrievedGtidSet     sql.NullString `db:"Retrieved_Gtid_Set" json:"retrievedGtidSet"`
	SlaveSQLRunningState sql.NullString `db:"Slave_SQL_Running_State" json:"slaveSQLRunningState"`
	PGExternalID         sql.NullString `db:"external_id" json:"postgresExternalId"`
	DoDomainIds          sql.NullString `db:"Replicate_Do_Domain_Ids" json:"eeplicateDoDomainIds"`
	IgnoreDomainIds      sql.NullString `db:"Replicate_Ignore_Domain_Ids" json:"replicateIgnoreDomainIds"`
	IgnoreServerIds      sql.NullString `db:"Replicate_Ignore_Server_Ids" json:"replicateIgnoreServerIds"`
}

type Privileges struct {
	Select_priv      string `json:"selectPriv"`
	Process_priv     string `json:"processPriv"`
	Super_priv       string `json:"superPriv"`
	Repl_slave_priv  string `json:"replSlavePriv"`
	Repl_client_priv string `json:"replClientPriv"`
	Reload_priv      string `json:"reloadPriv"`
}

type SpiderTableNoSync struct {
	Tbl_src      string
	Tbl_src_link string
	Tbl_dest     string
	Srv_dsync    string
	Srv_sync     string
}

type BinlogEvents struct {
	Log_name    string `db:"Log_name" json:"logName"`
	Pos         uint   `db:"Pos" json:"pos"`
	Event_type  string `db:"Event_type" json:"eventType"`
	Server_id   uint   `db:"Server_id" json:"serverId"`
	End_log_pos uint   `db:"End_log_pos" json:"endLogPos"`
	Info        string `db:"Info" json:"info"`
}

type MySQLServer struct {
	Server_name string `db:"Server_name" json:"serverName"`
	Host        string `db:"Host" json:"host"`
	Db          string `db:"Db" json:"db"`
	Username    string `db:"Username" json:"username"`
	Password    string `db:"Password" json:"password"`
	Port        uint   `db:"Port" json:"port"`
	Socket      string `db:"Socket" json:"socket"`
	Wrapper     string `db:"Wrapper" json:"wrapper"`
	Owner       string `db:"Owner" json:"owner"`
}

type Variable struct {
	Variable_name string `json:"variableName"`
	Value         string `json:"value"`
}

type Binarylogs struct {
	Log_name  string `json:"logName"`
	File_size uint   `json:"fileSize"`
	Encrypted string `json:"encrypted"` //mysql 8.0
}

type Explain struct {
	Id            uint           `db:"id" json:"id"`
	Select_type   sql.NullString `db:"select_type" json:"selectType"`
	Table         sql.NullString `db:"table" json:"table"`
	Type          sql.NullString `db:"type" json:"type"`
	Possible_keys sql.NullString `db:"possible_keys" json:"possibleKeys"`
	Key           sql.NullString `db:"key" json:"key"`
	Key_len       sql.NullString `db:"key_len" json:"keyLen"`
	Ref           sql.NullString `db:"ref" json:"ref"`
	Rows          sql.NullString `db:"rows" json:"rows"`
	Extra         sql.NullString `db:"Extra" json:"extra"`
}

type VariableSorter []Variable

func (a VariableSorter) Len() int           { return len(a) }
func (a VariableSorter) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a VariableSorter) Less(i, j int) bool { return a[i].Variable_name < a[j].Variable_name }

func MySQLConnect(user string, password string, address string, parameters ...string) (*sqlx.DB, error) {
	dsn := user + ":" + password + "@" + address + "/"
	if len(parameters) > 0 {
		dsn += "?" + parameters[0]
	}
	db, err := sqlx.Connect("mysql", dsn)
	return db, err
}

// SQLiteConnect returns a SQLite connection
func SQLiteConnect(path string) (*sqlx.DB, error) {
	db, err := sqlx.Connect("sqlite3", path+"/arbitrator.db")
	return db, err
}

func GetQueryDigest(q string) string {
	f := query.Fingerprint(q)
	return f
}

func GetAddress(host string, port string, socket string) string {
	var address string
	if host != "" {
		address = "tcp(" + host + ":" + port + ")"
	} else {
		address = "unix(" + socket + ")"
	}
	return address
}

func GetQueryExplain(db *sqlx.DB, version *MySQLVersion, schema string, query string) ([]Explain, string, error) {
	pl := []Explain{}
	var err error
	if schema != "" {
		_, err = db.Exec("USE " + schema)
	}
	stmt := "Explain " + query
	err = db.Select(&pl, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get Explain: %s", err)
	}
	return pl, stmt, nil
}

func GetMetaDataLock(db *sqlx.DB, version *MySQLVersion) ([]MetaDataLock, string, error) {
	/*	select pid from pg_locks l
		join pg_class t on l.relation = t.oid
		and t.relkind = 'r'  */
	pl := []MetaDataLock{}
	var err error
	query := "SELECT * FROM information_schema.metadata_lock_info"
	if version.IsMariaDB() {
		//MariaDB
		err = db.Select(&pl, query)
	}
	if err != nil {
		return nil, query, fmt.Errorf("ERROR: Could not get MetaDataLock: %s", err)
	}
	return pl, query, nil
}

func GetQueryResponseTime(db *sqlx.DB, version *MySQLVersion) ([]ResponseTime, string, error) {
	pl := []ResponseTime{}
	var err error
	stmt := "SELECT * FROM INFORMATION_SCHEMA.QUERY_RESPONSE_TIME"
	if version.IsMySQL() || version.IsPostgreSQL() {
		return nil, stmt, fmt.Errorf("ERROR: QUERY_RESPONSE_TIME not available on MySQL or PostgeSQL: %s", err)
	}
	err = db.Select(&pl, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get query response time: %s", err)
	}
	return pl, stmt, nil
}

func GetBinaryLogs(db *sqlx.DB, version *MySQLVersion) (map[string]uint, string, error) {

	vars := make(map[string]uint)
	query := "SHOW BINARY LOGS"
	if version.IsPostgreSQL() {
		return nil, query, fmt.Errorf("ERROR: QUERY_RESPONSE_TIME not available on PostgeSQL")
	}
	rows, err := db.Queryx(query)

	if err != nil {
		return nil, query, errors.New("Could not get binary logs: " + err.Error())
	}
	defer rows.Close()
	for rows.Next() {
		var v Binarylogs
		if version.IsMySQLOrPercona() && version.Major >= 8 {
			err = rows.Scan(&v.Log_name, &v.File_size, &v.Encrypted)
		} else {
			err = rows.Scan(&v.Log_name, &v.File_size)
		}
		if err != nil {
			return nil, query, errors.New("Could not get binary logs: " + err.Error())
		}
		vars[v.Log_name] = v.File_size
	}
	return vars, query, nil
}

func AnalyzeQuery(db *sqlx.DB, version *MySQLVersion, schema string, query string) (string, string, error) {
	var res string
	if schema != "" {
		db.Exec("USE " + schema)
	}
	stmt := "ANALYZE  FORMAT=JSON " + query
	rows, err := db.Query(stmt)
	if err != nil {
		return "", stmt, err
	}
	defer rows.Close()

	if rows.Next() {
		if err := rows.Scan(&res); err != nil {
			return res, stmt, err
		}
	}
	return res, stmt, err
}

func GetProcesslistTable(db *sqlx.DB, version *MySQLVersion) ([]Processlist, string, error) {
	pl := []Processlist{}
	var err error
	stmt := ""
	if version.IsMariaDB() {
		//MariaDB
		stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time_ms as Time, State, SUBSTRING(COALESCE(INFO_BINARY,''),1,1000) as Info, CASE WHEN Max_Stage < 2 THEN Progress ELSE (Stage-1)/Max_Stage*100+Progress/Max_Stage END AS Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command='query' ORDER BY TIME_MS DESC LIMIT 50"
	} else if version.IsMySQLOrPercona() {
		//MySQL
		stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time as Time, State, SUBSTRING(COALESCE(INFO,''),1,1000) as Info ,0 as Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command='query' ORDER BY TIME DESC LIMIT 50"
		if version.GreaterEqual("8.0") {
			stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time_ms as Time, State, SUBSTRING(COALESCE(INFO,''),1,1000) as Info ,0 as Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command='query' ORDER BY TIME DESC LIMIT 50"
		}
	} else if version.IsPostgreSQL() {
		// WHERE state <> 'idle' 		AND pid<>pg_backend_pid()
		stmt = `SELECT pid as "Id", coalesce(usename,'') as "User",coalesce(client_hostname || client_port,'') as "Host" , coalesce(datname,'') as db , coalesce(query,'') as "Command", extract(epoch from NOW()) - extract(epoch from query_start) as "Time",  coalesce(state,'') as "State",COALESCE(application_name,'')  as "Info" ,0 as "Progress"  FROM pg_stat_activity`
	}
	err = db.Select(&pl, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get processlist: %s", err)
	}
	return pl, stmt, nil
}

func GetProcesslistTableFromUser(db *sqlx.DB, version *MySQLVersion, user string) ([]Processlist, string, error) {
	pl := []Processlist{}
	var err error
	stmt := ""
	if version.IsMariaDB() {
		//MariaDB
		stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time_ms as Time, State, SUBSTRING(COALESCE(INFO_BINARY,''),1,1000) as Info, CASE WHEN Max_Stage < 2 THEN Progress ELSE (Stage-1)/Max_Stage*100+Progress/Max_Stage END AS Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE User='+ user +'"
	} else if version.IsMySQLOrPercona() {
		//MySQL
		stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time as Time, State, SUBSTRING(COALESCE(INFO,''),1,1000) as Info ,0 as Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE  User='+ user +'"
		if version.GreaterEqual("8.0") {
			stmt = "SELECT Id, User, Host, `Db` AS `db`, Command, Time_ms as Time, State, SUBSTRING(COALESCE(INFO,''),1,1000) as Info ,0 as Progress FROM INFORMATION_SCHEMA.PROCESSLIST WHERE   User='+ user +'"
		}
	} else if version.IsPostgreSQL() {
		// WHERE state <> 'idle' 		AND pid<>pg_backend_pid()
		stmt = `SELECT pid as "Id", coalesce(usename,'') as "User",coalesce(client_hostname || client_port,'') as "Host" , coalesce(datname,'') as db , coalesce(query,'') as "Command", extract(epoch from NOW()) - extract(epoch from query_start) as "Time",  coalesce(state,'') as "State",COALESCE(application_name,'')  as "Info" ,0 as "Progress"  FROM pg_stat_activity`
	}
	err = db.Select(&pl, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get processlist: %s", err)
	}
	return pl, stmt, nil
}

func GetProcesslist(db *sqlx.DB, version *MySQLVersion) ([]Processlist, string, error) {
	pl := []Processlist{}
	var err error
	query := ""
	if version.IsMariaDB() {
		//MariaDB
		query = "SHOW FULL PROCESSLIST"
	} else if version.IsPostgreSQL() {
		// WHERE state <> 'idle' 		AND pid<>pg_backend_pid()
		query = `SELECT pid as "Id", coalesce(usename,'') as "User",coalesce(client_hostname || client_port,'') as "Host" , coalesce(datname,'') as db ,COALESCE(application_name,'')  as "Command", extract(epoch from NOW()) - extract(epoch from query_start) as "Time",  coalesce(state,'') as "State", coalesce(query,'')  as "Info" ,0 as "Progress"  FROM pg_stat_activity`
	} else {
		//MySQL
		query = "SHOW FULL PROCESSLIST"
	}
	err = db.Select(&pl, query)
	if err != nil {
		return nil, query, fmt.Errorf("ERROR: Could not get processlist: %s", err)
	}
	return pl, query, nil
}

func GetServers(db *sqlx.DB) ([]MySQLServer, string, error) {
	db.MapperFunc(strings.Title)
	var err error
	ss := []MySQLServer{}
	query := "SELECT * FROM mysql.servers"
	err = db.Select(&ss, query)
	return ss, query, err
}

func GetLastPseudoGTID(db *sqlx.DB) (string, string, error) {
	var value string
	value = ""
	query := "select * from replication_manager_schema.pseudo_gtid_v"
	err := db.QueryRowx(query).Scan(&value)
	return value, query, err
}

func GetBinlogEventPseudoGTID(db *sqlx.DB, uuid string, lastfile string) (string, string, string, error) {

	lastpos := "4"
	exitloop := true
	logs := ""
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 60"
		logs += sql + "\n"
		err := db.Select(&events, sql)
		if err != nil {
			return "", "", logs, err
		}

		for _, row := range events {
			pos := strconv.FormatUint(uint64(row.Pos), 10)
			endpos := strconv.FormatUint(uint64(row.End_log_pos), 10)
			if strings.Contains(row.Info, uuid) {
				return row.Log_name, pos, logs, err
			}
			lastpos = endpos
		}
		if len(events) == 0 {
			binlogindex, _ := strconv.Atoi(strings.Split(lastfile, ".")[1])
			binlogindex = binlogindex - 1
			lastfile = strings.Split(lastfile, ".")[0] + "." + fmt.Sprintf("%06d", binlogindex)
			lastpos = "4"
		}
	}
	return "", "", logs, errors.New("Not found Psudo GTID")
}

func GetBinlogPosAfterSkipNumberOfEvents(db *sqlx.DB, file string, pos string, skip int) (string, string, string, error) {

	events := []BinlogEvents{}
	sql := "show binlog events IN '" + file + "'  from " + pos + " LIMIT " + strconv.Itoa(skip)

	err := db.Select(&events, sql)
	if err != nil {
		return "", "", sql, err
	}
	if len(events) == 0 {
		return "", "", sql, err
	}
	return events[(len(events) - 1)].Log_name, strconv.FormatUint(uint64(events[(len(events)-1)].Pos), 10), sql, err
}

func GetNumberOfEventsAfterPos(db *sqlx.DB, lastfile string, lastpos string) (int, string, error) {

	exitloop := true
	logs := ""
	ct := 0
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 1"
		logs += sql + "\n"
		err := db.Select(&events, sql)
		if err != nil {
			return 0, logs, err
		}

		for _, row := range events {
			lastfile = strconv.FormatUint(uint64(row.End_log_pos), 10)
		}
		if len(events) == 0 {
			return ct, logs, nil
		}
		ct = ct + 1
	}
	return 0, logs, errors.New("Not found Psudo GTID")
}

func GetMaxscaleVersion(db *sqlx.DB) (string, error) {
	var value string
	value = ""
	err := db.QueryRowx("Select @@maxscale_version").Scan(&value)
	return value, err
}

type ChangeMasterOpt struct {
	Host      string
	Port      string
	User      string
	Password  string
	Retry     string
	Heartbeat string
	SSL       bool
	Logfile   string
	Logpos    string
	Mode      string

	Channel         string
	PostgressDB     string
	IsDelayed       bool
	Delay           string
	DoDomainIds     string
	IgnoreDomainIds string
	IgnoreServerIds string
	//	SSLCa     string
	//	SSLCert   string
	//	SSLKey    string
}

func ChangeReplicationPassword(db *sqlx.DB, opt ChangeMasterOpt, myver *MySQLVersion) (string, error) {
	_, err := StopSlave(db, opt.Channel, myver)
	if err != nil {
		return "Stop slave error", err
	}
	masterOrSource := "MASTER"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		masterOrSource = "SOURCE"
	}
	cm := ""
	if myver.IsMariaDB() && opt.Channel != "" {
		cm += "CHANGE " + masterOrSource + " '" + opt.Channel + "' TO "
	} else {
		cm += "CHANGE  " + masterOrSource + " TO "
	}
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		cm = "CHANGE REPLICATION SOURCE TO "
	}

	if opt.Mode == "GROUP_REPL" {
		cm += masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
	} else {
		cm += " " + masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
	}

	if myver.IsMySQLOrPercona() && opt.Channel != "" {
		cm += " FOR CHANNEL '" + opt.Channel + "'"
	}
	_, err = db.Exec(cm)
	cm = strings.Replace(cm, opt.Password, "XXX", -1)
	if err != nil {
		return cm, fmt.Errorf("Change "+masterOrSource+" statement %s failed, reason: %s", cm, err)
	}
	_, err = StartSlave(db, opt.Channel, myver)
	if err != nil {
		return "Start slave error", err
	}
	return cm, nil
}

func ChangeMaster(db *sqlx.DB, opt ChangeMasterOpt, myver *MySQLVersion) (string, error) {
	//CREATE PUBLICATION alltables FOR ALL TABLES;
	/*
		Group replication we will check opt.Mode=GROUP_REPL
		The master_host is not used
		mysql> CHANGE MASTER TO MASTER_USER='rpl_user', MASTER_PASSWORD='password' \\
				      FOR CHANNEL 'group_replication_recovery';

		Or from MySQL 8.0.23:
		mysql> CHANGE REPLICATION SOURCE TO SOURCE_USER='rpl_user', SOURCE_PASSWORD='password' \\
				      FOR CHANNEL 'group_replication_recovery';
	*/
	masterOrSource := "MASTER"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
		masterOrSource = "SOURCE"
	}
	cm := ""
	if myver.IsPostgreSQL() {
		if opt.Channel == "" {
			opt.Channel = "alltables"
		}
		cm += "CREATE SUBSCRIPTION " + opt.Channel + " CONNECTION 'dbname=" + opt.PostgressDB + " host=" + misc.Unbracket(opt.Host) + " user=" + opt.User + " port=" + opt.Port + " password=" + opt.Password + " ' PUBLICATION  " + opt.Channel + " WITH (enabled=false, copy_data=false, create_slot=true)"
	} else {
		if myver.IsMariaDB() && opt.Channel != "" {
			cm += "CHANGE " + masterOrSource + " '" + opt.Channel + "' TO "
		} else {
			cm += "CHANGE  " + masterOrSource + " TO "
		}
		if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 23)) {
			cm = "CHANGE REPLICATION SOURCE TO "
		}

		if opt.Mode == "GROUP_REPL" {
			cm += masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "'"
		} else {
			cm += " " + masterOrSource + "_host='" + misc.Unbracket(opt.Host) + "', " + masterOrSource + "_port=" + opt.Port + ", " + masterOrSource + "_user='" + opt.User + "', " + masterOrSource + "_password='" + opt.Password + "', " + masterOrSource + "_connect_retry=" + opt.Retry + ", " + masterOrSource + "_heartbeat_period=" + opt.Heartbeat
		}
		if opt.IsDelayed {
			cm += " ," + masterOrSource + "_delay=" + opt.Delay
		}
		if myver.IsMariaDB() {
			if opt.DoDomainIds != "" {
				cm += " ,DO_DOMAIN_IDS=" + opt.DoDomainIds
			}
			if opt.IgnoreDomainIds != "" {
				cm += " ,IGNORE_DOMAIN_IDS=" + opt.IgnoreDomainIds
			}
			if opt.IgnoreDomainIds != "" {
				cm += " ,IGNORE_DOMAIN_IDS=" + opt.IgnoreDomainIds
			}
			if opt.IgnoreServerIds != "" {
				cm += " ,IGNORE_SERVER_IDS=" + opt.IgnoreServerIds
			}
		}
		switch opt.Mode {
		case "SLAVE_POS":
			cm += ", " + masterOrSource + "_USE_GTID=SLAVE_POS"
		case "CURRENT_POS":
			if myver.Greater("10.10.0") && myver.IsMariaDB() {
				cm += ", " + masterOrSource + "_USE_GTID=SLAVE_POS, MASTER_DEMOTE_TO_SLAVE=1"
			} else {
				cm += ", " + masterOrSource + "_USE_GTID=CURRENT_POS"
			}
		case "MXS":
			cm += ", " + masterOrSource + "_log_file='" + opt.Logfile + "', " + masterOrSource + "_log_pos=" + opt.Logpos
		case "POSITIONAL":
			cm += ", " + masterOrSource + "_log_file='" + opt.Logfile + "', " + masterOrSource + "_log_pos=" + opt.Logpos
			if myver.IsMariaDB() {
				cm += ", " + masterOrSource + "_USE_GTID=NO"
			}
		case "MASTER_AUTO_POSITION":
			cm += ", " + masterOrSource + "_AUTO_POSITION=1"
		}
		if opt.SSL {
			cm += ", " + masterOrSource + "_SSL=1"
			//cm +=, MASTER_SSL_CA='" + opt.SSLCa + "', MASTER_SSL_CERT='" + opt.SSLCert + "', MASTER_SSL_KEY=" + opt.SSLKey + "'"
		}
		if myver.IsMySQLOrPercona() && opt.Channel != "" {
			cm += " FOR CHANNEL '" + opt.Channel + "'"
		}
	}
	_, err := db.Exec(cm)
	cm = strings.Replace(cm, opt.Password, "XXX", -1)
	if err != nil {
		return cm, fmt.Errorf("Change "+masterOrSource+" statement %s failed, reason: %s", cm, err)
	}
	return cm, nil
}

func GetCPUUsageFromUserStats(db *sqlx.DB) (string, string, error) {
	db.MapperFunc(strings.Title)
	var value string
	value = ""

	query := "select SUM(BUSY_TIME) FROM INFORMATION_SCHEMA.USER_STATISTICS"

	err := db.QueryRowx(query).Scan(&value)
	return value, query, err
}

func MariaDBVersion(server string) int {
	if server == "" {
		return 0
	}
	re := regexp.MustCompile(`([0-9]+).([0-9]+).([0-9]+)*`)
	match := re.FindStringSubmatch(server)
	if len(match[1]) == 0 || len(match[2]) == 0 || len(match[3]) == 0 {
		return 0
	}
	x, _ := strconv.Atoi(match[1])
	y, _ := strconv.Atoi(match[2])
	z, _ := strconv.Atoi(match[3])
	return (x*10000 + y*100 + z)
	//return ((versionSplit[0]*10000+versionSplit[1])*100 + versionSplit[2])
}

func GetDBVersion(db *sqlx.DB) (*MySQLVersion, string, error) {
	stmt := "SELECT version()"
	var version string
	var versionComment string
	err := db.QueryRowx(stmt).Scan(&version)
	if err != nil {
		return &MySQLVersion{}, stmt, err
	}
	v, _ := NewMySQLVersion(version, "")
	if !v.IsPostgreSQL() {
		stmt = "SELECT @@version_comment"
		db.QueryRowx(stmt).Scan(&versionComment)
	}
	nv, _ := NewMySQLVersion(version, versionComment)
	return nv, stmt, nil
}

// Unused does not look like safe way or documenting it
func GetHostFromProcessList(db *sqlx.DB, user string, version *MySQLVersion) (string, string, error) {
	pl := []Processlist{}
	var err error
	logs := ""
	pl, logs, err = GetProcesslistTableFromUser(db, version, user)
	if err != nil {
		return "N/A", logs, err
	}
	for i := range pl {
		if pl[i].User == user {
			return strings.Split(pl[i].Host, ":")[0], logs, err
		}
	}
	return "N/A", logs, err
}

func GetHostFromConnection(db *sqlx.DB, user string, version *MySQLVersion) (string, string, error) {
	if version == nil {
		return "N/A", "", errors.New("No database version")
	}
	var value string
	query := "select user()"
	if version.IsPostgreSQL() {
		query = "select inet_client_addr()"
	}
	err := db.QueryRowx(query).Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get SQL User()", err)
		return "N/A", query, err
	}
	if version.IsPostgreSQL() {
		return value, query, nil
	}
	return strings.Split(value, "@")[1], query, nil

}

func GetPrivileges(db *sqlx.DB, user string, host string, ip string, myver *MySQLVersion) (Privileges, string, error) {
	db.MapperFunc(strings.Title)
	stmt := ""
	var err error
	priv := Privileges{}
	if ip == "" {
		return priv, "", errors.New("Error getting privileges for non-existent IP address")
	}

	if strings.Contains(ip, ":") {
		splitip := strings.Split(ip, ":")
		iprange1 := splitip[0] + ":%:%:%"
		iprange2 := splitip[0] + ":" + splitip[1] + ":%:%"
		iprange3 := splitip[0] + ":" + splitip[1] + ":" + splitip[2] + ":%"

		if myver.IsPostgreSQL() {
			stmt = `SELECT 'Y' as "Select_priv" ,'Y'  as "Process_priv",  CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END  as "Super_priv",  CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_slave_priv", CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_client_priv" ,CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END as "Reload_priv" FROM pg_catalog.pg_user u WHERE u.usename = '` + user + `'`
			row := db.QueryRowx(stmt)
			err = row.StructScan(&priv)
			if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
				return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
			}

		} else {
			stmt = "SELECT COALESCE(MAX(Select_priv),'N') as Select_priv, COALESCE(MAX(Process_priv),'N') as Process_priv, COALESCE(MAX(Super_priv),'N') as Super_priv, COALESCE(MAX(Repl_slave_priv),'N') as Repl_slave_priv, COALESCE(MAX(Repl_client_priv),'N') as Repl_client_priv, COALESCE(MAX(Reload_priv),'N') as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
			row := db.QueryRowx(stmt, user, host, ip, "::", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255:255.255.0", iprange1, iprange2, iprange3)
			err = row.StructScan(&priv)

			if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
				return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
			}
		}
		return priv, stmt, err
	}
	splitip := strings.Split(ip, ".")

	iprange1 := splitip[0] + ".%.%.%"
	iprange4 := splitip[0] + ".%"

	iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
	iprange5 := splitip[0] + "." + splitip[1] + ".%"

	iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"

	if myver.IsPostgreSQL() {
		stmt = `SELECT 'Y' as "Select_priv" ,'Y'  as "Process_priv",  CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END  as "Super_priv",  CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_slave_priv", CASE WHEN  u.userepl THEN 'Y' ELSE 'N' END as "Repl_client_priv" ,CASE WHEN u.usesuper THEN 'Y' ELSE 'N' END as "Reload_priv" FROM pg_catalog.pg_user u WHERE u.usename = '` + user + `'`
		row := db.QueryRowx(stmt)
		err = row.StructScan(&priv)
		if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
			return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
		}

	} else {
		stmt := "SELECT COALESCE(MAX(Select_priv),'N') as Select_priv, COALESCE(MAX(Process_priv),'N') as Process_priv, COALESCE(MAX(Super_priv),'N') as Super_priv, COALESCE(MAX(Repl_slave_priv),'N') as Repl_slave_priv, COALESCE(MAX(Repl_client_priv),'N') as Repl_client_priv, COALESCE(MAX(Reload_priv),'N') as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?,?,?)"
		row := db.QueryRowx(stmt, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3, iprange4, iprange5)
		err = row.StructScan(&priv)
		if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
			return priv, stmt, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
		}
	}
	return priv, stmt, err

}

func CheckReplicationAccount(db *sqlx.DB, pass string, user string, host string, ip string, myver *MySQLVersion) (bool, string, error) {

	stmt := ""
	if myver.IsPostgreSQL() {
		stmt = "SELECT passwd  AS pass ,passwd AS upass  FROM pg_catalog.pg_user u WHERE usename = ?"
		rows, err := db.Query(stmt, user)
		if err != nil {
			return false, stmt, err
		}
		for rows.Next() {
			var pass, upass string
			err = rows.Scan(&pass, &upass)
			if err != nil {
				return false, stmt, err
			}
			if pass != upass {
				return false, stmt, nil
			}
		}
	} else {
		db.MapperFunc(strings.Title)

		splitip := strings.Split(ip, ".")

		iprange1 := splitip[0] + ".%.%.%"
		iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
		iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"

		stmt = "SELECT STRCMP(Password) AS pass, PASSWORD(?) AS upass FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
		rows, err := db.Query(stmt, pass, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3)
		if err != nil {
			return false, stmt, err
		}
		for rows.Next() {
			var pass, upass string
			err = rows.Scan(&pass, &upass)
			if err != nil {
				return false, stmt, err
			}
			if pass != upass {
				return false, stmt, nil
			}
		}
	}
	return true, stmt, nil
}

func HaveExtraEvents(db *sqlx.DB, file string, pos string) (bool, string, error) {
	db.MapperFunc(strings.Title)
	evts := []BinlogEvents{}
	udb := db.Unsafe()
	stmt := "SHOW BINLOG EVENTS IN '" + file + "' FROM " + pos
	err := udb.Get(&evts, stmt)
	if err != nil {
		return true, stmt, err
	}
	if len(evts) == 1 {
		return false, stmt, nil
	}
	if len(evts) > 1 {
		return true, stmt, nil
	}
	return false, stmt, nil
}

func GetSlaveStatus(db *sqlx.DB, Channel string, myver *MySQLVersion) (SlaveStatus, string, error) {
	db.MapperFunc(strings.Title)
	var err error
	udb := db.Unsafe()
	ss := SlaveStatus{}
	query := ""
	if Channel == "" {

		query = "SHOW SLAVE STATUS"
		if myver.IsPostgreSQL() {
			/*		query = `select
						received_lsn ,subname "Connection_name",
						pg_walfile_name(received_lsn) as "Master_Log_File",
						(SELECT file_offset  FROM pg_walfile_name_offset(received_lsn)) as "Master_Log_Pos" ,
						CASE WHEN latest_end_lsn = received_lsn   THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master"
					from pg_catalog.pg_stat_subscription`
			*/
			query = `SELECT
							ss.subname as "Connection_name",
							ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[2],'host=') as "Master_Host",
							ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[4],'port=') as "Master_Port",
							ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[3],'user=') as "Master_User",
							'master.' || pg_walfile_name(ss.received_lsn) as "Master_Log_File",
							(SELECT file_offset  FROM pg_walfile_name_offset(ss.received_lsn)) as "Read_Master_Log_Pos" ,
							'master.' || pg_walfile_name(ss.latest_end_lsn) as "Relay_Master_Log_File",
							CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_IO_Running"  ,
							CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_SQL_Running",
								(SELECT file_offset  FROM pg_walfile_name_offset(ss.latest_end_lsn)) as "Exec_Master_Log_Pos",
							CASE WHEN latest_end_lsn = received_lsn  THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master",
							'' as  "Last_IO_Errno",
							'' as "Last_SQL_Errno",
							'' as "Last_SQL_Error" ,
							0 "Master_Server_Id",
							'Slave_Pos' as  "Using_Gtid" ,
							'0-0-' || ('x'|| replace(text(ss.received_lsn), '/' ,''))::bit(64)::bigint  as  "Gtid_IO_Pos" ,
							'0-0-' || ('x'|| replace(text(ss.latest_end_lsn), '/' ,''))::bit(64)::bigint as "Gtid_Slave_Pos" ,
							1 as "Slave_Heartbeat_Period" ,
							'' as "Slave_SQL_Running_State",
							ros.external_id
						FROM pg_replication_origin_status ros
							LEFT JOIN (
								pg_catalog.pg_stat_subscription ss
									INNER JOIN  pg_catalog.pg_subscription s
									ON ss.subname =s.subname
							) ON ros.external_id='pg_' || ss.subid::text ,
							(SELECT count(*) as nbrep FROM pg_stat_subscription) AS sqt `
		}

		err = udb.Get(&ss, query)

	} else {
		if myver.IsMariaDB() {
			query = "SHOW SLAVE '" + Channel + "' STATUS"
			err = udb.Get(&ss, query)
		} else if myver.IsMySQLOrPercona() {
			query = "SHOW SLAVE STATUS FOR CHANNEL '" + Channel + "'"
			err = udb.Get(&ss, query)
		}
	}
	//
	if ss.ChannelName.Valid {
		if ss.ChannelName.String != "" {
			ss.ConnectionName.String = ss.ChannelName.String
			ss.ConnectionName.Valid = true
		}
	}

	return ss, query, err
}

func GetChannelSlaveStatus(db *sqlx.DB, myver *MySQLVersion) ([]SlaveStatus, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	err := udb.Select(&ss, "SHOW SLAVE STATUS")
	// Unified MariaDB MySQL ConnectionName and ChannelName
	uniss := []SlaveStatus{}
	if err == nil {
		for _, s := range ss {
			if s.ChannelName.Valid {
				if s.ChannelName.String != "" {
					s.ConnectionName.String = s.ChannelName.String
					s.ConnectionName.Valid = true
				}
			}
			uniss = append(uniss, s)
		}
	}
	return uniss, "SHOW SLAVE STATUS", err
}

func GetPGSlaveStatus(db *sqlx.DB, myver *MySQLVersion) ([]SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	query := `SELECT usename, application_name,
			COALESCE(client_hostname::text, client_addr::text, ''),
			COALESCE(EXTRACT(EPOCH FROM backend_start)::bigint, 0),
			backend_xmin, COALESCE(state, ''),
			COALESCE(sent_lsn::text, ''),
			COALESCE(write_lsn::text, ''),
			COALESCE(flush_lsn::text, ''),
			COALESCE(replay_lsn::text, ''),
			COALESCE(EXTRACT(EPOCH FROM write_lag)::bigint, 0),
			COALESCE(EXTRACT(EPOCH FROM flush_lag)::bigint, 0),
			COALESCE(EXTRACT(EPOCH FROM replay_lag)::bigint, 0),
			COALESCE(sync_priority, -1),
			COALESCE(sync_state, ''),
			pid
		  FROM pg_stat_replication
		  ORDER BY pid ASC`

	err := udb.Select(&ss, query)

	return ss, err
}

func GetDisks(db *sqlx.DB, myver *MySQLVersion) ([]Disk, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []Disk{}
	query := `SELECT * FROM information_schema.DISKS`
	err := udb.Select(&ss, query)
	return ss, query, err
}

func GetMSlaveStatus(db *sqlx.DB, conn string, myver *MySQLVersion) (SlaveStatus, string, error) {

	s := SlaveStatus{}
	ss := []SlaveStatus{}
	var err error
	logs := ""

	if myver.IsMariaDB() || myver.IsPostgreSQL() {
		ss, logs, err = GetAllSlavesStatus(db, myver)
	} else {
		var s SlaveStatus
		s, logs, err = GetSlaveStatus(db, conn, myver)
		ss = append(ss, s)
	}

	for _, s := range ss {
		if s.ConnectionName.String == conn {
			return s, logs, err
		}
	}
	return s, logs, err
}

func GetAllSlavesStatus(db *sqlx.DB, myver *MySQLVersion) ([]SlaveStatus, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	var err error
	/*





		   type SlaveStatus struct {
		   	ConnectionName       sql.NullString `db:"Connection_name" json:"connectionName"`
		   	MasterHost           sql.NullString `db:"Master_Host" json:"masterHost"`
		   	MasterUser           sql.NullString `db:"Master_User" json:"masterUser"`
		   	MasterPort           sql.NullString `db:"Master_Port" json:"masterPort"`
		   	MasterLogFile        sql.NullString `db:"Master_Log_File" json:"masterLogFile"`
		   	ReadMasterLogPos     sql.NullString `db:"Read_Master_Log_Pos" json:"readMasterLogPos"`
		   	RelayMasterLogFile   sql.NullString `db:"Relay_Master_Log_File" json:"relayMasterLogFile"`
		   	SlaveIORunning       sql.NullString `db:"Slave_IO_Running" json:"slaveIoRunning"`
		   	SlaveSQLRunning      sql.NullString `db:"Slave_SQL_Running" json:"slaveSqlRunning"`
		   	ExecMasterLogPos     sql.NullString `db:"Exec_Master_Log_Pos" json:"execMasterLogPos"`
		   	SecondsBehindMaster  sql.NullInt64  `db:"Seconds_Behind_Master" json:"secondsBehindMaster"`
		   	LastIOErrno          sql.NullString `db:"Last_IO_Errno" json:"lastIoErrno"`
		   	LastIOError          sql.NullString `db:"Last_IO_Error" json:"lastIoError"`
		   	LastSQLErrno         sql.NullString `db:"Last_SQL_Errno" json:"lastSqlErrno"`
		   	LastSQLError         sql.NullString `db:"Last_SQL_Error" json:"lastSqlError"`
		   	MasterServerID       uint           `db:"Master_Server_Id" json:"masterServerId"`
		   	UsingGtid            sql.NullString `db:"Using_Gtid" json:"usingGtid"`
		   	GtidIOPos            sql.NullString `db:"Gtid_IO_Pos" json:"gtidIoPos"`
		   	GtidSlavePos         sql.NullString `db:"Gtid_Slave_Pos" json:"gtidSlavePos"`
		   	SlaveHeartbeatPeriod float64        `db:"Slave_Heartbeat_Period" json:"slaveHeartbeatPeriod"`
		   	ExecutedGtidSet      sql.NullString `db:"Executed_Gtid_Set" json:"executedGtidSet"`
		   	RetrievedGtidSet     sql.NullString `db:"Retrieved_Gtid_Set" json:"retrievedGtidSet"`
		   	SlaveSQLRunningState sql.NullString `db:"Slave_SQL_Running_State" json:"slaveSQLRunningState"`

				select * from pg_replication_origin_status

		   }
	*/

	query := "SHOW ALL SLAVES STATUS"
	//		CASE WHEN sqt.nbrep=1 THEN	 ss.subname ELSE '' END as "Connection_name",
	if myver.IsPostgreSQL() {
		query = `SELECT
								ss.subname as "Connection_name",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[2],'host=') as "Master_Host",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[4],'port=') as "Master_Port",
								ltrim((regexp_split_to_array(s.subconninfo, '\s+'))[3],'user=') as "Master_User",
								'master.' || pg_walfile_name(ss.received_lsn) as "Master_Log_File",
								(SELECT file_offset  FROM pg_walfile_name_offset(ss.received_lsn)) as "Read_Master_Log_Pos" ,
								'master.' || pg_walfile_name(ss.latest_end_lsn) as "Relay_Master_Log_File",
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_IO_Running"  ,
								CASE WHEN s.subenabled THEN 'Yes' ELSE 'No' END as "Slave_SQL_Running",
									(SELECT file_offset  FROM pg_walfile_name_offset(ss.latest_end_lsn)) as "Exec_Master_Log_Pos",
								CASE WHEN latest_end_lsn = received_lsn  THEN 0 ELSE EXTRACT(EPOCH FROM latest_end_time -last_msg_send_time) END AS "Seconds_Behind_Master",
								'' as  "Last_IO_Errno",
							  '' as "Last_SQL_Errno",
								'' as "Last_SQL_Error" ,
								0 "Master_Server_Id",
							  'Slave_Pos' as  "Using_Gtid" ,
								'0-0-' || ('x'|| replace(text(ss.received_lsn), '/' ,''))::bit(64)::bigint  as  "Gtid_IO_Pos" ,
								'0-0-' || ('x'|| replace(text(ss.latest_end_lsn), '/' ,''))::bit(64)::bigint as "Gtid_Slave_Pos" ,
								1 as "Slave_Heartbeat_Period" ,
								'' as "Slave_SQL_Running_State",
								ros.external_id
							FROM pg_replication_origin_status ros
							  LEFT JOIN (
									pg_catalog.pg_stat_subscription ss
								  	INNER JOIN  pg_catalog.pg_subscription s
									  ON ss.subname =s.subname
								) ON ros.external_id='pg_' || ss.subid::text ,
							  (SELECT count(*) as nbrep FROM pg_stat_subscription) AS sqt `
	}
	err = udb.Select(&ss, query)
	return ss, query, err
}

func SetMultiSourceRepl(db *sqlx.DB, master_host string, master_port string, master_user string, master_password string, master_filter string) (string, error) {
	crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
	checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(master_host+":"+master_port), crcTable))

	stmt := "CHANGE MASTER 'mrm_" + checksum64 + "' TO master_host='" + misc.Unbracket(master_host) + "', master_port=" + master_port + ", master_user='" + master_user + "', master_password='" + master_password + "' , master_use_gtid=slave_pos"
	logs := stmt
	_, err := db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	if master_filter != "" {

		stmt = "SET GLOBAL mrm_" + checksum64 + ".replicate_do_table='" + master_filter + "'"
		logs += "\n" + stmt
		_, err = db.Exec(stmt)
		if err != nil {
			return logs, err
		}
	}
	stmt = "START SLAVE 'mrm_" + checksum64 + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}

	return logs, err
}

func InstallSemiSync(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	stmt := "INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "INSTALL PLUGIN rpl_semi_sync_replica SONAME 'semisync_replica.so'"
	}
	logs := stmt
	_, err := db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "INSTALL PLUGIN rpl_semi_sync_source SONAME 'semisync_source.so';"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "set global rpl_semi_sync_master_enabled='ON'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "SET GLOBAL rpl_semi_sync_source_enabled=ON"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	stmt = "set global rpl_semi_sync_slave_enabled='ON'"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		stmt = "SET GLOBAL rpl_semi_sync_replica_enabled=ON"
	}
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetBinlogFormat(db *sqlx.DB, format string) (string, error) {
	query := "set global binlog_format='" + format + "'"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetBinlogAnnotate(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL binlog_annotate_row_events=ON"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL replicate_annotate_row_events=ON"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetInnoDBLockMonitor(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_status_output=ON"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return logs, err
	}
	query = "SET GLOBAL innodb_status_output_locks=ON"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func UnsetInnoDBLockMonitor(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_status_output_locks=0"
	logs := query
	_, err := db.Exec(query)

	if err != nil {
		return logs, err
	}
	query = "SET GLOBAL innodb_status_output=0"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

func SetRelayLogSpaceLimit(db *sqlx.DB, size string) (string, error) {
	query := "SET GLOBAL relay_log_space_limit=" + size
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

// SetBinlogSlowqueries Enable queries in replication to be reported in slow queries
func SetBinlogSlowqueries(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL log_slow_slave_statements=ON"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetLongQueryTime(db *sqlx.DB, querytime string) (string, error) {
	query := "SET GLOBAL long_query_time=" + querytime
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

// SetSyncBinlog Enable Binlog Durability
func SetSyncBinlog(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL sync_binlog=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

// SetSyncInnodb Enable InnoDB Durability
func SetSyncInnodb(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL innodb_flush_log_at_trx_commit=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

// SetBinlogChecksum Enable binlog checksum and check on master
func SetBinlogChecksum(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL binlog_checksum=1"
	logs := query
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}

	query = "SET GLOBAL master_verify_checksum=1"
	logs += "\n" + query
	_, err = db.Exec(query)
	if err != nil {
		return logs, err
	}
	return logs, nil
}

// SetBinlogCompress Enable MaraiDB 10.2 binlog compression
func SetBinlogCompress(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL log_bin_compress=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetSlowQueryLogOn(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL slow_query_log=1"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func SetSlowQueryLogOff(db *sqlx.DB) (string, error) {
	query := "SET GLOBAL slow_query_log=0"
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

func ResetAllSlaves(db *sqlx.DB, myver *MySQLVersion) (string, error) {

	ss := []SlaveStatus{}
	var err error
	logs := ""

	if myver.IsMariaDB() {
		ss, logs, err = GetAllSlavesStatus(db, myver)
	} else {
		var s SlaveStatus
		s, logs, err = GetSlaveStatus(db, "", myver)
		ss = append(ss, s)
	}
	if err != nil {
		return logs, err
	}

	for _, src := range ss {

		log, err := SetDefaultMasterConn(db, src.ConnectionName.String, myver)
		logs += "\n" + log
		if err != nil {
			return logs, err
		}

		if myver.IsMySQLOrPercona() {
			log, _ = StopSlave(db, src.ConnectionName.String, myver)
			logs += "\n" + log
		}
		log, err = ResetSlave(db, true, src.ConnectionName.String, myver)
		logs += "\n" + log
		if err != nil {
			return logs, err
		}
	}
	return logs, err
}

func GetMasterStatus(db *sqlx.DB, myver *MySQLVersion) (MasterStatus, string, error) {
	db.MapperFunc(strings.Title)
	ms := MasterStatus{}
	udb := db.Unsafe()
	query := "SHOW MASTER STATUS"
	if myver.IsPostgreSQL() {
		query = `select
		 	 'master.' ||	pg_walfile_name(pg_current_wal_lsn()) as "File" ,
				(SELECT file_offset  FROM pg_walfile_name_offset(pg_current_wal_lsn())) as "Position" ,
				'' as Binlog_Do_DB ,
				'' as "Binlog_Ignore_DB"`

	}
	err := udb.Get(&ms, query)
	//Binlog can be off
	if err == sql.ErrNoRows {
		return ms, query, nil
	}
	return ms, query, err
}

func GetSlaveHosts(db *sqlx.DB) (map[string]interface{}, string, error) {
	query := "SHOW SLAVE HOSTS"
	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts")
	}
	defer rows.Close()
	results := make(map[string]interface{})
	for rows.Next() {
		err = rows.MapScan(results)
		if err != nil {
			return nil, query, err
		}
	}
	return results, query, nil
}

func GetSlaveHostsArray(db *sqlx.DB) ([]SlaveHosts, string, error) {
	sh := []SlaveHosts{}
	query := "SHOW SLAVE HOSTS"
	err := db.Select(&sh, query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts array")
	}
	return sh, query, nil
}

func GetSlaveHostsDiscovery(db *sqlx.DB) ([]string, string, error) {
	slaveList := []string{}
	/* This method does not return the server ports, so we cannot rely on it for the time being. */
	query := "select host from information_schema.processlist where command ='binlog dump'"
	err := db.Select(&slaveList, query)
	if err != nil {
		return nil, query, errors.New("Could not get slave hosts from the processlist")
	}
	return slaveList, query, nil
}

func GetEventStatus(db *sqlx.DB, version *MySQLVersion) ([]Event, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()

	ss := []Event{}

	query := "SELECT /*replication-manager*/ db as Db, name as Name, definer as Definer, status+0  AS Status FROM mysql.event"
	if version.IsMySQLOrPercona() && version.Major >= 8 {
		query = "SELECT /*replication-manager*/ EVENT_SCHEMA as Db, EVENT_NAME as Name, definer as Definer, status+0  AS Status FROM information_schema.EVENTS"
	}
	err := udb.Select(&ss, query)
	if err != nil {
		return nil, query, errors.New("Could not get event status")
	}
	return ss, query, err
}

func SetEventStatus(db *sqlx.DB, ev Event, status int64) (string, error) {
	definer := strings.Split(ev.Definer, "@")
	if len(definer) != 2 {
		return "", errors.New("Incorrect definer format")
	}
	stmt := fmt.Sprintf("ALTER /*replication-manager*/ DEFINER='%s'@'%s' EVENT ", definer[0], definer[1])
	if status == 3 {
		stmt += ev.Db + "." + ev.Name + " DISABLE ON SLAVE"
	} else {
		stmt += ev.Db + "." + ev.Name + " ENABLE"
	}
	_, err := db.Exec(stmt)
	if err != nil {
		return stmt, err
	}
	return stmt, nil
}

func GetVariableSource(db *sqlx.DB, myver *MySQLVersion) string {

	var source string
	if !myver.IsMariaDB() && ((myver.Major >= 5 && myver.Minor >= 7) || myver.Major >= 6) {
		source = "performance_schema"
	} else {
		source = "information_schema"
	}
	return source
}

func GetStatus(db *sqlx.DB, myver *MySQLVersion) (map[string]string, string, error) {

	source := GetVariableSource(db, myver)
	vars := make(map[string]string)
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status"
	if myver.IsPostgreSQL() {
		query = `SELECT 'COM_QUERY' as "variable_name",  SUM(xact_commit + xact_rollback)::text as "value" FROM pg_stat_database
			UNION ALL SELECT 'COM_INSERT' as "variable_name",SUM(tup_inserted)::text as "value" FROM pg_stat_database
			UNION ALL SELECT 'COM_UPDATE' as "variable_name",SUM(tup_updated)::text as "value" FROM pg_stat_database
			UNION ALL SELECT 'COM_DELETE' as "variable_name",SUM(tup_deleted)::text as "value" FROM pg_stat_database
			UNION ALL SELECT 'COM_DEADLOCK' as "variable_name",SUM(deadlocks)::text as  "value" FROM pg_stat_database
			UNION ALL SELECT 'COM_ROLLBACK' as "variable_name",SUM(xact_rollback)::text as  "value" FROM pg_stat_database
			UNION ALL SELECT 'HANDLER_READ_RND_NEXT' as "variable_name",SUM(tup_fetched)::text as "value" FROM pg_stat_database
			UNION ALL SELECT 'CREATED_TMP_TABLES' as "variable_name",SUM(temp_files)::text as  "value" FROM pg_stat_database
			UNION ALL SELECT 'ROWS_SENT' as "variable_name",SUM(tup_returned)::text as  "value" FROM pg_stat_database
			UNION ALL SELECT 'UPTIME' as "variable_name", EXTRACT(EPOCH FROM pg_postmaster_start_time())::bigint::text  as  "value"
			UNION ALL SELECT 'THREADS_CONNECTED' as "VARIABLE_NAME",  sum(numbackends)::text  as  "value" FROM pg_stat_database
			 `
	}
	rows, err := db.Queryx(query)

	if err != nil {
		return nil, query, errors.New("Could not get status variables")
	}
	defer rows.Close()
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return nil, query, errors.New("Could not get results from status scan")
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, query, nil
}

func GetEngineInnoDBStatus(db *sqlx.DB) (string, string, error) {
	query := "SHOW ENGINE INNODB STATUS"
	rows, err := db.Query(query)
	if err != nil {
		return "", query, err
	}
	defer rows.Close()
	var typeCol, nameCol, statusCol string
	// First row should contain the necessary info. If many rows returned then it's unknown case.
	if rows.Next() {
		if err := rows.Scan(&typeCol, &nameCol, &statusCol); err != nil {
			return statusCol, query, nil
		}
	}
	return statusCol, query, err
}

func GetEngineInnoDBVariables(db *sqlx.DB) (map[string]string, string, error) {

	statusCol, logs, err := GetEngineInnoDBStatus(db)
	if err != nil {
		return nil, logs, err
	}
	vars := make(map[string]string)
	// 0 queries inside InnoDB, 0 queries in queue
	// 0 read views open inside InnoDB
	rQueries, _ := regexp.Compile(`(\d+) queries inside InnoDB, (\d+) queries in queue`)
	rViews, _ := regexp.Compile(`(\d+) read views open inside InnoDB`)
	rHistory, _ := regexp.Compile(`History list length (\d+)`)
	for _, line := range strings.Split(statusCol, "\n") {
		if data := rQueries.FindStringSubmatch(line); data != nil {
			vars["queries_inside_innodb"] = data[1]
			vars["queries_in_queue"] = data[2]
		} else if data := rViews.FindStringSubmatch(line); data != nil {
			vars["read_views_open_inside_innodb"] = data[1]

		} else if data := rHistory.FindStringSubmatch(line); data != nil {
			vars["history_list_lenght_inside_innodb"] = data[1]
		}
	}
	return vars, logs, nil
}

func EnablePFSQueries(db *sqlx.DB) (string, error) {

	query := "UPDATE setup_consumers SET ENABLED='YES' WHERE NAME IN('events_statements_history_long','events_stages_history')"
	_, err := db.Exec(query)
	return query, err
}

func DisablePFSQueries(db *sqlx.DB) (string, error) {

	query := "UPDATE setup_consumers SET ENABLED='NO' WHERE NAME IN('events_statements_history_long','events_stages_history')"
	_, err := db.Exec(query)
	return query, err
}

func GetSampleQueryFromPFS(db *sqlx.DB, Query PFSQuery) (string, error) {
	query := "SELECT COALESCE( B.SQL_TEXT,'')  as query FROM performance_schema.events_statements_history_long B WHERE B.DIGEST =''" + Query.Digest + "'"
	rows, err := db.Queryx(query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var res string
		err := rows.Scan(&res)
		if err != nil {
			return "", err
		}
		return res, nil
	}
	return "", err
}

func GetQueries(db *sqlx.DB) (map[string]PFSQuery, string, error) {

	vars := make(map[string]PFSQuery)
	query := "set session group_concat_max_len=2048"
	db.Exec(query)
	/*	COALESCE((SELECT B.SQL_TEXT FROM performance_schema.events_statements_history_long B WHERE
		A.DIGEST = B.DIGEST LIMIT 1 ),'')  as query, */
	// to expensive FULL SCAN to extact during explain
	query = `SELECT /*replication-manager*/
	A.digest as digest,
	'' as query,
	A.digest_text as digest_text,
	A.LAST_SEEN as last_seen,
	COALESCE(A.SCHEMA_NAME,'') as schema_name,
	IF(A.SUM_NO_GOOD_INDEX_USED > 0 OR A.SUM_NO_INDEX_USED > 0, '*', '') AS plan_full_scan,
	A.SUM_CREATED_TMP_DISK_TABLES as plan_tmp_disk,
	A.SUM_CREATED_TMP_TABLES as plan_tmp_mem,
	A.COUNT_STAR AS exec_count,
  A.SUM_ERRORS AS err_count,
	A.SUM_WARNINGS AS warn_count,
	SEC_TO_TIME(A.SUM_TIMER_WAIT/1000000000000) AS exec_time_total,
	(A.MAX_TIMER_WAIT/1000000000000) AS exec_time_max,
	(A.AVG_TIMER_WAIT/1000000000000) AS exec_time_avg,
	A.SUM_ROWS_SENT AS rows_sent,
	ROUND(A.SUM_ROWS_SENT / A.COUNT_STAR) AS rows_sent_avg,
	A.SUM_ROWS_EXAMINED AS rows_scanned,
	round(A.sum_timer_wait/1000000000000, 6) as value
	FROM performance_schema.events_statements_summary_by_digest A
	WHERE A.digest_text is not null`

	// Do not order as it's eavy fot temporary directory
	//ORDER BY A.sum_timer_wait desc
	//LIMIT 50`

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get queries")
	}
	defer rows.Close()
	for rows.Next() {
		var v PFSQuery
		err := rows.Scan(&v.Digest, &v.Query, &v.Digest_text, &v.Last_seen, &v.Schema_name, &v.Plan_full_scan, &v.Plan_tmp_disk, &v.Plan_tmp_mem, &v.Exec_count, &v.Err_count, &v.Warn_count, &v.Exec_time_total, &v.Exec_time_max, &v.Exec_time_avg_ms, &v.Rows_sent, &v.Rows_sent_avg, &v.Rows_scanned, &v.Value)
		if err != nil {
			return nil, query, errors.New("Could not get results from status scan")
		}
		vars[v.Digest] = v
	}
	return vars, query, nil
}

func GetTableChecksumResult(db *sqlx.DB) (map[uint64]chunk, string, error) {
	vars := make(map[uint64]chunk)
	query := "SELECT /*replication-manager*/ * from replication_manager_schema.table_checksum"
	rows, err := db.Queryx(query)
	if err != nil {
		return vars, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v chunk
		err = rows.Scan(&v.ChunkId, &v.ChunkMinKey, &v.ChunkMaxKey, &v.ChunkCheckSum)
		if err != nil {
			return vars, query, err
		}
		vars[v.ChunkId] = v
	}
	return vars, query, nil
}

func GetPlugins(db *sqlx.DB, myver *MySQLVersion) (map[string]Plugin, string, error) {

	vars := make(map[string]Plugin)
	query := `SHOW PLUGINS`
	if myver.IsMariaDB() {
		query = `SHOW PLUGINS soname`
	}

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get queries")
	}
	defer rows.Close()
	for rows.Next() {
		var v Plugin
		err := rows.Scan(&v.Name, &v.Status, &v.Type, &v.Library, &v.License)
		if err != nil {
			return nil, query, errors.New("Could not get results from plugins scan")
		}
		vars[v.Name] = v
	}
	return vars, query, nil
}

func GetStatusAsInt(db *sqlx.DB, myver *MySQLVersion) (map[string]int64, string, error) {
	type Variable struct {
		Variable_name string
		Value         int64
	}
	vars := make(map[string]int64)
	source := GetVariableSource(db, myver)
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status"
	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get status variables as integers")
	}
	defer rows.Close()
	for rows.Next() {
		var v Variable
		rows.Scan(&v.Variable_name, &v.Value)
		vars[v.Variable_name] = v.Value
	}
	return vars, query, nil
}

func GetVariables(db *sqlx.DB, myver *MySQLVersion) (map[string]string, string, error) {
	return GetVariablesCase(db, myver, "UPPER")
}

func GetVariablesCase(db *sqlx.DB, myver *MySQLVersion, vcase string) (map[string]string, string, error) {

	source := GetVariableSource(db, myver)
	vars := make(map[string]string)

	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, Variable_Value AS value FROM " + source + ".global_variables"
	if vcase == "UPPER" {
		query = "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_variables"
	}
	if myver.IsPostgreSQL() {
		query = "SELECT upper(name) AS variable_name, setting AS value FROM pg_catalog.pg_settings UNION ALL Select 'SERVER_ID' as variable_name, system_identifier::text as value FROM pg_control_system()"
		if vcase == "UPPER" {
			query = "SELECT upper(name) AS variable_name, upper(setting) AS value FROM pg_catalog.pg_settings UNION ALL Select 'SERVER_ID' as variable_name, system_identifier::text as value FROM pg_control_system()"
		}
	}
	rows, err := db.Queryx(query)
	if err != nil {
		return vars, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Variable
		err = rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return vars, query, err
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, query, err
}

func GetPFSVariablesConsumer(db *sqlx.DB) (map[string]string, string, error) {

	vars := make(map[string]string)
	query := "SELECT /*replication-manager*/ 'SLOW_QUERY_PFS' AS variable_name, IF(count(*)>0,'OFF','ON') AS VALUE from performance_schema.setup_consumers  WHERE NAME IN('events_statements_history_long','events_stages_history') AND ENABLED='NO'"
	rows, err := db.Queryx(query)
	if err != nil {
		return vars, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Variable
		err = rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return vars, query, err
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, query, err
}

func GetNoBlockOnMedataLock(db *sqlx.DB, myver *MySQLVersion) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	noBlockOnMedataLock := "/*replication-manager*/ "
	if myver.IsMariaDB() && ((myver.Major == 10 && myver.Minor > 0) || myver.Major > 10) {
		noBlockOnMedataLock += "SET STATEMENT LOCK_WAIT_TIMEOUT=0 FOR "
	}
	return noBlockOnMedataLock
}
func GetTables(db *sqlx.DB, myver *MySQLVersion) (map[string]v3.Table, []v3.Table, string, error) {
	vars := make(map[string]v3.Table)
	var tblList []v3.Table

	logs := ""
	query := GetNoBlockOnMedataLock(db, myver) + "SELECT SCHEMA_NAME from information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN('information_schema','mysql','performance_schema', 'sys') AND SCHEMA_NAME NOT LIKE '#%'"
	if myver.IsPostgreSQL() {
		query = `SELECT SCHEMA_NAME AS "SCHEMA_NAME" FROM information_schema.schemata  WHERE SCHEMA_NAME not in ('information_schema','pg_catalog')`
	}
	databases, err := db.Queryx(query)
	if err != nil {
		return nil, nil, query, errors.New("Could not get table list")
	}
	defer databases.Close()
	logs += query

	for databases.Next() {
		var schema string
		err = databases.Scan(&schema)
		if err != nil {
			return vars, tblList, query, err
		}
		query := GetNoBlockOnMedataLock(db, myver) + "SELECT a.TABLE_SCHEMA as Table_schema ,  a.TABLE_NAME as Table_name, COALESCE(a.ENGINE,'') as Engine,COALESCE(a.TABLE_ROWS,0) as Table_rows ,COALESCE(a.DATA_LENGTH,0) as Data_length,COALESCE(a.INDEX_LENGTH,0) as Index_length , 0 as Table_crc FROM information_schema.TABLES a WHERE a.TABLE_TYPE='BASE TABLE' AND  a.TABLE_SCHEMA='" + schema + "'"
		if myver.IsPostgreSQL() {
			query = `SELECT a.schemaname as "Table_schema" ,  a.tablename as "Table_name" ,'postgres' as "Engine",COALESCE(b.n_live_tup,0) as "Table_rows" ,0 as "Data_length",0 as "Index_length" , 0 as "Table_crc"  FROM pg_catalog.pg_tables  a LEFT JOIN pg_catalog.pg_stat_user_tables b ON (a.schemaname=b.schemaname AND a.tablename=b.relname )  WHERE  a.schemaname='` + schema + `'`
		}
		logs += "\n" + query

		rows, err := db.Queryx(query)

		//	rows, err := db.Queryx("SELECT a.TABLE_SCHEMA as Table_schema ,  a.TABLE_NAME as Table_name ,a.ENGINE as Engine,a.TABLE_ROWS as Table_rows ,COALESCE(a.DATA_LENGTH,0) as Data_length,COALESCE(a.INDEX_LENGTH,0) as Index_length ,COALESCE((select CONV(LEFT(MD5(group_concat(concat(b.column_name,b.column_type,COALESCE(b.is_nullable,''),COALESCE(b.CHARACTER_SET_NAME,''), COALESCE(b.COLLATION_NAME,''),COALESCE(b.COLUMN_DEFAULT,''),COALESCE(c.CONSTRAINT_NAME,''),COALESCE(c.ORDINAL_POSITION,'')))), 16), 16, 10)    FROM information_schema.COLUMNS b left join information_schema.KEY_COLUMN_USAGE c ON b.table_schema=c.table_schema  and  b.table_name=c.table_name where b.table_schema=a.table_schema  and  b.table_name=a.table_name ),0) as Table_crc FROM information_schema.TABLES a WHERE a.TABLE_TYPE='BASE TABLE' and a.TABLE_SCHEMA NOT IN('information_schema','mysql','performance_schema')")
		if err != nil {
			return nil, nil, logs, errors.New("Could not get table list : " + err.Error())
		}
		defer rows.Close()
		crc64Table := crc64.MakeTable(0xC96C5795D7870F42)
		for rows.Next() {
			var v v3.Table

			err = rows.Scan(&v.TableSchema, &v.TableName, &v.Engine, &v.TableRows, &v.DataLength, &v.IndexLength, &v.TableCrc)
			if err != nil {
				return vars, tblList, logs, err
			}
			//This produce 12 temp table on disk
			/*	query := "SELECT COALESCE(CONV(LEFT(MD5(group_concat(concat(b.column_name,b.column_type,COALESCE(b.is_nullable,''),COALESCE(b.CHARACTER_SET_NAME,''), COALESCE(b.COLLATION_NAME,''),COALESCE(b.COLUMN_DEFAULT,''),COALESCE(c.CONSTRAINT_NAME,''),COALESCE(c.ORDINAL_POSITION,'')))), 16), 16, 10),0)  FROM information_schema.COLUMNS b inner join information_schema.KEY_COLUMN_USAGE c ON b.table_schema=c.table_schema  AND  b.table_name=c.table_name where b.table_schema='" + schema + "' AND  b.table_name='" + v.Table_name + "'"
				err = db.QueryRowx(query).Scan(&crcTable)
			*/

			query := GetNoBlockOnMedataLock(db, myver) + "SHOW CREATE TABLE `" + schema + "`.`" + v.TableName + "`"
			if myver.IsPostgreSQL() {
				query = "SELECT 'CREATE TABLE `" + schema + "`.`" + v.TableName + "` (' || E'\n'|| '' || string_agg(column_list.column_expr, ', ' || E'\n' || '') ||   '' || E'\n' || ') ENGINE=postgress;' FROM (   SELECT '    `' || column_name || '` ' || data_type ||   coalesce('(' || character_maximum_length || ')', '') ||   case when is_nullable = 'YES' then '' else ' NOT NULL' end as column_expr  FROM information_schema.columns  WHERE table_schema = '" + schema + "' AND table_name = '" + v.TableName + "' ORDER BY ordinal_position) column_list"
			}
			logs += "\n" + query
			var tbl, ddl string
			err := db.QueryRowx(query).Scan(&tbl, &ddl)
			if err == nil {
				//	v.Table_crc = crcTable
				pos := strings.Index(ddl, "ENGINE=")
				ddl = ddl[12:pos]
				crc64Int := crc64.Checksum([]byte(ddl), crc64Table)
				v.TableCrc = crc64Int
			}
			tblList = append(tblList, v)
			vars[v.TableSchema+"."+v.TableName] = v
		}
		rows.Close()
	}
	return vars, tblList, logs, nil
}

func GetUsers(db *sqlx.DB, myver *MySQLVersion) (map[string]Grant, string, error) {
	vars := make(map[string]Grant)
	// password was remover from system table in mysql 8.0

	query := "SELECT user, host, password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10)    FROM mysql.user where host<>'localhost' "
	if myver.IsPostgreSQL() {
		query = "SELECT usename as user , '%' as host , 'unknow'  as password, 0 FROM pg_catalog.pg_user"
	} else if (myver.IsMySQL() || myver.IsPercona()) && (myver.Major > 7 || (myver.Major == 5 && myver.Minor >= 7)) {
		if myver.Major > 7 {
			query = "SELECT user, host, authentication_string as password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10)  FROM mysql.user"
		} else {
			query = "SELECT user, host, '****' as password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10)    FROM mysql.user"
		}
	}

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get DB user list")
	}
	defer rows.Close()
	for rows.Next() {
		var g Grant
		err = rows.Scan(&g.User, &g.Host, &g.Password, &g.Hash)
		if err != nil {
			return vars, query, err
		}
		vars["'"+g.User+"'@'"+g.Host+"'"] = g
	}
	return vars, query, nil
}

func GetProxySQLUsers(db *sqlx.DB) (map[string]Grant, string, error) {
	vars := make(map[string]Grant)
	query := "SELECT username, password  FROM mysql_users"
	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get proxySQL user list")
	}
	defer rows.Close()
	for rows.Next() {
		var g Grant
		err = rows.Scan(&g.User, &g.Password)
		if err != nil {
			return vars, query, err
		}
		vars[g.User+":"+g.Password] = g
	}
	return vars, query, nil
}

func GetSchemas(db *sqlx.DB) ([]string, string, error) {
	sch := []string{}
	query := "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE  SCHEMA_NAME NOT IN('information_schema','mysql','performance_schema','sys') AND SCHEMA_NAME NOT LIKE '#%' "
	err := db.Select(&sch, query)
	if err != nil {
		return nil, query, errors.New("Could not get table list")
	}
	return sch, query, nil
}

func GetSchemasMap(db *sqlx.DB) (map[string]string, string, error) {
	query := "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE  SCHEMA_NAME NOT IN('information_schema','mysql','performance_schema')"
	schemas := make(map[string]string)
	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, errors.New("Could not get schema list")
	}
	defer rows.Close()
	for rows.Next() {
		var schema string
		err = rows.Scan(&schema)
		if err != nil {
			return schemas, query, err
		}
		schemas[schema] = schema
	}
	return schemas, query, nil
}

func GetVariableByName(db *sqlx.DB, name string, myver *MySQLVersion) (string, string, error) {
	var value string
	source := GetVariableSource(db, myver)
	query := "SELECT Variable_Value AS Value FROM " + source + ".global_variables WHERE Variable_Name = ?"
	err := db.QueryRowx(query, name).Scan(&value)
	if err != nil {
		return "", query, errors.New("Could not get variable by name")
	}
	return value, query, nil
}

func GetVariableByNameToUpper(db *sqlx.DB, name string, myver *MySQLVersion) (string, string, error) {
	var value string
	source := GetVariableSource(db, myver)
	query := "SELECT UPPER(Variable_Value) AS Value FROM " + source + ".global_variables WHERE Variable_Name = ?"
	err := db.QueryRowx(query, name).Scan(&value)
	if err != nil {
		return "", query, errors.New("Could not get variable by name")
	}
	return value, query, nil
}

func FlushBinaryLogsLocal(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH LOCAL BINARY LOGS")
	return "FLUSH LOCAL BINARY LOGS", err
}

func FlushBinaryLogs(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH  BINARY LOGS")
	return "FLUSH BINARY LOGS", err
}

func FlushTables(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH TABLES")
	return "FLUSH TABLES", err
}

func FlushTablesNoLog(db *sqlx.DB) (string, error) {
	_, err := db.Exec("FLUSH NO_WRITE_TO_BINLOG TABLES")
	return "FLUSH NO_WRITE_TO_BINLOG TABLES", err
}

func MariaDBFlushTablesNoLogTimeout(db *sqlx.DB, timeout string) (string, error) {
	query := "SET STATEMENT max_statement_time=" + timeout + " FOR FLUSH NO_WRITE_TO_BINLOG TABLES"
	_, err := db.Exec(query)
	//MySQL does not support DML timeout only SELECT
	return query, err
}

func FlushTablesWithReadLock(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	query := "FLUSH NO_WRITE_TO_BINLOG TABLES WITH READ LOCK"
	_, err := db.Exec(query)
	return query, err
}

func UnlockTables(db *sqlx.DB) (string, error) {
	query := "UNLOCK TABLES"
	_, err := db.Exec(query)
	return query, err
}

func StopSlave(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	cmd := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		cmd += "ALTER SUBSCRIPTION " + Channel + " DISABLE"
	} else {
		cmd += "STOP SLAVE"
		if myver.IsMariaDB() && Channel != "" {
			cmd += " '" + Channel + "'"
		}
		if myver.IsMySQLOrPercona() && Channel != "" {
			cmd += " FOR CHANNEL '" + Channel + "'"
		}
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func StopSlaveIOThread(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	cmd := "STOP SLAVE IO_THREAD"
	if myver.IsMariaDB() && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "'  IO_THREAD"
	}
	if myver.IsMySQLOrPercona() && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return cmd, err
}
func StopSlaveSQLThread(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	cmd := "STOP SLAVE SQL_THREAD"
	if myver.IsMariaDB() && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "' SQL_THREAD"
	}
	if myver.IsMySQLOrPercona() && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func SetSlaveHeartbeat(db *sqlx.DB, interval string, Channel string, myver *MySQLVersion) (string, error) {
	var err error
	logs := ""
	log := ""
	log, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "change master to MASTER_HEARTBEAT_PERIOD=" + interval
	logs += "\n" + stmt
	_, err = db.Exec(stmt)

	if err != nil {
		return logs, err
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveGTIDMode(db *sqlx.DB, mode string, Channel string, myver *MySQLVersion) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "change master to master_use_gtid=" + mode
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveExecMode(db *sqlx.DB, mode string, Channel string, myver *MySQLVersion) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "set global slave_exec_mode='" + mode + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetSlaveParallelMode(db *sqlx.DB, mode string, Channel string, myver *MySQLVersion) (string, error) {
	var err error
	logs := ""
	log := ""
	logs, err = StopSlave(db, Channel, myver)
	logs += log
	if err != nil {
		return logs, err
	}
	stmt := "set global slave_parallel_mode='" + mode + "'"
	logs += "\n" + stmt
	_, err = db.Exec(stmt)
	if err != nil {
		return logs, err
	}
	if Channel != "" {
		stmt := "set global " + Channel + ".slave_parallel_mode='" + mode + "'"
		_, err = db.Exec(stmt)
		if err != nil {
			return logs, err
		}
	}
	log, err = StartSlave(db, Channel, myver)
	logs += "\n" + stmt
	if err != nil {
		return logs, err
	}
	return logs, err
}

func SetGTIDSlavePos(db *sqlx.DB, gtid string) (string, error) {
	query := "SET GLOBAL gtid_slave_pos='" + gtid + "'"
	_, err := db.Exec(query)
	return query, err
}

func GetBinlogDumpThreads(db *sqlx.DB, myver *MySQLVersion) (int, string, error) {
	var i int
	query := "SELECT COUNT(*) AS n FROM INFORMATION_SCHEMA.PROCESSLIST WHERE command LIKE 'binlog dump%'"
	err := db.Get(&i, query)
	return i, query, err
}

func SetMaxConnections(db *sqlx.DB, connections string, myver *MySQLVersion) (string, error) {

	query := "SET GLOBAL max_connections=" + connections
	_, err := db.Exec(query)
	return query, err
}

func SetSemiSyncSlave(db *sqlx.DB, myver *MySQLVersion) (string, error) {

	query := "SET GLOBAL rpl-semi-sync-slave-enabled=1"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_replica_enabled=ON"
	}
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL rpl-semi-sync-master-enabled=0"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_source_enabled=OFF"
	}
	_, err = db.Exec(query)
	return query, err
}

func SetSemiSyncMaster(db *sqlx.DB, myver *MySQLVersion) (string, error) {

	query := "SET GLOBAL rpl-semi-sync-master-enabled=1"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_source_enabled=ON"
	}
	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	query = "SET GLOBAL rpl-semi-sync-slave-enabled=0"
	if myver.IsMySQLOrPercona() && ((myver.Major >= 8 && myver.Minor > 0) || (myver.Major >= 8 && myver.Minor == 0 && myver.Release >= 26)) {
		query = "SET GLOBAL rpl_semi_sync_replica_enabled=OFF"
	}
	_, err = db.Exec(query)
	return query, err
}

func SetSlaveGTIDModeStrict(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	var err error
	stmt := ""
	//MySQL is strict per default with GTID tracking gap trx
	if myver.IsMariaDB() {
		stmt = "set global gtid_strict_mode=1"
		_, err = db.Exec(stmt)
		if err != nil {
			return stmt, err
		}
	}
	return stmt, nil
}

func StopAllSlaves(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	_, err := db.Exec("STOP ALL SLAVES")
	return "STOP ALL SLAVES", err
}

func SkipBinlogEvent(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	if myver.IsMariaDB() {
		stmt := "SET @@default_master_connection='" + Channel + "'"
		_, err := db.Exec(stmt)
		if err != nil {
			return stmt, err
		}
	}
	query := "SET GLOBAL sql_slave_skip_counter=1"
	_, err := db.Exec(query)
	return query, err
}

func StartSlave(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	cmd := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		cmd += "ALTER SUBSCRIPTION " + Channel + " ENABLE"
	} else {
		cmd += "START SLAVE"
		if myver.IsMariaDB() && Channel != "" {
			cmd += " '" + Channel + "'"
		}
		if myver.IsMySQLOrPercona() && Channel != "" {
			cmd += " FOR CHANNEL '" + Channel + "'"
		}
	}
	_, err := db.Exec(cmd)
	return cmd, err
}

func StartGroupReplication(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	cmd := "START GROUP_REPLICATION"
	_, err := db.Exec(cmd)
	return cmd, err
}

func BootstrapGroupReplication(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	cmd := "SET GLOBAL group_replication_bootstrap_group = ON"

	_, err := db.Exec(cmd)
	if err != nil {
		return cmd, err
	}
	cmd, err = StartGroupReplication(db, myver)
	if err != nil {
		return cmd, err
	}
	cmd = "SET GLOBAL group_replication_bootstrap_group = OFF"
	_, err = db.Exec(cmd)

	return cmd, err
}
func ResetSlave(db *sqlx.DB, all bool, Channel string, myver *MySQLVersion) (string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		stmt += "DROP SUBSCRIPTION " + Channel
	} else {
		stmt += "RESET SLAVE"
		if myver.IsMariaDB() && Channel != "" {
			stmt += " '" + Channel + "'"
		}
		if all == true {
			stmt += " ALL"
			if myver.IsMySQLOrPercona() && Channel != "" {
				stmt += " FOR CHANNEL '" + Channel + "'"
			}
		}
	}
	_, err := db.Exec(stmt)
	return stmt, err
}

func ResetMaster(db *sqlx.DB, Channel string, myver *MySQLVersion) (string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {
		if Channel == "" {
			Channel = "alltables"
		}
		stmt += "DROP PUBLICATION " + Channel
	} else {
		stmt += "RESET MASTER"
	}
	_, err := db.Exec(stmt)

	return stmt, err
}

func PostgresGetChannel(db *sqlx.DB, myver *MySQLVersion) (string, string, error) {
	stmt := ""
	if myver.IsPostgreSQL() {

		stmt += "select slot_name from pg_replication_slots"
		channels := []string{}
		err := db.Select(&channels, stmt)
		return channels[0], stmt, err
	}
	return "", stmt, errors.New("Not PostgreSQL")

}

func SetDefaultMasterConn(db *sqlx.DB, dmc string, myver *MySQLVersion) (string, error) {

	if myver.IsMariaDB() {
		stmt := "SET @@default_master_connection='" + dmc + "'"
		_, err := db.Exec(stmt)
		return stmt, err
	}
	// MySQL replication channels are not supported at the moment
	return "", nil
}

/*
	Check for a list of slave prerequisites.

- Slave is connected
- Binary log on
- Connected to master
- No replication filters
*/
func CheckSlavePrerequisites(db *sqlx.DB, s string, myver *MySQLVersion) bool {
	if debug {
		log.Printf("CheckSlavePrerequisites called") // remove those warnings !!
	}
	err := db.Ping()
	/* If slave is not online, skip to next iteration */
	if err != nil {
		log.Printf("WARN : Slave %s is offline. Skipping", s)
		return false
	}
	vars, _, _ := GetVariables(db, myver)
	if vars["LOG_BIN"] == "OFF" {
		return false
	}
	return true
}

func CheckBinlogFilters(m *sqlx.DB, s *sqlx.DB, myver *MySQLVersion) (bool, string, error) {
	logs := ""

	ms, log, err := GetMasterStatus(m, myver)
	logs += log
	if err != nil {
		return false, log, errors.New("Cannot check binlog status on master")
	}

	ss, log, err := GetMasterStatus(s, myver)
	logs += "\n" + log
	if err != nil {
		return false, logs, errors.New("ERROR: Can't check binlog status on slave")
	}
	if ms.Binlog_Do_DB == ss.Binlog_Do_DB && ms.Binlog_Ignore_DB == ss.Binlog_Ignore_DB {
		return true, logs, nil
	}
	return false, logs, nil
}

func CheckReplicationFilters(m *sqlx.DB, s *sqlx.DB, myver *MySQLVersion) bool {
	mv, _, _ := GetVariables(m, myver)
	sv, _, _ := GetVariables(s, myver)
	if mv["REPLICATE_DO_TABLE"] == sv["REPLICATE_DO_TABLE"] && mv["REPLICATE_IGNORE_TABLE"] == sv["REPLICATE_IGNORE_TABLE"] && mv["REPLICATE_WILD_DO_TABLE"] == sv["REPLICATE_WILD_DO_TABLE"] && mv["REPLICATE_WILD_IGNORE_TABLE"] == sv["REPLICATE_WILD_IGNORE_TABLE"] && mv["REPLICATE_DO_DB"] == sv["REPLICATE_DO_DB"] && mv["REPLICATE_IGNORE_DB"] == sv["REPLICATE_IGNORE_DB"] {
		return true
	} else {
		return false
	}
}

func GetEventScheduler(dbM *sqlx.DB, myver *MySQLVersion) bool {

	sES, _, _ := GetVariableByNameToUpper(dbM, "EVENT_SCHEDULER", myver)
	if sES != "ON" {
		return false
	}
	return true
}

func SetEventScheduler(db *sqlx.DB, state bool, myver *MySQLVersion) (string, error) {
	var err error
	stmt := ""
	if state {
		stmt = "SET GLOBAL event_scheduler=1"
	} else {
		stmt = "SET GLOBAL event_scheduler=0"
	}
	_, err = db.Exec(stmt)
	return stmt, err
}

/* Check if a slave is in sync with his master */
func CheckSlaveSync(dbS *sqlx.DB, dbM *sqlx.DB, myver *MySQLVersion) bool {
	if debug {
		log.Printf("CheckSlaveSync called")
	}
	sGtid, _, _ := GetVariableByNameToUpper(dbS, "GTID_CURRENT_POS", myver)
	mGtid, _, _ := GetVariableByNameToUpper(dbM, "GTID_CURRENT_POS", myver)
	if sGtid == mGtid {
		return true
	} else {
		return false
	}
}

func CheckSlaveSemiSync(dbS *sqlx.DB, myver *MySQLVersion) bool {
	if debug {
		log.Printf("CheckSlaveSemiSync called")
	}
	sync, _, _ := GetVariableByNameToUpper(dbS, "RPL_SEMI_SYNC_SLAVE_STATUS", myver)

	if sync == "ON" {
		return true
	} else {
		return false
	}
}

func MasterWaitGTID(db *sqlx.DB, gtid string, timeout int) (string, error) {
	query := "SELECT MASTER_GTID_WAIT(?, ?)"
	_, err := db.Exec(query, gtid, timeout)
	return query + "(" + gtid + "-" + strconv.Itoa(timeout) + ")", err
}

func MasterPosWait(db *sqlx.DB, myver *MySQLVersion, log string, pos string, timeout int, channel string) (string, error) {
	// SOURCE_POS_WAIT  before MySQL 8.0.26
	funcname := "MASTER_POS_WAIT"
	if myver.IsMySQLOrPercona() && myver.GreaterEqual("8.0.26") {
		funcname = "SOURCE_POS_WAIT"
	}

	if channel == "" {
		query := "SELECT " + funcname + "(?, ?, ?)"
		_, err := db.Exec(query, log, pos, timeout)
		return query + "(" + log + "-" + pos + "-" + strconv.Itoa(timeout) + ")", err
	} else {
		query := "SELECT " + funcname + "(?, ?, ?, ?)"
		_, err := db.Exec(query, log, pos, timeout, channel)
		return query + "(" + log + "-" + pos + "-" + strconv.Itoa(timeout) + ")", err
	}
}

func SetReadOnly(db *sqlx.DB, flag bool) (string, error) {
	if flag == true {
		query := "SET GLOBAL read_only=1"
		_, err := db.Exec(query)
		return query, err
	} else {
		query := "SET GLOBAL read_only=0"
		_, err := db.Exec(query)
		return query, err
	}
}

func SetSuperReadOnly(db *sqlx.DB, flag bool) (string, error) {
	if flag == true {
		_, err := db.Exec("SET GLOBAL super_read_only=1")
		return "SET GLOBAL super_read_only=1", err
	} else {
		_, err := db.Exec("SET GLOBAL super_read_only=0")
		return "SET GLOBAL super_read_only=0", err
	}
}

func SetQueryCaptureMode(db *sqlx.DB, mode string) (string, error) {
	var err error
	query := "SET GLOBAL log_output='" + mode + "'"

	if mode == "TABLE" || mode == "FILE" {
		_, err = db.Exec(query)
	} else {
		err = errors.New("Unvalid mode")
	}
	return query, err
}

func CheckLongRunningWrites(db *sqlx.DB, thresh int) (int, string, error) {
	var count int
	query := "select SUM(ct) from ( select count(*) as ct from information_schema.processlist  where command = 'Query' and time >= ? and info not like 'select%' union all select count(*) as ct  FROM  INFORMATION_SCHEMA.INNODB_TRX trx WHERE trx.trx_started < CURRENT_TIMESTAMP - INTERVAL ? SECOND) A"
	err := db.QueryRowx(query, thresh, thresh).Scan(&count)
	return count, query + "(" + strconv.Itoa(thresh) + ")", err
}

func KillThreads(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	//SELECT pg_terminate_backend(11929);
	var ids []int
	query := "SELECT Id FROM information_schema.PROCESSLIST WHERE Command != 'binlog dump' AND User != 'system user' AND Id != CONNECTION_ID()"
	if myver.IsPostgreSQL() {
		query = "SELECT pid  FROM pg_stat_activity where backend_type='client backend' and pid<>pg_backend_pid()"
	}
	logs := query
	err := db.Select(&ids, query)
	if err != nil {
		return logs, err
	}
	for _, id := range ids {
		log, err := KillThread(db, strconv.Itoa(id), myver)
		logs += log
		//Should we exit in case of error ?
		if err != nil {
			return logs, err
		}
	}
	return logs, err

}

func KillThread(db *sqlx.DB, id string, myver *MySQLVersion) (string, error) {
	if myver.IsPostgreSQL() {
		_, err := db.Exec("SELECT pg_terminate_backend(" + id + ")")
		return "SELECT pg_terminate_backend(" + id + ")", err
	}
	_, err := db.Exec("KILL ?", id)
	return "KILL ? (" + id + ")", err
}

func KillQuery(db *sqlx.DB, id string, myver *MySQLVersion) (string, error) {

	if myver.IsPostgreSQL() {
		_, err := db.Exec("SELECT pg_terminate_backend(" + id + ")")
		return "SELECT pg_terminate_backend(" + id + ")", err
	}
	_, err := db.Exec("KILL QUERY ?", id)
	return "KILL QUERY ? (" + id + ")", err

}

/* Check if string is an IP address or a hostname, return a IP address */
func CheckHostAddr(h string) (string, error) {
	var err error
	if net.ParseIP(h) != nil {
		return h, err
	}
	ha, err := net.LookupHost(h)
	if err != nil {
		return "", err
	}
	return ha[0], err
}

func GetSpiderShardUrl(db *sqlx.DB) (string, error) {
	var value string
	value = ""
	err := db.QueryRowx("select coalesce(group_concat(distinct concat(coalesce(st.host,s.host ),':',coalesce(st.port,s.port))),'') as value  from mysql.spider_tables st left join mysql.servers s on st.server=s.server_name").Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get spider shards", err)
	}
	return value, err
}

func GetSpiderMonitor(db *sqlx.DB) (string, error) {
	var value string
	value = ""
	err := db.QueryRowx("select  coalesce(group_concat(distinct concat(coalesce(st.host,s.host ),':',coalesce(st.port,s.port))),'') as value  from mysql.spider_link_mon_servers st left join mysql.servers s on st.server=s.server_name").Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get spider shards", err)
	}
	return value, err
}

func GetSpiderTableToSync(db *sqlx.DB) (map[string]SpiderTableNoSync, error) {
	vars := make(map[string]SpiderTableNoSync)
	rows, err := db.Queryx(`
		select usync.*, sync.srv_sync from (
		  select  group_concat( distinct concat(db_name, '.',substring_index(table_name,'#P#', 1))) as tbl_src ,  group_concat( distinct concat(db_name, '.', table_name)) as tbl_src_link,concat( coalesce(st.tgt_db_name,s.db) ,'.', tgt_table_name ) as tbl_dest, concat(coalesce(st.host,s.host ),':',coalesce(st.port,s.port)) as srv_desync  from (select * from mysql.spider_tables where link_status=3) st left join mysql.servers s on st.server=s.server_name group by tbl_dest, srv_desync
		) usync inner join (
		  select  group_concat( distinct concat(db_name, '.',table_name)) as tbl_src ,concat( coalesce(st.tgt_db_name,s.db) ,'.', tgt_table_name ) as tbl_dest, concat(coalesce(st.host,s.host ),':',coalesce(st.port,s.port)) as srv_sync  from (select * from mysql.spider_tables where link_status=1) st left join mysql.servers s on st.server=s.server_name group by tbl_dest, srv_sync
		) sync ON  usync.tbl_src_link= sync.tbl_src and usync.tbl_dest=sync.tbl_dest
		`)
	if err != nil {
		return vars, err
	}
	defer rows.Close()
	for rows.Next() {
		var v SpiderTableNoSync
		rows.Scan(&v.Tbl_src, &v.Tbl_src_link, &v.Tbl_dest, &v.Srv_dsync, &v.Srv_sync)
		vars[v.Tbl_src] = v
	}
	return vars, err
}

func runPreparedExecConcurrent(db *sqlx.DB, n int, co int) error {
	query := "UPDATE replication_manager_schema.bench SET val=val+1 WHERE id=1"
	stmt, err := db.Prepare(query)
	if err != nil {
		return err
	}

	remain := int64(n)
	var wg sync.WaitGroup
	wg.Add(co)

	for i := 0; i < co; i++ {
		go func() {
			for {
				if atomic.AddInt64(&remain, -1) < 0 {
					wg.Done()
					return
				}

				if _, err1 := stmt.Exec(); err1 != nil {
					wg.Done()
					err = err1
					return
				}
			}
		}()
	}
	wg.Wait()
	stmt.Close()
	return err
}

func runPreparedQueryConcurrent(db *sqlx.DB, n int, co int) error {
	stmt, err := db.Prepare("SELECT ?, \"foobar\"")
	if err != nil {
		return err
	}

	remain := int64(n)
	var wg sync.WaitGroup
	wg.Add(co)

	for i := 0; i < co; i++ {
		go func() {
			var id int
			var str string
			for {
				if atomic.AddInt64(&remain, -1) < 0 {
					wg.Done()
					return
				}

				if err1 := stmt.QueryRow(i).Scan(&id, &str); err1 != nil {
					wg.Done()
					err = err1
					return
				}
			}
		}()
	}
	wg.Wait()
	stmt.Close()
	return err
}

func benchPreparedExecConcurrent1(db *sqlx.DB, n int) error {
	return runPreparedExecConcurrent(db, n, 1)
}

func benchPreparedExecConcurrent2(db *sqlx.DB, n int) error {
	return runPreparedExecConcurrent(db, n, 2)
}

func benchPreparedExecConcurrent4(db *sqlx.DB, n int) error {
	return runPreparedExecConcurrent(db, n, 4)
}

func benchPreparedExecConcurrent8(db *sqlx.DB, n int) error {
	return runPreparedExecConcurrent(db, n, 8)
}

func benchPreparedExecConcurrent16(db *sqlx.DB, n int) error {
	return runPreparedExecConcurrent(db, n, 16)
}

func InjectLongTrx(db *sqlx.DB, time int) error {
	benchWarmup(db)
	_, err := db.Exec("set binlog_format='STATEMENT'")
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val)  select  sleep(" + fmt.Sprintf("%d", time) + ") from dual")
	if err != nil {
		return err
	}
	return nil
}

func InjectTrx(db *sqlx.DB) error {

	_, err := db.Exec("INSERT INTO replication_manager_schema.bench(val)  VALUES(1)")
	if err != nil {
		return err
	}
	return nil
}

func BenchCleanup(db *sqlx.DB) error {
	_, err := db.Exec("DROP TABLE replication_manager_schema.bench")
	if err != nil {
		return err
	}
	return nil
}

func AnalyzeTable(db *sqlx.DB, myver *MySQLVersion, table string) (string, error) {
	query := "ANALYZE TABLE " + table
	if myver.Greater("10.4.0") && myver.IsMariaDB() {
		query += " PERSISTENT FOR ALL"
	}
	_, err := db.Exec(query)
	if err != nil {
		log.Println("ERROR: Could not analyze table", err)
	}
	return query, err
}

func ChecksumTable(db *sqlx.DB, table string) (string, error) {
	var tableres string
	var checkres string
	tableres = ""
	checkres = ""
	err := db.QueryRowx("CHECKSUM TABLE "+table+" EXTENDED").Scan(&tableres, &checkres)
	if err != nil {
		log.Println("ERROR: Could not checksum table", err)
	}
	return checkres, err
}

func InjectTrxWithoutCommit(db *sqlx.DB) error {
	benchWarmup(db)
	_, err := db.Exec("START TRANSACTION")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val)  VALUES(1)")
	if err != nil {
		return err
	}
	return nil
}

func benchWarmup(db *sqlx.DB) error {
	db.SetMaxIdleConns(16)
	_, err := db.Exec("CREATE DATABASE IF NOT EXISTS  replication_manager_schema")
	if err != nil {
		return err
	}
	_, err = db.Exec("DROP TABLE IF EXISTS replication_manager_schema.bench")
	if err != nil {
		return err
	}
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS replication_manager_schema.bench(id bigint unsigned primary key auto_increment, val bigint unsigned  )")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench(val) VALUES(1)")
	if err != nil {
		return err
	}

	for i := 0; i < 2; i++ {
		rows, err := db.Query("SELECT val FROM replication_manager_schema.bench")
		if err != nil {
			return err
		}

		if err = rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func WriteConcurrent2(dsn string, qt int) (string, error) {
	var err error

	bs := BenchmarkSuite{
		WarmUp:      benchWarmup,
		Repetitions: 1,
		PrintStats:  true,
	}

	if err = bs.AddDriver("mysql", "mysql", dsn); err != nil {
		return "", err
	}

	bs.AddBenchmark("PreparedExecConcurrent2", qt, benchPreparedExecConcurrent2)

	result := bs.Run()
	return result, nil
}

func IsGroupReplicationMaster(db *sqlx.DB, myver *MySQLVersion, host string) (bool, error) {
	var value bool
	value = false
	err := db.QueryRowx("SELECT 1 FROM  performance_schema.replication_group_members WHERE  MEMBER_STATE='ONLINE' AND MEMBER_ROLE='PRIMARY' AND MEMBER_HOST='" + host + "'").Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		log.Println("ERROR: Could not check Group Replication Master", err)
	}
	return value, nil
}

func IsGroupReplicationSlave(db *sqlx.DB, myver *MySQLVersion, host string) (bool, error) {
	var value bool
	value = false
	err := db.QueryRowx("SELECT 1 FROM  performance_schema.replication_group_members WHERE  MEMBER_STATE='ONLINE' AND MEMBER_ROLE='SECONDARY' AND MEMBER_HOST='" + host + "'").Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		log.Println("ERROR: Could not check Group Replication Secondary", err)
	}
	return value, nil
}

func SetGroupReplicationPrimary(db *sqlx.DB, myver *MySQLVersion) (string, error) {
	var value string
	value = ""
	uuid := ""
	err := db.QueryRowx("SELECT @@server_uuid").Scan(&uuid)
	if err != nil {
		return "", err
	}

	err = db.QueryRowx("SELECT group_replication_set_as_primary('" + uuid + "')").Scan(&value)

	if err != nil {
		log.Println("ERROR: Could not set Group Replication Primary", err)
	}
	return value, nil
}

func AddMonitoringUser(db *sqlx.DB, myver *MySQLVersion, user string, password string, method string) error {

	return nil
}

func AddReplicationUser(db *sqlx.DB, myver *MySQLVersion, user string, password string, method string) error {

	return nil
}

func SetUserPassword(db *sqlx.DB, myver *MySQLVersion, user_host string, user_name string, new_password string) (string, error) {
	query := "ALTER USER '" + user_name + "'@'" + user_host + "' IDENTIFIED BY '" + new_password + "'"
	_, err := db.Exec(query)
	if err != nil {

		return query, err
	}
	return query, nil
}

func RenameUserPassword(db *sqlx.DB, myver *MySQLVersion, user_host string, old_user_name string, new_password string, new_user_name string) (string, error) {
	query := "RENAME USER '" + old_user_name + "'@'" + user_host + "' TO '" + new_user_name + "'@'" + user_host + "'"
	_, err := db.Exec(query)
	if err != nil {

		return query, err
	}
	return query, nil
}

func DuplicateUserPassword(db *sqlx.DB, myver *MySQLVersion, old_user_name string, user_host string, new_user_name string) (string, error) {
	if myver.IsMySQLOrPercona() && myver.Major >= 8 {
		query := "SHOW CREATE USER  `" + old_user_name + "`@`" + user_host + "`"
		rows, err := db.Queryx(query)
		if err != nil {
			return query, errors.New("Could not get grant for user ")
		}
		defer rows.Close()
		var grant string

		for rows.Next() {
			err = rows.Scan(&grant)
			if err != nil {
				return query, err
			}
			querygrant := strings.Replace(grant, old_user_name, new_user_name, 1)
			query += ";" + querygrant
			_, err = db.Queryx(querygrant)
			if err != nil {
				return query, err
			}
		}
	}
	query := "SHOW GRANTS FOR '" + old_user_name + "'@'" + user_host + "'"
	rows, err := db.Queryx(query)
	if err != nil {
		return query, errors.New("Could not get grant for user ")
	}
	defer rows.Close()
	var grant string

	for rows.Next() {
		err = rows.Scan(&grant)
		if err != nil {
			return query, err
		}
		querygrant := strings.Replace(grant, old_user_name, new_user_name, 1)
		query += ";" + querygrant
		_, err = db.Queryx(querygrant)
		if err != nil {
			return query, err
		}
	}
	return query, nil
}

func PurgeBinlogTo(db *sqlx.DB, filename string) (string, error) {
	var err error
	query := "PURGE BINARY LOGS TO '" + filename + "'"

	if filename != "" {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid filename for PURGE BINARY LOGS TO")
	}
	return query, err
}

func PurgeBinlogBefore(db *sqlx.DB, ts int64) (string, error) {
	var err error
	var tstring string = time.Unix(ts, 0).Format(DDMMYYYYhhmmss)
	query := "PURGE BINARY LOGS BEFORE '" + tstring + "'"
	_, err = db.Exec(query)
	return query, err
}

func SetMaxBinlogTotalSize(db *sqlx.DB, size int) (string, error) {
	var err error
	query := "SET GLOBAL max_binlog_total_size = " + strconv.Itoa(size) + ""

	if size >= 0 {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid size for max_binlog_total_size")
	}
	return query, err
}

func SetSlaveConnectionsNeededForPurge(db *sqlx.DB, size int) (string, error) {
	var err error
	query := "SET GLOBAL slave_connections_needed_for_purge = " + strconv.Itoa(size) + ""

	if size >= 0 {
		_, err = db.Exec(query)
	} else {
		return query, errors.New("Invalid value for slave_connections_needed_for_purge")
	}
	return query, err
}

func GetBinlogFormatDesc(db *sqlx.DB, binlogfile string) ([]BinlogEvents, string, error) {
	logs := ""
	logpos := "0"
	events := []BinlogEvents{}

	sql := fmt.Sprintf("show binlog events IN '%s' from %s LIMIT 3", binlogfile, logpos)
	logs += sql + "\n"
	err := db.Select(&events, sql)
	if err != nil {
		return nil, logs, err
	}

	for _, row := range events {
		if strings.ToUpper(row.Event_type) == "FORMAT_DESC" {
			return []BinlogEvents{row}, logs, nil
		}
	}

	return nil, logs, errors.New("Binlog Format Desc Not Found")
}
