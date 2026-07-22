# API credential authentication model

The API auth model is keyed on **whether an account carries a local password**.
This makes local vs SSO identity explicit and prevents a same-named SSO identity
from taking over a local account.

## The invariant

| Account | Declared by | Authenticates via | Other path |
|---------|-------------|-------------------|------------|
| **Local** | a **non-empty** password in `api-credentials` | local password only | SSO refused |
| **SSO**   | **no** password (email-only entry) | OIDC/SSO only | local password refused |
| **Owner** | `Cloud18GitUser` (registering user) | OIDC/SSO (its credential is a GitLab password) | — |

So: *a password means the account is local; no password means it is SSO; the
registering owner is the single exception.*

## Enforcement (`cluster.IsValidACL`, `cluster/cluster_acl.go`)

- **OIDC path:** if the matched account has a non-empty password it is refused —
  a password-protected name is local, and SSO must not authenticate (or bind to)
  it. Exempt: `Cloud18GitUser`.
- **Password path:** if the stored **or** submitted password is empty, auth is
  refused — a passwordless (SSO) account never authenticates by a blank local
  password. `GetAPIUser` (gRPC auth) carries the same guard.

## Collision handling (`server/api.go`, OIDC callback)

When an SSO login's identity matches a **password-protected local account**, the
login is denied and a security event is logged
(`api_sso_local_collision`, see [SECURITY_LOGGING.md](SECURITY_LOGGING.md)) —
loud by design, so a real attempt to authenticate against a reserved local name
(`admin`, `system`, `dba`, …) is visible rather than silently ignored. The owner
(`Cloud18GitUser`) is exempt.

## Why

Two API auth doors share one `APIUsers` map: the local password login and the
OIDC callback. Without this model, an empty-password entry (any SSO-provisioned
peer, or a hand-written passwordless `api-credentials` line) could be
authenticated on the **local** door with a blank password, and an SSO identity
could ride a same-named local account's ACL. Binding auth method to password
presence closes both.

## Operational / migration note

- **A local API account must have a password.** Any account intended for local
  password login needs a non-empty password in `api-credentials` — a passwordless
  entry is now treated as SSO-only and will not accept a local password.
- **An SSO peer must be passwordless.** Grant partners by **email only** (no
  `:password`), so they authenticate through the identity manager.
- The default `admin:repman` and normal password-protected accounts are
  unaffected.

## Tests

`cluster/cluster_acl_auth_guard_test.go`: correct/blank/wrong password on the
local path, blank-password rejected for a passwordless account, SSO allowed for
passwordless, SSO refused for a password-protected local account, and the owner
exemption.
