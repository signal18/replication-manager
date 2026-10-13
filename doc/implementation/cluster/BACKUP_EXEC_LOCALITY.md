# Logical backup exec locality: remote mysqldump (#1628)

User doc: `doc/backup_remote_dump.md`.

`mysqldump` is a `TaskCapBoth` task (`config.TaskRegistry`), local by default.
Listing it in `scheduler-jobs-exec-remote` makes the logical backup run the
dump client on the database node through dbjobs, streaming back to a receiver
in replication-manager. Everything after the dump client (compression or
splitdump, metadata, cancel, progress, restic, retention) stays in the common
logical backup path. The DDL lock moves to the node, around the dump client.

## Decision

`ServerMonitor.mysqldumpExecRemote()` (`cluster/srv_job_backup_remote.go`) is
`config.IsRemoteTask(ConstTaskDump, SchedulerJobsExecOverrides)`.
`JobBackupLogicalWithOptions` computes it once, skips the local binary check
when remote and records it as `BackupMetadata.ExecRemote`; `JobBackupMysqldump`
registers the cancel and branches into `jobBackupMysqldumpRemote`, without
taking the DDL lock itself (see DDL lock).

`SchedulerJobsExecOverrides` is rebuilt from the cluster's own
`scheduler-jobs-exec-remote` in `Cluster.Init` and `ReloadConfig`: before, it
was built from the global config only, so a per-cluster value was lost on
restart.

## Dump arguments

`GetRemoteMysqlDumpOptions` = `GetMysqlDumpOptions` minus
`GetDumpCredentials` and `GetSSLClientParam("client-dump")`, and minus the
connection options of `backup-mysqldump-options` (`remoteDumpConnectionOption`),
plus the TLS options of the node (`remoteDumpTLSOptions`). The arguments travel
in the clear (base64 only): the names removed are logged, never their values.

`remoteDumpConnectionOptions` lists the removed options by exact name, never by a
family of names: a client option added later is never removed by accident. A
name is read as the clients read it, a closed set of forms: `_` for `-`, one
modifier of `remoteDumpOptionModifiers` (`--skip-ssl`, `--loose-password`), and a
shortened `--password` (2 letters or more: the clients accept an unambiguous
prefix of an option, a deprecated form) whose value then never travels. Short
forms `-u -p -h -P -S`, attached or with the next argument as value.

## TLS of the dump client

`remoteDumpTLS` keeps the decision of a local dump, `GetSSLClientParam("client-dump")`
(`hosts-tls-ssl-mode`, `hosts-tls-*`, the `ssl` tag, generated certificates, the
server's TLS), and changes only what is replication-manager's:

- the mode is read back from either syntax as `GetSSLClientParam` maps it
  (`--ssl --ssl-verify-server-cert=false` REQUIRED, `+ --ssl-ca` VERIFY_CA,
  `+ --ssl-cert/--ssl-key` VERIFY_IDENTITY, `--ssl=FALSE` DISABLED);
- the files of replication-manager's working directory among `ca-cert.pem`,
  `client-cert.pem`, `client-key.pem` are named at `/etc/mysql/ssl/`
  (`ReplicationSSLCaProvisioned`'s directory, #1933): the configurator ships them
  there, and the jobs containers mount the database's `/etc/mysql`. Any other file
  (`hosts-tls-*`, `old_certs` during a rotation) has no known place on the host:
  it is left out with a warning, and a verifying mode then takes `replication-ssl-ca`
  (#1933, the CA path on the database hosts) when set. A verifying mode without a CA
  is kept: the dump fails, never silently unverified;
- the syntax is the node's client's, of the server's flavor: `--ssl-mode` for
  MySQL/Percona 8, `--ssl`/`--ssl-verify-server-cert` for the others.
`RemoteMysqlDumpArgs` encodes them one per line, base64, so they travel as one
word. dbjobs decodes them with `mapfile` and adds the node's
`--protocol=TCP --user --host --password --port`.

## DDL lock

`MysqldumpLockDDL()` (`backup-lockddl`, MariaDB 10.4+) is the condition of both
paths. A local dump takes `BACKUP STAGE START` / `BLOCK_DDL` from
replication-manager before the dump client starts. A remote one does not: the
backup waits for dbjobs to take the dump (up to the pickup timeout), and a lock
taken then would block DDL for that wait too. replication-manager tells dbjobs
to lock instead (`LOCK_DDL=1` in the receive-task answer, `<args>@1` in the
jobs row payload), and dbjobs' `lock_ddl_start` holds it in a client session of
its own (a bash coprocess) from just before the dump client to just after it
(`lock_ddl_end`: `BACKUP STAGE END`, session closed). The server releases the
lock when that session ends, whatever ends dbjobs. The client runs unbuffered
(`-n`): its confirmation (`SELECT 'ddl-blocked'`) must reach dbjobs while the
session is open, a client writing to a pipe keeps its output until it exits.
The coprocess `exec`s the client, so killing it ends the session. As for a
local dump, the wait is the server's (`lock_wait_timeout` on the stage), and a
failed lock leaves a warning while the dump goes on.

The lock ends with the dump client, so dbjobs stops a client that replication-
manager no longer waits for (`stream_dump`): the stream is a two-way socat, which
also reads the connection and so ends when replication-manager closes it, even
while the client sends nothing (blocked on a lock); a client still running once
the stream ended is stopped after a 10s grace. A client that ends normally
closes its output only when it exits, after its last statements (`START SLAVE`
of `--dump-slave`), so a normal end is never cut short.

## api mode

```
JobBackupMysqldump -> jobBackupMysqldumpRemote -> runStreamTask (srv_job_postgres.go)
  armStreamTask(mysqldump)            done channel in streamTasks
  setTaskCookie -> cookie_waitmysqldump (postgresJobCookie)
dbjobs: needs/mysqldump -> CheckTaskNeeded (cookie)
        receive-task/mysqldump -> WaitStreamTaskArmed (backup-logical-remote-arm-timeout)
                                 OpenRemoteDumpReceiver -> "RECEIVER_PORT=<p> DUMP_ARGS=<b64> [LOCK_DDL=1]"
        dump client | socat -> receiver
        job-state/mysqldump/done|error (exit codes of client and socat)
receiver end -> JobFinishReceiveFile -> signalStreamTaskDone
waitRemoteJobReport (JobResults, report timeout)
```

`receive-task` opens a receiver only while a backup is armed for the task, so
a stale cookie can never stream into a file a local run writes. With the
scheduler on, `JobInsertTask` sets the cookie before the backup arms (it still
prepares its output): a `receive-task` that times out costs dbjobs one cycle,
since `runStreamTask` sets the cookie again once armed and its pickup timer
starts there.

## sql mode

`runRemoteDumpJobsRow`: the receiver is opened first, then
`JobInsertTaskWithPayload(mysqldump, <port>, MonitorAddress, <b64 args>@<0|1>)`,
the last field telling dbjobs whether to block DDL.
`JobBackupLogicalWithOptions` does not insert the port-0 row first
(`remoteRow`), and does not rewrite the row on success: dbjobs recorded the
end on it.

dbjobs polls `task='mysqldump' AND done=0 AND state=0 AND port<>0` (a local
dump's row has port 0) and reads `server:port` and the two payload fields; `doneJob`
writes state 3/done 1 or state 5 from the two exit codes.

The wait reads its own row every second (`remoteDumpRowState`, through
`dbhelper.GetJobTaskState`, on the pool: a read needs no session of its own),
since the job cache is refreshed lazily in this mode:

- state left 0: taken;
- state 5/6: failed, the receiver is stopped;
- stream ended and done=1 with state 3/4: success.

At the pickup timeout `withdrawRemoteDumpRow` runs `dbhelper.WithdrawJobTask`:
`UPDATE ... SET done=1, state=5 ... WHERE task=? AND done=0 AND state=0`, one
statement, so either dbjobs took the row or it never will. Both helpers
(`utils/dbhelper/jobs.go`) take any connection with `ExecContext` /
`QueryRowContext`, and return `ErrNoJobsTable` on PostgreSQL, whose jobs go
through the API only. The stream is bounded to 12 hours (`postgresJobStreamTimeout`),
as the pg_dump of the PostgreSQL jobs sidecar in api mode (`runStreamTask`).

## Receivers

`OpenRemoteDumpReceiver(task)`:

- splitdump: `SSTRunReceiverToWriter` into the sink the backup registered
  (`streamTasks["sink:mysqldump"]`), the write side of
  `setupSplitDumpPipeline` behind a `fallbackWriter`. It reuses
  `tcp_con_handle_to_file` with `outfilewriter` = the sink.
- else: `SSTRunReceiverToGZip` into `PostgresStreamDest(task)` (the staged
  `LastBackupMeta.Logical.Dest`), with the observer
  `streamTasks["observe:mysqldump"]` called on every chunk before compression.

The receivers take the first connection on their port without authenticating it,
as every SST receiver does (physical backups, fetched logs); a dump stream goes to
the position reader and to splitdump. The peer is not checked against the server's
address, which can differ from dbjobs' source address (sidecar networking, NAT,
multi-homed hosts); restricting the port to the database nodes is left to the
network, and hardening all receivers to a separate change.

The position reader runs in the receiver's goroutine and writes plain variables
read after the wait: both waits succeed only once the receiver signalled the end of
the stream (`JobFinishReceiveFile` closes `done` in its cleanup, after the copy loop),
which orders those writes before the reads (`TestStreamReaderResultsAfterDone`, -race).
The client version is read from the first 1 KiB of the stream whatever its chunks
(`dumpClientVersion`); its pattern needs the character after the version, so a cut
one never matches.

The splitdump input is closed at the end of the wait, with the wait's error
when it failed, so a cut stream is never taken for a complete dump.

`OpenRemoteDumpReceiver` records the port it opened (`streamTasks["port:mysqldump"]`):
a backup that is cancelled or fails cuts its stream (`stopRemoteDumpStream`) in
both modes, also in api mode where dbjobs' request opened the receiver. The dump
client on the node then fails on its next write. `stopRemoteDumpReceiver` closes
the listener and the accepted connection under `SSTs`; the gzip and file
receivers set `sst.in` under that lock after `Accept`, so the close does not
race the receiver goroutine (`SSTCloseReceiver`, used by physical backups,
reads it without the lock).

## Reading the stream

The dump header and position comments stay in the stream, read on its way
(the receiver also feeds compression, so the reading must not slow it down):

- first chunk: client version (`dumpClientRegex`), stored as
  `BackupToolVersion`;
- `dumpPositionLines` picks lines by their first 64 bytes
  (`dumpPositionHeadRegex`) and passes data lines (extended INSERTs) without
  copying them. A kept line or joined GTID statement is bounded by
  `dumpPositionLineMax` (1 MiB, checked before each copy): past it, it is
  dropped, its buffer freed, and the backup logs a warning; a long line's
  buffer is released once the line is read:
  - position lines go to `dumpStreamParser` (`getBackupRegexPatterns`, which
    now accepts MariaDB 10.x `CHANGE MASTER '' TO`);
  - `-- Current Database:` / `-- Table structure for table` call
    `BackupProgress.TableBoundary`.

The GTID of a `--dump-slave` dump comes at the end of the stream, which is why
the stream is read throughout rather than its head afterwards.

GTID sets are recorded whole or not at all, never partially or wrong:

- MariaDB writes `gtid_slave_pos` on one line, kept whole up to the 1 MiB
  bound (tested with 20000 domains, about 300 KB).
- MySQL / Percona 8 write `GTID_PURGED=/*!80000 '+'*/ '<set>'` with one source
  per line. The reader joins the lines of the statement up to its `;`
  (indentation and CRLF tolerated); a continuation line that is not a GTID set
  part drops the statement, which keeps the buffer to one set. The GTID pattern
  (`getBackupRegexPatterns`) skips the `'+'` marker and never captures a value
  starting with `+`. Tested with 10000 sources with several intervals and 8.4
  tags.

Throughput measured on 64 MiB of 1 MiB INSERT lines: pgzip alone 3.4-4.2 GB/s,
pgzip with the reader 3.6-3.7 GB/s.

## Progress

`physicalBackupProgressForTask` maps `mysqldump` to the running logical
progress: the receiver counts bytes, the table lines set the schema level.
The dump's verbose output (what a local dump reads for tables) stays on the
node in `backup.out`; dbjobs forwards it through write-log, too late for
progress (about 140 encrypted posts for a 100 MB lab dump that lasts 5 s).

## Restore on the node

`reseedMysqldumpWithMetadata` routes to `jobReseedMysqldump(..., onNode=true)`
when the reseed state is `reseedmysqldump` (not a flashback) and
`restoreLogicalOnNode(meta, path)`: `meta.ExecRemote`, not a splitdump (metadata
or directory). Old backups have no `ExecRemote` and keep the local restore.
Splitdump on the node comes later.

Only the client moves. `jobReseedMysqldump` keeps the preamble
(`buildLogicalRestorePreamble`), the classification of `mysql.*` content and the
system replay; its client is a `reseedClient{stdin, wait}`:

- `startLocalReseedClient`: replication-manager's client, unchanged
  (`--force`, `backup-mysqlclient-options`).
- `startNodeReseedClient` (`srv_job_backup_remote.go`): the task
  `reseedmysqldump` without payload, an `io.Pipe` as the stream source of
  `WaitAndSendSSTStream(..., trackProgress=false)`. sql mode: `JobInsertTask`
  with the node's `SSTPort`. api mode: the cookie only (`setTaskCookie`):
  `JobInsertTask` refuses a task the reseed already tracks as running, and
  signals only a task `scheduler-jobs-exec-remote` lists. A stream that never
  starts withdraws the row (sql) or the cookie (api).
- `waitNodeReseed`: the row (sql) or `JobResults` (api) each second, bounded
  by `postgresJobStreamTimeout`, the context and the cluster exit. State 5/6 is
  a failure (sql: with the result lines), done=1 with 3/4 is success. `wait`
  then closes the pipe, so a writer is never left blocked.

`trackProgress=false`: the restore already counts the backup file it reads
(`startReseedProgress`); `WaitAndSendSSTStream`'s own progress would reset it,
add the sent bytes again and label it with the physical tool.

dbjobs (`reseedmysqldump`): `socat` from `SST_RECEIVER_PORT` into
`$BINARY_CLIENT --batch`, no `--force`, output in `reseed.out`. Either exit
code non-zero sets `PR_STATUS=1` and writes "Restore failed (stream exit, client
exit): <last 5 lines>" to `reseedmysqldump.out`, the row's result (sql); api
mode reports `error`. `--force` is left out on purpose: a statement that fails
leaves a replica that differs from its backup, which must fail the reseed, not
be skipped. The dump never holds `replication_manager_schema.jobs`
(`--ignore-table`), so the restore never touches the row it is tracked by.

## Replication position: backup and reseed (all mysqldump, local or node)

Backup: `GetMysqlBackupDumpOptions` (`cluster_get.go`) = the dump options with
`--master-data=2` / `--dump-slave=2` and without `--apply-slave-statements`,
for the local and the remote dump. `GetMysqlDumpOptions` keeps the active
position for the direct rejoin stream (`JobRejoinMysqldumpFromSource`), which
never points the replica itself. The metadata parsers are not anchored, so the
commented lines still give `BinLogFileName`/`BinLogFilePos`/`BinLogGtid`.

Why: an active `CHANGE MASTER` names no channel. MySQL refuses it when a
replica has several channels; MariaDB applies it to `default_master_connection`.
On a MySQL GTID replica pointed with auto-position it fails (error 1776), which
the local restore only hid with `--force`. MySQL's multi-source provisioning
dumps without `--source-data`, and the MySQL, MariaDB, Percona XtraBackup and
mariadb-backup replica procedures load the data, then run `CHANGE MASTER`,
then `START SLAVE`.

Reseed (`ProcessReseedLogical`, mysqldump, not PITR):

1. `StopAllSlaves`.
2. Every single-channel replica, all flavors and modes: `ResetSlave` (`RESET
   SLAVE ALL`), as a new replica. An older backup (`--master-data=1`,
   `--apply-slave-statements`) then applies its own `CHANGE MASTER` to a
   channel without a primary, and its `START SLAVE` can not start replication
   before the position is set (started, it applies what follows the dump, and
   setting the dump's position afterwards replays it: duplicate keys). It is
   skipped with several channels: without `FOR CHANNEL` it would drop them all.
3. The restore: local or on the node, file or splitdump. Each records the
   position it restored in `server.logicalRestorePosition` (the classified
   stream's `splitdump.Metadata`, or the splitdump metadata).
4. `pointLogicalReseedReplica`, also after a failed restore (the replica
   keeps its primary): it stops all channels again first (an older backup's
   `START SLAVE` / `START ALL SLAVES` restarts replication at the end of the
   load, and `gtid_slave_pos` or `CHANGE MASTER` need it stopped); position from `logicalReseedPosition` (the restored
   one, else the backup metadata). MySQL GTID: auto-position. MariaDB with a
   GTID and GTID replication (not `force-slave-no-gtid-mode`): `SET GLOBAL
   gtid_slave_pos`, then `SLAVE_POS`. Else file/position
   (`POSITIONAL`), else `pointSlaveToMasterAutoDetect`. Then, after a
   successful restore, `StartSlave` (the managed channel). The GTID state is the
   backup's as it is (`GTID_PURGED`, `gtid_slave_pos`, all sources and
   domains). Multi-source is not supported: extra channels stay stopped with a
   warning naming them, for the operator.

mydumper and the backup-load-script path keep their pointing before the load.

## Dump client compatibility

Rules between the dump client and the server, next to the existing tool rules
(`CheckLogicalBackupToolVersion`: restore tool and backup tool of the same
major.minor; `getDumpParameter`: no `--system=all` on MySQL;
`GetMyLoaderCompatibleOptions`: mydumper options by version). Verified in the lab.

| # | Rule | Applied by |
|---|---|---|
| C1 | MariaDB server: MariaDB client. MySQL/Percona server: MySQL client (`mariadb-dump` writes no `GTID_PURGED` and can not dump MySQL 8.4) | the cluster's `backup-mysqldump-path` / `backup-mysqlclient-path` / `backup-mysqlbinlog-path`; a remote dump uses the node's client. Checked: WARN0244 |
| C2 | MySQL client of the server's line: 8.0 for 5.7 and 8.0, 8.4 for 8.4 (the 8.4 client sends `SHOW BINARY LOG STATUS`, unknown to 8.0; the 8.0 client `SHOW MASTER STATUS`, removed in 8.4). An 8.4 client dumps 5.7 | same. Checked: WARN0244 |
| C3 | `--skip-log-queries` only when the client and the server name the slow log the same way: `slow_query_log` before MariaDB 10.11, `log_slow_query` from 10.11. The dump writes `/*M!100101 SET LOCAL SQL_LOG_OFF=0, LOCAL LOG_SLOW_QUERY=0 */` with a 10.11+ client, which a 10.4-10.6 server rejects (ERROR 1193) | `mariadbSkipLogQueriesCompatible` in `getMysqlDumpOptions`, with the client version: `client-dump` for a local dump, the server's for a remote one (the node's client) |

`CheckDumpClientCompatibility` (`cluster_bck.go`, from `CheckBackupServerDumpClient` every 30
heartbeats) raises WARN0244 on the backed-up server when the cluster's local dump
client (`client-dump`) is one of the cases seen failing (`dumpClientIncompatibility`): a
MariaDB client for MySQL/Percona, a pre-8.2 client for an 8.2+ server, an 8.2+ client for an
8.0/8.1 server. It is skipped when the dump runs on the DB node, and cleared once the client
fits.

## Settings

| Key | Default | Use |
|---|---|---|
| `backup-logical-remote-pickup-timeout` | 300 | `remoteDumpPickupTimeout` |
| `backup-logical-remote-report-timeout` | 120 | `remoteDumpReportTimeout` |
| `backup-logical-remote-arm-timeout` | 10 | `RemoteDumpArmTimeout()`: receive-task waits for the backup to arm |

0 or less uses the default (`secondsOr`). Settable through
`/settings/actions/set/<key>` and Settings > Backup.

## Tests

- Unit (`cluster/srv_job_backup_remote_test.go`): locality decision,
  connection-name binlog regex, MySQL 8 GTID pattern, client version, observer,
  arm wait, report wait, line reader (chunk splits, 20000-domain GTID,
  multi-line 10000-source GTID, CRLF and indentation, a cut statement, the
  1 MiB bound, table lines), connection options left out, receiver close with
  and without a stream, splitdump sink receiver, timeouts default.
  Restore on the node: `TestRestoreLogicalOnNode` (remote dump, local, no
  metadata, splitdump by metadata and by name, missing file),
  `TestWaitNodeReseed` (sql row failed with its lines, finished only once done,
  success, api reports, canceled). Position: `TestGetMysqlBackupDumpOptionsPositionComment`
  (cluster_get_test.go: backup dumps comment the position, the direct stream
  keeps it), `TestLogicalReseedPosition` (restored position first, then the
  backup metadata).
  `utils/dbhelper/jobs_test.go`: both jobs table helpers, MariaDB/MySQL and
  PostgreSQL.
- Regtest `testRemoteMysqldump` (needs dbjobs next to each database, not in the
  ALL list; run it by name, once per `scheduler-jobs-mode`): gzip and splitdump
  of the server the scheduler backs up, the designated backup server else the
  primary (metadata, GTID of the server, complete artifact), a reseed of a
  replica from the gzip dump (restored by the replica's dbjobs: in sql mode its
  `reseedmysqldump` row succeeded; replication running after it), a 1s pickup
  timeout withdrawing the row, with a user and password in
  `backup-mysqldump-options` all along.
- Lab (OpenSVC, MariaDB 10.11 replica), each in api and sql mode:
  - gzip: version, binlog position and GTID in the metadata, `gzip -t`;
  - splitdump: 240 files, identical INSERT rows to a local splitdump of the
    same server;
  - progress: bytes and tables live;
  - pickup timeout: the row is withdrawn and never run;
  - mid-stream cancel: dbjobs records both exit codes;
  - a local dump's row is never taken (scheduler on).
- Lab, MySQL 8.0.35 (`repmy8`) and Percona 8.4.11 (`repmy84`), primary and
  replica, sql mode: the node's client version, binlog position (8.4:
  `CHANGE REPLICATION SOURCE`) and the full two-source GTID set equal to the
  server's `gtid_executed`. A local dump of the same MySQL replica, with
  replication-manager's MariaDB client, records no GTID.
