# Job login: the one-time job body

The database jobs script (`share/scripts/dbjobs_new.sh`, served by replication-manager to each
database server) talks to the API in two ways:

- **Job bodies:** `secret-login`, job state, tasks, config receiver, jobs upgrade, `write-log`.
  The body is `{"data": <base64>, "iv": <hex>}`: a JSON object encrypted with AES-256-CBC, the
  key being SHA-256 of the server's password. replication-manager decrypts it with the password
  it monitors the server with (`ServerMonitor.OpenJobBody`, `cluster/cluster_sec_job_login.go`).
- **Bearer token:** the token `secret-login` returns, for the other calls (DBU sensor, config
  fetch, ...).

## One-time format

| Part | Value |
|---|---|
| IV | random, 16 bytes, sent in clear as `iv` (32 hex) |
| Clear object | the request's JSON, stamped by the script with `"ts"` (unix seconds) and `"nonce"` (16 random bytes, hex) |

`OpenJobBody` accepts a one-time body once:

1. `ts` within `api-secret-login-window` seconds of now (default 300: the clock skew allowed
   between a database server and replication-manager);
2. `nonce` not seen within twice the window. The nonces live in one in-memory store for the
   process, bounded at 65,536 (F4): when full, expired nonces are purged; if it is still full,
   the body is refused with 503 (more bodies within the window than the store holds, an
   attack, never a normal load: a server posts a few bodies a minute).

A replay is refused with 401 and logged. A body cannot be forged without the password: it does
not decrypt to a JSON object.

`secret-login` with a one-time body returns a token valid `api-secret-login-token-timeout`
minutes (default 15). The script keeps it in `<jobs dir>/.job_token` (0600) so its background
tasks share it, and logs in again once it is older than 10 minutes (`job_token`), so a long task
(a backup) always posts with a valid token. Each run starts with a fresh login
(`job_token refresh`); a failed login stops the run.

## Legacy format

`{"data": <base64>}`: the IV derived from the password (MD5), no timestamp, no nonce. The body
is the same at every run and can be replayed. It is what scripts before this change send.

- `api-secret-login-legacy` (default `true`): accepted, so a server with an older script can
  still log in and fetch the new script (jobs upgrade). Its token keeps `api-token-timeout`
  (hours): an older script uses one token for a whole run.
- Each use records the time on the server (`legacyJobLoginAt`); `CheckSecretLoginLegacy` (every
  tick) opens the security state **WARN0272** naming the servers seen within 10 minutes.
- Once no cluster shows WARN0272, turn the switch off (GUI: Global Settings, **Accept Legacy Job
  Login**; it applies to every cluster at once). Off, a legacy body is refused (401): a server
  still running an older script cannot log in until its script is upgraded by hand.

On OpenSVC clusters, a replication-manager start flags every server for a jobs upgrade
(WARN0155, `SetRollingJobsUpgradeState`), so the scripts move to the one-time format at the
first start of the new release.

## Not covered here

- **Certificate check.** The script talks TLS to `api-port` with `verify=0`: without
  `monitoring-ssl-cert`, replication-manager serves a self-signed certificate generated at each
  start, which a database server cannot verify. A man in the middle can still read a live token
  (15 minutes) or a body before it is used; it can no longer replay a body, and a token it
  steals expires. Pinning the certificate is a separate change.
- **The HTTP listener.** `secret-login` is an unprotected route, also served on `http-port`
  (bound to localhost by default). The script is only given `api-port`.
