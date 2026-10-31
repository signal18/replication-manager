# Cloud18 login without GitLab's password grant (#2182)

## Why

GitLab ends its password grant (ROPC). The registration no longer needs it (`CLOUD18_REGISTRATION.md`),
but the **login** did, in three places:

- `/api/login` (dashboard login form, CLI, API): after a failed local login, the email and password were
  checked with GitLab's password grant (`githelper.GetGitLabTokenBasicAuth`), synchronously and in the
  async SSO upgrade (`runAsyncSSOUpgrade`);
- acting on another Cloud18 infrastructure ("Enter" on a peer, `DynamicPeerHandler`; the MCP tools,
  `peerIdentityFor` / `peerLoginAs`): the session carried the user's GitLab password, encrypted, replayed
  to the other instance's `/api/login`, which checked it with the same grant;
- subscribe (`handlerMuxClusterSubscribe`) re-checked the GitLab password.

A Cloud18-only account (no GitLab password: a new sign-up, the website assistant) could not log in at all.
Goal: **no GitLab password anywhere in repman**; the GitLab token stays, for git only.

## Login with the Cloud18 account (`server/api_login_cloud18.go`, `ssoPasswordLogin`)

`/api/login`, the async SSO upgrade and subscribe check a name and password: local account (unchanged),
then the **Cloud18 account** (`config.Cloud18Login`). GitLab's password grant is no longer tried: the
GitLab passwords were moved into the realm (2026-10-26, bcrypt hashes copied, re-hashed by the realm at
each person's next login), and the `cloud18-gitlab-password-grant` switch is gone. The error is the Cloud18 one; `classifySSOAuthError`
reads `ErrCloud18BadCredentials` as `credential_mismatch` and `ErrCloud18AccountNotReady` as
`not_registered`. One failed login counts once for the lockout.

`config.Cloud18Login` (`config/cloud18_login.go`) is the registration's walk (`cloud18GitLabWalk`: GitLab's
OAuth with PKCE, "Sign in with Cloud18", the realm's login form), which gives the GitLab token, then, with
the realm session the walk left in its cookies, the realm's authorization for the login client
`replication-manager-login` (PKCE, redirect `http://127.0.0.1/cloud18-login`, never opened): the realm
answers at once with a code, traded for a **realm token** and its refresh token. No second password post.
A realm without that client still logs the person in (GitLab token only; the other instances then get the
password as before).

The session JWT keeps today's SSO shape (`profile`, `email`, `token` = the GitLab token), so the ACL and
self-service are unchanged, plus `cloud18`: the refresh token, encrypted with the instance key.
meet logs in with the walk's GitLab session (`meethelper.CreateMeetUserClientWeb`), no password; the
meet token is now per login (the package variable it used could hand one login another's token).

## A realm token between instances

The receiving instance verifies a realm token by itself (`config.VerifyCloud18Token`): signature against
the realm's published keys (`/protocol/openid-connect/certs`, go-oidc `RemoteKeySet`), issuer =
`cloud18-identity-url`, expiry, **audience `replication-manager-api`** (a token minted for another realm
client, the S3 storage, GitLab, an instance's own identity, is refused), and a verified email. The person is
the email, through the same ACL as an SSO login (no account on the instance: no access; a local-only
account of that name is never taken by an SSO login, `IsLocalOnlyAccount`).

Taken only on a **registered Cloud18 instance** (`acceptsCloud18Tokens`: `cloud18` on and the bearer's
issuer is the realm): an instance not registered to Cloud18 never trusts the realm for its users.

- `/api/login` with `Authorization: Bearer <realm token>` and an empty body (`{}`): the session of the
  token's person (`cloud18TokenLogin`).
- Every other route (`cloud18TokenMiddleware`, on both routers, before any token check): the request
  goes on with the SSO session of the token's person (`cloud18TokenSession`: one session JWT per realm
  token, cached until the token ends, bounded), so the token middleware, the routes that check the token
  themselves (`isValidRequest`, `IsValidClusterACL`: `/api/clusters`, `/api/peers`…) and the MCP all read
  an SSO login. A refused token is a 401 with a generic text, the detail in the security log. The session
  keeps the realm token encrypted (`cloud18_token`) for the next instance.
- Forged tokens (F4): an expired token, or one naming a key id the realm does not publish, is refused
  before any request to the realm, and an unknown key id refetches the realm's keys at most once a minute
  (`cloud18KeyKnown`, never holding its lock during the fetch); a refused token is answered from memory
  for a minute (bounded); the security log takes at most 30 refusals a minute, then one line saying the
  rest are not logged.
- Realm out of reach: a failed key fetch keeps the keys already known and is retried after 10 s; it
  answers `ErrCloud18KeysUnavailable`, which is not a refusal, so the token is not remembered as refused
  and the next request tries again. A key the realm stops publishing stays known here until a refetch;
  the signature check (go-oidc, same certs URL) stays the authority.
- Another instance (`peerLoginAs`, `DynamicPeerHandler`): logged into with the caller's realm token
  (`peerLoginAsIdentity`), got from the session (`cloud18TokenFor`: the token it came with, or one
  refreshed from its `cloud18` refresh token, one refresh per token lifetime, `Cloud18LoginAccess`). The
  password is sent only when that instance refuses the token (an older release) and the session has one.

## Realm setup

- Client `replication-manager-login`: public, standard flow, PKCE S256, redirect URI
  `http://127.0.0.1/cloud18-login`, no consent.
- Audience `replication-manager-api` in its access tokens (an Audience mapper), and in the tokens of any
  other client allowed to act as a person on the instances (the website assistant's client).
- The device-login client `replication-manager` (an instance's own identity) does **not** get that
  audience.
- Refresh tokens must be reusable (Keycloak's "Revoke Refresh Token" off, its default): the session
  keeps the first refresh token of the login. The newest one the realm gives is kept in memory
  (`Cloud18LoginAccess`), but an entry rebuilt after a restart or an eviction starts from the session's;
  with a rotating realm that one is revoked, and the session goes on with the password until the person
  logs in again. A refresh token the realm refuses (`invalid_grant`) is not sent again for a minute.
- The password goes to another instance only on a 401 to the token login (an older release; or a token
  that ended on the way): never on a 403, a 429, a 5xx or an unreachable instance.

## Second factor: the authentication level (`cloud18-login-acr`)

A password proves only that someone knows it; a one-time code (an authenticator app) proves the person
holds the device. The realm gives the second factor (Keycloak's OTP, enrolled once by scanning a QR code);
repman decides on each instance which level a Cloud18 login must reach, with the OpenID Connect `acr`:

- `cloud18-login-acr` (scope server, empty by default: any level) names the accepted levels, comma
  separated, e.g. `mfa` (password and one-time code).
- Repman asks for them: `acr_values` in the browser sign-in (`Cloud18SSOAuthorizeURL`) and in the realm
  token the Sign in form gets (`cloud18LoginToken`). The realm then asks the person for the code.
- The request proves nothing (anyone can remove a URL parameter): `VerifyCloud18Token` refuses a token
  whose `acr` claim is not one of the levels. Every path taking a realm token goes through it: the
  browser sign-in, a bearer at the API and the MCP, `/api/login` with a token, the realm token a peer sends.
- The Sign in form posts a password only and cannot answer the code page: with a level set, it refuses
  the login (`ErrCloud18SecondFactor`, "use Sign in with Cloud18") when the realm shows the code page or
  gives a token below the level, and never falls back to the password for the other instances.
- With a level set, ANY failure to get the realm token fails the whole login, even though the GitLab
  part succeeded: a realm outage or a missing login client looks like a total sign-in failure (the
  error says its real cause, not "second factor"). Without a level the old fallback stays.
- The code page is found by its Keycloak theme id (`kc-otp-login-form`, also reported by the GitLab
  walk with no level set). A custom theme or another second factor (WebAuthn, recovery codes) gives
  a generic message instead; the login still fails closed, the token check refuses it.
- The `acr` claim is compared literally, as a string, as the Cloud18 Keycloak realm sends it: another
  provider sending a URN, `0` or an array does not match.
- Each instance judges with its own `cloud18-login-acr`: in a fleet where peers differ, a realm token
  accepted by one can be refused by another.

Realm side, once for the realm (no client per repman): in the browser flow, the "Conditional - Level Of
Authentication" steps (level 1 password, level 2 password and OTP), and on the client
`replication-manager-login` the ACR to LoA mapping (`mfa` -> 2) and the `acr` claim in its access tokens
(client scope `acr`). Check on a token that `acr` reads `mfa` before setting `cloud18-login-acr`.

## Rollout (step 3, later)

Older releases keep logging in with GitLab's password grant as long as GitLab accepts it. Once the fleet
runs this release: (done: the switch `cloud18-gitlab-password-grant` is removed, the passwords are in the realm) drop the encrypted password from the session
(it is kept now only for instances on an older release), then disable the grant on GitLab. Rule kept from
#2159: GitLab is not upgraded past the password grant before the fleet runs this release.

Off-switch: with `cloud18-identity-url` empty, no Cloud18 login and no realm token is taken.

## Tests

`server/api_login_cloud18_test.go` on the registration's fake GitLab and realm pages
(`server_cloud18_register_test.go`, extended with the realm's login client, its token endpoint and keys):
a Cloud18 login (GitLab token, realm token verified, refresh token kept encrypted), a wrong password with
the grant off, a realm without the login client; realm tokens refused for a foreign audience, an unverified
email, an expired token, another key, another realm, an unregistered instance; the middleware, `/api/login`
and the MCP with a realm token; a peer on this release (token) and on an older one (password); one refresh
per token lifetime; `TestCloud18LoginACR`: the level asked and checked, a token below it refused, the
form refused on the code page and on a password-level token. `TestPeerIdentityFor` covers the realm token in the peer identity.
