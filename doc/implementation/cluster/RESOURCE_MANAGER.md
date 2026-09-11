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
| **Compute/App** (APU) | 1 | **1 GB** | **10 GB** | **— (none)** |
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

**States are PER SERVER; the plan signals COMPOSE at the cluster.** Consumption is measured per
server, resources within the plan are managed per server, so the atomic states live on the
`ServerMonitor` — never a `Σ/N` cluster average that would hide a hot master. All are **signals
only** — no action, no mutation, no resize (that is composed downstream; nothing consumes them
yet, see Status). The axes are recorded (not a bare bool) because the consequence differs per axis
(mem → OOM, disk → full, cpu → throttle, io → latency).

`ServerMonitor.CheckResourceConsumed()` (checkState) sets, from THIS server's own `DBUConsumed`,
four per-axis states — two references × two directions, with a **dead-band** between the
high-water (`prov-db-cap-safety-pct`, default 15 % → over at 85 %) and the low-water
(`prov-db-cap-shrink-pct`, default 50 % → under at 50 %) so it never flaps:

| server state | consumed vs | condition (per axis) | consequence |
|---|---|---|---|
| `ResourceConsumedOverConfigAxes`  | **config** (`GetConfigDBUPerNode`) | `≥ config × (1 − safety%)` | **raise THIS server's resources** (saturation) |
| `ResourceConsumedUnderConfigAxes` | **config** | `≤ config × shrink%`        | **shrink THIS server's resources** |
| `ResourceConsumedOverPlanAxes`    | **plan / cap** (`GetPlanDBUPerNode`) | `≥ plan × (1 − safety%)` | feeds cap-**up** |
| `ResourceConsumedUnderPlanAxes`   | **plan / cap** | `≤ plan × shrink%`          | feeds cap-**down** |

`Cluster.CheckResourceCapPlan()` (checkState) then COMPOSES the plan signals from the per-server
`*Plan` states — **one server suffices to force OR to break the action**:
- `IsNeedResourceCapUp`   = **ANY** up server over the plan (one hitting the envelope forces ↑);
- `IsNeedResourceCapDown` = **EVERY** up server under the plan (one non-under server BREAKS ↓ —
  safe-shrink: never lower the plan while any server still needs it).

The **cap is already set at the plan**, so `IsNeedResourceCapUp` stays false while consumed <
plan − margin. There is deliberately **no `config ≥ plan` path** (being provisioned AT the plan is
the normal state — would fire permanently). Natural progression: consumption first saturates a
server's config → raise that server's resources within the plan; once every server's consumption
climbs to the plan envelope → cap up; when all fall back under → cap down.

**Measurement source & window (defines what "consumed" means).** Consumption comes from the DBU
sensor `share/scripts/dbjobs_new.sh` → `collect_dbu`, which runs **once per dbjobs_new invocation,
~60 s cadence**, inside each DB container. It reads the database cgroup and pushes to
`/api/clusters/<c>/servers/<h>/<p>/dbu`:
- **CPU & IO = the MEAN rate over the ~60 s window** (differential of cgroup `cpu.stat`
  usage_usec / `io.stat` rios+wios between two runs, ÷ dt) — so saturation is a **~60 s
  sustained** condition, not a sub-second spike.
- **Mem & disk = instantaneous** at the sample (`memory.current`, `df` under the datadir).

**Memory occupancy is NOT a scaling signal — the demand axes are cpu/io (+disk).** A healthy
InnoDB buffer pool is *always* ~full (clean + dirty pages, adaptive hash index, change buffer),
so `dbu_mem` (cgroup occupancy) is pinned near the cap regardless of load: it never legitimately
means "grow" (always saturated) and never means "shrink" (memory is sticky — the pool does not
release on idle; only a restart or an explicit `SET GLOBAL` buffer-pool-down frees it). So
`CheckResourceConsumed` **excludes the mem axis** from all four consumed-vs-reference states
(`dropMem`). Growth is driven by **CPU usage and IO saturation** (and disk usage); real memory
NEED surfaces *as IO* — a too-small buffer pool causes misses (`Innodb_buffer_pool_reads` → disk
reads), which the io axis already sees. Memory SHRINK is the deliberate reclaim path, never an
occupancy trigger. **Memory grow now rides pressure** (`checkBufferPoolPressure`): a rising
`Innodb_buffer_pool_wait_free` (InnoDB waited for a free page — threshold-free) sustained over
`prov-db-scale-up-config-in-plan-speed` sets `BufferPoolMemGrowDue`, which `CanScaleConfigInPlan(up)`
folds back into the mem axis (so `CINF0007` fires on mem only under real pressure, and resolves when
it clears). Read from in-memory `server.Status` via `GetStatusDeltaValue` (the BP counters are
whitelist-gated in graphite, so not reliably queryable there). An io-saturation trigger is
disambiguated into grow-memory vs grow-iops **not** by a static hit-ratio heuristic but by the
QPS-driven escalation (see "Dynamic-resize trigger" below): grow memory first, and escalate to IOPS
only when another memory step stops improving QPS.

Repman re-emits the last reading every monitor tick (~2 s) for a continuous graph, but the
underlying value only refreshes per sensor push (~60 s). A down / never-measured node is skipped
(contributes no axis). **Flap caveat:** the value being a 60 s mean checked per tick, a
consumption hovering at the threshold would flap the derived workload state — so wiring it to the
WorkloadStateMachine needs **hysteresis** (open 85 % / close ~75 %) + `pstatesN` preservation.

**Granularity of a size-up = one DBU-equivalent on the SATURATED axis** (mem +4096 MB, cpu
+1 core, io +1000 iops, disk +40 GB) — not a whole DBU (would grow idle axes) and not a free
native step (would drift off the DBU grid). The grow follows `ResourceCapUpAxes`.

**Vocabulary (settled):** the container cgroup `--memory` cap = the DBU tier × mem-ratio, where
the tier is chosen by `prov-db-resource-align`: `plan` (default) = the per-node plan, or `up` =
the per-node config rounded up to the next DBU (`GetProvDbuFromConfigPerNode`). No extra offset.
*Overcommit* = OVER-consumption (`consumed > plan`), *undercommit* = under-consumption
(`plan > consumed`) — both DERIVED in the GUI from graphite (`diffSeries`), nothing emitted.
`prov-db-overcommit-pct` = the commercial scale-up ceiling above the plan.

## Two memory limits: live working memory vs the container ceiling

A DB container has **two** distinct memory limits, changed by two different mechanisms:

1. **Working memory — live, no restart.** The in-plan resize
   (`cluster_resize_dynamic.go`, gated by `prov-db-dynamic-resource`) moves the *running*
   memory and reconfigures MariaDB in lockstep, orchestrator-agnostically:
   - `ResourceResizer.ConfigResize` changes the cgroup live — OpenSVC via the om3 **PG update**
     (`pg_mem_limit`, `PGUpdateInstanceV3`); Kubernetes via the **in-place Pod `resize`
     subresource** (1.27+, `k8sResizer`, on branch `k8s-proxy`). No container recreate.
   - the shared orchestration then runs `resizeMemorySQL` (`SET GLOBAL innodb_buffer_pool_size`
     + key_buffer / tmp_table / join_buffer / max_session_mem_used …).
   - anti-OOM ordering: **grow** = cgroup up → buffer pool up; **shrink** = buffer pool down →
     (deferred, async-aware) cgroup down, never below live memory
     (`completePendingCgroupShrink`).

2. **The container ceiling — only on recreate.** The docker `--memory` in the OpenSVC
   `container#db` run_args (`GetDBContainerMemoryCapMB`) is the outer hard limit, set at
   container **create**. It is the plan tier (`prov-db-resource-align`, default `plan` →
   `(plan DBU / #nodes) × mem-ratio`), deliberately above `prov-db-memory` so the live resize
   has headroom to grow into. It cannot change without recreating the container.

**`prov-orchestrator-deployment-upgrade-on-start`** (default on) is what applies (2). On each
node (re)start in a rolling restart/upgrade, `UpgradeDatabaseDeploymentOnStart` (prov.go)
re-renders and pushes the full deployment BEFORE start, so the recreated container comes up on
the current service config — the plan-driven ceiling, image, run_args, env — instead of the
one written at the last provision. OpenSVC v3 → `OpenSVCUpdateDatabaseTemplate` (full re-push);
K8s → the on-develop image-update path (container resources stay owned by `k8sResizer`).
Non-fatal in the rolling loop: a push failure leaves the previous cap and never breaks the
restart. So: the live path moves working memory under the ceiling with no restart; this gate
raises the ceiling itself, on the next restart, when the plan grows past it.

## Live-resize timing: `prov-db-dynamic-resize-policy`

The live memory resize adjusts `innodb_buffer_pool_size`. On a MariaDB without the modern
instant/resizable buffer pool (MDEV-29445), that resize is *chunked* — quantized to a
non-dynamic `innodb_buffer_pool_chunk_size` grid (re-tuning it needs a restart) **and** it can
**stall** the workload while it runs (especially a shrink, which withdraws pages). We do **not**
block the resize on old releases — we control *when* it happens so a stall lands at a chosen
time. Two orthogonal timing knobs:

- **`prov-db-scale-*-speed`** (existing) — *how long* saturation must persist before a resize is
  **triggered** (the sustained-saturation throttle; see the gate section above).
- **`prov-db-dynamic-resize-policy`** (this) — *when* a triggered memory resize is **applied**:
  - **`scale-speed`** (default) — apply immediately; cadence is the scale-speed timeframe. (No
    separate "live" value — that IS the scale-speed-governed default.)
  - **`daily-time`** — apply only inside the daily window `prov-db-dynamic-resize-daily-time`
    (`HH:MM`, server-local), so any buffer-pool-resize stall is contained to an off-peak hour.

`maintenance` and `restart` are deliberately **not** policy values: a resize deferred to a
restart already rides the maintenance window via the deployment-upgrade-on-start gate, so they
collapse into "defer to restart" (`prov-db-dynamic-resource = off`), not a resize-timing mode.

Mechanics (`cluster_resize_dynamic.go`): the memory branch of `ResizeDynamicResources` is gated
by `dynamicMemoryResizeApplyAllowedNow()` — under `daily-time` outside the window it skips the
apply; `DriveDailyDynamicResize()` (called per tick from `SetStatus`) reconciles live memory to
the provisioned target once per day when the window opens (`LastDynamicResizeDay` guard,
`dynamicMemoryResizeDue` decides grow/shrink and skips a no-op). CPU/IO tuning never routes
through the gate (no stall) and is unaffected.

**Gated by jobs execution.** A live memory resize never runs on a server while a dbjob
(backup/optimize/reseed) is executing there (`server.IsRunningJobs`) — a buffer-pool resize or
cgroup change under a heavy job risks OOM/contention and could fail the job. This gate applies
to BOTH policies: `scale-speed` skips a busy server (retries on the next trigger), and because
backups typically run at the same off-peak hour as the daily window, `daily-time` opens a
`dynamicResizeWindowMinutes` (60 min) window from the daily time and **waits** for the job to
finish (`anyServerRunningJobs`) — the day is marked done only once the resize actually applies,
so a backup running at `03:00` just delays the resize to the first idle moment in the window
rather than skipping the day.

## Status / TODO

Implemented: the substrate above, plus the per-axis **emission** of consumed metrics
(`service_*` raw + `dbu_*` DBU translation, srv_snd.go) and the **GUI graph** that reads
them — `ChartGroupedDBU` (grouped bars per axis: real conso → DBU, pivot max line, plan
line = the configurator ceiling the GUI reads but does not own).
Follow-ups: `SetPlan` wiring — the plan is a **client-set DBU size** (whole units); a
**+1/−1 DBU resizes `prov-db-*`** at the locked ratio (except admin-immutable resources)
and is **NOT** derived from them; future dedicated `Cloud18DatabaseCredits*` vars mirror
the app credit model, driven by `AddDBU`/`RemoveDBU`. Also `SetServerAgent` /
`SetAgentCapacity` from physical monitoring (#1778),
per-cluster/agent/minute **emission** (the data), then the burst/overcommit **policy**
and the heatmap.

**APU plan — now wired (Compute track, apps + proxies unified).** The two stateless
Compute workloads the configurator provisions — app deployments (`cluster.Apps`) and
proxies (`cluster.Proxies`, ProxySQL/HAProxy/MaxScale) — share ONE Compute/APU track,
keyed `AppKey{Cluster, App, Kind}` with `Kind` = `app` | `proxy`. `RefreshComputePlanAPU`
(per tick from `SetStatus`) projects each unit's configured resources (`prov-app-*` /
`prov-proxy-*`) into APU via the Compute profile (`ComputeUsedAPU`) and records the plan
(`SetAppPlan`); `AppPlanByCluster` / `AppPlanByClusterKind` aggregate it (`APUAggregate`,
3 axes, no io), mirroring `PlanByCluster` on the DBU track. The plan is deterministic
from config, so no sensor is needed. **Still open:** the APU **consumed** sensor (a
compute sensor reading the app/proxy container cgroups, like the DB sensor feeds
`IngestDBUMaxes`) — until it lands `AppConsumedByCluster` reads zero; and apps use
cluster-level app sizing (per-app `AppConfig` matching is a refinement).

Resize gates — **step 1 now wired** (2026-09): the memory grow branch of `ResizeDynamicResources`
calls `resourceManagerAllowsGrow` **before** the infra feasibility. A within-plan grow passes
untouched; a grow PAST the plan is gated on `CanGrowBeyondPlan` (the commercial overcommit budget)
AND, when the node's `AgentCapacity` is known, on the physical free pool (`UsableCeilingDBU(agent)`
must cover `ConsumedByAgent(agent) + (target − plan)`). A refusal logs and skips the live grow —
the client must raise the plan (the `IsNeedResourceCapUp` claim).

### Dynamic-resize trigger — the QPS-driven escalation (CPU → memory → IOPS)

`DriveDynamicResize` (per tick from `SetStatus`, gated by the existing
`prov-db-dynamic-resource` — no separate flag, the dynamic resize IS this trigger) grows the axis
the database is **actually binding on**, one +1 DBU step per scale-up window. The order is not
arbitrary and it is not per-axis-independent — the axes are coupled through the buffer pool, so
the driver uses **QPS as the objective** and lets the DB tell us which resource is short instead
of guessing:

1. **CPU first.** If any node's cores are pinned (`cpu` in `CanScaleConfigInPlan(true)`), nothing
   else moves throughput — grow CPU. A CPU-bound server will not answer a memory or IOPS grow with
   more QPS, so this must be checked before the throughput lever.
2. **Memory is the first throughput lever.** A bigger buffer pool cuts IO on **both** sides — read
   misses (`Innodb_buffer_pool_reads` → disk reads) *and* forced eviction / dirty-page flushes
   (`Innodb_buffer_pool_wait_free` → disk writes). So **IO saturation is usually relieved by
   memory, not IOPS.** When throughput-limited (mem or io due) and not CPU-bound, grow memory.
3. **IOPS is the last resort.** After a memory step, the next window compares cluster QPS
   (`currentClusterQPS`, the master's `Queries` delta) against the pre-grow value. If the step
   bought **> `dynamicResizeQPSMarginPct` (5 %)** more QPS, memory was the constraint → keep
   growing memory. If QPS **plateaued**, the working set now fits / the cache is no longer the
   bottleneck → the IO is *genuine* (not eviction) → escalate to a **+1 DBU IOPS** step. Buying
   IOPS before this point would only paper over an undersized cache.

So the loop is a hill-climb: **CPU (if pinned) → memory (while it pays off) → IOPS (once memory
stops paying off)**, with QPS measured over the scale-up window at each step (verify-after-action,
same cooldown as the resize convergence check). `growAxisInPlan` clamps every step to the per-node
plan ceiling (memory `GetDBContainerMemoryCapMB`, cpu `floor(plan)` cores, io `plan × 1000` iops),
records the axis + pre-grow QPS for the next window's plateau test, and applies live via the axis
setter. One step per scale-up window (cooldown), skipped during failover, hill-climb memory reset
when nothing is constrained.

**In-flight gate** (`isMemoryResizeInFlight`): a new memory resize is never stacked on one that has
not converged — both `ResizeDynamicResources` (memory) and `DriveDynamicResize` skip while the
runtime `INNODB_BUFFER_POOL_SIZE` is still off its target (async InnoDB resize) or a
`PendingCgroupShrink` is outstanding. This turns the cooldown from "wait 1 minute" into "wait until
the pool actually reached its new size", closing the grow-vs-async-resize / grow-vs-pending-shrink /
double-SET-GLOBAL races.

**Physical depth caveat (the follow-up that makes CPU/IOPS steps real).** Memory grow is
physically effective today (buffer-pool grow under the container ceiling). CPU and IOPS steps
currently only re-tune DB `SET GLOBAL` vars — `SetDBCores`/`SetDBDiskIOPS` do **not** yet resize
the container cpu/io cgroup (`pg_cpus` / io weight). So the escalation *decides and applies*
correctly, but a CPU or IOPS step will not add real capacity until the cgroup cpu/io resize is
built (extend `openSVCResize`/`PGUpdateInstanceV3` the same way it already does `pg_mem_limit`).
Until then, on a CPU-bound saturation the state fires and the config axis grows, but the container
stays pinned — the memory hill-climb is the provable half. Still open beyond that: dynamic
**shrink** (the deliberate reclaim, must clamp at the plan floor), cap-up (`IsNeedResourceCapUp`)
still a signal nothing consumes, the physical gate is instantaneous (no reserve / co-tenant
reclaim — Type-2 safety is a later step), and `overcommit_dbu` is not yet a tracked/emitted
ledger. The other resize entry
points remain `SetDB*` via the API, the CLI configurator, or a plan apply (`applyPlanSpec`). Wiring needs a **target-first** restructure
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

## The reservation contract: per-instance, classed, rolled up

repman matches a **technical reservation contract** to **real usage** against the
**hardware quota** — three distinct things. The RM owns only the third (capacity / room /
auto-placement); the **reservation lives with each service**, and the diff (reservation −
usage) is what repman watches.

**Unit is about *what* a service provisions, not *who* provisions it.** DBU = stateful /
storage (a MariaDB/MySQL node, *or* an app-deployed **pg**/**minio/S3**); APU = stateless
compute (a proxy, *or* a stateless app like phpMyAdmin/PHP). An app can be **either** — so
neither contract is "per-node × count"; both are **sums over per-instance reservations**
classified by unit.

**The tracking primitive is per service instance, attributed to placement — never a cluster
total.** A `# APU per cluster` can't be decomposed back to a node, so it can't drive
per-node usage-diff or a correct scale delta. Each instance is a record
`{ unit, reservation, real usage, node/agent placement, class }`, keyed `AppKey{Cluster,
Name, Kind}`, rolled up **per node** (room + auto-placement) and **per cluster** (the
contract view — `prov-service-plan-dbu`/`-apu` are reporting rollups, not the primitive).

**Class decides how a ±1 is affected:**
- **Controlled** (repman orchestrates + sizes): MariaDB/MySQL topology (DBU), proxies (APU)
  → `ChangePlanUnits(±1)` is **applied**, repman resizes/places (resource-follow).
- **Uncontrolled** (apps, client-created): repman does not define them but **tracks
  reservation + usage and scales the APU reservation with usage** — apps are the *primary*
  scale target (dev PHP ≈0 CPU → large idle gap; prod PHP → unbounded growth). Class changes
  *how* we act (only its reservation/limits, never its definition), not *whether* it scales.

**Borrow — overcommit is Plan + Borrow, tracked per service.** A service's guaranteed reservation
is its Plan (`PlanDBU`/`PlanAPU`). To burst beyond it, the service **borrows** DBU/APU from the
ResourceManager pool (the slack under-consumers leave on the agent). The borrow is tracked **per
service under its contract**, so the effective **cgroup cap = Plan + Borrow**, never just Plan.
Borrow is best-effort — it yields when a lender reclaims its reservation — and is the billable
overage (funded by `contract − real` on the lenders), always bounded by `Σ real ≤ agent ceiling`.
The RM is the lender and the ledger of who borrowed what.

**Implemented (this pass):** the per-instance reservation / usage / placement / class ledger
(`RefreshComputePlanAPU` → `SetAppPlan`/`SetAppAgent`/`AppKey.Kind`; `IngestAppConsumedAPU` →
`SetAppConsumed`); `prov-proxy-apu` (default 2) drives the per-proxy reservation (plan ledger,
`ChangePlanUnits(APU)`, resource-follow); apps sized **per-app** from their own `AppConfig`; the APU
**contract = the `AppPlanByCluster` rollup** (`plan_apu` emit + GWARN016 read it, not the now-vestigial
`prov-service-plan-apu`).

**Remaining:** (1) **Borrow tracking** — per-service `BorrowDBU`/`BorrowAPU` from the RM pool; cgroup
cap = Plan + Borrow. (2) Register app-provisioned **pg/minio** as **DBU** ledger entries into
`prov-service-plan-dbu`. (3) **Consumed source** — scrape OpenSVC `/metrics/pg` (`opensvc_pg_cgroup_*`:
`cpu_usage_usec` Δ→cores, `memory_current_bytes`, io) into `Ingest*`, retiring the custom sidecar
**(needs the om3 upgrade)**. (4) Real **CPU/IO cgroup resize** via the om3 `pg reset` / `pg_cpu_quota`
fix / `"default"` pg_* kw patch (today the CPU/IOPS steps only re-tune `SET GLOBAL`).

**Vocabulary:** units are **DBU**/**APU**; classes **controlled**/**uncontrolled**; never
"credits" (the app system's own accounting) or "tier".

## Laws

T7 (unified interface — capacity is orchestrator-agnostic), T16 (ratios/params in TOML
when made configurable), T18 (Graphite is the bounded history, no in-memory buffer),
T20 (one conversion source — reconcile `ComputeUsedDBUPerNode`), T6 (GUI for the editable
capacity). Commercial axis (refund %, overage, tiered price) stacks on top and never
gates the technical path.
