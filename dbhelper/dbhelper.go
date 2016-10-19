// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package dbhelper
package dbhelper

import (
	"database/sql"
	"fmt"
	"github.com/jmoiron/sqlx"
	"hash/crc64"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
)

const debug = false

type Processlist struct {
	Id       uint64
	User     string
	Host     string
	Database sql.NullString
	Command  string
	Time     float64
	State    string
}

type SlaveHosts struct {
	Server_id uint64
	Host      string
	Port      uint
	Master_id uint64
}

type MasterStatus struct {
	File             string
	Position         uint
	Binlog_Do_DB     string
	Binlog_Ignore_DB string
}

type SlaveStatus struct {
	Connection_name               string
	Slave_SQL_State               string
	Slave_IO_State                string
	Master_Host                   string
	Master_User                   string
	Master_Port                   uint
	Connect_Retry                 uint
	Master_Log_File               string
	Read_Master_Log_Pos           uint
	Relay_Log_File                string
	Relay_Log_Pos                 uint
	Relay_Master_Log_File         string
	Slave_IO_Running              string
	Slave_SQL_Running             string
	Replicate_Do_DB               string
	Replicate_Ignore_DB           string
	Replicate_Do_Table            string
	Replicate_Ignore_Table        string
	Replicate_Wild_Do_Table       string
	Replicate_Wild_Ignore_Table   string
	Last_Errno                    uint
	Last_Error                    string
	Skip_Counter                  uint
	Exec_Master_Log_Pos           uint
	Relay_Log_Space               uint
	Until_Condition               string
	Until_Log_File                string
	Until_Log_Pos                 uint
	Master_SSL_Allowed            string
	Master_SSL_CA_File            string
	Master_SSL_CA_Path            string
	Master_SSL_Cert               string
	Master_SSL_Cipher             string
	Master_SSL_Key                string
	Seconds_Behind_Master         sql.NullInt64
	Master_SSL_Verify_Server_Cert string
	Last_IO_Errno                 uint
	Last_IO_Error                 string
	Last_SQL_Errno                uint
	Last_SQL_Error                string
	Replicate_Ignore_Server_Ids   string
	Master_Server_Id              uint
	Master_SSL_Crl                string
	Master_SSL_Crlpath            string
	Using_Gtid                    string
	Gtid_IO_Pos                   string
	Retried_transactions          uint
	Max_relay_log_size            uint
	Executed_log_entries          uint
	Slave_received_heartbeats     uint
	Slave_heartbeat_period        float64
	Gtid_Slave_Pos                string
}

type Privileges struct {
	Select_priv      string
	Process_priv     string
	Super_priv       string
	Repl_slave_priv  string
	Repl_client_priv string
	Reload_priv      string
}

type SpiderTableNoSync struct {
	Tbl_src      string
	Tbl_src_link string
	Tbl_dest     string
	Srv_dsync    string
	Srv_sync     string
}

/* Connect to a MySQL server. Must be deprecated, use MySQLConnect instead */
func Connect(user string, password string, address string) *sqlx.DB {
	db, _ := sqlx.Open("mysql", user+":"+password+"@"+address+"/")
	err := db.Ping()
	if err != nil {
		log.Fatal(err)
	}
	return db
}

func MySQLConnect(user string, password string, address string, parameters ...string) (*sqlx.DB, error) {
	dsn := user + ":" + password + "@" + address + "/"
	if len(parameters) > 0 {
		dsn += "?" + parameters[0]
	}
	db, err := sqlx.Connect("mysql", dsn)
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

func GetProcesslist(db *sqlx.DB) []Processlist {
	pl := []Processlist{}
	err := db.Select(&pl, "SELECT Id, User, Host, `Db` AS `Database`, Command, Time_ms as Time, State FROM INFORMATION_SCHEMA.PROCESSLIST")
	if err != nil {
		log.Fatalln("ERROR: Could not get processlist", err)
	}
	return pl
}

func GetHostFromProcessList(db *sqlx.DB, user string) string {
	pl := []Processlist{}
	pl = GetProcesslist(db)
	for i := range pl {
		if pl[i].User == user {
			return strings.Split(pl[i].Host, ":")[0]
		}
	}
	return "N/A"
}
func GetPrivileges(db *sqlx.DB, user string, host string, ip string) (Privileges, error) {
	db.MapperFunc(strings.Title)
	splitip := strings.Split(ip, ".")

	iprange1 := splitip[0] + ".%.%.%"
	iprange2 := splitip[0] + "." + splitip[1] + ".%.%"
	iprange3 := splitip[0] + "." + splitip[1] + "." + splitip[2] + ".%"
	priv := Privileges{}
	stmt := "SELECT MAX(Select_priv) as Select_priv, MAX(Process_priv) as Process_priv, MAX(Super_priv) as Super_priv, MAX(Repl_slave_priv) as Repl_slave_priv, MAX(Repl_client_priv) as Repl_client_priv, MAX(Reload_priv) as Reload_priv FROM mysql.user WHERE user = ? AND host IN(?,?,?,?,?,?,?,?,?)"
	row := db.QueryRowx(stmt, user, host, ip, "%", ip+"/255.0.0.0", ip+"/255.255.0.0", ip+"/255.255.255.0", iprange1, iprange2, iprange3)
	//	fmt.Println("'" + user + "',''" + host + "','" + ip + "', ''" + ip + "/255.0.0.0'" + ", ''" + ip + "/255.255.0.0'" + "','" + ip + "/255.255.255.0" + "','" + iprange1 + "','" + iprange2 + "','" + iprange3)
	err := row.StructScan(&priv)
	/*
		if err != nil && err == sql.ErrNoRows {
			row := db.QueryRowx(stmt, user, "%")
			err = row.StructScan(&priv)
			if err != nil && err == sql.ErrNoRows {
				row := db.QueryRowx(stmt, user, misc.GetLocalIP())
				err = row.StructScan(&priv)
				return priv, err
			}
			return priv, err
		} */
	return priv, err
}

func GetSlaveStatus(db *sqlx.DB) (SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := SlaveStatus{}
	err := udb.Get(&ss, "SHOW SLAVE STATUS")
	return ss, err
}

func GetMSlaveStatus(db *sqlx.DB, conn string) (SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := SlaveStatus{}
	err := udb.Get(&ss, "SHOW SLAVE '"+conn+"' STATUS")
	return ss, err
}

func GetAllSlavesStatus(db *sqlx.DB) ([]SlaveStatus, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []SlaveStatus{}
	err := udb.Select(&ss, "SHOW ALL SLAVES STATUS")
	return ss, err
}

func SetHeartbeatTable(db *sqlx.DB) error {
	stmt := "SET sql_log_bin=0"
	_, err := db.Exec(stmt)
	if err != nil {
		return err
	}

	stmt = "CREATE DATABASE IF NOT EXISTS replication_manager_schema"
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}
	stmt = "CREATE TABLE IF NOT EXISTS replication_manager_schema.heartbeat(uuid varchar(128) PRIMARY KEY,date timestamp, status CHAR(1) ) engine=innodb"
	_, err = db.Exec(stmt)
	if err != nil {
		return err
	}
	return err
}

func WriteHeartbeat(db *sqlx.DB, uuid string, status string) error {
	stmt := "INSERT INTO replication_manager_schema.heartbeat(uuid,date,status) SELECT '" + uuid + "', NOW(),'" + status + "'"
	_, err := db.Exec(stmt)
	return err
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

func ResetAllSlaves(db *sqlx.DB) error {
	ss, err := GetAllSlavesStatus(db)
	if err != nil {
		return err
	}
	for _, src := range ss {
		err = SetDefaultMasterConn(db, src.Connection_name)
		if err != nil {
			return err
		}
		err = ResetSlave(db, true)
		if err != nil {
			return err
		}
	}
	return err
}

func GetMasterStatus(db *sqlx.DB) (MasterStatus, error) {
	db.MapperFunc(strings.Title)
	ms := MasterStatus{}
	err := db.Get(&ms, "SHOW MASTER STATUS")
	return ms, err
}

func GetSlaveHosts(db *sqlx.DB) map[string]interface{} {
	rows, err := db.Queryx("SHOW SLAVE HOSTS")
	if err != nil {
		log.Fatalln("ERROR: Could not get slave hosts", err)
	}
	defer rows.Close()
	results := make(map[string]interface{})
	for rows.Next() {
		err = rows.MapScan(results)
		if err != nil {
			log.Fatal(err)
		}
	}
	return results
}

func GetSlaveHostsArray(db *sqlx.DB) []SlaveHosts {
	sh := []SlaveHosts{}
	err := db.Select(&sh, "SHOW SLAVE HOSTS")
	if err != nil {
		log.Fatalln("ERROR: Could not get slave hosts array", err)
	}
	return sh
}

func GetSlaveHostsDiscovery(db *sqlx.DB) []string {
	slaveList := []string{}
	/* This method does not return the server ports, so we cannot rely on it for the time being. */
	err := db.Select(&slaveList, "select host from information_schema.processlist where command ='binlog dump'")
	if err != nil {
		log.Fatalln("ERROR: Could not get slave hosts from the processlist", err)
	}
	return slaveList
}

func GetStatus(db *sqlx.DB) map[string]string {
	type Variable struct {
		Variable_name string
		Value         string
	}
	vars := make(map[string]string)
	rows, err := db.Queryx("SELECT Variable_name AS variable_name, Variable_Value AS value FROM information_schema.global_status")
	if err != nil {
		log.Fatalln("ERROR: Could not get status variable", err)
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			log.Fatalln("ERROR: Could not get results from status scan", err)
		}
		vars[v.Variable_name] = v.Value
	}
	return vars
}

func GetStatusAsInt(db *sqlx.DB) map[string]int64 {
	type Variable struct {
		Variable_name string
		Value         int64
	}
	vars := make(map[string]int64)
	rows, err := db.Queryx("SELECT Variable_name AS variable_name, Variable_Value AS value FROM information_schema.global_status")
	if err != nil {
		log.Fatal("ERROR: Could not get status as integer", err)
	}
	for rows.Next() {
		var v Variable
		rows.Scan(&v.Variable_name, &v.Value)
		vars[v.Variable_name] = v.Value
	}
	return vars
}

func GetVariables(db *sqlx.DB) (map[string]string, error) {
	type Variable struct {
		Variable_name string
		Value         string
	}
	vars := make(map[string]string)
	rows, err := db.Queryx("SELECT Variable_name AS variable_name, Variable_Value AS value FROM information_schema.global_variables")
	if err != nil {
		return vars, err
	}
	for rows.Next() {
		var v Variable
		err := rows.Scan(&v.Variable_name, &v.Value)
		if err != nil {
			return vars, err
		}
		vars[v.Variable_name] = v.Value
	}
	return vars, err
}

func GetVariableByName(db *sqlx.DB, name string) string {
	var value string
	err := db.QueryRowx("SELECT Variable_Value AS Value FROM information_schema.global_variables WHERE Variable_Name = ?", name).Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not get variable by name", err)
	}
	return value
}

func FlushTables(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH TABLES")
	return err
}

func FlushTablesNoLog(db *sqlx.DB) error {
	_, err := db.Exec("FLUSH NO_WRITE_TO_BINLOG TABLES")
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

func StopSlave(db *sqlx.DB) error {
	_, err := db.Exec("STOP SLAVE")
	return err
}

func StopAllSlaves(db *sqlx.DB) error {
	_, err := db.Exec("STOP ALL SLAVES")
	return err
}

func StartSlave(db *sqlx.DB) error {
	_, err := db.Exec("START SLAVE")
	return err
}

func ResetSlave(db *sqlx.DB, all bool) error {
	stmt := "RESET SLAVE"
	if all == true {
		stmt += " ALL"
	}
	_, err := db.Exec(stmt)
	return err
}

func ResetMaster(db *sqlx.DB) error {
	_, err := db.Exec("RESET MASTER")
	return err
}

func SetDefaultMasterConn(db *sqlx.DB, dmc string) error {
	stmt := "SET default_master_connection='" + dmc + "'"
	_, err := db.Exec(stmt)
	return err
}

/* Check for a list of slave prerequisites.
- Slave is connected
- Binary log on
- Connected to master
- No replication filters
*/
func CheckSlavePrerequisites(db *sqlx.DB, s string) bool {
	if debug {
		log.Printf("CheckSlavePrerequisites called")
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

func CheckBinlogFilters(m *sqlx.DB, s *sqlx.DB) bool {
	ms, err := GetMasterStatus(m)
	if err != nil {
		log.Println("ERROR: Can't check binlog status on master")
		return false
	}
	ss, err := GetMasterStatus(s)
	if err != nil {
		log.Println("ERROR: Can't check binlog status on slave")
		return false
	}
	if ms.Binlog_Do_DB == ss.Binlog_Do_DB && ms.Binlog_Ignore_DB == ss.Binlog_Ignore_DB {
		if debug {
			log.Println("INFO: Binlog filters check OK")
		}
		return true
	} else {
		if debug {
			log.Println("INFO: Binlog filters differ on both servers")
		}
		return false
	}
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

/* Check if server is connected to declared master */
func IsSlaveof(db *sqlx.DB, s string, m string) bool {
	ss, err := GetSlaveStatus(db)
	if err != nil {
		// log.Printf("WARN : Server %s is not a slave. Skipping", s)
		return false
	}
	masterHost, err := CheckHostAddr(ss.Master_Host)
	if err != nil {
		// log.Println("ERROR: Could not resolve master hostname", ss.Master_Host)
	}
	if masterHost != m {
		// log.Printf("WARN : Slave %s is not connected to the current master %s (master_host=%s). Skipping", s, m, masterHost)
		return false
	}
	return true
}

/* Check if a slave is in sync with his master */
func CheckSlaveSync(dbS *sqlx.DB, dbM *sqlx.DB) bool {
	if debug {
		log.Printf("CheckSlaveSync called")
	}
	sGtid := GetVariableByName(dbS, "GTID_CURRENT_POS")
	mGtid := GetVariableByName(dbM, "GTID_CURRENT_POS")
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
	sync := GetVariableByName(dbS, "RPL_SEMI_SYNC_SLAVE_STATUS")

	if sync == "ON" {
		return true
	} else {
		return false
	}
}

func MasterPosWait(db *sqlx.DB, gtid string, timeout int) error {
	_, err := db.Exec("SELECT MASTER_GTID_WAIT(?, ?)", gtid, timeout)
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

func CheckLongRunningWrites(db *sqlx.DB, thresh int) int {
	var count int
	err := db.QueryRowx("select count(*) from information_schema.processlist where command = 'Query' and time >= ? and info not like 'select%'", thresh).Scan(&count)
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
	} else {
		ha, err := net.LookupHost(h)
		if err != nil {
			return "", err
		} else {
			return ha[0], err
		}
	}
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
	stmt, err := db.Prepare("UPDATE replication_manager_schema.bench SET val=val+1 ")
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
	_, err := db.Exec("set binlog_format='STATEMENT'")
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench  select  sleep(" + fmt.Sprintf("%d", time) + ") from dual")
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
	_, err = db.Exec("CREATE OR REPLACE TABLE replication_manager_schema.bench(val bigint unsigned )")
	if err != nil {
		return err
	}
	_, err = db.Exec("INSERT INTO replication_manager_schema.bench VALUES(1)")
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
