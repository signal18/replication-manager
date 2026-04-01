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

type schemaExecutor interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryxContext(ctx context.Context, query string, args ...any) (*sqlx.Rows, error)
}

// Short timeout to avoid long metadata lock waits during scans.
const defaultSchemaScanTimeout = 5 * time.Second

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
func GetTableChecksumResult(db *sqlx.DB) (map[uint64]Chunk, string, error) {
	vars := make(map[uint64]Chunk)
	query := "SELECT /*replication-manager*/ chunkId , chunkRangeCondition, chunkChecksum FROM replication_manager_schema.table_checksum"
	rows, err := db.Queryx(query)
	if err != nil {
		return vars, query, err
	}
	defer rows.Close()
	for rows.Next() {
		var v Chunk
		err = rows.Scan(&v.ChunkId, &v.ChunkRangeCondition, &v.ChunkCheckSum)
		if err != nil {
			return vars, query, err
		}
		vars[v.ChunkId] = v
	}
	return vars, query, nil
}

// GetTables retrieves all tables from all schemas (excluding system schemas)
// timeout is specified in seconds. If <= 0, defaultSchemaScanTimeout is used.
func GetTables(db *sqlx.DB, myver *version.Version, getColumns, getIndexes bool, timeoutSeconds int) (map[string]*Table, []Table, string, error) {
	var logBuilder strings.Builder

	crc64Table := crc64.MakeTable(0xC96C5795D7870F42)

	// Convert timeout from seconds to time.Duration, fallback to default if invalid
	timeout := defaultSchemaScanTimeout
	if timeoutSeconds > 0 {
		timeout = time.Duration(timeoutSeconds) * time.Second
	}

	connCtx, connCancel := scanContext(timeout)
	conn, err := db.Connx(connCtx)
	connCancel()
	if err != nil {
		return nil, nil, logBuilder.String(), err
	}
	defer conn.Close()

	// Disable information_schema stats expiry to reduce stale size data.
	appendLog(&logBuilder, applyInformationSchemaStatsExpiry(conn, myver, timeout))

	// Bulk information_schema scans reduce round trips and metadata lock pressure.
	tables, qlog, err := getAllTables(conn, myver, timeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		return nil, nil, logBuilder.String(), err
	}

	tablemap := make(map[string]*Table, len(tables))
	for i := range tables {
		t := &tables[i]
		key := t.TableSchema + "." + t.TableName
		tablemap[key] = t
	}

	qlog, err = loadAllColumns(conn, myver, tablemap, timeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		return tablemap, tables, logBuilder.String(), err
	}

	qlog, err = loadAllIndexes(conn, myver, tablemap, timeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		return tablemap, tables, logBuilder.String(), err
	}

	for i := range tables {
		t := &tables[i]
		t.HashColumns(crc64Table)
		t.HashIndexes(crc64Table)
		t.HashTableCrc(crc64Table)
	}

	// ── Graph links ──────────────────────────────────────────────────────
	// Runs after hashing so TableIndexMap is fully built on every table.
	// loadFKLinks issues one SQL query; loadColumnMatchLinks works in-memory from the loaded tablemap.

	qlog, err = loadFKLinks(conn, myver, tablemap, timeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		// Non-fatal: log and continue; graph edges will be absent.
		logBuilder.WriteString(fmt.Sprintf(" -- loadFKLinks error: %v", err))
	}
	qlog, err = loadColumnMatchLinks(conn, myver, tablemap, timeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		logBuilder.WriteString(fmt.Sprintf(" -- loadColumnMatchLinks error: %v", err))
	}
	computeSizeWeights(tables)
	// ── end graph links

	if !getColumns {
		for i := range tables {
			t := &tables[i]
			t.TableColumns = nil
			t.TableColumnMap = nil
			t.TableColumnsCrc64 = 0
		}
	}

	if !getIndexes {
		for i := range tables {
			t := &tables[i]
			t.TableIndexes = nil
			t.TableIndexMap = nil
			t.TableIndexesCrc64 = 0
		}
	}

	return tablemap, tables, logBuilder.String(), nil
}

func appendLog(builder *strings.Builder, query string) {
	if query == "" {
		return
	}
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString(query)
}

func scanContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.Background(), func() {}
	}
	return context.WithTimeout(context.Background(), timeout)
}

func applyInformationSchemaStatsExpiry(ext schemaExecutor, myver *version.Version, timeout time.Duration) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	if myver.IsMariaDB() {
		return ""
	}

	query := "SET SESSION information_schema_stats_expiry = 0"
	ctx, cancel := scanContext(timeout)
	defer cancel()
	if _, err := ext.ExecContext(ctx, query); err != nil {
		log.Printf("schema scan: %s failed: %v", query, err)
		return fmt.Sprintf("%s -- failed: %v", query, err)
	}
	return query
}

func getAllTables(ext schemaExecutor, myver *version.Version, timeout time.Duration) ([]Table, string, error) {
	query := tablesQueryAll(myver)
	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := ext.QueryxContext(ctx, query)
	if err != nil {
		return nil, query, errors.New("could not get table list: " + err.Error())
	}
	defer rows.Close()

	var tables []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(
			&t.TableSchema,
			&t.TableName,
			&t.Engine,
			&t.TableType,
			&t.RowFormat,
			&t.TableCollation,
			&t.CreateOptions,
			&t.TableComment,
			&t.AutoIncrement,
			&t.TableRows,
			&t.DataLength,
			&t.IndexLength,
		); err != nil {
			return nil, query, err
		}
		tables = append(tables, t)
	}
	if err := rows.Err(); err != nil {
		return nil, query, fmt.Errorf("error iterating table rows: %w", err)
	}

	return tables, query, nil
}

func tablesQueryAll(myver *version.Version) string {
	if myver.IsPostgreSQL() {
		return `SELECT table_schema, table_name, 'BASE TABLE' AS engine, table_type,
			'' AS row_format, '' AS table_collation, '' AS create_options, '' AS table_comment,
			0::bigint AS auto_increment, 0::bigint AS table_rows, 0::bigint AS data_length,
			0::bigint AS index_length
			FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
			ORDER BY table_schema, table_name`
	}
	return `SELECT table_schema, table_name, engine, table_type,
		COALESCE(row_format, ''), COALESCE(table_collation, ''), COALESCE(create_options, ''),
		COALESCE(table_comment, ''), COALESCE(auto_increment, 0),
		table_rows, data_length, index_length
		FROM information_schema.TABLES
		WHERE table_schema NOT IN ('information_schema','mysql','performance_schema','sys')
		AND table_schema NOT LIKE '#%' AND table_type = 'BASE TABLE'
		ORDER BY table_schema, table_name`
}

func loadAllColumns(ext schemaExecutor, myver *version.Version, tablemap map[string]*Table, timeout time.Duration) (string, error) {
	query := columnDefQueryAll(myver)
	if query == "" {
		return "", nil
	}

	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := ext.QueryxContext(ctx, query)
	if err != nil {
		return query, err
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
			return query, err
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
	if err := rows.Err(); err != nil {
		return query, fmt.Errorf("error iterating column rows: %w", err)
	}

	return query, nil
}

func columnDefQueryAll(myver *version.Version) string {
	if myver.IsPostgreSQL() {
		return `SELECT table_schema, table_name, ordinal_position, column_name, udt_name AS column_type,
			is_nullable, column_default, '' AS extra, '' AS character_set_name, '' AS collation_name
			FROM information_schema.columns WHERE table_schema = 'public'
			ORDER BY table_schema, table_name, ordinal_position`
	}
	return `SELECT table_schema, table_name, ordinal_position, column_name, column_type,
		is_nullable, column_default, extra, character_set_name, collation_name
		FROM information_schema.COLUMNS
		WHERE table_schema NOT IN ('information_schema','mysql','performance_schema','sys')
		AND table_schema NOT LIKE '#%'
		ORDER BY table_schema, table_name, ordinal_position`
}

func loadAllIndexes(ext schemaExecutor, myver *version.Version, tablemap map[string]*Table, timeout time.Duration) (string, error) {
	query := indexDefQueryAll(myver)
	if query == "" {
		return "", nil
	}

	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := ext.QueryxContext(ctx, query)
	if err != nil {
		return query, err
	}
	defer rows.Close()

	// Per-table index name map avoids O(n^2) scans when assembling indexes.
	indexPositions := make(map[string]map[string]int)

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
			return query, err
		}

		key := r.TableSchema + "." + r.TableName
		t, ok := tablemap[key]
		if !ok {
			continue
		}

		idxMap, ok := indexPositions[key]
		if !ok {
			idxMap = make(map[string]int)
			indexPositions[key] = idxMap
		}

		idxPos, ok := idxMap[r.IndexName]
		if !ok {
			t.TableIndexes = append(t.TableIndexes, Index{
				Name:   r.IndexName,
				Unique: r.NonUnique == 0,
				Type:   strings.ToUpper(r.IndexType),
			})
			idxPos = len(t.TableIndexes) - 1
			idxMap[r.IndexName] = idxPos
		}

		idx := &t.TableIndexes[idxPos]
		col := IndexColumn{Name: r.ColumnName}
		if r.SubPart.Valid {
			p := uint16(r.SubPart.Int64)
			col.Prefix = &p
		}
		idx.Columns = append(idx.Columns, col)
	}
	if err := rows.Err(); err != nil {
		return query, fmt.Errorf("error iterating index rows: %w", err)
	}

	return query, nil
}

func indexDefQueryAll(myver *version.Version) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	return `SELECT table_schema, table_name, index_name, non_unique, index_type, seq_in_index,
		column_name, sub_part
		FROM information_schema.STATISTICS
		WHERE table_schema NOT IN ('information_schema','mysql','performance_schema','sys')
		AND table_schema NOT LIKE '#%'
		ORDER BY table_schema, table_name, index_name, seq_in_index`
}

func ddlQuery(myver *version.Version, schema, table string) string {
	if myver.IsPostgreSQL() {
		return ""
	}
	return fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`", schema, table)
}

// LoadDDLForDiff returns the raw DDL for optional debug/diff paths.
func LoadDDLForDiff(ctx context.Context, db *sqlx.DB, myver *version.Version, schema, table string) (string, string, error) {
	query := ddlQuery(myver, schema, table)
	if query == "" {
		return "", "", nil
	}

	var tbl string
	var ddl string
	if err := db.QueryRowxContext(ctx, query).Scan(&tbl, &ddl); err != nil {
		return "", query, err
	}

	return ddl, query, nil
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
		columnsTrimmed := strings.TrimSpace(columns)
		indexesTrimmed := strings.TrimSpace(indexes)
		if strings.EqualFold(columnsTrimmed, "ALL") {
			query += " PERSISTENT FOR ALL"
		} else {
			// MariaDB 10.4+ requires both COLUMNS and INDEXES for named PERSISTENT FOR.
			if columnsTrimmed == "" && indexesTrimmed == "" {
				return "", errors.New("persistent requires columns and indexes")
			}
			if columnsTrimmed == "" || indexesTrimmed == "" {
				return "", errors.New("persistent requires both columns and indexes")
			}
			if err := validateIdentifierList(columnsTrimmed, true, "column"); err != nil {
				return "", err
			}
			if err := validateIdentifierList(indexesTrimmed, false, "index"); err != nil {
				return "", err
			}
			quotedColumns, err := quoteIdentifierList(columnsTrimmed)
			if err != nil {
				return "", err
			}
			quotedIndexes, err := quoteIdentifierList(indexesTrimmed)
			if err != nil {
				return "", err
			}
			query += " PERSISTENT FOR COLUMNS (" + quotedColumns + ") INDEXES (" + quotedIndexes + ")"
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

type MaxPKValue struct {
	Type string
	Max  string
}

func CheckPrimaryKeyMaxValues(conn *sqlx.DB, schema, table string, pks []string) (map[string]MaxPKValue, error) {
	results := make(map[string]MaxPKValue)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	maxPKquery := "SELECT "
	maxPKValues := make([]interface{}, 0)
	for _, p := range pks {
		maxPKquery = maxPKquery + "MAX(" + p + ") as '" + p + "',"
		maxPKValues = append(maxPKValues, new(interface{}))
	}
	maxPKquery = strings.TrimSuffix(maxPKquery, ",") + " FROM " + schema + "." + table
	err := conn.QueryRowxContext(ctx, maxPKquery).Scan(maxPKValues...)
	if err != nil {
		return nil, err
	}

	for i, v := range maxPKValues {
		// v is pointer to the value, so we need to dereference it
		val := *(v.(*interface{}))
		p := pks[i]

		var maxValue MaxPKValue
		maxValue.Max = fmt.Sprintf("%v", v)
		switch val.(type) {
		case int64, uint64:
			maxValue.Type = "int"
		case string:
			maxValue.Type = "string"
		default:
			return nil, fmt.Errorf("unsupported primary key type for column %s", p)
		}
		results[p] = maxValue
	}

	return results, nil
}

func CreateChunkTable(conn *sqlx.DB, schema, table string, pks []string, chunkSize int) (string, error) {
	pkQuery := []string{}

	query := "CREATE TEMPORARY TABLE replication_manager_schema.table_chunk ENGINE=MYISAM " +
		"SELECT FLOOR((@rows:=@rows+1/" + fmt.Sprintf("%d", chunkSize) + ")) as chunkId, "

	if len(pks) == 0 {
		return "", fmt.Errorf("table %s.%s has no primary key, cannot create chunk table", schema, table)
	} else if len(pks) == 1 {
		// Single PK can be used directly without needing to check max values or apply padding
		pkQuery = append(pkQuery, pks[0])
	} else {
		MaxPKValue, err := CheckPrimaryKeyMaxValues(conn, schema, table, pks)
		if err != nil {
			return "", fmt.Errorf("error checking primary key max values: %w", err)
		}

		for p, maxVal := range MaxPKValue {
			switch maxVal.Type {
			case "int", "uint":
				pkQuery = append(pkQuery, fmt.Sprintf("lpad(%s,%d)", p, len(maxVal.Max)))
			case "string":
				pkQuery = append(pkQuery, p)
			}
		}
	}

	query = query +
		"MIN(CONCAT_WS('/*;*/'," + strings.Join(pkQuery, ",") + ")) as chunkMinKey, MAX(CONCAT_WS('/*;*/'," + strings.Join(pkQuery, ",") + ")) as chunkMaxKey from " + schema + "." + table + " , (SELECT @rows:=0 FROM DUAL) A group by chunkId"

	_, err := conn.Exec(query)
	if err != nil {
		return query, fmt.Errorf("error creating chunk table: %w", err)
	}

	_, err = conn.Exec(`ALTER TABLE replication_manager_schema.table_chunk ADD PRIMARY KEY (chunkId)`)

	return query, err
}

type fkLinkRow struct {
	ChildSchema    string
	ChildTable     string
	ParentSchema   string
	ParentTable    string
	ChildCols      string // comma-separated, GROUP_CONCAT ordered by ORDINAL_POSITION
	ParentCols     string
	ConstraintName string
	FKColCount     int
	ChildPKCols    int
	ChildFKCount   int
	ChildExtraCols int
}

// fkLinksQuery returns the SQL that fetches every explicit FK in one round-trip.
// Mirrors the schema exclusion list used by tablesQueryAll().
const fkLinksQueryMySQL = `
SELECT
    fk.TABLE_SCHEMA,
    fk.TABLE_NAME,
    fk.REFERENCED_TABLE_SCHEMA,
    fk.REFERENCED_TABLE_NAME,
    GROUP_CONCAT(fk.COLUMN_NAME
                 ORDER BY fk.ORDINAL_POSITION SEPARATOR ',') AS child_cols,
    GROUP_CONCAT(fk.REFERENCED_COLUMN_NAME
                 ORDER BY fk.ORDINAL_POSITION SEPARATOR ',') AS parent_cols,
    fk.CONSTRAINT_NAME,
    COUNT(fk.COLUMN_NAME) AS fk_col_count,

    /* child PK column count */
    (SELECT COUNT(*)
     FROM information_schema.KEY_COLUMN_USAGE kc2
     JOIN information_schema.TABLE_CONSTRAINTS tc2
       ON  tc2.CONSTRAINT_NAME = kc2.CONSTRAINT_NAME
       AND tc2.TABLE_SCHEMA    = kc2.TABLE_SCHEMA
       AND tc2.TABLE_NAME      = kc2.TABLE_NAME
     WHERE tc2.CONSTRAINT_TYPE = 'PRIMARY KEY'
       AND kc2.TABLE_SCHEMA    = fk.TABLE_SCHEMA
       AND kc2.TABLE_NAME      = fk.TABLE_NAME
    ) AS child_pk_cols,

    /* total FK constraints on child (N-N junction detection) */
    (SELECT COUNT(DISTINCT fk2.CONSTRAINT_NAME)
     FROM information_schema.KEY_COLUMN_USAGE fk2
     JOIN information_schema.TABLE_CONSTRAINTS tc3
       ON  tc3.CONSTRAINT_NAME = fk2.CONSTRAINT_NAME
       AND tc3.TABLE_SCHEMA    = fk2.TABLE_SCHEMA
       AND tc3.TABLE_NAME      = fk2.TABLE_NAME
     WHERE tc3.CONSTRAINT_TYPE       = 'FOREIGN KEY'
       AND fk2.REFERENCED_TABLE_NAME IS NOT NULL
       AND fk2.TABLE_SCHEMA          = fk.TABLE_SCHEMA
       AND fk2.TABLE_NAME            = fk.TABLE_NAME
    ) AS child_fk_count,

    /* non-PK, non-FK payload columns (0 → pure junction table) */
    (SELECT COUNT(c.COLUMN_NAME)
     FROM information_schema.COLUMNS c
     WHERE c.TABLE_SCHEMA = fk.TABLE_SCHEMA
       AND c.TABLE_NAME   = fk.TABLE_NAME
       AND c.COLUMN_NAME NOT IN (
           SELECT kc3.COLUMN_NAME
           FROM   information_schema.KEY_COLUMN_USAGE kc3
           JOIN   information_schema.TABLE_CONSTRAINTS tc4
             ON   tc4.CONSTRAINT_NAME = kc3.CONSTRAINT_NAME
             AND  tc4.TABLE_SCHEMA    = kc3.TABLE_SCHEMA
             AND  tc4.TABLE_NAME      = kc3.TABLE_NAME
           WHERE  tc4.CONSTRAINT_TYPE IN ('PRIMARY KEY','FOREIGN KEY')
             AND  kc3.TABLE_SCHEMA = fk.TABLE_SCHEMA
             AND  kc3.TABLE_NAME   = fk.TABLE_NAME
       )
    ) AS child_extra_cols

FROM information_schema.KEY_COLUMN_USAGE  fk
JOIN information_schema.TABLE_CONSTRAINTS tc
  ON  tc.CONSTRAINT_NAME = fk.CONSTRAINT_NAME
  AND tc.TABLE_SCHEMA    = fk.TABLE_SCHEMA
  AND tc.TABLE_NAME      = fk.TABLE_NAME
WHERE tc.CONSTRAINT_TYPE          = 'FOREIGN KEY'
  AND fk.REFERENCED_TABLE_NAME   IS NOT NULL
  AND fk.TABLE_SCHEMA NOT IN ('information_schema','mysql','performance_schema','sys')
  AND fk.TABLE_SCHEMA NOT LIKE '#%'
GROUP BY
    fk.TABLE_SCHEMA, fk.TABLE_NAME,
    fk.REFERENCED_TABLE_SCHEMA, fk.REFERENCED_TABLE_NAME,
    fk.CONSTRAINT_NAME
ORDER BY fk.TABLE_SCHEMA, fk.TABLE_NAME, fk.CONSTRAINT_NAME
`

// loadFKLinks queries information_schema for explicit FK constraints and
// attaches TableLink entries to both sides of every FK pair found in tablemap.
// Tables outside tablemap (different schema, filtered out) are skipped silently.
// Returns the query string for the log builder, matching the existing convention.
func loadFKLinks(ext schemaExecutor, myver *version.Version, tablemap map[string]*Table, timeout time.Duration) (string, error) {
	if myver.IsPostgreSQL() {
		return "", nil // PostgreSQL FK discovery is a separate concern
	}

	query := fkLinksQueryMySQL
	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := ext.QueryxContext(ctx, query)
	if err != nil {
		return query, fmt.Errorf("loadFKLinks: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r fkLinkRow
		if err := rows.Scan(
			&r.ChildSchema, &r.ChildTable,
			&r.ParentSchema, &r.ParentTable,
			&r.ChildCols, &r.ParentCols,
			&r.ConstraintName,
			&r.FKColCount, &r.ChildPKCols, &r.ChildFKCount, &r.ChildExtraCols,
		); err != nil {
			return query, fmt.Errorf("loadFKLinks scan: %w", err)
		}

		card := fkCardinality(r.FKColCount, r.ChildPKCols, r.ChildFKCount, r.ChildExtraCols)
		childCols := splitCSV(r.ChildCols)
		parentCols := splitCSV(r.ParentCols)

		childKey := r.ChildSchema + "." + r.ChildTable
		parentKey := r.ParentSchema + "." + r.ParentTable

		// Attach to child table: this table references the parent.
		if child, ok := tablemap[childKey]; ok {
			child.TableParents = append(child.TableParents, TableLink{
				LinkedSchema:  r.ParentSchema,
				LinkedTable:   r.ParentTable,
				LocalColumns:  childCols,
				RemoteColumns: parentCols,
				RelationName:  r.ConstraintName,
				Source:        RelationForeignKey,
				Cardinality:   card,
			})
		}

		// Attach to parent table: it is referenced by the child.
		if parent, ok := tablemap[parentKey]; ok {
			parent.TableChildren = append(parent.TableChildren, TableLink{
				LinkedSchema:  r.ChildSchema,
				LinkedTable:   r.ChildTable,
				LocalColumns:  parentCols,
				RemoteColumns: childCols,
				RelationName:  r.ConstraintName,
				Source:        RelationForeignKey,
				Cardinality:   card,
			})
		}
	}
	return query, rows.Err()
}

// colMatchLinksQuery detects implicit FK-like relationships from shared PRIMARY
// KEY column names — zero application-side filtering needed.
//
// Rules enforced in SQL:
//  1. The shared columns form the complete PRIMARY KEY of the parent table.
//  2. The same columns appear at the same ordinal positions as the leading
//     prefix of the child table's PRIMARY KEY (parent PK ⊆ child PK prefix).
//  3. No explicit FOREIGN KEY already exists between the pair.
//
// Result columns: child_schema, child_table, parent_schema, parent_table,
//
//	shared_cols (csv), child_pk_cols (int), child_fk_count (int), child_extra_cols (int)
//
// loadColumnMatchLinks detects implicit FK-like relationships from the
// already-loaded tablemap — zero additional SQL queries.
//
// A link is emitted when ALL of the following hold:
//  1. Two different tables in the same schema share at least one PK column name.
//  2. The shared column(s) form the complete PRIMARY KEY of the parent.
//  3. The parent PK is a strict prefix of the child PK (child PK is wider).
//     Equal-width PKs mean shards of the same logical table — skipped.
//  4. No explicit FK already exists between the pair.
func loadColumnMatchLinks(_ schemaExecutor, _ *version.Version, tablemap map[string]*Table, _ time.Duration) (string, error) {
	type tableKey struct{ schema, table string }

	// ── Pass 1: PK-prefix structural match ───────────────────────────────────
	// A link is emitted when the parent's full PRIMARY KEY appears as a strict
	// leading prefix of the child's PRIMARY KEY (column names and ordinal
	// positions must match exactly). Equal-width PKs = shards, skipped.

	type pkEntry struct {
		tk      tableKey
		pkWidth int
		pkCols  []string
	}
	pkIndex := make(map[string][]pkEntry) // schema\x00col\x00pos → tables

	for _, t := range tablemap {
		var pkCols []string
		for _, idx := range t.TableIndexes {
			if idx.Name == "PRIMARY" {
				pkCols = make([]string, len(idx.Columns))
				for i, c := range idx.Columns {
					pkCols[i] = c.Name
				}
				break
			}
		}
		if len(pkCols) == 0 {
			continue
		}
		tk := tableKey{t.TableSchema, t.TableName}
		for pos, col := range pkCols {
			k := t.TableSchema + "\x00" + col + "\x00" + string(rune(pos))
			pkIndex[k] = append(pkIndex[k], pkEntry{tk, len(pkCols), pkCols})
		}
	}

	// linkedPairs tracks all emitted links (both passes) to prevent duplicates.
	linkedPairs := make(map[string]bool)

	for _, child := range tablemap {
		var childPK []string
		for _, idx := range child.TableIndexes {
			if idx.Name == "PRIMARY" {
				childPK = make([]string, len(idx.Columns))
				for i, c := range idx.Columns {
					childPK[i] = c.Name
				}
				break
			}
		}
		if len(childPK) == 0 {
			continue
		}

		explicitParents := make(map[string]bool, len(child.TableParents))
		for _, lk := range child.TableParents {
			explicitParents[lk.LinkedSchema+"."+lk.LinkedTable] = true
		}

		indexedCols := make(map[string]bool)
		for _, idx := range child.TableIndexes {
			for _, c := range idx.Columns {
				indexedCols[c.Name] = true
			}
		}
		extraCols := 0
		for _, c := range child.TableColumns {
			if !indexedCols[c.Name] {
				extraCols++
			}
		}
		childFKCount := len(child.TableParents)

		for pos, col := range childPK {
			k := child.TableSchema + "\x00" + col + "\x00" + string(rune(pos))
			for _, pe := range pkIndex[k] {
				if pe.tk.table == child.TableName {
					continue
				}
				parentKey := pe.tk.schema + "." + pe.tk.table
				pairKey := child.TableSchema + "\x00" + child.TableName + "\x00" + parentKey
				if explicitParents[parentKey] || linkedPairs[pairKey] {
					continue
				}
				if pe.pkWidth >= len(childPK) {
					continue // same-width PK = shard
				}
				if !isPKPrefix(pe.pkCols, childPK) {
					continue
				}
				parent, ok := tablemap[parentKey]
				if !ok {
					continue
				}
				card := fkCardinality(pe.pkWidth, len(childPK), childFKCount, extraCols)
				relName := "implicit_pk_" + child.TableName + "_" + parent.TableName
				child.TableParents = append(child.TableParents, TableLink{
					LinkedSchema:  parent.TableSchema,
					LinkedTable:   parent.TableName,
					LocalColumns:  pe.pkCols,
					RemoteColumns: pe.pkCols,
					RelationName:  relName,
					Source:        RelationColumnNameMatch,
					Cardinality:   card,
				})
				parent.TableChildren = append(parent.TableChildren, TableLink{
					LinkedSchema:  child.TableSchema,
					LinkedTable:   child.TableName,
					LocalColumns:  pe.pkCols,
					RemoteColumns: pe.pkCols,
					RelationName:  relName,
					Source:        RelationColumnNameMatch,
					Cardinality:   card,
				})
				linkedPairs[pairKey] = true
				childFKCount++
			}
		}
	}

	// ── Pass 2: table-name-in-column-name inference ───────────────────────────
	// A column named {table}, {table}_id, id_{table}, {table}_fk, fk_{table}
	// — where {table} is the name of another table in the same schema — is
	// treated as an implicit FK to that table's PRIMARY KEY.
	// Also handles naive plurals: clients_id → client.
	//
	// Patterns (c = column name lowercased, t = parent table name):
	//   c == t                 exact:   client     → client
	//   c == t + "_id"                  client_id  → client
	//   c == "id_" + t                  id_client  → client
	//   c == t + "_fk"                  client_fk  → client
	//   c == "fk_" + t                  fk_client  → client
	//   above with t = singular(table)  clients_id → client  (strip trailing s)

	type parentMeta struct {
		table  string
		schema string
		pkCols []string
	}
	nameIndex := make(map[string]parentMeta) // schema\x00normalizedName → meta

	for _, t := range tablemap {
		var pkCols []string
		for _, idx := range t.TableIndexes {
			if idx.Name == "PRIMARY" {
				pkCols = make([]string, len(idx.Columns))
				for i, c := range idx.Columns {
					pkCols[i] = c.Name
				}
				break
			}
		}
		if len(pkCols) == 0 {
			continue
		}
		base := strings.ToLower(t.TableName)
		pm := parentMeta{t.TableName, t.TableSchema, pkCols}
		nameIndex[t.TableSchema+"\x00"+base] = pm
		if strings.HasSuffix(base, "s") {
			sing := strings.TrimSuffix(base, "s")
			if _, exists := nameIndex[t.TableSchema+"\x00"+sing]; !exists {
				nameIndex[t.TableSchema+"\x00"+sing] = pm
			}
		}
	}

	columnRefersTo := func(schema, col string) (parentMeta, bool) {
		c := strings.ToLower(col)
		for _, cand := range []string{
			strings.TrimSuffix(strings.TrimSuffix(c, "_id"), "_fk"),
			strings.TrimPrefix(strings.TrimPrefix(c, "id_"), "fk_"),
			c,
		} {
			cand = strings.Trim(cand, "_")
			if cand == "" {
				continue
			}
			if pm, ok := nameIndex[schema+"\x00"+cand]; ok {
				return pm, true
			}
		}
		return parentMeta{}, false
	}

	for _, child := range tablemap {
		explicitParents := make(map[string]bool, len(child.TableParents))
		for _, lk := range child.TableParents {
			explicitParents[lk.LinkedSchema+"."+lk.LinkedTable] = true
		}

		indexedCols := make(map[string]bool)
		for _, idx := range child.TableIndexes {
			for _, c := range idx.Columns {
				indexedCols[c.Name] = true
			}
		}
		extraCols := 0
		for _, c := range child.TableColumns {
			if !indexedCols[c.Name] {
				extraCols++
			}
		}
		childFKCount := len(child.TableParents)

		var childPKCols int
		for _, idx := range child.TableIndexes {
			if idx.Name == "PRIMARY" {
				childPKCols = len(idx.Columns)
				break
			}
		}

		for _, col := range child.TableColumns {
			pm, ok := columnRefersTo(child.TableSchema, col.Name)
			if !ok {
				continue
			}
			if pm.table == child.TableName {
				continue
			}
			parentKey := pm.schema + "." + pm.table
			pairKey := child.TableSchema + "\x00" + child.TableName + "\x00" + parentKey
			if explicitParents[parentKey] || linkedPairs[pairKey] {
				continue
			}
			parent, ok := tablemap[parentKey]
			if !ok {
				continue
			}
			card := fkCardinality(len(pm.pkCols), childPKCols, childFKCount, extraCols)
			relName := "implicit_col_" + child.TableName + "_" + parent.TableName
			child.TableParents = append(child.TableParents, TableLink{
				LinkedSchema:  parent.TableSchema,
				LinkedTable:   parent.TableName,
				LocalColumns:  []string{col.Name},
				RemoteColumns: pm.pkCols,
				RelationName:  relName,
				Source:        RelationColumnNameMatch,
				Cardinality:   card,
			})
			parent.TableChildren = append(parent.TableChildren, TableLink{
				LinkedSchema:  child.TableSchema,
				LinkedTable:   child.TableName,
				LocalColumns:  pm.pkCols,
				RemoteColumns: []string{col.Name},
				RelationName:  relName,
				Source:        RelationColumnNameMatch,
				Cardinality:   card,
			})
			linkedPairs[pairKey] = true
			childFKCount++
		}
	}

	return "", nil
}

func computeSizeWeights(tables []Table) {
	schemaTotal := make(map[string]int64, 4)
	for i := range tables {
		schemaTotal[tables[i].TableSchema] += tables[i].DataLength + tables[i].IndexLength
	}
	for i := range tables {
		total := schemaTotal[tables[i].TableSchema]
		if total > 0 {
			tables[i].SizeWeightPct = float64(tables[i].DataLength+tables[i].IndexLength) /
				float64(total) * 100.0
		}
	}
}

// ── Small private utilities ───────────────────────────────────────────────────

// fkCardinality applies the three-rule heuristic.
//
//	N-N : child has ≥2 FK constraints AND zero payload columns (junction table)
//	1-1 : every FK column is also part of the child's own PK
//	1-N : default
func fkCardinality(fkCols, childPKCols, childFKCount, childExtraCols int) Cardinality {
	if childFKCount >= 2 && childExtraCols == 0 {
		return CardinalityManyToMany
	}
	if fkCols > 0 && fkCols == childPKCols {
		return CardinalityOneToOne
	}
	return CardinalityOneToMany
}

// splitCSV splits a GROUP_CONCAT comma-separated list into a trimmed slice.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// primaryKeyColsFor returns the ordered column names of the PRIMARY KEY of t,
// or nil if t has no primary key.
func primaryKeyColsFor(t *Table) []string {
	for _, idx := range t.TableIndexes {
		if idx.Name == "PRIMARY" {
			out := make([]string, len(idx.Columns))
			for i, c := range idx.Columns {
				out[i] = c.Name
			}
			return out
		}
	}
	return nil
}

// isPKPrefix returns true when parentPK is a prefix of (or equal to) childPK.
// Both slices must be in key-column order.
//
// Examples:
//
//	parent={id}        child={id}           → true  (exact match, 1-1)
//	parent={id}        child={id,type_id}   → true  (parent PK is leading part)
//	parent={id,ver}    child={id,ver,seq}   → true  (multi-col prefix)
//	parent={id}        child={other,id}     → false (id not at position 0)
//	parent={a,b}       child={a,c}          → false (b not at position 1 of child)
//	parent={id}        child={}             → false (child has no PK)
func isPKPrefix(parentPK, childPK []string) bool {
	if len(parentPK) == 0 || len(childPK) == 0 {
		return false
	}
	if len(parentPK) > len(childPK) {
		return false // parent PK has more cols than child PK — can't be a prefix
	}
	for i, col := range parentPK {
		if childPK[i] != col {
			return false
		}
	}
	return true
}

// parentIndexColsFor is retained for backward compatibility with any callers
// outside loadColumnMatchLinks. Prefer primaryKeyColsFor for new code.
func parentIndexColsFor(t *Table, colName string) []string {
	for _, idx := range t.TableIndexes {
		if !idx.Unique {
			continue
		}
		for _, c := range idx.Columns {
			if c.Name == colName {
				out := make([]string, len(idx.Columns))
				for i, ic := range idx.Columns {
					out[i] = ic.Name
				}
				return out
			}
		}
	}
	return nil
}
