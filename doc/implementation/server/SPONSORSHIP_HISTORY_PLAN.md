# Cloud18 sponsorship and settlement state plan

> **Status note.** This document is now historical design context. The active implementation plan for
> the current Phase 1 sponsorship-state work is
> `doc/implementation/server/CRM_SPONSORSHIP_USAGE_PREPARATION_PLAN.md`.
>
> In particular, the newer plan supersedes this document on:
>
> - the Phase 1 non-breaking handler behavior,
> - `occurred_at + event_key` as the current CRM freshness/idempotency model,
> - app/proxy pricing separation,
> - the current staged scope that implements local authority first while deferring stable-ref minting
>   and live CRM runtime sending.

## Objective

Add sponsorship tracking that follows the final product boundary:

- **replication-manager is the authority** for the current sponsor-to-cluster binding,
- CRM and git are **downstream consumers only** for billing/tracking,
- sponsorship history stays **local**,
- CRM billing events are derived from authoritative local state, not the source of truth.

This plan does **not** require CRM to fetch BO data or sponsorship history, and it
does **not** send sponsorship history to CRM.

## Scope

Track the Cloud18 sponsorship lifecycle around these existing flows:

- `requested` — `server/api.go` → `handlerMuxClusterSubscribe`
- `accepted` — `server/api_cluster.go` → `handlerMuxAcceptSubscription`
- `rejected` — `server/api_cluster.go` → `handlerMuxRejectSubscription`
- `ended` — `server/api_cluster.go` → `handlerMuxRemoveSponsor`

Also prepare the authoritative local state needed for future CRM settlement events.

## Final authority model

### Replication-manager is authoritative for

- the **current sponsor-to-cluster binding**,
- the current sponsorship status,
- the current sponsorship lifecycle correlation key,
- the current sponsorship event watermark and ordering,
- local sponsorship audit history,
- local settlement sequencing state.

### Replication-manager is **not** authoritative for

- the global billing-owner identity namespace.

That namespace is represented by `billing_owner_ref`, which is externally assigned
and stored locally by repman.

### Authority vs. enforcement

The current code path realizes sponsorship through a mix of:

- API user role / ACL changes,
- sponsor and DBA credential changes,
- optional mail and script side effects.

In the target design, those mutations must be treated as **derived enforcement or
reconciliation work**, not as independent sources of truth.

That means:

- `sponsorship-state.json` is the only authoritative statement of the current
  sponsorship binding,
- ACL / user / credential state should be applied from that authoritative state,
- failure in derived enforcement after the authoritative commit is a degraded
  reconcile condition, not a loss of authority.

## Identity split

The design must keep two distinct identities:

### 1. `billing_owner_ref`

- externally assigned,
- stable across time and clusters,
- opaque,
- immutable,
- non-secret,
- stored by repman,
- used in CRM settlement events.

Repman must **not** mint `billing_owner_ref`.

### 2. `sponsor_ref`

- local,
- audit-oriented,
- used only for local history and troubleshooting,
- never used by CRM as the billable owner identity.

## Authoritative durability rule

The current sponsor-to-cluster binding must survive restart **once repman returns
success** for a sponsorship lifecycle action.

That requires one concrete rule:

> A sponsorship state change is not successful until authoritative local
> sponsorship/settlement state has been durably persisted.

More precisely:

> The durable write of `sponsorship-state.json` is the **only commit point** for a
> sponsorship transition.

The implementation must therefore compute a **proposed next authoritative state** in
memory, durably write that next state, and only then treat the new binding as
committed.

`clusterstate.json` alone is **not** the authoritative durability anchor because the
existing cluster save path is asynchronous/gated and could lose the current binding
after a crash.

## Authoritative local state file

Use a **dedicated per-cluster authoritative state file** written atomically on the
success path.

### File

- `filepath.Join(cluster.WorkingDir, "sponsorship-state.json")`

### Write requirement

- write on the success path,
- write the full contents to a temporary file,
- fsync the temporary file,
- atomically rename the temporary file to `sponsorship-state.json`,
- if platform policy requires it, fsync the parent directory,
- do not return success before this sequence completes.

This file is the authoritative persisted source for current sponsorship and
settlement state.

It is also the source from which derived cluster enforcement should be reconciled:

- sponsor / pending / unsubscribed user-role state,
- cluster ACL changes,
- sponsor credential presence / removal,
- other cluster-local sponsorship side effects that need to match the current
  authoritative binding.

### Proposed `sponsorship` block

Store:

- `cluster_ref`
- `status` — one of `none`, `pending`, `active`, `ended`
- `billing_owner_ref`
- `sponsorship_cycle_ref`
- `sponsorship_event_sequence`
- `sponsorship_last_event_id`
- `last_event_type`
- `last_event_at`

### Proposed `settlement` block

Store:

- `settlement_event_sequence`
- `last_billing_event_key`
- `last_billing_event_type`
- `last_billing_event_at`
- `pricing_mode`

### Why this belongs in a dedicated file

- restart-safe,
- independent from broader async config persistence,
- enough to build settlement events without replaying history,
- keeps CRM from becoming the authority,
- contains only safe opaque refs and timestamps.

## `clusterstate.json` becomes a mirrored safe summary

`clusterstate.json` should still expose the **current safe sponsorship and settlement
summary** for BO/config/runtime visibility, but it is a **mirror**, not the only
durability anchor.

Use the conservative visibility default:

- keep `billing_owner_ref` authoritative only in `sponsorship-state.json`,
- do **not** mirror `billing_owner_ref` into `clusterstate.json` unless broader
  replication visibility is explicitly approved later.

### Mirrored fields

Mirror the current safe fields from `sponsorship-state.json`, for example:

- `cluster_ref`
- `status`
- `sponsorship_cycle_ref`
- `sponsorship_event_sequence`
- `sponsorship_last_event_id`
- `last_event_type`
- `last_event_at`
- `settlement_event_sequence`
- `last_billing_event_key`
- `last_billing_event_type`
- `last_billing_event_at`
- `pricing_mode`

### Explicit exclusions from `clusterstate.json`

Do **not** store:

- sponsor email,
- masked sponsor display,
- `billing_owner_ref`,
- `sponsor_ref`,
- credentials,
- tokens,
- secrets,
- embedded sponsorship history arrays,
- pricing formula internals.

## Local sponsorship audit history

Keep sponsorship history as a **per-cluster local append-only JSONL file**.

### File

- `filepath.Join(cluster.WorkingDir, "sponsorship-history.jsonl")`

This follows the existing cluster-local persistence pattern used by files such as
`interventions.json`.

### Purpose

Use this file for:

- audit,
- troubleshooting,
- internal traceability.

Do **not** use it as the authority for current sponsorship state, and do **not**
send it to CRM.

### Event types

- `requested`
- `accepted`
- `rejected`
- `ended`

### Suggested event schema

Each JSONL line should include only safe, explicit fields:

- `event_id`
- `timestamp_utc`
- `cluster_name`
- `event_type`
- `operator_user`
- `sponsor_ref`
- `billing_owner_ref`
- `sponsorship_cycle_ref`
- `sponsorship_event_sequence`
- `reason`
- `source`

### Sensitive-data rules

Never store any of the following in history:

- full sponsor email when it is personally identifying,
- sponsor DB username/password,
- DBA credentials,
- GitLab/JWT/OAuth tokens,
- raw secret values,
- full serialized request, user, or config structs.

## Ref ownership and lifecycle rules

### `billing_owner_ref`

- externally assigned,
- cached and persisted by repman,
- best received at **acceptance time**,
- required before entering active billable sponsorship.

### `sponsorship_cycle_ref`

- generated by repman,
- created at **request time**,
- reused for the full sponsorship episode:
  - `requested`
  - `accepted`
  - `rejected`
  - `ended`
- rotated only when a new sponsorship episode begins.

If a sponsorship is accepted without a prior request, generate the cycle ref at
acceptance time and treat that acceptance as the start of a new episode.

### `sponsorship_event_sequence`

- generated and persisted by repman,
- monotonically increasing per cluster,
- incremented on every sponsorship lifecycle event,
- the ordering source for stale-event rejection.

### `sponsorship_last_event_id`

- generated by repman from the monotonic event sequence,
- must be comparable in order,
- used as the current sponsorship event watermark in settlement events.

Best practical format:

- `evt_<zero-padded sequence>`

for example:

- `evt_000000000042`

### `cluster_ref`

- canonical stable cluster identity used in settlement events,
- locally persisted by repman unless product already provides one.

## Best practical workflow rule

- `requested` sponsorship may exist without `billing_owner_ref`.
- `accepted` sponsorship must have a valid external `billing_owner_ref`.
- `accepted` is the moment the cluster becomes eligible for `sponsorship_started` billing emission.
- If `billing_owner_ref` is missing, repman must **not** transition the cluster
   into active billable sponsorship.

This preserves local workflow flexibility while keeping billable activation strict.

App lifecycle billing is separate from the sponsorship lifecycle itself:

- sponsorship state activates or ends the base cluster commercial episode,
- app provision and unprovision events adjust app billing state inside that episode,
- repman must not infer those app billing events solely from first-seen snapshots.

## Settlement event design boundary

Future CRM settlement events must be built **only** from the **current
authoritative local state** plus the current local commercial state. Passive usage snapshots may be
attached as evidence, but they are not billing authority by themselves.

That means they must be built from:

- authoritative local sponsorship state,
- authoritative local settlement state,
- the current local price resolution for the event being emitted,
- optional current local usage evidence.

They must **not** be built from:

- sponsorship history replay,
- CRM lookups,
- BO lookups.

### Minimal CRM event target

The consumer should eventually send only the minimal event contract CRM requested,
including fields such as:

- `event_type`
- `event_key`
- `event_sequence`
- `cluster_ref`
- `occurred_at`
- `pricing_mode`
- `currency`
- `sponsorship_cycle_ref`
- `billing_owner_ref`
- `sponsorship_last_event_id`

For sponsorship-driven billing events, CRM may also require fields such as:

- `db_units`
- `infra_amount`
- `license_amount`
- `sysops_amount`
- `dbops_amount`
- `base_amount`

For app lifecycle billing events, CRM may also require fields such as:

- `app_ref`
- `app_units`
- `app_amount`

### Explicit exclusions from CRM events

Do **not** send:

- sponsorship history,
- sponsor email,
- masked sponsor display,
- `sponsor_ref`,
- status booleans not explicitly required,
- producer metadata,
- pricing input internals,
- credentials, tokens, or secrets.

Passive usage evidence may be sent separately or attached optionally, but must not replace the
explicit billing event contract.

## Expected write flow

For each successful sponsorship lifecycle action:

1. load the current authoritative sponsorship state,
2. validate transition/idempotency,
3. build the **next** authoritative sponsorship state in memory,
4. increment `sponsorship_event_sequence`,
5. derive `sponsorship_last_event_id`,
6. durably write `sponsorship-state.json`,
7. mark the new binding committed,
8. append one local JSONL history event,
9. refresh the `clusterstate.json` mirror,
10. continue with derived enforcement / reconciliation work (ACL, user, credential
    application),
11. continue with optional side effects (mail/scripts, later CRM event send, later git tracking).

The authoritative file write in step 6 is part of the success path and is the sole
commit boundary for the sponsorship binding.

## Per-cluster sponsorship lock

All sponsorship lifecycle updates for one cluster must run under a **single
per-cluster sponsorship lock**.

The lock must cover the full authoritative critical section:

1. load authoritative state,
2. validate transition,
3. build/mutate the proposed next in-memory state,
4. increment `sponsorship_event_sequence`,
5. derive `sponsorship_last_event_id`,
6. write `sponsorship-state.json`,
7. append `sponsorship-history.jsonl`,
8. refresh the `clusterstate.json` mirror.

The lock should **not** cover optional side effects such as:

- email,
- scripts,
- later CRM event send.

Whether ACL / user / credential reconciliation remains inside the lock or is retried
immediately after commit is an implementation choice, but the authoritative binding
must already be committed in `sponsorship-state.json` before those derived actions are
treated as required to converge.

## Commit semantics

Some sponsorship flows already commit business state before optional side effects run.
The persistence design must follow that reality:

- if the durable write of `sponsorship-state.json` fails, the handler must return
  failure and the new sponsorship binding must be treated as **not committed**,
- that failure path must be retry-safe because the authoritative state did not
  advance,
- do **not** return success before `sponsorship-state.json` has completed the
  temporary-file → fsync file → atomic rename → optional parent-dir fsync sequence,
- once `sponsorship-state.json` has been durably written, the sponsorship binding is
  committed and must **not** be flipped back to failure because a later secondary
  action failed,
- derived ACL / user / credential application after authoritative commit must be
  treated as reconcile work; failures there are degraded reconcile conditions rather
  than authority loss,
- treat history/mirror/export failures after authoritative-state persistence as
  degraded-audit or degraded-tracking conditions that must be logged loudly and
  surfaced operationally.

## Likely implementation touchpoints

### Server handlers

- `server/api.go`
  - `handlerMuxClusterSubscribe`
- `server/api_cluster.go`
  - `handlerMuxAcceptSubscription`
  - `handlerMuxRejectSubscription`
  - `handlerMuxRemoveSponsor`
- `server/server_cloud.go`
  - current sponsorship role/ACL transition helpers that will need to align with
     the authoritative-state-first model

### Cluster persistence/state

- `cluster/cluster.go`
  - extend `ClusterState` for the mirrored safe fields
  - mirror sponsorship/settlement state during `SaveCallBack`
- `cluster/cluster_get.go`
  - restore authoritative sponsorship state from `sponsorship-state.json` before
    any runtime logic that can emit settlement events or process sponsorship
    transitions

### New helper area

Recommended helper split:

- `cluster/cluster_sponsorship_state.go`
  - authoritative `sponsorship-state.json`
- `cluster/cluster_sponsorship_history.go`
  - per-cluster local JSONL audit history
- derived reconciliation helpers for ACL / user / credential convergence from the
  authoritative state

Responsibilities:

- define authoritative state structs,
- define audit event structs,
- manage `billing_owner_ref` caching,
- provide one per-cluster sponsorship lock,
- generate `sponsorship_cycle_ref`,
- generate `sponsorship_event_sequence`,
- derive `sponsorship_last_event_id`,
- persist and advance `settlement_event_sequence` for CRM event emission,
- generate or persist `cluster_ref`,
- append JSONL rows safely,
- write `sponsorship-state.json` atomically,
- update mirrored `clusterstate.json` fields without exposing `billing_owner_ref`.
- drive or schedule derived ACL / user / credential reconciliation from the
  authoritative state.

## Failure policy

Because repman is the authority:

- authoritative state persistence must complete before success is returned,
- failed authoritative persistence must return failure because the binding did not
  commit,
- JSONL appends should be serialized with a mutex,
- failures to write local history should be treated as high-severity audit failures,
- failures to refresh the `clusterstate.json` mirror should be treated as degraded mirror/export readiness failures,
- failure logs must not include secret-bearing request/config payloads.

## Validation plan

### Unit tests

- `requested` creates a new `sponsorship_cycle_ref` when a new episode begins.
- `requested` appends one safe local JSONL event.
- `accepted` requires `billing_owner_ref`.
- `accepted` reuses the existing `sponsorship_cycle_ref` from the request episode.
- every lifecycle event increments `sponsorship_event_sequence`.
- `sponsorship_last_event_id` is strictly derived from the persisted event sequence.
- `settlement_event_sequence` survives restart and remains monotonic for later CRM event emission.
- `sponsorship-state.json` survives restart and preserves the authoritative binding.
- `sponsorship-state.json` is written before history and mirror refresh.
- failed `sponsorship-state.json` writes return failure and leave the authoritative
  binding unchanged.
- concurrent sponsorship actions for the same cluster cannot corrupt sequence,
  cycle ref, or current binding.
- authoritative sponsorship state is restored from `sponsorship-state.json` before
  runtime sponsorship transitions or settlement event generation.
- `clusterstate.json` mirrors the authoritative safe fields without `billing_owner_ref` or `sponsor_ref`.
- local history does not contain passwords, tokens, raw secrets, or full unmasked sponsor identity.

### Manual validation

- request sponsorship from UI/API,
- accept sponsorship with external `billing_owner_ref`,
- reject sponsorship,
- end sponsorship,
- crash/restart after successful sponsorship changes,
- confirm:
  - `sponsorship-state.json` preserves the current authoritative binding,
  - local JSONL history remains append-only and readable,
  - `clusterstate.json` reflects the mirrored safe state,
  - no sensitive fields appear in persisted artifacts,
  - CRM can be offline without loss of authoritative state.

## Deferred work

Out of scope for this phase:

- direct CRM settlement event submission,
- CSV generation,
- BO-side aggregation/reporting endpoint,
- backfill/migration of historical sponsorships from old logs,
- final base/app commercial amount semantics if they are not yet modeled.

## Final recommendation

Phase 1 should implement:

- a dedicated authoritative per-cluster `sponsorship-state.json`,
- a mirrored safe sponsorship/settlement summary in `clusterstate.json`,
- per-cluster local append-only `sponsorship-history.jsonl`,
- externally assigned `billing_owner_ref` stored by repman,
- repman-generated `sponsorship_cycle_ref` created at request time,
- repman-generated monotonic `sponsorship_event_sequence`,
- repman-derived ordered `sponsorship_last_event_id`,
- repman-persisted monotonic `settlement_event_sequence` for downstream CRM events,
- future CRM settlement events derived only from local authoritative state.

This is the cleanest model because:

- `billing_owner_ref` remains an external account identity,
- `billing_owner_ref` remains outside mirrored clusterstate visibility by default,
- repman remains authoritative only for the **current sponsor-to-cluster binding**,
- local audit identity (`sponsor_ref`) stays local,
- CRM receives only the minimal current billing-event context it actually needs,
- and the current authoritative binding survives restart once repman has returned success.
