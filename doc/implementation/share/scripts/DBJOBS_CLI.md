# The jobs script talks to replication-manager through replication-manager-cli

`share/scripts/dbjobs_new.sh` makes every call and every stream to replication-manager through
one binary, `replication-manager-cli` (#2405). The script no longer implements the jobs protocol:
no `openssl`, no `socat`, no HTTP written by hand, no crypto in bash.

## Why

The script used to implement the protocol itself: AES-256-CBC job bodies and HMAC stream headers
with `openssl`, HTTP requests written to `socat`, TLS with `verify=0`. The same protocol exists in
Go (`utils/jobapi`), used by the CLI and checked by the server. Two implementations of one security
protocol drift apart (T2). The bash one also needs `openssl` and `socat` in the database image,
which stock MySQL and Percona Server images do not ship.

## Where the CLI comes from

- **OpenSVC:** the bootstrap downloads it from `/static/configurator/bin/replication-manager-cli`
  into the init directory, beside the script.
- **Kubernetes:** the init container copies it into `/docker-entrypoint-initdb.d`.
- **On-premise (SSH):** replication-manager copies it into the server's jobs directory before
  each run that needs it (see below).
- **The script:** it looks beside itself first (`$SCRIPT_DIR/replication-manager-cli`), then in
  the jobs directory (`$TMP_DIR`), then on the `PATH` (a CLI installed by hand).
- **Without the CLI:** `require_repman_client` writes one error on stderr and
  `<unix time> failed no replication-manager-cli ...` in `$TMP_DIR/jobs-status`, and the run
  stops. There is no fallback to another implementation.
- **How replication-manager sees it:** jobs that never reach it open **WARN0324** (the jobs of a
  server silent for 10 minutes); a dedicated state for "no CLI" is a follow-up.

## On-premise jobs over SSH: the CLI copied to the host

replication-manager pipes the script into a shell on the database host (`JobRunViaSSH`), so the
script has no directory of its own (`BASH_SOURCE` is empty). Before each run,
`ensureJobsCliViaSSH` (`cluster/srv_job_ssh_cli.go`) puts the monitor's CLI in the server's jobs
directory (`GetJobDatadir`, the script's `TMP_DIR`), over the same SSH connection:

1. **Check, one command:** `uname -sm` and the marker
   `<jobs dir>/replication-manager-cli.sha256`, which holds `<sha256> <CLI version>` of the copy.
   The CLI is never hashed on the host at each run: jobs run every minute on a production host.
2. **Platform:** the copy is the monitor's binary, so only a host of the monitor's own OS and
   architecture gets it. On a mismatch nothing is copied, and the run fails with an error naming
   both platforms. The error says the CLI can be installed by hand on the jobs user's `PATH`,
   which the script also looks at. Multi-architecture CLIs are a follow-up.
3. **Copy:** when the marker is missing or names another hash. The copy streams the file into one
   SSH session:
   - written to a temporary name in the jobs directory;
   - its sha256 checked against the monitor's (catches a full disk);
   - run once (`version`; catches a `noexec` mount);
   - made 755, then renamed over `replication-manager-cli`;
   - then the marker is written.

   A running job never sees a half-written CLI. The monitor's hash and version are computed once
   per file (path, size, modification time).
4. **Failure:** permissions, space, `noexec`, a checksum mismatch or another platform skip the
   run. The cause is logged once (task module, error) and kept in the server's `jobsCliError`
   (API). A failed copy is retried at most every 10 minutes per server, not at every run.

**The version in use:** every CLI call names its version in the User-Agent
(`replication-manager-cli/<version>`, `jobapi.Client.Version`). The jobs check and jobs upgrade
handlers record it in the server's `jobsCliVersion`, so a host running an older CLI shows.

## Identity

Every call passes `--url --cluster --server --port` (`JOBS_IDENTITY`):

- **URL:** `https://` on 443 and 10005, `http://` otherwise, as before.
- **Signing key:** the CLI signs with the cluster's jobs key, `$JOBS_API_KEY`, else
  `/credentials/JOBS_API_KEY`.
- **Without a jobs key:** it falls back to the database password from `$MYSQL_ROOT_PASSWORD`. The
  password stays in the environment, never in an argument (`ps`).

## Calls

| Script | CLI | Route |
|---|---|---|
| `send_lines_to_api` | `job log TASK LEVEL` (text on stdin) | `jobs-log-level`, then `write-log` |
| `check_task_needs` | `job needs TASK` | `needs` |
| `get_task_receiver` | `job receiver TASK` | `receive-task`: `host:port`, then `DUMP_ARGS=` and `LOCK_DDL=` for mysqldump |
| `report_job_state` | `job state TASK STATE [--restore JSON]` | `job-state`, the restore metadata in the body |
| `collect_dbu` | `job usage` (JSON on stdin) | `secret-login`, then `dbu` |
| `need_refresh_config` | `job config-refresh` | `need-config-refresh` |
| `fetch_config_receiver` | `job config-receiver` | `secret-login`, then `config-receiver` |
| `fetch_and_extract_config` | `job config-dummy FILE --listen :PORT` | `secret-login`, then `config-dummy-sender`, then the signed stream |
| `jobsCheck` | `job check FILE` | `receive-jobs-check`, then the signed stream |
| `jobsUpgrade` | `job upgrade FILE --listen ADDR` | `send-jobs-upgrade`, then the signed stream |

How the calls behave:

- **Exit codes:** 0 done (or yes), 1 no, 2 error, 3 a stream refused.
- **Retries:** `--retries N` repeats a state or log call 2 seconds apart, each time with a new
  one-time body. A state report gets 3 attempts, or 5 for `done` and `error`, as before.
- **CLI errors:** they go to `$LOG_DIR/api_calls.log`, which is rotated at 1 MB.
- **Log text:** `job log` encodes it as JSON itself, so the script no longer escapes lines. A
  quote in a log line used to break the body.
- **Listeners opened first:** `job upgrade` and `job config-dummy` open the listener before they
  ask replication-manager to send, so the sender never finds the port closed.
- **Port check:** `job config-dummy` refuses to wait when replication-manager names another port
  than the one it listens on.

## Upgrading a running container: deferred until the service restarts

A jobs upgrade sends the script only, never the CLI. The CLI of a container is the one its init
brought at the last service start. The current script would fail next to an older CLI, which
lacks `job log`, `job check`, `job upgrade` and `job config-*`. So:

- **The gate:** every CLI call names its version in the User-Agent, and the older script (bash
  over socat) names none. When `needs jobs-upgrade` comes without a CLI version
  (`handlerMuxServerNeeds`), replication-manager answers `false` and calls `DeferJobsUpgrade`.
  PostgreSQL servers are not gated: their script needs nothing new from the CLI.
- **Rolling upgrade:** the upgrade and rolling cookies are dropped, so the rolling jobs upgrade
  does not wait 5 minutes on that server.
- **WARN0325 (config domain, per server):** it names the server and the CLI version it saw
  ("none" for an older script or CLI). `CheckJobsUpgradeDeferred` re-opens it every tick.
- **Until the restart:** the jobs go on with their current script, through socat. This is why
  socat stays in the xtrabackup bundle for this release.
- **When it closes:** at the service restart, the init brings the new CLI and the new script
  together. The first call that names a CLI version (`JobsCliCalled`) closes WARN0325, and so does
  a delivered upgrade (`UpgradeJobsScript`).
- **After a monitor-only deploy:** every database server whose service has not restarted since
  the deploy shows WARN0325. On-premise SSH servers do not: their CLI is copied before each run.
- **Follow-up:** a live CLI upgrade over the jobs listener (#2405).

## Streams

| Script | CLI |
|---|---|
| backups (mariabackup, xtrabackup), the dump, logs, print-defaults | `stream --job --to host:port` (stdin) |
| `receiveBackup` (reseed, flashback) | `stream --job --listen ADDR --accept-timeout 600` (stdout) |

- **Header and refusals:** `utils/jobapi/stream.go` writes and checks the header. A listener
  takes only the stream replication-manager signed for this server, once (`Client.CheckStream`).
  A refused connection is closed and the listener waits for the next one. When none is taken,
  the exit code is 3.
  - `receiveBackup` records the listener's exit code in `$LOG_DIR/stream.rc` and its stderr in
    `stream.err`.
  - It records the listener's PID in `stream.pid`, so a disk-floor stop can kill it.
- **Two-way:** `stream --to` reads the connection as well, as the old `socat stdio` did.
  - When replication-manager closes the stream first (a backup cancelled or failed), the CLI
    exits 3 at once, even while stdin sends nothing.
  - For a dump: `stream_dump` sees the stream end, waits 10 seconds for the dump client, then
    stops it. This ends the client's session and the DDL lock `lock_ddl_start` holds
    (`netstream.Send`, `ErrReceiverClosed`).
- **Stale listeners:** `listenerCleaner`, formerly `socatCleaner`, still frees the jobs port when
  `lsof` exists. Without it, a stale listener ends at its accept timeout.

## The xtrabackup bundle

The bundle (`prov-db-docker-xtrabackup-img`) still copies `socat` for one release.

- A container whose config fetch fails keeps the script it had. That older script talks through
  `socat`, and it can receive its upgrade only through `socat`.
- The new script needs only `xtrabackup` and `xbstream` from the bundle.
- `socat` leaves the bundle in a later release, once every running script is the CLI one.

## What is left as is

- **The `start` task:** it calls `curl` on `need-start`. The branch compares a literal string with
  200 and is never taken.
- **The SQL jobs mode:** it reads the jobs table with the database client. Its log lines and
  streams go through the CLI like the API mode's.

## Tests

- **Unit tests:**
  - `utils/netstream` tests the two-way send (a receiver that closes first while the input is
    silent, with and without gzip) and the checked accept (a refused stream, then the signed one;
    `ErrRefused` when none is taken).
  - `utils/jobapi` tests each new call, decrypting the body as the server does.
- **Cluster and server tests:**
  - They built job bodies and streams with the script's bash functions; they now build them with
    `jobapi`: `SealBody`, `StreamHeader`, `CheckStream`.
  - The producer-tail tests send through `netstream.Send`.
- **Shell checks:** `onpremise_jobs_paths`, `mariabackup_target_dir` and `mariabackup_undo_dir`
  now stub or cut `stream_cli` and `listenerCleaner`.
- **T13:** `testJobsRoundTrip` runs on a MariaDB cluster and on a stock `mysql:8.4` cluster with
  the bundle off (no socat, no openssl in the jobs container). Each covers a logical and a
  physical backup, a reseed, the error-log collection, the script upgrade, and a cancelled dump
  that ends within the grace period. The results are in the PR.
