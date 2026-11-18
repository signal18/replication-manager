# Job login: the one-time job body

The database jobs script (`share/scripts/dbjobs_new.sh`, served by replication-manager to each
database server) talks to the API through `replication-manager-cli job` and `stream --job`, which
implement the protocol below with `utils/jobapi` (the script has no crypto of its own since #2405,
see `doc/implementation/share/scripts/DBJOBS_CLI.md`). Two kinds of calls:

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
- **Jobs side:** the CLI reads `$JOBS_API_KEY`, else `/credentials/JOBS_API_KEY`
  (`jobapi.JobsKeyFromEnv`). With a key, the bodies carry no `secret` and `secret-login` accepts
  them without it: the
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
| Clear object | the request's JSON, stamped by the CLI with `"ts"` (unix seconds) and `"nonce"` (16 random bytes, hex) |

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
minutes (default 15). The CLI logs in for each call that needs a token (`job usage`,
`job config`, `job config-receiver`, `job config-dummy`) and keeps nothing: no token is stored in
the jobs directory, and a long task never holds an expired one. The other calls carry their own
one-time body and need no token.

## The jobs token: the jobs grants, its own server

The token `secret-login` returns does not name the `system` account. It names the cluster's
**jobs principal**, `token:jobs:system` (`EnsureJobsPrincipal`), the API-token mechanism of
#1835 without a stored token:

- grants: `db-jobs` (needs, write-log, receive-task, job-state, jobs check and upgrade, and the
  config fetch: config-receiver and config-dummy-sender have their own `db-jobs` rule) and
  `cluster-resource-sensor` (dbu), intersected with what `system` holds at each check
  (`GetACLUser`). Never `db-config-flag`: that grant also reaches the config of any server,
  apply-dynamic-config, the database tags and the secret stores;
- scope: its cluster, and **its server**: the JWT names the server whose job body logged in
  (`Server`), and `IsValidClusterACL` refuses the token on any URL that does not name that server
  (`JobsTokenServerInScope`: another server's endpoints, cluster endpoints);
- registered at cluster start and at each secret-login: one per cluster, in memory, so a restart
  or a reload never leaves a valid token without its principal;
- lifetime `api-secret-login-token-timeout` (15 min);
- the JWT carries no password (it used to carry the `system` password, encrypted).

The global ACL and the MCP authentication look the name up as a user and refuse it: a jobs token
reaches nothing outside its server's job endpoints. This holds for legacy bodies too: an older
script calls the same endpoints.

## The `system` account: no session of its own

The `system` service account holds the grants its two principals narrow (`SystemServiceGrants`):
`db-jobs` and `cluster-resource-sensor`. Every load sets it to exactly that in memory
(`reconcileSystemServiceGrants`, which also keeps it from ever holding a token grant), and
`EnsureSystemServiceUser` saves it in the cluster ACL entry. An older secret-login created it with
`db proxy`: every db and proxy action.

No session acts as `system` itself:

- a password login of `system` (`/api/login`, the compute sensor of the proxies and apps with the
  repman-wide derived key) returns a token naming the **sensor principal**, `token:sensor:system`:
  `cluster-resource-sensor` only, for `api-secret-login-token-timeout` (15 min), not
  `api-token-timeout` (48 h). The sensor logs in before each push;
- `IsValidClusterACL` refuses a JWT naming bare `system` (a token issued by an older release).

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
  `CheckSecretLoginLegacy` (every tick) opens the security state **WARN0276** naming the servers
  seen within 10 minutes. It closes by itself once they run the new script.
- After a restart, the memory is empty: a server still on an older script can again only fetch
  the new script. Nothing else is reachable with its body.

On OpenSVC clusters, a replication-manager start flags every server for a jobs upgrade
(WARN0155, `SetRollingJobsUpgradeState`), so the scripts move to the one-time format at the
first start of the new release.

## Streams between replication-manager and the jobs

The data does not go through the API: it goes over raw TCP (`replication-manager-cli stream` on
the database side).
- **To the database:** replication-manager sends to the jobs listener (`SST_RECEIVER_PORT`)
  a backup to restore (reseed, flashback, PostgreSQL restore), the jobs script (jobs upgrade)
  and a config.
- **To replication-manager:** the jobs send to a port replication-manager opens for the
  transfer: a backup, a remote dump, a collected log, the jobs check.

Each listener used to take the first connection, whoever made it. A stream sent there in time
was restored, installed and run as the new jobs script, or stored as a backup. The database
password is not in the config, so neither side had a secret to check the stream with. The
jobs key is that secret.

Every stream now starts with a header line (`utils/jobapi/stream.go`, on both sides):

    MRMSTREAM1 <ts> <nonce> <hex HMAC-SHA256(SHA256(secret), "<direction>:<server>:<ts>:<nonce>")>

- **Secret:** the key of the server's job bodies: the jobs key, else the server's password.
  replication-manager learns it from the server's one-time bodies, with the name the script
  gives its server (`OpenJobBody`, `jobStreamAuth`).
- **Direction:** `to-db` or `to-repman`, so a header sent one way cannot be replayed the other.
- **To the database:**
  - The four senders of `cluster_sst.go` write the header before any data.
  - The listener is the CLI (`stream --listen --job`, `job upgrade`, `job config-dummy`:
    `Client.CheckStream`). It copies the rest only when the MAC matches, the timestamp is within
    300 seconds (`jobapi.StreamWindow`) and the nonce is new (a file in the temporary directory,
    twice the window: bounded).
  - Otherwise nothing is written and the CLI exits 3: the restore aborts before unpacking, the
    new script is not installed, the config is not extracted, and the job logs why.
- **To replication-manager:**
  - The jobs (the script and the PostgreSQL sidecar) sign with `stream --to --job`.
  - The receivers (`tcp_con_handle_to_file`, `_to_gzip`) check it with `acceptJobStream`:
    MAC, window, and the nonce in the job body store.
  - A refused connection is closed, and the receiver waits for the next one until its
    deadline. Whoever connects first can neither write the backup nor block the real transfer.
  - Each refusal opens the security state **WARN0277** for 10 minutes, naming the server, the
    task and the source address.
- **Older scripts:**
  - A server whose script posts legacy bodies gets no header and is not asked for one. Its
    first jobs upgrade still goes over the unchecked listener, and it is the last.
  - Once a server has posted a one-time body, a legacy body for it is refused (a replay could
    otherwise downgrade it) until replication-manager restarts.
  - Once WARN0276 is gone, every server's script signs and every stream is checked.
- **PostgreSQL:** `utils/jobapi` posts the one-time body too, with the jobs key when the
  container has one (`JobsKeyFromEnv`): the PostgreSQL jobs never depend on the legacy body.
- **What it does not do:** it authenticates the stream, it does not encrypt it (that is
  `scheduler-receiver-use-ssl`). Repeated connections from an attacker can still delay a
  transfer until the receiver's deadline.

## The init container: the config fetch with the jobs key

A database service's init container (OpenSVC `container#02`) fetches the server's generated
config before the database starts. It used to log in as `admin` with
`REPLICATION_MANAGER_PASSWORD` from the namespace's `env` secret: after an admin password change
that login failed, nothing was fetched, the bootstrap exited 0 and the database started on its
old files.

With the cluster's jobs key, the init container logs in like the jobs:
- **Definition** (`OpenSVCGetDBInitContainerSection`): the key alone is mounted read-only from
  the jobs secret volume (`{name}-sec/JOBS_API_KEY` at `/credentials/JOBS_API_KEY`); no
  `REPLICATION_MANAGER_USER`, no `REPLICATION_MANAGER_PASSWORD`. Without a persistent key the
  definition is unchanged (admin login). The proxies keep the admin login.
- **Bootstrap** (`share/dashboard/static/configurator/opensvc/bootstrap`, downloaded at each
  start, so older definitions get it too): picks the replication-manager that answers its public
  `/api/version` (main, then DR), downloads `replication-manager-cli` from `/static` (public,
  cached per version), and with a jobs key runs `replication-manager-cli job config
  config.tar.gz`. Without one it falls back to the admin login, as before.
- **CLI** (`job config`, `jobapi.Client.Config`): asks `need-config-fetch`, logs in with the
  one-time body (`secret-login`), downloads `<server>/<port>/config` with the jobs token, at most
  64 MB. Exit 0 written, 1 not wanted, 2 error.
- **Server** (`handlerMuxServersPortConfig`, `jobsTokenForServer`): the config download accepts a
  jobs token minted for the very server the URL names, GET only. No ACL rule is added: the
  matcher falls back to shorter patterns, so `db-jobs` on `/config` would also open `config-gen`
  and `config-path-preserve` to the token.
- **Trace**: the bootstrap writes `/bootstrap/init-status`, one line `<unix time> <fetched |
  not-needed | failed> <detail>`, for the database's jobs to report as a state.
- **The CLI download** (`/static/configurator/bin/replication-manager-cli`,
  `handlerStaticCli`): outside test mode `/static` comes from the dashboard embedded in the
  binary. A source build (`make cli pro`) copies the CLI into `share/dashboard` before the server
  is built, so its embed holds one; a release image builds the server first, so its embed has
  none and the URL answered 404. Every init then failed with "no replication-manager-cli to log
  in with the jobs key" and the database started on its old files (found on the first image
  instance of the v3.1.50 fleet deploy). The route is now registered before the `/static/` prefix
  in both routers (API and HTTP monitor) and serves the binary from disk, located by
  `config.Config.GetReplicationManagerCliPath` (`replication-manager-cli-path`, else beside the
  running executable, else `PATH`), the one lookup the backup and job code use too. Not found on
  disk: the embedded copy, 404 when there is none, with a log line naming where it looked. The
  binary (about 150 MB) is never added to the embed.

## Not covered here

- **Certificate check.** The CLI talks TLS to `api-port` without checking the certificate: without
  `monitoring-ssl-cert`, replication-manager serves a self-signed certificate generated at each
  start, which a database server cannot verify. A man in the middle can still read a live token
  (15 minutes) or a body before it is used; it can no longer replay a body, and a token it
  steals expires. Pinning the certificate is a separate change.
- **The HTTP listener.** `secret-login` is an unprotected route, also served on `http-port`
  (bound to localhost by default). The script is only given `api-port`.
