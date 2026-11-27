// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cdc

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// InnoDB is the InnoDB destination (replplugin-innodb, and the destination of the capture
// sources that write a MariaDB InnoDB target): the tables stay InnoDB; each checkpoint is
// applied in one transaction that also records its position on the target (Positioner), so a
// crash never applies a checkpoint twice nor loses one. The apply runs with the binlog on: the
// target's own replicas follow the tables.
type InnoDB struct{}

func (InnoDB) Name() string            { return "innodb" }
func (InnoDB) Refuse(TableDesc) string { return "" }
func (InnoDB) Session() []string       { return nil }

// Create: the table the target holds is already InnoDB.
func (InnoDB) Create(context.Context, *sqlx.Conn, TableDesc) error { return nil }

func (InnoDB) Apply(ctx context.Context, conn *sqlx.Conn, schema, table string, t wire.TableFiles, files map[string][]byte, name string, keys func([][]interface{}) error) (string, error) {
	return ApplyDeleteThenLoad(ctx, conn, schema, table, t, files, name, keys)
}

func (InnoDB) Truncate(ctx context.Context, conn *sqlx.Conn, schema, table string) error {
	q, err := dbhelper.TruncateTable(ctx, conn, schema, table)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, q)
	}
	return nil
}

func (InnoDB) Position(ctx context.Context, conn *sqlx.Conn, plugin string) (wire.Applied, error) {
	seq, gtid, q, err := dbhelper.ReplPluginPosition(ctx, conn, StagingSchema, plugin)
	if err != nil {
		return wire.Applied{}, fmt.Errorf("%w (%s)", err, q)
	}
	return wire.Applied{Seq: seq, GTID: gtid}, nil
}

func (InnoDB) SavePosition(ctx context.Context, conn *sqlx.Conn, plugin string, a wire.Applied) error {
	q, err := dbhelper.SaveReplPluginPosition(ctx, conn, StagingSchema, plugin, a.Seq, a.GTID)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, q)
	}
	return nil
}
