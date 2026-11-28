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

// The partitioned destination of a CDC index (CDC_CORE.md "Partitioning"): a table
// partitioned by RANGE on a time column, its partitions named and bounded by the CDC core
// (one per period), the next ones added ahead of time. RANGE COLUMNS on a DATE, DATETIME or
// integer column; RANGE (UNIX_TIMESTAMP(col)) on a TIMESTAMP column, which RANGE COLUMNS does not take.
// MariaDB and MySQL only (T23): the CDC core writes a MariaDB target; a PostgreSQL server is
// never a destination of these statements (a PostgreSQL destination partitions its own way).

// RangePartition is one partition: its name and the value it holds less than, a date or a
// datetime literal ('2026-12-01', '2026-12-01 00:00:00'), or an integer (an id range).
type RangePartition struct {
	Name     string
	LessThan string
}

var (
	partitionName    = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	partitionLiteral = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}( [0-9]{2}:[0-9]{2}:[0-9]{2})?$`)
	partitionInteger = regexp.MustCompile(`^-?[0-9]{1,20}$`)
)

func rangePartitionsSQL(unixTimestamp bool, parts []RangePartition) (string, error) {
	if len(parts) == 0 {
		return "", fmt.Errorf("no partition")
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		if !partitionName.MatchString(p.Name) {
			return "", fmt.Errorf("partition %q: invalid name", p.Name)
		}
		var bound string
		switch {
		case partitionInteger.MatchString(p.LessThan):
			bound = p.LessThan // an integer column (an id range)
		case partitionLiteral.MatchString(p.LessThan):
			bound = "'" + p.LessThan + "'"
			if unixTimestamp {
				bound = "UNIX_TIMESTAMP(" + bound + ")"
			}
		default:
			return "", fmt.Errorf("partition %q less than %q: invalid", p.Name, p.LessThan)
		}
		out[i] = "PARTITION " + QuoteMySQLIdentifier(p.Name) + " VALUES LESS THAN (" + bound + ")"
	}
	return strings.Join(out, ", "), nil
}

// RangePartitionClause is the PARTITION BY clause of column with parts.
func RangePartitionClause(column string, unixTimestamp bool, parts []RangePartition) (string, error) {
	list, err := rangePartitionsSQL(unixTimestamp, parts)
	if err != nil {
		return "", err
	}
	if unixTimestamp {
		return "PARTITION BY RANGE (UNIX_TIMESTAMP(" + QuoteMySQLIdentifier(column) + ")) (" + list + ")", nil
	}
	return "PARTITION BY RANGE COLUMNS(" + QuoteMySQLIdentifier(column) + ") (" + list + ")", nil
}

// PartitionTable partitions schema.table on conn by clause (RangePartitionClause): the
// destination is made, still empty, then partitioned.
func PartitionTable(ctx context.Context, conn *sqlx.Conn, schema, table, clause string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	query := "ALTER TABLE " + quoted + " " + clause
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// AddRangePartitions adds parts after the last partition of schema.table.
func AddRangePartitions(ctx context.Context, conn *sqlx.Conn, schema, table string, unixTimestamp bool, parts []RangePartition) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	list, err := rangePartitionsSQL(unixTimestamp, parts)
	if err != nil {
		return "", err
	}
	query := "ALTER TABLE " + quoted + " ADD PARTITION (" + list + ")"
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// GetPartitionNames lists the partitions of schema.table in their order; none for a table
// that is not partitioned.
func GetPartitionNames(ctx context.Context, conn *sqlx.Conn, schema, table string) ([]string, string, error) {
	query := "SELECT PARTITION_NAME FROM information_schema.PARTITIONS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND PARTITION_NAME IS NOT NULL ORDER BY PARTITION_ORDINAL_POSITION"
	var out []string
	err := conn.SelectContext(ctx, &out, query, schema, table)
	return out, query, err
}

// GetColumnDataType is the DATA_TYPE of schema.table.column (date, datetime, timestamp...),
// "" when there is no such column.
func GetColumnDataType(ctx context.Context, conn *sqlx.Conn, schema, table, column string) (string, string, error) {
	query := "SELECT DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = ?"
	var out []string
	if err := conn.SelectContext(ctx, &out, query, schema, table, column); err != nil {
		return "", query, err
	}
	if len(out) == 0 {
		return "", query, nil
	}
	return strings.ToLower(out[0]), query, nil
}

// CreateTableFromColumnsIndexed creates schema.table on conn as CreateTableFromColumns, with a
// secondary index on each of indexes; the schema created when missing.
func CreateTableFromColumnsIndexed(ctx context.Context, conn *sqlx.Conn, schema, table string, cols []ColumnDef, key []string, indexes [][]string) (string, error) {
	q, err := CreateTableFromColumnsIndexedSQL(schema, table, cols, key, indexes)
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

// DropPartitions drops the partitions names of schema.table, their rows with them: the purge
// of a CDC destination's oldest periods. MariaDB and MySQL only, as the rest of this file: a
// PostgreSQL destination drops its own partitions (DROP TABLE of a partition), never through it.
func DropPartitions(ctx context.Context, conn *sqlx.Conn, schema, table string, names []string) (string, error) {
	quoted, err := QuoteMySQLTableIdentifier(schema + "." + table)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("no partition to drop")
	}
	q := make([]string, len(names))
	for i, n := range names {
		if !partitionName.MatchString(n) {
			return "", fmt.Errorf("partition %q: invalid", n)
		}
		q[i] = QuoteMySQLIdentifier(n)
	}
	query := "ALTER TABLE " + quoted + " DROP PARTITION " + strings.Join(q, ", ")
	_, err = conn.ExecContext(ctx, query)
	return query, err
}
