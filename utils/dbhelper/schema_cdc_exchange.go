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

// The statements of a CDC destination fed partition by partition (CDC_CORE.md "A destination with
// partition work of its own"; the S3 destination): delta tables of the destination's partitioning
// beside it, and per changed partition a work table rebuilt from the partition and its delta, then
// swapped in with EXCHANGE PARTITION. MariaDB only (the S3 engine, EXCHANGE PARTITION): a
// PostgreSQL server never reaches them; a PostgreSQL destination attaches and detaches its
// partitions its own way.

func quotedTable(schema, table string) (string, error) {
	return QuoteMySQLTableIdentifier(schema + "." + table)
}

// CreateTableLikeIfMissing creates schema.table LIKE likeSchema.likeTable (its columns, keys and
// partitioning) when it does not exist.
func CreateTableLikeIfMissing(ctx context.Context, conn *sqlx.Conn, schema, table, likeSchema, likeTable string) (string, error) {
	t, err := quotedTable(schema, table)
	if err != nil {
		return "", err
	}
	l, err := quotedTable(likeSchema, likeTable)
	if err != nil {
		return "", err
	}
	query := "CREATE TABLE IF NOT EXISTS " + t + " LIKE " + l
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// DropColumnsExcept drops every column of schema.table but keep (a keys table made LIKE its
// table); nothing when there is none to drop.
func DropColumnsExcept(ctx context.Context, conn *sqlx.Conn, schema, table string, keep []string) (string, error) {
	cols, q, err := GetColumnNamesConn(ctx, conn, schema, table)
	if err != nil {
		return q, err
	}
	t, err := quotedTable(schema, table)
	if err != nil {
		return "", err
	}
	var drops []string
	for _, c := range cols {
		kept := false
		for _, k := range keep {
			kept = kept || strings.EqualFold(c, k)
		}
		if !kept {
			drops = append(drops, "DROP COLUMN "+QuoteMySQLIdentifier(c))
		}
	}
	if len(drops) == 0 {
		return "", nil
	}
	query := "ALTER TABLE " + t + " " + strings.Join(drops, ", ")
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// GetPrimaryKeyColumnsConn lists the primary key columns of schema.table in their order, on conn.
func GetPrimaryKeyColumnsConn(ctx context.Context, conn *sqlx.Conn, schema, table string) ([]string, string, error) {
	query := "SELECT k.COLUMN_NAME FROM information_schema.KEY_COLUMN_USAGE k WHERE k.TABLE_SCHEMA = ? AND k.TABLE_NAME = ? AND k.CONSTRAINT_NAME = 'PRIMARY' ORDER BY k.ORDINAL_POSITION"
	var cols []string
	err := conn.SelectContext(ctx, &cols, query, schema, table)
	return cols, query, err
}

// ReplaceRows is InsertRows with REPLACE: a row of the same key is replaced (a delta table's
// rows to put, the last change wins).
func ReplaceRows(ctx context.Context, conn *sqlx.Conn, schema, table string, rows [][]interface{}) (string, error) {
	return insertRows(ctx, conn, "REPLACE", schema, table, rows)
}

// AlterEngine converts schema.table to engine (S3 for a work table before its exchange, InnoDB to
// write a table that was S3).
func AlterEngine(ctx context.Context, conn *sqlx.Conn, schema, table, engine string) (string, error) {
	t, err := quotedTable(schema, table)
	if err != nil {
		return "", err
	}
	if !partitionName.MatchString(engine) {
		return "", fmt.Errorf("engine %q: invalid", engine)
	}
	query := "ALTER TABLE " + t + " ENGINE=" + engine
	_, err = conn.ExecContext(ctx, query)
	return query, err
}

// PartitionHasRows says whether the partition of schema.table holds a row.
func PartitionHasRows(ctx context.Context, conn *sqlx.Conn, schema, table, partition string) (bool, string, error) {
	t, err := quotedTable(schema, table)
	if err != nil {
		return false, "", err
	}
	if !partitionName.MatchString(partition) {
		return false, "", fmt.Errorf("partition %q: invalid", partition)
	}
	query := "SELECT 1 FROM " + t + " PARTITION (" + QuoteMySQLIdentifier(partition) + ") LIMIT 1"
	var one []int
	err = conn.SelectContext(ctx, &one, query)
	return len(one) > 0, query, err
}

// CountPartitionRows counts the rows of the partition of schema.table.
func CountPartitionRows(ctx context.Context, conn *sqlx.Conn, schema, table, partition string) (int64, string, error) {
	t, err := quotedTable(schema, table)
	if err != nil {
		return 0, "", err
	}
	if !partitionName.MatchString(partition) {
		return 0, "", fmt.Errorf("partition %q: invalid", partition)
	}
	query := "SELECT COUNT(*) FROM " + t + " PARTITION (" + QuoteMySQLIdentifier(partition) + ")"
	var n int64
	err = conn.QueryRowContext(ctx, query).Scan(&n)
	return n, query, err
}

// RebuildPartitionStatements are the statements that rebuild one partition of schema.table from
// its delta in work (a table of no partitioning): the work table made from the delta's structure,
// filled from the partition, the rows to put replaced, the keys deleted removed, converted to
// engine, exchanged with the partition, then dropped (it holds the old partition) and the
// partition's delta emptied. Run in order, each one again from the first after a failure gives the
// same partition (the delta is emptied last).
func RebuildPartitionStatements(schema, table, partition, replaceTable, deleteTable, work, engine string, key []string) ([]string, error) {
	t, err := quotedTable(schema, table)
	if err != nil {
		return nil, err
	}
	r, err := quotedTable(schema, replaceTable)
	if err != nil {
		return nil, err
	}
	d, err := quotedTable(schema, deleteTable)
	if err != nil {
		return nil, err
	}
	w, err := quotedTable(schema, work)
	if err != nil {
		return nil, err
	}
	if !partitionName.MatchString(partition) || !partitionName.MatchString(engine) || len(key) == 0 {
		return nil, fmt.Errorf("partition %q engine %q key %v: invalid", partition, engine, key)
	}
	p := QuoteMySQLIdentifier(partition)
	return []string{
		"DROP TABLE IF EXISTS " + w,
		"CREATE TABLE " + w + " LIKE " + r,
		"ALTER TABLE " + w + " REMOVE PARTITIONING",
		"INSERT INTO " + w + " SELECT * FROM " + t + " PARTITION (" + p + ")",
		"REPLACE INTO " + w + " SELECT * FROM " + r + " PARTITION (" + p + ")",
		"DELETE w FROM " + w + " AS w JOIN " + d + " PARTITION (" + p + ") AS k USING (" + quoteColumns(key) + ")",
		"ALTER TABLE " + w + " ENGINE=" + engine,
		"ALTER TABLE " + t + " EXCHANGE PARTITION " + p + " WITH TABLE " + w,
		"DROP TABLE " + w,
		"ALTER TABLE " + r + " TRUNCATE PARTITION " + p,
		"ALTER TABLE " + d + " TRUNCATE PARTITION " + p,
	}, nil
}
