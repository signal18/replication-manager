// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"bufio"
	"bytes"
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Destination is what a destination plugin brings to the CDC core, two things only: making
// its table from the source's table, created on the target as the source describes it (or
// refusing it with the reason), and applying a checkpoint's table to it. The core does the
// capture, the checkpoints, their position, the accumulation and the status.
type Destination interface {
	// Name is the destination of the plugin's TOML (wire.Config.Destination)
	Name() string
	// Refuse says why the destination cannot hold the table described, "" when it can
	Refuse(d TableDesc) string
	// Create turns d's table, as the source describes it on the target, into the destination's
	Create(ctx context.Context, conn *sqlx.Conn, d TableDesc) error
	// Session is what each apply session runs first (a destination written out of the
	// binlog: dbhelper.SessionNoBinlogMixed)
	Session() []string
	// Apply applies one table of a checkpoint to schema.table on conn: its keys deleted, then
	// its rows loaded; a checkpoint applied twice gives the same table. keys, when not nil, is
	// told each batch of keys deleted (the staging records them). ApplyDeleteThenLoad is the
	// apply of a MariaDB destination.
	Apply(ctx context.Context, conn *sqlx.Conn, schema, table string, t wire.TableFiles, files map[string][]byte, name string, keys func([][]interface{}) error) (string, error)
}

// TableDesc is a table of the source as the target holds it before the destination makes it
// its own: what a destination checks.
type TableDesc struct {
	Schema, Table string
	Options       dbhelper.TableOptions
	Columns       []dbhelper.TableColumn
	PrimaryKey    bool
}

// ReadTableDesc describes schema.table on db (a MariaDB server).
func ReadTableDesc(db *sqlx.DB, schema, table string) (TableDesc, error) {
	d := TableDesc{Schema: schema, Table: table}
	var err error
	if d.Options, _, err = dbhelper.GetTableOptions(db, nil, schema, table); err != nil {
		return d, err
	}
	if d.Columns, _, err = dbhelper.GetTableColumns(db, nil, schema, table); err != nil {
		return d, err
	}
	idx, _, err := dbhelper.GetTableIndexNames(db, nil, schema, table)
	if err != nil {
		return d, err
	}
	for _, i := range idx {
		if i == "PRIMARY" {
			d.PrimaryKey = true
		}
	}
	return d, nil
}

// applyKeysBatch: the keys of one DELETE of a checkpoint's apply
const applyKeysBatch = 200

// ApplyDeleteThenLoad is the apply of a MariaDB destination: the keys deleted in batches, then
// the rows loaded with LOAD DATA LOCAL INFILE from memory.
func ApplyDeleteThenLoad(ctx context.Context, conn *sqlx.Conn, schema, table string, t wire.TableFiles, files map[string][]byte, name string, keys func([][]interface{}) error) (string, error) {
	if t.Deletes != "" {
		var batch [][]interface{}
		flush := func() (string, error) {
			q, err := dbhelper.DeleteRowsByKey(ctx, conn, schema, table, t.KeyColumns, batch)
			if err == nil && keys != nil {
				err = keys(batch)
			}
			batch = batch[:0]
			return q, err
		}
		sc := bufio.NewScanner(bytes.NewReader(files[t.Deletes]))
		sc.Buffer(make([]byte, 64*1024), 64*1024*1024)
		for sc.Scan() {
			fields := wire.DecodeTSV(sc.Bytes())
			key := make([]interface{}, len(fields))
			for i, f := range fields {
				if f != nil {
					key[i] = f
				}
			}
			batch = append(batch, key)
			if len(batch) >= applyKeysBatch {
				if q, err := flush(); err != nil {
					return q, fmt.Errorf("delete in %s.%s: %w", schema, table, err)
				}
			}
		}
		if err := sc.Err(); err != nil {
			return "", fmt.Errorf("keys of %s.%s in the checkpoint: %w", schema, table, err)
		}
		if len(batch) > 0 {
			if q, err := flush(); err != nil {
				return q, fmt.Errorf("delete in %s.%s: %w", schema, table, err)
			}
		}
	}
	if t.Inserts != "" {
		q, err := dbhelper.LoadDataLocal(ctx, conn, schema, table, name, files[t.Inserts])
		if err != nil {
			return q, fmt.Errorf("load into %s.%s: %w", schema, table, err)
		}
	}
	return "", nil
}
