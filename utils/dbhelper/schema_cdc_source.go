// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

// The destination table of a CDC feed whose source is not a MariaDB server
// (doc/implementation/cluster/CDC_SOURCES.md): the capture source describes each source table
// as MariaDB columns (its type mapping), and the table is made from that list on the MariaDB
// target. MariaDB and MySQL only (T23): it writes a MariaDB target; a PostgreSQL server is never
// a destination of the CDC core.

// ColumnDef is one column of a destination table: its name and its full MariaDB type, as the
// capture source mapped it (e.g. "DECIMAL(12,2)", "DATETIME(6)", "JSON"), NULL allowed or not.
type ColumnDef struct {
	Name     string
	Type     string
	Nullable bool
}

// CreateTableFromColumns creates schema.table on conn, InnoDB, utf8mb4 with the binary
// no-pad collation (a source's text keys compare byte by byte and keep their trailing
// spaces: two keys of the source never collide on the target), its primary key the columns of
// key; the schema created when missing. It never drops a table: an existing table of the same
// name is the caller's to check (CDC feeds empty a table of the same columns, refuse another).
func CreateTableFromColumns(ctx context.Context, conn *sqlx.Conn, schema, table string, cols []ColumnDef, key []string) (string, error) {
	q, err := CreateTableFromColumnsSQL(schema, table, cols, key)
	if err != nil {
		return q, err
	}
	queries := []string{"CREATE DATABASE IF NOT EXISTS " + QuoteMySQLIdentifier(schema), q}
	for _, s := range queries {
		if _, err := conn.ExecContext(ctx, s); err != nil {
			return s, err
		}
	}
	return strings.Join(queries, "; "), nil
}

// CreateTableFromColumnsSQL is the CREATE TABLE statement of CreateTableFromColumns.
func CreateTableFromColumnsSQL(schema, table string, cols []ColumnDef, key []string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("no column for %s.%s", schema, table)
	}
	if len(key) == 0 {
		return "", fmt.Errorf("no primary key for %s.%s", schema, table)
	}
	parts := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		if strings.ContainsAny(c.Type, ";`") {
			return "", fmt.Errorf("column %s: invalid type %q", c.Name, c.Type)
		}
		null := " NOT NULL"
		if c.Nullable {
			null = " NULL"
		}
		parts = append(parts, QuoteMySQLIdentifier(c.Name)+" "+c.Type+null)
	}
	parts = append(parts, "PRIMARY KEY ("+quoteColumns(key)+")")
	return "CREATE TABLE " + quoted + " (" + strings.Join(parts, ", ") + ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_nopad_bin", nil
}
