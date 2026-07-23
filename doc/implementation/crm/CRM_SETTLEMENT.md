# Cloud18 Backend/CRM Settlement Plan

**Status:** implementation plan

**Repository note:** the canonical home for this document is the **`crm-api`** repository.
When this plan is mirrored or reviewed from `replication-manager`, references such as
`docs/...` point to **`crm-api` repo paths**, not to files in this repository.

**Purpose:** define the correct boundary between backend and CRM for Cloud18 pricing, usage reporting,
and balance settlement without mixing pricing authority, settlement authority, and future refund
automation.

## 1. Executive summary

The correct implementation is:
1. **Backend owns pricing resolution and billing event production.**
2. **Sponsorship acceptance is the only authority that activates base cluster billing.**
3. **CRM owns EUR settlement, balance movement, billing cycles, and ledger history.**
4. **App provision / unprovision events are the authority for app billing state changes.**
5. **Base cluster cost is the fixed non-refundable baseline.**
6. **Only app credit is refundable or creditable.**
7. **Usage telemetry remains evidence and reconciliation input, not direct debit authority.**

This matches the direction in `docs/CONSUMER_CLOUD18_CREDIT_MODEL.md`:

- the cluster base contract is fixed,
- app usage is the refundable delta around that baseline,
- and lower app state should not let CRM invent charges or refunds from first-seen observations.

## 2. Frozen decisions

The following decisions are fixed for this plan.

### 2.1 Responsibility split

**Backend owns:**

- `pricing_mode`
- base cluster pricing snapshot
- app pricing snapshot per app billing event
- `db_units`
- `app_units`
- `infra_amount`
- `license_amount`
- `sysops_amount`
- `dbops_amount`
- `base_amount`
- `app_amount`
- `cluster_ref`
- `event_key`
- `event_sequence`
- `sponsorship_cycle_ref`
- `billing_owner_ref`
- `sponsorship_last_event_id`
- `app_ref`

**CRM owns:**

- validation of payload freshness and sponsorship binding consistency
- balance-account lookup from `billing_owner_ref`
- cluster billing activation state
- app billing item state
- billing-cycle opening and closing
- debit transactions
- app-only refund or credit transactions
- idempotency
- historical ledger

### 2.2 Pricing modes

Both pricing modes must remain supported:

1. `csv-service-plan`
2. `global-unit-pricing`

CRM does not calculate either mode. Backend sends the authoritative resolved price snapshot for the
event being recorded.

### 2.3 Accounting unit

The accounting ledger must be monetary:

- settlement currency: `EUR`
- storage type: MySQL `DECIMAL(12,2)`
- API/metadata representation: decimal strings such as `"12.34"`

`db_units` and `app_units` remain operational pricing metadata, not the accounting currency.

### 2.4 Historical truth

CRM must store the backend-provided billing event payload used for settlement and must never
recompute old transactions from current prices or current formulas.

### 2.5 Billing authority rule

The brownfield-safe settlement rule is:

- no charge from first-seen cluster state,
- no charge from first-seen usage increase,
- no charge from passive usage snapshots,
- charge only from explicit billing lifecycle events plus recurring billing-cycle processing.

### 2.6 Base versus App commercial behavior

**Base cluster:**

- starts when sponsorship becomes actively billable,
- uses the backend-resolved base cluster price snapshot,
- is billed every cycle while the sponsorship remains active,
- is not refundable from lower observed usage.

**Application / Web:**

- becomes billable on `app_provisioned`,
- stops future billing on `app_unprovisioned`,
- is the only refundable or creditable portion,
- remains eligible for later measurement-backed reconciliation.

## 3. Scope boundaries

### 3.1 In scope now

Phase 1 must include only the foundation below:

1. internal backend-to-CRM settlement event ingest route
2. immutable billing event storage
3. EUR balance ledger
4. cluster billing state
5. app billing item state
6. recurring billing-cycle charging
7. app refund-ready hooks, but no measurement-driven refund automation
8. peer-compatible latest-price mirroring
9. brownfield-safe import and activation rules

### 3.1.1 Backward compatibility boundary

Backward compatibility for this plan is defined by the current consumer route set documented in
`docs/CONSUMER.md`.

For Phase 1, the public CRM routes that must remain externally compatible for current
replication-manager and frontend consumers are:

1. `GET /api/credits/personal`
2. `GET /api/users/transactions`

Compatibility here is route-contract compatibility:

- preserve the existing route paths,
- preserve the existing auth model,
- preserve bootstrap and status behavior relied on by current consumers,
- preserve currently used response fields unchanged,
- allow only additive response fields in Phase 1.

This compatibility promise is about the public route behavior, not about preserving any particular
CRM-internal table names or storage implementation.

For Phase 1, `docs/CONSUMER.md` is the compatibility source of truth for these public routes.

If CRM groundwork is implemented before consumer emits the new internal settlement events, follow
`docs/CLOUD18_CRM_PRE_CONSUMER_EXECUTION_PLAN.md` and defer live public-route repoint until the
consumer cutover gate is met.

### 3.2 Explicitly out of scope now

The following are deferred and must not be added to Phase 1:

1. charging from raw usage snapshots alone
2. automatic measurement-driven app refunds
3. refund finalizers driven only by sustained lower telemetry
4. public UI work
5. changing the external contract of existing public end-user billing routes
6. CRM-side price calculation for either pricing mode
7. replacing existing legacy pricing routes as part of the same implementation
8. promotion logic on top of unit pricing

## 4. High-level architecture

### 4.1 Flow

1. Backend accepts sponsorship with a valid `billing_owner_ref`.
2. Backend resolves the authoritative base cluster price snapshot for that sponsorship episode.
3. Backend sends `sponsorship_started` to CRM.
4. CRM opens active cluster billing state and records the current base cluster commercial baseline
   from that sponsorship start time.
5. Backend sends `app_provisioned` and `app_unprovisioned` events as app lifecycle changes occur.
6. CRM updates app billing item state idempotently from those events.
7. At each billing cycle, CRM charges the active base cluster amount and all active app amounts.
8. If app policy grants a credit or refund, CRM creates app-only monetary adjustments.
9. Usage telemetry may be stored for evidence and reconciliation, but it does not directly create a
   debit.
10. CRM mirrors the latest effective component amounts into the existing peer-compatible config
    keys.

### 4.2 Ownership rule

Backend is the **pricing authority**.

CRM is the **settlement authority**.

This means:

- CRM trusts backend event payloads for pricing and sponsorship binding identity.
- CRM does not derive price from units by itself.
- CRM does not reconstruct sponsorship truth from BO, git, or local heuristics.
- CRM does not infer billable app creation from raw usage deltas alone.
- CRM is responsible for billing-cycle execution, monetary balance movement, and ledger history.

## 5. Internal contract

### 5.1 Route

Add a dedicated internal route for backend settlement events.

Recommended path:

- `POST /api/internal/settlement-events`

This route is internal service-to-service traffic and must not reuse end-user PAT authentication.

It is additive only and is not part of the `docs/CONSUMER.md` backward-compatibility set.

### 5.2 Authentication

Use a dedicated per-instance machine credential for the settlement sender.

Preferred Phase 1 model:

1. CRM issues a settlement credential per registered instance,
2. replication-manager stores it locally,
3. `POST /api/internal/settlement-events` authenticates that per-instance
   credential from day one of the new route,
4. CRM resolves sender identity from the credential binding, not from a global
   shared secret.

Recommended header form:

```http
Authorization: Bearer <settlement_secret>
X-Settlement-Key-Id: <settlement_key_id>
```

See `docs/CLOUD18_SETTLEMENT_INSTANCE_AUTH_PLAN.md` for the credential issue,
rotate, revoke, and active-standby rules.

### 5.3 Request payload

Required common fields:

```json
{
  "event_type": "app_provisioned",
  "event_key": "cluster:123:app:web-1:provision:2026-07-23T10:00:00Z",
  "event_sequence": 182,
  "cluster_ref": "cluster-123",
  "occurred_at": "2026-07-23T10:00:00Z",
  "pricing_mode": "global-unit-pricing",
  "currency": "EUR",
  "sponsorship_cycle_ref": "spc_01...",
  "billing_owner_ref": "own_01...",
  "sponsorship_last_event_id": "evt_000000000042"
}
```

Supported `event_type` values in Phase 1:

1. `sponsorship_started`
2. `sponsorship_ended`
3. `base_plan_changed`
4. `app_provisioned`
5. `app_unprovisioned`

Additional required fields by event type:

- `sponsorship_started`
  - `db_units`
  - `infra_amount`
  - `license_amount`
  - `sysops_amount`
  - `dbops_amount`
  - `base_amount`
- `base_plan_changed`
  - `db_units`
  - `infra_amount`
  - `license_amount`
  - `sysops_amount`
  - `dbops_amount`
  - `base_amount`
- `app_provisioned`
  - `app_ref`
  - `app_units`
  - `app_amount`
- `app_unprovisioned`
  - `app_ref`

Optional fields:

- `usage_snapshot_json`
- `pricing_inputs_json`
- `backend_metadata_json`

If an app changes size, backend should emit `app_unprovisioned` for the old billable app shape and
`app_provisioned` for the new one rather than introducing a second price authority.

### 5.4 Contract rules

1. Backend always sends the resolved price snapshot for the event being recorded.
2. `event_key` must be unique and stable across retries.
3. `event_sequence` must be monotonically increasing for one cluster billing stream.
4. `currency` is `EUR` for this implementation.
5. Active billable events must include `billing_owner_ref`.
6. `sponsorship_last_event_id` must represent the current authoritative sponsorship watermark from
   the producer.
7. `app_ref` must be stable across the lifecycle of one logical app billing item.
8. Payloads must be built from current authoritative local state only, not from history replay,
   CRM lookups, or BO lookups.
9. CRM treats event payloads as authoritative pricing and binding input, subject to freshness and
   consistency validation.
10. Passive usage telemetry must never, by itself, create a charge.

## 6. Data model

### 6.1 `cluster_billing_events`

Purpose: immutable backend billing evidence used for settlement.

Recommended columns:

- `id`
- `cluster_ref`
- `event_key` unique
- `event_sequence`
- `event_type`
- `occurred_at`
- `pricing_mode`
- `currency`
- `db_units` `DECIMAL(12,2)` nullable
- `app_ref` nullable
- `app_units` `DECIMAL(12,2)` nullable
- `infra_amount` `DECIMAL(12,2)` nullable
- `license_amount` `DECIMAL(12,2)` nullable
- `sysops_amount` `DECIMAL(12,2)` nullable
- `dbops_amount` `DECIMAL(12,2)` nullable
- `base_amount` `DECIMAL(12,2)` nullable
- `app_amount` `DECIMAL(12,2)` nullable
- `sponsorship_cycle_ref`
- `billing_owner_ref`
- `sponsorship_last_event_id`
- `usage_snapshot_json` `JSON` or `TEXT`
- `pricing_inputs_json` `JSON` or `TEXT`
- `backend_metadata_json` `JSON` or `TEXT`
- `received_at`

### 6.2 `cluster_billing_state`

Purpose: one current billing baseline per active cluster sponsorship episode as last accepted by
CRM.

Recommended columns:

- `cluster_ref`
- `billing_status` (`active`, `ended`, `paused`)
- `current_period_ref`
- `current_period_started_at`
- `next_billing_at`
- `last_event_id`
- `last_event_sequence`
- `last_sponsorship_cycle_ref`
- `last_billing_owner_ref`
- `last_sponsorship_last_event_id`
- `current_db_units` `DECIMAL(12,2)`
- `current_base_amount` `DECIMAL(12,2)`
- `current_total_active_app_amount` `DECIMAL(12,2)`
- `currency` `CHAR(3)`
- `updated_at`

### 6.3 `cluster_app_billing_items`

Purpose: track the current billable state of each app independently from the base cluster.

Recommended columns:

- `id`
- `cluster_ref`
- `app_ref`
- `status` (`active`, `inactive`)
- `last_provision_event_id`
- `last_unprovision_event_id` nullable
- `current_app_units` `DECIMAL(12,2)`
- `current_app_amount` `DECIMAL(12,2)`
- `activated_at`
- `deactivated_at` nullable
- `updated_at`

### 6.4 `balance_accounts`

Purpose: EUR balance per billed owner as resolved from `billing_owner_ref`.

Recommended columns:

- `id`
- `billing_owner_ref` unique
- `owner_type`
- `client_id`
- `user_id`
- `currency` `CHAR(3)`
- `balance` `DECIMAL(12,2)`
- `created_at`
- `updated_at`

### 6.5 `balance_ledger_entries`

Purpose: immutable EUR transaction history.

Recommended columns:

- `id`
- `account_id`
- `entry_type`
- `amount` `DECIMAL(12,2)`
- `balance_after` `DECIMAL(12,2)`
- `idempotency_key` unique
- `reference_type`
- `reference_id`
- `metadata_json`
- `created_at`

### 6.6 `cluster_billing_cycles`

Purpose: one settlement record per cluster billing period.

Recommended columns:

- `id`
- `cluster_ref`
- `billing_period_ref`
- `period_started_at`
- `period_ended_at`
- `base_amount_charged` `DECIMAL(12,2)`
- `app_amount_charged` `DECIMAL(12,2)`
- `app_amount_credited` `DECIMAL(12,2)`
- `currency` `CHAR(3)`
- `status`
- `created_at`

Use a unique key on `cluster_ref + billing_period_ref`.

## 7. Settlement rules

### 7.1 Sponsorship activation

CRM must validate each new billing event against `cluster_billing_state` before settlement.

Validation must reject events that are stale or binding-inconsistent, including:

- `event_sequence` older than `last_event_sequence`
- `sponsorship_last_event_id` older than `last_sponsorship_last_event_id`
- unexpected `billing_owner_ref` change within the same active `sponsorship_cycle_ref`
- unknown or unmapped `billing_owner_ref`

Billing starts only after `sponsorship_started` is accepted.

That event:

- opens the active billing episode,
- establishes the fixed base cluster commercial baseline,
- is the only valid start of billable cluster state,
- must never be synthesized from first-seen cluster presence or first-seen usage.

### 7.2 Base cluster behavior

The base cluster amount is the fixed non-refundable baseline described by
`docs/CONSUMER_CLOUD18_CREDIT_MODEL.md`.

Rules:

- base billing starts when sponsorship becomes actively billable,
- `sponsorship_started` makes the base cluster chargeable from its `occurred_at` time under the
  configured current-cycle policy,
- base billing uses backend-resolved `base_amount`,
- base billing renews every billing cycle while the sponsorship remains active,
- lower observed DB or cluster usage does not create a base refund,
- base amount changes only from explicit commercial lifecycle events such as
  `base_plan_changed`, not from passive telemetry.

### 7.3 Application behavior

Application units are the delta around the fixed base contract.

Rules:

- `app_provisioned` creates or reactivates one billable app item,
- `app_provisioned` corresponds to an app usage increase, but the billing event is the accounting
  authority,
- `app_provisioned` makes that app chargeable from its `occurred_at` time under the configured
  current-cycle policy,
- `app_unprovisioned` closes that app item and stops future app-cycle charging,
- `app_unprovisioned` corresponds to an app usage decrease, but the billing event is the accounting
  authority,
- `app_unprovisioned` ends app chargeability from its `occurred_at` time under the configured
  current-cycle policy,
- app decreases must not be inferred solely from first-seen lower usage,
- only app amounts are eligible for refund or credit,
- usage measurements may validate app state later, but they do not create the initial charge.

### 7.4 Billing cycle processing

At each billing cycle, CRM must:

1. lock the active cluster billing state,
2. resolve and lock the EUR balance account,
3. charge the current base amount if the sponsorship is active,
4. charge all active app billing items,
5. apply any app-only credits or refunds allowed by policy,
6. write the billing-cycle record,
7. commit.

The exact policy for same-cycle proration may be configured later, but the following are fixed now:

- recurring base billing happens only while sponsorship is active,
- base billing is never refunded because observed usage fell,
- app-only credits may exist,
- no billing cycle may create duplicate debits or duplicate app credits.

### 7.5 Usage evidence role

Usage snapshots are allowed as evidence and future reconciliation input, but they are not direct
settlement authority in this plan.

They may be used for:

- operator audit,
- mismatch detection,
- later measurement-backed app refund validation,
- later overage or underuse reporting.

They must not be used for:

- brownfield activation,
- charging a cluster that has not had `sponsorship_started`,
- charging an app that has not had `app_provisioned`.

## 8. Idempotency and anti-duplication

Phase 1 must guarantee no duplicate charges and no duplicate app credits.

### 8.1 Event idempotency

- `cluster_billing_events.event_key` is unique
- retries of the same backend event must not create a second settlement movement

### 8.2 Event ordering and binding freshness

- `event_sequence` is the ordering source for one cluster billing stream
- `sponsorship_last_event_id` is the ordering source for sponsorship binding freshness
- stale events must be rejected before billing-state mutation

### 8.3 Billing-cycle idempotency

- each cluster billing period uses a deterministic unique `billing_period_ref`
- duplicate cycle processing is treated as replay, not a second charge

### 8.4 Ledger idempotency

- each ledger entry uses a deterministic unique `idempotency_key`
- duplicate insert is treated as replay, not a second charge

Recommended key shapes:

- `cluster_ref + sponsorship_cycle_ref + "base-start"`
- `cluster_ref + billing_period_ref + "base-renewal"`
- `cluster_ref + app_ref + event_key + "app-activate"`
- `cluster_ref + app_ref + billing_period_ref + "app-renewal"`
- `cluster_ref + app_ref + billing_period_ref + "app-credit"`

### 8.5 Transaction boundary

For any monetary settlement movement, CRM must perform the following in one DB transaction:

1. lock billing state
2. resolve and lock the EUR balance account
3. insert ledger row
4. update account balance
5. update cluster or app billing state
6. update billing-cycle state when applicable
7. commit

This is the mandatory foundation for later refund safety as well.

## 9. Peer compatibility

After successful settlement-event processing, CRM mirrors the latest effective component amounts
into `clusters_config` using the existing peer-compatible keys:

- `cloud18-monthly-infra-cost`
- `cloud18-monthly-license-cost`
- `cloud18-monthly-sysops-cost`
- `cloud18-monthly-dbops-cost`
- `cloud18-cost-currency`

This keeps current peer/export consumers working without changing peer SQL in Phase 1.

CRM is only mirroring backend-authoritative state here, not recalculating it.

## 10. Public billing route compatibility

Phase 1 must preserve the externally consumed contracts of the existing billing routes:

1. `GET /api/credits/personal`
2. `GET /api/users/transactions`

When these routes are repointed to the new settlement source, CRM must preserve the route
behavior and response-shape compatibility relied on by current consumers.

Phase 1 compatibility for these routes is **additive only**:

- do not remove currently used fields,
- do not rename currently used fields,
- do not change current route-level bootstrap/status behavior,
- only add new fields where needed to surface EUR settlement context.

In Phase 1, these two routes remain **user-scoped compatibility views** for the authenticated CRM
user. They do **not** become direct sponsorship-owner routes, even though the internal settlement
ingest path is owner-bound through `billing_owner_ref`.

For `GET /api/credits/personal`, Phase 1 should preserve the currently consumed fields such as:

- `user_id`
- `owner_type`
- `balance`

Those fields remain the compatibility surface. If EUR settlement data is surfaced through this
route in Phase 1, it must be additive and must not remove, rename, or replace the currently used
fields.

It may add fields such as:

- `amount`
- `currency`

For `GET /api/users/transactions`, Phase 1 should preserve the current envelope and row-field
compatibility used by consumers, including:

- top-level `transactions`
- top-level `pagination`
- row fields such as `id`, `created_at`, `entry_type`, `amount`, `balance_after`,
  `idempotency_key`, `reference_type`, `reference_id`, and `metadata`

If EUR-specific settlement data needs to be exposed through the transaction rows, it must be added
without removing or renaming the currently consumed fields.

### 10.1 Bootstrap/status contract freeze

Before rollout, CRM must reconcile the live handler behavior with `docs/CONSUMER.md` and the
current end-to-end consumer expectation, then freeze one source-of-truth contract.

Because backward compatibility for this plan is defined by `docs/CONSUMER.md`, any mismatch must be
resolved before rollout by either:

- aligning the live handlers to `docs/CONSUMER.md`, or
- explicitly updating `docs/CONSUMER.md` first and treating the updated document as the frozen
  contract.

During Phase 1, that route-level bootstrap/status contract must not change as part of the
settlement-source migration.

This plan does not assume that compatibility depends on preserving current table names such as
`credit_balances` or `credit_ledger_entries`.

## 11. Brownfield rollout

Existing active sponsored clusters must not be back-billed accidentally.

### 11.1 Brownfield rule

For already-active clusters at rollout:

- import current active sponsorship state into billing state without creating historical debits,
- import current active app inventory as billing baseline without creating historical app debits,
- do not synthesize `sponsorship_started` charges from old state,
- do not synthesize `app_provisioned` charges from old state,
- do not create a retroactive refund.

The first real money movement for imported brownfield clusters must happen only from an explicit
chosen billing-cycle boundary or from new post-cutover billing events.

### 11.2 New clusters after rollout

For new clusters after cutover:

- `sponsorship_started` is the first billable event,
- the base cluster becomes billable from that sponsorship start,
- later app billing begins only from explicit `app_provisioned` events,
- no charge is created from first-seen usage snapshots.

## 12. Phases

### Phase 1: Event-driven settlement foundation

**Goal:** build the correct final architecture around explicit billing authority and recurring cycle
settlement.

Deliverables:

1. internal `settlement-events` ingest route
2. `cluster_billing_events`
3. `cluster_billing_state`
4. `cluster_app_billing_items`
5. `balance_accounts`
6. `balance_ledger_entries`
7. `cluster_billing_cycles`
8. sponsorship-start base billing activation
9. app provision / unprovision billing-state handling
10. recurring cycle charging for active base and app amounts
11. app refund-ready hooks without telemetry-only automation
12. peer-compatible cost mirroring
13. brownfield-safe import path
14. tests and docs

**Exit criteria:**

- duplicate event replay produces no second charge
- `sponsorship_started` opens billing once only
- `app_provisioned` creates one app billing activation only
- `app_unprovisioned` stops future app-cycle charges
- cycle renewal charges base and active apps once only
- brownfield import creates no historical debit
- no passive usage snapshot creates a charge
- latest component values are mirrored to peer-compatible fields
- public billing route compatibility remains preserved where repointed

### Phase 2: Measurement-backed app reconciliation

**Gate:** backend must provide a proven periodic confirmed usage feed suitable for reliable app-state
reconciliation.

Deliverables:

1. optional usage-evidence ingest and storage hardening
2. reconciliation between app event state and measured lower app usage
3. app-only refund or credit finalization driven by confirmed policy
4. exactly-once refund or credit finalization using cycle state + ledger idempotency in one
   transaction

**Exit criteria:**

- mismatched app event state can be detected from confirmed telemetry
- app-only credit creates exactly one monetary adjustment
- repeated finalizer runs create no duplicate app credit
- base cluster still never refunds from lower observed usage alone

### Phase 3: Legacy cleanup

Only after Phases 1 and 2 are stable:

1. deprecate old CRM-owned pricing assumptions where no longer needed
2. decide whether legacy cluster-rate APIs remain as compatibility/admin surfaces or are retired

This phase is intentionally not part of the current implementation scope.

## 13. Testing plan

Phase 1 tests must cover:

1. wrong method -> `405`
2. missing/invalid internal auth -> `401`
3. invalid JSON / missing required fields -> `400`
4. missing active billing binding inputs -> clear failure
5. duplicate `event_key` -> idempotent replay with no second charge
6. stale `event_sequence` -> rejected
7. stale `sponsorship_last_event_id` -> rejected
8. missing `billing_owner_ref` for active billable events -> rejected
9. unknown `billing_owner_ref` -> rejected
10. `sponsorship_started` -> exactly one billing activation and one base commercial baseline
11. `base_plan_changed` -> updates the base billing state once only
12. `app_provisioned` -> exactly one app billing activation
13. `app_unprovisioned` -> future app-cycle charge stops
14. recurring cycle settlement -> exactly one base renewal charge per billing period
15. recurring cycle settlement -> exactly one active-app renewal charge per billing period
16. brownfield import -> no historical ledger rows
17. passive usage evidence -> no transaction
18. peer-compatible fields updated correctly
19. `GET /api/credits/personal` keeps its current route path, auth, bootstrap/status behavior, and
    currently used fields; any EUR-oriented fields are additive only
20. `GET /api/users/transactions` keeps its current route path, auth, bootstrap/status behavior,
    envelope compatibility, and currently used row fields; any EUR-oriented fields are additive only
21. ledger metadata contains full pricing and sponsorship-binding snapshot for each monetary event
22. the live public billing-route behavior is reconciled with `docs/CONSUMER.md` before rollout and
    remains stable after any Phase 1 repoint

Per repo testing policy, any broad or expensive suite should be logged to a file.

## 14. Documentation updates

Phase 1 must update:

1. `README.md`
2. `docs/openapi.yaml`
3. `docs/CONSUMER_CLOUD18_CREDIT_MODEL.md`

The key clarification to add everywhere is:

- backend owns pricing snapshots and billing event production
- sponsorship acceptance activates base cluster billing
- CRM owns EUR settlement and recurring billing cycles
- app provision / unprovision drive app billing state changes
- backward compatibility is defined by the `docs/CONSUMER.md` route contracts, not by current CRM
  storage internals
- existing consumed billing-route fields remain contract-stable in Phase 1; those routes may be
  internally re-backed by the new settlement source, and any EUR settlement fields exposed there
  are additive only
- base cluster cost is the fixed non-refundable baseline
- only app credit is refundable or creditable
- passive usage snapshots are evidence and reconciliation inputs, not direct debit authority

## 15. Final recommendation

Implement **Phase 1 only** now.

This is the correct plan because it builds:

- the final ownership boundary,
- the correct accounting unit,
- explicit billing authority,
- immutable historical evidence,
- idempotent cycle settlement,
- peer compatibility,
- and a clean path to later measurement-backed app reconciliation.

It also closes the brownfield flaw by making one rule non-negotiable:

- nothing is charged merely because CRM first sees it.
