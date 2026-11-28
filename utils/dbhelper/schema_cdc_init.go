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

// The reads of a CDC init by SELECT (CDC_CORE.md "Init"): a table read range by range on its
// primary key, and the earliest value of a partition column, both inside the consistent
// snapshot the caller opened on conn. Read only: nothing is written on the source (F1).
// MariaDB and MySQL only (T23): the CDC core reads a MariaDB source; a PostgreSQL source is
// another capture of the core (CDC_SOURCES.md), with its own initial load.

// SelectTableRange reads up to limit rows of schema.table (columns cols, in order) whose key
// columns come after the key after (none: from the start), in key order: one range of a
// keyset scan. The condition is written out (a > ? OR (a = ? AND b > ?)), which every
// server reads as a range of the primary key.
func SelectTableRange(ctx context.Context, conn *sqlx.Conn, schema, table string, cols, key []string, after []interface{}, limit int) (*sql.Rows, string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return nil, "", err
	}
	if len(key) == 0 || limit <= 0 {
		return nil, "", fmt.Errorf("a range of %s.%s needs its key and a limit", schema, table)
	}
	query := "SELECT " + quoteColumns(cols) + " FROM " + quoted
	var args []interface{}
	if len(after) > 0 {
		if len(after) != len(key) {
			return nil, "", fmt.Errorf("a key of %d values for %d columns", len(after), len(key))
		}
		var ors []string
		for i := range key {
			var ands []string
			for j := 0; j < i; j++ {
				ands = append(ands, QuoteMySQLIdentifier(key[j])+" = ?")
				args = append(args, after[j])
			}
			ands = append(ands, QuoteMySQLIdentifier(key[i])+" > ?")
			args = append(args, after[i])
			ors = append(ors, "("+strings.Join(ands, " AND ")+")")
		}
		query += " WHERE " + strings.Join(ors, " OR ")
	}
	query += fmt.Sprintf(" ORDER BY %s LIMIT %d", quoteColumns(key), limit)
	rows, err := conn.QueryContext(ctx, query, args...)
	return rows, query, err
}

// SelectMinOf is the smallest value of column among the rows of from (a table, or a query
// as a derived table: "(SELECT ...) AS d"), as text; "" when there is none.
func SelectMinOf(ctx context.Context, conn *sqlx.Conn, from, column string) (string, string, error) {
	query := "SELECT MIN(" + QuoteMySQLIdentifier(column) + ") FROM " + from
	var v sql.NullString
	err := conn.QueryRowContext(ctx, query).Scan(&v)
	return v.String, query, err
}

// SelectGTIDCurrentPos is the server's @@gtid_current_pos (MariaDB).
func SelectGTIDCurrentPos(ctx context.Context, conn *sqlx.Conn) (string, string, error) {
	query := "SELECT @@gtid_current_pos"
	var v sql.NullString
	err := conn.QueryRowContext(ctx, query).Scan(&v)
	return v.String, query, err
}
