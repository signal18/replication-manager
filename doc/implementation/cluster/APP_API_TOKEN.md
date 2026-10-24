# App API token: `{{app.api.url}}` and `{{app.api.token}}` (#2158)

An app that reads its own cluster from the replication-manager API (the `status` template:
Uptime Kuma kept in sync with the cluster's apps, servers and proxies) gets its URL and a
credential from replication-manager, the way `{{app.db.*}}` and `{{app.randompassword}}` are
provided. A template can then be added to any cluster and work, with nothing pasted by hand.

## Keys

| Key | Value |
|---|---|
| `{{app.api.url}}` | `Conf.MonitorAPIURL()`: the URL the app's sensor is given (`REPLICATION_MANAGER_URL`) |
| `{{app.api.token}}` | the app's API token, encrypted (`hash_…`); a secret-type variable receives it decrypted at push (`GetDecryptedEmbedded`) |

Only a template whose content references `{{app.api.token}}` gets a token
(`appAPITokenWanted`, the app's own key, not a sibling's).

## The token

- **Owner**: the cluster's admin user (`admin`, `appAPITokenOwner`): the token is listed in that
  user's API tokens, label `app <cluster>/<app>`, revocable there.
- **Scope**: the app's cluster only.
- **Grants**: fixed in the code (`appAPITokenGrants` = `cluster-show-routes`), read-only. The
  token store embeds at least one grant; the topology reads need none beyond a valid token in scope.
- **Lifetime**: `app-api-token-expire-days` (server setting, default 1825; 0 = never; negative refused).
- **Storage**: app settings `app-api-token` (encrypted), `app-api-token-id`, `app-api-token-expires`.
  Without an encryption key the token would be saved in clear: the add is refused.
- **On the wire**: masked (`*****`, `MaskAppGeneratedSecrets`) in `GET /topology/apps` and
  `GET /apps/{name}`, as is `app-random-password`.

## Life cycle

| When | What (`cluster/app_api_token.go`) |
|---|---|
| add (`AddSeededApp`) | `ApplyAppAPIToken`: minted when the app has none, before the substitution; an add that fails afterwards revokes it (rollback) |
| provision (`OpenSVCProvisionAppService`) | `RenewAppAPIToken`: when under 90 days remain, a new token, the variables holding the old value get the new one, the app config is saved, **then** the old one is revoked. A failed save puts the old token back and revokes the new one: the app never holds a revoked token. A failure keeps the current token and the provision goes on (the variables are pushed after, so a running app gets the new token) |
| daily (server) | `WarnAppAPITokenExpiry`: a WARN per app whose token is due, renewal being at provision only |
| drop (`RemoveAppMonitor`) | `revokeAppAPIToken` |
| peer import (`ImportAppConfig`) | the peer's token fields are cleared and the file saved again: the token belongs to the peer's store, this one can neither renew nor revoke it |

The app config is read and written under `app.Lock()`; the token store call runs unlocked.

The token store lives in the server: cluster code reaches it through
`ServerGlobals.IssueAppAPIToken` / `RevokeAppAPIToken` (`server/api_token_app.go`), the
cycle-avoiding carrier already used for the backup semaphore.

## Reads the app does

- `GET /api/clusters/{c}/topology/apps` now carries each app's `routes` (cname, ports, protocol,
  mode, monitor), so the app needs no deployment grant.
- `topology/apps`, `topology/servers`, `topology/proxies` check no grant (any valid login reads
  them); they now refuse (403) an **API token** whose scope excludes the cluster
  (`refuseAPITokenOutOfScope`). Logins are unchanged.

## Tested (dev3, 2026-10-24)

Unit: `cluster/app_api_token_test.go` (wanted key, renewal threshold, mint/keep, refusal without a
store, without an encryption key or by the store, renewal of the variable and revoke of the old
token, failed save keeps the old token and revokes the new one, masking and routes in the API view,
substitution); `server/api_token_app_test.go` (in-scope read passes, other cluster 403, no token
left to the login checks, revoke, negative lifetime refused, 0 = never, label cut to 64 bytes on a
character boundary).

End to end, an app added from the `status` template:
- token listed under `admin`, scope `[dev3]`, grants `[cluster-show-routes]`, expiry +5 years;
  the saved variable holds it encrypted; `{{app.api.url}}` = the sensor's URL;
- with the token: topology apps/servers/proxies 200 (routes present, token masked); app setting,
  deployment variable, app stop, cluster setting, token creation 403;
- an add refused later (plan pot) revokes the token it minted;
- drop revokes it: the next read is 401.

Known: `GET /apps/{id}/deployment` (the bare path, secrets masked) is readable by any token or
login of the cluster: the `/deployment/` rule matches the sub-paths only. Pre-existing.
