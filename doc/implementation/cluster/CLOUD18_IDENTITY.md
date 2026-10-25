# Cloud18 identity: a Keycloak client per instance, no GitLab password (#2159)

## Why

A Cloud18 instance proved who it was with the GitLab password grant (ROPC): `cloud18-gitlab-user` /
`cloud18-gitlab-password` kept in its config and sent to GitLab, then the GitLab `id_token` traded at
the S3 storages' STS. ROPC is deprecated (OAuth 2.1, RFC 9700), cannot work with 2FA, can be turned
off by a GitLab admin setting, and is removed in GitLab 19 (#2146).

## What the instance uses now

The Cloud18 identity realm, `cloud18-identity-url` (default `https://identity.cloud18.io/realms/cloud18`,
Keycloak). Each registered instance is a client of it, `cloud18-identity-client-id` and
`cloud18-identity-client-secret` (a secret of `SecretKeys`, never replicated to peers:
`instanceLocalConfigKeys`), pushed by the BO at registration (`cluster_set.go`) or set from the
Cloud18 settings page (`api_global_settings.go`).

`config.Cloud18IdentityToken()` (`config/cloud18_identity.go`) gets a token with the
`client_credentials` grant at `<realm>/protocol/openid-connect/token`, keeps it per realm and client
until 30 s before it expires (every S3 mount and the vault share it), and returns its `sub`.

- **Locking**: the cache lock is never held during the request. The requests of one client are
  serialized (`fetch`, a lock per realm and client): callers waiting take the token the first one got.
  A slow realm holds up only the callers of that client.
- **Checks before sending the secret**: `cloud18-identity-url` must be `https` (plain `http` to the
  loopback only, for tests); an empty `cloud18-identity-client-secret` is refused without a request.
- **Answer**: a token without `expires_in` is refused; one is kept 24 h at most. A `sub` of `.` or `..`
  is refused, like one holding a path separator. The cache key holds no secret: a new secret takes
  effect when the token is renewed.

## Refused or transient

`config.ErrCloud18Refused` marks a definitive answer: a 4xx of the realm or of the CRM (except 408 and
429), no identity client, no secret, no CRM URL, a URL not `https`. Anything else (network error,
timeout, 5xx, 429) is transient. Only a refusal turns Cloud18 off: `InitGitConfig` and
`RotateGitAccessToken` keep it on for a transient error. `InitGitConfig` then tries again every 5 minutes
(`retryInitGitConfig`, one loop at a time) until it succeeds, Cloud18 is turned off or the server stops;
the rotation is tried again at the next push refused by GitLab.

## GitLab token from the CRM (`Config.Cloud18GitLabToken`)

The GitLab token the instance pushes and pulls its configuration repositories with comes from the
Cloud18 CRM (`cloud18-crm-api-url`, `https`, `POST /api/instances/gitlab-token`) for the identity token.
The CRM revokes the instance's previous token of the same name, so the requests are serialized and a
token got less than 5 minutes ago is handed to the next callers instead of asked for again: two callers
(start, the Cloud18 connect, a rotation) never get two tokens of which the first is already revoked. The
CRM's error text is cut to 200 characters in the logs. The grant's `user_id` and `expires_at` are not
used yet: `InitGitConfig` reads the user from GitLab, the rotation happens when GitLab refuses a push.

## Support chat (meet)

The support chat login (`meethelper.CreateMeetUserClient`) took the GitLab password; it is no longer done
at start, so `repman.MeetUserID` stays empty and the `GINF005` state ("Support chat connected") is no
longer raised. The login made when an admin logs in (`server/api.go`) still uses the GitLab password,
when the instance has one.

The client carries the realm's `cloud18-storage` client scope:

- `sub` = the instance's permanent GitLab user id (attribute `gitlab_id` of its service account): the
  folder an SSO archive is written to (`${jwt:sub}` in the storage policy) is the one it had with the
  GitLab login;
- audience `cloud18-storage` (`config.ConstCloud18StorageAudience`): the client id the storages trust.

## S3 SSO storages (`cluster/cluster_s3_sso.go`)

`cloud18SSOCredentials.Retrieve()` takes the identity token and trades it at the storage's STS
(`AssumeRoleWithWebIdentity`) with the role MinIO derives from the storages' client id:
`minioRoleArn(region, "cloud18-storage")` = `arn:minio:iam:<region>::role/uis-o2VW9GtZuoJDSshFCPUr0Rs`
(or the provider's own `roleArn`). `cloud18SSOReady()` refuses a provider when the instance is not
registered (`ErrCloud18SSONotRegistered`) or has no identity client (`config.ErrCloud18IdentityNotSet`).
The STS exchange and the renewal before expiry are unchanged.

## Vault (`config.GetVaultConnection`)

`vault-auth = jwt` logs in at `auth/jwt/login` with the same identity token; the role is `vault-role-id`
when set, else the identity client id (one role per instance on the Cloud18 vault, `vault.cloud18.io`,
OpenBao). AppRole stays the default and the way to use one's own vault. The KV layout read is the same
(`vault-mode = config_store_v2`, mount `vault-mount`).

## Switch, no setting

There is no setting to keep the GitLab login for the storages: `cloud18-sso-client-id` /
`cloud18-sso-client-secret` are removed. The servers accept both during the fleet upgrade: the Cloud18
MinIOs have two OpenID providers (GitLab and Keycloak, two roles, the same policy), so an instance on the
previous release keeps working until it upgrades. Once the fleet has upgraded, the GitLab provider is
removed from the storages.

## Not here

- The BO creating each instance's client (scope `cloud18-storage`, service account `gitlab_id`) and its
  vault jwt role and policy, and pushing the client id and secret.
- `private_key_jwt` client authentication (a key generated by the instance) instead of a secret.
- The other uses of the GitLab password (`cloud18-gitlab-password`), to remove before the setting is:
  - registration (`server/api_register.go`) and the Cloud18 connect from the settings
    (`api_global_settings.go`), which store it;
  - `Config.CreateGitlabProjects` (`config/config.go`), through a GitLab basic-auth token;
  - the peer credentials (`server/server.go`, `SetPeerCredentials`) and the peer login check
    (`server/api.go`, "Peer does not have cloud18 setup");
  - the Cloud18 git user added to the cluster's users (`server/server.go`, `AddCloud18GitUser`);
  - the admin-login support chat (`server/api.go`), the Cloud18 user's password (`server/api.go`);
  - Cloud18 infra and MCP logins (`server/server_cloud18_infra.go`, `server/server_cloud18_mcp.go`).
