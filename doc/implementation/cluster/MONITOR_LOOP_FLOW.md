# Monitor Loop Flow Diagram

Phased execution of the cluster monitor tick. `TopologyDiscover` must complete
before any other task reads `Servers`. Tasks within each phase run in parallel.

## Server Level

The server-level heartbeat runs once per tick and detects split brain by pinging
peer repman instances. The result propagates to all clusters.

```
Server Heartbeat (every tick)
    │
    ├── For each peer:
    │   └── HTTP GET /api/heartbeat
    │       ├── Success → SplitBrain = false
    │       └── Failure → SplitBrain = true
    │
    └── Propagate SplitBrain to all clusters
```

## Cluster Level

Each cluster runs its own monitor tick with three synchronized phases and
a set of fire-and-forget background tasks.

```
Cluster Monitor Loop (every tick, per cluster)
    │
    ├── Phase 1: TopologyDiscover() ← alone, must finish first
    │   ├── newServerList() rebuilds Servers
    │   ├── State alerts: WARN0079 (split brain)
    │   │                 WARN0080 (lost majority)
    │   │                 WARN0090 (failed arbitrator)
    │   └── wg.Wait() ← Servers stable after this point
    │
    ├── Fire-and-forget (long-running, no barrier):
    │   ├── go MonitorSchema()               schema scan via INFORMATION_SCHEMA
    │   ├── go initOrchetratorNodes()        HTTP to OpenSVC/K8S (every 30)
    │   ├── go CheckCredentialRotation()     DB credential rotation (every 30)
    │   ├── go RefreshAllAppTemplateMD5()    HTTP per app template (every 3600)
    │   └── ReconcileSnapshotMetadataAsync() spawns own goroutine internally
    │
    ├── Phase 2: parallel (reads stable topology) ─────────────────────────┐
    │   ├── ArbitratorHandler()                                            │
    │   │    └── if Arbitration AND IsSplitBrain:                          │
    │   │         ├── SetArbitratorReport()                                │
    │   │         │   ├── Compute IsLostMajority                           │
    │   │         │   ├── POST /heartbeat to arbitrator                    │
    │   │         │   └── Set IsFailedArbitrator                           │
    │   │         └── if transition (IsSplitBrainBck != IsSplitBrain):     │
    │   │              ├── Sleep 5s                                        │
    │   │              └── ArbitratorElection() (3 retries)                │
    │   │                   ├── "winner" → SetActiveStatus("A")            │
    │   │                   └── "loser"  → SetActiveStatus("S")            │
    │   │                        └── LostArbitration()                     │
    │   ├── refreshProxies()                                               │
    │   ├── refreshApps()                                                  │
    │   ├── MonitorTableSchemaDiff()         (every 10)                    │
    │   ├── ResticFetchRepo()                (every 30)                    │
    │   ├── MonitorVariablesDiff()           (every 30)                    │
    │   └── MonitorVariablesChange()         (every 30)                    │
    │   └── wg.Wait() ─────────────────────────────────────────────────────┘
    │
    ├── Phase 3: parallel (depends on Phase 2) ────────────────────────────┐
    │   ├── MonitorQueryRules()              needs proxies (every 30)      │
    │   ├── InjectProxiesTraffic()           needs proxies                 │
    │   ├── ResticPurgeRepo()                needs fetch (every 3600)      │
    │   ├── CheckSlavesReplicationsPurge()   reads slave state             │
    │   ├── CheckAppsCredit()                                              │
    │   ├── CheckWaitRunJobSSH()                                           │
    │   ├── CheckDummyConfigSendCookies()                                  │
    │   ├── CheckRestartContainerCookies()                                 │
    │   ├── PrintDelayStat()                                               │
    │   ├── CheckJobsVersion()               (every 10)                   │
    │   ├── HasValidBackup()                 (every 30)                    │
    │   ├── HasCatalogBackupForRejoin()      (every 30) WARN0190/WARN0191   │
    │   ├── CheckCanSaveDynamicConfig()      (every 30)                    │
    │   ├── CheckIsOverwrite()               (every 30)                    │
    │   ├── CheckAllBackupEstimatedSize()    (every 30)                    │
    │   ├── CheckAvailableCredit()           (every 30)                    │
    │   ├── CheckOpenSVCTresholds()          (every 30)                    │
    │   ├── JobsCheckSchedulerTable()        (every 30)                    │
    │   ├── CheckOnPremiseSSHKey()           (every 30)                    │
    │   ├── CheckConfiguratorPrerequisites() (every 30)                    │
    │   ├── CheckGlobalDeprecatedKeys()      (every 30)                    │
    │   ├── CheckClusterDeprecatedKeys()     (every 30)                    │
    │   ├── CheckClusterServiceAgents()      (every 30)                    │
    │   ├── RefreshToolVersions()            (every 3600)                  │
    │   ├── CheckBackupToolVersions()        (every 3600)                  │
    │   ├── CheckComplianceUpdate()          (every 3600)                  │
    │   ├── ReloadDockerRepos()              (every 3600)                  │
    │   ├── SendGraphiteMetrics()            (every 5)                     │
    │   └── CheckDisksUsage()                (every 5)                     │
    │   └── wg.Wait() ─────────────────────────────────────────────────────┘
    │
    ├── PreserveState for non-running ticks
    │
    ├── StateProcessing() → detect resolved/new alerts
    ├── CheckAlert() → fire notifications
    └── ClearState() → cycle OldState ← CurState
```

## Why Fire-and-Forget

Fire-and-forget tasks are long-running I/O operations that would block the
tick if waited on. Each has a reentry guard to prevent piling up:

| Task | I/O type | Guard |
|------|----------|-------|
| `MonitorSchema` | SQL `INFORMATION_SCHEMA` scan | `IsInSchemaMonitor` + 60s cooldown |
| `initOrchetratorNodes` | HTTP to orchestrator API | `inInitNodes` bool |
| `CheckCredentialRotation` | DB queries per slave | `inConnectVault` bool |
| `RefreshAllAppTemplateMD5` | HTTP per app template | `IsHashingTemplate` per app |
| `ReconcileSnapshotMetadataAsync` | Disk I/O for snapshot files | `atomic.CompareAndSwap` |

These tasks do NOT call `SetState()`, so they cannot interfere with the
state machine lifecycle (ClearState → SetState/PreserveState → StateProcessing).

## Why Three Phases

| Phase | Barrier | Reason |
|-------|---------|--------|
| 1 → 2 | `wg.Wait()` | Phase 2 reads `Servers`, which Phase 1 rebuilds |
| 2 → 3 | `wg.Wait()` | Phase 3 needs proxy state from `refreshProxies` and fetch data from `ResticFetchRepo` |

Without barriers, `ArbitratorHandler` would read `hosts=0` during `newServerList()`
rebuild (the Hosts=0 bug), and `MonitorQueryRules` would read stale proxy state.

## Monitor connections are bounded by the connect timeout (#2196)

`TopologyDiscover` holds the cluster lock while it pings the servers, so a monitor connect
must end quickly whatever answers on the port. `connectMonitorDSN` (and the extra-port
fallback) open their connections through `connectBounded`: `sqlx.ConnectContext` with a
deadline of `db-servers-connect-timeout` (5 s by default), which go-sql-driver/mysql and
lib/pq apply to the dial, the server greeting and the ping. The DSN's `readTimeout`
(`db-servers-read-timeout`, an hour) stays for queries only: before this, a port that does
not speak first, as MySQL does (a PostgreSQL server declared without `/postgres`, any other
service), held the connect, the cluster lock and every waiter of that lock for an hour at
each attempt.

`addserver` with the `postgresql` type declares `host:port/postgres` (default port 5432),
the form the PostgreSQL app uses (`registerEngineAppAsServer`): in a topology other than
pg-stream or pg-logical, the suffix is what makes a server PostgreSQL.

### A failed connect returns no pool

`sqlx.Connect` closes the pool when its ping fails; `sqlx.ConnectContext` (sqlx v1.4.0) does
not: it returns the pool, still open, with the error. Every caller of a monitor connect
drops the connection when the connect failed, so from v3.1.47 (the switch to
`connectBounded`) each failed connect left one `database/sql` pool and its
`connectionOpener` goroutine behind: one per failing server per tick, millions after a few
days, until the monitor ran out of memory. `connectBounded` closes the pool and returns nil
on error, the contract of `sqlx.Connect`; `Ping` also closes any connection handed back
with an error before its failure branch, whose returns (the access denied early return
among them) never keep it.

`connectMonitorDSN`'s TLS retry with the old certificate assigns the connection it opens
instead of shadowing it, so the working connection is returned, not the failed first one.

Guards: `TestFailedMonitorConnectClosesItsPool` and `TestPingOnFailingServersLeavesNoPool`
(`cluster/srv_monitor_pool_leak_test.go`) run many connects and monitor ticks on a server
that refuses, one that closes during the handshake and one that answers access denied
(1045), and count the open pools (their `connectionOpener` goroutines) before and after.
