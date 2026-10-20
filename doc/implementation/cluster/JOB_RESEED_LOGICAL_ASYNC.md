# JobReseedLogicalBackup async process

This document describes the current asynchronous flow for logical reseed jobs started by `JobReseedLogicalBackup` and related entry points.

## Entry points

- `cluster/srv_job_backup.go:JobReseedLogicalBackup` calls `JobReseedLogicalBackupPrepare` and returns immediately after preparing the job state and payload.
- `cluster/srv_job_backup.go:JobReseedLogicalBackupFromPathWithOptions` calls `JobReseedLogicalBackupFromPathPrepare` to enqueue a job for a specific backup path (used by restic restore workflows).
- `cluster/srv_job_backup.go:JobReseedLogicalBackupProcess` is a thin wrapper that delegates to `ProcessReseedLogical`.
- Job runners and API call sites are in:
  - `server/api_database.go` (API-triggered reseed)
  - `cluster/cluster.go` (state machine triggers)
  - `cluster/srv_job_restic.go` (restic-driven reseed)

## Async handoff overview

1. **Prepare phase** (synchronous):
   - Validate cluster discovery, master presence, mysql client path, and backup tool version.
   - Determine the backup file path (using backup server cookies, metadata paths, or defaults).
   - Acquire the reseed lock with `TrySetInReseedBackup(task)`.
   - Build a payload via `buildLogicalReseedPayload` (see payload section below).
   - Update the task runtime state and payload and signal the cluster state machine.

2. **Cluster state signal** (handoff):
   - `JobReseedLogicalBackupPrepare` sets cluster state `WARN0075` with the server URL.
   - Job table polling (`JobsUpdateEntries`) also sets `WARN0075` when it sees `reseedmysqldump` or `reseedmydumper` tasks.

3. **Process phase** (asynchronous):
   - The monitoring loop in `cluster/cluster.go` consumes `WARN0075` and calls `ProcessReseedLogical(task)` for the target server.
   - This is the point where the actual restore work begins.

## Payload

The payload is stored in the job runtime cache and, when scheduler monitoring is enabled, in `replication_manager_schema.jobs.payload`.

Fields (all strings):

- `backup_type`: logical tool (`mysqldump` or `mydumper`), defaults to `cluster.Conf.BackupLogicalType`.
- `backup_path`: resolved backup path; may be empty for discovery-based reseed.
- `split_user`: whether the backup includes split user data (true/false).
- `split_user_override`: true if the caller forced `split_user`.
- `skip_metadata`: true to skip metadata-driven splitdump handling.
- `is_pitr`: true if reseed is part of PITR flow.
- `server_url`: target server URL.

## Prepare phase details

### Common validation

- Require cluster discovery and a master server.
- Validate `cluster.GetMysqlclientPath()` exists.
- Enforce logical backup tool version compatibility when `BackupRestoreVersionStrict` is enabled.
- Block concurrent reseeds using `TrySetInReseedBackup(task)`.

### Backup path resolution

- Prefer a backup server that has a matching cookie and a resolvable backup path.
- Fall back to the master if the backup server cookie is stale or missing.
- Accept a caller-provided backup path when using `JobReseedLogicalBackupFromPathPrepare`.

### PITR handling

- If `PointInTimeMeta.IsInPITR` is true, the prepare phase stops and resets replication before processing:
  - `StopSlave()`
  - `ResetSlave()` (ignores MySQL error 1617)
  - Set server state to `stateUnconn`

### Task state updates

- For default reseed (not path-specific):
  - `JobsUpdateState(task, "", 1, 0)` updates runtime (and jobs table if scheduler monitoring is on).
  - `JobsUpdatePayload(task, payload)` stores the payload.
  - `cluster.SetState("WARN0075", ...)` triggers async processing.

- For reseed from a specific path:
  - `JobInsertTaskWithPayload(task, "0", monitorAddress, payload)` inserts the job row.
  - The jobs polling loop (`JobsUpdateEntries`) sets `WARN0075` once it sees the task.

## Process phase details (`ProcessReseedLogical`)

### Preconditions and payload parsing

- Verify the reseed lock is still set for the task.
- Block operation if target is a super read-only replica.
- Parse payload fields to override backup type, backup path, split-user settings, skip-metadata, and PITR flag.

### Script-based restore

If `cluster.Conf.BackupLoadScript` is set:

- Stop replication and re-point the replica (unless PITR).
- Mark the job as processing with `JobsUpdateState(task, "processing", 1, 0)`.
- Run `JobReseedBackupScript()` and update job state to success or error.

### Mysqldump restore

- Resolve backup path (payload path first, then backup server, then master).
- Decide whether to use splitdump restore based on metadata:
  - `reseedMysqldumpWithMetadata` (metadata-driven)
  - `reseedMysqldumpWithSplitdump` (scan-based)
- Stop and re-point replication unless PITR.
- Update job state to completed or failed.

### Mydumper restore

- Run `JobReseedMyLoader`.
- If successful and not PITR, parse mydumper metadata to apply MariaDB GTID and restart replication.
- Update job state to completed or failed.

## The replica's replication links (law T27, #2093)

A reseed restores data; it does not own the replica's links. Since #2093:

- **Stop, never delete.** The restore stops every channel (`STOP ALL SLAVES`: the load
  replaces the data all of them apply to) and clears the managed channel's positions and
  relay logs with `RESET SLAVE '<channel>'` **without `ALL`** (`ResetSlavePositions`), at
  request time (`JobReseedLogicalBackupPrepare`) and before the load. The link's definition
  (host, user, channel) stays: a reseed that fails half-way leaves the link stopped, never
  gone. #2020 (v3.1.44) used `RESET ... ALL` there, which deleted the link; for a child's
  server reseeded from its parent, the parent then dropped the server from its topology.
- **Position, never move.** After the load the managed channel is positioned
  (`pointLogicalReseedReplica`, and the mydumper position) only when it points to the
  cluster's primary, or does not exist yet (a new replica). A link pointing to another
  source is left as it is, stopped: moving it is the failover, switchover and rejoin's job
  (`reseedLinkPointsToPrimary`). The match is the primary's host or IP and port: a link
  reaching the primary through a proxy, a VIP or another alias counts as another source
  and is left stopped (the conservative side); with no primary known nothing is positioned.
  A managed channel with an empty host counts as "no link yet" and may be positioned (rare).
  A link left in place is not started either: the final START of the older branch and the
  mydumper start check the same gate (`linkOnSource`).
- **A state, not a log.** The channels the reseed stopped are recorded
  (`markReseedStoppedLinks`); every server refresh (`checkReseedStoppedLinks`) drops those
  that run again, whoever started them, and keeps **WARN0252** open while one does not,
  naming the channels and the reason (the restore, a link pointing elsewhere, the other
  sources of a multi-source replica left for review).
  A channel that disappears from the replica's status while the server answers (removed by
  the operator) leaves the list too: the state never stays open forever (F3/F4).
  The record is guarded by a mutex (the reseed records, the refresh drops), and within two
  monitoring ticks of a recording a running status, possibly read before the STOP, is ignored.
- `dbhelper.ResetSlaveStatement` names the channel with or without `ALL`: MySQL's
  `RESET REPLICA` without `FOR CHANNEL` resets every channel.
- **Without the accounts, the dump's account-import role bracket is left out.**
  `mariadb-dump` 11.x wraps the accounts it dumps in a temporary role
  (`SELECT … INTO @current_role`, `CREATE ROLE IF NOT EXISTS mariadb_dump_import_role`,
  `GRANT mariadb_dump_import_role TO CURRENT_USER()`, `SET ROLE …`, then `SET ROLE NONE`,
  `DROP ROLE …` and `SET ROLE @current_role`). The classifier keeps these lines on the
  application side (#1980, so a system artifact replayed alone does not fail), so a restore
  without restore-user ran them with the data. On a master restored with its binary log on
  (a child master reseeded from its parent) they reached its replicas, whose applier is no
  account: `GRANT … TO CURRENT_USER()` stopped them ("Can't find any matching row in the
  user table"). The mysqldump restore passes `splitdump.IsImportRoleBracket` as the
  classifier's `Skip` when restore-user is off; the number of lines left out is logged.
  `IsImportRoleBracket` matches the seven statements of the bracket whole, as the client
  writes them, never a view, routine or row that mentions the role, and `Skip` acts on the
  application side only. The #1980 routing of these lines is unchanged. With restore-user
  on, nothing changes.
- **An older backup's own replication statements run.** A backup with active replication
  statements (as written before v3.1.44) carries an unnamed `CHANGE MASTER`; the restore runs
  its statements as written, as v3.1.43 did. The forms depend on `--apply-slave-statements`
  (`--apply-replica-statements`), not on whether the backup was taken from a primary or a
  replica:
  - without it (`--master-data=1` / `--dump-slave=1`): the position only (`CHANGE MASTER`,
    with `--gtid` `SET GLOBAL gtid_slave_pos`), no `START`. The channel stays stopped;
    `pointLogicalReseedReplica` positions and starts it as for any backup;
  - with it, from a primary or a replica (as v3.1.43 wrote its backups): `STOP SLAVE` /
    `STOP REPLICA` / `STOP ALL SLAVES` in its header, before any table data, the
    `CHANGE MASTER` (`CHANGE MASTER '' TO` on MariaDB from a replica), and `START SLAVE` /
    `START REPLICA` / `START ALL SLAVES` in its footer: the dump starts replication itself
    (`dumpStartsReplication` reads that header). The position must be active there: with
    `--master-data=2` / `--dump-slave=2` and the apply statements the clients still write
    `STOP` ... `START` but
    the `CHANGE MASTER` and `gtid_slave_pos` as comments, so the `START` resumes from the
    server's old position, before repman positions it: left alone it resumes from an unrelated
    position, positioned afterwards what it applied meanwhile is applied twice. A reseed and a
    child seed refuse such a dump before anything is stopped or loaded
    (`dumpStartsReplicationUnpositioned`); a new backup has no such statement.
    The header decides, except for a MariaDB dump whose header stops replication without a
    position (#2118): the MariaDB client with `--gtid` writes the `SET GLOBAL gtid_slave_pos`
    of `--master-data=1` / `--dump-slave=1` after the table data, just before the `START`
    (MariaDB 11.4.13 client, read in the lab, as v3.1.43's backups have it: its header has
    `STOP ALL SLAVES` and the mode-only `CHANGE MASTER TO MASTER_USE_GTID=slave_pos`). Such a
    dump (`-- MariaDB dump` in its header) is read on to its `START`; an active
    `SET GLOBAL gtid_slave_pos` before it positions the dump, which is then restored and left
    as it started (`dumpStartsReplication`). With `--master-data=2` / `--dump-slave=2` that line
    is a comment, and the dump is still refused: lab, refusal disabled, writes on the primary
    during the reseed: the replica stopped on a duplicate key, the row written during the
    reseed applied twice. The extra read happens for those older dumps only. A MySQL or
    Percona dump is decided by its header alone (no such layout read).
  - MySQL/Percona with GTID: the channel keeps its auto-position. The dump's
    `SET @@GLOBAL.GTID_PURGED` sets the position; its file/position `CHANGE MASTER` is refused
    (error 1776): skipped with `--force` in `backup-mysqlclient-options` (the default); the
    dump's `START SLAVE`, when it has the apply statements, then starts replication on
    auto-position, otherwise `pointLogicalReseedReplica` starts it. Without `--force` the restore
    stops there, before any data, and the reseed fails; repman does not add `--force`
    itself: the operator adds it to `backup-mysqlclient-options`, or takes a new backup
    (written by this release, it has no such statement).
  - A dump that starts replication itself, restored on stopped channels: a managed channel
    with a thread running, or its IO thread still connecting (right after `START SLAVE`),
    was started by that dump, from the position it set. `pointLogicalReseedReplica` leaves it
    as it is (after the link check): stopped, set back and started again, what it executed
    meanwhile would be applied twice. Without such a dump (every other backup, a splitdump,
    the backup verification restore) a running channel is positioned and started like a
    stopped one: its state alone is no proof of who started it. A status read that fails is
    logged.

## Cleanup and error handling

- `ProcessReseedLogical` always clears the reseed state on exit via a deferred reset.
- Errors update the job state to `state=5, done=1` and log at `ConstLogModTask`.
- Success updates the job state to `state=3, done=1`.

## Scheduler and job table behavior

- `JobsUpdateState` and `JobsUpdatePayload` always update the in-memory `JobResults` cache.
- When `cluster.Conf.MonitorScheduler` is enabled, these updates are persisted to the jobs table.
- The polling loop (`JobsUpdateEntries`) reads the jobs table and emits `WARN0075` for logical reseed tasks, which drives the async handoff.

### Closing the jobs row

The row a reseed from a given path inserts
(`JobReseedLogicalBackupFromPathPrepare`, `JobInsertTaskWithPayload`, SQL jobs
mode: the restic reseed, a reseed from a chosen backup) is closed when the
reseed ends, by replication-manager. The default reseed inserts no row (its
task lives in memory, `JobsUpdateState`): the close finds none.
replication-manager runs the restore itself (`ProcessReseedLogical` records its outcome in
memory, `JobsUpdateStateRuntimeOnly`), and dbjobs never takes a logical reseed task
(`reseedmysqldump` / `reseedmydumper` are not in its `JOBS` list), so nothing
else closed it. Left open (`done=0, state=0`), it refused the next reseed of the
server (`jobOpenTaskError`: "still open in the jobs table") and kept `WARN0075`
coming back.

`closeLogicalReseedJobRow(task, runErr)`, called after `ProcessReseedLogical`
in `launchLogicalReseed` (async path, deferred, after the error is recorded)
and in `JobReseedLogicalBackupProcess` (sync path):

- the outcome: the run's error (`state=5`), else the result the run recorded
  when done (`state=3`, its result), else a failure ("the reseed ended without
  a result");
- `dbhelper.WithdrawJobTask` on a connection out of the binary log (no errant
  GTID on a replica): only the open row no runner took (`done=0 AND state=0`),
  never the task's past rows (`JobsUpdateState`'s SQL path updates them all);
- nothing in API mode or on PostgreSQL (no jobs table). A restore that
  replaced the jobs table (the backup holds `replication_manager_schema`)
  leaves no row: the update matches none.
