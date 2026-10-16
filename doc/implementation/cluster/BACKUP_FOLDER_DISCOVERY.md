# Backup folder discovery

User doc: `doc/backup_folder_discovery.md`. Code: `cluster/backup_discovery.go` (find and
check), `cluster/backup_discovery_register.go` (register, unregister, change tracking,
states). Issues: #2051 (discovery), #2073 (register on demand, proper backups, changes).

## Why

A backup is known only through a `BackupMetadata` in `Cluster.BackupMetaMap`,
loaded from meta files: at a server's start `FetchLastBackupMetadata` reads the
fixed `<tool>.meta.json` files, and only when the server has a backup cookie.
A backup without a meta file was invisible, and a reseed only ever used the
server's last scheduled backup (`resolveLogicalBackupPathFromMeta`,
`buildBackupCatalog`) or, without metadata, the fixed names `mysqldump.sql.gz` /
`splitdump` (`findExistingBackupPath`).

#2051 registered every `.sql` / `.sql.gz` file and dump folder it found: account
files (`grants.sql.gz`, `mysql.user.sql.gz` that older releases wrote next to a
dump) became "completed mysqldump backups" offering Reseed and Verify, every
discovered dump showed "Unknown" as its strategy, each dump was read to its end
at each automatic discovery, and the file time was shown as the dump time. #2073
makes discovery find-and-check only; registration is an operator action limited
to proper backups, and a registered backup's file is tracked for changes.

## Discovery (find and check)

`ServerMonitor.DiscoverBackups` / `discoverBackups(retry)`, called at the end of
`FetchLastBackupMetadata` (the goroutine `NewServerMonitor` starts, `srv.go`;
never the monitor ticker) unless `backup-discovery` is off (then only
`LoadAdhocBackupMetadata`), and by `Cluster.StartBackupDiscovery` (API: a
goroutine over the servers, `retry` true; 202; refused with
`ErrBackupDiscoveryInBackup` while `IsInBackup`). It writes nothing to disk but
for unregistering a discovered backup whose artifact is gone (its own meta file).

`Cluster.backupDiscoveries` (key server URL) is the one guard per server folder:
a discovery, a Register (its whole background read), an Unregister and the
server's part of the change check take it; the others get
`ErrBackupDiscoveryRunning` (API 409) or, for the change check, skip the server
until its next run. So a meta file is never written by two of them at once, and
the change check never records a change against a fingerprint a Register is
replacing.

1. `LoadAdhocBackupMetadata` first, even while a backup runs (a live entry is
   kept): a server monitor created then lists its ad-hoc and registered backups.
   `readBackupMetadataFile` loads a discovered entry with no strategy (3.1.44) as
   `BackupStrategyFull`.
2. Refused while `IsInBackup`.
3. `unregisterGoneDiscoveredBackups`: a discovered backup whose artifact is gone
   loses its meta file and map entry (and its change): the discovered entries
   never outlive their artifacts. Not one kept only in its archive
   (`OriginalRemoved`).
4. Known artifacts (`knownBackupArtifactsOnDisk`: the `Dest` of every
   registered backup and of every meta file of the folder, loaded or not, with
   their `.enc` / `.tar.enc` forms) are skipped.
5. `classifyBackupArtifact` for the other entries (unchanged from #2051: staging
   and kept-aside names, the direct reseeds' working folder and the current
   users sidecar are left out; `.tar.enc`, `.sql[.gz][.enc]` files; a folder by
   its `manifest`, its `metadata` content, or `splitdump.Detect` under a
   splitdump name). Whether it is a backup is the checks' to tell.
6. Each candidate's fingerprint (`restoreArtifactFingerprintBounded`, bound
   `backupProofWalkMax`): its size, file count and newest mtime for the list; a
   folder beyond the bound is listed unchecked (it can not be registered: its
   changes could not be tracked). Modified within `backupDiscoverySettle` (1
   min): `settling`, not read.
7. The cheap check, `checkBackupCandidate`, reused from the previous discovery's
   result while the candidate's size, file count and mtime did not change (not
   with `retry`; never for one deferred by the budget):
   - a dump file: `readDiscoveredStream` with `stopAtTable` (the read ends at the
     first `CREATE TABLE`) and `readMax` (`backupCandidateReadMax`, 64 MiB
     decompressed: beyond it, `unchecked`, Register reads it). No table at its
     end: `not-backup`, the reason from `notABackupReason` (empty, accounts only
     when an account line was seen: `CREATE USER` / `ROLE`, `GRANT`, `ALTER
     USER`, `SET PASSWORD`, `SET DEFAULT ROLE`, a client's version comment before
     them included; else no `CREATE TABLE`). Not readable: `unreadable`.
     `backupDiscoveryMaxPerRun` (8) dump files are read per discovery, the others
     `unchecked` and `deferred`.
   - a folder: `inspectDiscoveredFolder` on its names and small files (manifest,
     metadata through `readDiscoverySmallFile`): `incomplete` for a completeness
     failure, `not-backup` when it holds no table file. Not budgeted: no content
     is read.
   - a `.tar.enc`: `unchecked` (its read is a decrypt to its end: Register's).
8. The candidates, newest first, are bounded by `backup-discovery-candidate-max`
   (`backupDiscoveryCandidateMax`, default `ConstBackupDiscoveryCandidateMax`
   100, 0 = default): the oldest are dropped into a fresh slice (T18), the drop
   count kept in the set; `checkBackupDiscoveryStates` raises WARN0250 while it
   is not 0. The set (`backupCandidateSet`) replaces the server's in
   `Cluster.backupCandidates`, whole: readers never see one being built.
   `pruneBackupRegisterResults` then drops the Register outcomes of artifacts no
   longer listed.

The cheap checks bound a discovery's reads: a large dump is read to its first
table, its content once (until it changes).

## Register

`Cluster.RegisterBackup(serverURL, path, takenAt)` (API `POST
/backups/register`, CLI `backup register PATH --server`): synchronous refusals
first, no read: unknown server; `path` not a direct entry of the server's backup
folder (`filepath.Dir(path)` must be the folder: no traversal); `takenAt` in the
future; not a logical backup (`classifyBackupArtifact`); already known; the last
discovery found it `not-backup` (`ErrBackupNotABackup`) or `incomplete`
(`ErrBackupNotRegisterable`). Then `startBackupRegistration`: refused while a
backup runs, while it is verified (a change), while it settles; the server's
guard taken; the outcome recorded in `backupRegisterResults` (key path: running,
then the refusal kept for the list, or deleted on success) and the read run in a
goroutine: an artifact is read to its end, never in a request.

`registerDiscoveredBackup(path, tool, split, prev, takenAt)`:

1. The fingerprint before (`restoreArtifactFingerprintBounded` with
   `backupProofWalkMax`; refused beyond it, and without inode and ctime: its
   changes could not be tracked, `artifactUnchanged` fails closed).
2. A new id: the artifact's mtime, bumped while taken; a change keeps `prev`'s.
3. The metadata: logical, ad hoc (`<tool>.<id>.meta.json`), no retention,
   `Discovered`, `BackupStrategyFull`, `TimeSource` file-time until the read
   says otherwise.
4. `inspectDiscoveredBackup` reads it to its end as a restore would (below). Not
   a backup (`NotABackup`) or not completed: refused with the reason, nothing
   written.
5. `takenAt`: set as the time (`TimeSource` operator) only when the backup
   records none (`file-time`); refused otherwise.
6. The fingerprint after must equal the one before (`artifactFingerprintsEqual`):
   a file changed during the read is refused ("register it again once the copy
   ends").
7. `Registration{Artifact, RegisteredAt}`. A change (`prev`) carries over what is
   not read from the file: `Verification`, `VerificationRuns`,
   `VerificationRunSequence` (the GUI marks the result as taken before the
   change: its `verification.artifact` differs from `registration.artifact`),
   the archive snapshot fields, the session.
8. `writeDiscoveredBackupMeta`: the meta file written to `<path>.tmp` and
   renamed (never a half-written meta file), `prev`'s removed when its name
   differs, then `BackupMetaMap.Set` (a new pointer: readers holding the old one
   see a consistent old entry).

On success the candidate is dropped from the set and the backup's change
cleared.

`Cluster.RegisterBackupChange(id, takenAt)` (API `POST /backups/{id}/register`,
CLI `backup register ID`): a discovered backup only (`ErrBackupNotDiscovered`);
refused when unchanged (no recorded change and `artifactUnchangedBounded`:
`ErrBackupUnchanged`); then as above with `prev`.

## Unregister

`Cluster.UnregisterBackup(id)` (API `POST /backups/{id}/unregister`, CLI `backup
unregister`): a discovered backup only; under `backupVerificationMu` (as
`DeleteBackupByID`) and refused while it is verified; the server's guard taken;
its meta file removed, its map entry, change and Register outcome dropped. The
backup file is not touched (F1). A discovery is started so it is listed again.

## Change tracking

`Cluster.checkRegisteredBackups`, started with `trackTickGoroutine` from Run's
`heartbeats%30` block next to `refreshBackupProofs` (never `goRun`: the tick
waits for those), one run at a time (`backupRegistrationChecking`
CompareAndSwap), each server under its guard (skipped while a discovery or a
registration holds it). It runs whatever `backup-discovery`: the refusals depend
on it.

For each discovered backup of the server:

- kept only in its archive: no change (nothing on disk to compare);
- no `Registration` (registered by 3.1.44): adopted, `backupDiscoveryMaxPerRun`
  per run, not while verified nor while it settles (`adoptDiscoveredBackup`):
  its fingerprint as it is now (`Adopted`), Full, its time source file-time when
  its start and end are equal (3.1.44 set both to the file time or the dump
  footer: the footer can not be told apart, an accepted imprecision of the label
   only), and the cheap check: `not-backup` flags it `NotABackup`, not completed,
   the reason in `DiscoveryNote`.
- a record of a scheduled backup file (`isDuplicateScheduledBackup`: its `Dest`
  is one of `scheduledLogicalBackupNames`, `mysqldump.sql.gz`, `splitdump`,
  `mydumper`, `dumpling`, or their `.enc` / `.tar.enc` forms, in the server's
  backup folder; by name, since the scheduled metadata is one per tool and
  moves between `mysqldump.sql.gz` and `splitdump` with the split mode, so it
  does not tell which files the scheduled backups wrote): removed
  (`removeScheduledBackupRecord`: its meta file, its entry, change and Register
  outcome; logged; not while verified), before any adoption or fingerprint. A
  3.1.44 discovery registered such files; each scheduled backup rewrites them,
  so the record describes nothing. The discovery removes them too
  (`unregisterGoneDiscoveredBackups`). Until removed, `backupUseRefusal`
  refuses it (`ErrBackupDuplicateScheduled`) and `discoveredBackupPaths` skips
  it: the automatic restores take the scheduled backup's file. The name check
  reads no file. A file of those names no record describes is listed as a
  candidate (`Scheduled`), and registering one, or one replaced by hand, makes
  it the scheduled backup (see the lock below): one record per file. This narrows the issue's "nothing removed
  automatically" to the records that describe nothing.
- else `artifactUnchangedBounded(Dest, Registration.Artifact,
  backupProofWalkMax)`: the fingerprint verification uses (#2074), same rules: a
  replaced file (other inode or ctime) is a change, a fingerprint without inode
  and ctime matches nothing, a folder beyond the bound reads as changed.
  Unchanged: the change is cleared (the registered file read as registered
  again, e.g. a mount back). Changed: a `BackupRegistrationChange` (was, now or
  the error, `describeBackupChange`: sizes, file counts, modification time, or
  "replaced or rewritten, same size and modification time") replaces the
  backup's in `Cluster.backupChanges`.

`checkBackupDiscoveryStates` (every tick, `cluster_set.go`, memory only): one
WARN0249 per server (key + server URL) listing its changed backups; a change
whose backup was deleted since is dropped there. WARN0250 per server whose last
discovery dropped candidates. Both `ErrFrom: "BACKUP"`, `ServerUrl` set, like
the other backup states (WARN0238, WARN0248, ERR00117); they go to the main
`StateMachine`, as those do: there is no backup state machine.

Restoring the original from its archive puts back a new file (another inode):
it does not match the registration, the operator registers it (a change). A
change clears by itself only when the registered file itself comes back.

## The lock on replication-manager's own backups

No backup file is used without being checked. `WriteBackupMetadata` calls
`recordBackupRegistration` before it writes the metadata of a completed logical
backup (after `finalizeBackupEncryption`: the final file): its fingerprint in
`Registration`, under `backupMetaMutex`. A file whose fingerprint can not be
taken (a folder beyond `backupProofWalkMax`, no inode and ctime off Linux) gets
none and is used as before. Physical backups get none: Register can not read
them.

The change check covers every logical backup of the server, discovered or not;
one without a fingerprint, taken before this release, is adopted in place
(`adoptOwnBackup`: its fingerprint as its file is now, `Adopted`, its metadata
file rewritten through a temporary file). A record whose file a newer record
owns (`isSupersededBackupRecord`, `backupRecordFor`: the newest record of a
`Dest`; a scheduled backup rewrites its file and its record) and a backup kept
only in its archive are not compared. No check while `IsInBackup`: a backup
rewrites, renames (`backup-keep-until-valid`) or encrypts its files; the last
check's changes are kept until it ends.

The automatic reseeds check the file when they are about to use it
(`backupFileLocked` at the three call sites of `isDiscoveredBackupPath` in
`srv_job_backup.go`, and `ReseedFromBackupID`): the newest record of the path,
`backupUseRefusal`, then `artifactUnchangedBounded` against its fingerprint,
without waiting for the 30-heartbeat check. A backup restored by
`restoreArchivedOriginal` from replication-manager's own snapshot
(`OriginalRemoved`) is not locked.

`RegisterBackupChange` takes any logical backup. For a scheduled backup
(default line, not discovered) the registered file becomes the scheduled
backup under a new id: `writeScheduledBackupMeta` writes `<tool>.meta.json`
(through a temporary file, under `backupMetaMutex`), drops the previous
scheduled records of the tool from the list, sets `LastBackupMeta.Logical` and
the backup cookie. `RegisterBackup` on a scheduled file name (replaced by hand,
or left without a record) does the same: one record per file, never a
discovered one.

## Untrusted content

The artifacts are placed by hand: their reads are hardened.
- Symbolic links: `classifyBackupArtifact` skips a link (never listed),
  `RegisterBackup` reads the entry with `os.Lstat` and refuses a link, and
  `discoveryFolderNames` refuses a dump folder holding one: a link may point
  outside the backup folder. An archive's link members are not kept (only
  regular members are read).
- Panics: `checkBackupCandidate` and `inspectDiscoveredBackup`, the two entry
  points of the parsing, recover a panic (the candidate unreadable, the
  registration refused, `logDiscoveryPanic` logs it with its stack), as the
  backup verification does; the register goroutine recovers any other.
- Bounds: memory is bounded (lines kept to 4 KiB, a GTID statement to 1 MiB,
  an archive's names and small files); a full read is not bounded in time, a
  dump being read whole at Register, but it ends when replication-manager
  stops (`cluster.exit`, `errBackupDiscoveryStopping`, checked per line and per
  archive member).

## Refusals

`backupUseRefusal(meta)`: for any backup, `ErrBackupChangedSinceRegistered` while
a change is recorded (with the change's description); for a discovered backup,
also `ErrBackupDuplicateScheduled` for a record of a scheduled backup file and
`ErrBackupNotABackup` when flagged; nil otherwise. Used by
`ReseedFromBackupID` (before the other checks), `buildBackupCatalog` (skipped;
discovered backups do not reach `LastBackupMeta`, the check makes it explicit)
and `refreshBackupProofs` (not proven). `verifiableLogicalBackup` refuses a
`NotABackup` entry; a changed backup stays verifiable on demand. Nothing runs on
a changed file by itself.

## Inspection

`inspectDiscoveredBackup` reads as a restore reads; what it finds goes to the
position fields, the times, `Completed` and `DiscoveryNote`:

- mysqldump file: `inspectDiscoveredDump`, `readDiscoveredStream` through `openRestoreArtifactStream`
  (decrypts `.enc` with `backupRestorePasswords`: no metadata needed, every
  password of the history is tried), gzip detected by its magic, read to its
  end (gzip CRC). Lines are kept to 4 KiB (`readDiscoveryLine`). Read on the
  client's own lines only, by their start (commented or not; a data line can
  hold any text): the header (`-- MySQL dump` / `-- MariaDB dump`), the
  position (`discoveryBinlogRegex`: `CHANGE MASTER` with or without a
  connection name, `CHANGE REPLICATION SOURCE`), the GTID (`SET GLOBAL
  gtid_slave_pos`, which MariaDB writes after the data; `SET
  @@GLOBAL.GTID_PURGED` with or without the 8.0 `'+'` comment). The first of
  each is kept. A MySQL client writes a GTID set of several sources over several
  lines: a `GTID_PURGED` statement not closed on its line is joined with its
  continuation lines (GTID set parts, `discoveryGtidContinuation`) up to its
  `';`, at most 1 MiB, as the backup's own reader joins it; any other line
  drops it. The last line: the
  footer and its time (`parseDiscoveryTime`, local time zone; `TimeSource`
  dump-footer). A `CREATE TABLE` at a line's start sets `table`; none at the
  end: `NotABackup`, refused. Header without footer: not completed. No header: noted (not a dump client's file, or `--skip-comments`),
  its completion not checked; without a position it is refused like any.
- splitdump folder: its `metadata` for the position; its manifest parsed and
  validated, each listed file present, no `.sql` / `.sql.gz` file it does not
  list, a table file (`discoveryTableFile`, `<db>.<table>-schema.sql[.gz|.zst]`)
  among its schema files or `NotABackup`. At Register a plain folder is then
  read to its end by `validateSplitDumpBackup` (the integrity verification's:
  every listed file, gzip to its end, no recognized artifact unlisted). No manifest: not completed. The splitter
  writes it last (`SplitDumpLineParser`, after `metadata`; every splitdump
  since 3.1.24 has one), so a folder without it may be a dump or a copy that
  did not end, and its files can not be checked; the manifest arriving changes
  the file count and the refresh pass inspects it again. The `metadata` and
  `manifest` of a folder are read through `readDiscoverySmallFile`
  (`discoveryArchiveSmallFile`, 16 MiB at most), in `classifyBackupArtifact`
  too: an unrelated folder's large `metadata` is never loaded whole.
- `.tar.enc`: `readDiscoveredArchive` reads the decrypted tar stream to its
  end through `openRestoreArtifactStream` (its integrity code is checked; a
  foreign key fails it), keeping in memory its members' base names
  (`discoveryArchiveMaxMembers`, 500,000 at most: an archive with more is not
  completed) and its `manifest` and `metadata` (16 MiB at most;
  `splitdump.ParseManifest` / `ParseMetadata`): nothing is written to disk.
  `discoveredArchive.classify` then sets the tool from that content, as for a
  folder (a `manifest`: splitdump; mydumper's `Started dump at`: mydumper;
  another `metadata`: splitdump; neither: the name's guess is kept), so a
  mydumper archive under any name is never restored as a splitdump.
  `inspectDiscoveredFolder` runs the folder's checks through read/exists
  functions, on a plain folder or on that. Base names: the folders are flat, a
  nested member would share one, harmless for the checks.
- mydumper folder: `Finished dump at` in its `metadata`, a table file among its
  files or `NotABackup`, its start and end (`parseDiscoveredMyDumperTimes`:
  `Started dump at` / `Finished dump at`, `# ` before them since 0.14.1;
  `TimeSource` mydumper-metadata), then the position
  (`parseDiscoveredMyDumperPosition`): both forms the reseed reads
  (`JobParseMyDumperMetaNew` / `Old`), `File =` / `SOURCE_LOG_FILE =` and their
  position and GTID lines, or the indented `Log:` / `Pos:` / `GTID:` of
  mydumper before 0.14.1; the first of each. Read from the content, not by the
  installed mydumper's version: a copied folder may be from another.

A completed backup with neither a binary log file nor a GTID set
(`hasReplicationPosition`) gets the `DiscoveryNote` "no replication position"
(after a note it already has) and stays completed: listed, refused by
`ReseedFromBackupID`. Without one, the reseed's last step
(`pointLogicalReseedReplica`, `pointSlaveToMasterAutoDetect`) would point the
replica at the primary's current position: a silent divergence.

A dump is read whole only at Register (and its change); a discovery reads it
to its first table.

## Use

Registered discovered backups never enter `buildBackupCatalog` (it takes
`LastBackupMeta` only, which discovery does not touch; `backupUseRefusal`
also skips them). The legacy fallback of
the automatic reseeds and the flashback (no catalogued backup: the fixed names
through `findExistingBackupPath`, then `<backup dir>/<fixed name>`) skips them:
`findExistingBackupPath` passes over a discovered artifact
(`isDiscoveredBackupPath`), and the three callers refuse a fixed path that is
one before their existence check, the backup cookie kept (the backup exists,
it is not for automatic use). `Cluster.ReseedFromBackupID(server, id)` reseeds on request:
logical (`mysqldump`, `mydumper`) only (`ErrBackupDiscoveryNotLogical`),
not refused by `backupUseRefusal`, `Completed`, a position (`hasReplicationPosition`), not `OriginalRemoved`, artifact present (`backupArtifactExists`),
target not the primary; then `JobReseedLogicalBackupFromPathWithOptions` (the
restic restore's path). The position comes from the artifact during the
restore, not from the meta file.

## API and ACL

- `POST /api/clusters/{clusterName}/backups/discover`
  (`handlerMuxClusterBackupDiscover`): ACL rule `/backups/`; the handler also
  requires `db-backup` (`requestACLUser`): the rule matcher falls back from
  `/backups/` to the `/backups` list rule (`show-backups`) when the first
  denies. 409 while a backup runs.
- `GET /api/clusters/{clusterName}/backups/candidates`
  (`handlerMuxClusterBackupCandidates`, `GetBackupDiscovery`): ACL rule
  `/backups/candidates` (`cluster-show-backups`), before `/backups/`. The
  candidates per server with their last Register's outcome, and the changes.
- `POST /api/clusters/{clusterName}/backups/register` (body `server`, `path`,
  `takenAt` RFC 3339), `POST .../backups/{backupID}/register` (body `takenAt`,
  optional), `POST .../backups/{backupID}/unregister`: ACL rule `/backups/`, and
  the handlers require `db-backup`. `writeBackupRegisterError`: 404 unknown
  backup; 409 a backup, a discovery or registration, a verification runs,
  already registered, unchanged; 422 not a backup, not registerable, not
  discovered. Register answers 202 (the read runs in the background),
  Unregister 200.
- `POST /api/clusters/{clusterName}/servers/{serverName}/actions/reseed/backup/{backupID}`
  (`handlerMuxServerReseedFromBackup`): ACL rule `/actions/reseed/`
  (`db-restore`), and the handler requires `db-restore` too (the rule matcher
  falls back to a shorter rule when the specific one denies); 404 for an
  unknown backup (`ErrBackupNotFound`), 409 while a backup of the cluster runs
  (`ErrBackupInProgress`: a scheduled backup may be rewriting the artifact; a
  second reseed of the server is refused by `TrySetInReseedBackup`), 400 for
  the other refusals. The `IsInBackup` check and the reseed's start are not
  atomic: a backup started between them may rewrite a scheduled backup's
  artifact the reseed restores (a discovered one is never rewritten by a
  backup). Accepted: a window of a few milliseconds on an operator's request.

## GUI

`Pages/Maintenance/index.jsx`:

- **Found in backup folders** (accordion): the candidates of every server
  (`getBackupCandidates`, polled with the backup list in `Home` and
  `Slideshow`), their check and reason, the Register outcome; the Discover
  button (moved there from Current Backups); a Register action per registerable
  candidate opening a dialog with an optional time (`datetime-local`, sent as
  RFC 3339); the WARN0250 drop per server.
- **Current Backups**: under the times, where they come from
  (`timeSourceLabel`); status icons for a backup not a backup and one changed
  since registered (the change's description, the last refused Register); the
  verification tooltip marked "taken before the file changed"
  (`verificationBeforeChange`); Reseed disabled with the reason in its tooltip
  (`reseedRefusal`: not logical, not a backup, changed, not completed, archived
  only, no position); for a discovered backup, Register the change (when
  changed) and Unregister in place of Delete (Delete would remove the backup
  file, which the operator placed). Verify disabled for a non-backup.
- **Settings → Backups**: `backup-discovery` (switch), `backup-discovery-candidate-max`.

The strategy column shows Full for discovered backups (`getBackupStrategy`
unchanged: the backend records 1).

Meta files are written 0644 like every backup meta file (`WriteBackupMetadata`,
`srv_bck.go`): one permission for the folder's meta files.

Retention: a discovered backup is ad hoc without a retention window
(`RetentionDays` 0, no `RetentionDuration`): `retentionDeadline` never expires
it, so the ad-hoc purge leaves it; the `backup-keep-until-valid` rotation
(`.old`, `.err`) handles the scheduled artifacts only; the restic purges handle
snapshots. It is bounded by the artifacts in the folder (unregistered when its
artifact is gone, `unregisterGoneDiscoveredBackups`) and unregistered by the
operator.

Meta files of registered backups are replication-manager's own files in the
backup folders, not in the config repository: T25 does not apply to them.

## Tests

`cluster/backup_discovery_test.go`: classification; the candidates (each "not a
backup" case: accounts with and without the client's header, empty, no `CREATE
TABLE`, a splitdump of views only, a mydumper dump without table; incomplete
folders; an archive unchecked; nothing registered); the candidate cap (oldest
dropped, fresh array, WARN0250 raised and cleared); the cheap read bound;
Register (Full, position, time source, fingerprint, the candidate dropped,
already registered, a restart); the refusals (at once after a discovery, after
the read otherwise, outside the folder); each completeness failure (no footer,
a listed file missing, an unlisted file, no manifest, mydumper not finished, a
foreign key, the archive member cap); encrypted archives; each time source and
the operator's time (refused for a dump with its footer, in the future); the
mydumper times; a change (WARN0249, reseed refused, catalog skip, verify
allowed, a broken change refused, a good one registered keeping its id and its
verification, unchanged refused); a file replaced with its size and mtime put
back; a change cleared when the file reads as registered again; Unregister
(file kept, candidate again, a taken backup refused); the adoption of 3.1.44
entries (Full, fingerprint, a non-backup flagged and refused by a reseed and a
verification, unregistered); the reseed refusals; no position; live entries
kept; the fallback skip; a copy in progress; a removed artifact unregistered;
one discovery or registration per server; a multi-line GTID set; the per-run
read cap; the refusal while a backup runs; the retry on request; a splitdump
without its manifest. `cluster_acl_test.go`: the reseed route needs
`db-restore`; the candidates route `cluster-show-backups`, register and
unregister `db-backup`.

Regtest (T13, real servers): `testBackupDiscovery` (`regtest/test_backup_discovery.go`,
run by name). An accounts file is listed not a backup and refused (D9). For a
mysqldump file, a splitdump folder and a mydumper folder (mydumper installed): a
backup of the primary is copied under another name, backdated past the settle
window, listed as a candidate and not registered (D2), registered (D3: position
and GTID as the backup recorded, Full, fingerprint), the first replica reseeded
from it (D4, checked as `TestLogicalReseed` checks a reseed; mydumper needs
myloader). The mysqldump copy is then edited (a marker schema before its
footer): WARN0249, the reseed refused, the change registered, the reseed loads
the edit (the marker read on the replica, then dropped there with
`sql_log_bin=0`) (D5); then truncated: WARN0249, Verify fails, Register of the
change refused (D6). Each copy is unregistered (file kept, state cleared, a
candidate again) and removed by the test (D7). A splitdump copy whose
`metadata` holds no position is registered with the "no replication position"
note, and its reseed refused (D8). Last, the record a 3.1.44 discovery made of the scheduled
`mysqldump.sql.gz` is written by hand and removed by the next discovery, the
file kept (D10).
