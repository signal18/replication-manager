// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// replplugin-columnstore is the ColumnStore destination of the CDC core (#2015,
// cluster/replplugin/cdc): it carries a source's tables into a ColumnStore copy, which takes
// no row UPDATE or DELETE from replication. The checkpoints are applied with ColumnStore's
// bulk path, the keys deleted then the rows loaded with LOAD DATA. The ColumnStore tables are
// made by the splitdump restore's conversion in replication-manager (the restore feeds this
// plugin their rows), not by -create.
package main

import (
	"context"
	"errors"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/cdc"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

type columnstore struct{}

func (columnstore) Name() string                { return "columnstore" }
func (columnstore) Refuse(cdc.TableDesc) string { return "" }
func (columnstore) Session() []string           { return nil }
func (columnstore) Create(context.Context, *sqlx.Conn, cdc.TableDesc) error {
	return errors.New("a ColumnStore table is made by the seed's restore (the splitdump conversion)")
}
func (columnstore) Apply(ctx context.Context, conn *sqlx.Conn, schema, table string, t wire.TableFiles, files map[string][]byte, name string, keys func([][]interface{}) error) (string, error) {
	return cdc.ApplyDeleteThenLoad(ctx, conn, schema, table, t, files, name, keys)
}

func main() { cdc.Main(columnstore{}) }
