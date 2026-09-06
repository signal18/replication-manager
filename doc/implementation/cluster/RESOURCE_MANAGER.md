# ResourceManager — repman-side infra-wide resource authority

**Status:** substrate implemented (this increment); wiring + emission + policy are follow-ups.
**Code:** `cluster/resource_manager.go`, `cluster/srv_dbu.go`. **Epic:** #1776. **Tasks:** #1777/#1778/#1779.

## Why

Only repman sees every service running on a given agent and can net their
consumption, so the infra-wide resource picture belongs to it. The `ResourceManager`
is that authority: a single object, created once by `ReplicationManager` and injected
into every `Cluster` (`SetResourceManager`), living **above** the `ServerMonitor` and
the `Cluster` — both recreated on a config reload, which is exactly what used to blank
the in-memory reading and make the DBU graph flap.

**Principle: physical first, bubbled up.** Capacity is observed on the metal via
monitoring, then aggregated upward (service → cluster → agent → infra). Units (DBU,
APU) are projections on top — never the reverse.

## Units are projections, resources are the substance

DBU and APU are **workload-classified unit projections** over native resources
(cores / mem / disk / iops). Storing DBU is fine — conversion is trivial. What differs
per workload is the **ratio**, not just the price:

| Profile (unit) | cpu | mem | disk | iops |
|---|---|---|---|---|
| **Database** (DBU) | 1 | 4 GB | 40 GB | **1000** (locked) |
| **Compute/App** (APU) | 1 | 4 GB | **10 GB** | **— (none)** |
| **Storage** (backup) | low | low | high | low (TBD) |

A database is **not** an app (proxy/phpMyAdmin): little disk, no IOPS lock. Ratios are
the **operator's rules**, held on the manager as `ratios map[WorkloadProfile]UnitRatios`
and **configurable** (`SetProfileRatios`) — the product does not hard-lock them; the
marketplace "lock" is a commercial policy, nothing is contracted outside these ratios.
An axis with ratio 0 is excluded (never binds) — that is how Compute drops IOPS.

## Two SEPARATE tracks (a DB is not an app)

| | DB track | App track |
|---|---|---|
| key | `ResourceKey{Cluster, Server}` | `AppKey{Cluster, App}` |
| reading | `DBUReading` (`Dbu*` fields) | `APUReading` (`Apu*` fields) |
| profile | Database | Compute |
| wire series | `…dbu` | `…apu` |

The tracks never mix; they only meet at **agent capacity** (shared physical metal).
Both reuse the same normalisation math (`UnitRatios.project`) — same math, different
ratios, distinct output types.

## What the manager holds & exposes

Per **service** (a "server" is a DB service; a proxy/app is another kind of service):
- `consumed` — real, measured, pushed by the sensor. `nil` when a service is off or
  not configured to send metrics (never fabricated).
- `plan` — the client's **technical contract**, from `prov-db-*` config. Always known.

Aggregated views (`DBUAggregate`: per-axis + a global pivot = the binding axis):
- `ConsumedByCluster` / `ConsumedByAgent`
- `PlanByCluster`
- `DeltaByCluster` = **contract − real** (the economic signal, below)
- `AgentSlackDBU` = usable ceiling − real on the agent (the pool)

Per **agent** (physical, capping — an agent is NOT a contract):
- `AgentCapacity` — monitored + modifiable per axis (cores/mem/disk from node stats;
  iops from a multi-core sysbench calibration, #1779). `UsableCeilingDBU` = metal ×
  `cap%` (`cloud18-infra-repman-quota-pct`), which protects non-repman workloads.

## Restore on reload (the graph flap)

`SetDBUConsumed` writes both the `ServerMonitor` field and the manager. On a config
reload the `ServerMonitor` is recreated with `DBUConsumed = nil`; `newServerMonitor`
calls `RestoreDBUConsumed` (after `ClusterGroup` is set), which reloads the last
reading from the manager. So repman keeps **emitting** across the reload window and
Graphite gets no hole. NB: Graphite stores the history; the manager only holds the
last value — the flap was an **emission** gap, not a storage loss. This is a
side-effect, not the manager's reason to exist.

## The conversion entry point

The sensor push handler (`server/api_database.go`) stays dumb: it forwards raw maxima
to `ServerMonitor.IngestDBUMaxes`, which calls `ResourceManager.ComputeDBU` (Database
profile ratios) and stores. The ratios live **only** on the manager (one source of
truth). ⚠️ At the `marketplace-pricing` merge, `ComputeDBUPerNode` (Ahmad) hardcodes
the same ratio and must defer to the manager (T20).

## Economic model (the WHY — policy is a follow-up)

- Two things evolve continuously: **real** (consumed, via non-instant dynamic capping)
  and **contract** (plan, client-driven — lower it to cut waste, or commit bigger for a
  cheaper reserved €/unit). Because resize is not instantaneous, a coherent contract
  carries headroom to **absorb bursts** while capping catches up.
- **delta = contract − real.** Positive = over-contracted (partial refund of only a %
  of the unused → lower the contract). Negative = **overage**: compensated from the
  agent **pool** and/or billed at a premium, **hard-capped by agent capacity**
  (`Σ real ≤ ceiling`).
- **Overcommit:** under-consumers' slack funds over-consumers' bursts, bounded by the
  metal.
- **Anti-abuse (1 DBU contract + 80 DBU Saturday batch):** two-tier QoS — reserved
  (`Σ plans`) is **guaranteed + priority**; burst is **best-effort up to K× contract**,
  only from leftover pool, and yields to reserved reclaim, so it can never starve a
  properly-contracted neighbour. `DBUReading.Dbu` is the **peak** over the window, so
  the spike is captured (billable), not averaged away.
- **Graphite is the history store** (repman's embedded carbon): manager emits → Graphite
  stores → policy reads history back (by querying the cluster) to detect temporal abuse
  patterns and shape pricing. In-memory = instant only (no in-memory time-series; T18).
- **GUI vision:** a per-agent × per-minute price-ratio **heatmap** — a "cluster weather
  map" (green = slack/cheap, red = saturated/expensive) — the reference a client/ops
  consults before overcommitting, so bursts move to "good weather" (demand-shaping).

## Status / TODO

Implemented: the substrate above (build green, DBU output unchanged, DBU test green).
Follow-ups: `SetPlan` wiring (contract from `prov-db-*`, per tick), `SetServerAgent` /
`SetAgentCapacity` from physical monitoring (#1778), APU compute wiring for apps/proxies,
per-cluster/agent/minute **emission** (the data), then the burst/overcommit **policy**
and the heatmap.

## Laws

T7 (unified interface — capacity is orchestrator-agnostic), T16 (ratios/params in TOML
when made configurable), T18 (Graphite is the bounded history, no in-memory buffer),
T20 (one conversion source — reconcile `ComputeDBUPerNode`), T6 (GUI for the editable
capacity). Commercial axis (refund %, overage, tiered price) stacks on top and never
gates the technical path.
