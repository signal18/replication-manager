// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// The statements of the CDC core's denormalize mode (CDC_CORE.md "Denormalize"): the
// denormalized table made from the description of the empty result of the denormalizing query,
// the denormalizing query restricted to the primary keys of a micro_checkpoint_pack, the
// keys of the denormalized rows a pack touches, and the position a transactional destination
// keeps with its rows. MariaDB and MySQL only (T23): the CDC core reads a MariaDB binlog and
// writes a MariaDB target; a PostgreSQL server never reaches these functions, a PostgreSQL
// source would be another capture of the core, with its own statements.

// KeyCondition is one source table's part of a restriction: the columns of the query's result
// that hold its primary key, and the key values to match.
type KeyCondition struct {
	Columns []string
	Keys    [][]interface{}
}

// GetPrimaryKeyColumns lists the primary key columns of schema.table in their order; none for
// a table without one.
func GetPrimaryKeyColumns(ctx context.Context, db *sqlx.DB, schema, table string) ([]string, string, error) {
	query := "SELECT k.COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE k WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.CONSTRAINT_NAME = 'PRIMARY' ORDER BY k.ORDINAL_POSITION"
	var cols []string
	err := db.SelectContext(ctx, &cols, query, schema, table)
	return cols, query, err
}

// CreateDenormalizedTable (re)creates schema.table on conn from the description of the empty
// result of the denormalizing query sql, InnoDB, its primary key the columns of key, and an
// index on each of indexes (the primary key columns of one source table: what an unpack looks
// rows up by): the schema created when missing, an older table dropped first (an init starts
// from nothing).
func CreateDenormalizedTable(ctx context.Context, conn *sqlx.Conn, schema, table, sql string, key []string, indexes [][]string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	if len(key) == 0 {
		return "", fmt.Errorf("no key for the denormalized table %s.%s", schema, table)
	}
	// the keys in the CREATE itself, never added after it: a server with
	// innodb_force_primary_key=ON (replication-manager's configurator sets it) refuses a table
	// created without a primary key (error 1173)
	keys := "PRIMARY KEY (" + quoteColumns(key) + ")"
	for i, cols := range indexes {
		if len(cols) == 0 || isPrefix(cols, key) {
			continue // the primary key serves it
		}
		keys += fmt.Sprintf(", INDEX `k%d` (%s)", i, quoteColumns(cols))
	}
	queries := []string{
		"CREATE DATABASE IF NOT EXISTS " + QuoteMySQLIdentifier(schema),
		"DROP TABLE IF EXISTS " + quoted,
		"CREATE TABLE " + quoted + " (" + keys + ") ENGINE=InnoDB AS SELECT * FROM (" + sql + ") AS d WHERE 1=0",
	}
	for _, q := range queries {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return q, err
		}
	}
	return strings.Join(queries, "; "), nil
}

// RestrictedQuery is the denormalizing query sql restricted to the rows whose source-table
// primary key columns hold one of the keys of conds: `SELECT * FROM (sql) AS d WHERE
// (d.a, d.b) IN ((?, ?), ...) UNION SELECT * FROM (sql) AS d WHERE (d.c) IN ((?), ...)`, and
// its arguments. One branch per source table rather than one OR: MariaDB pushes each branch's
// condition into the derived table, on that table's primary key; an OR across the joined
// tables would read the whole join. UNION removes a row two branches return.
func RestrictedQuery(sql string, conds []KeyCondition) (string, []interface{}, error) {
	var ors []string
	var args []interface{}
	for _, c := range conds {
		if len(c.Keys) == 0 {
			continue
		}
		where, a, err := keysIn("d", c.Columns, c.Keys)
		if err != nil {
			return "", nil, err
		}
		ors = append(ors, "SELECT * FROM ("+sql+") AS d WHERE "+where)
		args = append(args, a...)
	}
	if len(ors) == 0 {
		return "", nil, fmt.Errorf("no key to restrict the query to")
	}
	return strings.Join(ors, " UNION "), args, nil
}

// isPrefix: cols are the first columns of key.
func isPrefix(cols, key []string) bool {
	if len(cols) > len(key) {
		return false
	}
	for i, c := range cols {
		if key[i] != c {
			return false
		}
	}
	return true
}

// SelectKeysWhere lists the keyColumns of the rows of schema.table whose cond.Columns hold one
// of cond.Keys: the denormalized rows a micro_checkpoint_pack touches.
func SelectKeysWhere(ctx context.Context, conn *sqlx.Conn, schema, table string, keyColumns []string, cond KeyCondition) ([][]interface{}, string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return nil, "", err
	}
	where, args, err := keysIn("t", cond.Columns, cond.Keys)
	if err != nil {
		return nil, "", err
	}
	query := "SELECT " + quoteColumns(keyColumns) + " FROM " + quoted + " AS t WHERE " + where
	rows, err := conn.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, query, err
	}
	defer rows.Close()
	var out [][]interface{}
	for rows.Next() {
		v, err := rows.SliceScan()
		if err != nil {
			return out, query, err
		}
		out = append(out, v)
	}
	return out, query, rows.Err()
}

// keysIn is `(alias.c1, alias.c2) IN ((?, ?), ...)` with its arguments (a NULL key part never
// matches, as in the source's join).
func keysIn(alias string, cols []string, keys [][]interface{}) (string, []interface{}, error) {
	if len(cols) == 0 || len(keys) == 0 {
		return "", nil, fmt.Errorf("no column or no key")
	}
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = alias + "." + QuoteMySQLIdentifier(c)
	}
	tuple := "(" + strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", ") + ")"
	tuples := make([]string, len(keys))
	var args []interface{}
	for i, k := range keys {
		if len(k) != len(cols) {
			return "", nil, fmt.Errorf("a key of %d values for %d columns", len(k), len(cols))
		}
		tuples[i] = tuple
		args = append(args, k...)
	}
	return "(" + strings.Join(q, ", ") + ") IN (" + strings.Join(tuples, ", ") + ")", args, nil
}

// TruncateTable empties schema.table (TRUNCATE TABLE: an init of a denormalized table).
func TruncateTable(ctx context.Context, conn *sqlx.Conn, schema, table string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "TRUNCATE TABLE " + quoted
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// ReplPluginPosition is the last checkpoint a transactional destination committed, read in
// schema.replplugin_position (made when missing) for plugin; 0 and "" when none.
func ReplPluginPosition(ctx context.Context, conn *sqlx.Conn, schema, plugin string) (int64, string, string, error) {
	quoted := QuoteMySQLIdentifier(schema)
	create := "CREATE TABLE IF NOT EXISTS " + quoted + ".`replplugin_position` (`plugin` VARCHAR(128) NOT NULL PRIMARY KEY, `seq` BIGINT NOT NULL, `gtid` TEXT NOT NULL) ENGINE=InnoDB"
	for _, q := range []string{"CREATE DATABASE IF NOT EXISTS " + quoted, create} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			return 0, "", q, err
		}
	}
	query := "SELECT `seq`, `gtid` FROM " + quoted + ".`replplugin_position` WHERE `plugin` = ?"
	var seq int64
	var gtid string
	err := conn.QueryRowContext(ctx, query, plugin).Scan(&seq, &gtid)
	if err == sql.ErrNoRows {
		return 0, "", query, nil
	}
	return seq, gtid, query, err
}

// SaveReplPluginPosition records plugin's last checkpoint in schema.replplugin_position, in
// the transaction open on conn (the table made by ReplPluginPosition at start).
func SaveReplPluginPosition(ctx context.Context, conn *sqlx.Conn, schema, plugin string, seq int64, gtid string) (string, error) {
	query := "REPLACE INTO " + QuoteMySQLIdentifier(schema) + ".`replplugin_position` (`plugin`, `seq`, `gtid`) VALUES (?, ?, ?)"
	_, err := conn.ExecContext(ctx, query, plugin, seq, gtid)
	return query, err
}
