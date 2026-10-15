# Backups placed in a backup folder

replication-manager lists the backups it describes with a metadata file. A
backup copied into a server's backup folder by hand, by another tool, or from
another replication-manager has none: it is not listed, and no reseed can use
it. The backup folder discovery registers such backups.

A server's backup folder is
`<working dir>/backups/<cluster>/<host>_<port>/`.

## What is discovered

Each entry of the folder that no metadata describes yet, when it is a logical
backup:

| Artifact | Recognized by |
|---|---|
| mysqldump file | its name: `.sql`, `.sql.gz`, encrypted `.sql.enc`, `.sql.gz.enc` |
| splitdump folder | its `manifest` file, or a `metadata` file without mydumper's `Started dump at` line; any folder name |
| encrypted splitdump | a `.tar.enc` archive |
| mydumper folder | its `metadata` file with mydumper's `Started dump at` line |

Physical backups are not discovered. Names ending in `.partial`, `.tmp`,
`.new`, `.old` or `.err` are left out: replication-manager writes or keeps its
own backups aside under them. So are `direct-reseed-system`, the working
folder of the direct reseeds, and `mysql.users.sql.gz`, the users file a backup
writes next to its dump with `backup-split-mysql-user`.

## When

- At the start of each server's monitoring.
- On demand: **Maintenance → Current Backups**, the search icon in the
  section header (grant `db-backup`), or the API:

```
POST /api/clusters/{clusterName}/backups/discover
```

The discovery runs in the background (it reads each new backup to its end):
the API answers `202` at once, the backups it registers are logged and appear
in the list when it ends. It is refused (`409`) while a backup of the cluster
runs: its artifact is not complete yet (the discovery at a server's start
waits for the next one then). One discovery runs at a time per server, and
reads at most 8 artifacts: the others are left to the next discovery, so a
folder of many large dumps is never read in one go.

An entry changed in the last minute is left to the next discovery: a copy
still running is not registered half written. A backup registered as not
completed is inspected again when its size or file count changed since (a
copy that ran on), and a discovered backup whose artifact was removed by hand
is unregistered: the discovered entries never outlive their artifacts.

The ad-hoc backups (one metadata file each) are read back by the same scan,
so they stay listed after a restart.

## What a discovered backup records

The backup is read as a restore would read it:

- **mysqldump file**: read to its end (decrypted, decompressed). Its
  replication position comes from the dump client's own lines (`CHANGE MASTER`
  / `CHANGE REPLICATION SOURCE`, `GTID_PURGED`, and MariaDB's `gtid_slave_pos`,
  written after the data), its time from its `-- Dump completed on` footer. A dump with the client's header and no
  footer did not finish: it is registered as not completed.
- **splitdump folder**: its position comes from its `metadata` file; every file
  its manifest lists must be there. A splitdump without a manifest (written
  before the manifest existed) is registered, its files not checked against
  one.
- **encrypted backup**: decrypted with this replication-manager's backup keys,
  current and previous, and read to its end. An encrypted splitdump or
  mydumper archive (`.tar.enc`) is then checked as its folder would be: the
  splitdump's position and manifest, the mydumper's completion. One encrypted
  with another key can not be read: it is registered as not completed.
- **mydumper folder**: complete when its `metadata` file has mydumper's
  `Finished dump at` line.

Its time is the dump's own when it records one (mysqldump), else the
artifact's modification time: the time of the copy, not of the data.

The backup is written a metadata file (`<tool>.<id>.meta.json`) and listed,
marked as discovered (a folder icon in the status column; a backup not
completed shows why). It has no retention: no purge (the ad-hoc retention, the
`backup-keep-until-valid` rotation, the restic purges) touches it; it is
deleted like any backup, and unregistered when its artifact is removed from
the folder.

## Using a discovered backup

A discovered backup is never picked by an automatic restore (the rejoin of a
server, the restore selector, nor the fallback that looks for
`mysqldump.sql.gz` or `splitdump` when no backup metadata is loaded): its time
may be the time of a copy, and an automatic restore must not take it for a
recent backup. An automatic restore that finds only a discovered backup fails,
naming it: reseed from it on request.

A server is reseeded from it on request: **Maintenance → Current Backups**, the
reseed icon of the backup, then the server to reseed (grant `db-restore`), or
the API:

```
POST /api/clusters/{clusterName}/servers/{serverName}/actions/reseed/backup/{backupID}
```

This works for any logical backup of the list, discovered or not. The API
answers `404` for an unknown backup, `400` for a backup it can not restore or
for the primary. The reseed
restores the artifact and restarts replication from the position the artifact
itself holds (the dump header, the splitdump metadata), as any logical reseed
does. It is refused for a backup not completed, kept only in its archive, or
missing, and for the primary.
