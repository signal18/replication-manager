# Cloud18 App HA Structural Pricing — Plan

**Status:** future implementation plan. This document is intentionally separate from
`CLOUD18_CREDIT_MODEL.md` so the base unit model
stays unchanged.

**Note:** the filename is kept for continuity, but the model described here is **structural
pricing** for App HA, not a marketplace promotion plan.

> **Settlement-boundary alignment.** This document describes a backend pricing-layer
> adjustment. Under `../crm/CRM_SETTLEMENT.md`, CRM must not derive App HA price
> from topology or unit counts itself. Replication-manager applies any structural
> pricing rule locally, resolves the final app commercial amount in EUR, and sends
> that resolved amount to CRM as settlement `app_amount`.

## 1. Purpose

Define a future **commercial pricing adjustment** for Application / Compute workloads when an app is
 deployed in HA mode across multiple OpenSVC nodes.

For the first implementation, the best model is to treat App HA pricing as a **structural pricing
rule**, not as a marketplace promotion. In other words, failover should cost less than flex because
that is the product model, while promotions remain a separate later pricing layer.

This plan distinguishes two HA modes:

1. **failover**
   - active-standby
   - shared storage pool
   - standby capacity is not fully utilized
2. **flex**
   - active-active
   - non-shared storage pool
   - all nodes actively consume value

The pricing consequence is:

- **flex gets no structural adjustment**
- **failover may receive a structural pricing adjustment**
- promotions, if any, apply later as a separate pricing layer

## 2. Core design choice

The best option is to keep **Application Units technical** and apply any HA adjustment only in the
**commercial pricing layer**.

That means:

- `ComputeApplicationUnits()` remains the technical consumption signal
- Application Unit math is **not** reduced for failover
- failover structural pricing changes only the **billed app price**, not the technical unit count

For the settlement path, that billed app price maps to the backend-owned settlement
field `app_amount`.

This preserves comparability and avoids mixing accounting with commercial policy.

### 2.1 Pricing layers

The clean pricing model is layered:

1. **Technical metering layer**
   - technical DB units
   - technical app units
   - technical proxy units
   - app topology and node count
2. **Base unit pricing layer**
   - raw `units × unit price` pricing before any commercial adjustment
3. **Structural pricing layer**
   - normal product-model pricing rules
   - App HA failover adjustment lives here
4. **Promotion layer**
   - temporary or business overlays such as campaigns, coupons, bundle offers, or partner promos
5. **Final billing layer**
   - final totals after structural pricing and promotions are applied

Recommended order:

- `technical units -> base unit pricing -> structural pricing adjustments -> promotions -> final price`

This keeps failover cheaper **by design**, while leaving room for separate promotions later.

> **Settlement note.** The output of these pricing layers is a backend-resolved
> commercial app price snapshot. CRM consumes the resolved snapshot; it does not
> recalculate the structural adjustment or final app price from units.

Promotion compatibility rule:

- promotions operate on the **post-structure commercial subtotal** for whatever scope they target
  (for example app-only, bundle-level, or full cluster subtotal)
- promotions never rewrite raw technical units
- this plan defines the **ordering boundary** only; promotion policy, eligibility, stacking, and
  scope remain future work

## 3. Technical baseline

Application Units remain as defined in the main implementation plan:

- per-node App Unit shape:
  - `1 core`
  - `4 GB RAM`
  - `10 GB disk`
- per-node units are derived from resource shape
- total technical units are:
  - `AppUnitsPerNode × AppNodeCount`

Example:

- 3 flex nodes
- 2 Application Units per node
- technical consumption = **6 Application Units**

That same technical count also applies to failover deployments before structural pricing adjustment.

## 4. HA mode classification

For this plan, app HA mode classification is:

### 4.1 failover

- active-standby
- shared storage pool (for example DRBD-backed)
- one active node, remaining nodes are standby capacity
- eligible for structural pricing adjustment

### 4.2 flex

- active-active
- non-shared storage pool (for example ZFS pool)
- all nodes are considered active capacity
- not eligible for structural pricing adjustment

## 5. Chosen structural pricing model

Use a **standby-factor** model.

This is better than a flat percentage discount because it scales with node count and better matches
actual utilization differences between failover and flex.

For the first implementation, use these recommended policy choices:

- `StandbyFactor` = **configurable**
- `StandbyFactor` default = **`0.3`**
- `StandbyFactor` initial scope = **global server-level setting**
- **single-node apps receive no structural adjustment**
- **proxy workloads receive no structural adjustment**
- structural pricing adjustment is computed **per app**, never from the cluster-wide combined
  `ApplicationUnits` total
- promotions are a **separate later layer** and must not be mixed into the HA structural pricing rule

### 5.1 Formula

For a failover app:

- `AppUnitsPerNode` = resource-derived units for one app node
- `ActiveNodes` = `1`
- `StandbyNodes` = `AppNodeCount - 1`
- `StandbyFactor` = configurable value between `0` and `1`

Then:

- `BillableAppUnits = AppUnitsPerNode × (ActiveNodes + StandbyNodes × StandbyFactor)`

And:

- `AppPrice = BillableAppUnits × AppUnitPrice`

This is a **structural pricing adjustment** to normal app pricing, not a promotion.

### 5.2 Flex rule

For a flex app:

- `BillableAppUnits = TechnicalApplicationUnits`
- no structural pricing adjustment is applied

### 5.3 Single-node rule

For an app with fewer than 2 nodes:

- `BillableAppUnits = TechnicalApplicationUnits`
- no structural pricing adjustment is applied, even if topology metadata says `failover`

This avoids treating non-HA or partially configured apps as failover-priced deployments.

### 5.4 Proxy rule

Proxy workloads do **not** participate in this app HA structural pricing model.

- proxy technical units remain computed under the normal App/Compute Unit ratio
- proxy commercial pricing remains full price
- any future proxy-specific structural pricing model must be designed separately

## 6. Worked examples

### 6.1 Flex example

- topology = `flex`
- `AppNodeCount = 3`
- `AppUnitsPerNode = 2`

Result:

- technical units = `2 × 3 = 6`
- billed units = `6`

### 6.2 Failover example

- topology = `failover`
- `AppNodeCount = 3`
- `AppUnitsPerNode = 2`
- `StandbyFactor = 0.3`

Result:

- technical units = `2 × 3 = 6`
- billed units = `2 × (1 + 2 × 0.3) = 3.2`

### 6.3 Single-node example

- topology = `failover` or `flex`
- `AppNodeCount = 1`
- `AppUnitsPerNode = 2`

Result:

- technical units = `2 × 1 = 2`
- billed units = `2`

### 6.4 Mixed-cluster example

Example cluster:

- 3 DB nodes
- 2 proxies
- 3 apps:
  - App A: 2 nodes, `flex`
  - App B: 2 nodes, `failover`
  - App C: 1 node

Let:

- `FlexUnitsPerNode = FA`
- `FailoverUnitsPerNode = FB`
- `SingleNodeUnitsPerNode = FC`
- `ProxyUnitsPerNode = PX`

Then:

- technical app units = `2 × FA + 2 × FB + 1 × FC`
- technical proxy units = `2 × PX` (assuming both proxies are live)
- current technical combined `ApplicationUnits` export = `2 × FA + 2 × FB + 1 × FC + 2 × PX`

Billable app units only:

- flex app = `2 × FA`
- failover app = `FB × (1 + 0.3)`
- single-node app = `1 × FC`

So:

- `BillableAppUnits = 2 × FA + 1.3 × FB + FC`

This example shows why the failover structural pricing rule must be applied **per app** and why pricing cannot be
derived safely from the current cluster-wide `ApplicationUnits` total alone, because that figure
includes proxies and mixed app topologies.

## 7. Scope boundaries

### In scope for the future implementation

- classify app HA mode as `failover` vs `flex`
- compute **billable** app units or equivalent structurally adjusted app price for failover
- keep DB pricing unchanged
- keep technical `ApplicationUnits` unchanged
- keep single-node apps at full price
- keep proxy pricing unchanged
- expose enough raw technical detail so BO/pricing can apply app-only failover structural pricing safely
- keep App HA structural pricing separate from marketplace promotions

### Out of scope for this plan

- changing DBU math
- changing `ComputeDatabaseUnits()`
- changing the App Unit ratio
- changing technical `ApplicationUnits`
- storage pricing
- fixed-amount discounts
- marketplace promotions for DB/app bundles
- proxy-specific HA structural pricing models
- general promotion policy/engine design beyond the App HA structural layer

## 8. Authoritative inputs needed

Future implementation must use authoritative runtime/config inputs for:

- app HA topology (`prov-app-ha-topology`)
- app/OpenSVC deployment node list
- per-node app resource shape

For the first implementation, use these concrete rules:

- **app topology source** = `prov-app-ha-topology`
- **app node count source** = the app's configured deployment node list (`prov-app-agents` /
  `GetAppAgents()`)
- **failover pricing eligibility** requires:
  - app is provisioned
  - topology is `failover`
  - node count is `>= 2`
- `prov-app-agents-failover` is **not** the pricing node-count authority for the first
  implementation; it may describe placement/failover behavior, but structural pricing eligibility is based on
  total configured app nodes

Promotion inputs, if added later, must be handled in a separate pricing layer and must not alter the
technical or App HA structural inputs above.

Additional validation should ensure:

- failover topology is only structurally adjusted when it is actually configured as failover
- flex topology is never structurally adjusted
- single-node apps are never structurally adjusted
- missing or ambiguous topology falls back to **no structural adjustment**

## 9. Likely code areas for future work

This is a follow-up plan; exact implementation is deferred. Likely touchpoints:

- `cluster/cluster.go`
  - keep `ComputeApplicationUnits()` technical
  - add separate helpers for:
    - technical app-only units
    - technical proxy-only units
    - billable app units after App HA structural pricing adjustment
- app/OpenSVC topology helpers
  - authoritative node count and topology detection
- pricing/export layer
  - expose structural app pricing separately from technical unit count
  - expose promotion pricing separately from structural app pricing
  - do not derive failover structural pricing from the current combined `ApplicationUnits` field alone
- UI/catalog/integration layer
  - render App HA structural pricing separately from promotions without corrupting technical unit accounting

Preferred export direction for first implementation:

- `technicalAppUnits`
- `technicalProxyUnits`
- `technicalApplicationUnitsTotal`
- `billableAppUnits`
- `baseAppPrice`
- `appHAStructuralAdjustment`
- optional promotion metadata / promotion adjustment
- `finalAppPrice`

> **Settlement alignment.** For CRM settlement, `finalAppPrice` is the backend-resolved
> app commercial amount that should feed settlement `app_amount`. The additional
> technical and structural fields are useful as backend/export metadata, but they are
> not a license for CRM to recompute price from units.

## 10. Recommended decisions for first implementation

1. `StandbyFactor` is **configurable**
2. Default `StandbyFactor` = `0.3`
3. `StandbyFactor` should be a **global server-level setting** for the first implementation
4. Single-node apps remain **full price**
5. Proxy workloads remain **full price** and outside this structural pricing model
6. Technical `ApplicationUnits` remain unchanged
7. Prefer exporting **raw technical app/proxy totals plus billable app units / structural metadata**
   rather than replacing raw technical totals with structurally adjusted figures
8. App HA pricing is a **structural pricing rule**, not a promotion
9. Promotions should apply **after** structural App HA pricing adjustments

## 10.1 Future questions still open

1. Should `StandbyFactor` later become partner-specific or per-offer?
2. Should proxy workloads ever participate in a separate HA structural pricing model?
3. Should BO consume structurally adjusted billable app units directly, or compute final price from raw totals
   plus structural/promotion metadata?
4. Which promotion scopes should be supported first: app-only, bundle-level, or full cluster subtotal?

> **Current settlement answer.** For the CRM settlement path, question 3 is no longer
> open: replication-manager should compute the final app commercial amount locally and
> send that resolved amount to CRM. Any remaining openness here applies only to other
> BO/export or presentation paths, not to settlement authority.

## 11. Validation plan

Future tests should prove:

- flex receives no structural adjustment
- failover receives a structural pricing adjustment
- single-node apps receive no structural adjustment
- proxies receive no structural adjustment
- technical `ApplicationUnits` do not change between flex and failover
- only the app commercial price changes
- DB price is unaffected
- 3-node failover and 3-node flex can have identical technical units but different billed app price
- mixed clusters with apps + proxies are priced correctly without structurally adjusting proxy units
- missing or ambiguous topology does not receive a structural pricing adjustment
- promotions, if added later, stack after structural pricing and do not alter technical units

## 12. Expected outcome

After the future implementation:

- Application Units remain a hard technical signal
- failover apps are billed more fairly than fully utilized flex apps
- single-node apps remain fully billed
- proxies remain fully billed
- flex remains fully billed because it consumes active multi-node value
- App HA structural pricing stays isolated in the pricing layer, not in the unit-accounting layer
- promotions remain a separate layer on top of the structural pricing model
