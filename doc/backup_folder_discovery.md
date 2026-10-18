# Backups placed in a backup folder

replication-manager lists the backups it describes with a metadata file. A
backup copied into a server's backup folder by hand, by another tool, or from
another replication-manager has none. The backup folder discovery finds such
backups and checks them; the operator registers the ones to use. Nothing found
is used, nor written about, until it is registered.

A server's backup folder is
`<working dir>/backups/<cluster>/<host>_<port>/`.

## What is found

Each entry of the folder that no metadata describes, when it may be a logical
backup:

| Artifact | Recognized by |
|---|---|
| mysqldump file | its name: `.sql`, `.sql.gz`, encrypted `.sql.enc`, `.sql.gz.enc` |
| splitdump folder | its `manifest` file, or a `metadata` file without mydumper's `Started dump at` line; any folder name |
| encrypted splitdump or mydumper | a `.tar.enc` archive; once decrypted, its content decides as for a folder |
| mydumper folder | its `metadata` file with mydumper's `Started dump at` line |

The scheduled backups' own files (`mysqldump.sql.gz`, `splitdump`,
`mydumper`, `dumpling`, and their `.enc` / `.tar.enc` forms) can be replaced by
hand under the same name. A replacement is not used until it is registered (see
"A backup's file changed" below); registered, it becomes the scheduled backup.
One no record describes (a `mysqldump.sql.gz` left from before the split mode
was turned on) is listed as a scheduled backup file. The backup folder holds the
latest backups only: the next scheduled backup rewrites them, keep a backup's
history in the archive (restic).

Physical backups are not discovered. Names ending in `.partial`, `.tmp`,
`.new`, `.old` or `.err` are left out: replication-manager writes or keeps its
own backups aside under them. So are `direct-reseed-system`, the working
folder of the direct reseeds, and `mysql.users.sql.gz`, the users file a backup
writes next to its dump with `backup-split-mysql-user`.

## What a proper backup is

A found artifact is a backup when it holds at least one table and is complete.
Its replication position only decides whether a replica can be reseeded from it.

| | Holds a table | Complete | Position (reseed only) |
|---|---|---|---|
| mysqldump / mariadb-dump file (`.sql`, `.sql.gz`, `.enc`) | a `CREATE TABLE` | the client's `-- Dump completed` last line (not checked when the dump has no header: `--skip-comments`) | `CHANGE MASTER` / `CHANGE REPLICATION SOURCE`, `gtid_slave_pos`, `GTID_PURGED` |
| splitdump (folder, `.tar.enc`) | a `<db>.<table>-schema.sql[.gz]` file in its manifest | a valid `manifest`, every file it lists present, no dump file it does not list | `metadata` |
| mydumper (folder, `.tar.enc`) | a `<db>.<table>-schema.sql[.gz\|.zst]` file | `metadata` with `Finished dump at` | `metadata` (0.14.1+ and older forms) |

Not a backup: an accounts or grants file (`CREATE USER` / `GRANT` only,
`mysqldump --system=user` output included), an empty file, a dump without a
table (`--no-create-info`), a dump folder whose dump excluded every table. A
schema-only dump, or a dump of the `mysql` schema, holds tables: it is a backup.

## Discovery: find and check

The discovery runs:

- at the start of each server's monitoring, unless `backup-discovery` is off;
- on demand: **Maintenance → Found in backup folders**, the search icon
  (grant `db-backup`), or the API `POST /api/clusters/{clusterName}/backups/discover`.

It runs in the background: the API answers `202` at once. It is refused
(`409`) while a backup job of the cluster runs; not for an archive push (restic
task queue), which only reads the backups. One discovery, registration or
unregistration runs at a time per server.

It lists what it finds in **Maintenance → Found in backup folders** (API
`GET /api/clusters/{clusterName}/backups/candidates`, CLI
`replication-manager-cli backup candidates`), the files that are not backups
included, each with what its cheap check found:

| Check | Meaning |
|---|---|
| Backup | holds a table: it can be registered |
| Not a backup | why: accounts only, empty, no `CREATE TABLE`, no table file |
| Incomplete | a dump folder that did not finish: no manifest, a listed file missing, no `Finished dump at` |
| Not checked | the cheap check can not tell (an encrypted folder archive, no `CREATE TABLE` in a dump's first 64 MB): registering it reads it |
| Unreadable | can not be read (an encryption key, a permission): registering it tries again |
| Being copied? | changed in the last minute: checked once it settles |

The cheap check reads a dump only up to its first `CREATE TABLE`, and a dump
folder's file names and its small `manifest` and `metadata` files: a large dump
is not read to its end at each discovery. One discovery reads the content of 8
dump files at most, the others are checked by the next ones; an unchanged
artifact is not checked again, except by the discovery you request.

The list is bounded per server by `backup-discovery-candidate-max` (default
100): beyond it the oldest artifacts are not listed and WARN0250 is raised
until a discovery lists them all.

The ad-hoc backups (one metadata file each) and the registered ones are read
back by the same scan, so they stay listed after a restart, also with
`backup-discovery` off.

## Register

**Maintenance → Found in backup folders**, the register icon of a backup
(grant `db-backup`); the API:

```
POST /api/clusters/{clusterName}/backups/register
{"server": "db1:3306", "path": "<the path the candidates list>", "takenAt": "2026-10-01T03:00:00+02:00"}
```

or the CLI `replication-manager-cli backup register <path> --server db1:3306 [--taken-at <RFC 3339>]`.

Registering reads the backup to its end as a restore would read it
(decrypted, decompressed; an encrypted backup's integrity code checked; every
file of a splitdump) and registers it only if it is a proper backup. It runs in
the background (`202`); a refusal, with its reason, is shown with the backup in
**Found in backup folders**. It is refused at once (`422`) for an artifact the
discovery already found is not a backup or not complete.

A registered backup records its position, its size, its time and where that
time comes from, and its fingerprint (its file's size, modification time, inode
and change time; for a folder its files' count, total size, newest
modification and change times). It is listed in **Current Backups** as a Full
backup, marked as registered from a backup folder (a folder icon). Only
replication-manager's own metadata file (`<tool>.<id>.meta.json`) is written
next to it: the backup itself is never touched.

It has no retention: no purge touches it. It is unregistered when its file is
removed from the folder.

### When the backup was taken

A backup placed by hand carries its dump time only when its format records one;
the file's modification time is when it was copied in (a plain `cp` or a
download), unless the copy kept it (`cp -p`, `rsync -a`, `tar`).

| | Recorded by the backup | Shown as |
|---|---|---|
| mysqldump / mariadb-dump file | its end: `-- Dump completed on <time>` (none with `--skip-comments`) | taken: the dump footer |
| mydumper | its start and end: `Started dump at` / `Finished dump at` in `metadata` | taken: mydumper metadata |
| splitdump, a dump without footer | none | file time: when the file was placed, not when it was dumped |

At Register, the operator can set the time of a backup that records none
(`takenAt`, the time field of the register dialog): it is shown as set by the
operator. It is refused for a backup that records its own time.

A dump's own times are in the time zone of the server that dumped it, without
a zone: replication-manager reads them in its own. Its replication position
orders backups against each other and the primary, not its time.

## A backup's file changed

A backup is used only as it was written or registered: no backup file is used
without being checked. replication-manager records the fingerprint of each
logical backup it takes when it writes its metadata (after its encryption), and
the fingerprint of each backup registered. A backup's file may be edited or
replaced on purpose, a scheduled backup's included: the edit stays yours, and
replication-manager waits for you to confirm it. The logical backups are
compared with their recorded fingerprint every 30 monitoring ticks, off the
monitoring loop and not while a backup runs (a stat, a bounded walk of a
folder: a folder of more than 100,000 entries can not be registered), and again
when a reseed is about to use one. A difference raises the server-level
backup warning **WARN0249**, naming what changed (e.g. `backup 1759300000
copy.sql.gz changed since registered: 90.8 MiB -> 12.1 MiB, modified
2026-10-16 03:40`), and the backup is shown as changed in **Current Backups**.

Until the change is registered:

- a reseed or a restore from it is refused: "changed since registered:
  register the change first", the automatic reseeds and rejoins included, which
  check the file when they are about to use it;
- the restore catalog skips it: an automatic rejoin does not pick it;
- it is not proven by a test restore; its verification result stays shown,
  marked as taken before the change;
- **Verify** stays available on demand, to check the edited file before
  registering it. Nothing runs on the changed file by itself.

What to do:

- **The change is intended**: register it, the register icon of the backup in
  **Current Backups**, `POST /api/clusters/{clusterName}/backups/{backupID}/register`,
  or `replication-manager-cli backup register <backupID>`. The file as it is now
  is read and checked as a new backup: refused, with the reason, if the edit
  broke it; otherwise its new fingerprint, position and size are recorded and
  WARN0249 clears. A discovered or ad hoc backup keeps its id; a scheduled
  backup's file registered this way becomes the scheduled backup under a new id,
  its record (`<tool>.meta.json`) replaced, used by the automatic reseeds again.
- **The change is not intended** (a truncated copy, a file replaced under the
  same name, disk corruption, an accidental edit): the backup is not used
  either way. Restore the original from its archive (`backup-archive`, restic)
  and register it: Register reads and checks it as it is. Or unregister the
  backup and take a new one. replication-manager never restores it on its own,
  even from an archive: only you can tell whether the change was intended. An
  encrypted backup's integrity code fails on any change.

WARN0249 also clears when the registered file itself reads as registered again
(a backup folder that was unmounted, mounted back), and when the backup is
unregistered.

## Unregister

**Current Backups**, the unregister icon of a registered backup (grant
`db-backup`), `POST /api/clusters/{clusterName}/backups/{backupID}/unregister`,
or `replication-manager-cli backup unregister <backupID>`. It removes
replication-manager's metadata file only: the backup file is left as it is, and
is listed in **Found in backup folders** again. A backup replication-manager
took is not unregistered: delete it.

## Backups taken before this release

The logical backups replication-manager took before it recorded fingerprints
get theirs, as their file is, the first time they are checked.

## Backups registered by 3.1.44

3.1.44 registered every dump it found. Those registrations stay: the proper
backups as Full, their fingerprint recorded as their file is the first time
this release checks them. The entries that are not backups (e.g. the
`grants.sql.gz` and `mysql.user.sql.gz` account files older releases wrote next
to a dump) stay listed, flagged "not a backup", never used, with Unregister:
replication-manager removes nothing on its own.

3.1.44 could also register replication-manager's own scheduled backup files
(`mysqldump.sql.gz`, `splitdump`, `mydumper`, `dumpling`, and their encrypted
forms) as discovered. Each scheduled backup rewrites those files, so such a
record describes nothing: it is removed at the next discovery or change check
(its metadata file only, logged), the scheduled backup file is left as it is.
Until then it is never used and does not keep an automatic restore from using
the scheduled backup.

## Using a registered backup

A registered backup is never picked by an automatic restore (the rejoin of a
server, the restore selector, nor the fallback that looks for
`mysqldump.sql.gz` or `splitdump` when no backup metadata is loaded): its time
may be the time of a copy, and an automatic restore must not take it for a
recent backup. An automatic restore that finds only a registered discovered
backup fails, naming it: reseed from it on request.

A server is reseeded from it on request: **Maintenance → Current Backups**, the
reseed icon of the backup, then the server to reseed (grant `db-restore`), or
the API:

```
POST /api/clusters/{clusterName}/servers/{serverName}/actions/reseed/backup/{backupID}
```

This works for any logical backup of the list. The API answers `404` for an
unknown backup, `409` while a backup of the cluster runs (retry once it ends),
`400` for a backup it can not restore or for the primary. The reseed restores
the artifact and restarts replication from the position the artifact itself
holds (the dump header, the splitdump or mydumper metadata), as any logical
reseed does. It is refused for a backup not completed, without a replication
position (the reseed icon is disabled, its tooltip says why), kept only in its
archive, missing, not a backup, or changed since registered, and for the
primary.

### A registered backup is reseeded from once proven

A registered backup is a file replication-manager did not take: reading it to
its end, as Register and the integrity verification do, does not load it. A dump
that reads fine can still fail to load (a duplicated primary key row, an object
the server refuses). With `backup-verification-registered-proven` (on by
default), the reseed from a registered backup is refused until it is
**proven**: its last verification is a passed test restore of its file as it is
now (**Test restore** in its row, on `backup-verification-host`; see
[Backup verification](backup_verification.md)). The automatic rejoin already
requires the same when its backup selector asks for a `Proven` backup.

Whatever this setting, the reseed is refused while the last verification of the
backup's current file failed (WARN0253): verify it again, register a fixed file
(Register of the change), or unregister it. A verification of the file before a
registered change does not count. Backups replication-manager took are not
concerned.

Turning `backup-verification-registered-proven` off gives back the reseed from a
registered backup never verified.

## Archive a registered backup

A registered backup never goes through a backup job, whose end pushes the backup
to the archive (restic). **Archive** in its row of **Maintenance → Current
Backups** (grant `db-backup`), or the API:

```
POST /api/clusters/{clusterName}/backups/{backupID}/archive
{"removeOriginal": false, "verifyRestore": false}
```

queues the push of its file in the restic task queue, after the pushes already
queued (the **Restic task queue** shows it); its snapshot is recorded in the
backup and listed with the snapshots. Two options, both off unless asked:

- **Remove the original once restic holds it** (`removeOriginal`): the file is
  removed once its snapshot is checked to hold it, as
  [`backup-archive-remove-original`](backup_archive_remove_original.md) does
  for the backups it covers; that setting does not apply to this action. A
  removal refused is WARN0238. A backup kept only in its archive is no longer
  reseeded from on request.
- **Verify by test restore first** (`verifyRestore`, offered when
  `backup-verification-host` is set and the backup is a mysqldump, grant
  `db-restore`): the test restore runs first; the push is queued only when it
  passes. A test restore that fails pushes nothing and keeps the file; its
  result is the backup's verification (WARN0253).

The API answers `202` once the archive started, `404` for an unknown backup,
`409` for a backup already archived or being archived, changed since
registered, or being verified or registered, `422` for a backup not registered,
without restic (`backup-restic` off), whose last verification of its current
file failed (archive it with a test restore first), or for a test restore that
can not run. The CLI:

```
replication-manager-cli backup archive BACKUP_ID [--remove-original] [--verify-restore]
```

## Settings

| Setting | Default | |
|---|---|---|
| `backup-discovery` | `true` | the discovery at the start of each server's monitoring; the Discover button runs one whatever it, and the registered backups are checked for changes whatever it |
| `backup-discovery-candidate-max` | `100` | the artifacts a discovery lists per server, the oldest dropped beyond (WARN0250); `0` or less: the default |
| `backup-verification-registered-proven` | `true` | the reseed from a registered backup requires it proven (a passed test restore of its current file); off: refused only while its last verification of its current file failed |

All are in **Settings → Backups**.

## States

| State | Level | Severity | Raised while |
|---|---|---|---|
| WARN0249 | server | warning (backup) | a registered discovered backup's file is not the one registered |
| WARN0250 | server | warning (backup) | the last discovery of the server's folder did not list every artifact, beyond `backup-discovery-candidate-max` |
| WARN0253 | server | warning (backup) | the last verification of a registered backup's current file failed: it is not reseeded from nor archived without a new test restore |
