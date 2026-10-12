# Storage engine switch with ALTER TABLE (#1999)

A generic tool, on any MariaDB/MySQL cluster: move tables from one storage engine to
another with `ALTER TABLE … ENGINE=`. Built for migration testing on a child cluster
(#1997, `CHILD_CLUSTERS.md`): the child replicates the parent's writes live, its tables are
switched, and the writes keep arriving on the new engine.

## Surface

| Where | What |
| --- | --- |
| `POST /api/clusters/{c}/actions/alter-engine` | `{engineFrom, engineTo, schema, confirm}`: every user table on `engineFrom` (optionally of one schema) |
| `POST /api/clusters/{c}/schema/{s}/{t}/actions/alter-engine/{engine}` | `{confirm}`: one table, whatever its engine |
| ACL | rule `/actions/alter-engine` → `db-optimize` (a table rebuild like OPTIMIZE); more specific than `/schema` (cluster-sharding), which still matches by fallback |
| MCP | `cluster-alter-engine`, `cluster-alter-table-engine`, calling the cluster functions directly behind the same ACL paths (`{schema}`, `{table}`, `{engine}` placeholders) |
| GUI | Schema tab: Switch Storage Engine button, and the Engine cell of each table; one modal (`EngineSwitchModal`), plan first, Run after a Check |

## Plan (`Cluster.PlanEngineSwitch`)

On the current master:
- `engineTo` must be available: `information_schema.ENGINES` SUPPORT YES or DEFAULT
  (`dbhelper.GetEngineSupport`); otherwise refused, naming the configurator tag that
  installs it.
- The tables: `dbhelper.GetTablesByEngine` (user schemas only: mysql, sys,
  information_schema, performance_schema and replication_manager_schema excluded), with
  their size and whether a foreign key involves them (declared on them or referencing them).
- Skipped with the reason: a table already on the target; a table with a foreign key when
  the target is not InnoDB (only InnoDB enforces foreign keys).
- Tables of any other engine are not listed at all: Aria, MEMORY, MyISAM are never touched.

## Run (`Cluster.RunEngineSwitch`)

- One switch per cluster at a time; refused when the master changed since the plan.
- A dedicated connection to the master (`GetNewDBConn`, never the monitor's pool), binlog on:
  the replicas apply the same ALTER.
- One `dbhelper.AlterTableEngine` per table (quoted identifier, engine name checked against
  `^[A-Za-z0-9_]+$`), in the background, stopping at the first error.
- Progress on the `Cluster` (`engineSwitch`), raised every tick: CINF0015 while it runs,
  WARN0240 with the table and the error once it stopped (until the next switch).
- PostgreSQL: `dbhelper.ErrNoStorageEngine` (no engine per table, T23).

## Limits

- An ALTER ENGINE copies the table and blocks its writes for the duration; the plan shows
  the sizes. One table is bounded by `db-servers-read-timeout` (3600 s by default) on the
  dedicated connection.
- On a cluster replicating from a parent, the parent's writes to a table being altered wait
  for the ALTER: the channel lags meanwhile.

## Regtest

`testAlterEngine` (`regtest/test_engine_switch.go`): a child cluster of the tested cluster,
a test schema with two InnoDB tables linked by a foreign key, one InnoDB table alone and one
MEMORY table; the InnoDB to Aria plan holds the lone table only and skips the two with the
foreign key; run, the lone table is Aria on the child's master and replica, the others keep
their engine, the child's channel from the parent keeps running.
