# The CDC core, its destinations and the analytic child

The ColumnStore child (#2002, #2015) carries a parent's tables into an engine that native
replication cannot feed, with a plugin that reads the parent's binlog. That plugin's code is now
the **CDC core**, a library shared by every **destination plugin**: one plugin per destination,
`replplugin-<name>`, each built from the core and its own destination code.

- The core is the Go module `github.com/signal18/replication-manager/cluster/replplugin/cdc`
  (its own `go.mod`), so a plugin kept in another repository imports it at a tag
  (`cluster/replplugin/cdc/vX.Y.Z`, with the root module at the matching tag).
- `cluster/replplugin/plugins/columnstore` is the ColumnStore destination on it, public.
- `cluster/replplugin/plugins/innodb` is the InnoDB destination on it, public: the copies and the
  denormalized tables stay InnoDB, applied in transactions that keep their position.
- Other destinations are external plugins installed next to replication-manager's plugins, with
  a manifest (below). A destination of the enterprise tier is refused unless the seat's plan
  grants the enterprise plugins (`EnterprisePluginsPlan`, the archive plugins' gate).

## What the core does

- **Capture**: go-mysql canal reads the source's binlog from a GTID. Per period it writes a tar.gz
  checkpoint, closed at a transaction boundary at `replication-plugin-checkpoint-interval` seconds
  or 64 MB. Per table it holds the keys to delete and the rows to load, in `LOAD DATA`'s format.
  - An update is a delete of its key and an insert of its new row.
  - An insert then a delete in one period cancel out.
  - The keys of the loaded rows are in the deletes, so a checkpoint applied twice gives the same
    table.
  - The capture pauses while the checkpoints not applied exceed `max_pending_mb` (the bound, F3).
    The pause is told without the capture's lock (it holds it while it waits), and a status
    line keeps coming while it lasts.
  - A period closes only at a transaction's end: a GTID event closes the previous transaction,
    an XID or not (a COMMIT of a non-transactional table). One source transaction is held whole
    in memory: a transaction of more row events than `max_pending_mb` (counted per transaction,
    from its GTID event) stops the plugin with that reason (WARN0242) instead of growing without
    bound (F4).
  - The pause shows in the plugin's status description, so in CINF0017 ("paused: N MB of
    checkpoints wait to be applied").
  - A crash between a checkpoint's file and the State that records it: at its start the plugin
    numbers its next checkpoint past every number used (State, applied, files waiting), so an
    applied checkpoint is never shadowed by a new one of the same number.
  - A plugin binary of wire 1 is refused by replication-manager's wire check (WARN0242 with the
    reason): rebuild or upgrade the plugin with this release.
  - A `RENAME TABLE` or `DROP TABLE` on the source is a DDL like any other: WARN0341, the copy
    keeps its rows.
- **Apply** (wire 2): the plugin applies its checkpoints itself, in order: to the destination, or
  to the staging (below). The last one applied is in `<name>.applied.json`, and its file is then
  removed. A failure is retried; its text is in the status (WARN0243).
- **Position**: the State (`<name>.state.json`, the GTID and the last checkpoint written) and the
  applied file. A restart reads again from the State's GTID, and a checkpoint applied again is
  harmless.
- **Status**: one JSON line per period (`wire.Status`) with the GTID captured and applied, the lag,
  the counts, the apply error and the last merge. replication-manager raises the states from it:
  - CINF0017 while the plugin runs;
  - WARN0242 while it is stopped (restart with backoff);
  - WARN0243 while an apply fails;
  - CINF0030 for the merges;
  - **WARN0341** for a DDL on the source, which no checkpoint carries: the destination keeps the
    old structure. Measured: after an `ADD COLUMN` on the parent, the next `LOAD DATA` into the
    old table silently dropped the new field. The state is what tells the operator.
- **Create** (`replplugin-<name> -create`): at an analytic child's seed, each table of the rules
  on the target, as restored from the parent's backup, is examined. The destination refuses it
  with the reason, or makes it its own. One `wire.CreateResult` line per table.

A destination plugin brings two things only, the `cdc.Destination` interface, and two optional
ones (a destination without them works as before):

- `cdc.Truncater`: empties a table its own way, for an init of the denormalize mode (without it,
  the core deletes every row);
- `cdc.Positioner`: a transactional destination keeps the last checkpoint applied in its own
  transaction. The core then applies each checkpoint in one transaction that ends with
  `SavePosition`, and at start takes `Position` when it is past `applied.json` (a crash between
  the commit and that file). The InnoDB destination keeps it in
  `replication_manager_staging.replplugin_position`, one row per plugin.

The two things:

1. its table, made from the source's table described on the target (`TableDesc`: options,
   columns, primary key), or a refusal with the reason;
2. the apply of a checkpoint's table (`cdc.ApplyDeleteThenLoad` for a MariaDB engine). Its
   `Session()` names the statements its sessions run first. An engine without row-level update
   or delete takes an UPDATE or DELETE statement whole only when the session's binlog format is
   not ROW: `dbhelper.SessionNoBinlogMixed`, `sql_log_bin=0` and `binlog_format=MIXED`. Such a
   copy is not binlogged, so its child has one database (manifest `singleServer`).

## The accumulation (InnoDB staging)

`replication-plugin-merge-interval` seconds is the client's choice, per child, in the GUI. The
default 0 exists only so that an unset key works. It is capped at 86400 (one day): the staging
grows on the child's disk until its merge (F3).

- **0**: each checkpoint goes to the destination at once.
- **More**: each checkpoint goes at once to an InnoDB staging on the child, in the schema
  `replication_manager_staging`. Each carried table has two staging tables:
  - `<schema>__<table>__r`: the rows to put, with the table's structure;
  - `<schema>__<table>__d`: every key deleted or replaced, as its primary key.

  The staging is written in one InnoDB transaction per checkpoint:
  - a later change of a key overwrites the earlier one;
  - a delete removes the key's row and records the key;
  - a checkpoint staged twice gives the same staging.

  Every merge interval, per table, one destination transaction deletes the staged keys (batches
  of 1000 explicit keys) and inserts the staged rows (`INSERT ... SELECT` of the columns both
  tables have, by name: a destination changed by hand after a DDL keeps merging). Only then is
  the staging purged, both tables in one InnoDB transaction. The tables merged are the carried
  ones and every table the staging holds (its rows tables name it in their comment), so rows
  staged for a table no longer carried are not lost. The apply's connections are never pooled:
  each one leaves with its session and any transaction.

  There is no transaction across InnoDB and the destination, so a merge stopped anywhere is run
  again whole and gives the same table. The last merge is `<name>.merged.json`, its state
  CINF0030. A merge interval set back to 0 merges the staging once before the checkpoints go to
  the destination.
- The keys are deleted by explicit key batches, never by `DELETE ... WHERE (k) IN (SELECT k FROM
  staging)`. On a columnar engine without indexes, that cross-engine statement crashed the whole
  MariaDB server (SIGSEGV in the engine's `delete_row`, the multi-table delete path).
- Why a staging: on a destination without indexes, each batch of keys deleted scans the table.
  Applying each checkpoint costs about the table's size times the change rate. Staging costs
  InnoDB lookups by primary key, and the merge scans the table once per 1000 keys. Measured on a
  columnar engine, 4 CPU / 8 GB containers, ~190 transactions/s:

  | table | each checkpoint applied (2 s checkpoints) | staged checkpoint | merge |
  |---|---|---|---|
  | 325k rows | p50 298 ms, max 406 ms | 20-90 ms | 26.5k keys + 23k rows in 2.9 s |
  | 5M rows | p50 1.3 s, p90 1.8 s, 2.3 cores of 4 on average | 20 ms, ~0 CPU | 36k keys + 24k rows in 6.0 s |

  The GUI suggests 3600 (one hour) when a carried table has 3 million rows or more.
- Crash tests on that engine, each followed by equal row checksums with the parent:
  - the child killed (`kill -9`) in the middle of a checkpoint's apply: the checkpoint was
    applied again;
  - the child killed in the middle of a merge, inside the destination's transaction: the table
    was exactly as before the merge and the staging intact, and the merge run again gave the
    parent's rows.

## The source

Setting `replication-plugin-source` (`cluster/cluster_repl_source.go`):

- `master` (the default): the parent's current master. After a failover or a switchover the
  TOML is rewritten to the new master, and the plugin restarts from its State GTID there (a
  MariaDB cluster's GTIDs are the same on every server of its replication).
- `host:port`: a dedicated replica of the parent. When it is lost (down, removed), the plugin is
  paused with **ERR00131** and never moved to another server: the operator points the setting
  elsewhere.
- The source is checked every tick before the plugin runs. It is refused with **ERR00130** (HA
  domain of the child, ERROR: the copies stop until someone acts) when:
  - `log_bin` is OFF;
  - `binlog_format` is not ROW;
  - `binlog_row_image` is not FULL (a copy needs every column);
  - `log_slave_updates` is OFF on a replica;
  - there is no GTID.

  Nothing is changed on the source (F1).

## The analytic child

A child cluster with `replication-plugin-destination` (a plugin name; empty means none, the off
switch) and `replication-plugin-tables` (`schema.table` or `schema.*`, comma separated, the
operator's choice). It is created by `create-child` with `destination` and `tables`. The plugin's
manifest (`replplugin-<name>.manifest.json`, `wire.PluginManifest`) gives:

- its tier and licence;
- the configurator tags its servers need (added to the child);
- its default image: `{version}` in it stands for the parent master's release (an engine
  package that must match its server release exactly), and `imageVersions` lists the releases
  it exists for; the create plan refuses a parent release without image, with the list. The
  release is compared as `X.Y.Z`, without a suffix (`11.8.10`, never `11.8.10-MariaDB-log`),
  and the image is resolved when the child is created only: a later upgrade of the parent does
  not change the child's image;
- `singleServer`, a child of one database.

The plan is refused when the plugin is missing or the plan does not grant its tier. The
supervisor refuses such a plugin with **ERR00132**, and sets `REPMAN_REPLPLUGIN_ENTERPRISE_PLAN=1`
in the plugin's environment only for a granting plan.

At the seed (`cluster_staging.go`, `prepareAnalyticChild`), after the parent's backup is restored
on the child master and **before its channel from the parent starts**:

1. the plugin is checked (installed, plan);
2. the previous plugin is stopped, and its State, checkpoints, merges and staging are removed;
3. the source is resolved and checked;
4. the start position is read: the child master's `gtid_slave_pos`, where the channel resumes;
5. the plugin runs with `-create`, in the seed's goroutine (never on the monitor tick), its
   results read line by line; **CINF0031** says so while it runs (a conversion copies its table).
   Each table it leaves as it is gets **WARN0340** in the schema domain, raised every tick from
   `analytic.skipped.json` (an unreadable file raises WARN0340 too). Those tables keep replicating
   natively. If `-create` fails, the tables it made before are added to the channel's ignore list
   all the same, and the channel stays stopped with the reseed's failure.
6. the channel's `replicate_ignore_table` is set to the tables made, live and in the
   configuration bundle for the next start (`replPluginReplicateIgnore`);
7. the State is written (restore done, the tables made) and the TOML enabled. The next tick
   starts the plugin.

A failure stops the reseed with the channel stopped and its state (WARN0252): a table made by
the destination must never receive native row events.

## Denormalize

The analytic tables are rarely the source's tables as they are: a report joins an entity with
its children and its dimensions every time. The denormalize mode keeps **the result of a
denormalizing query** up to date on the child, in a **denormalized table**. Free, in the core,
toward every destination.

**Configuration.** An **index** is one denormalization: `[[<cluster>.replication-plugin-index]]`
in the cluster configuration, with `name`, `sql` (the denormalizing query, run on the source),
`slave-schema` and `slave-table` (the denormalized table on the child, in its own schema) and
`max-keys-per-batch` (0: 10000). API: `replication-plugin-index-list`,
`replication-plugin-index-actions/put`, `.../remove/{indexName}` (ACL `GrantClusterSettings`);
GUI: the Analytic Child settings. The cluster writes the list into the plugin TOML (`[[index]]`,
`wire.Index`) and the plugin restarts with it. Nothing is given per source table:

- the query's **source tables** and the result columns that hold their columns come from the
  query's result metadata (the query run with `LIMIT 0` on the source: each column's original
  schema, table and column), and their **primary keys** from `information_schema`;
- the query must return the primary key columns of every table it reads, each under one name:
  together they are the denormalized table's **primary key**, and each source table's columns are
  indexed there (what an unpack looks rows up by). A missing column, a table joined twice, two
  result columns of one name are refused when the plugin starts: it stops with the reason
  (WARN0242) and restarts with its backoff until the index is fixed or removed;
- inner joins: a primary key column is never NULL.

**Init.** Once per index (`applied.json` lists the indexes done by name and a checksum of their
query and table, so a changed query runs its init again; the status's `indexesReady` names them):

1. the denormalized table is made from the description of the empty query result
   (`CREATE TABLE ... AS SELECT * FROM (<sql>) AS d WHERE 1=0`, then its primary key and indexes),
   and the destination makes it its own (`Create`); an existing table of the same columns is
   emptied instead (`Truncater`);
2. the query runs on the source in one `START TRANSACTION WITH CONSISTENT SNAPSHOT`, its rows
   applied by parts of 10000.

**Capture: the micro_checkpoint_pack.** For the source tables of the indexes, the capture keeps
the primary keys touched, nothing else, per source table: the keys to replace and the keys to
delete. A deleted key stays deleted; inserted again it is back to replace. An update that changes
the primary key deletes the old key. The pack goes into the checkpoint (`wire.Manifest.Packs`),
bounded as every checkpoint (64 MB of row events per period, `max_pending_mb`).

**Unpack: the micro checkpoint.** At apply, for each index and each batch of packed keys:

1. the denormalized rows whose source-table primary key columns hold a packed key are looked up
   on the child (and in the accumulation's rows when there is one): their keys are deleted;
2. the denormalizing query restricted to the same keys runs on the source, one branch per source
   table joined by `UNION` (`SELECT * FROM (<sql>) AS d WHERE (d.k1, d.k2) IN (...) UNION ...`):
   MariaDB pushes each branch's condition into the derived table, on that table's primary key; an
   `OR` across the joined tables would read the whole join. The rows it gives are loaded, their
   keys deleted first.

Together they are the micro checkpoint, a checkpoint's table like any other: the destination, the
accumulation and the merge interval apply it unchanged. A customer renamed rewrites the rows of
its orders; an item deleted removes its lines, inserted again it brings them back; an order line
moved to another item is rewritten with it. The keys are split across batches, never dropped
(`max-keys-per-batch`, and 60000 placeholders at most per query).

The unpack reads the source as it is when it runs, past the pack: a change after the pack is read
then, and again at its own pack, which gives the same rows. So the init needs no GTID, and packs
captured before it unpack after it on the current source.

**Where the design moved, and why.**

- No per-source-table mapping query: the primary key columns of every source table are in the
  denormalized table, so the old owner of a changed row is found there and the new one by the
  restricted query.
- The restricted query is a `UNION` of one branch per source table, not an `OR`, for the index use
  above.
- The init streams the query on one consistent snapshot instead of key ranges: one consistent read,
  no range scan per step, the same snapshot cost on the source.
- The key of the denormalized table is derived (every source table's primary key), not a `key`
  setting.
- The InnoDB destination comes first (public); a columnar destination takes the same micro
  checkpoints through its plugin.

Version 1 builds the denormalized table on the existing analytic child, seeded from the parent as
any child; its copied tables (`replication-plugin-tables`) are carried as before.

## Capture on MariaDB 11.4.13 and later (go-mysql)

Since 11.4.13 / 11.8.9, `binlog_legacy_event_pos=OFF` is the default, so the events inside a
transaction carry `end_log_pos 0`. go-mysql v1.8.0's canal dropped every event with a zero
position as a fake rotate. The plugin then captured **no row** while its GTID went forward: the
copy stayed stale forever, with no error. The core now uses go-mysql v1.16.0, which tells a fake
rotate by its zero timestamp. Every ColumnStore child of such a parent needs the new plugin.

## ColumnStore: open source, community-maintained

The ColumnStore destination stays open source, maintained as a community destination:

- one regtest (`testColumnstoreChildCDC`), on the MariaDB LTS release we already test;
- no ColumnStore-specific features beyond what the core gives every destination;
- fixes as they come;
- guaranteed support only under a support contract.

## Files

- `cluster/replplugin/cdc/`: the core (capture `capture.go`, checkpoints `checkpoint.go`, apply
  `apply.go`, staging `staging.go`, destination interface `destination.go`, create `create.go`).
- `cluster/replplugin/cdc/denormalize.go`: the denormalize mode (indexes, pack, unpack, init).
- `cluster/replplugin/plugins/columnstore/main.go`: the ColumnStore destination.
- `cluster/replplugin/plugins/innodb/main.go`: the InnoDB destination.
- `config/replplugin_index.go`, `cluster/cluster_replplugin_index.go`,
  `server/api_replplugin_index.go`: the index list, its save, its API.
- `utils/dbhelper/schema_denormalize.go`: the denormalize SQL and the destination position
  (MariaDB and MySQL; a PostgreSQL server never reaches it, T23).
- `cluster/replplugin/wire/wire.go`: the contract (wire 2, status, create results, manifest).
- `cluster/cluster_replplugin.go`: the supervisor, the states, the manifest and the plan gate.
- `cluster/cluster_repl_source.go`: the source and its checks.
- `cluster/cluster_analytic_child.go`: the analytic child's seed, tags, states, settings.
- `utils/dbhelper/schema_repl_staging.go`: the SQL (MariaDB; PostgreSQL has no storage engine
  to choose, `ErrNoStorageEngine`).
- `regtest/test_replication_plugin_child.go`: `testColumnstoreChildCDC` and
  `testAnalyticChildCDC` (the destination named by `REPMAN_REGTEST_ANALYTIC_DESTINATION`).
- `regtest/test_replication_plugin_denormalize.go`: `testAnalyticChildDenormalize`, TPC-C
  (order_line ⋈ orders ⋈ customer ⋈ item), checksums `COUNT(*)` and
  `BIT_XOR(CRC32(CONCAT_WS(...)))` of the query on the parent against the denormalized table, after
  the init, a change on each joined table, a deleted item inserted again and a load.
