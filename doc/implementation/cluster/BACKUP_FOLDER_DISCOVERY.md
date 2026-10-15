# Backup folder discovery

User doc: `doc/backup_folder_discovery.md`. Code: `cluster/backup_discovery.go`.

## Why

A backup is known only through a `BackupMetadata` in `Cluster.BackupMetaMap`,
loaded from meta files: at a server's start `FetchLastBackupMetadata` reads the
fixed `<tool>.meta.json` files, and only when the server has a backup cookie.
`LoadAdhocBackupMetadata` (the `<tool>.<id>.meta.json` files) had no caller but
the expired ad-hoc purge, itself without a caller. A backup without a meta file
was therefore invisible, and a reseed only ever used the server's last
scheduled backup (`resolveLogicalBackupPathFromMeta`, `buildBackupCatalog`) or,
without metadata, the fixed names `mysqldump.sql.gz` / `splitdump`
(`findExistingBackupPath`).

## Scan

`ServerMonitor.DiscoverBackups`, called at the end of `FetchLastBackupMetadata`
(the goroutine `NewServerMonitor` starts, `srv.go`; never the monitor ticker)
and by `Cluster.StartBackupDiscovery` (API: a goroutine over the servers, the
handler answers 202; refused with `ErrBackupDiscoveryInBackup` while
`IsInBackup`). A new artifact is read to its end (gzip CRC, integrity code), so
a scan is never run in a request or the monitor. `Cluster.backupDiscoveries`
(server URL) lets one scan run per server (`errBackupDiscoveryRunning`): a
config reload recreating the server, or an API scan during the startup one,
does not run a second. Every scan is refused while `IsInBackup` (the startup one too: a backup
writing between long pauses, mydumper on a large table, would be read half
written; the settle window and the refresh pass are the second line). A scan
reads at most `backupDiscoveryMaxPerRun` (8) artifacts, new or inspected again
(one budget): the rest is logged and left to the next scan. A config reload
re-reads only artifacts still unregistered: a registered one has its meta file
and is known.

1. `os.ReadDir` of `GetMyBackupDirectory`; a missing folder is no error.
2. `LoadAdhocBackupMetadata`: the ad-hoc and the previously discovered backups
   are registered from their meta files. A backup already in the map keeps its
   live entry (a running backup or verification updates it; the copy just read
   may be older): the loader replaced it before, harmless while only the
   uncalled purge used it, not once a scan runs it on a live cluster.
3. `refreshDiscoveredBackups`, over the server's discovered backups: an
   artifact gone (removed by hand) unregisters its backup (meta file removed,
   `BackupMetaMap.Delete`), so the discovered entries are bounded by the
   artifacts present (no retention purges them: they are never auto-purged);
   one not completed whose size or file count changed since its inspection
   (a copy that was still running) is inspected again under its id, once
   settled.
4. Known artifacts (`knownBackupArtifacts`, `addKnownBackupArtifact`): the
   `Dest` of every registered backup and of every meta file of the folder,
   loaded or not (a scheduled backup's file without its cookie), each with its
   `.enc` / `.tar.enc` forms.
5. `classifyBackupArtifact` for the other entries. Staging and kept-aside names
   (`.partial`, `.tmp`, `.new`, `.old`, `.err`) are skipped, and the direct
   reseeds' working folder (`directReseedSystemArtifactSubdir`) and the users
   sidecar (`mysqldumpUserSidecarPath`, plain or `.enc`): a sidecar, not a
   backup, and deleting it as a backup would remove the users of its dump. Files: `.tar.enc`
   is an encrypted splitdump (a mydumper one when its name starts with
   `mydumper`), `.sql` / `.sql.gz` / `.sql.enc` / `.sql.gz.enc` a mysqldump
   file. Folders: a `manifest` makes a splitdump; else a `metadata` file is
   mydumper's when it holds `Started dump at`, a splitdump's otherwise; else a
   folder `splitdump.Detect` accepts is a splitdump only under a splitdump name.
   File names do not decide: splitdump writes mydumper's.
   `isSplitDumpDir` (reseed path) is unchanged: the manifest only exists since
   2026-04-14 and it runs on the mysqldump reseed path only.
6. Settle: an entry modified within `backupDiscoverySettle` (1 min; a folder:
   any of its files, `artifactChangedSince`) is left to the next scan.
7. `registerDiscoveredBackup`: id = the artifact's mtime (unix), bumped while
   taken in the map or by a meta file (ids follow the folder's sorted order;
   the meta file keeps the id once written); `BackupLine` ad hoc (the meta file name
   `<tool>.<id>.meta.json`, read back by `LoadAdhocBackupMetadata`), no
   retention (`retentionDeadline` never expires it), `Discovered`, size and
   file count, compression; `inspectDiscoveredBackup`; session id; the meta
   file written (`backupMetaFilePath`), then `BackupMetaMap.Set`.

## Inspection

`inspectDiscoveredBackup` reads as a restore reads; what it finds goes to the
position fields, the times, `Completed` and `DiscoveryNote`:

- mysqldump file: `readDiscoveredStream` through `openRestoreArtifactStream`
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
  footer and its time (local time zone). Header without footer: not
  completed.
- splitdump folder: `splitdump.ReadMetadata` for the position;
  `ReadManifest` + `ValidateManifest`, then each listed file must exist (not
  read: a splitdump can be large). No manifest: registered, noted.
- `.tar.enc` (splitdump, or mydumper by its name): `readDiscoveredArchive`
  reads the decrypted tar stream to its end through `openRestoreArtifactStream`
  (its integrity code is checked; a foreign key fails it), keeping in memory
  its members' base names and its `manifest` and `metadata` (16 MiB at most;
  `splitdump.ParseManifest` / `ParseMetadata`): nothing is written to disk.
  `inspectDiscoveredFolder` runs the folder's checks through read/exists
  functions, on a plain folder or on that. Base names: the folders are flat, a
  nested member would share one, harmless for the checks.
- mydumper folder: `Finished dump at` in its `metadata`.

A discovered dump is read whole once (in the server's start goroutine, or the
API request); its meta file then makes it known.

## Use

Discovered backups never enter `buildBackupCatalog` (it takes
`LastBackupMeta` only, which discovery does not touch). The legacy fallback of
the automatic reseeds and the flashback (no catalogued backup: the fixed names
through `findExistingBackupPath`, then `<backup dir>/<fixed name>`) skips them:
`findExistingBackupPath` passes over a discovered artifact
(`isDiscoveredBackupPath`), and the three callers refuse a fixed path that is
one before their existence check, the backup cookie kept (the backup exists,
it is not for automatic use). `Cluster.ReseedFromBackupID(server, id)` reseeds on request:
logical (`mysqldump`, `mydumper`) only (`ErrBackupDiscoveryNotLogical`),
`Completed`, not `OriginalRemoved`, artifact present (`backupArtifactExists`),
target not the primary; then `JobReseedLogicalBackupFromPathWithOptions` (the
restic restore's path). The position comes from the artifact during the
restore, not from the meta file.

## API and ACL

- `POST /api/clusters/{clusterName}/backups/discover`
  (`handlerMuxClusterBackupDiscover`): ACL rule `/backups/`; the handler also
  requires `db-backup` (`requestACLUser`): the rule matcher falls back from
  `/backups/` to the `/backups` list rule (`show-backups`) when the first
  denies. 409 while a backup runs.
- `POST /api/clusters/{clusterName}/servers/{serverName}/actions/reseed/backup/{backupID}`
  (`handlerMuxServerReseedFromBackup`): ACL rule `/actions/reseed/`
  (`db-restore`), and the handler requires `db-restore` too (the rule matcher
  falls back to a shorter rule when the specific one denies); 404 for an
  unknown backup (`ErrBackupNotFound`), 400 for the other refusals.

## GUI

`Pages/Maintenance/index.jsx`, Current Backups: a search icon in the section
header (confirmation, `discoverBackups` thunk); in the status column a folder
icon for a discovered backup and, for one not completed, the reason in the
tooltip; a reseed action per logical, completed backup that opens a
confirmation with a server dropdown (`clusterServers` without the primary),
`reseedFromBackup` thunk.

Meta files are written 0644 like every backup meta file (`WriteBackupMetadata`,
`srv_bck.go`): one permission for the folder's meta files.

Retention: a discovered backup is ad hoc without a retention window
(`RetentionDays` 0, no `RetentionDuration`): `retentionDeadline` never expires
it, so the ad-hoc purge leaves it; the `backup-keep-until-valid` rotation
(`.old`, `.err`) handles the scheduled artifacts only; the restic purges handle
snapshots. It is bounded by the artifacts in the folder (unregistered when its
artifact is gone, `refreshDiscoveredBackups`) and deleted by the operator
(`DeleteBackupByID`).

## Tests

`cluster/backup_discovery_test.go`: classification (any splitdump name,
mydumper, staging names, unrelated files), a discovery (positions, dump time,
unfinished dump, damaged splitdump, scheduled backup not re-registered, second
run and restart register nothing new), a foreign encrypted backup, the
reseed refusals, encrypted archives, the fallback skip, live entries kept, a
copy in progress (left, then inspected again under its id), a removed artifact
unregistered, one scan per server, the per-scan cap, the refusal while a
backup runs. `cluster_acl_test.go`: the reseed route needs
`db-restore`.

Regtest (T13, real servers): `testBackupDiscovery` (`regtest/test_backup_discovery.go`,
run by name). For a mysqldump file, a splitdump folder and a mydumper folder
(mydumper installed): a backup of the primary is copied under another name,
backdated past the settle window, discovered (position and GTID as the backup
recorded), the first replica reseeded from it (`ReseedFromBackupID`, checked as
`TestLogicalReseed` checks a reseed; mydumper needs myloader), then the copy
deleted through `DeleteBackupByID`.
