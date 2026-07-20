# Cloud18 Credit / Unit Model

**Status:** SPEC + rationale + open decisions. The model is **largely implemented** on branch
`origin/marketplace-pricing` (caffeinated92 / Ahmad) — see [§3.4](#34-implemented-on-the-marketplace-pricing-branch-ahmad).
This doc explains *why* the model is shaped this way, records what the branch already decided,
and narrows the remaining open questions ([§4](#4-unitusage--implemented-vs-open),
[§7](#7-known-inconsistencies--broken-definitions)).

**Scope:** how Cloud18 service plans map to *units* (a.k.a. "credits"), the workload-profile
taxonomy, the ratio-lock rule, and the concrete implementation. Related code: `config/`
(`ServicePlan`, `AppUnit*`, the `Cloud18Marketplace*` fields), `cluster/` (`app_set.go` unit
math, `cluster.go` `ComputeDatabaseUnits`/`ComputeApplicationUnits`, `cluster_set.go` plan
apply), `configurator/` (auto-tuning), and `server/api_global_settings.go` (pricing settings).

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

### 2.1 Plans vs units — why both exist (units are the primitive, plans are shortcuts)

The named `ServicePlan`s (`x2.middle`, `x3.small`, …) **predate** the unit model. Historically
`replication-manager` needed a **quick way to deploy a full database cluster**, so a plan is a
**shortcut / preset**: a named bundle of a *sizing* (cores/mem/disk/iops) + a *topology* (the
`xN` node count) that provisions a whole cluster in one action. It is convenience, not a
separate pricing axis.

Units are the **primitive**; a plan is **sugar on top** — a pre-named point in the unit space:

```
plan        = preset(sizing, node_count)
UnitUsage   = unitsFor(sizing, DatabaseUnit) [ + Compute/Storage ]   // §4
price(plan) = UnitUsage × (partner €/unit)                           // DERIVED, not hand-entered
```

**Consequence:** plans do not compete with units — every plan simply *resolves to* a unit
count. This is the intended fix for §7.6: a plan's monthly cost should be **derived** from its
unit usage × the partner's €/unit, instead of being maintained as four independent absolute-€
columns in the registry sheet. New workloads that don't fit a preset are still expressible
directly in units; the plans just remain the fast path for "give me a standard cluster now."

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
Commit *"disable cluster plans in unit pricing"* turns the CSV service-plans off in unit mode
— i.e. §2.1 made real: in unit mode, plans (shortcuts) step aside and units drive pricing.

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
ComputeApplicationUnits() = Cloud18ApplicationCreditsUsed + Σ(live proxy) prov-proxy-cores
```
i.e. reserved app credits plus each *running* proxy's cores.

**Persisted for the BO** — `DatabaseUnits` / `ApplicationUnits` (float, `json:"..."`) are
written on cluster save so the BO can price = `units × €/unit`.

**Global per-partner pricing settings** (all `scope:"server"`, `server/api_global_settings.go`
+ `MarketplaceSettings.jsx`): `cloud18-marketplace-dbu-price` (€/Database unit),
`cloud18-marketplace-app-unit-price` (€/Application unit), `cloud18-application-credits(-price)`,
plus marketplace-level infra/SLA/cert/currency metadata (moved up from per-plan to per-partner).

**Not yet on the branch:** the **Storage** unit (Database + Application only), and enforcement
of the ratio lock on *user* DB sizing (§2 is stated policy; the branch prices units but does
not yet forbid off-ratio provisioning).

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

**Current branch behavior (implementation choices — NOT settled policy; Stephane signs off):**
- **Total = per-node × node count** — `ComputeDatabaseUnits` multiplies `ComputeDBUPerNode` by
  the DB-server count. This is *provisional* and is exactly what open decision 1 below may
  change.
- **IOPS is in the Database unit** — §7.2 fixed (a correct fix, not a contested call).
- **Plans yield to units** in `global-unit-pricing` mode — §7.6 direction.

**Open — Stephane decides (architecture/pricing policy, not the implementer):**
1. **HA vs node count (§7.3)** — *the* main open question. The published Database credit
   bundles "HA 1–2 replicas" *inside* one credit, but `ComputeDatabaseUnits` multiplies by
   every node. Pick one: replica nodes are **free** (bundled), or **each node counts**.
2. **Storage unit ratio** — disk-dominant, low cpu/mem (e.g. `{0, 1024, 100, 0}` → ~one unit
   per 100 GB) so bulk NVMe/backup bills as storage, not DB. Not implemented yet.
3. **Ratio-lock *enforcement*** — §2 is stated policy; the branch prices units but does not yet
   *forbid* a user from provisioning a DB off-ratio. Enforce, or advisory-only?

**Ruling in progress (Stephane, 2026-07-20): a backup counts as 1 Database unit.** A backup is a
full DB-sized copy, so instead of pricing its storage separately it adds a flat **+1 Database
unit** to the cluster's `ComputeDatabaseUnits`. Not on the branch yet — debrief Ahmad to add it.
Two points to pin:
- **Per-backup or flat?** +1 for *each* backup line/destination, or +1 if any backup is enabled?
  (And do the 8 retained snapshots count, or just the live backup?)
- **Reconcile with the published credit**, which already bundles *"1 backup / 8 snapshots"* per
  Database credit — so either the first backup is "included" (extra ones count) or every backup
  counts and the published text is updated.

## 5. Pricing: today vs the credit model

| | Today (code) | Website / target |
|---|---|---|
| Unit of price | absolute €/plan (`InfraCost`+`LicenceCost`+`DbaCost`+`SysCost`) | €/credit/month, partner-set |
| Where set | registry sheet columns per plan | one number per partner (Bso €15 … Ovh €36) |
| Total | fixed per plan | `credits_consumed × €/credit` |
| Billing | static | metered on consumption ("if we allow it") |

**Phase 2** (not this doc): partner sets €/credit; total = `UnitUsage × €/credit`; optional
metered consumption.

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

3. **Do HA replica nodes count, or are they "free"? — decide on resource reality, not the copy.**
   `ComputeDatabaseUnits` multiplies `ComputeDBUPerNode` by **every** DB server, so a 3-node HA
   cluster bills 3× the per-node units. The marketing copy says "HA 1–2 replicas" is bundled in
   one credit — but that is a *packaging claim*, not a spec, and technically each replica is a
   real node consuming real cores/mem/disk. So the technically-honest default is that **each
   node counts** (`× node_count`); "bundling HA" would be a deliberate commercial giveaway.
   Decide on technical + business grounds — and if replicas are billed, **update the marketing
   copy to match**, not the code. §4 open decision 1. (Legacy `SetServicePlan`,
   `cluster_set.go:1552`, separately gates `xN == len(db-servers-hosts)`.)

4. **`.compute` cores are "free" under the credit model but not under absolute €.** When mem
   binds, extra cores don't raise the credit count (`x2.small` and `x2.small.compute` are
   both 4 DB credits) — yet the registry charges more (`infracost` 50 vs 60). Credit pricing
   and absolute pricing disagree about what a core costs.

5. **Storage unit is undefined.** The code has only Database + Application units; "storage" is
   a technical decision not yet made (ratio TBD), so bulk-disk/backup pricing has no home yet.

6. **Absolute-€ plans vs per-unit pricing are not unified.** The legacy path prices a plan with
   four absolute € fields from the sheet; the unit model (§3.4) prices per unit per partner.
   Because a plan
   is just a shortcut that resolves to a unit count (§2.1), the fix is to make a plan's price
   **derived** (`UnitUsage × €/unit`) and retire the hand-entered € columns — instead of
   maintaining two sources of truth where "the price" means different things depending on
   where you look.

7. **Silent price failures (already documented in the plan-apply path).**
   - Count-gate mismatch → `SetServicePlan` returns *"Plan not possible for that cluster"*
     and skips the price, while the API still returns "Successfully reloaded"
     (`cluster_set.go:1552`, `server/api_cluster.go` reload handlers — instrumented on
     develop).
   - Zero/default cost is *deleted* on save (`cluster/cluster.go:1809-1811`), so a plan that
     matches no registry row publishes a blank price rather than an error.

## 8. Status & next steps

**Done on `marketplace-pricing` (Ahmad):** pricing-mode switch (`csv-service-plan` vs
`global-unit-pricing`); `ComputeDBUPerNode` / `ComputeDatabaseUnits` (Database units, IOPS
included); `ComputeApplicationUnits` (app credits + live proxy cores); per-partner €/unit +
marketplace metadata settings; units persisted on the cluster for the BO; plans disabled in
unit mode; tests + Marketplace GUI.

**Remaining:**
1. **Resolve §7.3 (HA vs `× node_count`)** — the one blocking design call.
2. **Storage unit** — decide the ratio (§4.2), add `ComputeStorageUnits` + a
   €/storage-unit price, mirroring the Database/Application paths.
3. **Enforce the ratio lock (§2)** on *user* DB sizing — reject or snap off-ratio provisioning
   to whole Database units so the configurator never gets an unbalanced shape.
4. **Retire per-plan absolute €** once unit pricing is default (§7.6), or keep
   `csv-service-plan` as a legacy-only mode.
5. Legacy silent-price failures (§7.7) are moot in unit mode but live while `csv-service-plan`
   remains.
