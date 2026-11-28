// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-mysql-org/go-mysql/schema"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The table mode of an index (CDC_CORE.md "Modes"): its tables copied as they are, each into
// <slave_schema>.<its name>, keyed on its primary key; the partition column, when the index
// is partitioned, added to the key (a partitioned table's primary key holds the partition
// column, and the delta deletes then reach one partition). The init fills each table (by
// SELECT or from the split dump), the binlog's row images carry the delta (copy mode: the
// source's binlog_row_image is FULL).

// copyDest is a source table of a table-mode index and its destination: what the config
// alone says (the apply's routing needs no source).
type copyDest struct {
	idx                   wire.Index
	id                    string // indexID
	srcSchema, srcTable   string
	destSchema, destTable string
}

func (d copyDest) initKey() string { return wire.InitKey(d.id, d.destSchema, d.destTable) }

// copyDests lists the tables of the table-mode indexes of cfg and their destinations; refused
// when a table is not schema.table, when a destination is its own source, or when two
// tables share a destination.
func copyDests(cfg wire.Config) ([]copyDest, error) {
	var out []copyDest
	seen := map[string]string{}
	for _, idx := range cfg.Indexes {
		if !idx.TableMode() {
			continue
		}
		if idx.Name == "" || idx.Schema == "" || len(idx.Tables) == 0 {
			return nil, fmt.Errorf("index %q (table mode): name, slave_schema and tables are required", idx.Name)
		}
		if idx.Table != "" && len(idx.Tables) > 1 {
			return nil, fmt.Errorf("index %s: slave_table renames one table, %d are listed", idx.Name, len(idx.Tables))
		}
		for _, t := range idx.Tables {
			s, n, ok := strings.Cut(strings.TrimSpace(t), ".")
			if !ok || s == "" || n == "" || strings.ContainsAny(n, ".*`") {
				return nil, fmt.Errorf("index %s: table %q: schema.table", idx.Name, t)
			}
			d := copyDest{idx: idx, id: indexID(idx), srcSchema: s, srcTable: n, destSchema: idx.Schema, destTable: n}
			if idx.Table != "" {
				d.destTable = idx.Table
			}
			if strings.EqualFold(d.srcSchema, d.destSchema) && strings.EqualFold(d.srcTable, d.destTable) {
				return nil, fmt.Errorf("index %s: %s.%s would be copied onto itself: give the index another slave_schema", idx.Name, s, n)
			}
			dest := strings.ToLower(d.destSchema + "." + d.destTable)
			if other, dup := seen[dest]; dup {
				return nil, fmt.Errorf("index %s: %s.%s is already the destination of %s", idx.Name, d.destSchema, d.destTable, other)
			}
			seen[dest] = s + "." + n
			out = append(out, d)
		}
	}
	return out, nil
}

// tableCopy is a copyDest resolved on the source: its columns, its key.
type tableCopy struct {
	copyDest
	part    *partitioning
	columns []dbhelper.TableColumn
	pk      []string
	key     []string // pk, then the partition column when it is not in it
}

// columnsOfTable reads a source table's columns; a variable for the tests.
var columnsOfTable = func(ctx context.Context, src *sqlx.DB, schema, table string) ([]dbhelper.TableColumn, error) {
	cols, q, err := dbhelper.GetTableColumns(src, nil, schema, table)
	if err != nil {
		return nil, fmt.Errorf("%w (%s)", err, q)
	}
	return cols, nil
}

// resolveCopy checks a source table for the table mode and reads its description.
func resolveCopy(ctx context.Context, src *sqlx.DB, d copyDest) (*tableCopy, error) {
	tc := &tableCopy{copyDest: d}
	var err error
	if tc.part, err = newPartitioning(d.idx); err != nil {
		return nil, err
	}
	name := d.srcSchema + "." + d.srcTable
	if tc.columns, err = columnsOfTable(ctx, src, d.srcSchema, d.srcTable); err != nil {
		return nil, fmt.Errorf("index %s: %s: %w", d.idx.Name, name, err)
	}
	if len(tc.columns) == 0 {
		return nil, fmt.Errorf("index %s: %s does not exist on the source", d.idx.Name, name)
	}
	if tc.pk, err = primaryKeyOf(ctx, src, d.srcSchema, d.srcTable); err != nil {
		return nil, fmt.Errorf("index %s: %w", d.idx.Name, err)
	}
	return tc, tc.check()
}

// check refuses what the copy cannot carry exactly: no primary key, a generated column (its
// value is not in every row image), a BIT column; and a partition column that is missing,
// not a time, or NULL-able outside the key (a primary key column is never NULL).
func (tc *tableCopy) check() error {
	name := tc.srcSchema + "." + tc.srcTable
	if len(tc.pk) == 0 {
		return fmt.Errorf("index %s: %s has no primary key", tc.idx.Name, name)
	}
	var partCol *dbhelper.TableColumn
	for i, c := range tc.columns {
		if c.GenerationExp.Valid && c.GenerationExp.String != "" || strings.Contains(strings.ToUpper(c.Extra), "GENERATED") {
			return fmt.Errorf("index %s: %s.%s is a generated column: not carried by the table mode", tc.idx.Name, name, c.Name)
		}
		if strings.EqualFold(c.DataType, "bit") {
			return fmt.Errorf("index %s: %s.%s is a BIT column: not carried by the table mode", tc.idx.Name, name, c.Name)
		}
		if tc.part != nil && strings.EqualFold(c.Name, tc.part.column) {
			partCol = &tc.columns[i]
		}
	}
	tc.key = append([]string(nil), tc.pk...)
	if tc.part == nil {
		return nil
	}
	if partCol == nil {
		return fmt.Errorf("index %s: %s has no column %s to partition by", tc.idx.Name, name, tc.part.column)
	}
	switch strings.ToLower(partCol.DataType) {
	case "date", "datetime", "timestamp":
	default:
		return fmt.Errorf("index %s: %s.%s is a %s: a partition column is a DATE, DATETIME or TIMESTAMP", tc.idx.Name, name, partCol.Name, partCol.DataType)
	}
	tc.part.column = partCol.Name
	for _, k := range tc.pk {
		if strings.EqualFold(k, partCol.Name) {
			return nil
		}
	}
	if partCol.Nullable {
		return fmt.Errorf("index %s: %s.%s may be NULL: a partition column outside the primary key must be NOT NULL (it joins the destination's key)", tc.idx.Name, name, partCol.Name)
	}
	tc.key = append(tc.key, partCol.Name)
	return nil
}

// columnNames are the source table's columns, in order.
func (tc *tableCopy) columnNames() []string {
	out := make([]string, len(tc.columns))
	for i, c := range tc.columns {
		out[i] = c.Name
	}
	return out
}

// schemaElement is the -schema element of the copy: the destination's description.
func (tc *tableCopy) schemaElement(gtid, source, from string) wire.SchemaElement {
	cols := make([]wire.Column, len(tc.columns))
	for i, c := range tc.columns {
		cols[i] = wire.Column{Name: c.Name, Type: c.ColumnType, Charset: c.Charset.String, Collation: c.Collation.String, Nullable: c.Nullable}
	}
	e := wire.SchemaElement{Name: tc.idx.Name + "-schema", Index: tc.id, Mode: wire.IndexModeTable, Schema: tc.destSchema, Table: tc.destTable,
		Columns: cols, Key: tc.key, DeleteKey: tc.key, GTID: gtid, Source: source, PartitionFrom: from}
	if tc.part != nil {
		e.PartitionColumn, e.PartitionPeriod = tc.part.column, tc.part.period
	}
	return e
}

// copyKey is the columns of t a copy's rows are found by: its key's names (the primary key,
// then the partition column), current when one is not a column of t.
func copyKey(t *schema.Table, names []string, current []int) []int {
	out := make([]int, 0, len(names))
	for _, n := range names {
		i := t.FindColumn(n)
		if i < 0 {
			return current
		}
		out = append(out, i)
	}
	return out
}
