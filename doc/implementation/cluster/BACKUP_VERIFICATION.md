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
  file (`writeBackupMetaFileLocked`).

API: `POST /api/clusters/{clusterName}/backups/{backupID}/verify[/{level}]`
(`handlerMuxClusterBackupVerifyDefault` / `handlerMuxClusterBackupVerifyLevel`,
POST only); ACL rule `/backups/` (`db-backup`). 409 for `ErrBackupInProgress` /
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

## Restore level

`verifyLogicalBackupRestore`:

- `openBackupVerificationServer`: `backup-verification-host` must be set. A
  server outside the cluster is connected with `dbhelper.MySQLConnect` and
  `backup-verification-credential`, its version read, PostgreSQL refused. A
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
  depth 0 after `VALUES`, outside quoted strings). A view's placeholder table
  (MariaDB before 10.6) is not expected as a table.
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
- `endVerificationReseed`: releases the reseed flag; leaves maintenance when it
  set it and replication resumed; after a failure the replica stays in
  maintenance, replication stopped, with an ERROR line: its data is the partial
  test restore.

`dbhelper` functions (T1, T23): `DropDatabaseIfExists`, `UseDatabaseConn`,
`CountTableRows`, `GetSchemaObjectNames`, `GetEventScheduler`, `SetEventStatus`
(the keyword by version, the definer's user and host quoted, not restricted to
identifier characters: a host is often `%`), `SetGtidPurged` (MySQL/Percona only:
MariaDB and PostgreSQL get `ErrSetGtidPurgedNotMySQL`); PostgreSQL gets
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

- `Settings/BackupSettings.jsx`: the switch, the level, the host, the
  credentials (`SetCredentialsModal`, base64 like the other `set` values).
- `Maintenance/index.jsx`: a verification icon in the status column (tooltip:
  level, time, warnings, notes or error; `verificationClass`: green ring for
  integrity, blue for restore, the `--info-secondary-color` token added to both
  themes, amber for a pass with warnings, red for a failure, an amber pulse while running, a dashed outline when never
  verified) and the **Verify integrity** / **Test restore**
  actions (`verifyBackup` thunk).

## Tests

- `cluster/backup_artifact_validation_test.go`: the artifact checks, encrypted
  single file and splitdump, a changed byte failing the HMAC.
- `cluster/backup_verification_test.go`: integrity level cases (footer,
  comments, position, sidecar, splitdump), encrypted backups, the recorded
  result and ERR00117, the on-demand refusals and run.
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
