# Cloud18 Credit / Unit Model

**Status:** SPEC + rationale + implementation notes + remaining open decisions. The model is
**largely implemented** on branch `origin/marketplace-pricing` (caffeinated92 / Ahmad) — see
[§3.4](#34-implemented-on-the-marketplace-pricing-branch-ahmad). This doc keeps the original model
and rationale as the base, and now annotates the implementation direction from
`CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md` where the branch intentionally diverges, stages the
rollout, or defers a feature.

**Scope:** how Cloud18 service plans map to *units* (a.k.a. "credits"), the workload-profile
taxonomy, the ratio-lock rule, and the concrete implementation. Related code: `config/`
(`ServicePlan`, `AppUnit*`, `marketplace_ratio.go`, the `Cloud18Marketplace*` fields), `cluster/`
(`app_set.go` unit math, `cluster_marketplace_ratio.go`, `cluster.go`
`ComputeDatabaseUnits`/`ComputeApplicationUnits`, `cluster_set.go` plan apply), `configurator/`
(auto-tuning), and `server/api_global_settings.go` / `server/api_cluster.go` (pricing settings and
atomic DBU resize path).

---

## 1. The model in one line

Cloud18 sells **credits**. A credit is a fixed *locking ratio* of cpu / mem / disk
(/ iops). A partner sets **one €/credit/month price**; a workload **consumes an integer
number of credits** determined by whichever resource dimension binds. This is marketed as a
*predictable / fixed* cost model (partner-priced, not hyperscaler per-second metering).

> **Technical drives; marketing is directional only.** [signal18.io/cloud18](https://signal18.io/cloud18)
> and [www.cloud18.io/costs](https://www.cloud18.io/costs) are **marketing pages** — they signal
> *intent* to customers but are **not a spec**. The binding definitions come from the code, the
> configurator's behaviour, and real resource consumption. **Where the marketing copy and the
> technical model disagree, the technical model wins** — the copy gets updated, not the code.

## 2. Objective & unit taxonomy

**Objective.** One *unit* locks a fixed **hardware resource ratio** (cpu : mem : disk : iops).
Units are defined **per workload profile** so that a workload consumes — and pays for — only
the ratio it actually needs. A partner sets one €/unit price per type; the total is
`units_consumed × €/unit`. The reason for having several unit types is that different
workloads bind on different resources: a web server must not pay for database-grade
disk/IOPS, and object storage must not pay for database-grade cores/RAM.

**The Database ratio is locked for BOTH partner and user — neither may leave it.** A DB is
sized *only* in whole Database units, keeping `cpu : mem : disk : IOPS` in the fixed
proportion. Concretely:
- A **partner** cannot redefine or rescale the Database unit's ratio — their *only* lever is
  the **€/unit price**.
- A **user** cannot provision a database off-ratio — no cranking disk (or cores) without the
  matching RAM/IOPS. Scaling means adding whole Database units, not editing one dimension.

This is **enforced, not advisory**, for two reasons: (1) a database performs badly when the
ratio is broken (too little RAM or IOPS for the data), so the lock protects users from
misconfiguring themselves; (2) if the ratio floated, a "Database unit" would mean something
different on every instance and cross-partner price comparison — the whole point of the
marketplace — would collapse (a partner could look "cheap" just by shrinking the unit). Ratios
are **global and enforced**; only €/unit is per-partner. (Compute/Storage workloads may allow
looser sizing — the strict lock is specifically the **Database** unit.)

> **Implementation note — enforcement scope.** The implementation plan enforces this strict DB
> ratio specifically when `cloud18-marketplace-pricing-mode == global-unit-pricing`. Root cause:
> cross-partner comparability is only at risk once price is being derived from the locked ratio.
> Outside that mode, replication-manager still supports in-house / advanced custom sizing.

**Three unit types, by workload profile:**

| Unit | Profile / example workloads | cpu | mem | disk | iops | Basis |
|---|---|---|---|---|---|---|
| **Database** | MariaDB/MySQL — **strict, fully-coupled** ratio; a DB degrades if *any* dimension is under-provisioned, so IOPS is part of the lock | 1 | 4 GB | 40 GB | **1000** | code `ComputeDBUPerNode` (§3.4) — authoritative |
| **Compute** | proxy, web server, stateless apps — cpu/mem bound, **little disk**, no IOPS lock | 1 | 4 GB | 10 GB | — | code `AppUnit` (§3.1) — authoritative |
| **Storage** | object storage / S3-like — **disk-dominant**, low cpu/mem | low | low | high | low | *not yet in code — ratio TBD* |

**Rationale (Stephane):** the **database** ratio is strict because a DB needs cpu + mem +
disk + **IOPS in balance** to work well — hence IOPS is locked into that unit and *only* that
unit. **Compute** covers apps like proxies and web servers that are cpu/mem-hungry but need
little disk (and no IOPS guarantee). **Storage** covers workloads such as S3 that need
capacity but *less* memory and cores. The unit types exist precisely so each of these binds
on the right dimension.

> Marketing reference (INDICATIVE only, [www.cloud18.io/costs](https://www.cloud18.io/costs)):
> the site advertises Database ≈ *"1 Core – 4G Mem – 40G NVMe … HA 1–2 replicas"* and
> Container ≈ *"1 Core – 4G Mem – 10G NVMe … HA Actif Passif"*. Treat as a starting hint, **not
> the definition** — the authoritative ratios are the code constants (§3.4 / §3.1), chosen on
> technical grounds. Storage is "not on the site" simply because the technical ratio hasn't
> been decided yet.

**Per-unit partner pricing (marketing figures, indicative, €/month):** Signal18‑Bso ~15 ·
Signal18‑RapidSpace ~10 · Sagacita‑Rdem ~29 · Signal18‑Ovh & Sagacita‑Ovh ~36. Real prices are
per-partner runtime settings (`cloud18-marketplace-dbu-price`, `-app-unit-price`), not these.

### 2.1 Plans vs units — two roles, and the GOAL of per-unit pricing

A "plan" plays **two separate roles** — keep them distinct:

1. **Deployment shortcut.** The `xN.size` preset (a *sizing* cores/mem/disk/iops + a *topology*
   node count) that provisions a whole cluster in one action. Historical, convenient, and it
   **stays regardless of how pricing works**.
2. **Pricing via a CSV list.** The `csv-service-plan` mode: the partner maintains the registry
   sheet with an explicit price per plan (database + proxy). This **enforces exact prices** —
   the partner dictates the number — but requires the partner to **curate the whole CSV**.

> **⭐ GOAL — relax the partner's need to maintain a CSV price list.** In `global-unit-pricing`
> mode the partner sets just a few numbers — €/Database-unit, €/Application-unit — and every
> price is **derived** (`UnitUsage × €/unit`, §4). No per-plan sheet to curate; add a new size
> and it is priced automatically. That convenience is the whole point of the unit model.

**So CSV-plan pricing and per-unit pricing are two coexisting *pricing modes*, not
replacements** (`Cloud18MarketplacePricingMode`, §3.4):

| Mode | Price source | Partner effort | When |
|---|---|---|---|
| `csv-service-plan` | absolute € per plan, from the sheet | **maintains the CSV** | wants to **enforce exact** prices |
| `global-unit-pricing` | derived `UnitUsage × €/unit` | sets a few €/unit values | wants **no list to maintain** (the goal) |

The **deployment shortcut** (role 1) works under *either* mode: a plan still gives one-click
sizing; only *where its price comes from* changes.

**Why keep the CSV plan at all once per-unit exists?** Two enforcement jobs per-unit can't do:
1. **Enforce promotions** — the plan's `PromotionPct` (registry `promo` column) applies a
   discount the derived price wouldn't.
2. **Lock an exact price on the database + proxy part** — when a partner wants to *dictate* that
   number rather than let `UnitUsage × €/unit` compute it.

So the CSV plan survives as the **enforcement / override** lane for promos and the db+proxy
price, while per-unit removes the day-to-day list upkeep for everything else. *Open detail:*
whether the CSV fully replaces per-unit for a cluster (a hard mode switch — Ahmad's current
model) or only **overrides those specific parts** (promo, db+proxy) on top of a per-unit base.

> **Implementation note — current mode behavior.** The implementation plan uses a **hard price
> source switch** for now: `csv-service-plan` keeps bundled plan pricing/promo, while
> `global-unit-pricing` keeps plans only as sizing shortcuts and derives price from units.
> Root cause: avoid mixed price authorities while unit accounting, DBU plan mapping, and App Unit
> accounting stabilize. Promotion / discount handling in unit pricing is explicitly deferred and
> will be a later pricing-layer feature, not part of raw unit accounting.

## 3. What already exists in code

### 3.1 The App credit == `AppUnit` (already implemented)

The published **Container Application credit** (`1c / 4GB / 10GB`) is *exactly* the existing
App Unit:

```
config/config.go:1246-1248
  AppUnitCpuCores = 1     // 1 core per App Unit
  AppUnitMemMB    = 4096  // 4 GB
  AppUnitDiskGB   = 10    // 10 GB
config/config.go:1240-1244
  AppSizingModeUnit   = "unit"   // credit-based sizing
  AppSizingModeManual = "manual" // direct resource editing
```

The credit-count formula is `deriveUnitFromStoredResources` (`cluster/app_set.go:151`):

```
units = max( ceil(cores  / AppUnitCpuCores),
             ceil(memMB  / AppUnitMemMB),
             ceil(diskGB / AppUnitDiskGB) )   // each term floored at 1
```

This *is* the "locking ratio → integer credits, binding dimension wins" rule. It is
currently used only for **apps**, not for the database plan.

### 3.2 The Database plan (`ServicePlan`) — raw resources + absolute € (no credits yet)

`ServicePlan` (`config/config.go:1263`) carries the raw sizing and **absolute** monthly
costs, sourced from the `prov-service-plan-registry` Google Sheet CSV →
`<WorkingDir>/serviceplan.json` (read by `GetServicePlans`, `cluster/cluster_get.go:1409`):

```
DbCores, DbMemory (MB), DbDataSize (GB), DbIops, PrxCores, PrxDataSize
InfraCost, LicenceCost, DbaCost, SysCost (float €), Devise (currency)
```

`SetServicePlanInfos` (`cluster/cluster_set.go:1389`) copies plan → cluster config
(`SetCloud18MonthlyInfraCost` etc.). There is **no credit field today** — the plan is a
fixed bundle priced with absolute euros, which is *not* how the website describes it
(per-credit). See [§7](#7-known-inconsistencies--broken-definitions).

### 3.3 The configurator already scales the DB from its resources (what "increasing a unit" does)

`cluster/configurator` derives the **entire** MariaDB/MySQL engine tuning from the provisioned
`ProvCores / ProvMem / ProvDisk / ProvIops` — not from hand-written my.cnf:

| Derived setting | Formula (configurator_get.go) | Scales with |
|---|---|---|
| InnoDB buffer pool | `usable_RAM × innodb_share%` (`GetConfigInnoDBBPSize`) | **mem** |
| InnoDB redo/log file | `buffer_pool / 2`, clamped 1–20 GB (`GetConfigInnoDBLogFileSize`) | mem |
| BP instances | `buffer_pool / 8GB + 1` (`GetConfigInnoDBBPInstances`) | mem |
| Write IO threads | `iops × iops_latency` (`GetConfigInnoDBWriteIoThreads`) | **iops** |
| Read IO threads | `= ProvCores` (`GetConfigInnoDBReadIoThreads`) | **cores** |
| max_connections, dirty-page pct, … | derived from the above | mem/cores |

So **increasing a Database unit re-tunes the whole engine proportionally**: more RAM → bigger
buffer pool + more BP instances + bigger redo log; more IOPS → more write IO threads; more
cores → more read IO threads. This is the mechanism that makes "size a DB in whole units"
actually work.

It is **also why the ratio must be locked (§2).** The auto-tuner *assumes* cpu/mem/disk/IOPS
are balanced. Feed it off-ratio input — e.g. lapasse's `2c / 16GB / 50GB / 300iops` (huge RAM
relative to its IOPS and cores) — and it emits an **unbalanced** config: a large buffer pool
starved of write-IO threads (300 iops → ~1 thread) and read-IO threads (2 cores), i.e. a DB
that under-performs under load. Locking the Database ratio protects the configurator's
core assumption; a user sizing off-ratio would silently get a badly-tuned engine.

### 3.4 Implemented on the `marketplace-pricing` branch (Ahmad)

Branch `origin/marketplace-pricing` (caffeinated92) implements the unit model. Key pieces:

**Pricing mode switch** (`config/config.go`):
```
ConstMarketplacePricingModeCsvServicePlan    = "csv-service-plan"     // legacy absolute-€ per plan
ConstMarketplacePricingModeGlobalUnitPricing = "global-unit-pricing"  // the unit model
Cloud18MarketplacePricingMode string `scope:"server" ...`             // per-instance selector
```
**Plans remain shortcuts in both modes, but unit mode rebuilds DB shape from DBU.** The current
implementation direction keeps `prov-service-plan` as a deployment shortcut in both modes (§2.1),
but in `global-unit-pricing` the Database shape is rebuilt from a **plan suffix → per-node DBU**
mapping instead of using the raw CSV DB resource columns. Root cause: the legacy CSV DB resource
rows are often off-ratio for the strict Database Unit, so using them directly would reintroduce
non-comparable Database shapes into unit pricing.

**Database Units** (`cluster/cluster.go` + `cluster_marketplace_units_test.go`):
```
ComputeDBUPerNode()   = max( cores/1, mem/4096, disk/40, iops/1000 )   // ratio 1c/4GB/40GB/1000iops
ComputeDatabaseUnits() = ComputeDBUPerNode() × (number of DB servers)
```
Test `TestComputeDBUPerNode_MaxOfFourRatios` pins each dimension (cores=4→4, mem=16384→4,
disk=160→4, iops=4000→4). **IOPS is counted** — this is the fix for §7.2. It is the
`DatabaseUnit` ratio from §2, exactly.

**Application Units** (== our "Compute" unit) (`cluster/cluster.go`):
```
// historical branch baseline
ComputeApplicationUnits() = Cloud18ApplicationCreditsUsed + Σ(live proxy) prov-proxy-cores
```
i.e. reserved app credits plus each *running* proxy's cores.

> **Implementation note — Application Unit model superseded.** The implementation plan treats the
> formula above as the historical branch baseline, not the final model. The target model is:
> **resource-derived App Units per app/OpenSVC node × app/OpenSVC node count**, plus the same App
> Unit ratio applied to live proxies. Root cause: app credits are not a stable technical accounting
> signal for flex/failover/multi-node app deployments, while resource-per-node accounting is.

**Persisted for the BO** — `DatabaseUnits` / `ApplicationUnits` (float, `json:"..."`) are
written on cluster save so the BO can price = `units × €/unit`.

> **Implementation note — export boundary.** `clusterstate.json` is the clean technical persistence
> path for unit totals. If the BO/export pipeline only scans another artifact (for example TOML or
> a repo-driven extraction path), implementation may temporarily mirror/export the computed values
> through that path too. Root cause: BO integration boundaries, not a change in the unit model.

**Global per-partner pricing settings** (all `scope:"server"`, `server/api_global_settings.go`
+ `MarketplaceSettings.jsx`): `cloud18-marketplace-dbu-price` (€/Database unit),
`cloud18-marketplace-app-unit-price` (€/Application unit), `cloud18-application-credits(-price)`,
plus marketplace-level infra/SLA/cert/currency metadata (moved up from per-plan to per-partner).

**Future work note:** the **Storage** unit, promotion / discount handling in
`global-unit-pricing`, and the App HA structural pricing layer for failover vs flex are all
intentionally deferred out of this implementation phase; see [§9](#9-future-goals-roadmap) and
`CLOUD18_APP_HA_DISCOUNT_PLAN.md`.

**DB ratio-lock enforcement is now part of the implementation direction.** The plan scopes strict
DB enforcement to `global-unit-pricing` and uses an **atomic Database Unit resize action** rather
than four independent `prov-db-*` writes. Root cause: the ratio is only commercially binding in
unit pricing, and per-field writes would otherwise create transient off-ratio states mid-update.

## 4. `UnitUsage` — implemented vs open

The unit breakdown is implemented per **cluster** (not per plan) as `DatabaseUnits` and
`ApplicationUnits` (§3.4). Conceptually it is one helper over a per-unit ratio:

```go
// the shape the branch converges to (Database + Application live; Storage TBD)
type UnitDef struct{ CpuCores, MemMB, DiskGB, Iops int } // a 0 dimension is NOT locked
DatabaseUnit    = UnitDef{1, 4096, 40, 1000}  // strict — all four locked (impl: ComputeDBUPerNode)
ApplicationUnit = UnitDef{1, 4096, 10, 0}     // "Compute": cpu/mem, low disk, no IOPS == AppUnit
StorageUnit     = UnitDef{/* disk-dominant, low cpu/mem */} // NOT YET IMPLEMENTED — ratio TBD
```

Naming: our taxonomy's **Compute** unit is called **Application Units** in code, and it adds
*live proxy cores* on top of reserved app credits (§3.4). IOPS is locked only for the Database
unit — matching §2.

**Current branch behavior (implementation choices — NOT settled policy in the original model):**
- **Total = per-node × node count** — `ComputeDatabaseUnits` multiplies `ComputeDBUPerNode` by
  the DB-server count. The original model treated this as provisional because the published
  Database credit bundled HA language, but the implementation plan locks this as the current
  technical counting rule for `global-unit-pricing`.
- **IOPS is in the Database unit** — §7.2 fixed (a correct fix, not a contested call).
- **Plans remain shortcuts** in `global-unit-pricing`, but DB sizing is rebuilt from the appendix
  DBU mapping rather than from raw CSV DB resource columns.
- **Application Unit target model** is now resource-per-node × app/OpenSVC node count; the older
  app-credit formula should be treated as historical branch context.

**Open in the original model / expectation:**
1. **HA vs node count (§7.3)** — *the* main original open question. The published Database credit
   bundles "HA 1–2 replicas" *inside* one credit, but `ComputeDatabaseUnits` multiplies by every
   node. Pick one: replica nodes are **free** (bundled), or **each node counts**.
2. **Storage unit ratio** — disk-dominant, low cpu/mem (e.g. `{0, 1024, 100, 0}` → ~one unit
   per 100 GB) so bulk NVMe/backup bills as storage, not DB. Not implemented yet.
3. **Ratio-lock *enforcement*** — §2 is stated policy; the branch prices units but does not yet
   *forbid* a user from provisioning a DB off-ratio. Enforce, or advisory-only?

> **Implementation note — current phase choices.** For the current implementation phase,
> `global-unit-pricing` uses **each DB node counts** as the technical DBU rule, scopes strict DB
> ratio-lock enforcement to unit pricing, and defers Storage, promotion/discount in unit pricing,
> and App HA structural pricing to future work. Root cause: lock down one clean technical unit
> model first, then add commercial overlays later.

**Implementation note — current backup handling:** when backup is enabled, current branch behavior
adds a flat **+1 Database Unit** to `ComputeDatabaseUnits`. Root cause: storage pricing is still
deferred, so backup is temporarily folded into DBU instead of being modeled as its own storage
priced line. Retained snapshots are not counted individually. This is an implementation staging
choice, not the final Storage-unit design.

### 4.1 App lifecycle — running vs stopped changes what it consumes

Application (Compute) workloads come in two kinds, and consumption follows their **state**:

- **Always-on** — persistent services (a proxy, a web server) that run continuously and consume
  their full **Application/Compute units** (cpu/mem) plus their storage.
- **Stoppable** — apps that can be turned off when idle. A **stopped** app should consume
  **only storage** (its disk footprint → **Storage units**), **not** compute — *provided the
  platform auto-downsizes its cgroup* (cpu/mem → ~0) on stop. Without auto-downsize it would keep
  holding reserved compute while idle and be billed for nothing.

The branch already encodes the running-only half for proxies: `ComputeApplicationUnits` counts
**live** proxies only (a down proxy adds no cores). Generalising this to apps needs the **live
cgroup auto-downsize** capability (§9.1): on stop, bind the cgroup down so Compute units drop to
zero and only the storage footprint remains billable. This is a concrete driver for the
**Storage unit** (§4.2) — idle/stopped app data is priced as storage, not compute — and it feeds
the delta reconciliation (§9.2): a stopped app naturally falls *below* its plan baseline → refund.

> **Implementation note — App HA structural pricing is separate.** The future commercial difference between
> **failover** apps (active-standby, shared storage) and **flex** apps (active-active, non-shared
> storage) is intentionally deferred to `CLOUD18_APP_HA_DISCOUNT_PLAN.md`. Root cause: it changes
> billed app price, not the technical `ApplicationUnits` count.

## 5. Pricing: today vs the credit model

| | Today (code) | Website / target |
|---|---|---|
| Unit of price | absolute €/plan (`InfraCost`+`LicenceCost`+`DbaCost`+`SysCost`) | €/credit/month, partner-set |
| Where set | registry sheet columns per plan | one number per partner (Bso €15 … Ovh €36) |
| Total | fixed per plan | `credits_consumed × €/credit` |
| Billing | static | metered on consumption ("if we allow it") |

**Phase 2** (not this doc): partner sets €/credit; total = `UnitUsage × €/credit`; optional
metered consumption.

> **Implementation note — current price-source discipline.** The implementation plan treats
> `global-unit-pricing` as a hard technical price-source switch: unit accounting first, later
> commercial overlays second. Promotions/discounts in unit pricing are deferred so the branch does
> not have two competing price authorities while the unit model stabilizes.

## 6. Appendix — units per current plan (computed)

Database units use `{1c,4GB,40GB,1000iops}`; Compute units use `{1c,4GB,10GB}` (no IOPS lock).
Per **one** DB node. "bind" = dimension that determines the count. The Database/Compute split
below is the same plan scored against two different unit ratios — the divergence at `.large`
and `.huge` is the whole point of having distinct unit types.

| plan | Database units | Compute units | binding dim |
|---|---|---|---|
| x*.tiny | 1 | 1 | cpu,mem,disk,iops |
| x*.small | 4 | 4 | mem |
| x*.small.compute | 4 | 4 | cpu,mem |
| x*.small.perf | 8 | 8 | mem |
| x*.middle | 8 | 8 | mem |
| x*.middle.compute | 8 | 8 | cpu,mem |
| x*.middle.perf | 16 | 16 | mem |
| x*.large | **16** | **26** | mem (DB) / disk (App) |
| x*.large.compute | **16** | **26** | mem (DB) / disk (App) |
| x*.large.perf | 32 | 32 | mem |
| x*.huge | **50** | **52** | iops (DB) / disk (App) |
| x*.huge.compute | **50** | **52** | iops (DB) / disk (App) |
| x*.huge.perf | 64 | 64 | mem |

(The `x1/x2/x3` prefix — the node count — does not change the *per-node* credit figure;
only §7.3 decides whether it multiplies the total.)

## 7. Known inconsistencies / "broken definitions"

These are the things to resolve — the model as published + implemented does not fully cohere.

1. **Two disk ratios — *by design*, but the code has only one.** DB unit = 40 GB, Compute
   unit = 10 GB: intentional, because a DB needs disk in its strict ratio while a proxy/web
   server does not (§2). The *bug* is that the code has a single `AppUnit` (10 GB) and no
   `DatabaseUnit` — so scoring a DB plan with today's helper over-counts disk 4×. `.large`
   plans already diverge (16 Database vs 26 Compute units). Fix = two ratios, one helper
   (§4), never conflate them.

2. **IOPS missing from the unit computation is an ERROR — must fix.** A DB needs guaranteed
   IOPS to perform, so IOPS belongs in the Database unit's locked ratio (1000/unit), and the
   published Database credit already states it. But `AppUnit`/`deriveUnitFromStoredResources`
   has **no IOPS term at all** — the code simply drops the dimension. This is a defect, not a
   design choice: DB sizing is scored wrong, most visibly on `x*.huge` where IOPS is the
   binding dimension (50000/1000 = **50 units**) yet the legacy code counts 0 for it.
   **FIXED on `marketplace-pricing`:** `ComputeDBUPerNode` counts IOPS. (The legacy
   `AppUnit`/`deriveUnitFromStoredResources` still has no IOPS term — correct there, since
   Application units don't lock IOPS.)

3. **Do HA replica nodes count, or are they "free"? — original model question kept.**
   `ComputeDatabaseUnits` multiplies `ComputeDBUPerNode` by **every** DB server, so a 3-node HA
   cluster bills 3× the per-node units technically. The marketing copy says "HA 1–2 replicas" is
   bundled in one credit — but that is a *packaging claim*, not a spec, and technically each
   replica is a real node consuming real cores/mem/disk. So the technically-honest default is that
   **each node counts** (`× node_count`); "bundling HA" would be a deliberate commercial giveaway.
   Decide on technical + business grounds — and if replicas are billed, **update the marketing
   copy to match**, not the code. §4 original open decision 1.

   **Implementation note:** the current implementation phase for `global-unit-pricing` uses **each
   node counts** as the technical DBU rule. Any future HA concession should be a pricing-layer
   commercial adjustment, not a change to raw DBU counting.

4. **`.compute` cores are "free" under the credit model but not under absolute €.** When mem
   binds, extra cores don't raise the credit count (`x2.small` and `x2.small.compute` are
   both 4 DB credits) — yet the registry charges more (`infracost` 50 vs 60). Credit pricing
   and absolute pricing disagree about what a core costs.

5. **Storage unit is undefined.** The code has only Database + Application units; "storage" is
   a technical decision not yet made (ratio TBD), so bulk-disk/backup pricing has no home yet.

6. **Two pricing sources — coexisting *by design*.** `csv-service-plan` = enforced exact prices
   from the sheet (partner maintains it), kept specifically to **enforce promos and lock the
   db+proxy price** (§2.1); `global-unit-pricing` = derived `UnitUsage × €/unit`, no list to
   maintain (the goal). Not a bug, not a reason to retire the CSV. **Implementation note:** the
   current implementation plan uses a **hard mode switch** for now, and explicitly defers
   promotions/discounts in `global-unit-pricing`. Root cause: keep price authority unambiguous
   while the unit model stabilizes.

7. **Silent price failures (already documented in the plan-apply path).**
   - Count-gate mismatch → `SetServicePlan` returns *"Plan not possible for that cluster"*
     and skips the price, while the API still returns "Successfully reloaded"
     (`cluster_set.go:1552`, `server/api_cluster.go` reload handlers — instrumented on
     develop).
   - Zero/default cost is *deleted* on save (`cluster/cluster.go:1809-1811`), so a plan that
     matches no registry row publishes a blank price rather than an error.

## 8. Status & next steps

**Done / implementation-plan-locked on `marketplace-pricing` (Ahmad):** pricing-mode switch
(`csv-service-plan` vs `global-unit-pricing`); `ComputeDBUPerNode` /
`ComputeDatabaseUnits` (Database units, IOPS included, each DB node counts); per-partner €/unit +
marketplace metadata settings; plans kept as shortcuts but with unit-mode DB shape rebuilt from
appendix DBU mapping; DB ratio-lock enforcement scoped to `global-unit-pricing`; atomic DBU resize
path as the implementation choice; Application Unit target model locked to resource-per-node ×
app/OpenSVC node count; units persisted/exported for BO consumption; tests + Marketplace GUI.

**Remaining in the current implementation phase:**
1. **Retire per-plan absolute €** once unit pricing is default (§7.6), or keep
   `csv-service-plan` as a legacy-only mode.
2. Legacy silent-price failures (§7.7) are moot in unit mode but live while `csv-service-plan`
   remains.
3. Any BO/export/catalog integration path that cannot yet consume the clean unit outputs directly
   may still need explicit export wiring. Root cause: integration boundaries, not the unit model
   itself.

## 9. Future goals (roadmap)

These build on the unit model above; not scoped yet, captured so the design points that way.

0. **Deferred marketplace pricing work from this phase.** These are acknowledged parts of the
   model, but intentionally not implemented in the current phase:
   - **Storage unit** — define a storage ratio and price bulk-disk/backup as storage instead of
     temporarily folding some cases into Database Units.
   - **Promotion / discount in `global-unit-pricing`** — keep technical unit accounting hard first,
     then add commercial overlays later.
   - **App HA structural pricing (failover vs flex)** — tracked separately in
     `CLOUD18_APP_HA_DISCOUNT_PLAN.md`; this changes billed app price, not technical
     `ApplicationUnits`.

1. **Live OpenSVC cgroup-binding integration.** Bind unit accounting to OpenSVC's live cgroup
   resource control API (the "linux kernel virtualization using CGROUP" the infra already uses),
   so a cluster's **actual** cpu/mem/disk/iops is *measured and enforced at the kernel*, not
   merely inferred from `ProvCores/ProvMem/...`. This turns `UnitUsage` from a static
   provisioning figure into a **live consumption signal**, and makes the ratio-lock (§2)
   enforceable in the kernel rather than by policy alone. The configurator's derived sizing
   (§3.3) becomes the cgroup binding target.

   **Feasibility — the online-resize primitives already exist today.** Dynamic resize of the
   **InnoDB buffer pool**, **CPU cores** (thread pool) and **IOPS** (io capacity / io threads)
   is all achievable *now* via **dynamic config**, applied at runtime with no restart
   (`SET GLOBAL innodb_buffer_pool_size`, `thread_pool_size`, `innodb_io_capacity` /
   io-threads) — and the configurator (§3.3) already computes these values from resources. So
   binding a live cgroup up or down and having the engine track it **without downtime** is
   within reach. This is what turns §9.1 (auto-downsize) and §9.2 (delta reconciliation) from
   aspirational into buildable: resize the cluster to match real usage, live.

2. **Fixed-plan baseline + delta reconciliation (refund / overage).** Treat a **fixed plan as
   the base contract** — the committed baseline the customer pays for up front (predictable).
   Then, using the live measurement from #1, compute the **delta = real consumption − plan
   baseline** and settle it under a **commercial contract**:
   - consumed **below** the plan → **credit refund** (return the unused units);
   - consumed **above** the plan → bill the **delta** (metered overage).

   This gives the best of both: a predictable committed price (the plan / CSV enforcement of
   §2.1) *and* fair pay-for-what-you-use adjustment (the delta), with the commercial contract
   defining the refund/overage terms per partner–customer. The fixed plan stays the anchor; the
   delta is the only metered part.

   **The measurement primitive exists; reporting it to BO is a task for the workload plugin
   (not yet done).** repman already *measures* per-server **CPU** (`WorkLoad.CpuThreadPool` /
   `CpuUserStats`) and **memory** — **average and peak** (`GetClusterMaxCpuUsage` etc. in
   `cluster_get.go`; the `WorkloadStateMachine` + `WorkloadLogrus` capture spikes) — and can emit
   to Graphite/carbon (`graphite-metrics`, `SendGraphiteMetrics()`). What is **not yet wired** is
   delivering that avg/peak consumption to the **BO** for delta reconciliation — that is a
   **task for the workload plugin** (the log-plugin system), the right home for turning measured
   workload into a BO-side consumption signal. So the raw signal exists; only its BO reporting
   remains to be built. §9.1's cgroup binding then adds *enforcement* on top of this
   *measurement*.

3. **Autoscaling control loop — how the cgroup change actually happens.** The live resize is not
   free-form; it is a bounded, unit-granular loop that repman drives:
   - **Unit-granular steps.** Change is applied **+1 unit / −1 unit** at a time — never arbitrary
     fractions — so the ratio-lock (§2) stays intact at every step.
   - **Backed by a repman-maintained free pool.** repman keeps a **free pool** of unallocated
     host capacity (cores/mem/disk/iops), sourced from the per-agent load it already tracks
     (e.g. `agents.json`). A `+1` **draws from** the pool; a `−1` **returns to** it; a `+1` only
     fires if the pool can satisfy it. The pool stays under repman's maintenance, not the
     workload's.
   - **Frequent free-pool recomputation.** The pool must be recomputed **often** so decisions use
     fresh capacity — a stale free-pool figure would over- or under-commit the host.
   - **Capped to a min/max % delta on the plan.** Auto-resize may drift only within a **min/max
     percentage band** around the plan baseline (§9.2): a cluster flexes with load but never runs
     away from its committed contract. Outside the band → no automatic change (needs a
     plan/contract change). This is the guardrail that keeps the delta (§9.2) predictable.

## 10. Long-horizon goal (roadmap): live partner-to-partner migration

The furthest-out vision. When a cluster's **average consumption** (§9.2) outgrows its current
partner — or a different partner is a better fit (capacity, cost, geo) — repman orchestrates a
**live partner-to-partner migration** of the whole cluster **and its apps**, with no downtime,
cut over by DNS. This composes almost entirely of building blocks that already exist.

The flow:

1. **Trigger — consumption-driven.** Sustained average consumption (or a capacity/cost/geo
   signal) indicates the cluster should move partners.
2. **Capacity discovery across peers.** Every peer is queried against its **free credit pool**
   (the §9.3 free pool, surfaced at marketplace level): who has room to host this cluster?
3. **Candidate list.** Build the list of partners **available to hold** the cluster — enough free
   pool, plus policy/eligibility.
4. **Migration request.** Because backups already live on **remote storage (S3 / SFTP)**, a
   migration request is sent to the **user** and the **target partner** for consent.
5. **Live build on the new partner.** On the chosen partner's repman:
   - **provision** a fresh cluster,
   - **restore** the remote backup (S3 / SFTP),
   - **replay the binlog on the fly** so the new cluster catches up and **both clusters live
     together** — cross-partner replication keeping them in sync.
6. **Cutover on "go".** When the go is fired, the cluster **and its apps** are moved by a **DNS
   change** (`Cloud18DatabaseReadWriteSrvRecord` / `...ReadSrvRecord` / `...ReadWriteSplitSrvRecord`
   + the app records) — traffic swings to the new partner and the old cluster is retired.

**Building blocks that already exist** (so §10 is orchestration, not greenfield): remote backups
(`backup-restic`, S3/SFTP), provision + restore, binlog capture/replay across instances, the DNS
SRV records above, and the peer catalogue + free-pool (§9.3). §10 wires them into one
orchestrated, consumption-driven migration.

## 11. CRM-side settlement implementation

CRM-side settlement now implements the explicit billing-lifecycle-event model described in
`docs/CLOUD18_BACKEND_CRM_SETTLEMENT_PLAN.md` (backend resolves and sends pricing snapshots; CRM
owns EUR settlement, billing cycles, and ledger history). The fixed-base / refundable-app-delta
distinction this document describes operationally is what
`cluster_billing_state.current_base_amount` (the fixed, non-refundable base cluster baseline) and
`cluster_app_billing_items` (the refundable/creditable app delta) encode on the CRM side.
