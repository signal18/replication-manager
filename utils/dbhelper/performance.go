// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/percona/go-mysql/query"
	"github.com/signal18/replication-manager/utils/version"
)

func GetQueryDigest(q string) string {
	f := query.Fingerprint(q)
	return f
}

func GetQueryExplain(db *sqlx.DB, version *version.Version, schema string, query string) ([]Explain, string, error) {
	pl := []Explain{}
	var err error
	if schema != "" {
		// Validate schema name before using in USE statement (cannot be parameterized)
		if err := ValidateIdentifier(schema); err != nil {
			return nil, "", fmt.Errorf("invalid schema name: %w", err)
		}
		_, err = db.Exec("USE " + QuoteMySQLIdentifier(schema))
	}
	stmt := "Explain " + query
	err = db.Select(&pl, stmt)
	if err != nil {
		return nil, stmt, fmt.Errorf("ERROR: Could not get Explain: %s", err)
	}
	return pl, stmt, nil
}

func GetMetaDataLock(db *sqlx.DB, version *version.Version) ([]MetaDataLock, string, error) {
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

func GetQueryResponseTime(db *sqlx.DB, version *version.Version) ([]ResponseTime, string, error) {
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

func AnalyzeQuery(db *sqlx.DB, version *version.Version, schema string, query string) (string, string, error) {
	var res string
	if schema != "" {
		// Validate schema name before using in USE statement (cannot be parameterized)
		if err := ValidateIdentifier(schema); err != nil {
			return "", "", fmt.Errorf("invalid schema name: %w", err)
		}
		db.Exec("USE " + QuoteMySQLIdentifier(schema))
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

func GetProcesslist(db *sqlx.DB, version *version.Version) ([]Processlist, string, error) {
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

func SetLongQueryTime(db *sqlx.DB, querytime string) (string, error) {
	// Convert to float64 for type-safe query execution (long_query_time accepts decimal values)
	timeValue, err := strconv.ParseFloat(querytime, 64)
	if err != nil {
		return "", fmt.Errorf("invalid query time value: %w", err)
	}

	query := "SET GLOBAL long_query_time = ?"
	_, err = db.Exec(query, timeValue)
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
	query := "SELECT COALESCE(B.SQL_TEXT,'') as query FROM performance_schema.events_statements_history_long B WHERE B.DIGEST = ?"
	rows, err := db.Queryx(query, Query.Digest)
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

func GetPlugins(db *sqlx.DB, myver *version.Version) (map[string]*Plugin, string, error) {

	vars := make(map[string]*Plugin)
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
		vars[v.Name] = &v
	}
	return vars, query, nil
}

func GetPFSVariablesInstruments(db *sqlx.DB) (map[string]string, string, error) {
	vars := make(map[string]string)
	query := "SELECT /*replication-manager*/ UPPER(NAME) AS variable_name, ENABLED AS VALUE from performance_schema.setup_instruments"
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

func GetNoBlockOnMetadataLock(db *sqlx.DB, myver *version.Version) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	noBlockOnMedataLock := "/*replication-manager*/ "
	if myver.IsMariaDB() && ((myver.Major == 10 && myver.Minor > 0) || myver.Major > 10) {
		noBlockOnMedataLock += "SET STATEMENT LOCK_WAIT_TIMEOUT=0 FOR "
	}
	return noBlockOnMedataLock
}

func SetQueryCaptureMode(db *sqlx.DB, mode string) (string, error) {
	var err error
	query := "SET GLOBAL log_output = ?"

	if mode == "TABLE" || mode == "FILE" {
		_, err = db.Exec(query, mode)
	} else {
		err = errors.New("Invalid mode: must be TABLE or FILE")
	}
	return query, err
}

func CheckLongRunningWrites(db *sqlx.DB, thresh int) (int, string, error) {
	var count int
	query := "select SUM(ct) from ( select count(*) as ct from information_schema.processlist  where command = 'Query' and time >= ? and info not like 'select%' union all select count(*) as ct  FROM  INFORMATION_SCHEMA.INNODB_TRX trx WHERE trx.trx_started < CURRENT_TIMESTAMP - INTERVAL ? SECOND) A"
	err := db.QueryRowx(query, thresh, thresh).Scan(&count)
	return count, query + "(" + strconv.Itoa(thresh) + ")", err
}

func KillThreads(db *sqlx.DB, myver *version.Version) (string, error) {
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

func KillThread(db *sqlx.DB, id string, myver *version.Version) (string, error) {
	// Convert to int64 for type-safe query execution
	idValue, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid thread id: %w", err)
	}

	if myver.IsPostgreSQL() {
		query := "SELECT pg_terminate_backend(?)"
		_, err = db.Exec(query, idValue)
		return query + " (" + id + ")", err
	}
	query := "KILL ?"
	_, err = db.Exec(query, idValue)
	return query + " (" + id + ")", err
}

func KillQuery(db *sqlx.DB, id string, myver *version.Version) (string, error) {
	// Convert to int64 for type-safe query execution
	idValue, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid query id: %w", err)
	}

	if myver.IsPostgreSQL() {
		query := "SELECT pg_terminate_backend(?)"
		_, err = db.Exec(query, idValue)
		return query + " (" + id + ")", err
	}
	query := "KILL QUERY ?"
	_, err = db.Exec(query, idValue)
	return query + " (" + id + ")", err
}
