# Offline License — Cloud18 plan for air-gapped / PCI instances

*repman-side (this public repo). The signing/generation pipeline is back-office
and documented in the private BO repo — never here (T15).*

## Problem
Some instances (PCI, air-gapped) run with **no internet**, so they cannot reach
the CRM to learn their Cloud18 **subscription plan**, and plan-gated features stay
disabled. The online path is `server.syncSubscriptionPlanFromCRM()`.

## Design
A **signed offline license file** is the offline courier of the same plan the CRM
would return. The client obtains a per-identity license artifact through their
Signal18 GitLab account (from a machine that has internet) and drops it on the
offline instance; repman verifies it **offline** and loads the plan into memory.

### Trust
- **Ed25519**, reusing the **existing plugin-signing keypair**. repman verifies
  with the already-embedded public key (`plugin-signing-public-key` /
  `Conf.PluginSigningPublicKey`) — the same key that verifies plugins. No new key
  material in repman.
- Only the **public key** is embedded in repman (the verifier). The `license.json`
  and its detached `license.sig` are **delivered/downloaded**, never embedded —
  they are per-client, generated after the binary is built, so they cannot be
  baked in. (Contrast plugins, whose binary + `.sig` are static and ship embedded
  together.)

### Identity binding
The signed payload carries `domain / sub-domain / sub-domain-zone`. repman accepts
a license **only** if that identity matches this instance's own `cloud18-domain` /
`cloud18-sub-domain` / `cloud18-sub-domain-zone`. A license copied to another
instance is rejected.

### No expiry — revocation = key rotation
No expiry field: a license is valid as long as the embedded public key verifies it.
To revoke/refresh, rotate the signing key — a new release embeds a new public key,
invalidating every license signed with the old one. Nothing time-based to drift on
an air-gapped box.

### Soft enforcement
Missing file / bad signature / identity mismatch / wrong (rotated) key ⇒ the plan
falls back to its default (`free`), plan-gated features are unavailable, and a
`GWARN015@license` WARN state is raised. **Monitoring is never blocked** — the plan
is self-declared/soft; the license is a cryptographically-vouched courier, not a
DRM lock.

## Config (TOML)
Single switch — no redundant boolean:
- `cloud18-license-file` — path to `license.json`. **Empty (default) = normal
  online CRM path.** When set, the instance sources its plan from this file and
  does **not** call the CRM.
- Verification key: reuses `plugin-signing-public-key`.

## `license.json` shape
```json
{
  "domain": "client",
  "subDomain": "site",
  "subDomainZone": "fr-1",
  "plan": "partner",
  "issuedAt": "2026-08-19T00:00:00Z",
  "nonce": "<random>"
}
```
`license.sig` = raw Ed25519 signature over the exact bytes of `license.json`,
alongside it (also accepts `license.json.sig`).

## Code
- `server/license.go` — `OfflineLicense` + `loadOfflineLicensePlan()` (read →
  verify Ed25519 → identity check → `persistInstanceSubscriptionPlan()`).
- `server/server.go` — post-config-load: `cloud18-license-file` set ⇒
  `loadOfflineLicensePlan()`, else `syncSubscriptionPlanFromCRM()`.
- `config/error.go` — `GWARN015` license-invalid state template.

## Follow-ups
- GUI surface showing license status (loaded / valid / identity / plan) so an
  operator can see why plan-gated features are (un)available (T6).
- Continuous re-evaluation of the WARN state across ticks (stamped once at startup
  load today).
- **BO side (private repo):** generate + sign + publish the per-client license.
