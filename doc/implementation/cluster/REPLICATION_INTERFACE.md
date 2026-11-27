# The replication link interface

Issue #2453. A server's replication links go through one Go interface with several backends
(law T7), as the orchestrator (`ORCHESTRATOR_INTERFACE.md`) and the proxies (`DatabaseProxy`).

## The interface

`cluster/replication.go`, `ReplicationLink`: one replication link of a server.

| method | native | cdc |
|---|---|---|
| `Kind()` | `native` | `cdc` |
| `Name()` | the channel (`""` the default one) | `cdc:<TOML name>` |
| `Status()` | the row of the monitor's last read | the feed's row from its last status line |
| `ReadStatus()` | `SHOW SLAVE '<channel>' STATUS` now | the same row (in memory) |
| `Start`, `Stop` | `START/STOP SLAVE '<channel>'` | the TOML's `enabled`, followed by the supervisor |
| `StopRead`, `StopApply` | the IO, the SQL thread | not supported |
| `ResetPositions` | `RESET SLAVE '<channel>'` (keeps the link) | not supported (`Rejoin` rebuilds) |
| `Remove` | `RESET SLAVE '<channel>' ALL` | not supported (the child's seed) |
| `ChangeSource` | `CHANGE MASTER` with the TLS decision (#1933) | not supported (`replication-plugin-source`) |
| `SkipEvent` | `sql_slave_skip_counter` | not supported |
| `Rejoin` | the rejoin family of `srv_rejoin.go` | resume, or rebuild (below) |

Not supported is `ErrReplicationNotSupported`, never nil. Every status row carries its kind,
`SlaveStatus.Kind` (`db:"-"`, never read from SQL). A field with no meaning for a backend stays
empty: a feed has no binlog file, no `Using_Gtid`, no TLS.

Accessors on the server: `ReplicationLink()` (the managed channel, `replication-source-name`, what
the old `StopSlave()`/`StartSlave()` used), `ReplicationChannelLink(name)`, `ReplicationLinks()`
(native channels then feeds) and `ReplicationLinkStatus()` (their rows: what the GUI, the API and
the MCP `get-database-replication` read).

## Link changes are states (law T27)

Each change method takes the reason, the action that did it (`LinkByFailover`, `LinkBySwitchover`,
`LinkByRejoin`, `LinkByReseed`, `LinkByOperator`, ...). The change is recorded on the server
(`srv_link_change.go`) and stays a state until the link runs again: **WARN0352** names the
server, each channel, the action (stopped, read stopped, apply stopped, positions reset,
repointed) and the reason. A reseed's stops keep their own text, **WARN0252** (the same tracker).
A `Start` that succeeds drops the record at once, so a failover or a switchover, which stops,
repoints and starts in one go, leaves nothing open. A `Remove` drops it too: the link is gone,
the promotion has its own states. A record leaves the list once its channel runs again, or once
the channel is gone from a server that answers (F3/F4); within two ticks of the record it stays
(a status read before the STOP).

## The native backend

`cluster/replication_native.go` is the code that was in `srv.go` (`StopSlave`, `StopSlaveChannel`,
`StartSlave`, `StartSlaveChannel`, `StopSlaveIOThread`, `StopSlaveSQLThread`, `ResetSlave`,
`ResetSlavePositions`, `SkipReplicationEvent`, `StopAllSlaves`, `StopAllExtraSourceSlaves`), in
`srv_set.go` (`ChangeMaster`) and in the monitor's status read, moved without a change of the
statements sent. MariaDB, MySQL/Percona and PostgreSQL: the engine branches are dbhelper's (T1,
T23). The rejoin family stays in `srv_rejoin.go`; its entry `RejoinMaster` became the unexported
`rejoinMaster`, which only the native link's `Rejoin` calls.

The helpers that point a replica (`ChangeMasterTo`, `SetReplicationGTIDSlavePosFromServer`,
`SetReplicationGTIDCurrentPosFromServer`, `SetReplicationFromMaxscaleServer`,
`pointSlaveToMaster*`) take the reason of their caller.

**The lock** (`replication_dispatch_test.go`, `TestNoReplicationLinkSQLOutsideBackend`): in the
cluster, server, mcp and regtest packages, only `replication_native.go` names dbhelper's
`GetAllSlavesStatus`, `GetChannelSlaveStatus`, `GetSlaveStatus`, `StartSlave`, `StopSlave`,
`StopSlaveIOThread`, `StopSlaveSQLThread`, `StopAllSlaves`, `ResetSlave`, `ResetAllSlaves`,
`ChangeMaster`, `SkipBinlogEvent`. The link helpers not behind the interface yet (the replication
password, the exec and GTID modes, the heartbeat, the filters, `default_master_connection`, the
Spider multi-source) are listed per file with their count, a list that only shrinks.

## The CDC backend

`cluster/replication_cdc.go`: a replication plugin's feed (`cluster_replplugin.go`, the CDC core
`cluster/replplugin/cdc`) is a link of the server it writes into, its target
(`wire.Config.Target`). Its surface is small so that every destination plugin is a link without
code here: the TOML, the State and applied files, and the supervisor's last status line.

- **Status** (`server.ReplicationFeeds`, refreshed with the server, never in `server.Replications`
  which the failover, rejoin and reseed act on): capture = read (`Slave_IO_Running`), apply = apply
  (`Slave_SQL_Running`, No while an apply fails), `Seconds_Behind_Master` = the lag, `Gtid_IO_Pos`
  = the GTID captured, `Gtid_Slave_Pos` = the GTID applied, `Last_IO_Error` = the plugin's error or
  its stop reason, `Last_SQL_Error` = the apply error, `Slave_SQL_Running_State` = the plugin's
  description (a pause) or disabled / stopped / not started.
- **Start, stop**: the TOML's `enabled`, then `CheckReplicationPlugins`. The feed of the
  `replication-plugin-feed-*` settings (`CDC_SOURCES.md`) has its TOML written every tick from
  those settings, one source of truth: its link refuses start and stop
  (`ErrReplicationNotSupported`, naming `replication-plugin-feed-source-cluster`) until the feed
  has a setting of its own for it; the link never writes that TOML.
- **Rejoin**: one implementation of the new start, `ReinitReplicationPlugin`
  (`cluster_replplugin_feed.go`: the plugin stopped and waited for, its position, applied file,
  checkpoints and merges dropped, what it owns on the source kept).
  - A capture source of another engine (`SourceKind`, PostgreSQL): when its State says it needs a
    new initial load (`NeedsInit`), `ReinitReplicationPlugin`; then it resumes.
  - A MariaDB/MySQL binlog: the GTID applied (`applied.json`, else the State) is checked against
    the source's binary logs (`gtidRetained`, shared with the backup verification: MySQL
    `gtid_purged`, MariaDB `BINLOG_GTID_POS` of the oldest binary log). Still there: the feed
    resumes from its State. Purged: `ReinitReplicationPlugin`, which refuses a binlog feed with the
    reason (its copies' new start is the child's seed): the feed stays recorded (WARN0352) with
    that reason, and the operator reseeds the child master from its parent.
- The source is read only: its binary log list, its positions (F1).
- **States**: the feed's health stays where it was, in the child's HA domain (WARN0242 stopped,
  WARN0243 apply failing, ERR00130/ERR00131 source, CINF0017): the copies stop until someone acts.
  The merge of the staging late by more than one merge interval is a throughput fact, in the
  workload domain: **WARN0353** (T3, T4: never in the HA view).

A DuckDB or any other destination, and any capture source of another engine, plugs in by being a
replication plugin: its feed is a CDC link with no code in this file.

## Consumers

- GUI, the server grid (`DBServerGrid`): a block per CDC feed after the native channels, with its
  kind, source, capture, apply, delay, GTID captured and applied, state and errors.
- MCP `get-database-replication`: `replications` lists every link with its kind.
- The API's server JSON: `replications` (native, as before) and `replicationFeeds` (cdc).

## T27: what each moved path stops, repoints or resets

Nothing changed: each path sends the statements it sent before, on the same channels. For review:

- failover, switchover (`cluster_fail.go`): stop the candidate's managed channel, `RESET SLAVE ALL`
  on it (its promotion), repoint and start the old master and the replicas on the managed channel;
  the extra multi-source channels are repointed on the new master and removed from the old master
  on a switchover; the ring is closed by repointing the old master's child;
- rejoin (`srv_rejoin.go`, the flashback and dump jobs): stop, repoint, start the managed channel;
  the loop rejoin repoints the sibling;
- reseed (`srv_job_backup.go`, `srv_job_postgres.go`, `cluster_staging.go`): stop every channel of
  the last status, reset the managed channel's positions (without ALL), repoint the managed
  channel to the primary only when it pointed there, start it; the PITR restore and the staging
  refresh remove the managed channel (`RESET SLAVE ALL`, as before: a gap of T27 noted, not
  changed by this move);
- replication bootstrap (`prov.go`, an operator action): stop and remove every channel, then set
  up the topology;
- the API: start, stop, reset all, skip, rejoin on the operator's request.
