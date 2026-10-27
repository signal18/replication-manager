# Sign in with Cloud18 on registered public repmans (#2185)

## Why

Each repman address is its own site: a person signed in on one repman typed the password again on the
next. Registered repmans with a public https address now let the Cloud18 realm (Keycloak) sign the
person in, in the browser: the first one shows the realm's login page, every other one comes back at
once thanks to the realm's session. **No repman talks to another** for this: only the browser, the repman and the realm.

## Where (`config.Cloud18SSO`)

Any repman registered to Cloud18 with a public address, whatever its plan: `cloud18` on with a
registered user (`cloud18-gitlab-user`), `api-public-url` a public https address (not the loopback
default, not a private address), a realm (`cloud18-identity-url`), and `cloud18-sso-login` on (default).
A repman without a public address keeps the Sign in form, which checks the Cloud18 account since #2182.
`GET /api/auth/cloud18` tells the login page (`{"enabled": …}`).

## What the person sees (`share/dashboard_react/src/Pages/Login`)

Three buttons on a repman in scope; any other repman (not registered, or without a public address) shows
only Sign in:
1. **Sign in**: name and password on repman's page, as before (an internal API user first, then the
   Cloud18 password checked by repman, #2182). Signs the person in on this repman only: a password typed
   in a repman form gives the browser no Cloud18 session other sites could reuse.
2. **Sign in with Cloud18** (below an "or", without the fields above): the browser goes to the realm's
   page and signs in there, the email prefilled when one is typed above (`login_hint`, emails only, never
   a password: the realm's page only takes a password typed on it); the realm session it leaves lets
   every other repman in scope in without a page.
3. **Sign up with Cloud18**: the realm's own sign-up form (`/registrations`).

On opening the login page of a repman in scope, a silent check at the realm (`prompt=none`, once per
tab): a browser already signed in at Cloud18 is straight in with its identity; otherwise the realm answers
`login_required` and the page shows its three buttons. **Logout** ends the session here and, for a
session that came from the realm, the realm session too (every repman forgets the person): the way back
to an internal API user.

The copy on write of `APIUsers` (`setAPIUser`, `deleteAPIUser`, `LoadAPIUsers` under `apiUsersMu`, grant
and role maps copied before a change) keeps the login routes, which read it without a lock, from ever
meeting a map being written, which would stop the process.

## Flow (`server/api_auth_cloud18.go`)

1. **Start** `GET /api/auth/cloud18/start` (`silent=1`, `signup=1`): a random `state`, a PKCE verifier and a nonce kept here
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
   `post_logout_redirect_uri = <api-public-url>/login`): the realm session ends, every such repman asks again.
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

The client `replication-manager-login` lists, for each such repman, the redirect URI
`https://<public address>/api/auth/cloud18/callback` and the post-logout redirect URI
`https://<public address>/login`. Signal18 adds them when the repman is registered.

## Support chat of a Sign in with Cloud18 session

The chat (meet) signs people in through GitLab, and a Sign in with Cloud18 session gives repman neither
the password nor a GitLab session. Such a session opens the chat with the instance's own account
(`instanceMeetUser`, `cloud18-gitlab-user`), as a local login does, and carries `chat_as = instance`:
`PostMeetHandler` starts each message with the person's name and email from the verified realm token
(`chatSignature`), so support sees who writes. The person's own channels and direct messages are in the
chat app, or with Sign in (name and password), which opens their own account as before. Old releases
are unchanged: the chat stays on GitLab.

## Tests

`server/api_auth_cloud18_test.go` on the registration's fake realm: the scope rule (switch, registration,
plan, loopback, private address, http, no realm); the round trip (start, the realm's code, callback with
the state used once and a one-time code never a token, the session read as the SSO login of the email,
the code used once, logout to the realm); refusals (unknown state, realm error, expired code, a repman
not in scope).
