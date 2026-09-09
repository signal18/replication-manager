# Maintenance Mode Persistence

Tracking issue: [#1783](https://github.com/signal18/replication-manager/issues/1783).

## Problem

`ServerMonitor.IsMaintenance` was runtime-only. It was set/cleared by
`SetMaintenance()` / `DelMaintenance()` / `SwitchMaintenance()`
(`cluster/srv_set.go`, `cluster/srv_del.go`, `cluster/srv_tgl.go`) but never
serialized. A process restart or a live config reload rebuilds every
`ServerMonitor` from scratch (`newServerMonitor`, `cluster/srv.go`), which
zero-values `IsMaintenance` back to `false` — a server an operator pulled out
of rotation silently rejoined proxy routing and failover election on the next
restart, including maintenance set automatically by failover logic
(`cluster/cluster_fail.go`) when a slave can't safely rejoin.

## Design

Maintenance membership is durable now, following the exact pattern already
used for `db-servers-ignored-hosts` (`IgnoreSrv`): a per-cluster comma-separated
host-membership string in `config.Config`, with matching, add and remove
helpers on `Cluster`.

### Persisted field

`config.Config.MaintenanceSrv` — `db-servers-maintenance-hosts`
(`config/config.go`). Bound as a Cobra flag in `server/server.go`. Written to
the cluster's dynamic TOML by the existing `ConfigManager.SaveConfig` /
`Cluster.Save()` flow — **no new writer**.

### Membership helpers (mirroring `cluster_has.go` / `cluster_set.go` /
`cluster_get.go`'s `IgnoreSrv` triplet — with two deliberate departures from
that precedent, below)

- `maintenanceListHasHost(hostList, url, name)` / `maintenanceTokens(hostList)`
  (`cluster/cluster_has.go`) — the single membership predicate, comparing
  comma-separated tokens with **exact equality only**. `IgnoreSrv`'s
  equivalent (`SetIgnoreSrv`) matches with `strings.Contains(list, srv.URL)`;
  that is a real substring hazard (`"1.2.3.4:3306"` is a literal substring of
  `"11.2.3.4:3306"`, so a Contains-based match would silently pull an
  unrelated server into maintenance). Maintenance is safety-critical enough
  that this file does not inherit that shortcut — every membership check
  (restoration, mutation) goes through this one predicate so they can't
  disagree.
- `Cluster.IsInMaintenanceHosts(server)` (`cluster/cluster_has.go`) — exact
  match against `Conf.MaintenanceSrv` via `maintenanceListHasHost`, false for
  child-cluster servers. Used both as the membership check and as the
  restart-restoration source (see below).
  There is no `GetMaintenanceHostList` (unlike `IgnoreSrv`'s
  `GetIgnoredHostList`, which reconstructs its list from live
  `cluster.Servers` and is therefore lossy for a host temporarily absent from
  `cluster.Servers`): `Add`/`RemoveMaintenanceSrv` below read
  `Conf.MaintenanceSrv` directly instead of going through a getter, and no
  caller outside this package currently needs the whole list, so a getter
  with no caller was dropped rather than shipped speculatively. Add one (or an
  API endpoint) when something needs to read the full durable list.
- `Cluster.SetMaintenanceSrv(newList)` (`cluster/cluster_set.go`) — the
  in-memory assignment/synchronization helper: dedupes and writes `newList`
  into `Conf.MaintenanceSrv`, then syncs the runtime `IsMaintenance` flag for
  every *currently-instantiated* server to match it (servers absent from
  `cluster.Servers` have no runtime flag to sync — their token is left
  untouched in `Conf.MaintenanceSrv` for the next restore). It does **not**
  call `ConfigManager.SaveConfig` itself — it's the pure state-transform step,
  reused below.
- `AddMaintenanceSrv(node)` / `RemoveMaintenanceSrv(node)`
  (`cluster/cluster_set.go`) — edit the existing token list from
  `Conf.MaintenanceSrv` directly (append/filter a token), never rebuilding it
  from `cluster.Servers`, so a token for a host not currently instantiated
  survives an unrelated add or remove. Each calls `SetMaintenanceSrv` with the
  updated list, then calls `ConfigManager.SaveConfig(cluster, false)` itself
  after a real membership change (a no-op add/remove, e.g. adding an
  already-tracked host, returns before either call). None of them run the
  state-change script or touch proxies — that stays the job of
  `SetMaintenance` / `DelMaintenance` / `SwitchMaintenance`, which call them as
  one more side effect alongside the existing script/proxy calls.
  `DelMaintenance` and `SwitchMaintenance`'s clear branch log a warning
  (`ConstLogModGeneral`/`LvlWarn`) rather than discard `RemoveMaintenanceSrv`'s
  error: not expected in practice (`IsMaintenance` was just true), but a
  future membership-tracking bug should surface in logs, not degrade
  silently. `SetMaintenance`/`AddMaintenanceSrv`'s add path has no error to
  discard — it's a no-op when already tracked.

### Restoration on startup and reload

Both process startup and `Cluster.ReloadConfig()` route through
`InitFromConf()` → `newServerList()` → `newServerMonitor()` (confirmed by
reading the call graph — there is exactly one `newProxyList()` call site,
inside `InitFromConf`, so one restoration point covers both paths). In
`newServerMonitor` (`cluster/srv.go`), alongside the existing
ignored/preferred restoration:

```go
server.IsMaintenance = cluster.IsInMaintenanceHosts(server)
```

This assigns the field directly rather than calling `SetMaintenance()`:
restoration is reconciliation, not a new state transition, so it must not
replay `db-servers-state-change-script` or send a duplicate proxy
notification (that happens once, explicitly, right after).

### Proxy reconciliation

`newProxyList()` runs after `newServerList()` inside `InitFromConf`, so
proxies are rebuilt with no knowledge of the maintenance state just restored
onto `Servers`. Immediately after `newProxyList()`:

```go
for _, server := range cluster.Servers {
    if server != nil && server.IsMaintenance {
        cluster.SetProxyServerMaintenance(server.ServerID)
    }
}
```

replays the same notification `SetMaintenance()` sends, so
HAProxy/ProxySQL/MaxScale converge immediately instead of waiting for their
next refresh cycle.

### Automatic (failover-triggered) maintenance

Per product decision, maintenance set automatically by internal failover
logic is persisted identically to operator/API-triggered maintenance — there
is only one code path (`SetMaintenance`), so this required no special
handling. A restart never silently returns a server to service, regardless of
who put it into maintenance.

## Prerequisite: `monitoring-save-config`

Durability depends on the existing `monitoring-save-config` flag (default
`true`). With it explicitly disabled, dynamic cluster configuration —
including maintenance membership — is not written and does not survive
restart. This is existing, documented behavior (see CLAUDE.md's
"Environment Variable Conflicts" note); maintenance persistence introduces no
new exception to it.

## No new API/CLI surface; UI copy updated

The existing maintenance endpoints (`server/api_database.go` toggle/set/clear
handlers, `clients/client_server.go`) already route through `SetMaintenance` /
`DelMaintenance` / `SwitchMaintenance`, so they inherit persistence without
code changes. Their Swagger `@Description` annotations were updated to state
the persistence contract and the `monitoring-save-config=true` dependency
(`server/api_database.go`), and the dashboard's maintenance confirmation
dialog (`share/dashboard_react/.../DBServers/ServerMenu.jsx`) now shows the
same note in its confirm-modal body so an operator sees it before confirming.

## Test coverage

### Unit tests (`cluster/cluster_maintenance_test.go`)

- exact URL/name membership matching, no substring collisions (`db1` vs
  `db10`), and child-cluster exclusion;
- a dedicated regression test for the `SetMaintenanceSrv` substring hazard
  above (`"11.2.3.4:3306"` configured, `"1.2.3.4:3306"` must stay untouched);
- a dedicated regression test that `Add`/`RemoveMaintenanceSrv` preserve a
  persisted entry for a host absent from `cluster.Servers` while mutating a
  different, present server;
- a dedicated test for the `entry := strings.ReplaceAll(node.URL,
  node.Domain+":3306", "")` normalization `AddMaintenanceSrv` copies from
  `SetIgnoreSrv`, against a domain-qualified server (`Domain != ""`, the
  `GetDomain()`/`GetDomainHeadCluster()` case the other tests don't cover):
  both the default-port form (stripped to the bare name) and a non-default
  port (left as the full domain-qualified URL) round-trip through
  `IsInMaintenanceHosts` for a freshly rebuilt `ServerMonitor`;
- `AddMaintenanceSrv` / `RemoveMaintenanceSrv` update both the durable
  membership and every server's runtime flag, request persistence
  (`IsNeedConfigSave`), and are idempotent / error on a no-op remove;
- `SetMaintenance` / `DelMaintenance` / `SwitchMaintenance` persist membership
  alongside the runtime flag;
- a freshly constructed `ServerMonitor` (simulating what `newServerMonitor`
  does on restart/reload) restores `IsMaintenance` from durable membership,
  and does not resurrect it once cleared.

### Live reload/rebuild regtest (partial T13): `regtest/test_maintenance_persist_reload.go`

Registered as `testMaintenancePersistReload` (`regtest/regtest.go`,
`server/regtest.go`), runnable standalone (`--test=testMaintenancePersistReload`)
or as part of `ALL` against a live, already-provisioned cluster. There is no
separate repman process for a regtest to kill and restart, so it drives
`cl.ReloadConfig(*cl.Conf)` directly. This exercises the real server/proxy
reconstruction sequence — `ReloadConfig` → `InitFromConf` → `newServerList` →
`newServerMonitor` → `newProxyList`, the same sequence both process startup
and the config-reload API endpoint (`ReplicationManager.ReloadClusterConfig` →
`mycluster.ReloadConfig`) run, and there is only one `newProxyList()` call
site in the codebase — but it is **not equivalent to a process restart**: it
passes the already-mutated in-memory `*cl.Conf` straight into `ReloadConfig`,
so it does not start a fresh process and does not prove that startup's config
loading (Viper reading the TOML file back off disk) merges the persisted
artifact correctly. That gap is exactly what `SaveConfigFile()` + reading the
file back (step 2 below) is there to narrow, but a real Docker process
restart remains the final T13 validation (see below).

Flow: `SetMaintenance()` on a replica (the same entry point the API uses) →
assert durable membership and, if a recognized proxy (HAProxy/ProxySQL) is
attached, assert the read backend drains → `SaveConfigFile()` and assert the
persisted TOML contains the entry → `ReloadConfig` (live rebuild, not a
process restart) → re-fetch the server by URL (the old pointer is stale
post-reload) and assert `IsMaintenance == true` (the same field the API's
JSON response serializes) and the proxy backend stayed excluded →
`DelMaintenance()` → `SaveConfigFile()` → `ReloadConfig` again → assert
maintenance was **not** resurrected and the proxy backend returned.

What this does verify, on a real cluster: the TOML artifact is actually
written; the real reconstruction lifecycle (`newServerMonitor`/`newProxyList`)
restores maintenance from configuration; rebuilt proxies converge correctly.
What it does not verify: that a fresh process's own startup config-load path
reads the same artifact back the same way.

Not exercised in the session that authored this: an actual run against a live
Docker DB/proxy matrix (it compiles and vets cleanly, and reuses the same
live-cluster helpers `test_proxy_read_backend_reconciliation.go` already
exercises in CI, but was not executed here — see PR for CI results).

### Remaining gap: process-level restart (T13, pre-release)

Before release, run the isolated Docker process-restart scenario this file
above stops short of: set maintenance through the API, stop the
`replication-manager` process, start a fresh one against the same
datadir/config, and assert via the API that `isMaintenance=true` and the
proxy backend is still excluded — then clear and repeat to confirm
non-resurrection. Tracked on issue #1783.

## User documentation

`docs.signal18.io` content does not live in this repository (no submodule; the
in-repo `doc/api_latest.md` and `docs/swagger.*` are the generated Swagger
reference, not the prose user guide). The source-of-truth `@Description`
annotations in `server/api_database.go` were updated with the persistence
contract (see above); regenerating `docs/swagger.*` / `doc/api_latest.md` from
them via `swag.sh` was tried and then deliberately left out of this change —
that regeneration also pulled in several already-merged, previously
ungenerated fields unrelated to maintenance (`maxscaleMode`,
`provDbComplianceAutoAgree`, etc.), and `swag.sh` isn't a tracked/Makefile
step this repo's contributor workflow otherwise runs per PR. Regenerate
separately, or as part of the ordinary docs refresh cadence. The prose user
guide entry for maintenance mode on `docs.signal18.io` — that it is now
durable across restart/reload, and depends on `monitoring-save-config=true` —
still needs to be written on that site; tracked as a follow-up on issue #1783
since it's outside this repo's reach.
