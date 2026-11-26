// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// The statements of the CDC core's destinations and of its InnoDB staging (CDC_CORE.md): a
// destination table made from the source's table, the staging tables of a carried table (its
// rows to put, its keys to delete), and the merge of the staging into the destination.
// MariaDB only: a PostgreSQL server has no storage engine to choose (ErrNoStorageEngine).

// SessionNoBinlogMixed is what the apply session of a destination written out of the binlog
// runs first: an engine without row-level update or delete takes an UPDATE or DELETE
// statement whole only when the session's binlog format is not ROW (even with sql_log_bin=0,
// it then fails with error 1031); the copy is not binlogged.
var SessionNoBinlogMixed = []string{"SET SESSION sql_log_bin = 0", "SET SESSION binlog_format = 'MIXED'"}

// ConvertTableEngineConn moves schema.table to engine on conn, dropping the AUTO_INCREMENT of
// autoInc (an engine that refuses the attribute, error 1164: the values come from the source): one
// ALTER TABLE ... MODIFY ..., ENGINE=..., a copy of the table. autoInc nil: the engine only.
// PostgreSQL: ErrNoStorageEngine.
func ConvertTableEngineConn(ctx context.Context, conn *sqlx.Conn, myver *version.Version, schema, table, engine string, autoInc *TableColumn) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return "", ErrNoStorageEngine
	}
	if !engineNameRe.MatchString(engine) {
		return "", fmt.Errorf("invalid engine name %q", engine)
	}
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "ALTER TABLE " + quoted + " "
	if autoInc != nil {
		def := QuoteMySQLIdentifier(autoInc.Name) + " " + autoInc.ColumnType
		if !autoInc.Nullable {
			def += " NOT NULL"
		}
		query += "MODIFY " + def + ", "
	}
	query += "ENGINE=" + engine
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// CreateStagingTables creates, when missing, the staging of srcSchema.srcTable in
// stageSchema: rowsTable, the rows to put (its structure, InnoDB) and keysTable, the keys to
// delete (the key columns, their primary key). PostgreSQL: ErrNoStorageEngine.
func CreateStagingTables(ctx context.Context, conn *sqlx.Conn, myver *version.Version, stageSchema, rowsTable, keysTable, srcSchema, srcTable string, keyColumns []string) (string, error) {
	if myver != nil && myver.IsPostgreSQL() {
		return "", ErrNoStorageEngine
	}
	if len(keyColumns) == 0 {
		return "", fmt.Errorf("no key column for the staging of %s.%s", srcSchema, srcTable)
	}
	src, err := QuoteMySQLTableIdentifier(srcSchema + "." + srcTable)
	if err != nil {
		return "", err
	}
	rows := QuoteMySQLIdentifier(stageSchema) + "." + QuoteMySQLIdentifier(rowsTable)
	keys := QuoteMySQLIdentifier(stageSchema) + "." + QuoteMySQLIdentifier(keysTable)
	cols := quoteColumns(keyColumns)
	var exists int
	if err := conn.GetContext(ctx, &exists, "SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA = ? AND TABLE_NAME IN (?, ?)", stageSchema, rowsTable, keysTable); err != nil {
		return "", err
	}
	if exists == 2 {
		return "", nil
	}
	queries := []string{
		"CREATE DATABASE IF NOT EXISTS " + QuoteMySQLIdentifier(stageSchema),
		// LIKE copies the destination's engine: the staging is InnoDB, transactional
		"CREATE TABLE IF NOT EXISTS " + rows + " LIKE " + src,
		"ALTER TABLE " + rows + " ENGINE=InnoDB COMMENT=" + QuoteMySQLString("replication-manager staging of "+srcSchema+"."+srcTable),
		"CREATE TABLE IF NOT EXISTS " + keys + " (PRIMARY KEY (" + cols + ")) ENGINE=InnoDB SELECT " + cols + " FROM " + src + " WHERE 0",
	}
	for _, q := range queries {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return q, err
		}
	}
	return strings.Join(queries, "; "), nil
}

// InsertIgnoreKeys adds keys to the keys table schema.table (its key columns, their primary
// key), the ones it holds already left: the staged deletes.
func InsertIgnoreKeys(ctx context.Context, conn *sqlx.Conn, schema, table string, keyColumns []string, keys [][]interface{}) (string, error) {
	if len(keys) == 0 || len(keyColumns) == 0 {
		return "", nil
	}
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	row := "(" + strings.TrimSuffix(strings.Repeat("?,", len(keyColumns)), ",") + ")"
	tuples := make([]string, len(keys))
	var args []interface{}
	for i, k := range keys {
		if len(k) != len(keyColumns) {
			return "", fmt.Errorf("a key of %d values for %d key columns", len(k), len(keyColumns))
		}
		tuples[i] = row
		args = append(args, k...)
	}
	query := "INSERT IGNORE INTO " + quoted + " (" + quoteColumns(keyColumns) + ") VALUES " + strings.Join(tuples, ",")
	_, err = conn.ExecContext(ctx, query, args...)
	return query, err
}

// InsertSelectAll copies every row of srcSchema.srcTable into dstSchema.dstTable (the same
// columns in the same order): the merge of the staged rows.
func InsertSelectAll(ctx context.Context, conn *sqlx.Conn, dstSchema, dstTable, srcSchema, srcTable string) (string, error) {
	dst, err := QuoteMySQLTableIdentifier(dstSchema + "." + dstTable)
	if err != nil {
		return "", err
	}
	src, err := QuoteMySQLTableIdentifier(srcSchema + "." + srcTable)
	if err != nil {
		return "", err
	}
	query := "INSERT INTO " + dst + " SELECT * FROM " + src
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// DeleteAllRows empties schema.table in the transaction of conn (not TRUNCATE: a DDL commits).
func DeleteAllRows(ctx context.Context, conn *sqlx.Conn, schema, table string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "DELETE FROM " + quoted
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// ForEachKeyBatch reads the key columns of schema.table on conn and calls fn with each batch
// of at most batch keys: the staged deletes, never all in memory.
func ForEachKeyBatch(ctx context.Context, conn *sqlx.Conn, schema, table string, keyColumns []string, batch int, fn func([][]interface{}) error) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "SELECT " + quoteColumns(keyColumns) + " FROM " + quoted
	rows, err := conn.QueryxContext(ctx, query)
	if err != nil {
		return query, err
	}
	defer rows.Close()
	var keys [][]interface{}
	for rows.Next() {
		v, err := rows.SliceScan()
		if err != nil {
			return query, err
		}
		keys = append(keys, v)
		if len(keys) >= batch {
			if err := fn(keys); err != nil {
				return query, err
			}
			keys = nil
		}
	}
	if err := rows.Err(); err != nil {
		return query, err
	}
	if len(keys) > 0 {
		return query, fn(keys)
	}
	return query, nil
}

func quoteColumns(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = QuoteMySQLIdentifier(c)
	}
	return strings.Join(q, ", ")
}
