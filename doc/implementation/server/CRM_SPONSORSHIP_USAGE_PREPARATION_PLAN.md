# CRM Sponsorship and Usage Preparation Plan

**Status:** implementation plan

> **Implementation-status note.** The currently implemented/staged Phase 1 slice covers:
> authoritative local sponsorship state, startup restore, safe `clusterstate.json` mirroring, and
> handler-side lifecycle persistence. It intentionally does **not** yet implement stable persisted
> `cluster_ref` / `app_ref` minting or any live `/api/instances/*` CRM runtime sending.

## Purpose

Define the best-first replication-manager implementation path for future Cloud18 CRM sponsorship and
usage settlement integration.

Hard boundary for this plan:

- **replication-manager calculates price**,
- **CRM performs accounting only**.

This plan intentionally chooses:

1. **authoritative local sponsorship state first**,
2. **stable ref target locked now**,
3. **feature-gated CRM settlement client scaffolding now**,
4. **no live `/api/instances/*` runtime sending yet**,
5. **no CRM price calculation now or later**.

## Scope

Prepare replication-manager for the future CRM flows around:

- sponsorship requested
- sponsorship accepted
- sponsorship rejected
- sponsorship ended
- app provisioned
- app unprovisioned

This plan does **not** change the current instance subscription behavior and does **not** enable live
runtime settlement traffic yet.

## Source documents

Primary upstream references live in `/home/ahmad/crm-api/docs`:

- `CONSUMER_FUTURE_SPONSORSHIP_PLAN.md`
- `CLOUD18_SPONSORSHIP_APPROVAL_BILLING_EXECUTION_PLAN.md`
- `CLOUD18_SETTLEMENT_INSTANCE_AUTH_PLAN.md`
- `openapi.yaml`

Related local implementation context:

- `doc/implementation/server/SPONSORSHIP_HISTORY_PLAN.md`
- `doc/implementation/config/CLOUD18_CREDIT_MODEL.md`
- `doc/implementation/config/CLOUD18_APP_HA_DISCOUNT_PLAN.md`
- `doc/implementation/crm/CRM_SETTLEMENT.md`
- `doc/implementation/crm/CRM_SETTLEMENT_AUTH.md`

## Chosen option set

### 1. Local authority before CRM sending

Replication-manager becomes authoritative for the current sponsor-to-cluster binding and the local
settlement event history needed to drive CRM later.

Reason:

- it matches the CRM direction,
- it is restart-safe,
- it avoids partial cutovers where CRM behavior changes before repman has durable state.

### 2. Stable ref target locked now

Do **not** use raw names as the long-term billing identity.

Long-run target:

- `cluster_ref`
- `app_ref`

Reason:

- CRM docs already warn that `cluster_ref = cluster_name` is only acceptable under a rename-stable
  assumption,
- current app identity is also name-derived,
- a stable ref target avoids a later billing identity migration.

Phase 1 implementation note:

- the current local authority slice still records `cluster_ref = cluster.Name`,
- stable ref minting/persistence is deferred to a later phase,
- live CRM runtime sending remains disabled until the wire contract and stable-ref implementation are
  both ready.

### 3. CRM client scaffolding now, but disabled

Add payload builders and transport helpers for future:

- settlement credential issue / rotate / revoke
- sender-state update
- sponsorship billing-owner resolve
- sponsorship workflow events
- settlement events

Keep all outbound runtime CRM calls behind an explicit disabled gate in this phase.

### 4. No live runtime sending in this pass

Do not enable:

- `POST /api/instances/sponsorship-billing-owner/resolve`
- `POST /api/instances/sponsorship-workflow-events`
- `POST /api/instances/settlement-events`
- `POST /api/instances/settlement-sender-state`

until local authority, stable refs, and event generation are validated.

## Authority boundary

### Replication-manager owns pricing

Replication-manager is the sole pricing engine. It owns:

- `pricing_mode`
- `db_units`
- `app_units`
- `proxy_units`
- `infra_amount`
- `license_amount`
- `sysops_amount`
- `dbops_amount`
- `base_amount`
- `app_amount`
- `proxy_amount`
- App HA structural pricing
- any future promotion or discount logic
- the final resolved commercial snapshot sent to CRM

This aligns with `doc/implementation/config/CLOUD18_CREDIT_MODEL.md` and
`doc/implementation/config/CLOUD18_APP_HA_DISCOUNT_PLAN.md`: technical unit accounting stays in
repman, commercial pricing is layered in repman, and CRM receives the resolved EUR amounts only.

Component boundary note:

- apps and proxies may share the same Application/Compute unit price basis,
- but they are still separate priced objects in repman,
- App HA structural pricing applies to apps only,
- proxies remain outside the App HA structural pricing rule.

### CRM owns accounting only

CRM owns:

- auth and sender/binding validation
- immutable event recording
- balance-account resolution
- billing cycles
- ledger entries / debits / credits
- idempotency and replay safety

### CRM must not calculate price

CRM must **not**:

- derive price from `db_units` or `app_units`
- derive App HA price from topology
- infer proxy/app/base price from cluster shape
- recompute historical prices from current pricing rules
- become a second pricing authority from stored events

## Current hook points in replication-manager

### Sponsorship lifecycle

- request: `server/api.go` → `handlerMuxClusterSubscribe`
- accept: `server/api_cluster.go` → `handlerMuxAcceptSubscription`
- reject: `server/api_cluster.go` → `handlerMuxRejectSubscription`
- end: `server/api_cluster.go` → `handlerMuxRemoveSponsor`

### App lifecycle

- provision success: `server/api_app.go` → `handlerMuxAppProvision` → `cluster.InitAppService()`
- unprovision success: `server/api_app.go` → `handlerMuxAppUnprovision` →
  `cluster.OpenSVCUnprovisionAppService()` + `cluster.ClearAppProvisionedCredits()`

These are the only paths that should mutate authoritative sponsorship/usage state in Phase 1.

## Phase plan

### Phase 1 — authoritative local sponsorship state

Add a dedicated per-cluster state helper area, preferably under `cluster/`:

- `cluster/cluster_sponsorship_state.go`
- optional `cluster/cluster_sponsorship_history.go`

Authoritative persisted file:

- `sponsorship-state.json`

Authoritative fields should include at minimum:

- `cluster_ref`
- sponsorship `status`
- sponsor audit identity snapshot
- `billing_owner_ref` (optional cache, if known)
- `sponsorship_cycle_ref` (optional cache, if known)
- last workflow event metadata
- last billing event metadata
- pricing mode
- deterministic event inputs needed to build `event_key` and `occurred_at`
- optional legacy/inert metadata only if useful for debugging or future migrations

Freshness model note:

- current CRM contract uses **`occurred_at + event_key`** for ordering/idempotency
- `event_sequence` and `sponsorship_last_event_id` are optional inert metadata, not core producer
  responsibilities

Write requirements:

- atomic write through temp file + rename
- no success returned before authoritative write completes
- restart restore must read this file before any sponsorship/runtime reconciliation

### Phase 2 — mirrored safe summary

Extend `cluster.SaveCallBack()` / `clusterstate.json` mirroring with safe sponsorship summary only.

Mirror allowed examples:

- `cluster_ref`
- sponsorship status
- `sponsorship_cycle_ref`
- last event type / time
- last billing event key / type / time
- pricing mode

Do **not** mirror:

- `billing_owner_ref`
- settlement secrets
- sponsor private identity details
- full audit history

### Phase 3 — stable ref persistence

Target later-phase work:

- `GetClusterRef()`
- `GetAppRef(app)`

Rules:

- ref is stamped once and persisted,
- ref does not change automatically on rename,
- raw names may be used only as the initial seed when first generating the persisted ref.

Contract note:

- upstream CRM docs currently still treat `cluster_ref = cluster_name` **for now** on some routes,
  especially sponsorship resolve,
- replication-manager should introduce `GetClusterRef()` / `GetAppRef()` before live CRM runtime
  sending so the final on-wire mapping can change later without reshaping local authority or pricing
  logic,
- the target long-run design is a stable persisted `cluster_ref` on the wire as well as locally,
- current name-shaped CRM route fields should therefore be treated as a transitional constraint, not
  as the desired identity model,
- live runtime sending remains disabled until this contract is finalized.

### Phase 4 — transition existing workflows onto authoritative state

Update existing handlers so the authoritative state transition happens first:

- subscribe → requested
- accept → active
- reject → rejected/cleared pending
- end → ended

Current ACL, role, credential, mail, and script work becomes derived enforcement or reconciliation.

Failure rule:

- failure before authoritative write = request fails,
- failure after authoritative write = transition remains committed and side-effect failure is logged /
  surfaced as degraded follow-up work.

Current Phase 1 handler behavior:

- **subscribe** remains behavior-preserving after commit: if the post-commit main subject
  `AddUser`/`UpdateUser` sync fails, the request is not retroactively failed; the handler logs a
  degraded reconciliation error instead,
- **accept / reject / end** surface failures in their core post-commit main-subject mutation path,
  because those flows already operate with stricter admin/operator semantics,
- ancillary external sysops/dbops sync in accept is logged as degraded reconciliation and does not
  fail the already-committed sponsorship transition.

### Phase 5 — capture app billing lifecycle locally

On successful app provision/unprovision, generate authoritative local usage transitions using stable
`app_ref` and persisted cluster sponsorship state.

Pricing-model rule for this phase:

- app pricing is app-specific,
- proxy pricing is proxy-specific,
- app and proxy may share the same Application/Compute unit price basis,
- proxy pricing must not be treated as app pricing.

Candidate local billing event types:

- `app_provisioned`
- `app_unprovisioned`

Payload inputs already available in repman should be reused where possible:

- `registeredInstanceURI()`
- `ComputeDatabaseUnits()`
- app-specific unit accounting helpers
- proxy-specific unit accounting helpers
- marketplace pricing config

Authoritative app settlement sources for later pricing/event work:

- app topology source = `prov-app-ha-topology`
- app node count source = configured deployment nodes / `GetAppAgents()`
- app per-node resource shape source = stored app provisioning resources
- app commercial pricing source = backend unit pricing + App HA structural pricing rules

Important accounting rule:

- do **not** use raw cluster-wide `ComputeApplicationUnits()` directly as the source for one app's
  settlement `app_units`, because the technical cluster total includes both apps and live proxies.

Legacy app-credit rule:

- `ProvAppCreditUsed` / `ProvAppCreditPlanned` may be kept as audit/debug/supporting metadata,
- they must **not** become the primary authority for future `app_units` or `app_amount` pricing.

### Phase 6 — settlement credential and sender-state plumbing

Add local storage/plumbing for future CRM machine-auth state.

These are **per-instance/server** concerns, not part of the per-cluster authoritative
`sponsorship-state.json`:

- `instance_registration_ref`
- `settlement_key_id`
- `settlement_secret`
- sender mode

Storage rules:

- store them outside the cluster sponsorship authority file
- treat `settlement_secret` as secret material
- never mirror `settlement_secret` into `clusterstate.json`
- never log `settlement_secret`
- keep outbound runtime usage disabled in this phase

### Phase 7 — disabled CRM settlement client

Add internal builders/helpers for future CRM calls, but keep send paths disabled by default.

Suggested helper area:

- `server/api_crm_settlement.go`

Expected helpers:

- credential issue / rotate / revoke
- sender-state update
- sponsorship billing-owner resolve
- sponsorship workflow event post
- settlement event post

## Future CRM route mapping

When live runtime sending is enabled later, the expected mapping is:

| Local lifecycle action | Future CRM route(s) | Billable? | Notes |
|---|---|---:|---|
| sponsorship requested | `POST /api/instances/sponsorship-workflow-events` | no | workflow event only |
| sponsorship accepted | `POST /api/instances/sponsorship-billing-owner/resolve` → `POST /api/instances/sponsorship-workflow-events` → `POST /api/instances/settlement-events` | yes | resolve first, then workflow `sponsorship_approved`, then settlement `sponsorship_started` |
| sponsorship rejected | `POST /api/instances/sponsorship-workflow-events` | no | workflow event only |
| sponsorship ended | `POST /api/instances/sponsorship-workflow-events` + `POST /api/instances/settlement-events` | yes | workflow `sponsorship_ended` plus settlement `sponsorship_ended` |
| app provisioned | `POST /api/instances/settlement-events` | yes | settlement `app_provisioned` |
| app unprovisioned | `POST /api/instances/settlement-events` | yes | settlement `app_unprovisioned` |

Current CRM contract gap:

- OpenAPI exposes explicit app lifecycle settlement, but no explicit proxy lifecycle settlement
  payloads or `proxy_amount` / `proxy_ref` fields,
- repman should still model proxy pricing separately internally,
- the best long-run cutover option is explicit CRM proxy settlement support rather than collapsing
  proxy pricing into app pricing.

Upstream contract ambiguity:

- current OpenAPI limits `sponsorship-workflow-events` to sponsorship events only,
- some planning docs discuss app tracking examples,
- implementation should assume **apps use settlement events only** unless CRM docs are updated.

## Pricing field mapping for future settlement payloads

The future disabled client should be designed around these source-of-truth mappings:

| Payload field | Owner | Expected source in repman |
|---|---|---|
| `pricing_mode` | repman | marketplace pricing mode |
| `db_units` | repman | `ComputeDatabaseUnits()` |
| `app_units` | repman | app-only unit accounting, not the cluster-wide combined application/proxy total |
| `proxy_units` | repman | proxy-only unit accounting |
| `base_amount` | repman | backend-resolved cluster commercial baseline |
| `app_amount` | repman | backend-resolved app commercial amount after App HA structural pricing |
| `proxy_amount` | repman | backend-resolved proxy commercial amount with no App HA structural pricing |
| `billing_owner_ref` | CRM-minted, cached by repman | resolve route response |
| `sponsorship_cycle_ref` | CRM-minted, cached by repman | resolve route response |

Proxy pricing rule for long-run alignment:

- proxies are **not** app lifecycle items,
- proxies are **not** app-priced objects even when they use the same unit price basis as apps,
- proxies receive **no** App HA structural pricing adjustment,
- repman should preserve proxy pricing as a separate internal component,
- the best long-run contract is explicit proxy settlement support rather than collapsing proxy and
  app pricing into one field,
- if a transitional transport fallback is ever required during a later cutover, it should be
  treated as compatibility-only and must not rewrite the internal pricing model.

## Decisions locked for Phase 1 vs later cutover

### Locked for Phase 1 implementation

- repman calculates all prices; CRM performs accounting only
- authoritative sponsorship state is local and restart-safe
- app pricing uses app-specific authoritative inputs
- legacy app-credit fields are not primary pricing authority
- proxy pricing stays separate from app pricing internally
- live `/api/instances/*` runtime sending stays disabled
- subscribe keeps existing request-flow semantics after the authoritative commit, with post-commit
  user/ACL sync failures logged as degraded reconciliation rather than turned into a new hard-fail
  path
- accept / reject / end surface failures in their core post-commit main-subject mutation path

### Deferred until live CRM cutover

- explicit proxy settlement transport shape in CRM
- final stable `cluster_ref` wire semantics once CRM routes accept the canonical model
- whether any temporary transport compatibility shim is needed at all

## Recommended package/file impact

### `cluster/`

- add authoritative sponsorship state structs and I/O
- add stable ref accessors
- add audit history append helpers
- add event builder helpers for workflow/billing event candidates
- add app-only vs proxy-only pricing/unit helpers so settlement payload generation does not reuse the
  combined cluster-wide application total blindly
- extend save/restore flow to mirror safe summary and restore authoritative state early

### `server/`

- wire sponsorship lifecycle handlers to authoritative state transitions
- add CRM settlement client scaffolding
- add future config/feature-gate plumbing for disabled outbound sending

### `config/`

- add only the minimal config needed for gated settlement behavior and optional future endpoint toggles

## Validation plan

### Unit tests

- authoritative `sponsorship-state.json` write/read/restore
- mirror safety for `clusterstate.json`
- stable `cluster_ref` / `app_ref` persistence
- request / accept / reject / end transition ordering
- app provision / unprovision local event generation
- disabled CRM settlement client makes no outbound calls

### Behavioral checks

- a sponsorship accept does not report success before authoritative state is persisted
- a post-commit side-effect failure does not erase the authoritative transition
- app unprovision clears provisioned usage and records a local end transition only after successful
  unprovision

## Known risks and follow-ups

### Multi-cluster vs CRM sender-state model

CRM sender-state is instance-registration based, while one repman process can manage multiple
clusters. This is the main reason live outbound settlement sending stays disabled in this phase.

### Existing rename sensitivity

Current name-derived identities are not safe long-term billing identities. Stable persisted refs are
therefore still part of the chosen best-first **target** option set, but their actual implementation
remains deferred beyond the current Phase 1 slice.

### Commit semantics differ from current handlers

Several current sponsorship flows perform ACL/mail/script work on the success path. This plan
redefines success around durable authoritative state, with side effects treated as reconciliation.

## Exit criteria for this plan

This preparation phase is complete when:

1. sponsorship authority is durably local and restart-safe,
2. safe sponsorship summary is mirrored into `clusterstate.json`,
3. stable persisted `cluster_ref` and `app_ref` exist,
4. sponsorship and app lifecycle events can be generated deterministically from local state,
5. CRM settlement client scaffolding exists but is disabled by default,
6. no current live CRM consumer behavior regresses.
