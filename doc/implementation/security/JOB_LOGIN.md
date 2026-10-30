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

## Streams to the jobs listener

replication-manager sends three things to the jobs listener (`SST_RECEIVER_PORT`, socat on the
database side): a backup to restore (reseed, flashback), the jobs script (jobs upgrade) and a
config. The listener used to take the first connection, whoever made it: a stream sent there in
time was restored, or installed and run as the new jobs script. The database password is not in
the config, so neither side had a secret to check the stream with; the jobs key is that secret.

Every stream now starts with a header line (`ServerMonitor.JobStreamHeader`, written by the four
senders of `cluster_sst.go` before any data):

    MRMSTREAM1 <ts> <nonce> <hex HMAC-SHA256(SHA256(secret), "stream:<server>:<ts>:<nonce>")>

- **Secret:** the key of the server's last accepted job body (`OpenJobBody` records it with the
  name the script gives its server): the jobs key, else the server's password.
- **Script:** `job_stream_open` sits between socat and whatever takes the stream (the unpack tool,
  the script file, the config file). It reads the header and copies the rest only when the MAC
  matches, its timestamp is within 300 seconds (`JOB_STREAM_WINDOW`) and its nonce was not taken
  before (`<jobs dir>/.stream_nonces`, the nonces of twice the window: bounded). Otherwise nothing
  is written, the reason goes to the job's log, and the job fails: a restore is aborted before
  anything is unpacked, a new script is not installed, a config is not extracted.
- **Older scripts:** a server whose script posts legacy bodies gets no header (it would take it
  as data), and none either before its first body since replication-manager started (every run
  logs in first). The first jobs upgrade from an older script therefore still goes over the
  unchecked listener: it is the last one. Once WARN0272 is gone and `api-secret-login-legacy`
  is off, every stream is checked.
- **What it does not do:** it authenticates the stream, it does not encrypt it (that is
  `scheduler-receiver-use-ssl`). Someone connecting first still makes that transfer fail; the
  job reports why.

The streams the database sends to replication-manager (backups, logs, the jobs check) go the
other way, to a port replication-manager opens for the transfer, and are not covered yet.

## Not covered here

- **Certificate check.** The script talks TLS to `api-port` with `verify=0`: without
  `monitoring-ssl-cert`, replication-manager serves a self-signed certificate generated at each
  start, which a database server cannot verify. A man in the middle can still read a live token
  (15 minutes) or a body before it is used; it can no longer replay a body, and a token it
  steals expires. Pinning the certificate is a separate change.
- **The HTTP listener.** `secret-login` is an unprotected route, also served on `http-port`
  (bound to localhost by default). The script is only given `api-port`.
