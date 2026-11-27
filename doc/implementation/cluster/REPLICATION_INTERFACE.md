# Replication plugins: one interface for every link

Issue #2453. Status: **design validated by the maintainer (2026-11-27)**. Steps 1 and 2 (below,
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
(and `LastSeenReplications` while it is down), filled by its `Refresh` (`readNativeLinks`) during
`TopologyDiscover`, which then builds the graph (who is the master, who replicates from whom). Nothing
new is added beside it (T2): each entry becomes an interface, `Replication`, implemented by the
plugin of that link. The discovery stays `TopologyDiscover`'s: where `Refresh` stores the rows it
read, it stores them as `Replication` objects of their plugin.

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
    Promote(reason string) (string, error)            // this edge ends, its target becomes a source
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
- `Promote` by engine: MariaDB and MySQL `RESET SLAVE ALL` on that replication then `read_only` off;
  PostgreSQL `pg_promote()`; Group Replication `group_replication_set_as_primary`; Galera nothing to do
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
| `repl-mariadb-s3-cdc` | cdc | S3 by partition or by whole table | external | enterprise |
| `repl-mariadb-s3files-cdc` | cdc | Parquet files in a bucket (also the migration path) | external | enterprise |
| `repl-mariadb-kafka-cdc` | cdc | a topic per index | external | enterprise |
| `repl-mariadb-redis-cdc`, `-memcached-cdc`, `-rabbitmq-cdc` | cdc | keys, messages | external | free |
| `repl-mariadb-clickhouse-cdc`, `-postgresql-cdc`, `-sphinx-cdc` | cdc | copy, denormalize | external | enterprise |
| `repl-postgresql-mariadb-cdc` | cdc | migration (bidirectional designed, not coded) | external | enterprise |
| `repl-<source>-<target>-trigger` | trigger | a source whose log can not be read | external | enterprise |

The CDC destinations, their apply and their position are the private CDC design
(`doc/enterprise-denormalize-plugin.md` in the plugin-signer repository): one plugin per
destination, each in copy and denormalize mode, S3 by periodic partition injection or by whole-table
rebuild and swap. Tiers: the maintainer's decision of 2026-11-26.

## Two ways to implement the same contract

### Built in: the native plugins

- Built into the repman binary, never an external process: a failover never waits on one (F2).
- **Binlog native** is one Go type registered three times (`repl-mariadb-mariadb-native`,
  `repl-mysql-mysql-native`, `repl-mysql-mariadb-native`) with three manifests. The manifest decides
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

## Where the code is today (steps 1 and 2, coded and tested)

- `cluster/replication.go`: the `ReplicationPlugin` interface, `ReplicationManifest`,
  `ReplicationTopology`, the `ReplicationAction*` names (the external contract's: `start-copy`,
  `stop-apply`, ...), and `Link` (the plugin, the server it writes into, the channel or feed name,
  the plugin's configuration of it). `Link`'s methods check the manifest, then call the plugin with
  the link: an action the manifest does not list answers `ErrReplicationNotSupported` before any
  call. `server.ReplicationLink()`, `ReplicationChannelLink(name)` and `ReplicationLinks()` return
  `Link` values.
- `cluster/replication_registry.go`: the registry, built as the orchestrators'
  (`newReplicationPluginList` beside `newOrchestratorList`, `cluster.ReplicationPlugin(name)` never
  nil: a null plugin with no action for an unknown name), and `unsupportedReplicationPlugin`, which
  each plugin embeds for the actions it does not implement.
- `cluster/replication_native.go`: the native plugin, one manifest (`repl-mariadb-mariadb-native`)
  for every engine until step 3, the code moved from `srv.go`, `srv_set.go` and the status read,
  the statements unchanged. `StartCopyEvents` and `StartApplyEvents` are new (`START SLAVE
  IO_THREAD` / `SQL_THREAD` of the channel, `dbhelper.StartSlaveIOThread` / `StartSlaveSQLThread`;
  PostgreSQL has no separate thread: an error, no statement). The lock test
  `TestNoReplicationLinkSQLOutsideBackend` keeps the link SQL there.
- `cluster/replication_cdc.go`: the CDC plugin, one generic manifest (`repl-cdc`) for every binary
  until step 4: status, start, stop, rejoin, over the feed's TOML, State and status line.
- Bootstrap and Create are in the interface; both plugins answer `ErrReplicationNotSupported`
  ("step 5") and the bootstrap stays in `prov.go`.
- `cluster/srv_link_change.go`: the link changes as states (WARN0352).
- Tested on dev3 (2026-11-27), each step: switchover with stopped replicas, failover, mysqldump
  reseed; no regression against release/4.0.1 (details in the pull request).

## From step 2 to the target

| Step | What changes |
|---|---|
| 2 | (done) `ReplicationLink` becomes `ReplicationPlugin` + `Link`; the verbs renamed (`StopCopyEvents`, `StopApplyEvents`) and `StartCopyEvents`, `StartApplyEvents` added; the manifest; the registry of plugins (as `newOrchestratorList`) |
| 2b | The `Link` of step 2 replaced by the `Replication` object: each entry of `server.Replications` (and `LastSeenReplications`) a `Replication` of its plugin, filled where `Refresh` stores the rows today, `ReplicationFeeds` merged into the list, the JSON kept; its four ends (`SourceCluster`, `SourceServer`, `TargetCluster`, `TargetServer`); `Promote`, `SetPosition`, `WaitCaughtUp`, `Configure`, `RotateCredential` added; the failover, switchover, reseed and rejoin calls moved onto them; `StopCopyEvents`, `StopApplyEvents` and `ChangeSource` record the link change once their statement succeeded, as `Stop` |
| 3 | The native code split into the built-in plugins: binlog (three manifests), galera, group replication, postgresql, columnstore |
| 4 | `replication_cdc.go` becomes the external adapter; the CDC plugins declare their settings in their manifests (the `replication-plugin-*` keys stay valid as their cluster level); the CDC binaries renamed (`replplugin-innodb` → `repl-mariadb-mariadb-cdc`, `replplugin-columnstore` → `repl-mariadb-columnstore-cdc`, `replplugin-duckdb` → `repl-mariadb-duckdb-cdc`, `replplugin-postgresql` → `repl-postgresql-mariadb-cdc`) with their manifests |
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
