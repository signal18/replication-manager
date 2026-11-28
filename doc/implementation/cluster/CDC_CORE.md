# The CDC core, its destinations and the analytic child

The ColumnStore child (#2002, #2015) carries a parent's tables into an engine that native
replication cannot feed, with a plugin that reads the parent's binlog. That plugin's code is now
the **CDC core**, a library shared by every **destination plugin**: one plugin per destination,
each built from the core and its own destination code. Each is a replication plugin of the
replication interface (`REPLICATION_INTERFACE.md`, step 4), named `repl-<source>-<target>-cdc`:
`repl-mariadb-duckdb-cdc`, `repl-mariadb-columnstore-cdc`, `repl-mariadb-mariadb-cdc` (InnoDB),
`repl-postgresql-mariadb-cdc`. Its binary is named after it, or as before `replplugin-<name>`
(`replplugin-duckdb`, ...): replication-manager looks for the new name first, then the old one,
and the short names the settings and TOMLs hold (`duckdb`, `columnstore`, `innodb`,
`postgresql`) stay valid as aliases.

- The core is the Go module `github.com/signal18/replication-manager/cluster/replplugin/cdc`
  (its own `go.mod`), so a plugin kept in another repository imports it at a tag
  (`cluster/replplugin/cdc/vX.Y.Z`, with the root module at the matching tag).
- `cluster/replplugin/plugins/columnstore` is the ColumnStore destination on it, public.
- `cluster/replplugin/plugins/innodb` is `repl-mariadb-mariadb-cdc` (binary also
  `replplugin-innodb`), the MariaDB destination on it, public: `target_engine` InnoDB, the copies
  and the denormalized tables plain InnoDB tables, partitioned when the index says so, applied
  directly in transactions that keep their position.
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

## The stream and its transport (the seam)

The core has three parts and one seam: **capture** (the init's row stream, the binlog delta) →
**micro checkpoint** (the tar.gz: its manifest, sequence and GTID, the keys to delete, the rows to
load) → **transport** → **apply** (the destination). The apply reads nothing of the capture but what
the transport gives, in sequence order (`cdc.Transport`, `transport.go`):

- `Create(seq)` opens a checkpoint as a **stream** (`io.Writer`), never a file path: `Close` makes it
  visible whole, `Abort` drops it, and it waits while the transport is over its bound
  (`max_pending_mb`: the capture pauses, it never drops a change). An init streams each part into it
  while it reads the dump's chunks or the SELECT's ranges, a part held in memory up to its bound
  (10000 rows or 16 MB), so a transport to an object store needs no local spool;
- `Pending(after)`, `Open(seq)`, `Ack(seq)` (applied: the transport may free it), `PendingBytes()`,
  `Clear()`.

The local-disk queue (`<name>.checkpoints`, a temporary file renamed whole) is the only transport.
**The S3 stream plugs in here** (a later step, not coded): the capturing replication-manager
publishes each element to a bucket, any other replication-manager reads the stream in order and
applies it with its own destination plugin and its own position, so a CDC travels across
replication-manager clusters. The apply's own configuration (which index, where its tables go,
how its destination is partitioned) is the consumer's.

The transport carries **one ordered sequence of elements**, all with the same sequence numbers:

1. **`<index>-schema`** (`wire.SchemaElement`, `Manifest.Schema`), first for each destination table:
   its columns (MariaDB types: a source column's own type from `information_schema`, an expression's
   from its result type), its key, the indexes an unpack looks rows up by, the partitioning, the
   earliest value of the partition column, the source GTID of the init that follows. The apply makes
   or empties the destination from it (never from the source): a table of the same columns is
   emptied; a denormalized table of other columns (its query changed) is dropped and made again; a
   table-mode destination of other columns is refused, never dropped.
2. **The init's parts** (`wire.InitPart`, `Manifest.Init`): the rows to load and their keys to
   delete first, so a part applied twice gives the same table. The last part records the init's GTID
   (`applied.json` `inits`); an index-mode init then marks its index ready.
3. **The delta**: the micro checkpoints of the binlog.
4. **A DDL element** (`wire.DDLElement`, `Manifest.DDL`): a DDL on a source table of an index closes
   the open micro checkpoint and is published as its own element (statement, schema, the tables it
   touched, its GTID); none mixes rows from before and after it. The apply hands it to the
   destination (`cdc.DDLMapper`), which maps it to its own DDL; a destination that does not, as
   `repl-mariadb-mariadb-cdc` today, **pauses the index** (`applied.json` `paused`, its parts no longer
   applied, the reason in the apply error: WARN0243) until a rebuild. The `-schema` element is the first
   DDL of the stream. A DDL on a table of the rules only keeps the old behaviour (WARN0341).

## Modes, init and the GTID start

An index (`[[index]]` in the plugin TOML, `[[<cluster>.replication-plugin-index]]` in the cluster
configuration) has two modes, and every destination takes both:

- **index** (`mode` empty or `index`): the denormalizing query `sql` (below, "Denormalize").
- **table** (`mode = "table"`): the tables of `tables` (`["logs.access", "logs.audit"]`) copied as they
  are, each into `<slave_schema>.<its name>` (`slave_table` renames a single table), keyed on its
  primary key. Large log tables are the first case. Refused when the destination is its own source,
  the table has no primary key, a generated or BIT column. Its delta is the binlog's row images (copy
  mode: `binlog_row_image=FULL`).

The init is **one streamed SQL**: rows flowing into the loader, which does not know their source.

- `init = "select"` (the default, the only one of the index mode): `@@gtid_current_pos` read, then
  `START TRANSACTION WITH CONSISTENT SNAPSHOT`; in table mode the table read by ranges of its primary
  key (`range_step` rows, keyset `a > ? OR (a = ? AND b > ?)`), in index mode the query streamed. The
  GTID is read **before** the snapshot opens: the transactions between the two are in the rows and in
  the delta, which applies them again (whole row images); none is missed.
- `init = "backup"` (table mode): the parent's logical backup a child is seeded from
  (`ParentLogicalBackup`: the mydumper folder, or the mysqldump `splitdump` folder), when it is a
  split dump; replication-manager writes its folder in `backup_dir`. The table's data files are read
  chunk by chunk, gzip or not (not zstd); the GTID is the backup's metadata's. No load on the source. The earliest partition value costs a first pass over the
  table's files.

The inits run in the capture, **before the binlog is read**, and publish into the transport; the
delta follows them in the stream. Where the delta starts:

- a feed with a position (State): its GTID. A **backup older than it is refused** (the transactions
  between the two would be lost): the plugin stops with the reason (WARN0242); take a new backup or
  init by select. The comparison is per domain, on the domains the backup knows: an analytic child's
  position (its `gtid_slave_pos`) also carries the child's own domain, which the source never had;
  no domain in common is refused;
- a feed without one: `start_gtid`, else the oldest backup an init reads, else the source's
  `@@gtid_current_pos` (read before any init's snapshot).

On the apply, the delta of a table-mode table is not applied before its init is (the init holds it),
nor when the checkpoint's GTID is within the init's (per domain, on the init's domains:
`coversKnownDomains`); after that
it goes to the destination. An index's packs are unpacked only once its init is applied. A restart
during an init publishes it again from its `-schema` element (the State's `inits` lists those
published whole); nothing is resumed halfway.

Nothing is written on the source (F1): the reads are `SELECT`s and a consistent snapshot; the
source's links are never touched (T27). The source is `replication-plugin-source` (a replica of the
parent, or its master).

## Partitioning

Partitioning is a property of the index, required by the destination: `partition_column` (a DATE,
DATETIME or TIMESTAMP column of the result) and `partition_period` (`day`, `week` from Monday, or
`month`), `partitions_ahead` (0: 3). A destination that needs it (InnoDB here, S3 next) takes it; an
analytic one does not.

- The destination is `PARTITION BY RANGE COLUMNS(col)` on a DATE or DATETIME, `RANGE
  (UNIX_TIMESTAMP(col))` on a TIMESTAMP (RANGE COLUMNS does not take one), one partition per period
  named `p<YYYYMMDD of its start>`, from the period of the earliest value of the init to the
  horizon. The first partition takes every older value and NULL.
- The partition column joins the primary key (MariaDB requires it, error 1503): a column outside the
  source's primary key must be NOT NULL. In table mode the capture keys the copy's rows by that key,
  so a delete reaches one partition.
- **Ahead of time**: every apply round keeps `partitions_ahead` periods after the current one; before
  a checkpoint's transaction (a DDL commits) the partitions its latest row needs are added. At most
  400 at once and 8000 in all: a value far in the future is refused with the reason, never thousands
  of empty partitions (F3).
- **Routing**: every row goes to its partition by the column's value, at the init (the part split by
  partition) as for the delta (MariaDB's own routing, the partitions ensured first).
- **The pool** (`pool.go`, `replication-plugin-threads`, `threads` in the TOML, default 2, at most 64):
  the jobs of one round, one per partition, on that many workers, never two on one partition, each
  under its deadline (10 minutes for an init part); a failed partition is reported by name and the
  others go on. The init's apply uses it; the S3 destination's partition exchange is its next user.

The InnoDB destination writes its rows with multi-row `INSERT`s after the keys' `DELETE`
(`cdc.ApplyDeleteThenInsert`), never `LOAD DATA LOCAL`: replication-manager's hardening turns
`local_infile` off on the target (seen on dev3, error 4166), and an InnoDB table takes inserts well.
It takes `target_engine` InnoDB (or unset); another engine is refused with the
reason (Aria later: its tables are not transactional, the position would not commit with the rows).

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
switch; `duckdb` and `repl-mariadb-duckdb-cdc` name the same plugin) and
`replication-plugin-tables` (`schema.table` or `schema.*`, comma separated, the operator's
choice). It is created by `create-child` with `destination` and `tables`. The plugin's manifest
(`<binary>.manifest.json`, `wire.PluginManifest`, and in the same file its replication fields,
`REPLICATION_INTERFACE.md` "The manifest") gives:

- its tier and licence;
- the configurator tags its servers need (added to the child);
- its default image: `{version}` in it stands for the parent master's release (an engine
  package that must match its server release exactly). The release is the parent master's
  `DBVersion` as the monitor parsed it (`utils/version`), written `X.Y.Z` without a suffix
  (`11.8.10`, never `11.8.10-MariaDB-log`). No list of releases is kept: when the registry has
  no tag for the parent's release, the child's provisioning fails on the pull, with its state.
  The image is resolved when the child is created only: a later upgrade of the parent does not
  change the child's image;
- `flavor`, the database flavor the destination exists for, as `utils/version` names it
  (`MariaDB` for the DuckDB engine): a parent master of another flavor is refused at the plan;
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

## The DuckDB child

An analytic child of destination `duckdb`: its chosen tables become MariaDB DuckDB-engine
tables (`ha_duckdb`, MariaDB's own plugin), columnar copies the client queries with plain
MariaDB SQL. The destination is an enterprise plugin, `repl-mariadb-duckdb-cdc` (its binary
`repl-mariadb-duckdb-cdc`, or `replplugin-duckdb` as delivered before), delivered with the
enterprise plugins and gated by its manifest's tier like any other (`EnterprisePluginsPlan`,
ERR00132 otherwise). This repository holds no DuckDB code: everything below is the analytic
child's, the plugin brings its two things (refuse or convert a table, apply a checkpoint).

- **Create**: `create-child` (API, the GUI's Create Child with "Analytic copies", MCP
  `cluster-create-child`) with `destination=duckdb` and `tables`. The manifest gives:
  - the image `ghcr.io/mariadb/duckdb:{version}`, MariaDB's public image (its mariadb-docker CI,
    `Containerfile.plugin` with `PLUGIN=duckdb`): the engine package must match its server
    release, so `{version}` is the parent master's release as the monitor parsed it. No list
    of releases: a release the registry has no tag for fails at the child's pull, and the
    creation stops at its `master` step with WARN0239 naming the image;
  - one database (`singleServer`): the copies are written out of the binlog;
  - the tags `duckdb` and `localinfile`. `duckdb` loads the engine from the configurator; until
    its ruleset exists on the collector (T21), the plugin's `-create` loads it with
    `INSTALL SONAME 'ha_duckdb'` on the child (persisted in `mysql.plugin`).
- **Feed**: native replication cannot feed the engine (error 1031 on every UPDATE or DELETE row
  event): the chosen tables are fed by the CDC core from the parent's binlog, every apply
  session with `sql_log_bin=0` and `binlog_format=MIXED` (`dbhelper.SessionNoBinlogMixed`). The
  channel from the parent ignores the converted tables; the others replicate natively.
- **Refused tables** (WARN0340 with the reason, the table stays InnoDB): not InnoDB, no primary
  key, partitioned, a character set other than utf8mb4/utf8mb3/ascii, a generated, invisible or
  BIT column.
- **Merge interval**: with 0, each checkpoint is applied to the DuckDB tables; on tables of
  millions of rows, 3600 stages them in InnoDB and merges once an hour (the measures above).
- **Denormalize**: an index's denormalized table is made InnoDB from the query, then converted
  by the plugin like a copied table; the init and the micro checkpoints are applied to it as to
  any table of the destination.
- **The parent**: only read. Its binlog is read from `replication-plugin-source`, no channel,
  setting or table of it changes (F1, T27).
- **Off**: the plugin not delivered, or a plan without the enterprise plugins, refuses the
  create plan and stops the feed (ERR00132); an empty `replication-plugin-destination` makes the
  child a plain child at its next seed. Stopping and starting the feed itself is the
  replication interface's (below).

### The feed as a replication link

The feed is a `Replication` of the plugin `repl-mariadb-duckdb-cdc` in the child master's
`server.Replications`, beside its native channel from the parent, as every CDC feed
(`cluster/replication_cdc.go`, the one adapter of every CDC binary): its row (capture and apply
running, GTID captured and applied, lag, errors), its four ends (source the parent's server,
target the child master), `kind` `cdc` and `plugin` `repl-mariadb-duckdb-cdc` in the JSON. Its
verbs are the supervisor's: `Status` (the last status line), `Start` and `Stop` (the TOML's
enabled flag, WARN0352 on a stop), `Rejoin` (resume, or a new initial load); the copy and apply
verbs only when the binary's manifest lists them, every other verb `ErrReplicationNotSupported`.
A feed is never promoted nor repointed by a failover (`CanFailover` false).

`Cluster.AnalyticFeedStatus()` (`cluster/cluster_analytic_feed.go`) reads it through that object
(`CDCReplication.Feed`): the plugin and the link's name, the destination, tables, source as set
and as read, intervals, whether the plugin runs or why it is stopped and when it restarts, the
`-create` running since, the plugin's last status line (GTID captured and applied, lag, counts,
apply error, last merge) and the tables left InnoDB. Read only and cheap (the TOML, the
supervisor's lock briefly, the runner's atomics, one small file), never a database: callable from
any goroutine. The indexes ready (the API's index list, MCP `list-cluster-analytic-indexes`) are
read through the feeds' objects too (`ReplicationPluginIndexesReady`), and MCP
`get-database-replication` returns every replication of a server with its plugin. A child seeded
under one name of the plugin keeps its TOML (`duckdb.toml`) when the destination is set to another
of its names. Tests `TestAnalyticFeedStatus`, `TestDuckDBChildFeedIsAReplication`,
`TestAnalyticPluginPathAcrossNames`.

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
query and table, so a changed query runs its init again; the status's `indexesReady` names them),
through the stream ("The stream and its transport"):

1. the `-schema` element describes the denormalized table: each column of a source table with that
   column's type, an expression with its result's, the primary key and the indexes in the same
   `CREATE` (a server with `innodb_force_primary_key=ON`, which replication-manager's configurator
   sets, refuses a table created without a key, error 1173); the destination makes it its own
   (`Create`), an existing table of the same columns is emptied instead (`Truncater`). The target no
   longer needs the source's tables to make it;
2. the query runs on the source in one `START TRANSACTION WITH CONSISTENT SNAPSHOT`, its rows
   published in parts of 10000.

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
then, and again at its own pack, which gives the same rows. The packs published before the
index's init are skipped (the init's snapshot holds them), those after it unpack on the current
source. The unpack still reads the source and the target at the apply: it does not travel across
replication-manager clusters yet (an open point of the S3 stream).

**Where the design moved, and why.**

- No per-source-table mapping query: the primary key columns of every source table are in the
  denormalized table, so the old owner of a changed row is found there and the new one by the
  restricted query.
- The restricted query is a `UNION` of one branch per source table, not an `OR`, for the index use
  above.
- The index mode's init streams the query on one consistent snapshot instead of key ranges: one
  consistent read, no range scan per step, the same snapshot cost on the source. The table mode reads
  its table by ranges of its primary key in the snapshot.
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
- `cluster/replplugin/cdc/transport.go`: the transport and the local-disk queue.
- `cluster/replplugin/cdc/initstream.go`, `copy.go`, `apply_init.go`: the init stream and its
  sources, the table mode, the apply of the `-schema`, init and DDL elements.
- `cluster/replplugin/cdc/partition.go`, `pool.go`: the partitioning and the pool.
- `utils/dbhelper/schema_cdc_partition.go`, `schema_cdc_init.go`: their SQL (MariaDB and MySQL; a
  PostgreSQL server never reaches it, T23).
- `cluster/replplugin/cdc/denormalize.go`: the denormalize mode (indexes, pack, unpack).
- `cluster/replplugin/cdc/source.go`, `innodb.go`: the capture sources of other engines and the
  InnoDB destination they reuse (`CDC_SOURCES.md`).
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
- `cluster/replication_cdc.go`: the replication interface's adapter of every CDC plugin (names
  and aliases, manifests, the feed's `Replication`, `CDCReplication.Feed`).
- `cluster/cluster_analytic_feed.go`: `AnalyticFeedStatus`, the feed read through its `Replication`.
- `mcp/tools_analytic.go`: the MCP tools of the indexes (the DuckDB child itself: `cluster-create-child`).
- `utils/dbhelper/schema_repl_staging.go`: the SQL (MariaDB; PostgreSQL has no storage engine
  to choose, `ErrNoStorageEngine`).
- `regtest/test_replication_plugin_child.go`: `testColumnstoreChildCDC` and
  `testAnalyticChildCDC` (the destination named by `REPMAN_REGTEST_ANALYTIC_DESTINATION`).
- `regtest/test_replication_plugin_denormalize.go`: `testAnalyticChildDenormalize`, TPC-C
  (order_line ⋈ orders ⋈ customer ⋈ item), checksums `COUNT(*)` and
  `BIT_XOR(CRC32(CONCAT_WS(...)))` of the query on the parent against the denormalized table, after
  the init, a change on each joined table, a deleted item inserted again and a load.
