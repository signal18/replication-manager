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
| **Compute/App** (APU) | 1 | **2 GB** | **10 GB** | **— (none)** |
| **Storage** (backup) | low | low | high | low (TBD) |

A database is **not** an app (proxy/phpMyAdmin): little disk, no IOPS lock. Ratios are
the **operator's rules**, held on the manager as `ratios map[WorkloadProfile]UnitRatios`
and **configurable** (`SetProfileRatios`) — the product does not hard-lock them; the
marketplace "lock" is a commercial policy, nothing is contracted outside these ratios.
An axis with ratio 0 is excluded (never binds) — that is how Compute drops IOPS.

**Network is a planned 5th axis, but NOT a cgroup axis.** cgroup v1 `net_cls`/`net_prio`
only *classify/prioritise* packets (no counters), and cgroup v2 has **no network
controller** at all (per-cgroup network accounting needs an eBPF `cgroup/skb` program).
So network is read from the container's **network namespace** instead —
`/proc/<pid>/net/dev` (rx/tx bytes & packets), the same source cAdvisor (K8s) and the
OpenSVC collectors use — a distinct but equally cheap system-level read. A network axis
would map to the marketplace `Cloud18InfraPublicBandwidth`. Kept in mind, **not wired**:
the current axes are cpu/mem/io/disk (cgroup + statfs); adding network means a 5th axis
sourced from the netns, not the cgroup.

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
- `plan` — the client's **resource RESERVATION contract**, **not a recalculation**. It
  is a **client-set DBU size** (whole DBU units, ratio LOCKED — adjusted by **+1 / −1
  DBU**) and authoritative in its own right: the DBU is **never inferred back** from the
  resources. A **+1 / −1 DBU resizes the resources**: each step adds/removes one whole
  balanced unit (1 core / 4 GB / 40 GB / 1000 IOPS) at the locked ratio. Raising the plan
  **legitimately provisions the resources to the full tier** — the client reserved them
  *because he needs them*, so setting them to the reservation is the expected behaviour,
  **not** an error. But it is a **claim**, and a claim must **not** be applied live at the
  client's sole decision: it is **mediated** — agent-capacity availability, reclaim from
  the overcommit **pool** (burst tenants yielding their best-effort slack), then an
  orchestrated resize — never an instantaneous unilateral client write. A claim **can be
  refused**, for any of a thousand reasons (no free agent capacity, the pool exhausted, a
  quota/policy limit, the orchestrator declining or failing the resize, an immutable axis,
  …): raising the plan is a *request*, not a guaranteed effect. The **first refusal level
  is capacity — "no room"** — decided from the **global multi-cluster consumption view**
  (`ConsumedByAgent` vs `UsableCeilingDBU` → `AgentSlackDBU`), the infra-wide picture only
  repman can net. That global view is therefore not merely a dashboard: it **is** the first
  gate of the claim. The one exception:
  **a resource made immutable (fixed by the admin)**
  is never touched by `+1/−1`; it stays at the admin-imposed value. The **bug** is the
  *reverse* dependency — the GUI *reconstructing* the DBU as `ceil(max(prov-db-*/ratio))`
  (configurator `DBUSlider`), which conflates the measured/provisioned size with the
  reservation contract and breaks the moment one axis is edited or admin-fixed alone; it
  is what the explicit **`AddDBU`/`RemoveDBU`** must replace. ⚠️ No dedicated DBU plan
  field exists yet: **today the reservation contract is carried by the marketplace
  `ServicePlan`** (`prov-service-plan`, sourced from the `prov-service-plan-registry` CSV
  — raw resources + an absolute €), and crucially it is **NOT expressed in DBU/APU**
  (CLOUD18_CREDIT_MODEL.md §3.2 "no credit field today — the plan is `ServicePlan`"). That
  gap — a contract in raw €/resources instead of units — is exactly what the DBU/APU model
  closes. Keeping the contract in the CSV `ServicePlan` **forces over-administration of the
  sale prices** (every plan's € hand-curated in the `prov-service-plan-registry` sheet) and
  is **tightly constrained to the DB perimeter** (it does not generalise to apps/storage).
  Per-unit pricing instead sets a few €/unit and **derives every price across all workload
  profiles** (CLOUD18_CREDIT_MODEL.md §2.1). The `ServicePlan` stays a provisioning
  template, not a unit contract (the self-declared tier `Cloud18SubscriptionPlan` sits
  alongside it). When the dedicated
  DBU/APU plan field is added it mirrors the app credit model (`Cloud18ApplicationCredits*`
  → `Cloud18DatabaseCredits*`; `prov-app-credit-planned` → the DB plan).

Aggregated views (`DBUAggregate`: per-axis + a global pivot = the binding axis):
- `ConsumedByCluster` / `ConsumedByAgent`
- `PlanByCluster`
- `DeltaByCluster` = **contract − real** (the economic signal, below)
- `AgentSlackDBU` = usable ceiling − real on the agent (the pool)

Per **agent** (physical, capping — an agent is NOT a contract):
- `AgentCapacity` — monitored + modifiable per axis (cores/mem/disk from node stats;
  iops from a multi-core sysbench calibration, #1779). `UsableCeilingDBU` = metal ×
  `cap%` (`resource-manager-infra-quota-pct`), which protects non-repman workloads.

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
to `ServerMonitor.IngestDBUMaxes`, which calls `ResourceManager.ComputeUsedDBU` (Database
profile ratios) and stores. The ratios live **only** on the manager (one source of
truth). ⚠️ `marketplace-pricing` (Ahmad) is a **pre-refund, bottom-up** DB billing
attempt: its `ComputeUsedDBUPerNode` **derives** DBU from the *provisioned* resources and
hardcodes the ratio — at the merge it must defer to the manager (T20). The model here is
**top-down** (client-set plan ceiling; **real measured** below; gap = **refund**), so the
consumed DBU is a projection of the *measured* consumption, not of the provisioned size.
The refund is only meaningful because the resource is **actually resized** toward the
real (dynamic-resource plugin / `prov-db-dynamic-resource-change-script`), not merely
re-accounted.

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

## Resize gates & saturation trigger (SETTLED 2026-09)

The dynamic resource resize is governed by **two ORTHOGONAL gates — never conflate them**:

- **`CanConfigResize` (feasibility / apply)** — the `ResourceResizer` interface method (per
  orchestrator; a client `prov-db-dynamic-resource-change-script` overrides, F7). Plan-agnostic;
  it **never refuses** a resize, it only decides **how** the infra follows: **live** (cgroup à
  chaud / SET GLOBAL) or a **restart** fallback. It is the ONLY gate for an **in-plan** resize —
  the config drives the system, so a change within the plan always lands.
- **`ResourceManager.CanGrowBeyondPlan(target, plan, pct)` (budget / authorise)** — consulted
  **ONLY when the target exceeds the plan**. Permits up to `plan × (1 + prov-db-overcommit-pct/100)`
  (the commercial scalability-up ceiling the client accepted); beyond it the growth is REFUSED
  and the plan must be raised (a claim). In-plan resizes never call it — that is why the name
  says *BeyondPlan*.

  | | authorise | apply (live / restart) |
  |---|---|---|
  | **in-plan** | *(always yes — no budget gate)* | `CanConfigResize` |
  | **beyond-plan** | `CanGrowBeyondPlan` | `CanConfigResize` |

**State definition.** `IsNeedResourceCapUp` (bool) + `ResourceCapUpAxes` (`[]string`,
cluster.go) = **at least one DB node is saturating at least one resource axis**, and the list
names WHICH axes. It is a **monitoring signal only** — nothing consumes it yet (the resize is
still admin/plan-triggered; see Status). The **axes are recorded** because the consequence
differs (mem → OOM, disk → full, cpu → throttle, io → latency), so the eventual per-axis resize
/ alert must distinguish them.

**Trigger — SATURATION, per axis.** It lights up when ANY **real node** — the worst node, never
a `Σ/N` average that would hide a hot master — has `consumed_axis ≥ config_axis × (1 −
prov-db-cap-safety-pct/100)` on ANY axis (default 15 % → fire at 85 %). There is deliberately
**no `config ≥ plan` path**: being provisioned AT the plan is the normal state (config == plan),
which would light the signal permanently for every properly provisioned cluster. Config-driven,
resource-termed (on-prem-compatible, not DBU/plan-specific).

**Measurement source & window (defines what "consumed" means).** Consumption comes from the DBU
sensor `share/scripts/dbjobs_new.sh` → `collect_dbu`, which runs **once per dbjobs_new invocation,
~60 s cadence**, inside each DB container. It reads the database cgroup and pushes to
`/api/clusters/<c>/servers/<h>/<p>/dbu`:
- **CPU & IO = the MEAN rate over the ~60 s window** (differential of cgroup `cpu.stat`
  usage_usec / `io.stat` rios+wios between two runs, ÷ dt) — so saturation is a **~60 s
  sustained** condition, not a sub-second spike.
- **Mem & disk = instantaneous** at the sample (`memory.current`, `df` under the datadir).

Repman re-emits the last reading every monitor tick (~2 s) for a continuous graph, but the
underlying value only refreshes per sensor push (~60 s). A down / never-measured node is skipped
(contributes no axis). **Flap caveat:** the value being a 60 s mean checked per tick, a
consumption hovering at the threshold would flap the derived workload state — so wiring it to the
WorkloadStateMachine needs **hysteresis** (open 85 % / close ~75 %) + `pstatesN` preservation.

**Granularity of a size-up = one DBU-equivalent on the SATURATED axis** (mem +4096 MB, cpu
+1 core, io +1000 iops, disk +40 GB) — not a whole DBU (would grow idle axes) and not a free
native step (would drift off the DBU grid). The grow follows `ResourceCapUpAxes`.

**Vocabulary (settled):** `prov-db-cap-burst-dbu` = TECHNICAL cgroup headroom above the config
(anti-OOM), NOT overcommit. *Overcommit* = OVER-consumption (`consumed > plan`), *undercommit* =
under-consumption (`plan > consumed`) — both DERIVED in the GUI from graphite (`diffSeries`),
nothing emitted. `prov-db-overcommit-pct` = the commercial scale-up ceiling above.

## Status / TODO

Implemented: the substrate above, plus the per-axis **emission** of consumed metrics
(`service_*` raw + `dbu_*` DBU translation, srv_snd.go) and the **GUI graph** that reads
them — `ChartGroupedDBU` (grouped bars per axis: real conso → DBU, pivot max line, plan
line = the configurator ceiling the GUI reads but does not own).
Follow-ups: `SetPlan` wiring — the plan is a **client-set DBU size** (whole units); a
**+1/−1 DBU resizes `prov-db-*`** at the locked ratio (except admin-immutable resources)
and is **NOT** derived from them; future dedicated `Cloud18DatabaseCredits*` vars mirror
the app credit model, driven by `AddDBU`/`RemoveDBU`. Also `SetServerAgent` /
`SetAgentCapacity` from physical monitoring (#1778), APU compute wiring for apps/proxies,
per-cluster/agent/minute **emission** (the data), then the burst/overcommit **policy**
and the heatmap.

Resize gates (above) are DEFINED but **not wired**: `CanGrowBeyondPlan` has no caller and
`IsNeedResourceCapUp`/`ResourceCapUpAxes` are a **signal nothing consumes yet**. There is **no
autonomous (saturation-driven) trigger** — a resize fires only from `SetDB*` via the API, the
CLI configurator, or a plan apply (`applyPlanSpec`). Wiring needs a **target-first** restructure
of the `SetDB*` setters (compute target → gate → mutate; today they mutate then resize). And the
**infra grow is memory-only**: `SetDBCores`/`SetDBDiskIOPS` only re-tune DB SET GLOBAL vars (no
cgroup resize; CPU cgroup limit resize is a marked follow-up), disk has no live path — so a real
multi-axis grow (needed for granularity-C on cpu/io/disk) is still missing.

## On-premise (first-class, not cloud18-only)

The whole model applies **on-premise**, not only in the cloud18 marketplace. The technical
core is identical — unit projection (DBU/APU), capacity monitoring, the mediated
claim/resize, refund on the measured gap; **only the commercial envelope changes**:
- **agent capacity** = the client's **own metal** (same physical-first monitoring);
- the "price" is a **license quota**, not a marketplace €/unit — and **over-quota is
  state + alert, NEVER a gate** on monitoring or on the running DB (F-laws);
- the claim's dynamic resize runs through **on-prem drivers** (reuse the OpenSVC
  zfs/lvm/snapshot drivers, or a client hook `prov-db-dynamic-resource-change-script`),
  not a cloud orchestrator.

So the DBU/APU contract, the claim, and the resize plugin must never assume the cloud18
marketplace is present.

## Laws

T7 (unified interface — capacity is orchestrator-agnostic), T16 (ratios/params in TOML
when made configurable), T18 (Graphite is the bounded history, no in-memory buffer),
T20 (one conversion source — reconcile `ComputeUsedDBUPerNode`), T6 (GUI for the editable
capacity). Commercial axis (refund %, overage, tiered price) stacks on top and never
gates the technical path.
