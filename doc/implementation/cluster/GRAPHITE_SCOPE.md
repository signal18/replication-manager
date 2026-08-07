# Graphite scope and behavior

## Purpose

This note records the current Graphite scope model in repman and the safe
rule for issue #1675 work: **fix the queue leak without changing existing
Graphite routing behavior**.

## Current scope split

### Repman-wide / embedded-carbon process

These settings describe the embedded carbon service started by the repman
process itself, so they are operationally repman-wide:

- `graphite-embedded`
- `graphite-carbon-api-port`
- `graphite-carbon-server-port`
- `graphite-carbon-link-port`
- `graphite-carbon-pickle-port`
- `graphite-carbon-pprof-port`

Code evidence:

- embedded carbon starts once from `repman.Conf` in `server/server.go`
- Graphite reverse-proxy endpoints use `repman.Conf` in `server/http.go`
  and `server/api.go`

### Per-cluster runtime state

Each cluster owns its own Graphite runtime state via `ClusterGraphite` in
`cluster/cluster_graphite.go`:

- metric queue (`metrics`)
- live connection (`gc`)
- flush guard (`flushing`)
- dropped-metric counters
- cluster WARN state when the sink backs up

This means the queue leak in issue #1675 is a **per-cluster** failure mode,
even when multiple clusters share one repman instance.

### Per-cluster filter content

Whitelist and blacklist file content is per cluster:

- `<workingdir>/<cluster>/whitelist.conf`
- `<workingdir>/<cluster>/blacklist.conf`

So the actual tracked metric patterns are already cluster-specific.

## `graphite-carbon-host` and `graphite-carbon-port`

These two are the messy part of the current design.

### How they are declared/administered today

They are tagged `scope:"server"` in `config/config.go` and are handled by
the global settings path in `server/api_global_settings.go`.

So by config/API model, they are treated as shared server-scoped settings.

### How they are consumed at runtime

Metric sending does not read them directly from `repman.Conf`; it reads them
from each cluster's live config object in `cluster/cluster_graphite.go` when
building the TCP client connection.

Graphite API readers also use `cluster.Conf`, via `cluster.graphiteAPIURL()`
in `cluster/srv_log_plugins.go`.

### Practical interpretation

Today they are best described as:

> **server-scoped operational settings, consumed through per-cluster config
> objects at runtime**.

That is not a clean architecture, but it is the existing behavior and should
not be changed as part of issue #1675.

## Embedded behavior

When `graphite-embedded` is on, current code already forces Graphite API reads
to `127.0.0.1` in `cluster/srv_log_plugins.go`.

Metric TCP writes currently still use `cluster.Conf.GraphiteCarbonHost` and
`cluster.Conf.GraphiteCarbonPort` from `cluster/cluster_graphite.go`.

This asymmetry is part of the current behavior. It may deserve a dedicated
cleanup later, but it is **out of scope** for the queue-bound fix.

## Scope rule for issue #1675

To preserve existing behavior while still putting the new setting in the
correct place:

- keep all existing Graphite settings/scopes unchanged
- make only the new queue-bound setting cluster-scoped:
  `graphite-metrics-queue-limit`

Reason:

- the bounded queue is per cluster
- drops are counted per cluster
- the WARN state is raised per cluster
- changing the scope of only the new setting does not redefine any existing
  Graphite routing semantics

## Recommended implementation boundary

For issue #1675, the allowed change set is:

- add a hard queue cap
- drop oldest into a fresh backing array
- track dropped metrics
- raise a sustained WARN state
- reuse the Graphite connection when healthy

The following are explicitly **not** part of the fix:

- redefining embedded-vs-external Graphite routing
- rescoping existing Graphite settings
- making Graphite destination semantics more coherent

Those belong in a separate Graphite architecture cleanup issue.

## Queue-bound implementation (issue #1675)

The mechanics implemented within the boundary above.

### The cap

`Config.GraphiteMetricsQueueLimit` (`config/config.go`), no `scope:"server"`
tag — see "Scope rule for issue #1675" above. CLI flag
`--graphite-metrics-queue-limit` defaults to `100000`
(`server/server.go`). `<= 0`, including unset, falls back to the same
hardcoded default via `ClusterGraphite.queueLimit()`
(`cluster/cluster_graphite.go`) — never unbounded.

### Drop policy

`ClusterGraphite.boundMetrics()` (`cluster/cluster_graphite.go`), shared by
both `AddMetrics()` and `requeue()`. Combines the older and newer metric
slices and, once over the cap, drops from the front (oldest first) into a
**freshly allocated slice** — not a forward-reslice of the existing backing
array — so dropped metric strings are actually released for GC.

### Drop tracking and the sustained-drop WARN state

`ClusterGraphite.DroppedMetricsTotal` (`atomic.Uint64`) is a monotonic
counter incremented on every trim. Separately, `checkSustainedDrops()`
(`cluster/cluster_graphite.go`) runs once per flush cycle and raises
`WARN0192` (`config/error.go`) only once drops persist across
`graphiteSustainedDropThreshold` = 2 consecutive flush cycles — a single
transient trim on an otherwise healthy sink must not alert.

State is raised via `cg.cl.SetState(...)` on the main `cluster.StateMachine`
— not `WorkloadStateMachine`, which is scoped to the monitored DB's own
workload/log-plugin domain. This is repman's own operational health, the
same category as `WARN0100`/`WARN0139`/`WARN0140`.

Preserved across non-flush ticks in `cluster/cluster.go`, but via its own
conditional — `if cluster.Conf.GraphiteMetrics && heartbeats%5 != 0 {
PreserveState("WARN0192") }` — separate from the pre-existing
`PreserveState("WARN0139", "WARN0140")` call. Bundling all three under one
condition (`!(GraphiteMetrics && heartbeats%5==0)`) would make WARN0192
self-sustain forever once `graphite-metrics` is turned off: that condition
goes permanently true (first operand false), so `PreserveState` would keep
re-adding WARN0192 from `OldState` every tick indefinitely, with nothing
left to ever resolve it (`SendGraphiteMetrics` never runs to clear it).
Requiring `GraphiteMetrics` in WARN0192's own condition means it simply
stops being preserved — and lapses within one tick — the moment Graphite is
disabled.

### Connection reuse

`graphite.Graphite.Connect()` (`graphite/graphite.go`) now returns
immediately if `conn != nil` instead of closing and redialing on every
flush. `sendMetrics()` nils `conn` on either a `SetWriteDeadline` or
`Write` failure, so the next `Connect()` call correctly redials a broken
connection rather than reusing a dead one.

### Where it's set

Cluster-scoped via `setClusterSetting` (`server/api_cluster.go`, grouped
next to `graphite-whitelist-template`) and the "Graphite Metrics Queue
Limit" row in `share/dashboard_react/src/Pages/Settings/GraphSettings.jsx`
(existing `NumberInput` component, same pattern as `DB Log Rotate Max
Size`).

### Tests

Unit: `cluster/cluster_graphite_test.go`, `graphite/graphite_test.go`,
`server/api_cluster_test.go` (`TestSetClusterSetting_GraphiteMetricsQueueLimit*`).

Regtest (T13 — real cluster, not a mock): `testGraphiteMetricsQueueBound`
(`regtest/test_graphite_metrics_queue_bound.go`, registered in
`regtest/regtest.go` and `server/regtest.go`). Points the live cluster's
Graphite connection at a guaranteed-refused TCP address, feeds it bursts of
metrics well over the configured cap, and asserts:

- queue length never exceeds the cap (`ClusterGraphite.QueueLength()`, an
  accessor added for this purpose — `cg.metrics` is otherwise unexported so
  its bound can't be observed or bypassed from outside the package);
- `WARN0192` is raised once drops persist across sustained flush cycles —
  checked via **both** `StateMachine.CurState.Search(...)` and
  `IsInState(...)` (which reads `OldState`), since each has the opposite
  blind spot against the real concurrent monitor loop's own
  `ClearState()` rotation: `CurState` can be wiped by a rotation racing
  right after this scenario's own call raised it, while `IsInState` won't
  see a freshly raised state until the *next* rotation completes (which
  depends on the ambient `monitoring-ticker` cadence, not this scenario's
  own retry loop) — either one seeing it is sufficient;
- a flush succeeds again once connectivity is restored — against a
  **locally-started accept-and-discard listener**, not the cluster's
  original/ambient Graphite destination, since that destination's
  reachability inside any given regtest environment isn't actually
  guaranteed (embedded carbon may be disabled, or the scenario's
  `config.toml` may not configure a real sink) and isn't part of #1675's
  acceptance criteria anyway. This also exercises the real
  reconnect-after-failure path, since the broken connection was nilled on
  failure and must be redialed, not reused.

Written to tolerate the real concurrent per-tick monitor goroutine also
running against the same live cluster throughout (bounded retries /
inequality checks rather than assuming exclusive access, and no assertion
depends on ambient environment state), unlike the deterministic bare-struct
unit tests.
