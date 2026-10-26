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
then the **Cloud18 account** (`config.Cloud18Login`), then GitLab's password grant while
`cloud18-gitlab-password-grant` is on (default during the rollout: accounts still on a GitLab password;
their GitLab and Cloud18 passwords may differ). The error is the last one tried; `classifySSOAuthError`
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
- The API middleware (`validateTokenMiddleware`) and the MCP (`AuthenticateMCP`): the request goes on with
  the SSO session of the token's person (`cloud18TokenSession`: one session JWT per realm token, cached
  until the token ends, bounded), so every handler and ACL check reads it as an SSO login. The session
  keeps the realm token encrypted (`cloud18_token`) for the next instance.
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

## Rollout (step 3, later)

Older releases keep logging in with GitLab's password grant as long as GitLab accepts it. Once the fleet
runs this release: switch `cloud18-gitlab-password-grant` off, drop the encrypted password from the session
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
per token lifetime. `TestPeerIdentityFor` covers the realm token in the peer identity.
