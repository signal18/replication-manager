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

## Cleanup and error handling

- `ProcessReseedLogical` always clears the reseed state on exit via a deferred reset.
- Errors update the job state to `state=5, done=1` and log at `ConstLogModTask`.
- Success updates the job state to `state=3, done=1`.

## Scheduler and job table behavior

- `JobsUpdateState` and `JobsUpdatePayload` always update the in-memory `JobResults` cache.
- When `cluster.Conf.MonitorScheduler` is enabled, these updates are persisted to the jobs table.
- The polling loop (`JobsUpdateEntries`) reads the jobs table and emits `WARN0075` for logical reseed tasks, which drives the async handoff.
