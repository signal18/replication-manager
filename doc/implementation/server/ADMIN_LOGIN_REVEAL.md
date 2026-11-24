# The admin login by one-time link

*Status: implemented (`server/api_admin_login_reveal.go`, on the one-time secret links of
`server/server_secret_reveal.go`, see `SECRET_REVEAL.md`).*

## Why

The admin zone (global settings) takes the **local `admin` account only**
(`isGlobalSettingsAdmin`, #2262): never a Cloud18 login, whatever its grants. An instance's sysops
who signs in with Cloud18 and does not hold the local admin password is locked out of it, and
nothing in repman gave it. This gives it, to the right people only, without ever putting it in a
mail, an API answer or a log.

## Who

- **The instance's sysops and extsysops, signed in with Cloud18**: a session whose `AuthType` is
  `SSO`, its login an email, holding the server credentials grant (`cluster-server-credentials`) on
  **every** cluster of the instance (`serverCredentialsACL`, the rule of the server credential
  stores). The grant comes from the role (sysops, extsysops), never from a person: a sponsor, dbops
  or extdbops does not hold it.
- Refused: a local login (the admin included: it already has the login), an API token, a service
  token, a session scoped to some clusters, a holder of the grant on some clusters only.

## Flow

1. **Ask** — the user profile's **Send me the admin login** button:
   `POST /api/clusters/settings/actions/admin-login-reveal/send`, no body. The recipient is the
   email **of the session** (its JWT claims), never an address from the request. The answer is the
   masked address (`j***@acme.example`) and the expiry: never the value, never the link.
2. **Mail** — `mailSecretRevealOf`, the mail of every one-time link: `<api-public-url>/reveal#<code>`,
   the code in the URL fragment (never sent to a server or a proxy, so in no access log). The store
   (`secret-reveals.json`, encrypted with the instance's key) keeps the SHA-256 of the code, the
   person, the kind `admin-login` and the expiry: **no secret**.
3. **Open** — the `/reveal` page asks the public route first (`POST /api/secret-reveal`), which
   refuses a link of a kind without burning it (403, header `X-Repman-Reveal-Session`). The page then
   posts the code to `POST /api/clusters/settings/actions/admin-login-reveal/open` with the browser's
   session; no session in this browser: it says to sign in with Cloud18 and open the link again.
   The open checks, in this order: a Cloud18 session holding the grant on every cluster **now** (a
   role removed since the request is refused), the link's kind, the same email as the link's person,
   the admin login in force. Any refusal keeps the link for its person; an expired, used or unknown
   link is 404 (the same answer for the three). The link is deleted at the first successful open
   only.

## The value

`instanceAdminLogin` reads the admin's password **when the link is opened**, from the instance's list
(`api-credentials`, `DecryptedAPIUserList`, through the credential chain): the list the admin zone
takes (`isInstanceCredential`). It is shown only when a cluster's loaded users hold the same
password (what `cluster.IsValidACL` compares at the login): a list just changed and not loaded yet
answers 409 (try again), never a value that would not log in. A rotation between the request and
the open shows the new password. **A reveal never rotates anything.**

## Bounds and events

- Single use, `api-secret-reveal-admin-login-expire-minutes` (15), the store's bound (256 reveals,
  expired ones dropped at every write).
- Rate limit: `api-secret-reveal-admin-login-max-per-hour` (5) links per person in a sliding hour,
  in memory, keyed on the email without case; a request whose mail was not sent (no mail server,
  store error) is given back. The requests older than an hour are dropped at every call and the map
  holds at most 1024 people (F4, T18): beyond, another person's count restarts (only the instance's
  sysops and extsysops reach it, far fewer).
- The open reads the value before taking the reveal store's lock; the check under the lock only
  compares (no other reveal waits on the credential chain or the clusters).
- Security log (`logSecurityEvent`): `admin_login_reveal_requested`, `_mailed`, `_opened`,
  `_refused` (with why), and the aggregated `secret_reveal_refused` for unknown, expired or used
  codes. Never the value, never the code (F10). `Cache-Control: no-store` on both answers.

## Trade-off

A Cloud18 sysops or extsysops can now reach the local admin password: a compromised Cloud18 account
together with its mailbox gives the admin login. Both are needed (the link opens only in the
person's own Cloud18 session, not by the mail alone); the switch turns the path off for an
instance that does not want it, and the user doc says so.

## Settings (server scope, global settings)

- `api-secret-reveal-admin-login` (default on): the switch (T14). It needs `api-secret-reveal`, an
  encryption key, a mail server and `api-public-url`.
- `api-secret-reveal-admin-login-expire-minutes` (default 15).
- `api-secret-reveal-admin-login-max-per-hour` (default 5).

## ACL

`/api/clusters/settings/actions/admin-login-reveal` maps to `cluster-server-credentials` in
`globalSettingsACLRules`, checked on every cluster.
