# Cloud18 registration versus optional DBaaS mode plan

## Objective

Define the `replication-manager` implementation boundary where:

- **instance registration is a base capability**, and
- **sponsorship, credits, and CRM billing/settlement are optional DBaaS features**.

This plan exists because some installations are in-house only and must be able to
register without taking on resale-platform behavior.

## Final product rule

The non-negotiable rule is:

> **Cloud18 instance registration must not automatically enable sponsorship,
> credit, or CRM settlement behavior.**

Registration and DBaaS billing are related, but they are not the same feature.

## Current model in this repository

Today, most Cloud18 controls are server-scoped configuration:

- `cloud18`
- `cloud18-domain`
- `cloud18-sub-domain`
- `cloud18-sub-domain-zone`
- `cloud18-gitlab-user`
- `cloud18-crm-api-url`
- `cloud18-subscription-plan`
- `cloud18-disable-for-sale`
- `cloud18-marketplace-pricing-mode`

This means the current repo already models Cloud18 primarily as an **instance-level**
capability, not as a mandatory per-cluster billing system.

The repository also already distinguishes one narrower marketplace behavior:

- `cloud18-disable-for-sale` hides marketplace sale visibility,
- but it does **not** mean the instance is unregistered,
- and it does **not** mean Cloud18 features are globally disabled.

That separation is the right direction and should be preserved.

## Best implementation choice

### 1. Keep registration independent

Instance registration remains the base platform path used for features such as:

- Cloud18 connectivity,
- peer/platform integration,
- support/subscription validation,
- future per-instance CRM machine identity.

Registration must remain usable even when the installation never participates in:

- sponsorship,
- credits,
- resale marketplace billing,
- CRM settlement-event production.

### 2. Add one explicit server-level DBaaS mode gate

The best next implementation option is a **single server-scope enablement flag** for
DBaaS/resale behavior.

Recommended naming:

- config / CLI / TOML key: `cloud18-dbaas-enabled`
- Go config field: `Cloud18DBaaSEnabled`

Why this is the best name:

- it stays inside the existing `cloud18-*` namespace,
- it is explicit that this is a boolean opt-in,
- it does not overload plain `cloud18`, which already means Cloud18 registration /
  connectivity,
- it is clearer than shorter names such as `cloud18-dbaas`, which can be read as a
  product identity rather than an enablement flag.

This gate should control whether the instance participates in:

- sponsorship subscribe / accept / reject / end flows,
- sponsorship-state persistence,
- billing-owner enforcement,
- local settlement-event sequencing,
- future CRM settlement-event production and delivery.

This gate should **not** control:

- instance registration,
- Cloud18 login/connectivity,
- subscription plan sync,
- peer health/platform features,
- general marketplace metadata configuration.

### 3. Do not introduce mandatory cluster-level billing gating now

Cluster-level DBaaS gating may be useful later if a real mixed-fleet requirement
emerges, but it is not the best default for the next implementation.

Reasons:

- current Cloud18 configuration is already mostly server-scoped,
- current sponsorship workflows are instance-oriented operational features,
- adding a second gate now increases complexity before the product requirement is
  proven.

The correct default is therefore:

- **server-level DBaaS gate now**,
- **cluster-level billing gate later only if needed**.

## Behavioral rules

### 1. In-house or support-only instance

If the instance is registered but DBaaS mode is disabled:

- registration works normally,
- Cloud18 peer/support/platform features remain available,
- sponsorship flows are disabled,
- no sponsorship state file is required,
- no billing-owner identity is required,
- no settlement sequence is maintained,
- no CRM billing events are produced.

### 2. DBaaS-enabled instance

If the instance is registered and DBaaS mode is enabled:

- sponsorship flows may be used,
- authoritative sponsorship state may be persisted,
- billable sponsorship acceptance requires `billing_owner_ref`,
- local settlement sequencing may be maintained,
- future CRM settlement events may be derived from local authoritative state.

### 3. Lazy state creation

Even when DBaaS mode is enabled, sponsorship/billing state should be created only
when those workflows are actually used.

That means:

- a DBaaS-capable instance may still host clusters that never enter sponsorship,
- such clusters should not be forced into billing state just because the instance is
  registered.

### 4. DBaaS disable and offboarding rule

DBaaS mode must not be treated as a harmless cosmetic toggle.

Current marketplace behavior depends on sponsorship-related roles:

- a cluster marked for sale is demoted out of the marketplace set when a user has
  `pending` or `sponsor` state,
- `reject` and `end` are therefore not optional convenience actions; they are the
  unwind path that clears active marketplace workflow state.

Best implementation rule:

- **DBaaS mode must not be disabled while any cluster still has active `pending` or
  `sponsor` state**.

That means:

1. operator must first drain/offboard that state through the normal sponsorship
   unwind flows,
2. only after no cluster remains in an active marketplace workflow state may DBaaS
   mode be disabled,
3. the disable operation must fail clearly and explain which clusters still need
   cleanup.

This is the safest default because it avoids stranding partially active sale or
delegation state.

## Explicit non-goals

This plan does **not** make the following mandatory for all Cloud18 instances:

- sponsorship-state persistence,
- credit management,
- CRM settlement-event posting,
- billing-owner binding,
- marketplace/resale lifecycle handling.

Those belong only to the DBaaS-enabled path.

## Current behavior that the new gate must normalize

Today, the sponsorship endpoints are not uniformly gated:

- `/api/clusters/{clusterName}/subscribe` is effectively gated by Cloud18
  registration and credentials,
- `accept`, `reject`, and `end` are operational ACL/sponsorship handlers but are not
  consistently protected by one single DBaaS-specific mode check.

The new DBaaS gate is intended to normalize this behavior by making the optional
DBaaS boundary explicit instead of leaving it implicit and uneven across handlers.

## Implementation touchpoints

### Registration and Cloud18 connectivity

Existing registration/connectivity flow stays independent:

- `server/api_register.go`
- related Cloud18 config in `config/config.go`

### Marketplace visibility

Keep `cloud18-disable-for-sale` scoped to marketplace visibility only:

- `server/api.go` → `/api/clusters/for-sale`

It must not be repurposed as the DBaaS mode switch.

### Cluster offer metadata remains separate

The DBaaS mode gate is **orthogonal** to per-cluster marketplace offer metadata.

Examples of offer/catalog fields that remain distinct from the mode gate include:

- `cloud18-shared`,
- cost fields,
- service-record metadata,
- plan/pricing presentation fields.

The DBaaS mode gate controls **workflow and local billing-state behavior**.
It does not replace existing per-cluster offer metadata.

### Flows explicitly outside the DBaaS gate

The following remain available independently from DBaaS mode:

- instance registration,
- Cloud18 connectivity/login state,
- CRM subscription-plan synchronization and persistence,
- peer/platform behavior,
- marketplace visibility toggling through `cloud18-disable-for-sale`.

### Sponsorship workflows

DBaaS gating must wrap the existing sponsorship workflow handlers:

- `server/api.go` → `handlerMuxClusterSubscribe`
- `server/api_cluster.go` → `handlerMuxAcceptSubscription`
- `server/api_cluster.go` → `handlerMuxRejectSubscription`
- `server/api_cluster.go` → `handlerMuxRemoveSponsor`
- supporting behavior in `server/server_cloud.go`

When DBaaS mode is disabled, these flows must be unavailable for new sponsorship
work, but the system must still enforce the offboarding rule above: disable is
blocked until existing `pending` and `sponsor` state has been drained.

### Optional authoritative sponsorship state

If DBaaS mode is enabled and sponsorship is used, repman should later add:

- `sponsorship-state.json` as authoritative local sponsorship/settlement state,
- `sponsorship-history.jsonl` as local audit history,
- additive mirrored safe fields in `clusterstate.json`.

These files are optional runtime artifacts of the DBaaS path, not mandatory for
every installation.

## Recommended phased implementation

### Phase 1: mode boundary

1. add one explicit server-scope DBaaS enablement flag
   (`cloud18-dbaas-enabled` / `Cloud18DBaaSEnabled`),
2. gate sponsorship endpoints and sponsorship workflow entry points behind it,
3. add disable-time validation that blocks DBaaS mode shutdown while active
   `pending` or `sponsor` state still exists,
4. keep registration and Cloud18 platform behavior unchanged,
5. keep `cloud18-disable-for-sale` as visibility-only.

### Phase 2: optional authoritative sponsorship state

Only for DBaaS-enabled instances:

1. add durable authoritative sponsorship state,
2. require `billing_owner_ref` for billable sponsorship acceptance,
3. maintain sponsorship and settlement event watermarks,
4. mirror safe summary fields to `clusterstate.json`.

### Phase 3: future CRM settlement integration

Only after Phase 2 is stable:

1. derive CRM settlement events from authoritative local state,
2. add optional CRM sender/auth integration,
3. keep settlement transport failures separate from authoritative local commits.

## Validation checklist

The implementation is correct only if all of the following are true.

### Registration-only path

- a Cloud18-registered instance can run with DBaaS mode disabled,
- sponsorship endpoints are unavailable or clearly rejected,
- no sponsorship files are created,
- no billing-owner requirement appears,
- registration, plan sync, and peer/platform behavior still work.

### DBaaS-enabled path

- sponsorship endpoints are available,
- sponsorship state is created only when the workflow is used,
- billable acceptance requires `billing_owner_ref`,
- later CRM settlement work can derive from local authoritative state,
- DBaaS mode cannot be disabled until `pending` and `sponsor` state has been
  drained.

### Marketplace visibility path

- toggling `cloud18-disable-for-sale` changes only for-sale visibility,
- it does not unregister the instance,
- it does not disable generic Cloud18/platform behavior,
- it does not substitute for the DBaaS mode gate.

## Terminology note

Future operator-facing help text, flags, and UI copy should keep these concepts
separate:

1. **Cloud18 registration**,
2. **DBaaS mode**,
3. **for-sale visibility**.

The current repository still uses some Cloud18 wording that can sound DBaaS-specific.
When the new mode gate is implemented, naming should be updated so operators can tell
which features are universal and which are optional resale behavior.

For that reason, the recommended user-facing flag/config wording is:

- `cloud18` = registered on Cloud18,
- `cloud18-dbaas-enabled` = enable optional sponsorship/credit/settlement behavior,
- `cloud18-disable-for-sale` = hide marketplace sale visibility.

## Final recommendation

Implement the next repman step with this strict boundary:

1. **registration is universal**,
2. **DBaaS/sponsorship/credit is optional**,
3. **the optional path is controlled by one explicit server-level gate**,
4. **cluster-level billing gating is deferred until a real mixed-fleet requirement is proven**.

This is the best option because it matches the current server-scoped Cloud18 model,
keeps in-house installations simple, and avoids over-designing per-cluster billing
control before the product requires it.
