// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains server status queries, variable retrieval, and monitoring functions.
// It provides utilities for querying global variables, status counters, system information,
// and runtime configuration of MySQL, MariaDB, and PostgreSQL servers.

package dbhelper

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// GetVariableSource returns the schema containing global variables
// Returns "performance_schema" for MySQL 5.7+ and "information_schema" for others
func GetVariableSource(db *sqlx.DB, myver *version.Version) string {
	var source string
	if !myver.IsMariaDB() && ((myver.Major >= 5 && myver.Minor >= 7) || myver.Major >= 6) {
		source = "performance_schema"
	} else {
		source = "information_schema"
	}
	return source
}

// GetDBVersion retrieves the database version
func GetDBVersion(db *sqlx.DB) (*version.Version, string, error) {
	stmt := "SELECT version()"
	var vString string
	var vComment string
	err := db.QueryRowx(stmt).Scan(&vString)
	if err != nil {
		return &version.Version{}, stmt, err
	}
	v, _ := version.NewMySQLVersion(vString, "")
	if !v.IsPostgreSQL() {
		stmt = "SELECT @@version_comment"
		db.QueryRowx(stmt).Scan(&vComment)
	}
	nv, _ := version.NewMySQLVersion(vString, vComment)
	return nv, stmt, nil
}

// MariaDBVersion parses a version string and returns numeric version
// Returns version as integer: major*10000 + minor*100 + patch
// Deprecated: Use version.Version type instead
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
}

// GetMaxscaleVersion retrieves MaxScale version
func GetMaxscaleVersion(db *sqlx.DB) (string, error) {
	var value string
	value = ""
	err := db.QueryRowx("Select @@maxscale_version").Scan(&value)
	return value, err
}

// GetStatus retrieves global status variables
// pfs_mutex: include Performance Schema mutex waits
// pfs_latch: include Performance Schema rwlock waits
// pfs_mem: include Performance Schema memory usage
func GetStatus(db *sqlx.DB, myver *version.Version, pfs_mutex bool, pfs_latch bool, pfs_mem bool) (map[string]string, string, error) {
	source := GetVariableSource(db, myver)
	vars := make(map[string]string)
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status"
	if pfs_mutex {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/mutex/innodb%' AND COUNT_STAR <>0"
	}
	if pfs_latch {
		query += " UNION ALL SELECT UPPER(REPLACE(EVENT_NAME,'/','_')) as Variable_name,COUNT_STAR as Value FROM performance_schema.events_waits_summary_global_by_event_name WHERE EVENT_NAME like 'wait/synch/rwlock/innodb%' AND COUNT_STAR <>0"
	}

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
	if pfs_mem {

		query_pfsmem := "SHOW ENGINE PERFORMANCE_SCHEMA STATUS"
		rows2, err := db.Queryx(query_pfsmem)

		if err != nil {
			return nil, query_pfsmem, errors.New("Could not get PFS engine status")
		}
		defer rows2.Close()
		var rtype string
		var rname string
		var rvalue uint64

		for rows2.Next() {

			err := rows2.Scan(&rtype, &rname, &rvalue)
			if err != nil {
				return nil, query, errors.New("Could not get results from PFS status scan")
			}

			if strings.Contains(rname, "performance_schema.memory") {
				vars["performance_schema_memory"] = strconv.FormatUint(rvalue, 10)
			}
		}
	}
	if vars["ARIA_PAGECACHE_BLOCKS_USED"] != "" {
		myUInt64, err := strconv.ParseUint(vars["ARIA_PAGECACHE_BLOCKS_USED"], 10, 64)
		if err == nil {
			vars["ARIA_PAGECACHE_BYTES_DATA"] = strconv.FormatUint(myUInt64*8192, 10)
		} else {
			vars["ARIA_PAGECACHE_BYTES_DATA"] = "0"
		}
	} else {
		vars["ARIA_PAGECACHE_BYTES_DATA"] = "0"
	}
	return vars, query, nil
}

// GetStatusAsInt retrieves global status variables as integers
func GetStatusAsInt(db *sqlx.DB, myver *version.Version) (map[string]int64, string, error) {
	type Variable struct {
		Variable_name string
		Value         int64
	}
	vars := make(map[string]int64)
	source := GetVariableSource(db, myver)
	query := "SELECT /*replication-manager*/ UPPER(Variable_name) AS variable_name, UPPER(Variable_Value) AS value FROM " + source + ".global_status"
	rows, err := db.Queryx(query)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Variable
		rows.Scan(&v.Variable_name, &v.Value)
		vars[v.Variable_name] = v.Value
	}
	return vars, query, nil
}

// GetVariables retrieves global variables (uppercased)
func GetVariables(db *sqlx.DB, myver *version.Version) (map[string]string, string, error) {
	return GetVariablesCase(db, myver, "UPPER")
}

// GetVariablesCase retrieves global variables with optional case conversion
// vcase: "UPPER" to uppercase values, otherwise keep original case
func GetVariablesCase(db *sqlx.DB, myver *version.Version, vcase string) (map[string]string, string, error) {

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

// GetVariableByName retrieves a specific global variable by name
func GetVariableByName(db *sqlx.DB, name string, myver *version.Version) (string, string, error) {
	var value string
	source := GetVariableSource(db, myver)
	query := "SELECT Variable_Value AS Value FROM " + source + ".global_variables WHERE Variable_Name = ?"
	err := db.QueryRowx(query, name).Scan(&value)
	if err != nil {
		return "", query, errors.New("Could not get variable by name")
	}
	return value, query, nil
}

// GetVariableByNameToUpper retrieves a specific global variable by name (uppercased)
func GetVariableByNameToUpper(db *sqlx.DB, name string, myver *version.Version) (string, string, error) {
	var value string
	source := GetVariableSource(db, myver)
	query := "SELECT UPPER(Variable_Value) AS Value FROM " + source + ".global_variables WHERE Variable_Name = ?"
	err := db.QueryRowx(query, name).Scan(&value)
	if err != nil {
		return "", query, errors.New("Could not get variable by name")
	}
	return value, query, nil
}

// GetEngineInnoDBStatus retrieves InnoDB engine status output
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

// GetEngineInnoDBVariables parses InnoDB status and returns key metrics
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

// GetCPUUsageFromUserStats retrieves CPU usage from USER_STATISTICS (MariaDB)
func GetCPUUsageFromUserStats(db *sqlx.DB) (string, string, error) {
	db.MapperFunc(strings.Title)
	var value string
	value = ""

	query := "select SUM(BUSY_TIME) FROM INFORMATION_SCHEMA.USER_STATISTICS"

	err := db.QueryRowx(query).Scan(&value)
	return value, query, err
}

// GetDisks retrieves disk usage information from information_schema.DISKS (MariaDB)
func GetDisks(db *sqlx.DB, myver *version.Version) ([]Disk, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []Disk{}
	query := `SELECT * FROM information_schema.DISKS`
	err := udb.Select(&ss, query)
	return ss, query, err
}

// GetProcesslistTable retrieves the process list with transaction information
// inactive_querying: include sleeping connections
// order_by_trx_time: sort by transaction time
// full_process_is: use full processlist query (vs SHOW FULL PROCESSLIST)
// limit: max number of processes to return
// user: filter by specific user (empty string for all users)
func GetProcesslistTable(db *sqlx.DB, version *version.Version, inactive_querying bool, order_by_trx_time bool, full_process_is bool, limit string, user string, queryLength int) ([]Processlist, string, error) {
	pl := []Processlist{}
	var err error
	stmt := ""
	filter_order_limit := ""
	filter_user := "1=1"
	if queryLength <= 0 {
		queryLength = 16384
	}
	qlStr := strconv.Itoa(queryLength)

	if user != "" {
		filter_user = "  User='" + user + "' "
	}
	if inactive_querying || order_by_trx_time {
		filter_order_limit = " WHERE " + filter_user
		if order_by_trx_time {
			filter_order_limit += " AND  (a.command='query' or b.trx_isolation_level IS NOT NULL)"
		}
	} else {
		filter_order_limit = " WHERE " + filter_user + " AND  a.command='query' "
	}
	if order_by_trx_time {
		if version.IsMariaDB() || (version.IsPercona() && version.GreaterEqual("8.0")) {
			filter_order_limit += " ORDER BY IF(a.Command='Query',a.TIME_MS, b.trx_started) DESC LIMIT " + limit
		} else {
			filter_order_limit += " ORDER BY IF(a.Command='Query',a.TIME, b.trx_started) DESC LIMIT " + limit
		}
	} else {
		if version.IsMariaDB() || (version.IsPercona() && version.GreaterEqual("8.0")) {
			filter_order_limit += " ORDER BY a.TIME_MS DESC LIMIT " + limit
		} else {
			filter_order_limit += " ORDER BY a.TIME DESC LIMIT " + limit
		}
	}
	if version.IsMariaDB() {
		//MariaDB
		stmt = "SELECT " +
			"a.Id, " +
			"a.User, " +
			"a.Host, " +
			"a.`Db` AS `db`, " +
			"IF(a.Command='Sleep' AND b.trx_isolation_level IS NOT NULL, 'Trx sleep', a.Command) as Command, " +
			"a.Time_ms/1000 as Time, " +
			"a.State, " +
			"SUBSTRING(COALESCE(a.INFO_BINARY,''),1," + qlStr + ") as Info, " +
			"CASE WHEN a.Max_Stage < 2 THEN Progress ELSE (a.Stage-1)/a.Max_Stage*100+a.Progress/a.Max_Stage END AS Progress, " +
			"GREATEST(COALESCE(TIMESTAMPDIFF(SECOND,b.trx_started, now()),0),0) as trx_time, " +
			"COALESCE(b.trx_isolation_level,'') as trx_isolation_level, " +
			"COALESCE(b.trx_tables_in_use,0) as trx_tables_in_use, " +
			"COALESCE(b.trx_tables_locked,0) as trx_tables_locked, " +
			"COALESCE(b.trx_lock_structs,0) as trx_lock_structs, " +
			"COALESCE(b.trx_lock_memory_bytes,0) as trx_lock_memory_bytes, " +
			"COALESCE(b.trx_rows_locked,0) as trx_rows_locked, " +
			"COALESCE(b.trx_rows_modified,0) as trx_rows_modified, " +
			"COALESCE(b.trx_is_read_only,0) as trx_is_read_only " +
			"FROM INFORMATION_SCHEMA.PROCESSLIST a " +
			"LEFT JOIN INFORMATION_SCHEMA.INNODB_TRX b ON b.trx_mysql_thread_id=a.id " +
			filter_order_limit
		if !full_process_is {
			stmt = "SHOW FULL PROCESSLIST"
		}
	} else if version.IsMySQLOrPercona() {
		//MySQL
		stmt = "SELECT " +
			"a.Id, " +
			"a.User, " +
			"a.Host, " +
			"a.`Db` AS `db`, " +
			"IF(a.Command='Sleep' AND b.trx_isolation_level IS NOT NULL, 'Trx sleep', a.Command) as Command, " +
			"a.Time as Time, " +
			"a.State, " +
			"SUBSTRING(COALESCE(a.INFO,''),1," + qlStr + ") as Info, " +
			"0 as Progress, " +
			"GREATEST(COALESCE(TIMESTAMPDIFF(SECOND,b.trx_started, now()),0),0) as trx_time, " +
			"COALESCE(b.trx_isolation_level,'') as trx_isolation_level, " +
			"COALESCE(b.trx_tables_in_use,0) as trx_tables_in_use, " +
			"COALESCE(b.trx_tables_locked,0) as trx_tables_locked, " +
			"COALESCE(b.trx_lock_structs,0) as trx_lock_structs, " +
			"COALESCE(b.trx_lock_memory_bytes,0) as trx_lock_memory_bytes, " +
			"COALESCE(b.trx_rows_locked,0) as trx_rows_locked, " +
			"COALESCE(b.trx_rows_modified,0) as trx_rows_modified, " +
			"COALESCE(b.trx_is_read_only,0) as trx_is_read_only " +
			"FROM INFORMATION_SCHEMA.PROCESSLIST a " +
			"LEFT JOIN INFORMATION_SCHEMA.INNODB_TRX b ON b.trx_mysql_thread_id=a.id " +
			filter_order_limit
		if version.IsPercona() && version.GreaterEqual("8.0") {
			stmt = "SELECT " +
				"a.Id, " +
				"a.User, " +
				"a.Host, " +
				"a.`Db` AS `db`, " +
				"IF(a.Command='Sleep' AND b.trx_isolation_level IS NOT NULL, 'Trx sleep', a.Command) as Command, " +
				"a.Time_ms as Time, " +
				"a.State, " +
				"SUBSTRING(COALESCE(a.INFO,''),1," + qlStr + ") as Info, " +
				"0 as Progress, " +
				"GREATEST(COALESCE(TIMESTAMPDIFF(SECOND,b.trx_started, now()),0),0) as trx_time, " +
				"COALESCE(b.trx_isolation_level,'') as trx_isolation_level, " +
				"COALESCE(b.trx_tables_in_use,0) as trx_tables_in_use, " +
				"COALESCE(b.trx_tables_locked,0) as trx_tables_locked, " +
				"COALESCE(b.trx_lock_structs,0) as trx_lock_structs, " +
				"COALESCE(b.trx_lock_memory_bytes,0) as trx_lock_memory_bytes, " +
				"COALESCE(b.trx_rows_locked,0) as trx_rows_locked, " +
				"COALESCE(b.trx_rows_modified,0) as trx_rows_modified, " +
				"COALESCE(b.trx_is_read_only,0) as trx_is_read_only " +
				"FROM INFORMATION_SCHEMA.PROCESSLIST a " +
				"LEFT JOIN INFORMATION_SCHEMA.INNODB_TRX b ON b.trx_mysql_thread_id=a.id " +
				filter_order_limit
		}
		if !full_process_is {
			stmt = "SHOW FULL PROCESSLIST"
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
