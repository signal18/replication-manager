# CDC sources: feeds from other engines, and what we take from SymmetricDS

The CDC core (`CDC_CORE.md`) reads a MariaDB binlog. This note adds **capture sources**: a feed
whose source is another engine, PostgreSQL first, into a MariaDB cluster. It also records which
ideas of SymmetricDS we take, rewritten in Go from the concepts (never its code: it is GPL v3,
which would block the enterprise plugins), and in which order.

## SymmetricDS, our CDC, the gaps

| SymmetricDS | What it does | Our CDC today | Gap, and our way |
|---|---|---|---|
| Capture by triggers | Triggers on each table write the changes into its own tables in the client's database | The log: MariaDB binlog (go-mysql canal) | **No triggers, ever** (F1: they change the client's database and its write path). PostgreSQL: logical decoding (pgoutput) through a publication and a replication slot |
| Routing | A change goes to the nodes or groups a router selects (by column, by node) | One feed, one destination, its table rules | Step 2: a row filter per destination, declared |
| Transforms | Column and table transforms in flight | The denormalize mode (a query) | Step 3: declared transforms (rename, cast, drop column), applied at unpack |
| Batches over HTTP with acks | Batches sent to remote nodes, acknowledged, retried | Local checkpoints (tar.gz), applied in order, idempotent | Step 4: remote delivery of the same checkpoints over HTTP, acknowledged by sequence, retried |
| Bidirectional sync with conflict resolution | Every node writes, conflicts resolved by rules | One direction | Later, and for migrations only (below): one writable side at a time, no conflict resolution |
| Initial load | Reload of a table to a node | Analytic child: the seed (a backup); denormalize: a consistent query | A capture source loads from its own consistent snapshot |

Order, after PostgreSQL to MariaDB: routing, declared transforms, remote delivery over HTTP with
acks, then the bidirectional migration.

## The capture source contract (public, in the core)

A plugin whose source is not a MariaDB binlog calls `cdc.MainSource(dest, src)` with a
`cdc.Source`. The core keeps everything else: the periods and their bounds, the checkpoints, the
apply and its position, the staging, the status. The source:

- `Prepare`: before the apply starts. With no initial load done it makes the destination's tables
  (`cdc.PrepareTable`: a table of the same columns is emptied, a missing one created from the
  source's description, `dbhelper.CreateTableFromColumns`, a table of other columns refused,
  never dropped). The apply is not running yet, so no checkpoint of an earlier attempt can land
  after a table is emptied; the checkpoints of an unfinished load are removed first.
- `Run`: the initial load (`Sink.Load`, `Sink.FinishLoad` at the position the stream starts
  from), then the changes (`Insert`, `Update`, `Delete`, `Commit` at each transaction's end with
  the source's position). A period closes only at a commit. The source acknowledges its log only
  up to `Sink.Applied()` (what the destination committed), or up to its position when
  `Sink.Idle()` (nothing captured waits).
- `Dropper` (optional): the plugin run with `-drop` removes what the source created on the
  source server for the feed, listed in the State (`Owned`), nothing else.

The position is the source's own: `State.GTID` holds a PostgreSQL LSN for that source. The wire
gains generic fields only: `Config.SourceKind`, `Config.SourceOptions`, `Endpoint.Database`;
`State.NeedsInit`, `Reason`, `Owned`; `Status.SourceKind`, `SourceRetainedMB`,
`SourceRetainedMaxMB`, `SourceWarning`, `InitialLoad`, `InitialLoadTables`, `SourceStatements`;
the state `needs_init`. The InnoDB destination moved into the core (`cdc.InnoDB`) so a capture
plugin reuses it.

## In replication-manager

- Settings of the destination cluster (T16, T14): `replication-plugin-feed-source-cluster` (a
  PostgreSQL cluster of this replication-manager; empty is the off switch),
  `replication-plugin-feed-enabled` (true: the feed's start and stop, the single switch the GUI and
  the replication interface's cdc link set; only `syncReplicationFeed` writes the TOML's
  `enabled`),
  `replication-plugin-feed-database`, `replication-plugin-feed-tables`,
  `replication-plugin-feed-create-source-objects` (false by default),
  `replication-plugin-feed-retained-max` (MB, 10240). GUI: Settings, High Availability, CDC Feed.
- `syncReplicationFeed` writes the feed's TOML (`replication-plugins/feed.toml`) from them every
  tick, the source being the source cluster's master and the target this cluster's master; the
  usual supervisor runs it (`replplugin-<plugin>`, signature, manifest tier gate ERR00132).
  Stopped (or without source), the TOML is disabled and the position and the source objects
  are kept. `ReinitReplicationPlugin` (the rebuild) and `stopReplPluginAt` (the stop-wait) are the
  one implementation the replication interface's cdc link calls; neither touches the source.
- States (HA domain of the destination cluster): **CINF0040** what the source keeps for the feed
  and its bound (and the initial load's progress); **WARN0360** a warning about the source
  (`max_slot_wal_keep_size` unset, a slot over the bound that is not the feed's); **ERR00140** the
  feed waits for a new initial load, with the reason; the usual CINF0017, WARN0242 and WARN0243.
  Each statement the source ran on the source server is logged once (F11).
- API (GUI buttons, Swagger, ACL `cluster-replication`):
  `replication-plugin-actions/reinit/{plugin}` (the feed's rejoin: a new initial load, the
  owned objects kept for it to replace), `replication-plugin-actions/drop-source-objects/{plugin}`
  (the feed switched off: its own slot and publication dropped, the statements returned).
- The feed appears through the generic replication interface as a link of kind cdc (lag,
  position read and applied, last error, from its status line); its rejoin is the resume from
  its slot, or a new initial load when the slot is gone.

## PostgreSQL to MariaDB (enterprise plugin)

The plugin is private (enterprise tier); its design is with its source. What the product
guarantees:

- **Capture**: logical decoding, pgoutput, a publication and a replication slot named
  `repman_<cluster>`; the position is the LSN; the WAL is acknowledged only once the destination
  committed it. The protocol layer is go-pq-cdc (MIT): its replication connection, pgoutput
  decoder, keepalives and standby status. Not its connector: it writes metadata tables into the
  source for its snapshots, may `ALTER TABLE ... REPLICA IDENTITY` and opens an HTTP port.
- **Initial load**: from the snapshot the slot exports at its creation, so the load and the
  stream join at the slot's consistent point, without a gap.
- **Types**: each PostgreSQL type maps to one MariaDB type and a checked conversion; a type
  without a faithful equivalent refuses its table with the reason, a value the MariaDB type
  cannot hold (NaN, infinity, a BC date) stops the feed with the table and column. Never a
  silent conversion (LOAD DATA LOCAL would only warn).
- **DDL**: pgoutput carries none. A relation whose columns changed stops the feed (schema drift)
  with the state; it resumes once the destination has the same columns, or after a new initial
  load. A `TRUNCATE` needs a new initial load.
- **Safety**:
  - creating the publication and the slot is an explicit opt-in, off by default, each statement
    logged as it ran; without it the feed says which statement to run;
  - the slot's retained WAL is a state; above the bound the feed drops its own slot, stops with
    ERR00140 and needs a new initial load; `max_slot_wal_keep_size` unset is a warning;
  - the client's other slots and publications are never changed: a publication the feed did not
    create must already carry the tables, a slot of the same name the feed did not create is
    refused;
  - a table without a primary key, or with a replica identity that does not carry it, is refused
    (a publication would also make the client's `UPDATE` and `DELETE` fail on it).

Product questions left to the maintainer: the tier of each further capture source.

## Bidirectional, for migrations only (design, not coded)

The purpose is a **migration** with a way back, not multi-master.

1. **Forward feed** old to new (e.g. PostgreSQL to MariaDB), with its initial load.
2. **Cut-over**, one orchestrated action, a state per step, each step logged with its exact
   statement and reproducible by hand (F11), each step a client script hook (F6):
   1. stop writes on the old side (the client's script, or read-only where repman can set it);
   2. wait for the forward lag to be 0 (applied position = the old side's current position);
   3. record both positions: the old side's (LSN or GTID) and the new side's (its GTID);
   4. start the **reverse feed** new to old from the new side's recorded position;
   5. release writes on the new side.
   A rollback is the same action the other way.
3. **One writable side at a time**: no conflict resolution. A write that reaches the side that
   must be passive (a change captured there that the feed did not apply) stops the feed with a
   state, never merges.
4. **Loop prevention**, mandatory: a change applied by a feed is never captured back.
   - MariaDB destination: the apply session of a reverse-capable feed runs with `sql_log_bin=0`
     (`dbhelper.SessionNoBinlogMixed` exists), or its transactions carry a marker the capture
     skips; the InnoDB destination today applies with the binlog on (for the destination's own
     replicas), so the reverse feed needs one of the two, decided at its design.
   - PostgreSQL destination: the apply session sets a replication origin
     (`pg_replication_origin_session_setup`), and the capture asks pgoutput for `origin = none`
     (PostgreSQL 16+). go-pq-cdc's `StartOptions` has no origin option today: the capture would
     send its own `START_REPLICATION` with it, or the option is added upstream (MIT).
5. **What step 1 already prepares**: the capture source contract (a reverse feed is another
   source and destination pair on the same core); positions recorded per feed in its State and
   its destination (`Positioner`), which the cut-over reads; the lag and applied position in the
   status; the owned-objects list for the slot a reverse feed would create; the needs-init state
   for a side that lost its position.

## Tests

- Unit: the sink (`cdc/source_test.go`: load then changes, closing at commit only, tables not
  carried); `dbhelper.CreateTableFromColumnsSQL`; the plugin's type table and conversions.
- Real (T13): PostgreSQL 16 to MariaDB 11.4, initial load, inserts, updates, deletes, a stop with
  writes and the catch-up, the slot's retained WAL state.
