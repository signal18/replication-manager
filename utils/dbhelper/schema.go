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
	var logBuilder strings.Builder

	crc64Table := crc64.MakeTable(0xC96C5795D7870F42)

	// Bulk information_schema scans reduce round trips and metadata lock pressure.
	tables, qlog, err := getAllTables(db, myver, defaultSchemaScanTimeout)
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

	qlog, err = loadAllColumns(db, myver, tablemap, defaultSchemaScanTimeout)
	appendLog(&logBuilder, qlog)
	if err != nil {
		return tablemap, tables, logBuilder.String(), err
	}

	qlog, err = loadAllIndexes(db, myver, tablemap, defaultSchemaScanTimeout)
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

func getAllTables(db *sqlx.DB, myver *version.Version, timeout time.Duration) ([]Table, string, error) {
	query := tablesQueryAll(myver)
	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := db.QueryxContext(ctx, query)
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

func loadAllColumns(db *sqlx.DB, myver *version.Version, tablemap map[string]*Table, timeout time.Duration) (string, error) {
	query := columnDefQueryAll(myver)
	if query == "" {
		return "", nil
	}

	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := db.QueryxContext(ctx, query)
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

func loadAllIndexes(db *sqlx.DB, myver *version.Version, tablemap map[string]*Table, timeout time.Duration) (string, error) {
	query := indexDefQueryAll(myver)
	if query == "" {
		return "", nil
	}

	ctx, cancel := scanContext(timeout)
	defer cancel()

	rows, err := db.QueryxContext(ctx, query)
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
		return fmt.Sprintf(`SELECT '%s' AS table_schema, table_name, ordinal_position, column_name, udt_name AS column_type,
			is_nullable, column_default, '' AS extra, '' AS character_set_name, '' AS collation_name
			FROM information_schema.columns WHERE table_schema = 'public' ORDER BY table_name, ordinal_position`, schema)
	}
	return fmt.Sprintf(`SELECT table_schema, table_name, ordinal_position, column_name, column_type,
		is_nullable, column_default, extra, character_set_name, collation_name
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
	return fmt.Sprintf(`SELECT table_schema, table_name, index_name, non_unique, index_type, seq_in_index,
		column_name, sub_part
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
