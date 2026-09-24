# User-issued API tokens

Issue #1835. Branch `feat/api-tokens-embedded-grants`.

A user can issue bearer tokens for themselves, each narrowed to a subset of their own
grants and to a cluster scope, with an expiry. Machine clients (an MCP server, CI, scripts,
the CLI) authenticate with the token directly, no login round-trip, and the security log
names the person the token belongs to.

This is the credential model for people and third-party clients. The `system` service
account keeps its derived key (`cluster.GetSystemAPIKey`, HMAC of the master secret): that
one is for the sensor repman operates itself, with no attribution and no individual
revocation, which is exactly what a person must not get.

## Token

- HS256 JWT signed with the persistent secret key (`monitoring-key-path`,
  `config.SecretKey`). The interactive login JWT is RSA-signed with a key regenerated at
  every repman start; the persistent key survives restarts and rotates on purpose with
  `monitoring-secret-versioning`. Rotating it invalidates every token (signature) and makes
  the store unreadable: tokens must be reissued after a rotation.
- Claims: `typ=api`, `sub=<user>`, `jti=<id>`, `grants=[compact prefixes]`,
  `clusters=[names]|["*"]`, `iat`, optional `exp`.
- Used as `Authorization: Bearer <token>`.

## Store

`<monitoring-datadir>/api-tokens.json`, mode 0600, written atomically through a `.tmp`
rename. The file is one hex string: the JSON document AES-CFB encrypted with the secret key
through `utils/crypto.Password`, the same primitive that encrypts config secrets. It is added
to the config git-sync `.gitignore` (`server_git.go`) and never leaves the instance.

Record: `{id, user, label, grants, clusters, createdAt, createdFrom, expiresAt, lastUsedAt,
lastUsedFrom, revokedAt, revokedBy, token}`. The token string is kept in the record (decided
2026-09-24) so tokens reload at restart and an owner can display one again. The store is
consulted on every request: a bearer whose `jti` is unknown, revoked, expired, or whose owner
no longer exists in any cluster is refused even with a valid signature. `lastUsedAt` is
updated in memory on every request and persisted at most once a minute per token.

The store is bounded (T18): a revoked or expired record is kept 90 days for the audit trail
(`apiTokenRetention`) and dropped at the next save. A load failure (unreadable file, wrong
key) leaves the store *not loaded*: every operation fails and nothing is written, so a
transient error can never rewrite an empty store over the real one.

## Authority: intersection, never escalation

- At creation (`createAPIToken`) every requested grant prefix must match a grant the owner
  holds in at least one targeted cluster (`cluster.TokenGrantsAllowedFor`); an empty list
  means "everything I hold", compacted with `config.GetCompactGrants`. Named clusters must
  be clusters the owner has an account on; an empty list or `*` is the global scope.
- At request time (`cluster.GetACLUser`) the ACL runs under the principal name
  `token:<id>:<user>` and resolves it to a transient `APIUser` whose grants are the
  token's expanded grants **AND** the owner's current grants in that cluster, roles
  inherited from the owner. Drop a grant from the user and every token loses it; delete the
  user and the tokens die.
- Scope (`tokenURLInScope`): a token scoped to named clusters may only touch
  `/api/clusters/<name>` and `/api/clusters/<name>/...` of those clusters. Global settings,
  peers, cluster add and every other endpoint need the `*` scope.
- A token cannot issue tokens (POST `/api/tokens` requires an interactive login), and a
  token-authenticated `GET /api/tokens` returns the records without the token strings: a
  narrowed token must never read back a wider sibling of the same owner.

## Auth path

`parseAPITokenFromRequest` (server/api_token.go) recognises a token by the HMAC signing
method and `typ=api`, then checks the store. It is consulted first by:

- `validateTokenMiddleware`: a valid token passes, otherwise the RSA login path runs as before.
- `GetUserFromRequest`: returns the owner.
- `IsValidClusterACL`: applies the scope, registers the principal on the cluster
  (`cluster.SetTokenPrincipal`, a per-cluster `sync.Map` refilled on every request so a
  cluster rebuilt by a config reload needs no lifecycle hook) and calls
  `cluster.IsValidACL(principal, "", url, "token")`, which skips the password compare and
  runs only the URL ACL. Denials log `api_token_denied` in the security log.
- `DecryptJWTPassword`: errors, a token carries no password.
- `requestACLUser`: the APIUser view for handlers that check a grant directly instead of
  through the URL ACL (`cluster-test`, `db-backup` on restic mutations,
  `UserHasGlobalGrant`). Any new direct `cluster.APIUsers[username].Grants[...]` check in a
  handler must go through it, or a token would act with the owner's full grants.

Every grant lookup in `cluster/cluster_acl_rules.go` and `cluster_acl.go` goes through
`cluster.GetACLUser`, so the rule engine, the DB log access check and the sub-ACLs are
token-aware without further changes.

## Surface

| Where | What |
| --- | --- |
| `GET /api/tokens` | the caller's tokens, token strings included for an interactive login only |
| `POST /api/tokens` | `{label, grants, clusters, expireDays}`; `expireDays` 0 = server default, -1 = never; returns the record with the token |
| `DELETE /api/tokens/{id}` | revoke: the owner always may; another user needs `cluster-grant` on every cluster the token covers (for a token-authenticated caller the check is on cluster membership of the scope, `requestACLUserOnCluster`, since the URL is not under a cluster path) |
| `GET /api/clusters/{name}/tokens` | every token covering the cluster, no token strings, needs `grant-show` there |
| GUI | user pill in the navbar → User Profile modal → "API tokens" button → tokens modal (`components/Modals/ApiTokensModal`): create (grant picker limited to the union of grants held across clusters, cluster scope from the clusters the user has an account on, expiry), show, revoke. Per person, not per cluster. |
| CLI | `replication-manager-cli token create --label x [--grants "db-show proxy"] [--clusters a,b] [--expire-days N]`, `token list [--cluster name]`, `token revoke <id>`; `--api-token <token>` on every command replaces `--user/--password` |
| Security log | `api_token_created`, `api_token_revoked`, `api_token_denied` |

Settings, both `scope:"server"`: `api-user-tokens` (default true, the T14 off-switch: refuses
issuing and authenticating) and `api-user-tokens-default-expire-days` (default 120, 0 = never).

## Tests

`cluster/cluster_acl_token_test.go`: principal name round trip, intersection with the
owner's grants and live revocation of a grant, unknown/mismatched/out-of-scope principals,
the `token` auth method, `TokenGrantsAllowedFor`.

`server/api_token_test.go`: creation narrowing and default expiry, parse and ACL on a
cluster-scoped token, direct grant checks through `requestACLUser`, revoke by owner and by
another user, expiry, off-switch, owner deletion, key rotation, encrypted store on disk
(nothing in clear, 0600) reloaded by a fresh manager, cluster scope table.

## Known limits

- One master key for everything: a key rotation kills all tokens at once. A per-token
  generation would need a second secret; not done.
- The store is per repman instance. Two repman peers do not share tokens.
- Swagger annotations are on the handlers; regenerate the spec with the usual tooling.
