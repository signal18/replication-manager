# The CDC core, its destinations and the analytic child

The ColumnStore child (#2002, #2015) carries a parent's tables into an engine that native
replication cannot feed, with a plugin that reads the parent's binlog. That plugin's code is now
the **CDC core**, a library shared by every **destination plugin**: one plugin per destination,
`replplugin-<name>`, each built from the core and its own destination code.

- The core is the Go module `github.com/signal18/replication-manager/cluster/replplugin/cdc`
  (its own `go.mod`), so a plugin kept in another repository imports it at a tag
  (`cluster/replplugin/cdc/vX.Y.Z`, with the root module at the matching tag).
- `cluster/replplugin/plugins/columnstore` is the ColumnStore destination on it, public.
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

A destination plugin brings two things only, the `cdc.Destination` interface:

1. its table, made from the source's table described on the target (`TableDesc`: options,
   columns, primary key), or a refusal with the reason;
2. the apply of a checkpoint's table (`cdc.ApplyDeleteThenLoad` for a MariaDB engine). Its
   `Session()` names the statements its sessions run first. An engine without row-level update
   or delete takes an UPDATE or DELETE statement whole only when the session's binlog format is
   not ROW: `dbhelper.SessionNoBinlogMixed`, `sql_log_bin=0` and `binlog_format=MIXED`. Such a
   copy is not binlogged, so its child has one database (manifest `singleServer`).

## The accumulation (InnoDB staging)

`replication-plugin-merge-interval` seconds is the client's choice, per child, in the GUI. The
default 0 exists only so that an unset key works.

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
  of 1000 explicit keys) and inserts the staged rows (`INSERT ... SELECT`). Only then is the
  staging purged, both tables in one InnoDB transaction.

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
- its default image;
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
5. the plugin runs with `-create`. Each table it leaves as it is gets **WARN0340** in the schema
   domain, raised every tick from `analytic.skipped.json`. Those tables keep replicating natively.
6. the channel's `replicate_ignore_table` is set to the tables made, live and in the
   configuration bundle for the next start (`replPluginReplicateIgnore`);
7. the State is written (restore done, the tables made) and the TOML enabled. The next tick
   starts the plugin.

A failure stops the reseed with the channel stopped and its state (WARN0252): a table made by
the destination must never receive native row events.

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
- `cluster/replplugin/plugins/columnstore/main.go`: the ColumnStore destination.
- `cluster/replplugin/wire/wire.go`: the contract (wire 2, status, create results, manifest).
- `cluster/cluster_replplugin.go`: the supervisor, the states, the manifest and the plan gate.
- `cluster/cluster_repl_source.go`: the source and its checks.
- `cluster/cluster_analytic_child.go`: the analytic child's seed, tags, states, settings.
- `utils/dbhelper/schema_repl_staging.go`: the SQL (MariaDB; PostgreSQL has no storage engine
  to choose, `ErrNoStorageEngine`).
- `regtest/test_replication_plugin_child.go`: `testColumnstoreChildCDC` and
  `testAnalyticChildCDC` (the destination named by `REPMAN_REGTEST_ANALYTIC_DESTINATION`).
