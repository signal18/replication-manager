# backup-archive-remove-original (#1987)

Removes the original of a logical or physical backup from the server backup
directory once its restic snapshot is verified to hold it, and fetches it back
in place when a restore needs it. User doc: `doc/backup_archive_remove_original.md`.

Off-switch (T14): `backup-archive-remove-original`, `Conf.BackupArchiveRemoveOriginal`,
default `false`, per cluster, switchable (`SwitchBackupArchiveRemoveOriginal`,
`/api/clusters/{name}/settings/actions/switch/backup-archive-remove-original`, ACL `/settings/actions/switch` → `cluster-settings`).

Code: `cluster/srv_job_backup_archive.go`; tests in `srv_job_backup_archive_test.go`
run against a real restic binary and a temporary local repository (skipped
without `restic` in `PATH`). The GitHub workflows run no Go tests today, so these
run only where `go test` is run by hand, with restic installed; the regtest
`testBackupArchiveRemoveOriginal` is the release gate (T13).

## Removal

Hook: the result goroutine of `ServerMonitor.BackupRestic`.

`BackupRestic` used to take `updateMetadata bool` and the callback read
`server.LastBackupMeta.{Logical,Physical}` when the snapshot finished. It now
takes the `*BackupMetadata` of the backup being pushed, captured by the caller
when the push is queued (`nil` for binary log copies, which record nothing).
`UpdateBackupMetadataWithRestic(backtype, meta, snapshotID)` records the
snapshot in that meta; a scheduled backup writes its meta file only while it
is still the current one, because the next scheduled run reuses the same file
name (`<tool>.meta.json`). A registered backup's meta has its own file and always writes.

`removeArchivedOriginal(backtype, meta, snapshotID)` then runs when the switch
is on:

1. A scheduled meta that is no longer the current one is skipped: `Dest` is
   the newer backup's artifact (same name). Not a refusal.
2. Already removed, or `Dest` missing: nothing to do.
3. `archivedOriginalRefusal` — the gate. Any reason keeps the original:
   - `!Completed`;
   - `Dest` not a strict descendant of `GetMyBackupDirectoryPath()`
     (`isStrictlyWithinBase`: `isPathWithinBase` accepts the directory itself,
     and a scheduled push snapshots that directory, so the snapshot check alone
     would let `Dest == <backup dir>` through and `os.RemoveAll` would take every
     backup, binary log copy and meta file of the server);
   - `snapshotMissingPath` reports `Dest` missing, or the listing fails;
   - encrypted (`meta.Encrypted` or `.enc`/`.tar.enc` suffix): the same for
     `IntegritySidecarPath(Dest)`.
4. Remove with `removeBackupArtifactWithSidecar` (encrypted, sidecar first) or
   `os.RemoveAll`.
5. `meta.OriginalRemoved = true`, meta file rewritten under the same
   current/registered rule.

`snapshotMissingPath` runs one `restic ls <id> <Dest> [<sidecar>]` and checks each
path against the entries (the path itself, or entries below it for a
directory): the snapshot's own tree, not the push result alone, is the proof.

### Encrypted pushes of one artifact

A push of one artifact archives `meta.Dest` alone (a scheduled push archives the whole
server directory, sidecars included). For an encrypted artifact the push now
adds its `.hmac` sidecar through the new `ResticBackupOption.ExtraPaths`
(appended after `DirPath` on the `restic backup` command line), so the sidecar
check of the gate can pass and the fetched-back artifact can be decrypted
(`OpenDecryptedFile` requires the sidecar).

### WARN0238 (F7)

A refusal (`archiveRemoveOriginalRefusal{Dest, Reason}`) is kept per server and
method in `archiveRemoveOriginalRefusal{Logical,Physical}` (guarded by
`backupMetaMutex`), cleared by the next successful removal of that method.
`checkArchiveRemoveOriginalStates` (cluster_set.go, every tick) raises
WARN0238 from it, both methods joined in one state per server
(`ErrFrom: BACKUP`). It reconciles first (F5): a refusal is dropped while the
switch is off (no stale reason when it comes back on) and once its `Dest` is no
longer on disk (deleted, purged, replaced). A refusal of a superseded scheduled
meta is never recorded.

## Fetch back

`cluster.restoreArchivedOriginal(src, backtype, tool) bool` is called at each
place a restore looks for the artifact on a server, before the existence
check:

| Path | Servers |
|---|---|
| `JobReseedPhysicalBackup` | backup server (with cookie), master |
| `ProcessReseedPhysical` (local fallback) | backup server (with cookie), master |
| `JobFlashbackPhysicalBackup`, `ProcessFlashbackPhysical` | backup server (with cookie), self |
| `JobReseedLogicalBackupPrepare` | backup server (with cookie), master |
| `JobFlashbackLogicalBackup` (legacy fallback) | master, backup server (with cookie) |
| `postgresReseedFromBackup` | master |

`ReseedPointInTime` goes through `JobReseed{Logical,Physical}Backup`.
`JobFlashbackLogicalBackup`'s selector path never sees these backups as local
(`backupMetaToCatalog` marks any backup with a snapshot `RepoRemote`), so it
falls to the legacy lookup, which is wired. Rejoin from a remote catalog pick
is #1590.

It acts only when the server's last backup of the method has
`OriginalRemoved`, a `ResticSnapshotID`, a matching tool (when given) and a
missing `Dest`. It returns whether the backup lives in the archive; the callers
then keep the backup type cookie (the "Remove false cookie" branches), so a
failed fetch does not erase the backup status.

`fetchArchivedOriginal`:

- `Dest` must be a strict descendant of the backup directory
  (`isStrictlyWithinBase`), as for the removal;
- free space of the backup directory measured now (`disk.Usage`, not the
  cached `DiskStatManager`, whose lookup misses the directory path) against
  `meta.Size`; a failed measure refuses the fetch (F3);
- `restic restore <id> --target <work> --include <Dest> [--include <sidecar>]`
  into `os.MkdirTemp(<backup dir>, ".archive-restore-")`, removed on return.
  The backup directory itself is never the target: `restoreSnapshotWithOptions`
  rewrites the permissions of the whole target tree (`setRestorePermissions`).
  Full paths are used, so no `<id>:<subfolder>` syntax (restic 0.16+) is needed.
  restic reads `--include` as a pattern: `escapeResticPattern` escapes
  `\ * ? [ ]` so a path holding them matches itself only;
- rename into place, sidecar first, on the same filesystem.

Every push excludes `.archive-restore-*` (`archiveRestoreWorkDirPattern`): a
scheduled push snapshots the whole backup directory and must not capture a
fetch in progress.

`archiveRestoreMutex` (per server) serializes fetches; a second caller finds
the file present. `Dest` only ever appears whole (rename out of the work
directory), so a present `Dest` is a complete artifact, never the leftover of an
interrupted fetch.

All callers run off the monitor loop (`trackTickGoroutine`, `go RejoinMaster`,
API handlers), so a slow fetch from S3 never stalls a tick (F2).

### Lifetime of a fetched copy

The fetched original keeps `OriginalRemoved = true` and stays on disk. These
restore paths only read scheduled backups (fixed names, or
`resolveLogicalBackupPathFromMeta`, which skips registered ones), so the next scheduled
backup of the method overwrites it, and that backup's own archive push removes
it. At most one backup per method and server is on disk at any time, as with
the switch off.

### Keep-until-valid

With `backup-keep-until-valid`, the next run renames the previous artifact to
`.old` before writing and removes it on success; a removed original makes the
rename a no-op (`mv` of a missing file). On failure the rollback restores the
previous meta, which still carries `OriginalRemoved` and its snapshot, so a
restore fetches it from the archive.

## Not covered

- Rejoin from a remote pick of the restore catalog: #1590.
- Restic retention: once `backup-keep-*` forgets the snapshot, a removed
  original cannot be fetched back. The removal does not change retention.
