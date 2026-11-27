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

import "github.com/signal18/replication-manager/cluster/replplugin/cdc"

func main() { cdc.Main(cdc.InnoDB{}) }
