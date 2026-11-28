// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"context"
	"fmt"
	"regexp"
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
	// Charset, Collation: a text column's, "" the table's (utf8mb4, binary no-pad)
	Charset, Collation string
}

// CreateTableFromColumns creates schema.table on conn, InnoDB, utf8mb4 with the binary
// no-pad collation (a source's text keys compare byte by byte and keep their trailing
// spaces: two keys of the source never collide on the target), its primary key the columns of
// key; the schema created when missing. It never drops a table: an existing table of the same
// name is the caller's to check (CDC feeds empty a table of the same columns, refuse another).
func CreateTableFromColumns(ctx context.Context, conn *sqlx.Conn, schema, table string, cols []ColumnDef, key []string) (string, error) {
	return CreateTableFromColumnsIndexed(ctx, conn, schema, table, cols, key, nil)
}

// CreateTableFromColumnsSQL is the CREATE TABLE statement of CreateTableFromColumns.
// columnTypeWord is a MariaDB column type made of words and an optional (n) or (p,s):
// BIGINT, DECIMAL(65,30), VARCHAR(49), TINYINT(1), DOUBLE PRECISION, INT UNSIGNED.
var columnTypeWord = regexp.MustCompile(`^[A-Za-z]+( [A-Za-z]+)*(\([0-9]+( *, *[0-9]+)?\))?( [A-Za-z]+)*$`)

// charsetName is a character set or collation name.
var charsetName = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// columnTypeList is ENUM or SET with its labels, each quoted with ” and \ escaped.
var columnTypeList = regexp.MustCompile(`^(?i:ENUM|SET)\('(?:[^'\\]|''|\\.)*'(?:,'(?:[^'\\]|''|\\.)*')*\)$`)

// validColumnType is the allowlist of the column types a CREATE takes from a source's
// catalogue: a type word, or an ENUM/SET; never a comment, a statement or a stray parenthesis.
func validColumnType(t string) bool {
	return columnTypeWord.MatchString(t) || columnTypeList.MatchString(t)
}

func CreateTableFromColumnsSQL(schema, table string, cols []ColumnDef, key []string) (string, error) {
	return CreateTableFromColumnsIndexedSQL(schema, table, cols, key, nil)
}

// CreateTableFromColumnsIndexedSQL is CreateTableFromColumnsSQL with a secondary index on each
// of indexes (one served by the primary key skipped).
func CreateTableFromColumnsIndexedSQL(schema, table string, cols []ColumnDef, key []string, indexes [][]string) (string, error) {
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
		if !validColumnType(c.Type) {
			return "", fmt.Errorf("column %s: invalid type %q", c.Name, c.Type)
		}
		null := " NOT NULL"
		if c.Nullable {
			null = " NULL"
		}
		cs := ""
		for _, n := range []struct{ kw, v string }{{" CHARACTER SET ", c.Charset}, {" COLLATE ", c.Collation}} {
			if n.v == "" {
				continue
			}
			if !charsetName.MatchString(n.v) {
				return "", fmt.Errorf("column %s: invalid charset or collation %q", c.Name, n.v)
			}
			cs += n.kw + n.v
		}
		parts = append(parts, QuoteMySQLIdentifier(c.Name)+" "+c.Type+cs+null)
	}
	parts = append(parts, "PRIMARY KEY ("+quoteColumns(key)+")")
	for i, ix := range indexes {
		if len(ix) == 0 || isPrefix(ix, key) {
			continue
		}
		parts = append(parts, fmt.Sprintf("INDEX `k%d` (%s)", i, quoteColumns(ix)))
	}
	return "CREATE TABLE " + quoted + " (" + strings.Join(parts, ", ") + ") ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_nopad_bin", nil
}
