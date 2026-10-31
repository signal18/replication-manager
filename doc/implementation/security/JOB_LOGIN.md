# Job login: the one-time job body

The database jobs script (`share/scripts/dbjobs_new.sh`, served by replication-manager to each
database server) talks to the API in two ways:

- **Job bodies:** `secret-login`, job state, tasks, config receiver, jobs upgrade, `write-log`.
  The body is `{"data": <base64>, "iv": <hex>, "key": "jobs"|"db"}`: a JSON object encrypted
  with AES-256-CBC, the AES key being SHA-256 of the cluster's **jobs key**, or of the server's
  password when the script has no jobs key (`ServerMonitor.OpenJobBody`,
  `cluster/cluster_sec_job_login.go`).
- **Bearer token:** the token `secret-login` returns, for the other calls (DBU sensor, config
  fetch, ...).

## The jobs key

The login and the job bodies do not depend on the database password: they use the cluster's
jobs key, `GetJobsKey` = hex(HMAC-SHA256(SecretKey, "jobs:" + cluster)), derived from
replication-manager's persistent key (`monitoring-key-path`), never stored.

- **Per cluster**, unlike the `system` key the app and proxy sensors log in with
  (`GetSystemAPIKey`, the same for the whole replication-manager): on a shared
  replication-manager, a database container of one cluster cannot write the job bodies of
  another cluster's servers. Within a cluster, the clear object names its server (`server`,
  its URL, host:port or ip:port) and a body naming another server is refused.
- **Delivery, OpenSVC:** published as `env/JOBS_API_KEY` in the namespace's `env` secret
  (`openSVCPublishJobsKey`, at provision and before each definition refresh, so no definition
  ever maps a key the secret does not hold), and mapped into the jobs container's shm secret
  volume `{name}-sec` (`/credentials/JOBS_API_KEY`, mode 600, user 99) next to the database
  password. A file read at use time, not an environment variable (frozen at container start,
  shown by `docker inspect`).
- **Delivery, on-premise (ssh):** exported in the ssh environment (`GetSshEnv`,
  `JOBS_API_KEY`) that precedes the script at each run; nothing is written on the host.
- **Script:** `jobs_key` reads `$JOBS_API_KEY`, else `/credentials/JOBS_API_KEY`. With a key,
  the bodies carry no `secret` (`job_secret`) and `secret-login` accepts them without it: the
  database password never travels, and rotating it does not touch the login. Without a key
  (no persistent key, or a service not refreshed yet), the bodies use the database password and
  carry it (`"key": "db"`).
- **Rotation:** the key changes when replication-manager's key rotates. No previous version is
  accepted yet and the script does not fall back on the database password: until a container
  gets the new key (OpenSVC refreshes the shm volume from the secret; on-premise it comes with
  the next run), its bodies are refused. Accepting the previous key for a while belongs to key
  rotation (#2232).

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

## The jobs token: the jobs grants only

The token `secret-login` returns does not name the `system` account (which holds `db proxy` and
the sensor grant: every db-* grant, backups, restores, maintenance included). It names the
cluster's **jobs principal**, `token:jobs:system` (`EnsureJobsPrincipal`), the API-token
mechanism of #1835 without a stored token:

- grants: `db-jobs` (needs, write-log, receive-task, job-state, jobs check and upgrade),
  `db-config-flag` (config-receiver, config-dummy-sender) and `cluster-resource-sensor` (dbu),
  intersected with what `system` holds at each check (`GetACLUser`);
- scope: its cluster only;
- registered at cluster start and at each secret-login: one per cluster, in memory, so a restart
  or a reload never leaves a valid token without its principal;
- the JWT carries no password (it used to carry the `system` password, encrypted).

`IsValidClusterACL` runs a JWT naming a token principal as a token (grants, cluster scope). The
global ACL and the MCP authentication look the name up as a user and refuse it: a jobs token
reaches nothing outside its cluster's job endpoints. This holds for legacy bodies too: an older
script calls the same endpoints.

## The `system` account: its grants exactly

The `system` service account itself holds only what the machine callers do with it
(`SystemServiceGrants`): `cluster-resource-sensor` (the app, proxy and database sensors),
`db-jobs` (get the tasks to do, report their results) and `db-config-flag` (get the config).
Every load sets it to exactly that in memory (`reconcileSystemServiceGrants`, which also keeps
it from ever holding a token grant), and `EnsureSystemServiceUser` saves it in the cluster ACL
entry. An older secret-login created it with `db proxy`: every db and proxy action.

## Legacy format

`{"data": <base64>}`: the IV derived from the password (MD5), no timestamp, no nonce. The body
is the same at every run and can be replayed. It is what scripts before this change send.

There is no setting for it: the transition is the script upgrade itself, decided per server, in
memory (`OpenJobBody`, `legacyAllowed`):

- **Accepted for one thing only, fetching the new script:** the jobs check and jobs upgrade
  endpoints (`receive-jobs-check`, `send-jobs-upgrade`, through `SecretLoginCheckJobsUpgrade`).
  A replay of it can only make replication-manager send that server the new script.
- **Refused everywhere else**, `secret-login` included: an older script gets no token at all,
  so no token outlives `api-secret-login-token-timeout` (15 min) any more; its job states,
  tasks and logs are refused until the new script runs.
- **Refused everywhere once the new script answers:** its first one-time body since start
  closes the legacy body of that server (`jobStream`). Not when the script is sent: a send that
  lands on another listener (an older script's reseed receiver) would leave the server refused
  everywhere with no way to upgrade.
- Each use, accepted or refused, records the time on the server (`legacyJobLoginAt`);
  `CheckSecretLoginLegacy` (every tick) opens the security state **WARN0274** naming the servers
  seen within 10 minutes. It closes by itself once they run the new script.
- After a restart, the memory is empty: a server still on an older script can again only fetch
  the new script. Nothing else is reachable with its body.

On OpenSVC clusters, a replication-manager start flags every server for a jobs upgrade
(WARN0155, `SetRollingJobsUpgradeState`), so the scripts move to the one-time format at the
first start of the new release.

## Streams between replication-manager and the jobs

The data does not go through the API: it goes over raw TCP (socat on the database side).
- **To the database:** replication-manager sends to the jobs listener (`SST_RECEIVER_PORT`)
  a backup to restore (reseed, flashback, PostgreSQL restore), the jobs script (jobs upgrade)
  and a config.
- **To replication-manager:** the jobs send to a port replication-manager opens for the
  transfer: a backup, a remote dump, a collected log, the jobs check.

Each listener used to take the first connection, whoever made it. A stream sent there in time
was restored, installed and run as the new jobs script, or stored as a backup. The database
password is not in the config, so neither side had a secret to check the stream with. The
jobs key is that secret.

Every stream now starts with a header line (`utils/jobapi/stream.go`; `job_stream_mac` in the
script):

    MRMSTREAM1 <ts> <nonce> <hex HMAC-SHA256(SHA256(secret), "<direction>:<server>:<ts>:<nonce>")>

- **Secret:** the key of the server's job bodies: the jobs key, else the server's password.
  replication-manager learns it from the server's one-time bodies, with the name the script
  gives its server (`OpenJobBody`, `jobStreamAuth`).
- **Direction:** `to-db` or `to-repman`, so a header sent one way cannot be replayed the other.
- **To the database:**
  - The four senders of `cluster_sst.go` write the header before any data.
  - `job_stream_open` sits between socat and the consumer. It copies the rest only when the
    MAC matches, the timestamp is within 300 seconds (`JOB_STREAM_WINDOW`) and the nonce is
    new (`<jobs dir>/.stream_nonces`, twice the window: bounded).
  - Otherwise nothing is written: the restore aborts before unpacking, the new script is not
    installed, the config is not extracted, and the job logs why.
  - The PostgreSQL sidecar does the same with `replication-manager-cli stream --listen --job`.
- **To replication-manager:**
  - The script signs with `job_stream_signed`; the PostgreSQL sidecar with `stream --to --job`.
  - The receivers (`tcp_con_handle_to_file`, `_to_gzip`) check it with `acceptJobStream`:
    MAC, window, and the nonce in the job body store.
  - A refused connection is closed, and the receiver waits for the next one until its
    deadline. Whoever connects first can neither write the backup nor block the real transfer.
  - Each refusal opens the security state **WARN0275** for 10 minutes, naming the server, the
    task and the source address.
- **Older scripts:**
  - A server whose script posts legacy bodies gets no header and is not asked for one. Its
    first jobs upgrade still goes over the unchecked listener, and it is the last.
  - Once a server has posted a one-time body, a legacy body for it is refused (a replay could
    otherwise downgrade it) until replication-manager restarts.
  - Once WARN0274 is gone, every server's script signs and every stream is checked.
- **PostgreSQL:** `utils/jobapi` posts the one-time body too, with the jobs key when the
  container has one (`JobsKeyFromEnv`): the PostgreSQL jobs never depend on the legacy body.
- **What it does not do:** it authenticates the stream, it does not encrypt it (that is
  `scheduler-receiver-use-ssl`). Repeated connections from an attacker can still delay a
  transfer until the receiver's deadline.

## Not covered here

- **Certificate check.** The script talks TLS to `api-port` with `verify=0`: without
  `monitoring-ssl-cert`, replication-manager serves a self-signed certificate generated at each
  start, which a database server cannot verify. A man in the middle can still read a live token
  (15 minutes) or a body before it is used; it can no longer replay a body, and a token it
  steals expires. Pinning the certificate is a separate change.
- **The HTTP listener.** `secret-login` is an unprotected route, also served on `http-port`
  (bound to localhost by default). The script is only given `api-port`.
