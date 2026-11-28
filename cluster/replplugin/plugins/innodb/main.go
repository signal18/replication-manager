// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// repl-mariadb-mariadb-cdc (binary also replplugin-innodb) is the MariaDB destination of the
// CDC core (cluster/replplugin/cdc) with target_engine InnoDB: in table mode the source's
// tables copied as they are, in index mode the denormalized tables, each a plain InnoDB table,
// partitioned when the index says so (partition_column, partition_period), applied directly
// (no accumulation unless a merge interval is set). InnoDB is transactional: each checkpoint is
// applied in one transaction that also records its position on the target (cdc.Positioner), so
// a crash never applies a checkpoint twice nor loses one. The apply runs with the binlog on: the
// child's own replicas follow the tables.
package main

import "github.com/signal18/replication-manager/cluster/replplugin/cdc"

func main() { cdc.Main(cdc.InnoDB{}) }
