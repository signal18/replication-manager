# Backup verification (#2023)

Checks that a logical backup can be restored: an integrity level that reads it
as a restore would, and a restore level that loads it into a verification
server outside the cluster and compares it with the dump. User doc:
`doc/backup_verification.md`.

Off-switch (T14): `backup-verification` (`Conf.BackupVerification`, default
`false`, per cluster, `SwitchBackupVerification`). The on-demand action runs
whatever the switch. Settings: `backup-verification-level`,
`backup-verification-host`, `backup-verification-credential` (a secret: listed
in `DecryptSecretsFromConfig`, `GetEncryptedValueFromMemory`, masked in the API
change log, set through `SetBackupVerificationCredential`, `json:"-"`: the
settings route encodes the configuration as it is).

Code: `cluster/backup_verification.go` (runner, integrity level, states),
`cluster/backup_verification_restore.go` (restore level),
`cluster/backup_artifact_validation.go` (artifact checks shared with the
pre-restore check), `utils/dbhelper/backup_verification.go` (the SQL of the
restore level).

## Scope

`verifiableLogicalBackup`: a completed mysqldump backup still on disk. Other
tools, an incomplete backup, or a backup only in its archive
(`OriginalRemoved`) return `ErrBackupNotVerifiable`, recorded nowhere.

## Runner

- On demand: `VerifyBackupByID(id, level)` checks the backup, refuses one in
  progress (`isBackupMetaInProgress`), reserves it (`Cluster.backupVerifying`,
  id -> level; `ErrBackupVerificationRunning` when already reserved), then runs
  in a goroutine that waits for a global backup slot (`waitForBackupSlot`) and
  releases it when done. Reserved before the wait, at most one goroutine waits
  per backup. Not a refusal when all slots are in use: a backup's slot is freed
  a monitor tick after the backup ends (state resolution), so a verification
  asked right after a backup would be refused.
- Once it holds its slot, an on-demand run claims the backup it reads
  (`claimBackupVerificationRead`: `Cluster.backupVerificationReads`, id ->
  source and whether the file is reused, i.e. not ad hoc), under
  `backupVerificationMu`, only while no logical backup runs (`InLogicalBackup`,
  the restic push of a logical backup included); it then checks the backup is
  still verifiable (its original archived meanwhile). A logical backup sets its
  state through `startLogicalBackupAfterVerification`, under the same mutex,
  once no claim holds a reused backup of its server: a scheduled backup
  rewrites its predecessor's file. `removeArchivedOriginal` waits while its
  backup is claimed (`waitBackupVerificationRead`). The waits end with the
  claim: the verification holds its slot already and waits on nothing the
  backup holds. Each wait polls (`backupVerificationPoll`, 1 s) and ends at
  `backupVerificationWaitMax`: `backup-verification-timeout`, at least
  `backupVerificationWaitFloor` (4 h), so that a timeout set to cap a test
  restore does not make a claim give up behind a long backup (F2/F4: a hung
  verification, or an integrity read on a dead volume, never holds the backups
  off for good). The backup then goes on (the verification still reading
  fails; the log names the host of its running record, a replica mid-load
  stays in maintenance), the archive removal goes on, a claim gives up with
  `errBackupVerificationClaimTimeout`: on a backup without a result the
  verification is recorded as `skipped` ("not run": listed, not a failure, no
  ERR00117); an earlier passed or failed result is kept and the skip only
  logged (`TestVerificationNotRunIsSkipped`). `errBackupVerificationStopping`
  records nothing. The
  floor and `backupVerificationPoll` are package variables the tests shorten:
  they restore them before their goroutines end and do not run in parallel. `TestBackupVerificationClaimAndBackupStartExclusive`
  starts both at once, 20 times: one goes on, the other waits until it ends.
  `PurgeExpiredAdhocBackups` (no caller today) skips a backup in
  `backupVerifying`. `InLogicalBackup` is a plain flag, written by the backup
  job without this mutex: the mutex orders the claim against the backup's
  start, which sets it under the mutex.
- `backupVerificationMu` makes a reservation and `DeleteBackupByID` exclusive:
  a backup is not deleted between its checks and its reservation, and
  `DeleteBackupByID` refuses a reserved one. It covers the reservation only,
  never a verification run.
- After a backup: `JobBackupLogicalWithOptions` captures its meta before
  `WriteBackupMetadata` (keep-until-valid can roll `LastBackupMeta.Logical` back
  to the previous backup) and calls `verifyBackupAfterBackup` when the backup
  succeeded: it runs inside the backup's own slot and state, before the restic
  push, so `backup-archive-remove-original` can not remove the original first.
- `verifyBackup` records `BackupVerification{Status: running}`, runs the levels,
  records the result: `meta.Verification` under `backupMetaMutex`, and the meta
  file (`writeBackupMetaFileLocked`). The levels parse untrusted content: a panic
  there is recovered in `verifyBackup` and recorded as a failed verification
  (its stack logged, 4 KiB at most), never reaching the monitor or the backup
  job; a replica's own defers have run by then (it stays in maintenance).

Verification server API: `GET /api/clusters/{clusterName}/backups/verification-server`
(`GetBackupVerificationServerState`: replica, marked, empty, schemas) and
`POST .../backups/verification-server/mark` (`MarkBackupVerificationServer`,
the user's name recorded in the mark; a replica of the cluster is refused).
ACL rule `/backups/` (`db-backup`); the mark also needs `db-restore`, checked
in the handler: it lets a test restore drop schemas there. The dashboard row
shows the state and the **Mark as verification server** button, after a
confirmation listing the schemas.

API: `POST /api/clusters/{clusterName}/backups/{backupID}/verify[/{level}]`
(`handlerMuxClusterBackupVerifyDefault` / `handlerMuxClusterBackupVerifyLevel`,
POST only: two handlers so that Swagger lists `{level}` only on its route);
ACL rule `/backups/` (`db-backup`); the restore level also needs
`db-restore` (`GrantDBRestore`, the reseed's), checked in the handler once
the level is known (the URL can not tell it apart). No wildcard CORS header. 409 for `ErrBackupInProgress` /
`ErrBackupVerificationRunning`, 400 for an
unknown id, an unknown level or a backup not verifiable.

## States

`checkBackupVerificationStates`, every tick (`cluster_set.go`), main state
machine, server level:

- CINF0016 (info) for each backup in `backupVerifying`;
- ERR00117 (error) when a server's `LastBackupMeta.Logical` failed its last
  verification: a reseed from it would fail until a new backup is taken. The
  state goes once a later backup becomes the server's last logical backup, or
  the backup passes a new verification.
- WARN0248 (warning) when that verification passed with `Warnings`: the
  backup lacks a kind of object its source has (`compareSource`); same
  lifetime.

## Integrity level

`verifyLogicalBackupIntegrity`:

- Single file: `readMysqldumpArtifact` opens the artifact through
  `openRestoreArtifactStream` (an encrypted one has its HMAC verified by
  `OpenDecryptedFile` before any plaintext), gunzips it to its end (a `.gz`
  name must be gzip), keeps the first 4 KiB of each line only, and finds: the
  client's header comment in the first 5 lines (a MariaDB 11 dump starts with
  its sandbox line), the last non-empty line, the position with
  `getBackupRegexPatterns` (the regexes the backup parsed it with). Without the
  header the dump has no comments, so no footer: a note, not a failure.
- `matchBackupPosition`: nothing to check when the metadata has no position; a
  dump without one, or with another, fails.
- Splitdump: `resolveRestoreArtifactPath` (an encrypted one is decrypted to a
  temporary directory, as a restore does), `validateSplitDumpBackup` (manifest,
  every listed file present and read to its end, no unlisted file), then
  `splitdump.ReadMetadata` and the position.
- `SplitUser`: the `mysql.users.sql.gz` sidecar next to the backup reads to its
  end.

## Pre-restore check

`checkLogicalRestoreArtifact`, at the start of `JobReseedMysqldump` and
`JobReseedSplitdumpWithMysql`, the two places every logical restore of a
mysqldump backup goes through: a single file is read to its end and must have
its footer when it has its header; a plain splitdump goes through
`validateSplitDumpBackup`; an encrypted splitdump is checked by its HMAC only,
the restore decrypts it right after. It runs once per restore: not in the
reseed preparation, which can run inside a request or a monitoring goroutine.

A backup already proven is not read twice: `verifyBackup` takes the
artifact's fingerprint (`restoreArtifactFingerprint`: size and mtime of the
file, or of the encrypted splitdump archive; files, total size and newest
mtime of a splitdump directory) before the levels and after them, and keeps
it in `BackupVerification.Artifact` with a pass only when both are equal.
`checkLogicalRestoreArtifact` first looks for the backup whose `Dest` is the
path (or its `.tar.enc`), reads its verification under the source's
`backupMetaMutex`, and skips the read when it passed and the fingerprint is
the same now (`time.Equal`: the recorded time may come back from the meta
file). Any other case reads the artifact as before: never verified, failed,
changed, a restic-fetched copy at another path. No override skips a check
that fails: such a backup is truncated or damaged, its load would fail halfway
with the server's data half replaced.

## Restore level

`verifyLogicalBackupRestore`:

- `verifyLogicalBackupRestore` reads `backup-verification-host` and its
  credential once and passes them down: a setting changed during a run never
  splits it across servers.
- `openBackupVerificationServer`: the host must be set. A server outside the
  cluster is connected with `dbhelper.MySQLConnect` and the credential, its
  version read, PostgreSQL refused; then `ensureVerificationMark` requires the
  mark there: the table `replication_manager_schema.backup_verification_server`
  (`dbhelper.HasVerificationMark`), the statement that the server is
  disposable, which a VIP, a proxy or another cluster's server never carries.
  An unmarked server is marked (`dbhelper.MarkVerificationServer`, a row per
  cluster: who marked it, when) only when `backupVerificationServerState` finds
  it empty: no schema besides the system ones (`dbhelper.ListDatabases`), and a
  `replication_manager_schema` without objects other than the mark
  (`dbhelper.GetSchemaObjectNames`; a server a replication-manager monitors has
  its tables there). Nothing can be lost on it. Any other is refused, its
  schemas named, until marked through the API (below). The mark lives in
  `replication_manager_schema`, which is never loaded there, so a test restore
  never drops it. On it, `replication_manager_schema`
  (`backupVerificationSkippedSchemas`) is not loaded: `single` skips its
  `CREATE DATABASE`, its `USE` and what follows until another schema's
  `CREATE DATABASE` or `USE`, batches included; a splitdump skips its files. A
  server of the cluster, recognized by name or by resolved address (an alias,
  an IP address in any form, a CNAME: `sameServerAddress`, each resolution
  bounded to 3 s), goes through `verificationReplica`
  (`backup_verification_replica.go`): only one `IsIgnored()`
  (`db-servers-ignored-hosts`), not the primary, not a relay (its replicas
  would replicate the load); its own pool and version are used, never closed.
- One pinned connection with the restore session settings
  (`prepareRestoreConn`). The statements are split and batched by the
  splitdump loader's own `planAndExecSplitdump` / `forEachSplitdumpStatement`
  (so `DELIMITER`, the footer and `LOCK TABLES` are handled as in a splitdump
  reseed). Each statement runs through the reseed's own runners,
  `execSplitdumpSingle` and `execSplitdumpBatch`: a `CREATE USER` of an account
  the server already has (`mariadb.sys`, root) is applied as `ALTER USER`, an
  active plugin is not installed again, the engine's protected accounts are
  left alone, lock contention is retried. A single file goes through the same
  loader rather than the mysql client: its statements must be filtered.
- Not run: replication statements and the GTID state (`stmtReplication`:
  `CHANGE MASTER`/`CHANGE REPLICATION SOURCE`, `START`/`STOP SLAVE|REPLICA`,
  `RESET SLAVE|REPLICA`, `GTID_PURGED`, `gtid_slave_pos`), and the MariaDB 11
  sandbox line. Backups of this release write the position as a comment
  (`--master-data=2`/`--dump-slave=2`); one taken before has an active
  `CHANGE MASTER TO` and `START SLAVE`, which would make the verification
  server replicate from the cluster.
- The whole dump is loaded, the `mysql` schema included (decided for #2023).
  Application schemas (not `mysql`, `sys`, `information_schema`,
  `performance_schema`) are dropped before their load: a single file at its
  `CREATE DATABASE`, or at its first `USE` when the dump has none
  (`--no-create-db`: then also created), a splitdump for every schema of
  `ListSchemas` before the first file (then created); all are dropped again
  when the level ends, whatever its result (on a verification server).
- Splitdump order: `BuildRestorePlan` (schema, data, post), `USE` of each
  file's schema, `mysql.gtid_slave_pos` data skipped.
- `dumpInventory`: what the run statements create, read from their start with
  the versioned comments opened (`normalizeDumpStatement`): `USE`, `CREATE
  DATABASE`, tables, views, procedures, functions, triggers, events, and the
  rows of each `INSERT`/`REPLACE` (`countInsertRows`: parentheses opened at
  depth 0 after `VALUES`, found anywhere in the statement (`valuesKeyword`: a
  complete insert's column list can pass 4 KiB), outside quoted strings; an
  `INSERT ... SELECT` counts none, a dump written with `NO_BACKSLASH_ESCAPES`
  quoting can miscount: documented limits, outside what repman's dumps
  write). A view's placeholder table (MariaDB before 10.6) is not expected as
  a table.
- `compare`: per application schema, every object of the inventory must exist
  (`dbhelper.GetSchemaObjectNames`) and every table must hold its rows
  (`dbhelper.CountTableRows`, an exact `COUNT(*)`); the first 10 differences
  are reported. System schemas are loaded, not compared. The note counts the
  tables, rows and other objects compared.
- `compareSource`: the restore checks restore == dump; this checks dump ==
  source, which nothing else does (`getDumpParameter` adds `--events=false`
  while the server's event scheduler is off: such a backup has no events and
  its restore matches it). Per application schema of the inventory, the
  source's objects (`GetSchemaObjectNames` on `server.Conn`) are counted by
  kind; a kind (view, procedure, function, trigger, event) the source has and
  the inventory none of is a warning (`BackupVerification.Warnings`, WARN0248),
  not a failure: the backup restores, without them. Whole kinds only, so an
  object created or dropped since the backup is no difference. A source that
  is down or can not be read leaves a note.

Restore on a replica of the cluster (`verifyLogicalBackupRestore`, replica
branch), a reseed from the backup being verified built from the logical
reseed's pieces (no reseed entry point takes a given backup):

- Before: a replica replicating with MySQL GTID and a backup without a GTID set
  is refused (it would resume with auto-position from an empty executed set).
  `checkVerificationResumable` then refuses a backup the primary no longer has
  the position of (error 1236 after the load, for good), reading the position
  as `pointLogicalReseedReplica` uses it (`logicalReseedPosition`): MySQL GTID
  `dbhelper.GtidPurgedSubsetOf` (`GTID_SUBSET(@@GLOBAL.gtid_purged, set)`);
  MariaDB GTID `dbhelper.BinlogGtidPos` of the primary's oldest binary log
  (`BINLOG_GTID_POS(file, 4)`), the backup at or past it in each domain
  (`mariadbGtidCovers`); positional, the backup's file not older than the
  oldest binary log of the primary, with the same base name (a file of
  another server is refused).
- `beginVerificationReseed`: `TrySetInReseedBackup("verify")` (refused while
  another reseed runs), `SetMaintenance` unless already in maintenance,
  `StopAllSlaves`, then `stopLiveReplication`: the channels read from the
  replica (as the monitor reads them: `GetAllSlavesStatus` on MariaDB,
  `GetChannelSlaveStatus` on MySQL), the running ones stopped and read again;
  one still running refuses the load (the cached channel list can be empty or
  stale).
- `loadAndCompareForVerification` on a `GetConnNoBinlog` connection of the
  replica, `dropAfter` false: its schemas are kept. The load drops and creates
  schemas on that same session (`dbhelper.ContextExecer`), so nothing of the
  test restore reaches the replica's binary log, even when it fails.
- Events: `pauseEventScheduler` (both targets) reads the scheduler
  (`dbhelper.GetEventScheduler`) and, when ON, switches it off for the load and
  back on after it (deferred: on a verification server after the schemas, and
  so their events, are dropped). On the replica, `disableVerificationEvents`
  then sets every event left ENABLED to disabled on the replicas
  (`dbhelper.SetEventStatus` status 3: `DISABLE ON SLAVE`, `DISABLE ON
  REPLICA` on MySQL/Percona from 8.2), whatever the result and before the
  scheduler comes back: a replica where it can not is not resumed.
- `resumeVerificationReplica`: `ResetMaster` (clears what the load's
  connection pool binlogged: the drops and creates of schemas), on MySQL
  `dbhelper.SetGtidPurged` with the backup's GTID set, then
  `pointLogicalReseedReplica(meta, true, false)` with the metadata's position
  (MariaDB `gtid_slave_pos` + `MASTER_USE_GTID=slave_pos`, MySQL
  `MASTER_AUTO_POSITION`, else file/position) and `START SLAVE`.
- `endVerificationReseed(setMaintenance, resumed, touched)`: releases the
  reseed flag. Resumed: leaves maintenance when it set it. Touched (the load
  started, `touched` set just before `loadAndCompareForVerification`) and not
  resumed: the replica stays in maintenance, replication stopped, with an ERROR
  line: its data is the partial test restore. Neither (a failure of
  `beginVerificationReseed` after `StopAllSlaves`, `pauseEventScheduler`,
  `GetConnNoBinlog`, `prepareRestoreConn`): its data is intact, `StartSlave`
  restarts its managed channel and it leaves maintenance; when the start fails
  it stays in maintenance with an ERROR line.
- Timeout: `verifyLogicalBackupRestore` reads `backup-verification-timeout`
  once and runs the load and the comparison under `context.WithTimeout`: the
  dump statements (`ExecContext` on the pinned connection) and the row counts
  (`dbhelper.CountTableRows` takes the context) are canceled; the error then
  names the setting. A replica canceled during its load is touched: it stays
  in maintenance.
- Interrupted runs: `verifyBackup` reads the restore level's host and
  credential once and records the host in the running `BackupVerification`
  (its meta file). `closeInterruptedVerification`, when the meta file is read
  (`AppendLastMetadata` at the server's start, `LoadAdhocBackupMetadata`),
  records a verification still `running` that no run of this process holds
  (`backupVerifying`) as failed, "interrupted", naming the host and what a
  replica is left with (maintenance, replication stopped, event scheduler off
  if it was on), and logs it as an error. It decides under
  `backupVerificationMu` against the meta file read again: the copy it was
  given may have been read while a run was running, which since recorded its
  result there and left `backupVerifying`; that result is kept, and copied into
  the given copy (under `backupMetaMutex`), which its caller registers in
  `BackupMetaMap`: the API never shows it running
  (`TestCloseInterruptedVerificationKeepsALaterResult`).

`dbhelper` functions (T1, T23): `DropDatabaseIfExists`, `UseDatabaseConn`,
`CountTableRows`, `GetSchemaObjectNames`, `GetEventScheduler`, `SetEventStatus`
(the keyword by version, the definer's user and host quoted, not restricted to
identifier characters: a host is often `%`; backquoted with backquotes
doubled, `` `user`@`host` `` is a valid account name), `SetGtidPurged`
(MySQL/Percona only: MariaDB and PostgreSQL get `ErrSetGtidPurgedNotMySQL`),
`GtidPurgedSubsetOf` (MySQL/Percona) and `BinlogGtidPos` (MariaDB), both
`ErrGtidRetentionNotMySQL` elsewhere; PostgreSQL gets
`ErrBackupVerificationPostgreSQL`, nothing is sent.

Logging (F10, F11): the dump file is the replayable record of the load, as in a
reseed (its statements are not logged one by one). Logged around it: the load
itself (backup file, verification host, flavor and version, user; never the
password), each statement repman sends on its own (`DROP`/`CREATE DATABASE`,
`USE`, the version, object and row queries) through `LogSQL` (module
`BackupVerification`, SQL log), and each dump statement or file the test
restore leaves out (sandbox line, replication statements, `gtid_slave_pos`
data), so a replay by hand leaves out the same. `dumpStatementSummary`, used in
these lines and in a failed statement's error, masks password literals
(`sqlmask.Secrets`).

## GUI

- `Settings/BackupSettings.jsx`: the switch, the level, the timeout
  (`NumberInput`, `backup-verification-timeout`), the verification
  server, the credentials (`SetCredentialsModal`, base64 like the other `set`
  values). The server is picked from a selector: **External server** (the
  host `TextForm` and the credentials row), or a server of the cluster from
  `clusterServers` by its `url`; the primary and a relay are disabled, as in
  `verificationReplica`. A picked replica goes through a confirmation that
  says what a test restore does to it; one not ignored yet is added to the
  ignored hosts with it (`setAsIgnored`, the server action `set-ignored`), so
  `verificationReplica` accepts it. The confirmation warns (not blocking)
  when no other failover candidate would remain: another server not the
  primary, not ignored, not `Failed`/`Suspect`/`ErrorAuth`. Cancel remounts
  the selector on the saved value.
- `Maintenance/index.jsx`: a verification icon in the status column (tooltip:
  level, time, warnings, notes or error; `verificationClass`: green ring for
  integrity, blue for restore, the `--info-secondary-color` token added to both
  themes, amber for a pass with warnings, red for a failure, an amber pulse while running, a dashed outline when never
  verified) and the **Verify integrity** / **Test restore**
  actions (`verifyBackup` thunk).

## Restore catalog

A backup is proven (`BackupCatalogEntry.Proven`) when its last verification is
a pass of the restore level that recorded the file it loaded (`backupProven`:
`Artifact` set; warnings included, the backup restored without the objects
named), that file is still on disk unchanged, and the entry is local: the entry
of a backup with a restic snapshot is the snapshot, and a test restore proved
the local file, not the snapshot. An integrity pass, a failed, running or
skipped result never prove a backup.

`buildBackupCatalog` runs on the monitor tick (`HasCatalogBackupForRejoin`,
through `goRun`, which the tick waits for) and when a rejoin starts. It takes no
lock and reads no file: it reads the proof snapshot `Cluster.backupProofs`, an
`atomic.Pointer` to a map keyed by server, backup id and file (`backupProofKey`:
an id is a unix time, two servers' backups can share one). No snapshot yet:
nothing proven.

`refreshBackupProofs` builds that snapshot. `Run` starts it every 30
heartbeats with `trackTickGoroutine`, which the tick does not wait for; one runs
at a time (a call while one runs returns at once). Under each server's
`backupMetaMutex` it copies the last backups `backupProven` accepts (their key
and fingerprint), then, without the lock, compares each file with its
fingerprint, reading at most `backupProofWalkMax` entries of a folder (a larger
one is not proven), and publishes a new map holding the unchanged ones: at most
two per server, the previous map dropped whole. A backup verified now is proven
once the next refresh has found its file unchanged; nothing stores a proof
without that check. The snapshot keeps each proven file's fingerprint: a rejoin
whose selector requires a proven backup checks the file it picked again before
using it (`provenStillUnchanged`, in `JobFlashbackLogicalBackup`, without a
lock), since the snapshot can be 30 heartbeats old. `proven` is a hard gate there:
when the catalog has no proven backup, or the file picked changed since, the
rejoin is refused ("no proven logical backup"), its job row ends in error and
the server's reseeding mark is cleared, before any database call; it never falls
back to the on-disk lookup, which would take any file.

The fingerprint (`backupmgr.ArtifactFingerprint`) is the file's size,
modification time, inode and ctime (for a folder: its files' count, total size,
newest modification time and newest ctime, and the folder's inode). `touch` and a
copy can set the modification time back, not the ctime, and a replaced file gets
another inode. A fingerprint without inode and ctime (recorded before they were
added, or on a system other than Linux, where `artifactStatIdentity` reads none)
matches nothing: not proven, and the reseed reads the file again
(`verifiedRestoreArtifact`, the same comparison, `artifactUnchanged`).

`Proven` only matters to a selector with `"proven": true`, so the default
(empty) selectors are unchanged.

## CLI

`clients/client_backup.go`: `replication-manager-cli backup verify ID
[--level] [--wait] [--timeout]` posts to `backups/{id}/verify[/{level}]` and
prints the API's error with its status when the run does not start.

Each on-demand run is recorded in the backup's run history,
`BackupMetadata.VerificationRuns` (`BackupVerificationRun`: sequence, run id,
level, status, start and end time, error), the authoritative record of the run;
`Verification` stays the backup's result for the GUI, the states and the proof.
`VerifyBackupByIDRun` reserves the backup (one run in flight per backup), draws
a random run id, takes the next per-backup sequence
(`VerificationRunSequence`), appends the run `running` and writes the meta file
(`startVerificationRun`) before it returns: an accepted run is observable as
soon as the API answers `cluster.BackupVerificationStart` (`id`, `runId`,
`sequence`, `message`). The run carries that pair in its result
(`BackupVerification.RunSequence`, `RunID`) and ends in its own record and no
other (`endVerificationRunLocked`, by sequence and run id together): passed or failed through
`recordBackupVerification`; skipped through `recordBackupVerificationSkipped`
when it ends before running (no backup slot as monitoring stops, its read not
claimed, the backup no longer verifiable), which sets `Verification` only when
the backup has no finished result, so an earlier pass or failure, ERR00117 and
the proof of a test restore stay. The record is replaced in a new slice: the API
encodes the metadata without the lock.

The history keeps `backup-verification-run-history` runs (default 16; 0 or
less: the default): when a run is accepted beyond it, the oldest finished runs
are dropped first, a running one only when no finished one is left, and the kept
runs are copied into a new slice (`boundVerificationRuns`). Dropping an old run
is the history's retention, not a failing sink: no state is raised for it. When
a backup's metadata is registered from its file,
`closeInterruptedVerificationRuns` (called by `closeInterruptedVerification`)
ends the records still `running`, failed and interrupted, unless a run of this
process holds the backup; it reads the file only when a record is running.

`--wait` polls `GET backups` (a map keyed by backup ID; the API has no
single-backup read) every 10 s for the run with its sequence and id
(`backupVerifyRunOutcome`): another run's record, earlier or later, is never
taken for it. Exit 0 passed, 1 failed, 3 skipped, 4 when the run is no longer in
the history (the oldest kept has a later sequence: expired, reported at once),
1 after `--timeout` seconds (0 waits forever). The notes and warnings of a run
that ran are printed from `Verification` while it is that run's. A run lost to a
crash between its acceptance and the meta file write is recorded nowhere: only
then does `--timeout` decide.

## Tests

- `cluster/backup_artifact_validation_test.go`: the artifact checks, encrypted
  single file and splitdump, a changed byte failing the HMAC.
- `cluster/backup_verification_test.go`: integrity level cases (footer,
  comments, position, sidecar, splitdump), encrypted backups, the recorded
  result and ERR00117, the on-demand refusals and run.
- `clients/client_backup_test.go`: `TestBackupVerifyRunOutcome` (`--wait` finds
  its own run by sequence and id: a later run started before its poll, another
  run's record, an expired run; exit codes).
- `cluster/backup_verification_test.go`: `TestVerificationNotRunIsSkipped` (an
  accepted run in the meta file, running, before the call returns; each skipped
  run in its own record; a test-restore proof kept through a later skipped run).
- `cluster/backup_verification_runs_test.go`: `TestBoundVerificationRuns`,
  `TestEndVerificationRunExactRecord` (passed and failed end their own record),
  `TestCloseInterruptedVerificationRuns`, `TestVerificationRunHistoryBound`.
- Outside Linux, the tests whose point is a fingerprint that matches are skipped
  (`requireArtifactIdentity`); the fail-closed ones assert there that nothing
  matches and the reseed reads the backup again.
- `utils/backupmgr/artifact_fingerprint_test.go`: a zero inode and ctime are not
  written.
- `cluster/restore_catalog_test.go`: `TestJobFlashbackLogicalBackupRequiresProven`
  (through the rejoin: no proven backup, or one changed after the refresh, refuses
  it; job row in error, reseeding mark cleared), `TestProvenStillUnchanged` (a file
  changed after the refresh is not used as proven),
  `TestArtifactUnchangedSplitdumpCtime` (a splitdump file rewritten in place, size
  and modification time put back), `TestBackupProven` (which results prove
  a backup), `TestBuildBackupCatalogProvenFileUnchanged` (none before a refresh,
  unchanged, replaced with its size and modification time put back, removed,
  dropped from the snapshot), `TestBuildBackupCatalogProofKey` (another server's
  backup with the same id, a restic snapshot entry),
  `TestArtifactUnchangedNeedsIdentity`, `TestArtifactUnchangedBoundedWalk`.
- `cluster/backup_verification_restore_test.go`: row counting, the inventory
  of real dump statement forms, the replication statements, the comparison
  with the source.
- `utils/dbhelper/backup_verification_test.go`: the SQL, both engines.
- `cluster/cluster_acl_test.go`: the verify route needs `db-backup`.
- Regtest `testBackupVerification` (run by name, dedicated test cluster: it cuts
  the last backup short): integrity of local, remote and splitdump backups, the
  restore level when `backup-verification-host` is set (the test schema holds
  a view, a procedure, a function, a trigger and an event, with the event
  scheduler on: at least 5 other objects compared, no warning), the automatic
  verification, a cut backup failing with ERR00117 cleared by a new backup,
  and a backup taken with the event scheduler off warning that its event is
  missing (WARN0248), cleared by a new backup with the scheduler on, and the
  test restore on the cluster's replica listed in `db-servers-ignored-hosts`
  (V7): reseeded, out of maintenance, a later write on the primary replicated,
  its data equal to the primary's.
