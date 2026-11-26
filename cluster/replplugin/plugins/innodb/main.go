// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// replplugin-innodb is the InnoDB destination of the CDC core (cluster/replplugin/cdc): the
// tables and the denormalized tables stay InnoDB on the target. InnoDB is transactional: each
// checkpoint is applied in one transaction that also records its position on the target
// (cdc.Positioner), so a crash never applies a checkpoint twice nor loses one. Its first use is
// the denormalize mode (CDC_CORE.md "Denormalize"): a denormalized table on an analytic child.
// The apply runs with the binlog on: the child's own replicas follow the denormalized table.
package main

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/cluster/replplugin/cdc"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

type innodb struct{}

func (innodb) Name() string                { return "innodb" }
func (innodb) Refuse(cdc.TableDesc) string { return "" }
func (innodb) Session() []string           { return nil }

// Create: the table the target holds is already InnoDB.
func (innodb) Create(context.Context, *sqlx.Conn, cdc.TableDesc) error { return nil }

func (innodb) Apply(ctx context.Context, conn *sqlx.Conn, schema, table string, t wire.TableFiles, files map[string][]byte, name string, keys func([][]interface{}) error) (string, error) {
	return cdc.ApplyDeleteThenLoad(ctx, conn, schema, table, t, files, name, keys)
}

func (innodb) Truncate(ctx context.Context, conn *sqlx.Conn, schema, table string) error {
	q, err := dbhelper.TruncateTable(ctx, conn, schema, table)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, q)
	}
	return nil
}

func (innodb) Position(ctx context.Context, conn *sqlx.Conn, plugin string) (wire.Applied, error) {
	seq, gtid, q, err := dbhelper.ReplPluginPosition(ctx, conn, cdc.StagingSchema, plugin)
	if err != nil {
		return wire.Applied{}, fmt.Errorf("%w (%s)", err, q)
	}
	return wire.Applied{Seq: seq, GTID: gtid}, nil
}

func (innodb) SavePosition(ctx context.Context, conn *sqlx.Conn, plugin string, a wire.Applied) error {
	q, err := dbhelper.SaveReplPluginPosition(ctx, conn, cdc.StagingSchema, plugin, a.Seq, a.GTID)
	if err != nil {
		return fmt.Errorf("%w (%s)", err, q)
	}
	return nil
}

func main() { cdc.Main(innodb{}) }
