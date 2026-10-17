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

### The dump's position and replication statements

mysqldump backups carry the active position again (`GetMysqlDumpOptions`, local and on the
DB node): `--master-data=1` on a primary, `--dump-slave=1` on a replica, with
`--apply-slave-statements`, so a dump loaded by hand ends positioned. In the reseed the
backup's GTID state is the position: MariaDB's `SET GLOBAL gtid_slave_pos`, MySQL/Percona's
`SET @@GLOBAL.GTID_PURGED`. The dump's replication statements name no channel, so the
restore decides on each of them (`restoreStatementSkip`, through the classifier's
`SkipStatement`; each statement left out is logged, the first 100 kept, the rest counted):

| Dump statement | Restore |
|---|---|
| `STOP`/`START ALL SLAVES` (MariaDB), `STOP`/`START SLAVE`/`REPLICA` (MySQL) | left out on every server: they act on every channel; the reseed stops the channels before the load and starts the managed one after its link is checked |
| `CHANGE MASTER TO` / `CHANGE REPLICATION SOURCE TO`, managed channel named or several channels | left out: MariaDB would apply it to the default connection (and create it), MySQL refuses it |
| the same, MySQL/Percona with GTID, after `GTID_PURGED` | left out: the channel stays on auto-position (error 1776 otherwise) |
| the same, the default channel alone | kept: it positions that channel |
| `SET GLOBAL gtid_slave_pos`, `SET @@GLOBAL.GTID_PURGED` | kept: the backup's GTID state |

When the dump positions the server by itself (`dumpPositionsServer`: MariaDB's active
`CHANGE MASTER TO MASTER_USE_GTID`, MySQL's active `GTID_PURGED`, read in the header; a read
error is logged and the position is set after the load instead), the reseed does not set the
position again: the managed channel is started if its link points to the primary, pointed
at the primary in GTID mode first if it has none yet (a new replica), and left stopped with
WARN0252 if it points to another source. Other dumps keep `pointLogicalReseedReplica`.

Channels touched (law T27): every channel is stopped before the load; only the managed
channel (`replication-source-name`) is positioned or started; the other channels of a
multi-source replica stay stopped with WARN0252. Their GTID domains come from the dump's
`gtid_slave_pos`, which replaces every domain.

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
