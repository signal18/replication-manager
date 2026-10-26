# Sign in with Cloud18 on partner repmans (#2185)

## Why

Each repman address is its own site: a person signed in on one partner repman typed the password again
on the next. Partner repmans now let the Cloud18 realm (Keycloak) sign the person in, in the browser:
the first partner shows the realm's login page, every other one comes back at once thanks to the realm's
session. **No repman talks to another** for this: only the browser, the repman and the realm.

## Where (`config.Cloud18SSO`)

Any repman registered to Cloud18 with a public address, whatever its plan: `cloud18` on with a
registered user (`cloud18-gitlab-user`), `api-public-url` a public https address (not the loopback
default, not a private address), a realm (`cloud18-identity-url`), and `cloud18-sso-login` on (default).
A repman without a public address keeps the Sign in form, which checks the Cloud18 account since #2182.
`GET /api/auth/cloud18` tells the login page (`{"enabled": …}`).

## What the person sees (`share/dashboard_react/src/Pages/Login`)

One Sign in button and one Sign up button, nothing else.
- **Opening the login page** of a partner: a silent check at the realm (`prompt=none`, once per tab).
  Already signed in at Cloud18 in this browser (on another partner): straight into the dashboard, no
  page, no password. Not signed in: the realm answers `login_required` and the page shows its form.
- **Sign in**: the page asks the name first. `POST /api/login/method` answers `password` for an internal
  repman account (a name that is not an email: admin, system, local users; or an email that is an
  administrator account other than the registered user) and the password field appears, checked by
  `/api/login` as before; it answers `cloud18` for any other email (a local password alone does not make
  an email internal: repman gives one to every user it adds, SSO users included), and the browser goes to the realm's page with the email prefilled
  (`login_hint`): one password, typed there once for every partner.
- **Sign up**: the realm's own sign-up form (`/registrations`), instead of the GitLab one.
- **Logout**: the session here, then the realm session (every partner forgets the person). That is also
  how to come back to an internal API user: Logout, then type its name. A repman without Sign in with
  Cloud18 shows the Sign in form as before (internal account, then the Cloud18 password, #2182).

`/api/login/method` tells whether an email is an administrator account (whatever its case): a way to
probe administrator emails, limited to them (any other email gets `cloud18`, a name without `@` always
`password`), accepted for the single
Sign in button; same origin only (no CORS header), so another site cannot probe from a visitor's browser.
It reads the API users without a lock, like every login route: `APIUsers` is copy on write
(`setAPIUser`, `deleteAPIUser`, `LoadAPIUsers` under `apiUsersMu`, grant and role maps copied before a
change), so a reader never meets a map being written, which would stop the process.

The password field appears only after the name, so a password manager does not prefill it for an
internal email-style account. A tab that opened with an expired session skips the silent check until
it is reloaded (the session is cleared, the form shows).

## Flow (`server/api_auth_cloud18.go`)

1. **Start** `GET /api/auth/cloud18/start` (`login_hint`, `silent=1`, `signup=1`): a random `state`, a PKCE verifier and a nonce kept here
   (5 min); the nonce also goes to the browser in the cookie `repman_cloud18_sso` (HttpOnly, Secure,
   SameSite=Lax, path `/api/auth/cloud18`), which ties the sign-in to this browser (login CSRF: someone
   else's callback URL opened in another browser finds no matching cookie). The browser is redirected to
   the realm's authorization of the client `replication-manager-login`, redirect
   `<api-public-url>/api/auth/cloud18/callback`.
2. The realm shows its login page, or answers at once when the person already has a realm session.
3. **Callback** `GET /api/auth/cloud18/callback`: the `state` must be one this repman started, used
   once, and the browser's cookie must carry its nonce; a silent check the realm answers with
   `login_required` (or another "needs the person") goes back to `/login?cloud18_silent=none`, not a
   failure; any other realm error goes back to the login page with a reason; the code is traded with the verifier; the realm
   token is verified as any realm token (#2182: keys, issuer, expiry, audience `replication-manager-api`,
   verified email); the SSO session of the email is issued (same shape and rights as an SSO login, the
   realm token and its refresh token encrypted in it). **No token in any address**: the browser goes to
   `/login?cloud18=<one-time code>` (60 s, bounded).
4. **Session** `POST /api/auth/cloud18/session {code}`: the dashboard trades the code for the session,
   once; it keeps `login_method = cloud18`.
5. **Logout**: the dashboard drops its sessions as before, then, for a Cloud18 session, goes to
   `GET /api/auth/cloud18/logout`, redirected to the realm's logout (`client_id`,
   `post_logout_redirect_uri = <api-public-url>/login`): the realm session ends, every partner asks again.
   Without an `id_token_hint` the realm asks the person to confirm the logout.

Refusals (unknown or reused state, another browser, realm error, refused token, unknown/used/expired
code) go to the security log (`api_auth_failure`, user `cloud18-sso`, with the detail) and back to the
login page with one fixed reason (`expired`, `refused`, `token`, `busy`, `failed`); the login page shows
its own text for each, never text from the address.

Bounds (F4): at most 1024 sign-ins in progress, a new start is then refused (`busy`) rather than dropping
the ones in progress; at most 1024 one-time codes, the oldest goes; ended entries are dropped. Someone
calling `/start` in a loop can keep the store full, refusing Sign in with Cloud18 to everyone for that
time (the Sign in form is not affected); there is no per-address limit in repman to reuse. The refusals
share the security-log bound of the realm-token refusals (30 lines a minute, text cleaned and capped).

The former GitLab OAuth callback (`/api/auth/callback`), unused by the dashboard, is removed with its
access token and its daily refresh of the instance's GitLab token (it ran only after a login through
that callback since the last start). Checked 2026-10-26: one fleet instance had a GitLab OAuth
application set (`api-oauth-client-id`), and GitLab's retained logs show no login through it.

## Realm setup

The client `replication-manager-login` lists, for each partner, the redirect URI
`https://<partner public address>/api/auth/cloud18/callback` and the post-logout redirect URI
`https://<partner public address>/login`. Signal18 adds them when a partner is set up.

## Tests

`server/api_auth_cloud18_test.go` on the registration's fake realm: the scope rule (switch, registration,
plan, loopback, private address, http, no realm); the round trip (start, the realm's code, callback with
the state used once and a one-time code never a token, the session read as the SSO login of the email,
the code used once, logout to the realm); refusals (unknown state, realm error, expired code, a repman
not in scope).
