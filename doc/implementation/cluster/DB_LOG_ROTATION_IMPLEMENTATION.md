# DB log rotation: implementation summary

Companion to `DB_LOG_ROTATION_COMPATIBILITY_PLAN.md` (the design doc). This
file documents what the staged changes actually implement, where, and why,
for reviewers who want the "what changed" view instead of the "what was
planned" view.

## Config surface

`config/config.go` / `server/server_cmd.go`:

| Field | Flag | Default | Meaning |
|---|---|---|---|
| `DBLogRotate` | `--db-log-rotate` | `false` | Enable repman-managed rotation/pruning of fetched DB logs |
| `DBLogOnBackupStorage` | `--db-log-on-backup-storage` | `false` | Store fetched DB logs under backup-backed storage instead of the legacy per-server cluster working dir |
| `DBLogRotateMaxSize` | `--db-log-rotate-max-size` | `100` (MB) | Active file size threshold |
| `DBLogRotateMaxBackup` | `--db-log-rotate-max-backup` | `10` | Rotated files kept |
| `DBLogRotateMaxAge` | `--db-log-rotate-max-age` | `7` (days) | Max rotated file age |

Both booleans default to `false` for compatibility: existing deployments keep
today's location and retention behavior (typically external `logrotate`)
until an operator opts in.

## Rotation backend

`utils/s18log/rotatefilehook.go` gained `NewRotateWriter(RotateFileConfig)
(io.WriteCloser, error)`, alongside the existing `NewRotateFileHook` (a
logrus hook). It's a thin wrapper around the same `lumberjack.Logger` engine,
for callers that write via plain file/appender paths rather than logrus.
Kept in the same file rather than a new one — it shares `RotateFileConfig`
and is a few lines.

## Canonical path + migration

`cluster/srv_job_logs.go` is the single source of truth for where a fetched
DB log file lives:

- `DBLogKind` (enum: `DBLogError`, `DBLogSlowQuery`, `DBLogAudit`,
  `DBLogSqlError`) + `DBLogKindFromTaskName` / `DBLogKindFromTailerType` to
  map both the API/scheduler task-name convention (`"errorlog"`, ...) and the
  tailer-type convention (`"error"`, ...) onto one shared type.
- `ServerMonitor.DBLogDir()` / `DBLogFilePath(kind)` resolve the directory
  based on `DBLogOnBackupStorage`:
  - `false`: `Datadir/log/` (legacy, unchanged)
  - `true`: `GetMyBackupDirectory()/dblogs/` (namespaced away from backup
    payload files)
- `ensureDBLogsMigrated()` / `migrateDBLogsToBackupStorage()` lazily move
  existing legacy files (active + any rotated history, matched by filename
  prefix so both the old manual-rename scheme and lumberjack's own scheme are
  covered) into the backup-backed location the first time it's needed.
  - Never overwrites an existing destination file (preserves both).
  - `renameOrCopyFile()` falls back from `os.Rename` to copy+remove on
    `EXDEV` (cross-device), since backup-backed storage is commonly a
    separate, larger partition.
  - Migration state is `dbLogMigrated atomic.Bool` + `dbLogMigrateMutex
    sync.Mutex` on `ServerMonitor`, not a `sync.Once`: a failed attempt
    (permission error, transient I/O error) does **not** latch success, so
    it's retried on a later call instead of being silently skipped for the
    rest of the process lifetime.

## Write paths

- `cluster/cluster_sst.go`: new `SSTRunReceiverToDBLogFile` — append-only
  passthrough to `SSTRunReceiverToFile` when `DBLogRotate` is off; routes
  through `NewRotateWriter` with DB-specific thresholds when on. The `SST`
  struct gained a `rotateWriter io.WriteCloser` field alongside the existing
  `file *os.File`, and the cleanup path closes whichever is set.
- `cluster/srv_job_logs.go`: the four scheduler jobs (`JobBackupErrorLog`,
  `JobBackupAuditLog`, `JobBackupSqlErrorLog`, `JobBackupSlowQueryLog`) now
  resolve their filename via `DBLogFilePath` and write through
  `SSTRunReceiverToDBLogFile`.
- `cluster/srv_get.go`: TABLE-mode slow-log export dropped its hard 100MB
  truncate-in-place and now uses the same rotate-writer-or-plain-append
  branch as the SST path.
- `server/api_database.go`: `receive-task/{taskname}` for the four DB-log
  task types now resolves its destination via `DBLogKindFromTaskName` +
  `DBLogFilePath`, replacing a prior bug where these tasks wrote to
  `GetMyBackupDirectory() + taskname` — a path scheduler-mode fetches and
  tailers never read from. Backup/reseed/flashback task routing is
  unchanged.

## Tailers

`cluster/srv.go`:

- `NewLogTailer` no longer contains its own startup-time rotate/prune logic
  (manual oversized-file rename + age-based prune). That logic was fully
  redundant with lumberjack, which already rotates an oversized pre-existing
  file on its first write — confirmed by reading lumberjack's source before
  removing it. `NewLogTailer` now only ensures the file exists so `tail` has
  something to open.
- `startLogTailers()` factors out the four `NewLogTailer` calls (used by both
  `InitLogTailers` and `RestartLogTailers`) and logs — rather than silently
  discards — any error from opening a tailer.
- `RestartLogTailers()` is new: stops + cleans up the current four tailers
  and reopens them at the (possibly new) canonical path. This exists because
  `tail.Tail` with `ReOpen: true` only follows rotation of the *same* path;
  it never notices a config change to a *different* path on its own. Without
  this, toggling `db-log-on-backup-storage` at runtime would leave
  already-running tailers reading a now-stale file until repman restarts.
- `AuditLogWatcher` / `SqlErrorLogWatcher` gained the same nil-tailer guard
  `ErrorLogWatcher` / `SlowLogWatcher` already had, for consistency and
  defense-in-depth on the restart path (currently unreachable in practice,
  since `tail.TailFile` can't fail with our config — `MustExist` is never
  set — but cheap to guard against regardless of that library-internal
  assumption).

`cluster/srv_job_logs.go` also gained `Cluster.RestartDBLogTailers()`, which
fans `RestartLogTailers()` out to every server in the cluster.

## API / UI

- `server/api_cluster.go`: `setClusterSetting` accepts `db-log-rotate`,
  `db-log-on-backup-storage`, `db-log-rotate-max-size`,
  `db-log-rotate-max-backup`, `db-log-rotate-max-age`, with the validation
  rules from the plan (size > 0, backup/age >= 0). Setting
  `db-log-on-backup-storage` calls `RestartDBLogTailers()` when the value
  actually flips.
- `share/dashboard_react/.../LogsSettings.jsx`: new "DB Log Retention"
  section — `DB Log Rotation` switch, `Use Backup Storage for DB Logs`
  switch, and three number inputs, with help text clarifying scope (fetched
  DB logs only, not repman's own logs) and that enabling backup storage
  migrates existing logs automatically without overwriting.

## Tests

- `utils/s18log/rotatefilehook_test.go`: `NewRotateWriter` rotates over
  `MaxSize` and doesn't rotate under it.
- `cluster/srv_job_logs_test.go`:
  - Migration: moves active + rotated-history files (both naming schemes),
    preserves an existing destination without overwriting, treats a missing
    legacy dir as trivial success, and treats a permission/read error as
    retriable failure (not a false success).
  - `renameOrCopyFile`: real cross-device fallback and its
    don't-overwrite-destination guard, exercised against `/tmp` vs
    `/dev/shm` (skipped if unavailable or co-located on the same device).
  - `RestartLogTailers`: a real `hpcloud/tail`-backed test that flips
    `DBLogOnBackupStorage`, restarts, and confirms both the tailer's
    `Filename` and that it actually streams newly written content — read via
    the `ErrorLog` ring buffer rather than the tailer's raw channel, since
    `RestartLogTailers` itself starts `ErrorLogWatcher` as a concurrent
    consumer of that same unbuffered channel.

## Known limitations / non-goals

- No automated migration *back* from backup-backed to legacy storage beyond
  "toggle the flag off and the legacy path resumes being used for new
  writes"; old files already moved to backup-backed storage are not moved
  back.
- Migration is per-server and lazy (triggered on first `DBLogDir()` call
  after the flag is on), not a bulk/eager migration across the whole
  cluster at the moment the setting changes.
- Repman's own main/security/internal SQL log rotation
  (`log-rotate-max-size` etc.) is deliberately untouched — this work only
  affects fetched DB working-dir logs.
