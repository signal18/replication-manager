// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
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

	"github.com/jmoiron/sqlx"
)

const debug = false

type Table struct {
	Table_schema   string `json:"tableSchema"`
	Table_name     string `json:"tableName"`
	Engine         string `json:"engine"`
	Table_rows     int64  `json:"tableRows"`
	Data_length    int64  `json:"dataLength"`
	Index_length   int64  `json:"indexLength"`
	Table_crc      uint64 `json:"tableCrc"`
	Table_clusters string `json:"tableClusters"`
}

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
	Id       uint64         `json:"id"`
	User     string         `json:"user"`
	Host     string         `json:"host"`
	Database sql.NullString `json:"database"`
	Command  string         `json:"command"`
	Time     float64        `json:"time"`
	State    string         `json:"state"`
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

func GetAddress(host string, port string, socket string) string {
	var address string
	if host != "" {
		address = "tcp(" + host + ":" + port + ")"
	} else {
		address = "unix(" + socket + ")"
	}
	return address
}

func GetProcesslist(db *sqlx.DB) ([]Processlist, error) {
	pl := []Processlist{}
	err := db.Select(&pl, "SELECT Id, User, Host, `Db` AS `Database`, Command, Time_ms as Time, State FROM INFORMATION_SCHEMA.PROCESSLIST")
	if err != nil {
		return nil, fmt.Errorf("ERROR: Could not get processlist: %s", err)
	}
	return pl, nil
}

func GetServers(db *sqlx.DB) ([]MySQLServer, error) {
	db.MapperFunc(strings.Title)
	var err error
	ss := []MySQLServer{}
	err = db.Select(&ss, "SELECT * FROM mysql.servers")
	return ss, err
}

func GetLastPseudoGTID(db *sqlx.DB) (string, error) {
	var value string
	value = ""
	err := db.QueryRowx("select * from replication_manager_schema.pseudo_gtid_v").Scan(&value)
	return value, err
}

func GetBinlogEventPseudoGTID(db *sqlx.DB, uuid string, lastfile string) (string, string, error) {

	lastpos := "4"
	exitloop := true
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 60"
		err := db.Select(&events, sql)
		if err != nil {
			return "", "", err
		}

		for _, row := range events {
			pos := strconv.FormatUint(uint64(row.Pos), 10)
			endpos := strconv.FormatUint(uint64(row.End_log_pos), 10)
			if strings.Contains(row.Info, uuid) {
				return row.Log_name, pos, err
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
	return "", "", errors.New("Not found Psudo GTID")
}

func GetBinlogPosAfterSkipNumberOfEvents(db *sqlx.DB, file string, pos string, skip int) (string, string, error) {

	events := []BinlogEvents{}
	sql := "show binlog events IN '" + file + "'  from " + pos + " LIMIT " + strconv.Itoa(skip)
	err := db.Select(&events, sql)
	if err != nil {
		return "", "", err
	}
	if len(events) == 0 {
		return "", "", err
	}
	return events[(len(events) - 1)].Log_name, strconv.FormatUint(uint64(events[(len(events)-1)].Pos), 10), err
}

func GetNumberOfEventsAfterPos(db *sqlx.DB, lastfile string, lastpos string) (int, error) {

	exitloop := true
	ct := 0
	for exitloop {
		events := []BinlogEvents{}
		sql := "show binlog events IN '" + lastfile + "'  from " + lastpos + " LIMIT 1"
		err := db.Select(&events, sql)
		if err != nil {
			return 0, err
		}

		for _, row := range events {
			lastfile = strconv.FormatUint(uint64(row.End_log_pos), 10)
		}
		if len(events) == 0 {
			return ct, nil
		}
		ct = ct + 1
	}
	return 0, errors.New("Not found Psudo GTID")
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
	IsMySQL   bool
	IsMariaDB bool
	Channel   string
	//	SSLCa     string
	//	SSLCert   string
	//	SSLKey    string
}

func ChangeMaster(db *sqlx.DB, opt ChangeMasterOpt) error {
	myver, _ := GetDBVersion(db)
	cm := "CHANGE MASTER "
	if opt.IsMariaDB && opt.Channel != "" {
		cm += " '" + opt.Channel + "'"
	}
	cm += " TO master_host='" + opt.Host + "', master_port=" + opt.Port + ", master_user='" + opt.User + "', master_password='" + opt.Password + "', master_connect_retry=" + opt.Retry + ", master_heartbeat_period=" + opt.Heartbeat
	switch opt.Mode {
	case "SLAVE_POS":
		cm += ", MASTER_USE_GTID=SLAVE_POS"
	case "CURRENT_POS":
		cm += ", MASTER_USE_GTID=CURRENT_POS"
	case "MXS":
		cm += ", master_log_file='" + opt.Logfile + "', master_log_pos=" + opt.Logpos
	case "POSITIONAL":
		cm += ", master_log_file='" + opt.Logfile + "', master_log_pos=" + opt.Logpos
		if myver.IsMariaDB() {
			cm += ", MASTER_USE_GTID=NO"
		}
	case "MASTER_AUTO_POSITION":
		cm += ", MASTER_AUTO_POSITION = 1"
	}
	if opt.SSL {
		cm += ", MASTER_SSL=1"
		//cm +=, MASTER_SSL_CA='" + opt.SSLCa + "', MASTER_SSL_CERT='" + opt.SSLCert + "', MASTER_SSL_KEY=" + opt.SSLKey + "'"
	}
	if opt.IsMySQL && opt.Channel != "" {
		cm += " FOR CHANNEL '" + opt.Channel + "'"
	}
	_, err := db.Exec(cm)
	if err != nil {
		return fmt.Errorf("Change master statement %s failed, reason: %s", cm, err)
	}
	return nil
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

func GetDBVersion(db *sqlx.DB) (*MySQLVersion, error) {
	stmt := "SELECT @@version"
	var version string
	var versionComment string
	err := db.QueryRowx(stmt).Scan(&version)
	if err != nil {
		return &MySQLVersion{}, err
	}
	stmt = "SELECT @@version_comment"
	err = db.QueryRowx(stmt).Scan(&versionComment)
	if err != nil {
		return &MySQLVersion{}, err
	}
	return NewMySQLVersion(version, versionComment), nil
}

func GetHostFromProcessList(db *sqlx.DB, user string) string {
	pl := []Processlist{}
	pl, err := GetProcesslist(db)
	if err != nil {
		return "N/A"
	}
	for i := range pl {
		if pl[i].User == user {
			return strings.Split(pl[i].Host, ":")[0]
		}
	}
	return "N/A"
}

func GetHostFromConnection(db *sqlx.DB, user string) (string, error) {

	var value string
	err := db.QueryRowx("select user()").Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get SQL User()", err)
		return "N/A", err
	}
	return strings.Split(value, "@")[1], nil

}

func GetPrivileges(db *sqlx.DB, user string, host string, ip string) (Privileges, error) {
	db.MapperFunc(strings.Title)

	priv := Privileges{}

	if ip == "" {
		return priv, errors.New("Error getting privileges for non-existent IP address")
	}

	splitip := strings.Split(ip, ".")

	iprange1 := splitip[0] + ".%.%.%"
	iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
	iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"
	stmt := "SELECT MAX(Select_priv) as Select_priv, MAX(Process_priv) as Process_priv, MAX(Super_priv) as Super_priv, MAX(Repl_slave_priv) as Repl_slave_priv, MAX(Repl_client_priv) as Repl_client_priv, MAX(Reload_priv) as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
	row := db.QueryRowx(stmt, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3)
	err := row.StructScan(&priv)
	if err != nil && strings.Contains(err.Error(), "unsupported Scan") {
		return priv, errors.New("No replication user defined. Please check the replication user is created with the required privileges")
	}
	return priv, err
}

func CheckReplicationAccount(db *sqlx.DB, pass string, user string, host string, ip string) (bool, error) {
	db.MapperFunc(strings.Title)

	splitip := strings.Split(ip, ".")

	iprange1 := splitip[0] + ".%.%.%"
	iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
	iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"
	stmt := "SELECT STRCMP(Password) AS pass, PASSWORD(?) AS upass FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
	rows, err := db.Query(stmt, pass, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3)
	if err != nil {
		return false, err
	}
	for rows.Next() {
		var pass, upass string
		err = rows.Scan(&pass, &upass)
		if err != nil {
			return false, err
		}
		if pass != upass {
			return false, nil
		}
	}
	return true, nil
}

func HaveExtraEvents(db *sqlx.DB, file string, pos string) (bool, error) {
	db.MapperFunc(strings.Title)
	evts := []BinlogEvents{}
	udb := db.Unsafe()
	err := udb.Get(&evts, "SHOW BINLOG EVENTS IN '"+file+"' FROM "+pos)
	if err != nil {
		return true, err
	}
	if len(evts) == 1 {
		return false, nil
	}
	if len(evts) > 1 {
		return true, nil
	}
	return false, nil
}

func GetSlaveStatus(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) (SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	var err error
	udb := db.Unsafe()
	ss := SlaveStatus{}
	if Channel == "" {
		err = udb.Get(&ss, "SHOW SLAVE  STATUS")
	} else {
		if IsMariaDB {
			err = udb.Get(&ss, "SHOW SLAVE '"+Channel+"' STATUS")
		} else {
			err = udb.Get(&ss, "SHOW SLAVE STATUS FOR CHANNEL '"+Channel+"'")
		}
	}

	return ss, err
}

func GetChannelSlaveStatus(db *sqlx.DB) ([]SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	err := udb.Select(&ss, "SHOW SLAVE STATUS")
	return ss, err
}

func GetMSlaveStatus(db *sqlx.DB, conn string) (SlaveStatus, error) {
	myver, _ := GetDBVersion(db)
	s := SlaveStatus{}
	ss := []SlaveStatus{}
	var err error

	if myver.IsMariaDB() {
		ss, err = GetAllSlavesStatus(db)
	} else {
		var s SlaveStatus
		s, err = GetSlaveStatus(db, conn, myver.IsMariaDB(), myver.IsMySQL())
		ss = append(ss, s)
	}

	for _, s := range ss {
		if s.ConnectionName.String == conn {
			return s, err
		}
	}
	return s, err
}

func GetAllSlavesStatus(db *sqlx.DB) ([]SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	var err error

	err = udb.Select(&ss, "SHOW ALL SLAVES STATUS")

	return ss, err
}

func SetMultiSourceRepl(db *sqlx.DB, master_host string, master_port string, master_user string, master_password string, master_filter string) error {
	crcTable := crc64.MakeTable(crc64.ECMA) // http://golang.org/pkg/hash/crc64/#pkg-constants
	checksum64 := fmt.Sprintf("%d", crc64.Checksum([]byte(master_host+":"+master_port), crcTable))

	stmt := "CHANGE MASTER 'mrm_" + checksum64 + "' TO master_host='" + master_host + "', master_port=" + master_port + ", master_user='" + master_user + "', master_password='" + master_password + "' , master_use_gtid=slave_pos"
	_, err := db.Exec(stmt)
	if err != nil {
		return err
	}
	if master_filter != "" {
		stmt = "SET GLOBAL mrm_" + checksum64 + ".replicate_do_table='" + master_filter + "'"
		_, err = db.Exec(stmt)
		if err != nil {
			return err
		}
	}
	stmt = "START SLAVE 'mrm_" + checksum64 + "'"
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}

	return err
}

func InstallSemiSync(db *sqlx.DB) error {
	stmt := "INSTALL PLUGIN rpl_semi_sync_slave SONAME 'semisync_slave.so'"
	_, err := db.Exec(stmt)
	if err != nil {
		return err
	}
	stmt = "INSTALL PLUGIN rpl_semi_sync_master SONAME 'semisync_master.so'"
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}
	_, err = db.Exec("set global rpl_semi_sync_master_enabled='ON'")
	if err != nil {
		return err
	}
	_, err = db.Exec("set global rpl_semi_sync_slave_enabled='ON'")
	if err != nil {
		return err
	}
	return nil
}

func SetBinlogFormat(db *sqlx.DB, format string) error {
	_, err := db.Exec("set global binlog_format='" + format + "'")
	if err != nil {
		return err
	}
	return nil
}

func SetBinlogAnnotate(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL binlog_annotate_row_events=ON")
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL replicate_annotate_row_events=ON")
	if err != nil {
		return err
	}
	return nil
}

func SetInnoDBLockMonitor(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL innodb_status_output=ON")
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL innodb_status_output_locks=ON")
	if err != nil {
		return err
	}
	return nil
}

func UnsetInnoDBLockMonitor(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL innodb_status_output_locks=0")
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL innodb_status_output=0")
	if err != nil {
		return err
	}
	return nil
}

func SetRelayLogSpaceLimit(db *sqlx.DB, size string) error {
	_, err := db.Exec("SET GLOBAL relay_log_space_limit=" + size)
	if err != nil {
		return err
	}
	return nil
}

// SetBinlogSlowqueries Enable queries in replication to be reported in slow queries
func SetBinlogSlowqueries(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL log_slow_slave_statements=ON")
	if err != nil {
		return err
	}
	return nil
}

func SetLongQueryTime(db *sqlx.DB, querytime string) error {
	_, err := db.Exec("SET GLOBAL long_query_time=" + querytime)
	if err != nil {
		return err
	}
	return nil
}

// SetSyncBinlog Enable Binlog Durability
func SetSyncBinlog(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL sync_binlog=1")
	if err != nil {
		return err
	}
	return nil
}

// SetSyncInnodb Enable InnoDB Durability
func SetSyncInnodb(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL innodb_flush_log_at_trx_commit=1")
	if err != nil {
		return err
	}
	return nil
}

// SetBinlogChecksum Enable binlog checksum and check on master
func SetBinlogChecksum(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL binlog_checksum=1")
	if err != nil {
		return err
	}
	_, err = db.Exec("SET GLOBAL master_verify_checksum=1")
	if err != nil {
		return err
	}
	return nil
}

// SetBinlogCompress Enable MaraiDB 10.2 binlog compression
func SetBinlogCompress(db *sqlx.DB) error {
	_, err := db.Exec("SET GLOBAL log_bin_compress=1")
	if err != nil {
		return err
	}
	return nil
}

func ResetAllSlaves(db *sqlx.DB) error {
	myver, _ := GetDBVersion(db)

	ss := []SlaveStatus{}
	var err error

	if myver.IsMariaDB() {
		ss, err = GetAllSlavesStatus(db)
	} else {
		var s SlaveStatus
		s, err = GetSlaveStatus(db, "", myver.IsMariaDB(), myver.IsMySQL())
		ss = append(ss, s)
	}
	if err != nil {
		return err
	}
	for _, src := range ss {
		err = SetDefaultMasterConn(db, src.ConnectionName.String)
		if err != nil {
			return err
		}

		if myver.IsMySQL() {
			err = StopSlave(db, src.ConnectionName.String, false, true)
		}
		err = ResetSlave(db, true, src.ConnectionName.String, myver.IsMariaDB(), myver.IsMySQL())
		if err != nil {
			return err
		}
	}
	return err
}

func GetMasterStatus(db *sqlx.DB) (MasterStatus, error) {
	db.MapperFunc(strings.Title)
	ms := MasterStatus{}
	udb := db.Unsafe()
	err := udb.Get(&ms, "SHOW MASTER STATUS")
	return ms, err
}

func GetSlaveHosts(db *sqlx.DB) (map[string]interface{}, error) {
	rows, err := db.Queryx("SHOW SLAVE HOSTS")
	if err != nil {
		return nil, errors.New("Could not get slave hosts")
	}
	defer rows.Close()
	results := make(map[string]interface{})
	for rows.Next() {
		err = rows.MapScan(results)
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

func GetSlaveHostsArray(db *sqlx.DB) ([]SlaveHosts, error) {
	sh := []SlaveHosts{}
	err := db.Select(&sh, "SHOW SLAVE HOSTS")
	if err != nil {
		return nil, errors.New("Could not get slave hosts array")
	}
	return sh, nil
}

func GetSlaveHostsDiscovery(db *sqlx.DB) ([]string, error) {
	slaveList := []string{}
	/* This method does not return the server ports, so we cannot rely on it for the time being. */
	err := db.Select(&slaveList, "select host from information_schema.processlist where command ='binlog dump'")
	if err != nil {
		return nil, errors.New("Could not get slave hosts from the processlist")
	}
	return slaveList, nil
}

func GetEventStatus(db *sqlx.DB) ([]Event, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()

	ss := []Event{}
	err := udb.Select(&ss, "SELECT db as Db, name as Name, definer as Definer, status+0  AS Status FROM mysql.event")
	if err != nil {
		return nil, errors.New("Could not get event status")
	}
	return ss, err
}

func SetEventStatus(db *sqlx.DB, ev Event, status int64) error {
	definer := strings.Split(ev.Definer, "@")
	if len(definer) != 2 {
		return errors.New("Incorrect definer format")
	}
	stmt := fmt.Sprintf("ALTER DEFINER='%s'@'%s' EVENT ", definer[0], definer[1])
	if status == 3 {
		stmt += ev.Db + "." + ev.Name + " DISABLE ON SLAVE"
	} else {
		stmt += ev.Db + "." + ev.Name + " ENABLE"
	}
	_, err := db.Exec(stmt)
	if err != nil {
		return err
	}
	return nil
}

func GetVariableSource(db *sqlx.DB) string {
	myver, _ := GetDBVersion(db)
	var source string
	if !myver.IsMariaDB() && myver.Major >= 5 && myver.Minor >= 7 {
		source = "performance_schema"
	} else {
		source = "information_schema"
	}
	return source
}

func GetStatus(db *sqlx.DB) (map[string]string, error) {
	type Variable struct {
		Variable_name string
		Value         string
	}
	source := GetVariableSource(db)
	vars := make(map[string]string)
	rows, err := db.Queryx("SELECT UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status")
	if err != nil {
		return nil, errors.New("Could not get status variables")
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return nil, errors.New("Could not get results from status scan")
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, nil
}

func GetEngineInnoDB(db *sqlx.DB) (map[string]string, error) {
	vars := make(map[string]string)
	rows, err := db.Query("SHOW ENGINE INNODB STATUS")
	if err != nil {
		return vars, err
	}
	defer rows.Close()

	var typeCol, nameCol, statusCol string
	// First row should contain the necessary info. If many rows returned then it's unknown case.
	if rows.Next() {
		if err := rows.Scan(&typeCol, &nameCol, &statusCol); err != nil {
			return vars, err
		}
	}

	// 0 queries inside InnoDB, 0 queries in queue
	// 0 read views open inside InnoDB
	rQueries, _ := regexp.Compile(`(\d+) queries inside InnoDB, (\d+) queries in queue`)
	rViews, _ := regexp.Compile(`(\d+) read views open inside InnoDB`)

	for _, line := range strings.Split(statusCol, "\n") {
		if data := rQueries.FindStringSubmatch(line); data != nil {
			vars["queries_inside_innodb"] = data[1]
			vars["queries_in_queue"] = data[2]
		} else if data := rViews.FindStringSubmatch(line); data != nil {
			vars["read_views_open_inside_innodb"] = data[1]
		}
	}
	return vars, nil
}

func GetQueries(db *sqlx.DB) (map[string]string, error) {
	type Variable struct {
		Digest string
		Value  string
	}
	vars := make(map[string]string)
	query := "set session group_concat_max_len=2048"
	db.Exec(query)
	query = "select digest_text as digest, round(sum_timer_wait/1000000000000, 6) as value from performance_schema.events_statements_summary_by_digest order by sum_timer_wait desc limit 20"

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, errors.New("Could not get queries")
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Digest, &v.Value)
		if err != nil {
			return nil, errors.New("Could not get results from status scan")
		}
		vars[v.Digest] = v.Value
	}
	return vars, nil
}

func GetStatusAsInt(db *sqlx.DB) (map[string]int64, error) {
	type Variable struct {
		Variable_name string
		Value         int64
	}
	vars := make(map[string]int64)
	source := GetVariableSource(db)
	rows, err := db.Queryx("SELECT UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status")
	if err != nil {
		return nil, errors.New("Could not get status variables as integers")
	}
	for rows.Next() {
		var v Variable
		rows.Scan(&v.Variable_name, &v.Value)
		vars[v.Variable_name] = v.Value
	}
	return vars, nil
}

func GetVariables(db *sqlx.DB) (map[string]string, error) {
	type Variable struct {
		Variable_name string
		Value         string
	}
	source := GetVariableSource(db)
	vars := make(map[string]string)
	rows, err := db.Queryx("SELECT UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_variables")
	if err != nil {
		return vars, err
	}
	for rows.Next() {
		var v Variable
		err = rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return vars, err
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, err
}

func GetTables(db *sqlx.DB) (map[string]Table, error) {
	vars := make(map[string]Table)
	rows, err := db.Queryx("SELECT a.TABLE_SCHEMA as Table_schema ,  a.TABLE_NAME as Table_name ,a.ENGINE as Engine,a.TABLE_ROWS as Table_rows ,COALESCE(a.DATA_LENGTH,0) as Data_length,COALESCE(a.INDEX_LENGTH,0) as Index_length ,COALESCE((select CONV(LEFT(MD5(group_concat(concat(b.column_name,b.column_type,COALESCE(b.is_nullable,''),COALESCE(b.CHARACTER_SET_NAME,''), COALESCE(b.COLLATION_NAME,''),COALESCE(b.COLUMN_DEFAULT,''),COALESCE(c.CONSTRAINT_NAME,''),COALESCE(c.ORDINAL_POSITION,'')))), 16), 16, 10)    FROM information_schema.COLUMNS b left join information_schema.KEY_COLUMN_USAGE c ON b.table_schema=c.table_schema  and  b.table_name=c.table_name where b.table_schema=a.table_schema  and  b.table_name=a.table_name ),0) as Table_crc FROM information_schema.TABLES a WHERE a.TABLE_TYPE='BASE TABLE' and a.TABLE_SCHEMA NOT IN('information_schema','mysql','performance_schema')")
	if err != nil {
		return nil, errors.New("Could not get table list")
	}
	for rows.Next() {
		var v Table
		err = rows.Scan(&v.Table_schema, &v.Table_name, &v.Engine, &v.Table_rows, &v.Data_length, &v.Index_length, &v.Table_crc)
		if err != nil {
			return vars, err
		}
		vars[v.Table_schema+"."+v.Table_name] = v
	}
	return vars, nil
}

func GetUsers(db *sqlx.DB) (map[string]Grant, error) {
	vars := make(map[string]Grant)
	rows, err := db.Queryx("SELECT user, host, password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10)    FROM mysql.user")
	if err != nil {
		return nil, errors.New("Could not get DB user list")
	}
	for rows.Next() {
		var g Grant
		err = rows.Scan(&g.User, &g.Host, &g.Password, &g.Hash)
		if err != nil {
			return vars, err
		}
		vars["'"+g.User+"'@'"+g.Host+"'"] = g
	}
	return vars, nil
}

func GetProxySQLUsers(db *sqlx.DB) (map[string]Grant, error) {
	vars := make(map[string]Grant)
	rows, err := db.Queryx("SELECT username, password  FROM mysql_users")
	if err != nil {
		return nil, errors.New("Could not get proxySQL user list")
	}
	for rows.Next() {
		var g Grant
		err = rows.Scan(&g.User, &g.Password)
		if err != nil {
			return vars, err
		}
		vars[g.User+":"+g.Password] = g
	}
	return vars, nil
}

func GetSchemas(db *sqlx.DB) ([]string, error) {
	sch := []string{}
	err := db.Select(&sch, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE  SCHEMA_NAME NOT IN('information_schema','mysql','performance_schema')")
	if err != nil {
		return nil, errors.New("Could not get table lis")
	}
	return sch, nil
}

func GetVariableByName(db *sqlx.DB, name string) (string, error) {
	var value string
	source := GetVariableSource(db)
	err := db.QueryRowx("SELECT UPPER(Variable_Value) AS Value FROM "+source+".global_variables WHERE Variable_Name = ?", name).Scan(&value)
	if err != nil {
		return "", errors.New("Could not get variable by name")
	}
	return value, nil
}

func FlushLogs(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH LOCAL BINARY LOGS")
	return err
}

func FlushTables(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH TABLES")
	return err
}

func FlushTablesNoLog(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH NO_WRITE_TO_BINLOG TABLES")
	return err
}

func MariaDBFlushTablesNoLogTimeout(db *sqlx.DB, timeout string) error {
	_, err := db.Exec("SET STATEMENT max_statement_time=" + timeout + " FOR FLUSH NO_WRITE_TO_BINLOG TABLES")
	//MySQL does not support DML timeout only SELECT
	return err
}

func FlushTablesWithReadLock(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH TABLES WITH READ LOCK")
	return err
}

func UnlockTables(db *sqlx.DB) error {
	_, err := db.Exec("UNLOCK TABLES")
	return err
}

func StopSlave(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) error {
	cmd := "STOP SLAVE"
	if IsMariaDB && Channel != "" {
		cmd += " '" + Channel + "'"
	}
	if IsMySQL && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return err
}

func StopSlaveIOThread(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) error {
	cmd := "STOP SLAVE IO_THREAD"
	if IsMariaDB && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "'  IO_THREAD"
	}
	if IsMySQL && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return err
}
func StopSlaveSQLThread(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) error {
	cmd := "STOP SLAVE SQL_THREAD"
	if IsMariaDB && Channel != "" {
		cmd = "STOP SLAVE '" + Channel + "' SQL_THREAD"
	}
	if IsMySQL && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return err
}

func SetSlaveHeartbeat(db *sqlx.DB, interval string, Channel string, IsMariaDB bool, IsMySQL bool) error {
	var err error

	err = StopSlave(db, Channel, IsMariaDB, IsMySQL)
	if err != nil {
		return err
	}
	stmt := "change master to MASTER_HEARTBEAT_PERIOD=" + interval
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}
	err = StartSlave(db, Channel, IsMariaDB, IsMySQL)
	if err != nil {
		return err
	}
	return err
}

func SetSlaveGTIDMode(db *sqlx.DB, mode string, Channel string, IsMariaDB bool, IsMySQL bool) error {
	var err error

	err = StopSlave(db, Channel, IsMariaDB, IsMySQL)
	if err != nil {
		return err
	}
	stmt := "change master to master_use_gtid=" + mode
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}
	err = StartSlave(db, Channel, IsMariaDB, IsMySQL)
	if err != nil {
		return err
	}
	return err
}

func StopAllSlaves(db *sqlx.DB) error {
	_, err := db.Exec("STOP ALL SLAVES")
	return err
}

func SkipBinlogEvent(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) error {
	if IsMariaDB {
		stmt := "SET @@default_master_connection='" + Channel + "'"
		_, err := db.Exec(stmt)
		if err != nil {
			return err
		}
	}
	_, err := db.Exec("SET GLOBAL sql_slave_skip_counter=1")
	return err
}

func StartSlave(db *sqlx.DB, Channel string, IsMariaDB bool, IsMySQL bool) error {
	cmd := "START SLAVE"
	if IsMariaDB && Channel != "" {
		cmd += " '" + Channel + "'"
	}
	if IsMySQL && Channel != "" {
		cmd += " FOR CHANNEL '" + Channel + "'"
	}
	_, err := db.Exec(cmd)
	return err
}

func ResetSlave(db *sqlx.DB, all bool, Channel string, IsMariaDB bool, IsMySQL bool) error {
	stmt := "RESET SLAVE"
	if IsMariaDB && Channel != "" {
		stmt += " '" + Channel + "'"
	}
	if all == true {
		stmt += " ALL"
		if IsMySQL && Channel != "" {
			stmt += " FOR CHANNEL '" + Channel + "'"
		}
	}
	_, err := db.Exec(stmt)
	return err
}

func ResetMaster(db *sqlx.DB) error {
	_, err := db.Exec("RESET MASTER")
	return err
}

func SetDefaultMasterConn(db *sqlx.DB, dmc string) error {
	myver, _ := GetDBVersion(db)
	if myver.IsMariaDB() {
		stmt := "SET @@default_master_connection='" + dmc + "'"
		_, err := db.Exec(stmt)
		return err
	}
	// MySQL replication channels are not supported at the moment
	return nil
}

/* Check for a list of slave prerequisites.
- Slave is connected
- Binary log on
- Connected to master
- No replication filters
*/
func CheckSlavePrerequisites(db *sqlx.DB, s string) bool {
	if debug {
		log.Printf("CheckSlavePrerequisites called") // remove those warnings !!
	}
	err := db.Ping()
	/* If slave is not online, skip to next iteration */
	if err != nil {
		log.Printf("WARN : Slave %s is offline. Skipping", s)
		return false
	}
	vars, _ := GetVariables(db)
	if vars["LOG_BIN"] == "OFF" {
		log.Printf("WARN : Binary log off. Slave %s cannot be used as candidate master.", s)
		return false
	}
	return true
}

func CheckBinlogFilters(m *sqlx.DB, s *sqlx.DB) (bool, error) {
	ms, err := GetMasterStatus(m)
	if err != nil {
		return false, errors.New("Cannot check binlog status on master")
	}
	ss, err := GetMasterStatus(s)
	if err != nil {
		return false, errors.New("ERROR: Can't check binlog status on slave")
	}
	if ms.Binlog_Do_DB == ss.Binlog_Do_DB && ms.Binlog_Ignore_DB == ss.Binlog_Ignore_DB {
		return true, nil
	}
	return false, nil
}

func CheckReplicationFilters(m *sqlx.DB, s *sqlx.DB) bool {
	mv, _ := GetVariables(m)
	sv, _ := GetVariables(s)
	if mv["REPLICATE_DO_TABLE"] == sv["REPLICATE_DO_TABLE"] && mv["REPLICATE_IGNORE_TABLE"] == sv["REPLICATE_IGNORE_TABLE"] && mv["REPLICATE_WILD_DO_TABLE"] == sv["REPLICATE_WILD_DO_TABLE"] && mv["REPLICATE_WILD_IGNORE_TABLE"] == sv["REPLICATE_WILD_IGNORE_TABLE"] && mv["REPLICATE_DO_DB"] == sv["REPLICATE_DO_DB"] && mv["REPLICATE_IGNORE_DB"] == sv["REPLICATE_IGNORE_DB"] {
		return true
	} else {
		return false
	}
}

func GetEventScheduler(dbM *sqlx.DB) bool {

	sES, _ := GetVariableByName(dbM, "EVENT_SCHEDULER")
	if sES != "ON" {
		return false
	}
	return true
}

func SetEventScheduler(db *sqlx.DB, state bool) error {
	var err error
	if state {
		stmt := "SET GLOBAL event_scheduler=1"
		_, err = db.Exec(stmt)
	} else {
		stmt := "SET GLOBAL event_scheduler=0"
		_, err = db.Exec(stmt)
	}

	return err
}

/* Check if a slave is in sync with his master */
func CheckSlaveSync(dbS *sqlx.DB, dbM *sqlx.DB) bool {
	if debug {
		log.Printf("CheckSlaveSync called")
	}
	sGtid, _ := GetVariableByName(dbS, "GTID_CURRENT_POS")
	mGtid, _ := GetVariableByName(dbM, "GTID_CURRENT_POS")
	if sGtid == mGtid {
		return true
	} else {
		return false
	}
}

func CheckSlaveSemiSync(dbS *sqlx.DB) bool {
	if debug {
		log.Printf("CheckSlaveSemiSync called")
	}
	sync, _ := GetVariableByName(dbS, "RPL_SEMI_SYNC_SLAVE_STATUS")

	if sync == "ON" {
		return true
	} else {
		return false
	}
}

func MasterWaitGTID(db *sqlx.DB, gtid string, timeout int) error {
	_, err := db.Exec("SELECT MASTER_GTID_WAIT(?, ?)", gtid, timeout)
	return err
}

func MasterPosWait(db *sqlx.DB, log string, pos string, timeout int) error {
	_, err := db.Exec("SELECT MASTER_POS_WAIT(?, ?, ?)", log, pos, timeout)
	return err
}

func SetReadOnly(db *sqlx.DB, flag bool) error {
	if flag == true {
		_, err := db.Exec("SET GLOBAL read_only=1")
		return err
	} else {
		_, err := db.Exec("SET GLOBAL read_only=0")
		return err
	}
}
func SetSuperReadOnly(db *sqlx.DB, flag bool) error {
	if flag == true {
		_, err := db.Exec("SET GLOBAL super_read_only=1")
		return err
	} else {
		_, err := db.Exec("SET GLOBAL super_read_only=0")
		return err
	}
}

func CheckLongRunningWrites(db *sqlx.DB, thresh int) int {
	var count int
	err := db.QueryRowx("select SUM(ct) from ( select count(*) as ct from information_schema.processlist  where command = 'Query' and time >= ? and info not like 'select%' union all select count(*) as ct  FROM  INFORMATION_SCHEMA.INNODB_TRX trx WHERE trx.trx_started < CURRENT_TIMESTAMP - INTERVAL ? SECOND) A", thresh, thresh).Scan(&count)
	if err != nil {
		log.Println("ERROR: Could not check long running writes", err)
	}
	return count
}

func KillThreads(db *sqlx.DB) {
	var ids []int
	db.Select(&ids, "SELECT Id FROM information_schema.PROCESSLIST WHERE Command != 'binlog dump' AND User != 'system user' AND Id != CONNECTION_ID()")
	for _, id := range ids {
		db.Exec("KILL ?", id)
	}
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
	for rows.Next() {
		var v SpiderTableNoSync
		rows.Scan(&v.Tbl_src, &v.Tbl_src_link, &v.Tbl_dest, &v.Srv_dsync, &v.Srv_sync)
		vars[v.Tbl_src] = v
	}
	return vars, err
}

func runPreparedExecConcurrent(db *sqlx.DB, n int, co int) error {
	stmt, err := db.Prepare("UPDATE replication_manager_schema.bench SET val=val+1 WHERE id=1")
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
