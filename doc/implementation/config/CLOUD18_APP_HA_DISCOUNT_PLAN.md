# Cloud18 App HA Discount — Plan

**Status:** future implementation plan. This document is intentionally separate from
`CLOUD18_CREDIT_MODEL.md` and `CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md` so the base unit model
stays unchanged.

## 1. Purpose

Define a future **commercial pricing adjustment** for Application / Compute workloads when an app is
 deployed in HA mode across multiple OpenSVC nodes.

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

- **flex gets no discount**
- **failover may receive a discount**

## 2. Core design choice

The best option is to keep **Application Units technical** and apply any HA adjustment only in the
**commercial pricing layer**.

That means:

- `ComputeApplicationUnits()` remains the technical consumption signal
- Application Unit math is **not** reduced for failover
- failover discount changes only the **billed app price**, not the technical unit count

This preserves comparability and avoids mixing accounting with commercial policy.

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

That same technical count also applies to failover deployments before discounting.

## 4. HA mode classification

For this plan, app HA mode classification is:

### 4.1 failover

- active-standby
- shared storage pool (for example DRBD-backed)
- one active node, remaining nodes are standby capacity
- eligible for commercial discount

### 4.2 flex

- active-active
- non-shared storage pool (for example ZFS pool)
- all nodes are considered active capacity
- not eligible for discount

## 5. Chosen discount model

Use a **standby-factor** model.

This is better than a flat percentage discount because it scales with node count and better matches
actual utilization differences between failover and flex.

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

### 5.2 Flex rule

For a flex app:

- `BillableAppUnits = TechnicalApplicationUnits`
- no discount is applied

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

## 7. Scope boundaries

### In scope for the future implementation

- classify app HA mode as `failover` vs `flex`
- compute **billable** app units or equivalent discounted app price for failover
- keep DB pricing unchanged
- keep technical `ApplicationUnits` unchanged

### Out of scope for this plan

- changing DBU math
- changing `ComputeDatabaseUnits()`
- changing the App Unit ratio
- changing technical `ApplicationUnits`
- storage pricing
- fixed-amount discounts
- marketplace promotions for DB/app bundles

## 8. Authoritative inputs needed

Future implementation must use authoritative runtime/config inputs for:

- app HA topology (`prov-app-ha-topology`)
- app/OpenSVC deployment node list
- per-node app resource shape

Additional validation should ensure:

- failover topology is only discounted when it is actually configured as failover
- flex topology is never discounted

## 9. Likely code areas for future work

This is a follow-up plan; exact implementation is deferred. Likely touchpoints:

- `cluster/cluster.go`
  - keep `ComputeApplicationUnits()` technical
  - add a separate billable-app-unit or app-price helper
- app/OpenSVC topology helpers
  - authoritative node count and topology detection
- pricing/export layer
  - expose discounted app price separately from technical unit count
- UI/catalog/integration layer
  - render failover discount without corrupting technical unit accounting

## 10. Open questions to lock before implementation

1. What is the default `StandbyFactor`?
2. Is `StandbyFactor` global, partner-specific, or per-offer?
3. Should the discounted figure be exported as:
   - discounted billable app units, or
   - base app units + separate discount metadata?
4. Should proxy workloads ever participate in this same failover discount model, or remain full
   priced unless explicitly modeled later?

## 11. Validation plan

Future tests should prove:

- flex receives no discount
- failover receives a discount
- technical `ApplicationUnits` do not change between flex and failover
- only the app commercial price changes
- DB price is unaffected
- 3-node failover and 3-node flex can have identical technical units but different billed app price

## 12. Expected outcome

After the future implementation:

- Application Units remain a hard technical signal
- failover apps are billed more fairly than fully utilized flex apps
- flex remains fully billed because it consumes active multi-node value
- discount logic stays isolated in the pricing layer, not in the unit-accounting layer
