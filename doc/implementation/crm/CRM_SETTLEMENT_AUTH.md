# Cloud18 Settlement Instance Auth Plan

**Status:** implementation plan

**Purpose:** define the preferred authentication model for
`POST /api/internal/settlement-events` as a per-instance machine credential
issued by CRM and bound to one registered replication-manager instance.

## 1. Executive summary

The best model is:

1. replication-manager owns cluster topology,
2. replication-manager owns canonical `cluster_ref`,
3. replication-manager owns `send_mode` such as `active` or `standby`,
4. CRM stores one registration row per instance,
5. CRM issues one settlement credential per registered instance,
6. settlement-event auth is per-instance, not per-`uri`,
7. only the instance currently marked `active` for one `cluster_ref` may send
   settlement events,
8. `POST /api/register/confirm` may return the new settlement credential only
   as additive fields.

This is better than one global shared secret because:

1. compromise is isolated to one instance,
2. active-standby peers can share one `uri` safely,
3. CRM does not invent topology that belongs to replication-manager.

## 2. Problem statement

The settlement route is powerful. It can send events such as:

1. `sponsorship_started`
2. `sponsorship_ended`
3. `base_plan_changed`
4. `app_provisioned`
5. `app_unprovisioned`

So CRM must know that the sender is a trusted registered instance for that
cluster.

The wrong models are:

1. no authentication,
2. one global secret for all instances,
3. reusing human registration credentials,
4. reusing a human GitLab bearer token for the settlement-event route itself,
5. treating `uri` as the unique machine identity.

## 3. Backward-compatibility boundary

`POST /api/internal/settlement-events` is a new additive internal route. It is
not part of the current frozen consumer contract in `docs/CONSUMER.md`.

Therefore:

1. the new route may require the correct machine-auth model from day one,
2. no global bridge is required just to preserve old consumers,
3. the compatibility requirement applies only to existing public routes and to
   preserving current `POST /api/register/confirm` behavior.

For `POST /api/register/confirm` specifically:

1. current status behavior must stay intact,
2. current required response fields must stay intact,
3. settlement-auth fields may be added only as additive response fields,
4. if `cluster_ref` and `send_mode` are introduced at confirm time, they must
   also be additive request fields rather than changes to the existing required
   request shape.

## 4. Ownership boundary

### 4.1 Replication-manager owns

1. canonical `cluster_ref`,
2. which instances belong to the same logical cluster,
3. whether an instance is `active`, `standby`, or `disabled`,
4. promotion and demotion decisions,
5. the decision to attach a newly registered standby instance to an existing
   logical cluster.

CRM must not invent or infer any of those from `uri` alone.

### 4.2 CRM owns

1. per-instance registration persistence,
2. per-instance settlement credential issuance,
3. credential rotation and revocation,
4. credential-to-instance binding verification,
5. enforcement that only the currently active sender for one `cluster_ref` may
   emit settlement events.

## 5. Identity model

The settlement sender identity must be a machine identity, not a human one.

### 5.1 Human identities that must not be reused

Do **not** use for settlement-event auth:

1. register body credentials from `POST /api/register`,
2. the caller's GitLab bearer from `POST /api/register/confirm`,
3. frontend user auth,
4. PAT-style end-user auth.

Those prove human identity, not settlement authority.

### 5.2 Machine identity

One settlement credential must be issued per registered instance and bound to:

1. `instance_registration_ref`,
2. `uri`,
3. `cluster_ref`,
4. registration status,
5. `send_mode`,
6. credential status.

This allows:

1. multiple registered instances to share one `uri`,
2. multiple registered instances to share one `cluster_ref`,
3. only one of them to be the active settlement sender.

## 6. CRM data model

### 6.1 `instance_registrations`

Purpose: one authoritative CRM row per registered replication-manager instance.

Recommended columns:

1. `id`
2. `instance_registration_ref` unique
3. `uri`
4. `cluster_ref`
5. `gitlab_user_id`
6. `status` (`pending`, `confirmed`, `unregistered`, `revoked`)
7. `send_mode` (`active`, `standby`, `disabled`)
8. `confirmed_at` nullable
9. `unregistered_at` nullable
10. `created_at`
11. `updated_at`

Rules:

1. one row represents one instance registration,
2. multiple rows may share the same `uri`,
3. multiple rows may share the same `cluster_ref`,
4. `cluster_ref` is supplied by replication-manager, not invented by CRM,
5. at most one row per `cluster_ref` may have `send_mode = active` at a time.

### 6.2 `instance_settlement_credentials`

Purpose: per-instance settlement auth credentials.

Recommended columns:

1. `id`
2. `instance_registration_ref`
3. `settlement_key_id` unique
4. `secret_hash`
5. `status` (`active`, `revoked`, `rotated`)
6. `issued_at`
7. `rotated_at` nullable
8. `revoked_at` nullable
9. `last_used_at` nullable
10. `created_by_registration_id` or equivalent nullable audit reference

Rules:

1. store only a hash of the secret,
2. never log or return the secret after initial issuance,
3. support one active credential per registered instance in Phase 1,
4. later rotation may allow overlapping old/new credentials for handoff.

## 7. Active-Standby rule

Because multiple registered instances may share one `uri` and one
`cluster_ref`, CRM must not rely on either alone as the sender identity.

Best rule:

1. each settlement credential belongs to exactly one
   `instance_registration_ref`,
2. multiple instance registrations may exist for one `cluster_ref`,
3. exactly one instance registration per `cluster_ref` may have
   `send_mode = active`,
4. standby instances keep their own credentials,
5. standby instances must not be allowed to send settlement events,
6. promotion is not a CRM decision,
7. CRM only persists and enforces the `send_mode` provided by
   replication-manager.

If a promotion route exists, it must not decide topology. It may only persist an
already-made replication-manager decision.

## 8. Credential model

Phase 1 recommended model:

1. CRM issues a per-instance bearer token,
2. CRM returns:
   - `instance_registration_ref`
   - `cluster_ref`
   - `settlement_key_id`
   - `settlement_secret`
3. replication-manager stores the plaintext locally,
4. CRM stores only the hash.

Example additive response fields:

```json
{
  "instance_registration_ref": "ir_01...",
  "cluster_ref": "cluster-123",
  "settlement_key_id": "sk_01...",
  "settlement_secret": "generated-once-and-returned-once"
}
```

### 8.1 Why `settlement_key_id` plus secret is preferred

This is better than returning only one opaque bearer token because it allows:

1. clean rotation,
2. explicit credential inventory in CRM,
3. future migration to signed requests without changing the instance identity
   model.

## 9. Auth flows

### 9.1 Initial issue at `POST /api/register/confirm`

1. replication-manager completes `POST /api/register/confirm` for one instance,
2. CRM confirms that registration,
3. CRM creates or resolves `instance_registration_ref`,
4. CRM issues one settlement credential for that instance,
5. CRM returns the credential once in the successful confirm response.

Backward-compatible rule:

1. these response additions must be optional and additive,
2. existing response semantics must not change,
3. old consumers may ignore the new fields safely.

Phase 1 explicit rule:

1. `POST /api/register/confirm` is **not** the canonical place where CRM first
   learns `cluster_ref` or `send_mode`,
2. CRM must not invent either value at confirm time,
3. replication-manager persists canonical `cluster_ref` and `send_mode` later
   through `POST /api/instances/settlement-sender-state` after it has finished
   its own topology decision,
4. the confirm response only returns settlement credential data additively.

### 9.2 Reissue / rotate / revoke

The plan must not rely on `POST /api/register/confirm` as the only issuance
moment.

Required flows:

1. reissue for already-registered brownfield instances,
2. rotate after local-state loss or planned replacement,
3. revoke on unregister.

Recommended additive route family:

1. `POST /api/instances/settlement-credentials/issue`
2. `POST /api/instances/settlement-credentials/rotate`
3. `POST /api/instances/settlement-credentials/revoke`
4. `POST /api/instances/settlement-sender-state`

Best auth model for these management routes:

1. use the existing GitLab Bearer auth model already used by protected instance
   routes,
2. require the same `uri` ownership or group-membership checks already used by
   subscription and unregister flows,
3. allow the caller to target either:
   - one explicit `instance_registration_ref`, or
   - one `uri` when recovering local state,
4. when `uri` is used, CRM resolves the authorized
   `instance_registration_ref` internally after authz passes,
5. if more than one eligible registration exists for the same caller and `uri`,
   CRM must refuse the mutation until the caller specifies the explicit
   `instance_registration_ref`,
6. resolve and mutate the final target instance row only after authz passes.

Sender-state atomicity rule:

1. `POST /api/instances/settlement-sender-state` persists
   replication-manager's topology decision only; it does not decide topology,
2. setting one instance to `send_mode = active` for a given `cluster_ref` must
   run in one DB transaction,
3. in that same transaction, CRM must set the target instance to `active` and
   demote every other instance for that `cluster_ref` to `standby` or
   `disabled`,
4. repeating the same target-active request must be idempotent,
5. after commit, CRM must still satisfy the invariant that at most one
   instance registration per `cluster_ref` has `send_mode = active`.

This is the best recovery path because it reuses an already-existing trusted
operator identity instead of inventing a second bootstrap secret.

Phase 1 explicit sequencing rule:

1. register-confirm creates the per-instance registration row and issues the
   credential,
2. `settlement-sender-state` is the canonical Phase 1 path for first writing
   `cluster_ref` and `send_mode`,
3. settlement-event sends must be rejected until CRM has the instance row,
   credential, `cluster_ref`, and `send_mode = active`.

### 9.3 Brownfield or recovery flow

For already-registered or recovered instances:

1. CRM authenticates the request using existing protected instance auth,
2. CRM resolves the target `instance_registration_ref` either from the explicit
   request field or by unique authorized `uri` lookup,
3. CRM issues or rotates a settlement credential for that instance,
4. CRM returns the new credential once,
5. the old credential is revoked immediately or after a controlled overlap
   window.

Recovery rule:

1. if the caller no longer knows `instance_registration_ref`, it may request
   recovery by `uri`,
2. CRM may perform the recovery only when exactly one authorized instance
   registration matches that caller plus `uri`,
3. otherwise CRM must return an ambiguity error and require explicit
   `instance_registration_ref`.

Brownfield bootstrap rule:

1. if CRM successfully authenticates the caller through existing protected
   instance auth and no `instance_registrations` row exists yet for that
   authorized instance context, CRM may create the missing registration row
   before issuing the first settlement credential,
2. that bootstrap must derive the row only from authenticated protected
   instance context plus CRM-owned persisted state,
3. if more than one candidate instance could match the same authorized caller
   and `uri`, CRM must reject the request as ambiguous until
   `instance_registration_ref` is supplied explicitly,
4. concurrent zero-match bootstrap attempts for the same `(uri, owner)` must
   be serialized with one application lock so they cannot both create a fresh
   registration row,
5. if CRM cannot acquire that bootstrap lock within the request timeout, it
   must fail with a retryable `503 lock_unavailable` response rather than
   proceeding without serialization.

## 10. Settlement-event request shape

### 10.1 Simple Phase 1 bearer form

```http
Authorization: Bearer <settlement_secret>
X-Settlement-Key-Id: <settlement_key_id>
```

Payload still includes:

1. `cluster_ref`
2. event contract fields from
   `docs/CLOUD18_BACKEND_CRM_SETTLEMENT_PLAN.md`

`uri` should **not** be required in the settlement event payload for auth.
CRM should derive the bound `uri` from the authenticated instance registration.

### 10.2 Better long-term signed form

Preferred future evolution:

```http
X-Settlement-Key-Id: <settlement_key_id>
X-Settlement-Timestamp: 2026-07-23T10:00:00Z
X-Settlement-Signature: <HMAC or signature>
```

Signature input should include:

1. method
2. path
3. timestamp
4. request body hash

This avoids treating the secret itself as the reusable bearer.

## 11. CRM-side validation rules

For any settlement event request, CRM must verify:

1. settlement credential exists,
2. credential status is `active`,
3. bound instance registration exists and is not revoked,
4. bound instance registration status is active enough to send,
5. bound instance registration `cluster_ref` matches payload `cluster_ref`,
6. bound instance registration has `send_mode = active`,
7. only after auth passes does CRM evaluate settlement event freshness,
   idempotency, and business validation.

Reject when:

1. credential missing,
2. credential revoked,
3. registration identity mismatch,
4. `cluster_ref` mismatch,
5. sender is not the currently active settlement sender,
6. instance registration is no longer active.

## 12. Why this is the right model

This model is preferred because:

1. settlement auth becomes instance-scoped instead of globally shared,
2. CRM's existing registration authority becomes the issuer of machine auth,
3. compromise of one instance does not compromise all instances,
4. active-standby peers can share one `uri` safely,
5. replication-manager remains the topology authority,
6. revoke and rotate become operationally manageable.

## 13. Final recommendation

Do not reuse human registration or GitLab user credentials for the settlement
event route itself.

Implement a **CRM-issued per-instance settlement machine credential** instead.

Use this flow:

1. confirm registration,
2. create or resolve `instance_registration_ref`,
3. issue instance settlement credential additively in the confirm response,
4. persist repman-supplied canonical `cluster_ref` later through
   `POST /api/instances/settlement-sender-state`,
5. persist repman-supplied `send_mode` through the same route,
6. use that credential for `POST /api/internal/settlement-events` from day one
   of the new route,
7. support explicit reissue and rotate for brownfield and recovery,
8. revoke credentials on unregister.
