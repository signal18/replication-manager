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
  - Before moving a given file, it checks
    `Cluster.IsFileOpenForSSTReceive(path)` (scans the global `SSTs`
    registry for a matching `Filename`) and skips — leaving it for a later
    attempt — any file an SST receiver currently has open. This matters
    specifically for the `EXDEV` copy-then-remove fallback: without the
    check, a receiver (scheduler-mode job or API-mode `receive-task`, either
    of which can hold the file open for up to an hour) could keep writing to
    an fd after the copy snapshot was taken, and those bytes would be lost
    once the now-unlinked legacy inode is freed on close. `SST` gained a
    `Filename` field (set by both `SSTRunReceiverToFile` and
    `SSTRunReceiverToDBLogFile`) to make this check possible; it covers
    API-mode too, which — unlike scheduler-mode — never sets the
    `Has/SetWait*Cookie` cookies, so those cookies alone aren't sufficient.
  - `Cluster.RestartDBLogTailers()` (see Tailers below) resets
    `dbLogMigrated` to `false` for every server whenever
    `db-log-on-backup-storage` flips, in either direction. Without this, the
    latch being process-lifetime-scoped meant toggling the setting off then
    back on would silently strand anything written to the legacy dir during
    the "off" window — `ensureDBLogsMigrated` would short-circuit on the
    stale `true` latch and never re-sweep. Resetting on every flip is safe
    regardless of direction, since a redundant re-sweep is a no-op (never
    overwrites an existing destination).
  - The destination-collision check treats an existing **non-empty** file as
    real content to preserve, but an existing **zero-byte** file as a
    placeholder to clear and replace. This matters because `NewLogTailer`
    (and any writer that opens a file with `O_CREATE`) will happily create an
    empty file at the new canonical path for a kind whose real migration was
    just skipped (e.g. because it was open for SST receive at flip time). A
    size-blind "exists → skip" check would treat that placeholder as
    "already migrated" forever, permanently stranding the real file in the
    legacy dir with no path to ever recover it — a strictly worse outcome
    than the in-flight skip itself. Checking size instead makes a later
    migration attempt (see below) actually able to finish the job once the
    receiver closes.
  - `ServerMonitor.maybeRetryDBLogMigration()` is called from
    `JobFinishReceiveFile` (in `cluster/srv_job_backup.go`, which every SST
    receiver's cleanup already calls, for both scheduler-mode jobs and
    API-mode `receive-task`) whenever a DB-log task finishes. If
    `db-log-on-backup-storage` is on and migration hasn't fully succeeded
    yet, it calls `RestartLogTailers()` immediately — retrying migration
    (now unblocked, since the receiver that was holding the file open just
    closed) and pointing tailers at whatever is now canonical. Without this,
    a file skipped at flip time had no automatic trigger to retry once its
    receiver closed; the legacy file (and a stale/empty tailer view of it in
    the UI) could persist indefinitely until some *unrelated* later
    `DBLogDir()` call happened to fire, or repman restarted. Gated behind a
    single atomic-bool read so the steady state (disabled, or already fully
    migrated) costs nothing on every job completion.

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

- `NewLogTailer` no longer contains its own startup-time *size*-rotation
  logic (manual oversized-file rename). That part was fully redundant with
  lumberjack, which already rotates an oversized pre-existing file on its
  first write — confirmed by reading lumberjack's source before removing it.
  It still calls `misc.RemoveOldLogFiles` when `DBLogRotate` is enabled, but
  now scoped to `DBLogRotateMaxAge` and the canonical `DBLogDir()` rather
  than the generic `LogRotateMaxAge`/hardcoded legacy dir. This specifically
  targets the old manual-rename naming scheme (`log_<type>_<timestamp>.log`,
  underscore separator) which predates the lumberjack-backed writer path and
  which lumberjack never touches (it only manages its own
  `log_<type>-<timestamp>.log` backups, hyphen separator) — without this
  call, any old-style files already on disk from before this feature would
  never be cleaned up by anything again. `NewLogTailer` otherwise only
  ensures the file exists so `tail` has something to open.
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
fans `RestartLogTailers()` out to every server in the cluster and resets each
server's `dbLogMigrated` latch (see the migration-latch note above).

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
    preserves an existing *non-empty* destination without overwriting,
    clears and replaces an existing *empty* destination (the placeholder
    case), treats a missing legacy dir as trivial success, treats a
    permission/read error as retriable failure (not a false success), and
    skips (rather than moves) a file currently registered as open in the
    `SSTs` registry. A dedicated end-to-end test walks the exact
    skip-then-placeholder-then-close sequence and confirms the real file
    still wins over the placeholder on the next attempt.
  - `maybeRetryDBLogMigration`: restarts tailers (and thus retries
    migration) when a receiver finishes while migration is still pending;
    no-ops when already migrated or when `db-log-on-backup-storage` is off
    (checked by confirming no tailer ever gets created in those cases).
  - `renameOrCopyFile`: real cross-device fallback and its
    don't-overwrite-destination guard, exercised against `/tmp` vs
    `/dev/shm` (skipped if unavailable or co-located on the same device).
  - `RestartLogTailers`: a real `hpcloud/tail`-backed test that flips
    `DBLogOnBackupStorage`, restarts, and confirms both the tailer's
    `Filename` and that it actually streams newly written content — read via
    the `ErrorLog` ring buffer rather than the tailer's raw channel, since
    `RestartLogTailers` itself starts `ErrorLogWatcher` as a concurrent
    consumer of that same unbuffered channel.
  - `RestartDBLogTailers`: confirms it resets a server's `dbLogMigrated`
    latch.
  - `NewLogTailer`: confirms old-style rotated files are pruned when
    `DBLogRotate` is on (and lumberjack-style ones are left alone), and that
    nothing is pruned when it's off.

## Known limitations / non-goals

- No automated migration *back* from backup-backed to legacy storage beyond
  "toggle the flag off and the legacy path resumes being used for new
  writes"; old files already moved to backup-backed storage are not moved
  back.
- Migration is per-server and lazy (triggered on first `DBLogDir()` call
  after the flag is on), not a bulk/eager migration across the whole
  cluster at the moment the setting changes.
- A file skipped because it's open for SST receive gets retried as soon as
  that receiver's `JobFinishReceiveFile` fires (`maybeRetryDBLogMigration`),
  not just on some later unrelated `DBLogDir()` call — but there's still no
  dedicated background retry loop. If a *new* receiver for the same log kind
  starts again immediately (scheduler-mode can't overlap the same kind due to
  its wait-cookie guard, but API-mode has no such guard), the file could in
  principle keep getting skipped back-to-back for longer than expected before
  it's actually moved.
- Repman's own main/security/internal SQL log rotation
  (`log-rotate-max-size` etc.) is deliberately untouched — this work only
  affects fetched DB working-dir logs.
