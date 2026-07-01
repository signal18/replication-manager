# Monitor Loop Flow Diagram

Phased execution of the cluster monitor tick. `TopologyDiscover` must complete
before any other task reads `Servers`. Tasks within each phase run in parallel.

```
Server Heartbeat (every tick)
    │
    ├── For each peer:
    │   └── HTTP GET /api/heartbeat
    │       ├── Success → SplitBrain = false (peer reachable)
    │       └── Failure → SplitBrain = true  (peer unreachable)
    │
    ├── Log split brain transition if changed
    │
    └── Propagate SplitBrain to all clusters
            │
            ▼
Cluster Monitor Loop (every tick, per cluster)
    │
    ├── Phase 1: TopologyDiscover() alone
    │   ├── newServerList()
    │   ├── State alerts: WARN0079/80/90
    │   └── wg.Wait() ← Servers stable after this
    │
    ├── Fire-and-forget (no sync):
    │   ├── go MonitorSchema()
    │   ├── go CheckSlavesReplicationsPurge()
    │   ├── go initOrchetratorNodes()        (every 30)
    │   ├── go CheckCredentialRotation()     (every 30)
    │   └── go RefreshAllAppTemplateMD5()    (every 3600)
    │
    ├── Phase 2: parallel (reads stable topology)
    │   ├── ArbitratorHandler() ─────────────────────────┐
    │   │    └── if Arbitration AND IsSplitBrain:         │
    │   │         ├── SetArbitratorReport()               │
    │   │         │   ├── Compute IsLostMajority          │
    │   │         │   ├── POST /heartbeat to arbitrator   │
    │   │         │   └── Set IsFailedArbitrator          │
    │   │         └── if transition:                      │
    │   │              ├── Sleep 5s                       │
    │   │              └── ArbitratorElection() (3x)      │
    │   │                   ├── "winner" → Active         │
    │   │                   └── "loser"  → Standby        │
    │   │                        └── LostArbitration()    │
    │   ├── refreshProxies()                              ├── parallel
    │   ├── refreshApps()                                 │
    │   ├── MonitorTableSchemaDiff()    (every 10)        │
    │   ├── ResticFetchRepo()           (every 30)        │
    │   ├── MonitorVariablesDiff()      (every 30)        │
    │   └── MonitorVariablesChange()    (every 30) ───────┘
    │   └── wg.Wait()
    │
    └── Phase 3: parallel (depends on Phase 2)
        ├── MonitorQueryRules()         (needs proxies)
        ├── InjectProxiesTraffic()      (needs proxies)
        ├── ResticPurgeRepo()           (needs fetch, every 3600)
        ├── CheckAppsCredit, CheckWaitRunJobSSH, ...
        ├── CheckJobsVersion            (every 10)
        ├── 12× Check* functions         (every 30)
        ├── RefreshToolVersions, ...     (every 3600)
        └── SendGraphiteMetrics, ...     (every 5)
        └── wg.Wait()
```
