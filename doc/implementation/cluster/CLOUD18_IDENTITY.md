# Cloud18 identity: the registered account's login, no GitLab password (#2159)

## Why

A Cloud18 instance proved who it was with the GitLab password grant (ROPC): `cloud18-gitlab-user` /
`cloud18-gitlab-password` kept in its config and sent to GitLab, then the GitLab `id_token` traded at
the S3 storages' STS. ROPC is deprecated (OAuth 2.1, RFC 9700), cannot work with 2FA, can be turned
off by a GitLab admin setting, and is removed in GitLab 19 (#2146).

## What the instance uses now

The Cloud18 identity realm, `cloud18-identity-url` (default `https://identity.cloud18.io/realms/cloud18`,
Keycloak). The instance gets its identity **by itself**: no client of its own, no secret, no redirect
URI, nothing pushed by the BO. Every instance uses the realm's one public client
`replication-manager` (`config.ConstCloud18IdentityClientID`): password and refresh grants only, every
other flow off, no redirect URI.

### The login: the registered account

The Cloud18 login is the account the instance is registered with. Nobody connects it by hand: an
instance that is not registered cannot use what the login opens anyway (2026-10-28, the Connect button
and the device login it started are removed).

- **At the registration** (`cloud18Register`, then `config.KeepCloud18Login`): once the instance is
  registered, repman signs in with the email and password the person just gave (`grant_type=password`,
  the public client, scope `openid offline_access`). The password serves this sign-in only and is never
  kept, as the registration never keeps it.
- **An instance registered before** keeps its password (`cloud18-gitlab-user`,
  `cloud18-gitlab-password`, encrypted with its key; the accounts and their passwords moved to the realm
  on 2026-10-26). With no login kept, it signs in with it (`cloud18RegisteredLogin`): at the first
  keep-alive after the start (`Cloud18IdentityKeepAlive`, every 5 minutes at most while the realm is out
  of reach), or at the first use. An instance upgraded from a GitLab-login release keeps its SSO archives
  with nothing to do.
- **Registered with neither** (no password, no login: registered by a release that kept none and made no
  login): state `refused`, "register this replication-manager again (same URI)", which makes the login.

The answer's **offline refresh token** is kept in `<working dir>/cloud18-identity.json` (0600, the
token encrypted with the instance's key, `hash_`), with the realm, the account (`preferred_username`,
`sub`) and the date. No encryption key: no login (never kept in clear). The password is never sent again
while the login is valid.

The file is the instance's own: git pushes only `<cluster>/*.toml`, `default.toml` and the event logs,
so it never reaches the config repository or a peer (each peer connects with its own login). A restore
of the config at start (`monitoring-restore-config-on-start`, which empties the working directory) keeps
it (`KeepCloud18LoginFile`).

API (global settings grant): `GET /api/clusters/settings/actions/cloud18-identity` (state `none` not
registered, `registered` no login yet, `connected`, `refused` with the reason; never a token). CLI:
`replication-manager-cli cloud18 status`. GUI: Cloud18 settings, row *Identity* (`Cloud18Identity.jsx`).

### Tokens (`config.Cloud18IdentityToken`)

The access token comes from the `refresh_token` grant of the public client with the kept offline token,
is cached per instance (working directory) until 30 s before it expires (every S3 mount and the vault
share it), and its `sub` is returned. A refresh token the realm rotates is written back to the file.

- **Locking**: the cache lock is never held during the request. The requests of one instance are
  serialized (`fetch`): callers waiting take the token the first one got.
- **Checks**: `cloud18-identity-url` must be `https` (plain `http` to the loopback only, for tests); a
  login of another realm than the one set is not used.
- **Answer**: a token without `expires_in` is refused; one is kept 24 h at most. A `sub` of `.` or `..`
  is refused, like one holding a path separator.
- **Lifetime**: the realm's offline session has **no maximum**, idle 30 days. A running instance
  refreshes the login at least daily (`Cloud18IdentityKeepAlive`, from the server's state checks),
  whatever uses it, so it never reaches the idle limit and nobody connects it again unless it is revoked.
- **Ended**: the realm refusing the refresh (revoked, ended, idle) ends the login; an instance with a
  kept password signs in again at once. An account the realm refuses is not asked again until the
  registration changes (its fingerprint, never the password): state `refused` with the realm's reason,
  and the server raises `GWARN022` ("register this replication-manager again").

## Refused or transient

`config.ErrCloud18Refused` marks a definitive answer: a 4xx of the realm or of the CRM (except 408 and
429), an instance not registered, a refused account, an ended login, no encryption key, no CRM URL, a URL not `https`. Anything else (network error,
timeout, 5xx, 429) is transient. A refusal of the refresh also ends the login (above). Only a refusal turns Cloud18 off: `InitGitConfig` and
`RotateGitAccessToken` keep it on for a transient error. `InitGitConfig` then tries again every 5 minutes
(`retryInitGitConfig`, one loop at a time) until it succeeds, Cloud18 is turned off or the server stops;
the rotation is tried again at the next push refused by GitLab.

## Git and registration

The instance's git (config and pull repositories) no longer depends on the identity token: an instance
registered through Cloud18 keeps its own GitLab project tokens and renews them itself, see
[CLOUD18_REGISTRATION.md](CLOUD18_REGISTRATION.md). The CRM issues no GitLab token (removed).

## Support chat (meet)

The support chat login (`meethelper.CreateMeetUserClient`) took the GitLab password; it is no longer done
at start, so `repman.MeetUserID` stays empty and the `GINF005` state ("Support chat connected") is no
longer raised. The login made when an admin logs in (`server/api.go`) still uses the GitLab password,
when the instance has one.

The public client carries the realm's `cloud18-storage` client scope:

- `sub` = the person's permanent GitLab user id (user attribute `gitlab_id`): the folder an SSO archive is
  written to (`${jwt:sub}` in the storage policy) is the one it had with the GitLab login, per user;
- audience `cloud18-storage` (`config.ConstCloud18StorageAudience`): the client id the storages trust.

## S3 SSO storages (`cluster/cluster_s3_sso.go`)

`cloud18SSOCredentials.Retrieve()` takes the identity token and trades it at the storage's STS
(`AssumeRoleWithWebIdentity`) with the role MinIO derives from the storages' client id:
`minioRoleArn(region, "cloud18-storage")` = `arn:minio:iam:<region>::role/uis-o2VW9GtZuoJDSshFCPUr0Rs`
(or the provider's own `roleArn`). `cloud18SSOReady()` refuses a provider when the instance is not
registered (`ErrCloud18SSONotRegistered`) or has no Cloud18 login and cannot make one
(`config.HasCloud18Login`: a login kept, or a registered account the realm did not refuse).
The STS exchange and the renewal before expiry are unchanged.

## Vault (`config.GetVaultConnection`)

`vault-auth = jwt` logs in at `auth/jwt/login` with the same identity token; the role is `vault-role-id`
when set, else `replication-manager` (the realm's client id; the Cloud18 vault, `vault.cloud18.io`,
OpenBao, binds it on `sub`). AppRole stays the default and the way to use one's own vault. The KV layout read is the same
(`vault-mode = config_store_v2`, mount `vault-mount`).

## Switch, no setting

There is no setting to keep the GitLab login for the storages: `cloud18-sso-client-id` /
`cloud18-sso-client-secret` are removed, and so are the per-instance `cloud18-identity-client-id` /
`cloud18-identity-client-secret` of an earlier state of this branch (a config still holding them is read
without them). The servers accept both during the fleet upgrade: the Cloud18
MinIOs have two OpenID providers (GitLab and Keycloak, two roles, the same policy). Releases before this
one keep logging in with GitLab: the storages keep trusting GitLab for as long as such releases run, and
the folder is the same with both (per user, the GitLab id), so a customer mixing versions sees the same
archives.

## Not here

- The realm's setup: the public client `replication-manager` (password grant, `directAccessGrantsEnabled`,
  2026-10-28; scope `cloud18-storage` as default, `offline_access`), offline session without maximum and
  idle 30 days, and the vault jwt role `replication-manager` bound on `sub`.
- A browser redirect login: only if repman registers its own redirect URL itself (a client of its own
  through the realm's client registration), not now.
- The other uses of the GitLab password (`cloud18-gitlab-password`), to remove before the setting is
  (registration no longer uses it, CLOUD18_REGISTRATION.md):
  - the Cloud18 connect from the settings (`api_global_settings.go`), which stores it;
  - the Cloud18 login of an instance registered before #2159 (`cloud18RegisteredLogin`): once that
    password is gone, the login made at the registration is the only one;
  - `Config.CreateGitlabProjects` (`config/config.go`), through a GitLab basic-auth token;
  - the peer credentials (`server/server.go`, `SetPeerCredentials`) and the peer login check
    (`server/api.go`, "Peer does not have cloud18 setup");
  - the Cloud18 git user added to the cluster's users (`server/server.go`, `AddCloud18GitUser`);
  - the admin-login support chat (`server/api.go`), the Cloud18 user's password (`server/api.go`);
  - Cloud18 infra and MCP logins (`server/server_cloud18_infra.go`, `server/server_cloud18_mcp.go`).
