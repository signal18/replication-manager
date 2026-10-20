# Global backup catalogue (design)

Status: **design**, not coded (2026-10-20), branch `archive-plugins` (#2091). Related:
`BACKUP_ARCHIVE_PLUGINS.md` (archives as plugins), `BACKUP_FOLDER_DISCOVERY.md` (registered
backups), `BACKUP_ARCHIVE_REMOVE_ORIGINAL.md` (a backup kept only in an archive), BKU/BAU
metering.

## Words

- **Backup directory**: a server's folder of backups, `<working-dir>/backups/<cluster>/<server>/`
  (on the repman disk, or the bucket through the backups folder mount).
- **Backup**: one dump or physical copy in a backup directory (`mydumper/`,
  `mydumper.<time>/`, `mysqldump.<time>.sql.gz` ...).
- **Snapshot**: the backup directory at one point in time.
- **Archive**: a store of snapshots (restic, plakar ...), attached or detached.
- **Catalogue**: repman's list of the backups and of the snapshots of every archive.

## Today

- The catalogue is the backups' own records: one `<backup>.meta.json` next to each backup, in
  its backup directory, read into `BackupMetaMap` (`FetchLastBackupMetadata`,
  `LoadAdhocBackupMetadata`, `cluster/srv_bck.go`, `cluster/backup_helpers.go`).
- An archive is known through a backup only: `archiveRefs` maps an archive name to the
  snapshot taken when the backup was pushed (`SetArchiveSnapshot`, `cluster/srv_bck.go:286`).
  repman keeps no list of an archive's snapshots: the snapshot tables ask the archive each time
  (`restic snapshots`, plugin `list`), the attached one only.
- What a snapshot holds depends on the backup: after a default backup, the whole backup
  directory; after an ad-hoc one, or the Archive button of a registered backup, that backup's
  folder or file only (`cluster/srv_job_backup.go` ~5458, ~8189,
  `cluster/backup_register_archive.go`). Whether a backup is default or ad-hoc is computed when
  it starts, from who is master or backup server then (`resolveBackupLine`,
  `cluster/backup_helpers.go:61`): the snapshot's content follows a moving role.
- The archive sizes are the only archive data kept outside: `<cluster working dir>/archive-sizes.json`.

## What goes wrong with it

Seen on dev3 while building #2091:

1. **The catalogue moves with the backup directory.** Switching the backups folder between
   the local disk and the bucket meant forgetting and re-reading every record
   (`resyncBackupCatalogue`); a record read through a fresh mount read as changed (WARN0249).
2. **A dead mount hides the catalogue.** After a restart, the records could not be read until
   the mount was back.
3. **One way only.** A backup says which snapshot holds it; a snapshot says nothing of the
   backups in it. A snapshot whose backup left the catalogue (purged locally, an older run, a
   binlog push) looks like any other.
4. **Detached archives are blind.** Their snapshots, size per snapshot and count are not shown;
   pricing or metering every archive (BAU) has nothing to count on.
5. **One snapshot per archive per backup.** A second push of a backup to the same archive
   replaced the first reference.

## Design

### Where

A catalogue per cluster in repman's own working directory, outside every backup directory:
`<cluster working dir>/backup-catalogue/`, next to `archive-sizes.json`:

- `backups.json`: one entry per backup;
- `archives/<archive name>.json`: one entry per snapshot of that archive.

Written whole to a temporary file then renamed (no half-written catalogue). SETTLED
(Stéphane, 2026-10-20): the **active** replication-manager writes it, always; the catalogue is
never synchronised to another one (a standby or DR repman builds its own when it becomes
active, by the rebuild below).

### What

**A backup entry**: id, server, tool, method, start and end time, the role of its server when
it started (`master` / `slave`, recorded once, never recomputed), its path in the backup
directory, its size and checksums, its proof (registration, verification), and where it is
now: in the backup directory (yes / no) and the snapshots holding it (archive name → snapshot
ids, all of them, oldest first).

**A snapshot entry**: archive, id, time, size, server, and the backups it holds: the backups
in the backup directory when it was taken (their ids), plus what else it holds (binlogs, files
no backup describes) as a count.

### When it is written

- **A backup ends**: its entry is added (in the backup directory).
- **A snapshot is taken**: its entry is added to the archive's list with the backups in the
  backup directory at that moment; each of those backups gets the snapshot.
- **A snapshot is deleted** (button, retention purge, archive removed): its entry leaves; the
  backups lose it; a backup with no copy left anywhere leaves the catalogue (as
  `dropArchiveRefs` today).
- **A backup leaves the backup directory** (local retention, remove original): its entry stays
  while a snapshot holds it, marked "in archives only".

### Snapshot content: always the backup directory

To match the words above, every snapshot is the server's whole backup directory, whatever
triggered it (default or ad-hoc backup, the Archive button). The restore is unchanged: it
takes the backup's own path out of the snapshot (`fetchArchivedOriginal`, `--include <path>`,
`cluster/srv_job_backup_archive.go`). Storage barely grows: restic and plakar store each piece
of data once.

### Rebuild, and what is observed

The catalogue is what repman believes; the backup directories and the archives are what it
observes:

- **The records next to the backups stay.** Each backup's `.meta.json` is still written, so a
  snapshot describes the backups it holds, and a catalogue can be rebuilt without repman's
  disk.
- **Rebuild** (first start, catalogue lost or asked from the API): read the backup
  directories' records, list every archive's snapshots, and read in each snapshot which backup
  records it holds.
- **Reconcile** (on each archive listing, on the backup directory checks already done): a
  backup in the catalogue missing from its directory and from every snapshot, a snapshot listed
  by the archive and unknown to the catalogue, or the reverse, is a **state** (a warning naming
  it), never fixed silently.

### Bounds

Snapshot entries leave when the archive deletes the snapshot (its retention), backup entries
when no copy is left: the catalogue holds what exists, no history beyond it. A detached
archive's list is kept as last read, with the time it was read.

## Snapback (coded, `cluster/cluster_archive_snapback.go`)

A server's backup directory put back as it was in a snapshot of the attached archive, every
archive kind (restic, plakar, pbs), from the snapshot rows of the Maintenance tab or
`POST /api/clusters/{cluster}/archive-actions/snapback/{snapshot}` (db-restore grant). Never
twice the size on disk:

1. the directory as it is now is snapshotted first: its **latest snapshot** (head); no snapshot,
   nothing deleted;
2. the directory is emptied: all it held is in the head;
3. the snapshot is restored into it: an archive plugin straight into it (wire `restore`: the
   pushed folder's content under Target), restic into a work folder outside the backups folder
   (`<cluster working dir>/backup/restic_temp/`), then moved in;
4. the server's backups are read again from the records the snapshot brought back.

Refused while a backup is written, the backups folder moves, a snapback of the server runs, for
a snapshot that is not a server's whole backup directory, or without room measured for the
snapshot (the current directory counted as freed when on the same disk). No retention purge
after the head's push: it could take the snapshot being restored.

**Not at its latest snapshot = locked** (Stéphane, 2026-10-20): the server's backups, archive
pushes and Archive button are refused (`snapbackLock`), WARN0255 open, until a snapback to the
head or a **release** (`.../snapback-release/{server}`: the directory kept as it is, its next
backup's snapshot is the new latest). The state is kept in `<cluster working dir>/snapbacks.json`
(a restart keeps the lock; a snapback running at a stop reads as failed, WARN0256, naming the
head to snap back to). CINF0020 while one runs.

## What it gives

- **Dashboard**: every archive, attached or not, with its size, its snapshot count and how many
  backups it holds; each snapshot row names the backups inside; each backup shows its
  snapshots, archive and short id, in the cell, not only in a tooltip.
- **Pricing and metering**: SETTLED (Stéphane, 2026-10-20): the external unit's cost is
  computed on the size of each archive, every archive counted, detached ones included (a
  detached archive is still invoiced, as the archive settings say); never per backup or per
  snapshot.
- **The backups folder switch** (local / bucket) no longer reloads the catalogue: only the
  observation changes.

## Migration

At the first start with it, the catalogue is built from today's records (their
`archiveRefs`, glued legacy snapshots) and the archives' listings. Today's per-backup fields
(`archiveRefs`, `resticSnapshotID`) stay readable; the catalogue becomes what the code reads.

## Open questions

1. Reading which backups a snapshot holds needs a listing of the snapshot's top folder: one
   call to the archive per snapshot (time and requests, not money). Proposed: done once per
   snapshot when it is taken (repman knows the backup directory then), kept, never at each
   refresh; a listing only for a rebuild.
2. A plugin's `list` answers snapshots only: does the wire need a `contents` op, or does repman
   read the backup records inside the snapshot through `restore` of those files only?
3. The default / ad-hoc split itself (folder name and retention follow a moving role): left as
   it is here, to look at separately.

## Steps

1. Snapshot content always the backup directory (ad-hoc and the Archive button): DONE; the
   Archive button is refused while a backup job writes the directory (`ErrBackupJobRunning`).
2. Role at backup time in the backup record: DONE (`role`: master / slave, "" unknown),
   shown in the backups table.
3. The catalogue files, written at backup end, snapshot push and delete; rebuild and
   reconcile states.
4. The code reads the catalogue (restore, reseed, retention, archive list, metering).
5. Dashboard: every archive's size and snapshots, the snapshot ↔ backup link both ways.
