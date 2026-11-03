# ProxySQL offline hostgroup and master recovery

Tracks: https://github.com/signal18/replication-manager/issues/2295
PR: https://github.com/signal18/replication-manager/pull/2296
Related: #2171 (same UNIQUE class of bug in `SetWriter` / `SetReader`)

## Problem

A master that came back from Failed through StandAlone could stay
`OFFLINE_SOFT` in the writer hostgroup on every ProxySQL. Writes then failed
(`ERROR 9001 ... Max connect timeout reached while reaching hostgroup 0`) until
fixed by hand. repman logged once:

```
Monitor ProxySQL setting online failed server <host>:<port>: Error 1045 (28000):
ProxySQL Admin Error: UNIQUE constraint failed: mysql_servers.hostgroup_id,
mysql_servers.hostname, mysql_servers.port
```

and never retried.

## The offline hostgroup

With `proxysql-bootstrap`, repman owns the server rows of its writer and reader
hostgroups, plus one more: `OfflineHG` (`666`, `router/proxysql`). This is where
a writer row is parked while its server must not take writes (standalone,
ignored, failed over). No user or query rule routes to it. It is repman's, not
a legacy hostgroup.

`mysql_servers` has `PRIMARY KEY (hostgroup_id, hostname, port)`, so a server
can have a row in 666 and one in the writer hostgroup at the same time, and
moving one onto the other fails.

| Writes a 666 row | Statement |
|---|---|
| `AddOfflineServer` (bootstrap `Init()` for a StandAlone or ignored server, staging) | `REPLACE INTO ... VALUES ('666', ...)` |
| `SetOffline` (failover, StandAlone tick) | `UPDATE ... SET hostgroup_id='666' WHERE hostgroup_id=<W>` |

| Removes a 666 row | Statement |
|---|---|
| `SetOnline` only | `UPDATE ... SET hostgroup_id=<W> WHERE hostgroup_id=666` |

Bootstrap `Init()` runs once after the first topology discovery, on every repman
start. For the leader it calls `AddServerAsWriter`, a `REPLACE INTO <W>`, which
leaves any 666 row alone. A server that was once standalone and later
bootstrapped as leader therefore keeps a stale 666 row. With
`proxysql-save-to-disk`, that row survives ProxySQL restarts.

## Failure sequence (before the fix)

1. **Failed.** No `Refresh()` branch handles `stateFailed`. Without a
   promotion, `Failover()` does not run. The writer row stays in `<W>`, and
   ProxySQL shuns it at runtime only.
2. **StandAlone tick** (`s.State == stateUnconn` branch). Both actions only ran
   on rows ProxySQL reported `ONLINE`:
   - `SetOffline` moved `<W>` → 666 and hit the stale 666 row (UNIQUE,
     reported as `ERR00070`). The writer stayed in `<W>`.
   - `SetOfflineSoft` filters on `hostgroup_id IN (<R>, <W>)`, so it drained
     the **writer** row along with the reader row.
3. **Master tick.** `ExistAsWriterOrOffline` matches `IN (666, <W>)`, so the
   `<W>` row alone selected the `SetOnline` path. `SetOnline` moved 666 onto
   `<W>`: UNIQUE.
4. **Stuck.** `PrevState` was now Master and `isBackendWriter` true (the
   OFFLINE_SOFT row is listed in `stats_mysql_connection_pool`), so the
   reintroduction branch was never entered again. Only `ERR00091` remained.

### Timing and environment (lab, #2295 comment 6601499605)

- Whether step 2 drains the writer is a race. If client traffic gets ProxySQL
  to unshun the server before repman detects the recovery, the rows are
  `ONLINE` and the drain runs (full outage). If repman detects it first, the
  rows are still `SHUNNED`, the branch is skipped, and the writer stays
  `ONLINE` (UNIQUE logged, no outage). Steady application traffic makes the
  first case likely.
- The bug is the same on ProxySQL 2.4.8 and 3.0.11.
- A `mysql_replication_hostgroups` row hides it. After three missed read_only
  checks, ProxySQL deletes the writer row itself (`Assuming read_only=1`), and
  `SetOnline` then moves 666 → `<W>` without a conflict.

## Fix

### Driver (`router/proxysql/proxysql.go`)

- `dropShadowed(host, port, drop, keep)` deletes the server's row in `drop` when
  it also has one in `keep`. `moveToHostgroup` (#2171) now uses it too.
- `SetOnline`: `dropShadowed(666, <W>)`, then
  `UPDATE ... SET status='ONLINE', hostgroup_id=<W> WHERE hostgroup_id IN (666, <W>)`.
  It works whether the server's row is in 666, in `<W>`, or in both. When both
  exist, the `<W>` row is kept because it carries the tuned `weight`,
  `max_connections` and `use_ssl`, while `AddOfflineServer` writes defaults.
  `status` is now reset, which the old `SetOnline` never did.
- `SetOffline`: `dropShadowed(666, <W>)`, then the move `<W>` → 666.
- `SetReaderOfflineSoft`: drains `<R>` only.
- `HasRowInHostgroup`: used by the regtest.

The delete and the update are two statements, not a transaction. If the update
fails after the delete, only the `<W>` row is left, which is the target state,
and the next tick retries.

### Monitor (`cluster/prx_proxysql.go`, `Refresh()`, bootstrap branch)

- **StandAlone:** the writer condition is `isBackendWriter && PrxStatus !=
  "OFFLINE_HARD"` instead of `== "ONLINE"`, so a writer row still `SHUNNED`
  after the outage is parked too. `OFFLINE_HARD` is a row already removed that
  the connection pool still lists, and is skipped so the branch does not fire
  every tick. With `topology-staging`, the same condition drives `DropWriter`.
  The reader drain uses `SetReaderOfflineSoft`.
- **New reconciliation branch:** when `s.IsLeader() && isBackendWriter &&
  PrxStatus == "OFFLINE_SOFT" && !s.IsMaintenance && !cluster.IsInFailover()`,
  it calls `SetOnline` on every tick until the row is `ONLINE`.
- A failed `SetOnline` or `AddServerAsWriter` raises `ERR00071` (it was a log
  line). Its missing third argument (`%!s(MISSING)`) is fixed. `AddState` keys
  the state on code + proxy name, so a persistent failure is one open state per
  proxy, not one alert per tick.

### Operator-visible behaviour change

In bootstrap mode, repman now owns the master's writer status. An operator who
drains the master directly in ProxySQL (`UPDATE mysql_servers SET
status='OFFLINE_SOFT'`) without putting it in repman maintenance will see it
set back `ONLINE` on the next tick. To drain a master, use repman maintenance,
which sets `OFFLINE_SOFT` on both hostgroups and is respected
(`!s.IsMaintenance`). Without `proxysql-bootstrap`, the branch is unreachable,
because the whole block is guarded by `cluster.Conf.ProxysqlBootstrap`.

## Tests

- Unit (sqlmock, `router/proxysql/proxysql_test.go`):
  `TestSetOnlineWithStaleOfflineRow`, `TestSetOfflineWithStaleOfflineRow`,
  `TestSetReaderOfflineSoftLeavesWriter`.
- Regtest `testProxySQLStaleOfflineRowRecovery`
  (`regtest/test_proxysql_stale_offline_row.go`, targets `proxysql = true` and
  `proxysql-bootstrap = true`). On every ProxySQL of the cluster, it adds a
  stale 666 row for the master and drains its writer row, then expects the
  writer back `ONLINE` with no 666 row within 60 s. Cleanup sets back `ONLINE`
  the rows the seed drained (an UPDATE, so it never adds a reader row).
- Lab verification (PR #2296, OpenSVC, MariaDB 10.6.28, ProxySQL 3.0.11 ×1 and
  2.4.8 ×2): the stuck state left by develop was repaired in 1–2 s, the
  full-outage replay showed no UNIQUE error and the writer was back `ONLINE` in
  3–5 s, and the regtest passed on both.

## Not covered

- The master's own reader row (`proxy-servers-read-on-master`) drained by the
  StandAlone tick stays `OFFLINE_SOFT`; no branch brings it back, and
  `ERR00091` stays open. Tracked in #2298.
- The staging topology was not exercised in the lab.
- The new statements are built with `fmt.Sprintf`, like the rest of the
  driver. Parameterized queries are a separate change.
