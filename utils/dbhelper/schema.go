// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// This file contains table, schema, user, and event management functions.
// It provides utilities for creating/dropping tables, managing users and grants,
// configuring Group Replication, and handling database schema operations.

package dbhelper

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/crc64"
	"log"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// Schema and table management functions

// columnRow represents a row from information_schema.COLUMNS
type columnRow struct {
	TableSchema      string
	TableName        string
	OrdinalPosition  int
	ColumnName       string
	ColumnType       string
	IsNullable       string
	ColumnDefault    sql.NullString
	Extra            string
	CharacterSetName sql.NullString
	CollationName    sql.NullString
}

// indexRow represents a row from information_schema.STATISTICS
type indexRow struct {
	TableSchema string
	TableName   string
	IndexName   string
	NonUnique   int
	IndexType   string
	SeqInIndex  int
	ColumnName  string
	SubPart     sql.NullInt64
}

// SetEventStatus enables or disables a database event
func SetEventStatus(db *sqlx.DB, ev Event, status int64) (string, error) {
	definer := strings.Split(ev.Definer, "@")
	if len(definer) != 2 {
		return "", errors.New("Incorrect definer format")
	}

	// Validate all identifiers before using
	if err := ValidateIdentifier(definer[0]); err != nil {
		return "", fmt.Errorf("invalid definer user: %w", err)
	}
	if err := ValidateIdentifier(definer[1]); err != nil {
		return "", fmt.Errorf("invalid definer host: %w", err)
	}
	if err := ValidateIdentifier(ev.Db); err != nil {
		return "", fmt.Errorf("invalid database name: %w", err)
	}
	if err := ValidateIdentifier(ev.Name); err != nil {
		return "", fmt.Errorf("invalid event name: %w", err)
	}

	stmt := fmt.Sprintf("ALTER /*replication-manager*/ DEFINER=%s@%s EVENT %s.%s",
		QuoteMySQLIdentifier(definer[0]),
		QuoteMySQLIdentifier(definer[1]),
		QuoteMySQLIdentifier(ev.Db),
		QuoteMySQLIdentifier(ev.Name))

	if status == 3 {
		stmt += " DISABLE ON SLAVE"
	} else {
		stmt += " ENABLE"
	}

	_, err := db.Exec(stmt)
	if err != nil {
		return stmt, err
	}
	return stmt, nil
}

// GetTableChecksumResult retrieves table checksum results
func GetTableChecksumResult(db *sqlx.DB) (map[uint64]chunk, string, error) {
	vars := make(map[uint64]chunk)
	query := "SELECT /*replication-manager*/ * FROM replication_manager_schema.table_checksum"
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

// GetTables retrieves all tables from all schemas (excluding system schemas)
func GetTables(db *sqlx.DB, myver *version.Version, getColumns, getIndexes bool) (map[string]*Table, []Table, string, error) {
	var tablemap map[string]*Table = make(map[string]*Table)
	var tblList []Table
	logs := ""

	schemas, q, err := getSchemas(db, myver)
	logs += q
	if err != nil {
		return nil, nil, logs, err
	}

	for _, schema := range schemas {
		crc64Table := crc64.MakeTable(0xC96C5795D7870F42)
		var qlog string
		tables, qlog, err := getTablesForSchema(db, myver, schema, crc64Table)
		logs += qlog
		if err != nil {
			return nil, nil, logs, err
		}

		for _, t := range tables {
			key := t.TableSchema + "." + t.TableName
			tblList = append(tblList, t)
			tablemap[key] = &t
		}

		if getColumns {
			qlog, err := getTableColumns(db, myver, schema, tablemap, crc64Table)
			logs += qlog
			if err != nil {
				return tablemap, tblList, logs, err
			}
		}

		if getIndexes {
			qlog, err := getTableIndexes(db, myver, schema, tablemap, crc64Table)
			logs += qlog
			if err != nil {
				return tablemap, tblList, logs, err
			}
		}
	}

	return tablemap, tblList, logs, nil
}

func getSchemas(db *sqlx.DB, myver *version.Version) ([]string, string, error) {
	var schemas []string
	query := schemaQuery(myver)
	err := db.Select(&schemas, query)
	if err != nil {
		return nil, query, fmt.Errorf("could not get schema list: %w", err)
	}
	return schemas, query, nil
}

func schemaQuery(myver *version.Version) string {
	if myver.IsPostgreSQL() {
		return "SELECT datname FROM pg_database WHERE datistemplate = false AND datname NOT IN ('postgres')"
	}
	return "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA WHERE SCHEMA_NAME NOT IN('information_schema','mysql','performance_schema','sys') AND SCHEMA_NAME NOT LIKE '#%'"
}

func getTablesForSchema(db *sqlx.DB, myver *version.Version, schema string, crc64Table *crc64.Table) ([]Table, string, error) {
	var logs string
	query := tablesQuery(db, myver, schema)
	logs += "\n" + query

	rows, err := db.Queryx(query)
	if err != nil {
		return nil, logs, errors.New("could not get table list: " + err.Error())
	}
	defer rows.Close()

	var tables []Table

	for rows.Next() {
		var t Table
		if err := rows.Scan(
			&t.TableSchema,
			&t.TableName,
			&t.Engine,
			&t.TableRows,
			&t.DataLength,
			&t.IndexLength,
			&t.TableCrc,
		); err != nil {
			return nil, logs, err
		}

		crc, qlog := getTableDDLCRC(db, myver, schema, t.TableName, crc64Table)
		logs += qlog
		if crc != 0 {
			t.TableCrc = crc
		}

		tables = append(tables, t)
	}

	return tables, logs, nil
}

func tablesQuery(db *sqlx.DB, myver *version.Version, schema string) string {
	if myver.IsPostgreSQL() {
		return fmt.Sprintf(`SELECT '%s' AS table_schema, tablename AS table_name, 'BASE TABLE' AS engine,
			0::bigint AS table_rows, 0::bigint AS data_length, 0::bigint AS index_length, 0::bigint AS table_crc
			FROM pg_tables WHERE schemaname = 'public'`, schema)
	}
	return fmt.Sprintf(`SELECT table_schema, table_name, engine, table_rows, data_length, index_length, 0 AS table_crc
		FROM information_schema.TABLES WHERE table_schema = '%s' AND table_type = 'BASE TABLE'`, schema)
}

func getTableDDLCRC(db *sqlx.DB, myver *version.Version, schema, table string, crc64Table *crc64.Table) (uint64, string) {
	query := ddlQuery(myver, schema, table)
	if query == "" {
		return 0, ""
	}

	var ddl string
	if err := db.QueryRowx(query).Scan(&ddl); err != nil {
		return 0, query + "\n"
	}

	return crc64.Checksum([]byte(ddl), crc64Table), query + "\n"
}

func ddlQuery(myver *version.Version, schema, table string) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	return fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`", schema, table)
}

func getTableColumns(db *sqlx.DB, myver *version.Version, schema string, tablemap map[string]*Table, crc64Table *crc64.Table) (string, error) {
	query := columnDefQuery(myver, schema)
	logs := "\n" + query

	defer func() {
		for _, t := range tablemap {
			t.HashColumns(crc64Table)
		}
	}()

	rows, err := db.Queryx(query)
	if err != nil {
		return logs, err
	}
	defer rows.Close()

	for rows.Next() {
		var r columnRow
		if err := rows.Scan(
			&r.TableSchema,
			&r.TableName,
			&r.OrdinalPosition,
			&r.ColumnName,
			&r.ColumnType,
			&r.IsNullable,
			&r.ColumnDefault,
			&r.Extra,
			&r.CharacterSetName,
			&r.CollationName,
		); err != nil {
			return logs, err
		}

		key := r.TableSchema + "." + r.TableName
		t, ok := tablemap[key]
		if !ok {
			continue
		}

		col := Column{
			Name:     r.ColumnName,
			Type:     strings.ToLower(r.ColumnType),
			Nullable: r.IsNullable == "YES",
			Extra:    normalizeExtraStr(r.Extra),
		}

		if r.ColumnDefault.Valid {
			col.Default = normalizeDefaultStr(r.ColumnDefault.String)
		}

		if r.CharacterSetName.Valid {
			col.Charset = &r.CharacterSetName.String
		}
		if r.CollationName.Valid {
			col.Collation = &r.CollationName.String
		}

		t.TableColumns = append(t.TableColumns, col)
	}

	return logs, nil
}

func columnDefQuery(myver *version.Version, schema string) string {
	if myver.IsPostgreSQL() {
		return fmt.Sprintf(`SELECT '%s', table_name, column_name, ordinal_position, column_default,
			is_nullable, data_type, character_maximum_length, numeric_precision, numeric_scale,
			'' AS character_set_name, '' AS collation_name, udt_name AS column_type,
			'' AS column_key, '' AS extra
			FROM information_schema.columns WHERE table_schema = 'public' ORDER BY table_name, ordinal_position`, schema)
	}
	return fmt.Sprintf(`SELECT table_schema, table_name, column_name, ordinal_position, column_default,
		is_nullable, data_type, character_maximum_length, numeric_precision, numeric_scale,
		character_set_name, collation_name, column_type, column_key, extra
		FROM information_schema.COLUMNS WHERE table_schema = '%s' ORDER BY table_name, ordinal_position`, schema)
}

func normalizeDefaultStr(def string) *string {
	def = strings.TrimSpace(def)
	if strings.EqualFold(def, "NULL") {
		return nil
	}
	return &def
}

func normalizeExtraStr(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}

func getTableIndexes(db *sqlx.DB, myver *version.Version, schema string, tablemap map[string]*Table, crc64Table *crc64.Table) (string, error) {
	query := indexDefQuery(myver, schema)
	logs := "\n" + query

	defer func() {
		for _, t := range tablemap {
			t.HashIndexes(crc64Table)
		}
	}()

	rows, err := db.Queryx(query)
	if err != nil {
		return logs, err
	}
	defer rows.Close()

	for rows.Next() {
		var r indexRow
		if err := rows.Scan(
			&r.TableSchema,
			&r.TableName,
			&r.IndexName,
			&r.NonUnique,
			&r.IndexType,
			&r.SeqInIndex,
			&r.ColumnName,
			&r.SubPart,
		); err != nil {
			return logs, err
		}

		key := r.TableSchema + "." + r.TableName
		t, ok := tablemap[key]
		if !ok {
			continue
		}

		// Find or create index
		var idx *Index
		for i := range t.TableIndexes {
			if t.TableIndexes[i].Name == r.IndexName {
				idx = &t.TableIndexes[i]
				break
			}
		}
		if idx == nil {
			idx = &Index{
				Name:   r.IndexName,
				Unique: r.NonUnique == 0,
				Type:   strings.ToUpper(r.IndexType),
			}
			t.TableIndexes = append(t.TableIndexes, *idx)
			idx = &t.TableIndexes[len(t.TableIndexes)-1]
		}

		// Add index column (order preserved by SQL)
		col := IndexColumn{
			Name: r.ColumnName,
		}
		if r.SubPart.Valid {
			p := uint16(r.SubPart.Int64)
			col.Prefix = &p
		}

		idx.Columns = append(idx.Columns, col)
	}

	return logs, nil
}

func indexDefQuery(myver *version.Version, schema string) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	return fmt.Sprintf(`SELECT table_schema, table_name, non_unique, index_name, seq_in_index,
		column_name, nullable, index_type, sub_part
		FROM information_schema.STATISTICS WHERE table_schema = '%s'
		ORDER BY table_name, index_name, seq_in_index`, schema)
}

// AnalyzeTable performs table analysis
func AnalyzeTable(db *sqlx.DB, myver *version.Version, table string, nobinlog, persistent bool, columns string, indexes string) (string, error) {
	quotedTable, err := QuoteMySQLTableIdentifier(table)
	if err != nil {
		return "", err
	}

	query := "ANALYZE "
	if nobinlog {
		query += "LOCAL "
	}
	query += "TABLE " + quotedTable

	if myver.Greater("10.4.0") && myver.IsMariaDB() && persistent {
		if columns == "ALL" {
			query += " PERSISTENT FOR ALL"
		} else if columns != "" || indexes != "" {
			query += " PERSISTENT FOR COLUMNS (" + columns + ") INDEXES (" + indexes + ")"
		}
	}

	_, err = db.Exec(query)
	if err != nil {
		log.Println("ERROR: Could not analyze table", err)
	}
	return query, err
}

// SetUserPassword changes a user's password
func SetUserPassword(db *sqlx.DB, myver *version.Version, user_host string, user_name string, new_password string) (string, error) {
	// Validate user and host before using
	if err := ValidateIdentifier(user_name); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}

	// Build query using parameterization where possible
	// ALTER USER syntax doesn't support full parameterization, but we validate and escape
	query := fmt.Sprintf("ALTER USER %s@%s IDENTIFIED BY ?",
		QuoteMySQLIdentifier(user_name),
		QuoteMySQLIdentifier(user_host))

	_, err := db.Exec(query, new_password)
	if err != nil {
		return query, err
	}
	return query, nil
}

// RenameUserPassword renames a user
func RenameUserPassword(db *sqlx.DB, myver *version.Version, user_host string, old_user_name string, new_password string, new_user_name string) (string, error) {
	// Validate usernames
	if err := ValidateIdentifier(old_user_name); err != nil {
		return "", fmt.Errorf("invalid old username: %w", err)
	}
	if err := ValidateIdentifier(new_user_name); err != nil {
		return "", fmt.Errorf("invalid new username: %w", err)
	}

	query := fmt.Sprintf("RENAME USER %s@%s TO %s@%s",
		QuoteMySQLIdentifier(old_user_name),
		QuoteMySQLIdentifier(user_host),
		QuoteMySQLIdentifier(new_user_name),
		QuoteMySQLIdentifier(user_host))

	_, err := db.Exec(query)
	if err != nil {
		return query, err
	}
	return query, nil
}

// SetUserGrants grants privileges to a user
func SetUserGrants(ctx context.Context, conn *sqlx.Conn, myver *version.Version, user_host string, user_name string, grants ...string) (string, error) {
	// Validate user and host
	if err := ValidateIdentifier(user_name); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}

	var query string
	for _, grant := range grants {
		// GRANT syntax doesn't support parameterization, but we validate identifiers
		query = fmt.Sprintf("GRANT %s TO %s@%s",
			grant, // Grant strings are controlled by application
			QuoteMySQLIdentifier(user_name),
			QuoteMySQLIdentifier(user_host))

		_, err := conn.ExecContext(ctx, query)
		if err != nil {
			return query, err
		}
	}
	return query, nil
}

// Additional functions extracted from backup - some contain SQL injection risks marked with TODO

// GetUsers retrieves all users from the database
func GetUsers(db *sqlx.DB, myver *version.Version) (map[string]*Grant, string, error) {
	vars := make(map[string]*Grant)
	query := "SELECT user, host, password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10) FROM mysql.user where host<>'localhost'"
	if myver.IsPostgreSQL() {
		query = "SELECT usename as user, '%' as host, 'unknow' as password, 0 FROM pg_catalog.pg_user"
	} else if myver.IsMySQLOrPercona() && myver.GreaterEqual("5.7.6") {
		query = "SELECT user, host, authentication_string as password, CONV(LEFT(MD5(concat(user,host)), 16), 16, 10) FROM mysql.user"
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
		vars["'"+g.User+"'@'"+g.Host+"'"] = &g
	}
	return vars, query, nil
}

// GetProxySQLUsers retrieves users from ProxySQL
func GetProxySQLUsers(db *sqlx.DB) (map[string]Grant, string, error) {
	vars := make(map[string]Grant)
	query := "SELECT username, password FROM mysql_users"
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

// GetEventStatus retrieves event status
func GetEventStatus(db *sqlx.DB, version *version.Version) ([]Event, string, error) {
	db.MapperFunc(strings.Title)
	udb := db.Unsafe()
	ss := []Event{}

	query := "SELECT /*replication-manager*/ db as Db, name as Name, definer as Definer, status+0 AS Status FROM mysql.event"
	if version.IsMySQLOrPercona() && version.Major >= 8 {
		query = "SELECT /*replication-manager*/ EVENT_SCHEMA as Db, EVENT_NAME as Name, definer as Definer, status+0 AS Status FROM information_schema.EVENTS"
	}
	err := udb.Select(&ss, query)
	if err != nil {
		return nil, query, errors.New("Could not get event status")
	}
	return ss, query, err
}

// IsGroupReplicationMaster checks if server is a group replication master
func IsGroupReplicationMaster(db *sqlx.DB, myver *version.Version, host string) (bool, error) {
	var value bool
	query := "SELECT 1 FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE' AND MEMBER_ROLE='PRIMARY' AND MEMBER_HOST=?"
	err := db.QueryRowx(query, host).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return value, err
}

// IsGroupReplicationSlave checks if server is a group replication slave
func IsGroupReplicationSlave(db *sqlx.DB, myver *version.Version, host string) (bool, error) {
	var value bool
	query := "SELECT 1 FROM performance_schema.replication_group_members WHERE MEMBER_STATE='ONLINE' AND MEMBER_ROLE='SECONDARY' AND MEMBER_HOST=?"
	err := db.QueryRowx(query, host).Scan(&value)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return value, err
}

// BootstrapGroupReplication bootstraps group replication
func BootstrapGroupReplication(db *sqlx.DB, myver *version.Version) (string, error) {
	cmd := "SET GLOBAL group_replication_bootstrap_group = ON"
	_, err := db.Exec(cmd)
	if err != nil {
		return cmd, err
	}
	cmd2, err := StartGroupReplication(db, myver)
	if err != nil {
		return cmd + ";" + cmd2, err
	}
	cmd3 := "SET GLOBAL group_replication_bootstrap_group = OFF"
	_, err = db.Exec(cmd3)
	return cmd + ";" + cmd2 + ";" + cmd3, err
}

// GetSpiderShardUrl gets spider shard URLs
func GetSpiderShardUrl(db *sqlx.DB) (string, error) {
	var value string
	query := "SELECT COALESCE(GROUP_CONCAT(DISTINCT CONCAT(COALESCE(st.host,s.host),':',COALESCE(st.port,s.port))),'') as value FROM mysql.spider_tables st LEFT JOIN mysql.servers s ON st.server=s.server_name"
	err := db.QueryRowx(query).Scan(&value)
	return value, err
}

// GetSpiderMonitor gets spider monitor URLs
func GetSpiderMonitor(db *sqlx.DB) (string, error) {
	var value string
	query := "SELECT COALESCE(GROUP_CONCAT(DISTINCT CONCAT(COALESCE(st.host,s.host),':',COALESCE(st.port,s.port))),'') as value FROM mysql.spider_link_mon_servers st LEFT JOIN mysql.servers s ON st.server=s.server_name"
	err := db.QueryRowx(query).Scan(&value)
	return value, err
}

// DuplicateUserPassword duplicates user password to new user
// TODO: Grant text manipulation still has risks - consider alternative approach
func DuplicateUserPassword(db *sqlx.DB, myver *version.Version, old_user_name string, user_host string, new_user_name string) (string, error) {
	// Validate identifiers
	if err := ValidateIdentifier(old_user_name); err != nil {
		return "", fmt.Errorf("invalid old username: %w", err)
	}
	if err := ValidateIdentifier(user_host); err != nil {
		return "", fmt.Errorf("invalid host: %w", err)
	}
	if err := ValidateIdentifier(new_user_name); err != nil {
		return "", fmt.Errorf("invalid new username: %w", err)
	}

	query := fmt.Sprintf("SHOW GRANTS FOR %s@%s",
		QuoteMySQLIdentifier(old_user_name),
		QuoteMySQLIdentifier(user_host))
	rows, err := db.Queryx(query)
	if err != nil {
		return query, errors.New("Could not get grants for user")
	}
	defer rows.Close()

	for rows.Next() {
		var grant string
		err = rows.Scan(&grant)
		if err != nil {
			return query, err
		}
		querygrant := strings.Replace(grant, old_user_name, new_user_name, 1)
		query += ";" + querygrant
		_, err = db.Exec(querygrant)
		if err != nil {
			return query, err
		}
	}
	return query, nil
}

// StartGroupReplication starts group replication
func StartGroupReplication(db *sqlx.DB, myver *version.Version) (string, error) {
	cmd := "START GROUP_REPLICATION"
	_, err := db.Exec(cmd)
	return cmd, err
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

func FetchLogsToBufferTable(conn *sqlx.Conn, version *version.Version, table string, timeStampString string, timeout time.Duration) error {
	desttablename := table + "_" + timeStampString
	copytablename := table + "_buffer"
	query := "USE replication_manager_schema"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set database as replication_manager_schema: %s", err.Error())
	}
	cancel()

	if version.IsMariaDB() {
		query = "SET SESSION sql_log_bin=0"
	} else {
		query = "SET sql_log_bin = OFF"
	}
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set sql_log_bin session to 0: %s", err.Error())
	}
	cancel()

	query = "DROP TABLE IF EXISTS `replication_manager_schema`." + copytablename
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create drop replication_manager_schema.%s: %s", copytablename, err.Error())
	}
	cancel()

	query = "CREATE TABLE IF NOT EXISTS `replication_manager_schema`." + copytablename + " LIKE `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create table replication_manager_schema.%s: %s", copytablename, err.Error())
	}
	cancel()

	query = "CREATE TABLE IF NOT EXISTS `replication_manager_schema`." + desttablename + " LIKE `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create table replication_manager_schema.%s: %s", desttablename, err.Error())
	}
	cancel()

	query = "TRUNCATE TABLE `replication_manager_schema`." + copytablename
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to truncate table replication_manager_schema.%s: %s", copytablename, err.Error())
	}
	cancel()

	query = "INSERT INTO `replication_manager_schema`." + copytablename + " SELECT * FROM `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create table replication_manager_schema.%s: %s", desttablename, err.Error())
	}
	cancel()

	return nil
}

func TruncateLogsTable(conn *sqlx.Conn, version *version.Version, table string, timeout time.Duration) error {
	query := "USE mysql"

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set database as replication_manager_schema: %s", err.Error())
	}
	cancel()

	if version.IsMariaDB() {
		query = "SET SESSION sql_log_bin=0"
	} else {
		query = "SET sql_log_bin = OFF"
	}
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set sql_log_bin session to 0: %s", err.Error())
	}
	cancel()

	query = "TRUNCATE TABLE `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to truncate table mysql.%s: %s", table, err.Error())
	}
	cancel()

	return nil
}

func MoveLogsToDailyTable(conn *sqlx.Conn, version *version.Version, table string, timeStampString string, timeout time.Duration) error {
	desttablename := table + "_" + timeStampString
	copytablename := table + "_buffer"

	query := "USE replication_manager_schema"
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set database as replication_manager_schema: %s", err.Error())
	}
	cancel()

	if version.IsMariaDB() {
		query = "SET SESSION sql_log_bin=0"
	} else {
		query = "SET sql_log_bin = OFF"
	}
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set sql_log_bin session to 0: %s", err.Error())
	}
	cancel()

	query = "CREATE TABLE IF NOT EXISTS `replication_manager_schema`." + desttablename + " LIKE `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create table replication_manager_schema.%s: %s", desttablename, err.Error())
	}
	cancel()

	query = "CREATE TABLE IF NOT EXISTS `replication_manager_schema`." + copytablename + " LIKE `mysql`." + table
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to create table replication_manager_schema.%s: %s", copytablename, err.Error())
	}
	cancel()

	query = "INSERT INTO `replication_manager_schema`." + desttablename + " SELECT * FROM `replication_manager_schema`." + copytablename
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set sql_log_bin session to 0: %s", err.Error())
	}
	cancel()

	query = "DROP TABLE IF EXISTS `replication_manager_schema`." + copytablename
	ctx, cancel = context.WithTimeout(context.Background(), timeout)
	if _, err := conn.ExecContext(ctx, query); err != nil {
		cancel()
		return fmt.Errorf("Unable to set sql_log_bin session to 0: %s", err.Error())
	}
	cancel()
	return nil
}

func GetBackupSizeEstimation(db *sqlx.DB, version *version.Version) (uint64, error) {
	var size uint64
	query := "SELECT SUM(data_length + index_length) AS total_size FROM information_schema.tables"
	if version.IsPostgreSQL() {
		return size, fmt.Errorf("ERROR: Backup estimation not available on PostgeSQL")
	}

	err := db.Get(&size, query)
	if err != nil {
		return size, errors.New("Could not get size: " + err.Error())
	}

	return size, nil
}

// SetEventScheduler enables or disables the event scheduler
func SetEventScheduler(db *sqlx.DB, state bool, myver *version.Version) (string, error) {
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

// SetGroupReplicationPrimary sets the current server as the group replication primary
func SetGroupReplicationPrimary(db *sqlx.DB, myver *version.Version) (string, error) {
	var value string
	uuid := ""
	err := db.QueryRowx("SELECT @@server_uuid").Scan(&uuid)
	if err != nil {
		return "", err
	}

	// Use parameterized query to prevent SQL injection
	query := "SELECT group_replication_set_as_primary(?)"
	err = db.QueryRowx(query, uuid).Scan(&value)
	if err != nil {
		log.Println("ERROR: Could not set Group Replication Primary", err)
	}
	return value, nil
}

// CreateUser creates a new database user
// TODO: Contains SQL injection risks - needs refactoring to use CREATE USER with parameters
func CreateUser(db *sqlx.DB, myver *version.Version, user_host string, user_name string, new_password string) (string, error) {
	// Validate identifiers
	if err := ValidateIdentifier(user_name); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}
	if err := ValidateIdentifier(user_host); err != nil {
		return "", fmt.Errorf("invalid host: %w", err)
	}

	// Note: CREATE USER cannot use parameterized password in all MySQL versions
	// Using quoted identifiers for user/host and escaping password
	query := fmt.Sprintf("CREATE USER %s@%s IDENTIFIED BY ?",
		QuoteMySQLIdentifier(user_name),
		QuoteMySQLIdentifier(user_host))

	_, err := db.Exec(query, new_password)
	return query, err
}

// RevokeUserGrants revokes all privileges from a user
// TODO: Contains SQL injection risks - needs refactoring
func RevokeUserGrants(ctx context.Context, conn *sqlx.Conn, myver *version.Version, user_host string, user_name string) (string, error) {
	// Validate identifiers
	if err := ValidateIdentifier(user_name); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}
	if err := ValidateIdentifier(user_host); err != nil {
		return "", fmt.Errorf("invalid host: %w", err)
	}

	query := fmt.Sprintf("REVOKE ALL PRIVILEGES, GRANT OPTION ON *.* FROM %s@%s",
		QuoteMySQLIdentifier(user_name),
		QuoteMySQLIdentifier(user_host))

	_, err := conn.ExecContext(ctx, query)
	return query, err
}

// SetUserGrantsWithGrantOption grants privileges to a user with grant option
// TODO: Contains SQL injection risks - grant text cannot be safely parameterized
func SetUserGrantsWithGrantOption(ctx context.Context, conn *sqlx.Conn, myver *version.Version, user_host string, user_name string, grants ...string) (string, error) {
	// Validate identifiers
	if err := ValidateIdentifier(user_name); err != nil {
		return "", fmt.Errorf("invalid username: %w", err)
	}
	if err := ValidateIdentifier(user_host); err != nil {
		return "", fmt.Errorf("invalid host: %w", err)
	}

	var query string
	for _, grant := range grants {
		// Note: Grant privileges cannot be parameterized, but user/host are quoted
		query = fmt.Sprintf("GRANT %s TO %s@%s WITH GRANT OPTION",
			grant,
			QuoteMySQLIdentifier(user_name),
			QuoteMySQLIdentifier(user_host))

		_, err := conn.ExecContext(ctx, query)
		if err != nil {
			return query, err
		}
	}
	return query, nil
}
