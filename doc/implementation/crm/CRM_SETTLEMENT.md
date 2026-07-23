# Cloud18 Backend/CRM Settlement Plan

**Status:** implementation plan

**Repository note:** the canonical home for this document is the **`crm-api`** repository.
When this plan is mirrored or reviewed from `replication-manager`, references such as
`docs/...` point to **`crm-api` repo paths**, not to files in this repository.

**Purpose:** define the correct boundary between backend and CRM for Cloud18 pricing, usage reporting,
and balance settlement without mixing pricing authority, settlement authority, and future refund
automation.

## 1. Executive summary

The best implementation is:

1. **Backend owns pricing and usage snapshots.**
2. **CRM owns settlement and ledger transactions.**
3. **Accounting is done in `EUR`, not in credits.**
4. **CRM stores backend snapshots immutably and never recomputes historical price later.**
5. **Phase 1 implements immediate debits only.**
6. **Automatic app refunds are deliberately deferred to a later phase gated by a proven periodic
   confirmed usage feed.**

This keeps scope tight while still building the final architecture in the right direction.

## 2. Frozen decisions

The following decisions are fixed for this plan.

### 2.1 Responsibility split

**Backend owns:**

- `pricing_mode`
- usage measurement / confirmed usage state
- `db_units`
- `app_units`
- `db_amount`
- `app_amount`
- resolved commercial price snapshot at that time
- `cluster_ref`
- `snapshot_key`
- `usage_sequence`
- `sponsorship_cycle_ref`
- `billing_owner_ref`
- `sponsorship_last_event_id`

**CRM owns:**

- validation of payload freshness and sponsorship binding consistency
- balance-account lookup from `billing_owner_ref`
- balance updates
- debit transactions
- future refund transactions
- idempotency
- settlement state
- historical ledger

### 2.2 Pricing modes

Both pricing modes must remain supported:

1. `csv-service-plan`
2. `global-unit-pricing`

CRM does not calculate either mode. Backend sends the authoritative price snapshot for both.

> **Pricing-source alignment.** The backend pricing inputs and structural pricing
> layering referenced here are described in
> `../config/CLOUD18_CREDIT_MODEL.md` and
> `../config/CLOUD18_APP_HA_DISCOUNT_PLAN.md`. Those documents define how
> replication-manager computes technical units and, where applicable, app
> structural pricing. This settlement plan defines that CRM consumes the **resolved
> EUR amounts** from backend, not the raw pricing logic.

### 2.3 Accounting unit

The accounting ledger must be monetary:

- settlement currency: `EUR`
- storage type: MySQL `DECIMAL(12,2)`
- API/metadata representation: decimal strings such as `"12.34"`

`db_units` and `app_units` remain operational pricing metadata, not the accounting currency.

That means:

- `db_units` / `app_units` help explain the backend pricing decision,
- `db_amount` / `app_amount` are the authoritative commercial amounts for settlement,
- CRM must not recompute the monetary result from the unit counts.

### 2.4 Historical truth

CRM must store the backend-provided snapshot used for settlement and must never recompute old
transactions from current prices or current formulas.

### 2.5 DB versus App settlement behavior

**Database:**

- debit immediately on increase
- no runtime auto-refund on lower observed usage

**Application / Web:**

- debit immediately on increase
- future refund path allowed only from sustained confirmed lower usage
- refund automation is out of scope for Phase 1

## 3. Scope boundaries

### 3.1 In scope now

Phase 1 must include only the foundation below:

1. internal backend-to-CRM settlement ingest route
2. immutable backend snapshot storage
3. EUR balance ledger
4. cluster settlement baseline state
5. immediate debit processing
6. peer-compatible latest-price mirroring
7. refund-ready state hooks, but no automatic refund execution

### 3.1.1 Backward compatibility boundary

Backward compatibility for this plan is defined by the current consumer route set documented in
`crm-api/docs/CONSUMER.md`.

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

For Phase 1, `crm-api/docs/CONSUMER.md` is the compatibility source of truth for these public routes.

### 3.2 Explicitly out of scope now

The following are deferred and must not be added to Phase 1:

1. automatic app refunds
2. refund cron/finalizer
3. public UI work
4. changing the external contract of existing public end-user billing routes
5. CRM-side price calculation for either pricing mode
6. replacing existing legacy pricing routes as part of the same implementation
7. promotion logic on top of usage pricing

## 4. High-level architecture

### 4.1 Flow

1. Backend measures or confirms current cluster usage state.
2. Backend resolves the current price snapshot for that state.
3. Backend builds one self-contained settlement snapshot from current authoritative local sponsorship
   state plus current usage/pricing state.
4. Backend sends that authoritative settlement snapshot to CRM.
5. CRM validates snapshot freshness, ordering, and sponsorship binding.
6. CRM stores that snapshot idempotently.
7. CRM compares the snapshot to the last settled baseline for the cluster binding.
8. CRM creates immediate debits when DB or App usage increases.
9. CRM records lower-app state for future refund support without creating a refund yet.
10. CRM mirrors the latest effective component amounts into the existing peer-compatible config
    keys.

### 4.2 Ownership rule

Backend is the **pricing authority**.

CRM is the **settlement authority**.

This means:

- CRM trusts the backend snapshot for pricing.
- CRM trusts the backend snapshot for sponsorship binding identity.
- CRM does not derive price from units by itself.
- CRM does not reconstruct sponsorship truth from BO, git, or local heuristics.
- CRM is responsible for monetary balance movement and ledger history.

## 5. Internal contract

### 5.1 Route

Add a dedicated internal route for backend settlement ingestion.

Recommended path:

- `POST /api/internal/usage-settlements`

This route is internal service-to-service traffic and must not reuse end-user PAT authentication.

It is additive only and is not part of the `crm-api/docs/CONSUMER.md` backward-compatibility set.

### 5.2 Authentication

Use a dedicated shared secret or internal token, for example:

- `Authorization: Bearer <INTERNAL_SETTLEMENT_TOKEN>`

### 5.3 Request payload

Required fields:

```json
{
  "cluster_ref": "cluster-123",
  "snapshot_key": "cluster:123:2026-07-23T10:00:00Z",
  "usage_sequence": 182,
  "measured_at": "2026-07-23T10:00:00Z",
  "pricing_mode": "global-unit-pricing",
  "usage_confirmed": true,
  "db_units": "12",
  "app_units": "5",
  "db_amount": "68.00",
  "app_amount": "32.00",
  "currency": "EUR",
  "infra_amount": "40.00",
  "license_amount": "15.00",
  "sysops_amount": "12.00",
  "dbops_amount": "33.00",
  "total_amount": "100.00",
  "sponsorship_cycle_ref": "spc_01...",
  "billing_owner_ref": "own_01...",
  "sponsorship_last_event_id": "evt_000000000042"
}
```

Optional fields:

- `pricing_inputs_json`
- `backend_metadata_json`

### 5.4 Contract rules

1. Backend always sends the resolved component snapshot.
2. Backend always sends `db_units` and `app_units`.
3. `snapshot_key` must be unique and stable across retries.
4. `currency` is `EUR` for this implementation.
5. `usage_sequence` must be monotonically increasing for one cluster settlement stream.
6. Active billable settlement snapshots must include `billing_owner_ref`.
7. `sponsorship_last_event_id` must represent the current authoritative sponsorship watermark from
   the producer.
8. Payloads must be built from current authoritative local state only, not from history replay,
   CRM lookups, or BO lookups.
9. CRM treats the payload as authoritative pricing and binding input, subject to freshness and
   consistency validation.

## 6. Data model

### 6.1 `cluster_usage_snapshots`

Purpose: immutable backend evidence used for settlement.

Recommended columns:

- `id`
- `cluster_ref`
- `snapshot_key` unique
- `usage_sequence`
- `measured_at`
- `pricing_mode`
- `usage_confirmed`
- `db_units` `DECIMAL(12,2)`
- `app_units` `DECIMAL(12,2)`
- `db_amount` `DECIMAL(12,2)`
- `app_amount` `DECIMAL(12,2)`
- `currency` `CHAR(3)`
- `infra_amount` `DECIMAL(12,2)`
- `license_amount` `DECIMAL(12,2)`
- `sysops_amount` `DECIMAL(12,2)`
- `dbops_amount` `DECIMAL(12,2)`
- `total_amount` `DECIMAL(12,2)`
- `sponsorship_cycle_ref`
- `billing_owner_ref`
- `sponsorship_last_event_id`
- `pricing_inputs_json` `JSON` or `TEXT`
- `backend_metadata_json` `JSON` or `TEXT`
- `received_at`

### 6.2 `cluster_settlement_state`

Purpose: one current settled baseline per active cluster binding as last accepted by CRM.

Recommended columns:

- `cluster_ref`
- `cycle_start_at`
- `last_settled_snapshot_id`
- `last_usage_sequence`
- `last_sponsorship_cycle_ref`
- `last_billing_owner_ref`
- `last_sponsorship_last_event_id`
- `last_billed_db_units` `DECIMAL(12,2)`
- `last_billed_app_units` `DECIMAL(12,2)`
- `last_billed_db_amount` `DECIMAL(12,2)`
- `last_billed_app_amount` `DECIMAL(12,2)`
- `currency` `CHAR(3)`
- `pending_lower_app_snapshot_id` nullable
- `pending_lower_app_seen_at` nullable
- `updated_at`

Phase 1 uses the pending-lower-app fields only as a future hook. They do not create refunds yet.

### 6.3 `balance_accounts`

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

### 6.4 `balance_ledger_entries`

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

## 7. Settlement rules

### 7.1 Baseline comparison

CRM validates each new snapshot against `cluster_settlement_state` before settlement.

Validation must reject snapshots that are stale or binding-inconsistent, including:

- `usage_sequence` older than `last_usage_sequence`
- `sponsorship_last_event_id` older than `last_sponsorship_last_event_id`
- unexpected `billing_owner_ref` change within the same active `sponsorship_cycle_ref`
- unknown or unmapped `billing_owner_ref`

Only after passing validation does CRM compare the snapshot against `cluster_settlement_state`.

The comparison is done separately for DB and App.

### 7.2 Database behavior

If the incoming snapshot shows a DB increase versus the settled baseline:

- compute immediate prorated debit
- write one EUR ledger entry
- update DB baseline in the same transaction

If DB decreases:

- no runtime auto-refund
- no monetary transaction

### 7.3 Application behavior

If the incoming snapshot shows an App increase versus the settled baseline:

- compute immediate prorated debit
- write one EUR ledger entry
- update App baseline in the same transaction

If App decreases:

- do not create a refund in Phase 1
- store the lower-app marker fields in `cluster_settlement_state`
- leave the settled App baseline unchanged

This keeps the implementation refund-ready without turning on refund automation prematurely.

## 8. Idempotency and anti-duplication

Phase 1 must guarantee no duplicate charges and lay the groundwork for no duplicate refunds later.

### 8.1 Snapshot idempotency

- `cluster_usage_snapshots.snapshot_key` is unique
- retries of the same backend snapshot must not create a second settlement movement

### 8.2 Snapshot ordering and binding freshness

- `usage_sequence` is the ordering source for one cluster settlement stream
- `sponsorship_last_event_id` is the ordering source for sponsorship binding freshness
- stale snapshots must be rejected before baseline mutation

### 8.3 Ledger idempotency

- each debit ledger entry uses a deterministic unique `idempotency_key`
- duplicate insert is treated as replay, not a second charge

### 8.4 Transaction boundary

For any immediate debit, CRM must perform the following in one DB transaction:

1. lock settlement state
2. resolve and lock the EUR balance account
3. insert ledger row
4. update account balance
5. update `cluster_settlement_state`
6. commit

This is the mandatory foundation for later refund safety as well.

## 9. Peer compatibility

After successful snapshot processing, CRM mirrors the latest effective component amounts into
`clusters_config` using the existing peer-compatible keys:

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

In Phase 1, CRM internally repoints these routes to the active settlement source while preserving
the route behavior and response-shape compatibility relied on by current consumers.

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

Those fields remain the compatibility surface. If EUR settlement data is surfaced through this route
in Phase 1, it must be additive and must not remove, rename, or replace the currently used fields.

Phase 1 may add fields such as:

- `amount`
- `currency`

The route may be internally backed by the new EUR settlement source, but the compatibility promise
is about the public contract, not about preserving legacy credit-storage semantics behind the same
field names. After the Phase 1 repoint, the preserved public fields may be sourced from the active
EUR settlement source rather than legacy credit storage.

CRM must therefore define a Phase 1 user-to-settlement-account resolution rule for these routes that
preserves their user-scoped behavior while reading from the active settlement source.

Best Phase 1 rule:

- `GET /api/credits/personal` and `GET /api/users/transactions` resolve as user-scoped
  compatibility views for the authenticated CRM user,
- that user-scoped resolution remains separate from the owner-bound internal settlement ingest path
  keyed by `billing_owner_ref`.

For `GET /api/users/transactions`, Phase 1 should preserve the current envelope and row-field
compatibility used by consumers, including:

- top-level `transactions`
- top-level `pagination`
- row fields such as `id`, `created_at`, `entry_type`, `amount`, `balance_after`,
  `idempotency_key`, `reference_type`, `reference_id`, and `metadata`

If EUR-specific settlement data needs to be exposed through the transaction rows, it must be added
without removing or renaming the currently consumed fields.

### 10.1 Bootstrap/status contract freeze

Before Phase 1 rollout, CRM must reconcile the live handler behavior with `crm-api/docs/CONSUMER.md`
and the current end-to-end consumer expectation, then freeze one source-of-truth contract.

Because backward compatibility for this plan is defined by `crm-api/docs/CONSUMER.md`, any mismatch
must be resolved before rollout by either:

- aligning the live handlers to `crm-api/docs/CONSUMER.md`, or
- explicitly updating `crm-api/docs/CONSUMER.md` first and treating the updated document as the frozen
  contract.

During Phase 1, that route-level bootstrap/status contract must not change as part of the
settlement-source migration.

This plan does not assume that compatibility depends on preserving current table names such as
`credit_balances` or `credit_ledger_entries`.

## 11. Brownfield rollout

Existing active sponsored clusters must not be back-billed accidentally.

### 11.1 Brownfield rule

For already-active clusters at rollout:

- initialize `cluster_settlement_state` from the first received snapshot
- do not create a retroactive debit
- do not create a retroactive refund

### 11.2 New clusters after rollout

For clusters created after cutover:

- the first post-creation snapshot is billable
- CRM may create the initial prorated debit

## 12. Phases

### Phase 1: Settlement foundation

**Goal:** build the correct final architecture without refund automation.

Deliverables:

1. internal settlement ingest route
2. `cluster_usage_snapshots`
3. `cluster_settlement_state`
4. `balance_accounts`
5. `balance_ledger_entries`
6. immediate debit logic for DB/App increases
7. lower-app marker storage only
8. internal repointing of the existing public billing read routes to the active settlement source,
   while preserving their current external contract
9. peer-compatible cost mirroring
10. brownfield baseline initialization path
11. tests and docs

**Exit criteria:**

- duplicate snapshot replay produces no second debit
- DB increase creates one debit only
- App increase creates one debit only
- DB decrease creates no refund
- App decrease creates no refund
- latest component values are mirrored to peer-compatible fields
- `GET /api/credits/personal` remains backward-compatible and additive only
- `GET /api/users/transactions` remains backward-compatible and additive only

### Phase 2: Refund enablement

**Gate:** backend must provide a proven periodic confirmed usage feed suitable for sustained lower-app
usage detection.

Deliverables:

1. dedicated app refund candidate table
2. hourly refund finalizer script
3. configurable cooldown, default `24h`
4. exactly-once refund finalization using candidate state + ledger idempotency + baseline update in
   one transaction

**Exit criteria:**

- app temporary decrease under cooldown creates no refund
- sustained lower confirmed App usage creates exactly one refund
- repeated finalizer runs create no duplicate refund

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
4. unknown `cluster_ref` or missing active billing binding -> clear failure
5. duplicate `snapshot_key` -> idempotent replay with no second debit
6. stale `usage_sequence` -> rejected
7. stale `sponsorship_last_event_id` -> rejected
8. missing `billing_owner_ref` for active billable settlement -> rejected
9. unknown `billing_owner_ref` -> rejected
10. DB increase -> exactly one immediate debit
11. App increase -> exactly one immediate debit
12. DB decrease -> no transaction
13. App decrease -> no refund, lower-app marker only
14. peer-compatible fields updated correctly
15. `GET /api/credits/personal` keeps its current route path, auth, bootstrap/status behavior, and
    currently used fields; any EUR-oriented fields are additive only
16. `GET /api/users/transactions` keeps its current route path, auth, bootstrap/status behavior,
    envelope compatibility, and currently used row fields; any EUR-oriented fields are additive only
17. ledger metadata contains full pricing, usage, and sponsorship-binding snapshot
18. the live public billing-route behavior is reconciled with `crm-api/docs/CONSUMER.md` before
    rollout and
    remains stable after the Phase 1 repoint

Per repo testing policy, any broad or expensive suite should be logged to a file.

## 14. Documentation updates

Phase 1 must update:

1. `README.md`
2. `crm-api/docs/openapi.yaml`
3. `crm-api/docs/CONSUMER_CLOUD18_CREDIT_MODEL.md`

The key clarification to add everywhere is:

- backend owns pricing snapshots
- CRM owns EUR settlement
- backward compatibility is defined by the `crm-api/docs/CONSUMER.md` route contracts, not by current CRM
  storage internals
- existing consumed billing-route fields remain contract-stable in Phase 1; those routes may be
  internally re-backed by the new settlement source, and any EUR settlement fields exposed there
  are additive only
- legacy credit-storage semantics are not the compatibility contract for those public routes
- refunds are a later gated phase, not part of the foundation milestone

## 15. Final recommendation

Implement **Phase 1 only** now.

This is the full correct plan without scope creep because it builds:

- the final ownership boundary
- the correct accounting unit
- immutable historical evidence
- idempotent settlement
- peer compatibility
- a clean path to future app refunds

while deliberately excluding the one risky area that is still gated by backend measurement maturity:

- automatic refund execution
