# Replication plugins: one interface for every link

Issue #2453. Status: **design validated by the maintainer (2026-11-27)**. Steps 1, 2, 2b, 3 and 4 (below,
"Where the code is today") are coded and tested; the rest of this document is the target.

## The rule

- **One interface, `ReplicationPlugin`.** Failover, switchover, rejoin, reseed, bootstrap, the
  monitor, the GUI, the API and MCP call it and nothing else (law T7, as the orchestrators and the
  proxies).
- **A link type is a plugin; a replication of a cluster is an instance of one** (an entry of `server.Replications`, the `Replication` object below: the plugin, the
  server it writes into, its source, its settings). A cluster with three MariaDB channels and a feed
  into DuckDB has four links of two plugins.
- **A plugin name is never a copy of code (T2).** Plugins that share a mechanism share its
  implementation; the engine differences stay in dbhelper (T1, T23).

## Names: `repl-<source>-<target>-<type>`

- `<source>`, `<target>`: the engines. The target is always named, even when it is the source's
  engine (`repl-mariadb-mariadb-native`). A storage engine inside MariaDB is not a target, except
  where it writes in its own way (ColumnStore, DuckDB, S3).
- `<type>`: who applies the changes.
  - `native`: the engine replicates by itself; repman drives it.
  - `cdc`: repman reads the source's log (binlog, PostgreSQL logical decoding) and applies it.
  - `trigger`: triggers on the source record the changes (a source whose log can not be read). To come.
- Never in a name: physical or logical, async, semi-sync or group (properties of a topology), and a
  mode (copy, denormalize, S3 partition or table: settings of the link).

## The object: each entry of `server.Replications` is a `Replication`, given by a plugin

The object already exists: every `ServerMonitor` holds its replications in `server.Replications`
(and `LastSeenReplications` while it is down). The call chain that fills it, each monitor tick:

1. `Cluster.TopologyDiscover` (`cluster_topo.go`) starts `server.Ping` for each server;
2. `ServerMonitor.Ping` (`srv.go`) calls `server.Refresh()`;
3. `ServerMonitor.Refresh` calls `server.refreshNativeLinks()` and `cluster.cdcFeedsOf(server)`
   and stores both, in that order, in `server.Replications` (one assignment);
4. `refreshNativeLinks` (`replication_native.go`) builds one native `Replication` per row of
   `readNativeLinks` (`SHOW ALL SLAVES STATUS`, or the channels one by one, or the PostgreSQL
   subscriptions); `cdcFeedsOf` (`replication_cdc.go`) one CDC `Replication` per feed whose target
   is the server;
5. back in `TopologyDiscover`, once every `Ping` is done, the graph is built: the master, the
   replicas, who replicates from whom.

Nothing new is added beside it (T2): each entry becomes an interface, `Replication`, implemented by
the plugin of that link. Steps 3 and 4 store `Replication` objects of their plugin instead of bare
rows, and the CDC feeds join the same list; steps 1, 2 and 5 do not change.

A replication is between two servers, often of two clusters:

```go
type Replication interface {
    Plugin() ReplicationManifest
    SourceCluster() *Cluster        // nil for an external source repman does not manage
    SourceServer() Endpoint         // a server of a cluster, or an external address
    TargetCluster() *Cluster
    TargetServer() *ServerMonitor   // the server whose server.Replications holds it
    Name() string                   // its identity on the target: the channel, the subscription, the feed

    Status() (dbhelper.SlaveStatus, string, error)   // its row (as today) and the statement (F11)
    WaitCaughtUp(until Position, timeout time.Duration) (string, error)

    Start(reason string) (string, error)              // copy + apply
    Stop(reason string) (string, error)
    StartCopyEvents(reason string) (string, error)
    StopCopyEvents(reason string) (string, error)
    StartApplyEvents(reason string) (string, error)
    StopApplyEvents(reason string) (string, error)

    ResetPositions(reason string) (string, error)     // the link stays (T27)
    SetPosition(at Position, reason string) (string, error)
    SkipEvent(reason string) (string, error)
    ChangeSource(to Endpoint, opt ChangeSourceOpt, reason string) (string, error) // failover, switchover, rejoin only
    Promote(reason string) (string, error)            // this edge ends, so its target can become a source
    Remove(reason string) (string, error)             // promotion or operator only (T27)
    Rejoin() error                                    // resume, or rebuild from a new initial load

    Configure(opts LinkOptions, reason string) (string, error) // the options its manifest declares
    RotateCredential(reason string) (string, error)
}

// The plugin keeps only what the code does not already do: describe itself, create an edge, set up a topology.
type ReplicationPlugin interface {
    Manifest() ReplicationManifest
    Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) // its checks first
    Bootstrap(c *Cluster, topology string, master *ServerMonitor) error
}

type Endpoint struct {
    Server *ServerMonitor // set when repman manages the source server
    Host   string         // otherwise its address, and the account read from the credential chain
    Port   string
}
```

- The four ends are named the same way everywhere: `SourceCluster`, `SourceServer`, `TargetCluster`,
  `TargetServer` (T19: one concept, one name, in Go, TOML, JSON, the API and the GUI).
- **One list**: the CDC feeds writing into a server are entries of its `server.Replications`, with
  their plugin; `ReplicationFeeds` (added in step 1) goes away. The failover only takes the
  replications whose plugin can `Promote` and `ChangeSource`, so never a CDC feed.
- **The JSON does not break**: each entry of `replications` keeps every field of today's row, and adds
  its plugin and its four ends beside them; the GUI and the API clients read what they read today.
- **The topology of a cluster is the graph of these objects.** Master-slave: one replication from the
  master into each replica. Multi-tier: a chain. Ring: a cycle. Multi-source: several into one
  target. A child cluster, a staging or a CDC feed: one whose `SourceCluster` is another cluster.
- **Nothing is "on a server"**: stopping every replication of a server before a reseed is `Stop` on
  each entry of its list (a plugin may send one `STOP ALL SLAVES` for them: its own business).
- **A failover is two operations on the graph**: `Promote` on the candidate's replication of the old
  master, then `ChangeSource` of the other replicas' replications to the new master.
- `Position` is the engine's position (a GTID set, a binlog file and offset, a PostgreSQL LSN), read
  and written by the plugin; repman compares positions only through it.
- `Promote` by engine: MariaDB and MySQL `RESET SLAVE ALL` on that replication (on MySQL the channel
  stopped first); `read_only` is the server's, not the link's, and stays the failover's, after it read
  the new master's position (below); PostgreSQL `pg_promote()`; Group Replication `group_replication_set_as_primary`; Galera nothing to do
  (every member writes); a CDC feed is never promoted (a child never replaces its parent).

- The verbs name what the link does whatever the engine: it **copies** the source's events, then
  **applies** them (for MariaDB the IO and SQL threads; for a CDC feed the capture and the apply).
- An action the manifest does not list answers `ErrReplicationNotSupported`, never nil; the GUI,
  the API and MCP hide it.
- `Status` gives every link the same `SlaveStatus` row; a field with no meaning for the type stays
  empty. It returns the statement it ran beside the row, as every other verb (law F11: the caller
  logs it, the operator can replay it).

### The manifest

```go
type ReplicationManifest struct {
    Name       string               // repl-<source>-<target>-<type>
    Type       string               // native | cdc | trigger
    Tier       string               // free | enterprise
    Source     []string             // engines it reads (with versions, utils/version)
    Target     []string             // engines it writes
    Topologies []ReplicationTopology
    Actions    []string             // the verbs it implements
    Settings   PluginSettings       // its settings and secrets, as the snapshot and archive plugins
}

type ReplicationTopology struct {
    Name        string // master-slave, galera, stream, ...
    Transport   string // physical | logical
    Consistency string // async | semi-sync | group
}
```

## The plugins

| Plugin | Type | Topologies | Implementation | Tier |
|---|---|---|---|---|
| `repl-mariadb-mariadb-native` | native | master-slave, multi-tier-slave, multi-master, multi-master-ring, active-passive, binlog-server (logical; async or semi-sync); galera (logical, group); multi-source between clusters | binlog, galera (built in) | free |
| `repl-mysql-mysql-native` | native | the binlog topologies; group-replication (logical, group) | binlog, group replication (built in) | free |
| `repl-mysql-mariadb-native` | native | external source, multi-source (logical, async) | binlog (built in) | free |
| `repl-postgresql-postgresql-native` | native | stream (physical), logical (publication and subscription) | postgresql (built in) | free |
| `repl-columnstore-columnstore-native` | native | columnstore | columnstore (built in) | free |
| `repl-mariadb-mariadb-cdc` | cdc | copy, denormalize; engine a parameter (InnoDB, Aria) | external | free |
| `repl-mariadb-columnstore-cdc` | cdc | copy, denormalize | external | free (community-maintained) |
| `repl-mariadb-duckdb-cdc` | cdc | copy, denormalize | external | enterprise |
| `repl-mariadb-s3-cdc` | cdc | S3 by partition: the delta kept by partition, each changed partition rebuilt and exchanged | external | enterprise |
| `repl-mariadb-s3files-cdc` | cdc | Parquet files in a bucket (also the migration path) | external | enterprise |
| `repl-mariadb-kafka-cdc` | cdc | a topic per index | external | enterprise |
| `repl-mariadb-redis-cdc`, `-memcached-cdc`, `-rabbitmq-cdc` | cdc | keys, messages | external | free |
| `repl-mariadb-clickhouse-cdc`, `-postgresql-cdc`, `-sphinx-cdc` | cdc | copy, denormalize | external | enterprise |
| `repl-postgresql-mariadb-cdc` | cdc | migration (bidirectional designed, not coded) | external | enterprise |
| `repl-<source>-<target>-trigger` | trigger | a source whose log can not be read | external | enterprise |

The CDC destinations, their apply and their position are the private CDC design
(`doc/enterprise-denormalize-plugin.md` in the plugin-signer repository): one plugin per
destination, each in copy and denormalize mode, S3 by partition (the index's partitioning, the delta of each changed partition applied to a work
table then exchanged). Tiers: the maintainer's decision of 2026-11-26.

## Two ways to implement the same contract

### Built in: the native plugins

- Built into the repman binary, never an external process: a failover never waits on one (F2).
- **Binlog native** is one Go type registered three times (`repl-mariadb-mariadb-native`,
  `repl-mysql-mysql-native`, `repl-mysql-mariadb-native`) with three manifests, and a fourth time
  for ColumnStore (`repl-columnstore-columnstore-native`), whose servers replicate their binlog. The manifest decides
  the engines and the topologies (Galera for MariaDB only, Group Replication for MySQL only); the SQL
  differences are dbhelper's. No `if mariadb` in the plugin.
- **Galera**, **Group Replication**, **PostgreSQL** and **ColumnStore** are their own code where the
  mechanism differs (SST/IST, group membership, publication/subscription, recovery configuration),
  each through dbhelper.

### External: one adapter for every plugin binary

- One adapter in repman (`replicationPluginAdapter`) drives every external plugin. Adding a link type
  is shipping a signed plugin, with no change to repman.
- **The contract** is the one the snapshot and archive plugins already speak (T2): the action as the
  first argument (`status`, `start`, `stop`, `start-copy`, `stop-copy`, `start-apply`, `stop-apply`,
  `create`, `remove`, `rejoin`, `bootstrap`), the context in `REPMAN_REPL_*` variables (settings,
  decrypted secrets only into its environment), tab-separated records on stdout, the error as the
  last line of stderr with a non-zero exit, a deadline on every call.
- **`run`**: the long-running capture and apply, started and supervised by repman as today
  (`superviseReplPlugin`: one process per link, restarted when its configuration changes, stopped on
  a standby). Start and stop of a CDC link are the supervisor's: `Start` sets the link enabled and the
  next tick starts `run`.
- The files stay the exchange with `run`: the link's TOML (written by the save callback only, T25),
  its State, its checkpoints, its status line.
- The CDC core (`cluster/replplugin/cdc`: checkpoints, initial load, resume, position, `Source` and
  `Destination`) is a **library** the CDC plugins are built with, an SDK. repman never sees `Source`
  or `Destination`.
- The plugins are signed and verified before they run, with their tier checked against the plan
  (ERR00132), as today.

## Bootstrap

Today `BootstrapReplication` (`prov.go`) holds the topologies as branches per engine. Target:

1. repman reads the cluster's plugin (from its engine, or the setting that names it) and the
   topology asked (`topology-target`).
2. The topology must be in the plugin's manifest; otherwise the bootstrap is refused with the
   topologies the plugin offers.
3. The cleanup before a bootstrap (every channel removed) stays an explicit operator request, as today.
4. `plugin.Bootstrap(cluster, topology, master)` sets the links up: for the binlog plugin, the
   `CHANGE MASTER` of each replica on the managed channel; for PostgreSQL logical, the publication
   then each subscription; for a CDC plugin, the feed and its initial load (today's seed of the child).
5. `Create` is the same step for one link: an operator adding a channel or a feed.

## Every replication function of dbhelper, and its place

Built from the code (`utils/dbhelper`), so that no operation repman does on a link is left out of the
interface. A function is a verb of every plugin, or the bootstrap of one plugin.

| dbhelper | Verb, or plugin |
|---|---|
| `StartSlave`, `StopSlave` | `Start`, `Stop` |
| `StartSlaveIOThread`, `StopSlaveIOThread` | `StartCopyEvents`, `StopCopyEvents` |
| `StartSlaveSQLThread`, `StopSlaveSQLThread` | `StartApplyEvents`, `StopApplyEvents` |
| `StopAllSlaves` | `Stop` on each link of the target (the binlog plugin may batch it) |
| `ResetSlave` without ALL | `ResetPositions` |
| `ResetSlave` with ALL, `ResetAllSlaves` | `Remove` (on each link for the bootstrap cleanup), `Promote` (binlog) |
| `ChangeMaster` | `Create` (a new edge), `ChangeSource` (an edge repointed) |
| `SkipBinlogEvent` | `SkipEvent` |
| `GetSlaveStatus`, `GetAllSlavesStatus`, `GetChannelSlaveStatus`, `GetMSlaveStatus`, `GetPGSlaveStatus`, `PostgresGetChannel` | `Status` |
| `SetGTIDSlavePos`, `SetGTIDBinlogState`, `SetGtidPurged` | `SetPosition` |
| `MasterWaitGTID`, `MasterWaitGTIDReached`, `CheckSlaveSync`, `GetReplicationLastReceived`, `PostgresSubscriberCaughtUp` | `WaitCaughtUp` |
| `SetSlaveHeartbeat`, `SetSlaveExecMode`, `SetSlaveGTIDMode`, `SetSlaveGTIDModeStrict`, `SetSlaveParallelMode`, `SetChannelReplicateIgnoreTable`, `SetSemiSyncMaster`, `SetSemiSyncMasterEnabled`, `SetSemiSyncSlave`, `InstallSemiSync`, `SetMySQLGtidMode`, `SetEnforceGTIDConsistency` | `Configure` (each option declared by the plugin's manifest) |
| `ChangeReplicationPassword`, `CreateReplicationUser`, `CheckReplicationAccount` | `RotateCredential`; the account's creation and check in `Create` and `Bootstrap` |
| `CheckSlavePrerequisites`, `CheckReplicationFilters`, `CheckSlaveSemiSync` | the checks of `Create` and `Bootstrap` (a link refused says why) |
| `PostgresPromote` | `Promote` (postgresql) |
| `PostgresEnsurePublication`, `PostgresOwnSubscription`, `PostgresSubscriptionName`, `PostgresRefreshSubscription`, `PostgresSubscriptionNeedsRefresh`, `PostgresInstallDDLReplication` | `Bootstrap`, `Create`, `Configure` of postgresql (logical) |
| `PostgresDropSubscription`, `PostgresDropReplicationSlot`, `PostgresDropDDLReplication` | `Remove` of postgresql (logical) |
| `BootstrapGroupReplication`, `StartGroupReplication`, `SetGroupReplicationAllowlist` | `Bootstrap`, `Start`, `Configure` of group replication |
| `SetGroupReplicationPrimary`, `IsGroupReplicationMaster`, `IsGroupReplicationSlave` | `Promote`, `Status` of group replication |
| `GaleraSSTAccountInitSQL`, `GaleraSSTPrivileges` | `Bootstrap`, `RotateCredential` of galera |
| `SetHeartbeatTable`, `WriteHeartbeat`, `SetStatusActiveHeartbeat` | the monitor's own lag probe, not a link verb: stays in the monitor |
| `GetBinlogEventPseudoGTID`, `GetLastPseudoGTID`, `BinlogGtidPos`, `GetFirstBinaryLogGtidList`, `GTIDSubset`, `GtidPurgedSubsetOf` | the binlog plugin's `Position` helpers (positions compared through the plugin) |
| `GetSlaveHosts`, `GetSlaveHostsArray`, `GetSlaveHostsDiscovery` | the discovery of the graph: which links point at a source (topology discovery) |
| `SetSlaveConnectionsNeededForPurge` | `Configure` of the source side of a binlog link |

## Links are the client's (T27, F1)

- `Remove` only for a promotion (failover, switchover) or an operator request, never in another path.
- `ChangeSource` only from the failover, switchover and rejoin code, under their guards.
- Every change of a link opens a state naming the server, the link, the action and the reason
  (**WARN0352**; a reseed keeps **WARN0252**), recorded once the statement succeeded, closed when
  the link runs again.
- A `trigger` plugin changes the client's database: off by default, an explicit switch (as
  `replication-plugin-feed-create-source-objects` for the PostgreSQL slot), and a `remove` that takes
  every trigger and table it created away.

## Where the code is today (steps 1, 2, 2b, 3 and 4, coded and tested)

- `cluster/replication.go`: the `Replication` interface of the design, plus **`Row()`**: the row of
  the monitor's last read, no statement, what the code that only reads a field uses (`Status()`
  reads the server again, as before). `ReplicationPlugin` keeps `Manifest`, `Create` and
  `Bootstrap` (both still `ErrReplicationNotSupported`, step 5). `Endpoint`, `Position` (an alias
  of `ReplicationTarget`, the type the waits already used: a GTID, else a file and a position),
  `ChangeSourceOpt` (an alias of `dbhelper.ChangeMasterOpt`: the rest of the definition) and
  `LinkOptions` (the properties `Configure` sets). `replicationBase` holds what every object
  holds and answers `ErrReplicationNotSupported` to every verb; each plugin's type embeds it and
  declares the verbs its manifest lists, as `unsupportedOrchestrator`.
- `server.Replications` and `LastSeenReplications` are a `ReplicationList` (`[]Replication`):
  `OfKind`, `Rows`, `Named`. `server.NativeReplications()` is the native channels only: what the
  code that acts on the engine's channels iterates (the multi-source failover, the reseed, the stop
  of every channel, the checks), so a CDC feed in the list changes none of them.
  `server.ReplicationLink()` / `ReplicationChannelLink(name)` return the object of the last read,
  or a new one with an empty row for a channel not defined yet (the `CHANGE MASTER` of a new
  replica).
- **The ends**: `TargetServer` the server, `TargetCluster` its cluster; `SourceServer` from the
  row's `Master_Host`/`Master_Port` (a feed: its TOML's source) matched against this cluster's
  servers, then the other clusters' (`serverOfAnyCluster`, the lookup `replPluginSourcePass` used,
  without the cluster lock), else an external `Endpoint{Host, Port}` with `SourceCluster` nil.
- **The JSON**: each entry is the row with today's keys, then `plugin`, `sourceCluster`,
  `sourceServer`, `targetCluster`, `targetServer` (the clusters' names, the servers' URLs);
  `replicationFeeds` is gone, the GUI reads the feeds from `replications` by `kind`. The list
  decodes back (the CLI, the gRPC API's double marshal) as objects detached from any server, whose
  verbs answer an error. `serverstate.json`, the staging's `replications.json` and the
  `slave-status` API keep writing the native rows only, as before: unchanged on purpose (the
  maintainer's decision, 2026-11-27).
- `cluster/replication_native.go`: the native object, every verb of the manifest, the statements
  unchanged. The new verbs took over the existing code:
  - `Promote`: the link part of the failover and switchover's promotion of the candidate
    (`cluster_fail.go`): on MySQL and Percona the stop of the channel, then `RESET SLAVE ALL` on
    it, each step logged as before; PostgreSQL `PostgresPromote` (`cluster_fail_postgres.go`: the
    failover, the switchover and its rollback, each at the place it was). `read_only` is not the
    link's: `openNewMaster` (`cluster_fail.go`) keeps the order of before, `Promote`, then on a
    switchover the new master's `Refresh` ("fresh GTID pos before open traffic"), then
    `SetReadWrite`. The position that refresh reads is the one the other replicas are repointed
    to; read after `read_only` off, a write of an application connected directly could slip in
    before it and positional replicas would skip it (F5: nothing proves no traffic reaches the
    new master before the proxies switch). `TestOpenNewMasterOrder` holds the order.
  - `SetPosition`: on MariaDB the link's resume position, `SetGTIDSlavePos` (the failover's copy
    of the old leader's GTID, the flashback rejoin, the ColumnStore rebootstrap, the seed of a
    child, the bootstrap cleanup). On MySQL it is `GTID_PURGED`, the server's whole GTID state,
    valid only right after a restore (after `RESET MASTER`): refused
    (`ErrReplicationPositionNotEmpty`, nothing sent) when `gtid_executed` is not empty, read
    through `dbhelper.GetVariableByName` (the backup verification, its one caller, resets the
    binary logs first). PostgreSQL not supported.
  - `WaitCaughtUp`: `MasterWaitGTIDReached` / `MasterPosWaitReached` (the same `MASTER_GTID_WAIT`
    and `MASTER_POS_WAIT` statements as `MasterWaitGTID` / `MasterPosWait`), for
    `WaitSyncToMaster` (switchover, rolling operations), `WaitSyncToTarget` and the positional
    rejoin of a relay. A timeout is `ErrReplicationNotCaughtUp`, a NULL result
    `dbhelper.ErrWaitAborted`; the callers that ignored both before still do. PostgreSQL not
    supported here yet (`postgresWaitSync` reads the primary: step 3).
  - `Configure`: the setters of the `force-slave-*` enforcement (`CheckSlaveSettings`: GTID mode,
    gtid_strict_mode, exec mode, parallel mode) and of `force-slave-heartbeat`
    (`checkReplicationHeartbeatPeriod`); the decision and the states stay the monitor's, no key
    changed (T16). PostgreSQL not supported.
  - `RotateCredential`: `ChangeReplicationPassword` with the cluster's replication account (the
    rejoin after a rotation). PostgreSQL not supported.
- `StopCopyEvents`, `StopApplyEvents` and `ChangeSource` record the link change (WARN0352) once
  their statement succeeded, as `Stop` and `ResetPositions`; a CDC `Stop` once the TOML says so.
- The failover's extra multi-source loop (`FailoverExtraMultiSource`) takes only the replications
  whose manifest lists `promote` and `change-source` (`CanFailover`): never a CDC feed.
- `cluster/replication_cdc.go`: the CDC object (`cdcReplication`, its feed and its row) and the
  adapter of every CDC plugin (step 4, below).
- The lock test `TestNoReplicationLinkSQLOutsideBackend` now covers the moved helpers too
  (`PostgresPromote`, `SetGTIDSlavePos`, `SetGtidPurged`, the four waits, the five setters,
  `ChangeReplicationPassword`): `cluster/replication_native.go` only.
- The `[[repl-link]]` level of the settings is not coded (later).
- `cluster/srv_link_change.go`: the link changes as states (WARN0352).
- Tested on dev3 (2026-11-27), each step: switchover with stopped replicas, failover, mysqldump
  reseed; no regression against release/4.0.1 (details in the pull request).

### Step 3: the native plugins

- **The plugins, built in** (`nativePlugins`, `replication_native.go`), each with its manifest:
  - binlog (`replication_binlog.go`): one Go type, `binlogReplicationPlugin`, registered four
    times: `repl-mariadb-mariadb-native` (MariaDB into MariaDB; the binlog topologies and
    `multi-master-wsrep`, logical, group), `repl-mysql-mysql-native` (MySQL and Percona; the
    binlog topologies and `multi-master-grprep`, logical, group), `repl-mysql-mariadb-native`
    (MySQL or Percona into MariaDB; the binlog topologies), `repl-columnstore-columnstore-native`
    (`columnstore`, logical, async). The binlog topologies: `master-slave` (async and semi-sync),
    `multi-tier-slave`, `multi-master`, `multi-master-ring`, `active-passive`, `binlog-server`,
    all logical. Every verb but `bootstrap` and `create` (step 5). The engine branches it had
    moved to dbhelper (`replication_position.go`): `ReplicaWaitReached` (MASTER_GTID_WAIT on
    MariaDB, MASTER_POS_WAIT / SOURCE_POS_WAIT otherwise), `SetReplicaPosition` (gtid_slave_pos,
    or GTID_PURGED on a server whose gtid_executed is empty), `ResetAllNeedsStop` (MySQL stops
    the channel before RESET SLAVE ALL); each refuses PostgreSQL, nothing sent (T23).
  - `repl-postgresql-postgresql-native` (`replication_postgres.go`): `master-slave-pg-stream`
    (physical, async) and `master-slave-pg-logical` (logical, async). Actions: status, start,
    stop, reset-positions, remove, change-source, promote (`pg_promote()`), rejoin. No copy or
    apply apart, no skip of an event, no position, no wait, no option, no credential: those
    answer `ErrReplicationNotSupported`, nothing sent (before, the copy, apply and skip verbs
    sent MariaDB statements a PostgreSQL server refused).
  - Group Replication (`replication_group.go`), under the MySQL plugin's manifest: a member's
    membership of the group, not a channel the monitor reads, so it is not in
    `server.Replications`: `server.GroupReplication()`, `Start` (the allowlist then
    START GROUP_REPLICATION: the rejoin, the bootstrap of the other members), `Promote`
    (group_replication_set_as_primary: the switchover), the bootstrap of the first member.
    The statements and log lines of srv.go and srv_set.go, moved.
  - Galera: a topology of the MariaDB plugin only. Its members send no link statement on a
    failover (every member writes) and its SST account is the configurator's: no object of
    its own until its bootstrap moves into the plugin (step 5).
  - ColumnStore: the binlog object under its manifest (its servers replicate their binlog as a
    MariaDB master-slave); its rebootstrap stays `cluster_columnstore_replica.go` until step 5.
- **The plugin of a replication** (`nativePluginName`), where `refreshNativeLinks` builds each
  entry and for a channel not read yet (`ReplicationChannelLink`): a PostgreSQL server (its
  engine, or a PostgreSQL topology declared) `repl-postgresql-postgresql-native`; a MySQL or
  Percona server `repl-mysql-mysql-native`; a server of a `columnstore` cluster
  `repl-columnstore-columnstore-native`; a MariaDB server whose source is a monitored MySQL or
  Percona server `repl-mysql-mariadb-native`; otherwise (and an engine not known yet)
  `repl-mariadb-mariadb-native`. No new discovery: the rows are the ones read before. A MariaDB
  source into a MySQL server keeps the MySQL plugin (there is no native `repl-mariadb-mysql`).
- **Unchanged**: every statement and its order (the promotion: `Promote`, the switchover's
  `Refresh`, then `SetReadWrite`), the JSON keys; `plugin` now names each replication's plugin,
  and a decoded entry is an object of that plugin.
- The lock test covers the three new files and the moved helpers (`ReplicaWaitReached`,
  `SetReplicaPosition`, the four group replication helpers).
- Regtests made a gate: the two-switchover test waits (30 s at most) until the monitor sees
  every replica on the new master (`WaitSlavesRunning`, shared with its semi-sync twin); the
  flashback rejoin test waits for the load to end before its checksum.

### Step 4: the CDC plugins through one adapter

- **One adapter** (`replicationPluginAdapter`, `cluster/replication_cdc.go`) is every CDC plugin
  binary as a `ReplicationPlugin`; no destination has code of its own in repman. Its feeds are
  `cdcReplication` objects in their target's `server.Replications`, built where `Refresh` builds
  them (`cdcFeedsOf`), the native channels first.
- **The names** (`cdcPlugins`): `repl-mariadb-duckdb-cdc`, `repl-mariadb-columnstore-cdc`,
  `repl-mariadb-mariadb-cdc`, `repl-postgresql-mariadb-cdc`, each with repman's manifest (its
  engines, its topologies: copy and denormalize, migration for PostgreSQL). **Aliases**: the short
  names the settings and TOMLs hold (`duckdb`, `columnstore`, `innodb`, `postgresql`) and the
  binaries of before (`replplugin-duckdb`, ...) name the same plugin: the registry, the JSON,
  the feed's `plugin` and `CanonicalReplicationPluginName` give the new name. A CDC name repman
  does not know keeps the generic `repl-cdc` manifest (or its own `repl-*` name from its binary's
  manifest, or from a peer's JSON).
- **The binary**: looked for under the plugin's name first, then under the name of before
  (`replPluginBinaryNames`): a delivery of either runs, signed and gated by its tier as before
  (ERR00132). The TOMLs are not renamed: an analytic child seeded as `duckdb.toml` keeps it when
  `replication-plugin-destination` is set to `repl-mariadb-duckdb-cdc` (`analyticPluginPath`).
- **The manifest** (`cdcManifestOf`): repman's, completed by the replication fields of the
  binary's `<binary>.manifest.json` (the file it already ships, read once per change): `source`,
  `target`, `topologies`, `settings` (a name the environment carries only), `actions`; the tier is
  the one the supervisor enforces. A known plugin keeps its name whatever its binary says.
- **The verbs**: `Status`, `Start`, `Stop`, `Rejoin` are the supervisor's, as before (the status
  line, the TOML's enabled flag, resume or rebuild). `StartCopyEvents`, `StopCopyEvents`,
  `StartApplyEvents`, `StopApplyEvents` only when the binary's manifest lists them:
  `<binary> <action> -config <TOML>`, a deadline of 2 minutes, `REPMAN_REPL_ACTION`,
  `REPMAN_REPL_NAME`, `REPMAN_REPL_REASON` and the settings in its environment, its stdout (at most
  64 KiB) returned as the statement (F11), the last line of stderr as the error; a stop recorded
  (WARN0352). Every other verb, and a verb the manifest does not list, answers
  `ErrReplicationNotSupported`, nothing run. `promote` and `change-source` are never taken from a
  binary's manifest: a feed is never moved by a failover.
- **Settings, per plugin**: the manifest declares them (`config.PluginSetting`, now with a
  `default`); the cluster's values are a `[[repl-plugin]]` entry (`config.ReplPluginEntry`, found
  under any name of the plugin), saved by the save callback (T25), a secret encrypted (F9) and
  never in the JSON (F10). Each run, `-create` and action gets them as `REPMAN_REPL_SETTING_<NAME>`
  and `REPMAN_REPL_SECRET_<NAME>`: the cluster's value, else the manifest's default; a secret held
  in clear never reaches the plugin. The existing keys (`replication-plugin-*`) are not entries:
  they stay keys, read and written into the feed's TOML as before, the cluster level of their
  plugin. The snapshot plugins' environment is built by the same function (`pluginSettingsEnv`).
- **Read through the object**: `AnalyticFeedStatus` (the DuckDB child's feed: the plugin and the
  link's name added), the indexes ready of the API and MCP (`ReplicationPluginIndexesReady`, over
  `Cluster.CDCReplications`), MCP `get-database-replication` (every replication with its plugin and
  its ends).
- Tests: `TestCDCPluginNamesAndAliases`, `TestCDCBinaryOfEitherName`,
  `TestDuckDBChildFeedIsAReplication` (the three names of the destination, the JSON and its
  decode, the status through the object), `TestAnalyticPluginPathAcrossNames`,
  `TestCDCAdapterRunsTheBinaryActions` (a binary with actions and settings),
  `TestCDCAdapterBinaryWithoutActions` (a binary of before: nothing run), and
  `TestReplPluginSettingsLevels` (the levels, the save, the read back beside the existing keys).

**Still missing after step 4:**

- The binaries of the plugin-signer repository keep their names and manifests of before: they run
  through the aliases. Renaming them (with their signatures) and adding the replication fields to
  their manifests is a delivery of that repository, not a change of repman.
- No binary implements an action yet (`<binary> <action>`): the copy and apply verbs of every
  CDC feed answer `ErrReplicationNotSupported` until one declares them. `remove`, `create` and
  `bootstrap` are not run through the binary: `Create` and `Bootstrap` are step 5 (the seed of
  the child), and what a CDC `Remove` leaves (the TOML, the State, the objects on the source,
  `DropReplicationFeedSourceObjects` today) is not decided.
- The `[[repl-plugin]]` entries have no API or GUI yet (step 6, with the GUI of the links): a
  value is set in the cluster's TOML. The `[[repl-link]]` level is not coded.
- The supervisor and the seed still call the binary directly (`-config`, `-create`, `-drop`): the
  `run` of the design, unchanged.

## From step 2 to the target

| Step | What changes |
|---|---|
| 2 | (done) `ReplicationLink` becomes `ReplicationPlugin` + `Link`; the verbs renamed (`StopCopyEvents`, `StopApplyEvents`) and `StartCopyEvents`, `StartApplyEvents` added; the manifest; the registry of plugins (as `newOrchestratorList`) |
| 2b | (done) The `Link` of step 2 replaced by the `Replication` object: each entry of `server.Replications` (and `LastSeenReplications`) a `Replication` of its plugin, filled where `Refresh` stores the rows today, `ReplicationFeeds` merged into the list, the JSON kept; its four ends (`SourceCluster`, `SourceServer`, `TargetCluster`, `TargetServer`); `Promote`, `SetPosition`, `WaitCaughtUp`, `Configure`, `RotateCredential` added; the failover, switchover, reseed and rejoin calls moved onto them; `StopCopyEvents`, `StopApplyEvents` and `ChangeSource` record the link change once their statement succeeded, as `Stop` |
| 3 | (done) The native code split into the built-in plugins: binlog (four manifests: MariaDB, MySQL, MySQL into MariaDB, ColumnStore; Galera and Group Replication topologies), group replication, postgresql; each replication gets its plugin from its engine, the topology and its source |
| 4 | (done) `replication_cdc.go` becomes the external adapter; the CDC plugins declare their settings in their manifests (the `replication-plugin-*` keys stay valid as their cluster level); the CDC binaries renamed (repman finds either name; the rename of the binaries is the plugin-signer delivery) (`replplugin-innodb` → `repl-mariadb-mariadb-cdc`, `replplugin-columnstore` → `repl-mariadb-columnstore-cdc`, `replplugin-duckdb` → `repl-mariadb-duckdb-cdc`, `replplugin-postgresql` → `repl-postgresql-mariadb-cdc`) with their manifests |
| 5 | `Bootstrap` and `Create` moved from `prov.go` into the plugins, topology by topology |
| 6 | GUI: the links of a server grouped by plugin, the actions from the manifest; API and MCP the same |

Each step keeps the statements sent and is tested on dev3 before the next (T13): the
switchover, failover, rejoin and reseed regtests for steps 2 and 3, the CDC regtests for step 4,
the bootstrap of each topology for step 5.

## Settings: per plugin

Decided by the maintainer (2026-11-27): each plugin declares its own settings and secrets in its
manifest (`Settings`), as the snapshot and archive plugins do; the settings of a link are stored per
plugin, never as keys of the repman core:

```toml
[[repl-plugin]]
plugin  = "repl-mariadb-kafka-cdc"
brokers = "..."
```

- A new plugin brings its settings without a change to repman; the GUI builds the form from the
  manifest, the API sets them (global-settings ACL for a secret, as the snapshot plugins), a secret is
  encrypted with the local key (F9) and never logged (F10).
- **Nothing existing breaks: no key is removed, renamed or rewritten** (T16: many are set under
  `/etc`, immutable). Today's keys (`force-slave-*`, `replication-delayed-hosts`,
  `replication-delayed-time`, `replication-source-name`, `replication-master-connect-retry`,
  `replication-use-ssl`, `replication-credential`, `replication-plugin-*`, ...) stay valid as they are
  and **are** the cluster level of their plugin's settings; the plugin reads them as repman does today.
- Three levels, the most specific wins: the plugin's default (its manifest), the cluster's value (the
  existing keys, or a new plugin setting), the link's value. The link level is the only addition:

  ```toml
  [[repl-link]]
  target-server = "db3:3306"
  name          = ""        # the channel
  delay         = 3600
  semisync      = false
  ```

  Without a `[[repl-link]]` the behaviour is today's, unchanged. `replication-delayed-hosts` keeps
  working (a listed host gets `replication-delayed-time`); a `delay` on a link only overrides it for
  that link. A key set under `/etc` keeps winning and stays refused by the API.
- Wanted versus observed (F5): the monitor reads each property in the link's status (`SQL_Delay`, the
  semi-sync state, the heartbeat, the GTID and exec modes) and calls `Configure` when they differ, as
  the `force-slave-*` keys do today; a lasting difference is a state of the config domain (T3).
  `Configure` sets the source side too when a property needs it (semi-sync on the master).
- A deprecation of an old key, if the maintainer ever wants one, comes later and with an alias for at
  least one major version.
- The `trigger` plugins do not exist yet: enterprise, by the maintainer's rule of 2026-11-26.

## T27: what each moved path stops, repoints or resets (step 1)

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
