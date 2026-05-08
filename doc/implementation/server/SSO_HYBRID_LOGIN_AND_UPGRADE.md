# SSO Hybrid Login and Upgrade Contract (Server)

This document describes the agreed login and SSO async-upgrade behavior for non-admin users, including the upgrade endpoint contract, security constraints, and impact areas in the codebase.

---

## Login Flow by Role

### Admin Users

Admin login uses **local auth only**. The login handler validates credentials against `cluster.APIUsers`. It does **not** wait for, initiate, or fall back to SSO.

### Non-Admin Users

Non-admin login supports both local and SSO in a single request:

1. **Local auth (synchronous, tried first).**
   Credentials are validated against `cluster.APIUsers`. If valid, a JWT is issued immediately with the local user's claims.

2. **SSO auth (asynchronous, started only on local success).**
   After the local login succeeds and the JWT is sent, the server independently attempts SSO authentication using the same login identity/credentials for that request. No client polling is required to trigger this; it is a fire-and-forget async step initiated server-side.

   **Retry rule:** retry SSO only for transient failures (network timeout, connection reset, HTTP `5xx`, HTTP `429`).
   Do **not** retry when credentials are invalid (for example OAuth `invalid_grant`, HTTP `401`, or provider message indicating wrong username/password).

   **Mismatch alert rule:** emit Cloud18 + email notification **only** when local auth succeeds and SSO fails with a credential mismatch for the same login attempt (example: wrong password for an existing SSO identity).
   Do **not** notify for users who are simply not registered in SSO.

3. **SSO fallback (synchronous, when local auth fails).**
   If local auth fails, the handler must still allow SSO as a synchronous fallback path. The user is not blocked from logging in via SSO if local credentials are wrong or the user does not exist locally.

   **Retry rule:** same policy as async path — transient failures may retry with short backoff; invalid credentials must fail fast (no retry).

> This means non-admin login never returns 401 purely because local auth failed, as long as SSO can succeed.

### Login Response Contract (Non-Admin)

When non-admin local auth succeeds and async SSO upgrade is started, login returns HTTP `200` with:

```json
{
  "token": "<local_jwt>",
  "upgrade_id": "<upgrade_job_id>"
}
```

- `token`: local JWT issued immediately.
- `upgrade_id`: polling handle for `GET /api/login/upgrade`.

When local auth fails but synchronous SSO fallback succeeds, login returns HTTP `200` with:

```json
{
  "token": "<sso_jwt>"
}
```

No `upgrade_id` is returned in the synchronous SSO fallback path.

### Login Flow Diagram (Non-Admin)

```
Login Request Received
        |
        v
Is user admin?
   |yes|-----------------> Local auth only
   |no |                     Issue JWT, done
        |
        v
  Try local auth (sync)
        |
   +----+----+
   |         |
  OK        FAIL
   |         |
   v         v
Issue local  Try SSO (sync)
JWT immediately
   |         |
   v         +----+----+
Start async        |
SSO upgrade        |
(in background)    |
   |         +-----+-----+
   |         |           |
   v         v           v
Upgrade    OK          FAIL
in progress  |           |
(poll later) v           v
           Issue SSO   Return 401
           JWT, done
```

---

## Billing Access

Billing endpoints remain under strict GitLab-token enforcement. Only users whose JWT contains a valid GitLab token in the `token` claim may access billing. SSO upgrade does not bypass this requirement — the new JWT issued after a successful async upgrade must still carry the GitLab token claim if the user qualifies for billing access.

---

## Cloud18 Alert on Local/SSO Credential Mismatch

When a user successfully logs in via local credentials, SSO result handling must distinguish between:

1. **Credential mismatch** (potential risk): local success + SSO invalid credentials for the same identity.
2. **Not registered in SSO** (onboarding/state): local success + SSO account does not exist.

Only case (1) triggers security notification.

### Trigger Condition

- User is **non-admin**.
- Local auth succeeded.
- SSO attempt completed with confirmed `credential_mismatch`:
  - provider explicitly reports invalid credentials for the same identity, **or**
  - provider returns `invalid_grant`/`401` and a trusted GitLab user-existence check confirms the identity exists.

### Classification Decision Rules

Use the following deterministic order when classifying SSO failure after local success:

1. If provider returns explicit "user not found / not registered" signal → classify as `not_registered`.
2. If provider returns explicit "wrong password / invalid credentials" signal → classify as `credential_mismatch`.
3. If provider returns ambiguous `invalid_grant`/`401` with no reliable discriminator → classify as `unknown_non_retryable`.
4. `unknown_non_retryable` must not trigger Cloud18/email mismatch alert; it is warning-log only.

### Non-Trigger Condition (No Notification)

- Local auth succeeded.
- SSO indicates user is **not registered / not found** in SSO.
- Action: keep user logged in locally, record informational audit/security log, and optionally surface a non-critical UI hint. No Cloud18 alert and no user email.

Also non-trigger:

- Local auth succeeded.
- SSO failure is classified as `unknown_non_retryable`.
- Action: keep user logged in locally, write warning/security log only. No Cloud18 alert and no user email.

### Purpose

- Detect and surface potentially unwanted login behavior where local credentials still grant access but SSO credentials are out of sync.
- Ensure security operators can review and investigate promptly.
- Notify the affected user directly so they can rotate credentials if the mismatch was not intentional.

### Alert Routing

- Use existing Cloud18 alert hook configuration:
  - `cloud18-alert`
  - `cloud18-alert-slack-url`
  - `cloud18-alert-slack-channel`
  - `cloud18-alert-slack-user`
- If Cloud18 alerting is disabled or hook is unavailable, always write a structured security log event as fallback.

### User Email Notification

In addition to the Cloud18 channel alert, send an email notification to the user **only for credential mismatch events**.

- Recipient: authenticated username email (or resolved user email from JWT/user directory).
- Subject (suggested): `Security Notice: Local login succeeded but SSO login failed`
- Body should include:
  - timestamp
  - source IP
  - what happened (local auth success, SSO credential mismatch)
  - recommended action (verify password parity, rotate credentials if unexpected)
  - support contact path

If user email cannot be resolved, still emit Cloud18 alert + security log event.

If SSO result is `not_registered`, do not send this email.

### Suggested Alert Payload

- Event key: `api_login_local_sso_mismatch`
- Fields:
  - username
  - user_email (if resolved)
  - source IP
  - auth path (`local_success_sso_credential_mismatch`)
  - cloud18 instance (`domain/subdomain-zone`)
  - timestamp

For non-critical onboarding state, use a separate event key (example): `api_login_local_sso_not_registered` (log-only, no alert/email).

---

## SSO Token Persistence

The server performs SSO authentication but **does not persistently store the SSO token**. No config, database, or file-system persistence of the SSO token occurs at any point. The token is used solely to extract claims and is discarded after the upgrade JWT is issued.

---

## Async Upgrade Endpoint Contract

### Endpoint

```
GET /api/login/upgrade?upgrade_id=<id>
```

### Purpose

Allows the frontend to poll for the result of an async SSO upgrade that started during login. The `upgrade_id` maps to an in-memory job tracked by the server.

### Response States

| HTTP Status | Meaning |
|---|---|
| `202 Accepted` | Upgrade is in progress. Frontend should continue polling. |
| `200 OK` | Upgrade succeeded. Body contains `{"token": "<new_jwt>"}`. This response is **one-time**; server marks job `consumed` and clears JWT from job state immediately after this response. |
| `409 Conflict` | Upgrade failed (non-success terminal state). Body must include a machine-safe reason, e.g. `credential_mismatch`, `not_registered`, `unknown_non_retryable`, `claim_mismatch`, `retry_exhausted`. |
| `404 Not Found` | `upgrade_id` does not exist. |
| `410 Gone` | Upgrade job is no longer retrievable. Response body must include `status: "expired"` or `status: "consumed"` for disambiguation. |

### Response Body on 200

```json
{
  "token": "<new_jwt>"
}
```

### Response Body on 410

```json
{
  "status": "expired"
}
```

or

```json
{
  "status": "consumed"
}
```

### Response Body on 409

```json
{
  "status": "failed",
  "reason": "unknown_non_retryable"
}
```

### Upgrade Polling Flow Diagram

```
Frontend sends GET /api/login/upgrade?upgrade_id=...
        |
        v
+------+------+
|             |
status=202   status=ready
        |             |
        v             v
  Keep polling     Return 200 with
  (wait ~2s)       new JWT; consume job
        |             |
        +------+------+
               |
          status=failed
               |
               v
          Return 409
          (upgrade failed;
           local JWT still valid)
```

---

## Upgrade Job State Machine

The server maintains an in-memory map of upgrade jobs (e.g., `map[string]*LoginUpgradeJob`). Each job carries:

- `status`: `pending`, `ready`, `failed`, `consumed`, `expired`
- `newJWT`: the upgraded token (set on success)
- `reason`: machine-safe failure reason exposed to the client on `409`
- `error`: internal/debug failure detail, never exposed directly
- `createdAt`: timestamp for TTL enforcement

### State Transitions

```
pending -> ready      (SSO auth succeeded)
pending -> failed     (SSO auth failed)
pending -> expired    (TTL exceeded before completion)
ready -> consumed     (one-time delivery to client)
ready -> expired      (TTL exceeded after ready but before polling)
```

### State Diagram

```
  [pending]
      |
  +---+---+---+
  |       |   |
  v       v   v
ready  failed expired
  |
  v
consumed (one-time)
```

---

## Security and Operational Constraints

### Short TTL

Upgrade jobs have a short server-side TTL (recommended: 60 seconds). Jobs not resolved within the TTL are marked `expired` and cleaned up.

### One-Time Consumption

The `ready` state yields the new JWT **exactly once**.

Implementation rule:
- On first successful `200`, mark job `consumed` and clear `NewJWT` immediately.
- Keep the `consumed` marker until TTL cleanup so repeated polls return `410` with `{"status":"consumed"}`.

This prevents replay of the upgrade token while preserving deterministic client-visible state.

### Retry Policy (SSO)

Recommended defaults:

- Max attempts: **3 total** (initial + 2 retries)
- Backoff: **250ms, 750ms** (bounded exponential with jitter)
- Retryable conditions:
  - network/transport errors
  - HTTP `429`
  - HTTP `5xx`
- Non-retryable conditions (fail fast):
  - OAuth `invalid_grant`
  - HTTP `401`
  - explicit invalid username/password messages from provider

Classification note:
- If provider can distinguish `not registered` from `invalid credentials`, map `not registered` to a separate non-security outcome (no alert/email).
- If provider cannot distinguish and only returns ambiguous auth failure, classify as `unknown_non_retryable`.

If retries are exhausted, mark upgrade job `failed` and return `409` on polling.

### Cleanup Goroutine

A background goroutine runs on a short interval (e.g., every 30 seconds) to sweep expired and consumed jobs from memory.

`consumed` jobs are retained only as markers (without JWT) until cleanup, to support deterministic `410 consumed` responses on repeat polling.

### No Persistence

Upgrade jobs are **not** written to disk, database, or config. They exist only in process memory and are lost on server restart. There is no failover or multi-instance sharing of upgrade state.

### No Config / DB / File Storage

Under no circumstances does the SSO token get written to:
- `config.Config` or any config file
- Any database table
- Any file on disk

---

## Frontend Behavior

When the async upgrade succeeds, the frontend receives a new JWT containing SSO claims. The frontend must:

1. Replace the existing token in `localStorage` (or whichever storage mechanism is in use) with the new token value.
2. Continue using the same storage slot — no separate SSO-specific storage key is introduced.
3. On `410 Gone` or `404`, stop polling and accept that the upgrade window has closed.
4. On `409`, do **not** log the user out; local JWT remains valid.
5. UI may show non-blocking guidance for safe reason categories, but must never expose raw provider errors.

---

## Code Impact Areas

### `server/api.go` — `loginHandler` (lines ~671–860)

Changes required:
- Detect admin vs. non-admin user at the start of the handler.
- **Admin path:** skip SSO entirely; issue JWT after local auth only.
- **Non-admin path (local success):** issue local JWT immediately, then spawn a goroutine that performs async SSO and writes the result to an upgrade job in memory.
- **Non-admin path (local fail):** attempt synchronous SSO; if SSO succeeds, issue SSO JWT; if SSO also fails, return 401.
- Register `GET /api/login/upgrade` route.

### JWT Claim Extraction and Issuance

The JWT-building block at the bottom of `loginHandler` (lines ~823–835) must be refactored into a shared helper:

```go
func issueJWT(userInfo interface{}, gitlabToken string, expDuration time.Duration) (string, error)
```

The upgrade handler (`upgradeHandler`) uses this same helper to produce a new JWT that:
- Carries updated `CustomUserInfo` with SSO claims merged in.
- Preserves or updates the `token` claim if a GitLab token was involved.
- Uses a fresh `iat` and a new `exp` (new expiry window).

### New `upgradeHandler` Function

Signature:

```go
func (repman *ReplicationManager) upgradeHandler(w http.ResponseWriter, r *http.Request)
```

Behavior:
- Read `upgrade_id` from query param.
- Look up job in in-memory map.
- Return `202` / `200` / `409` / `404` / `410` per contract above.
- On `200`, mark job `consumed` and clear `NewJWT` immediately to prevent replay; do not delete marker until TTL cleanup.

### In-Memory Upgrade Job Store

New struct and map on `ReplicationManager`:

```go
type LoginUpgradeJob struct {
   Mu       sync.Mutex
   Status   string // pending, ready, failed, consumed, expired
   NewJWT   string
   Reason   string // credential_mismatch, not_registered, unknown_non_retryable, claim_mismatch, retry_exhausted
   Error    string
   Attempts int
   CreatedAt time.Time
}

type LoginUpgradeStore struct {
   Mu   sync.RWMutex
   Jobs map[string]*LoginUpgradeJob
}
```

Concurrency rule:
- Store mutex protects map access (create/read/delete entries).
- Per-job mutex protects fields inside each `LoginUpgradeJob`.

Jobs are short-lived; no persistence, no expiration watchdog beyond the cleanup goroutine.

### Cleanup Goroutine

Start as part of server initialization:

```go
go func() {
    ticker := time.NewTicker(30 * time.Second)
    for range ticker.C {
        repman.cleanupExpiredLoginUpgradeJobs()
    }
}()
```

`cleanupExpiredLoginUpgradeJobs()` iterates the store and removes jobs where `Status` is `consumed` or `expired`, and jobs where `time.Since(CreatedAt) > upgradeJobTTL`.

### Frontend Login Component

Changes required:
- After a successful login response, check for `upgrade_id` in response body or header.
- If present, start polling loop:

```text
poll interval: 2 seconds
stop on: 200, 409, 404, 410
on 200: replace stored token with new token value
on 409: treat upgrade as failed; keep local JWT (no logout), optionally show non-blocking safe message by reason
on 410: stop polling (expired or consumed)
```

While server-side retries are in progress, endpoint should continue to return `202`.

---

## Implementation Checklist

- [ ] Add `LoginUpgradeJob` struct and `LoginUpgradeStore` to `server/api.go` or a dedicated file.
- [ ] Add `upgradeHandler` function with response state logic.
- [ ] Register `GET /api/login/upgrade` route in `server/http.go` or `server/api.go`.
- [ ] Refactor JWT issuance into shared `issueJWT(...)` helper.
- [ ] Update `loginHandler` to branch on admin vs. non-admin.
- [ ] Update `loginHandler` non-admin path: local success → issue JWT + spawn async upgrade goroutine.
- [ ] Update `loginHandler` non-admin path: local fail → synchronous SSO fallback.
- [ ] Add SSO retry helper with non-retryable invalid-credential detection.
- [ ] Ensure `upgradeHandler` returns machine-safe `reason` (never raw provider/internal error text).
- [ ] Start cleanup goroutine during server initialization.
- [ ] Frontend: detect `upgrade_id` in login response.
- [ ] Frontend: implement polling loop with correct status handling.
- [ ] Frontend: replace token in storage on `200`.
- [ ] Verify billing endpoints still require `token` claim with GitLab token.
- [ ] Verify admin login path has no SSO interaction.

---

## Summary of Behavioral Rules

| Scenario | Behavior |
|---|---|
| Admin login | Local auth only; no SSO, no async upgrade |
| Non-admin, local success | Issue local JWT immediately; start async SSO upgrade in background |
| Non-admin, local fail, SSO pass | Issue SSO JWT via synchronous fallback |
| Non-admin, local fail, SSO fail | Return 401 |
| SSO transient failure | Retry (bounded backoff), then fail if budget exhausted |
| SSO credential mismatch | No retry; fail immediately |
| Local success + SSO credential mismatch | Emit Cloud18 alert + security log event + notification email to affected user |
| Local success + SSO not registered | Keep local session; log-only informational event; no alert/email |
| Local success + SSO unknown_non_retryable | Keep local session; warning/security log only; no alert/email |
| Upgrade success | `200` with new JWT; job consumed |
| Upgrade polling after success | `410 Gone` with `status:"consumed"` |
| Upgrade TTL exceeded | `410 Gone` (job expired) |
| Billing access | Requires `token` claim with valid GitLab token; upgrade must preserve this if user qualifies |
| Server restart | All upgrade jobs lost; no recovery needed |
