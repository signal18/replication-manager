# backup-archive-remove-original (#1987)

Removes the original of a logical or physical backup from the server backup
directory once its restic snapshot is verified to hold it, and fetches it back
in place when a restore needs it. User doc: `doc/backup_archive_remove_original.md`.

Off-switch (T14): `backup-archive-remove-original`, `Conf.BackupArchiveRemoveOriginal`,
default `false`, per cluster, switchable (`SwitchBackupArchiveRemoveOriginal`,
`/api/clusters/{name}/settings/actions/switch/backup-archive-remove-original`, ACL `/settings/actions/switch` → `cluster-settings`).

Code: `cluster/srv_job_backup_archive.go`; tests in `srv_job_backup_archive_test.go`
run against a real restic binary and a temporary local repository (skipped
without `restic` in `PATH`).

## Removal

Hook: the result goroutine of `ServerMonitor.BackupRestic`.

`BackupRestic` used to take `updateMetadata bool` and the callback read
`server.LastBackupMeta.{Logical,Physical}` when the snapshot finished. It now
takes the `*BackupMetadata` of the backup being pushed, captured by the caller
when the push is queued (`nil` for binary log copies, which record nothing).
`UpdateBackupMetadataWithRestic(backtype, meta, snapshotID)` records the
snapshot in that meta; a scheduled backup writes its meta file only while it
is still the current one, because the next scheduled run reuses the same file
name (`<tool>.meta.json`). An ad-hoc meta has its own file and always writes.

`removeArchivedOriginal(backtype, meta, snapshotID)` then runs when the switch
is on:

1. A scheduled meta that is no longer the current one is skipped: `Dest` is
   the newer backup's artifact (same name). Not a refusal.
2. Already removed, or `Dest` missing: nothing to do.
3. `archivedOriginalRefusal` — the gate. Any reason keeps the original:
   - `!Completed`;
   - `Dest` outside `GetMyBackupDirectoryPath()` (`isPathWithinBase`);
   - `snapshotPathExists(snapshotID, Dest)` false or error;
   - encrypted (`meta.Encrypted` or `.enc`/`.tar.enc` suffix):
     `snapshotPathExists(snapshotID, IntegritySidecarPath(Dest))` false or error.
4. Remove with `removeBackupArtifactWithSidecar` (encrypted, sidecar first) or
   `os.RemoveAll`.
5. `meta.OriginalRemoved = true`, meta file rewritten under the same
   current/ad-hoc rule.

`snapshotPathExists` runs `restic ls <id> <path>`: the snapshot's own tree, not
the push result alone, is the proof.

### Ad-hoc encrypted pushes

An ad-hoc push archives `meta.Dest` alone (a scheduled push archives the whole
server directory, sidecars included). For an encrypted artifact the push now
adds its `.hmac` sidecar through the new `ResticBackupOption.ExtraPaths`
(appended after `DirPath` on the `restic backup` command line), so the sidecar
check of the gate can pass and the fetched-back artifact can be decrypted
(`OpenDecryptedFile` requires the sidecar).

### WARN0238 (F7)

A refusal is kept per server and method in
`archiveRemoveOriginalRefusal{Logical,Physical}` (guarded by
`backupMetaMutex`), cleared by the next successful removal of that method.
`checkArchiveRemoveOriginalStates` (cluster_set.go, every tick) raises
WARN0238 from it while the switch is on, both methods joined in one state per
server (`ErrFrom: BACKUP`). A refusal of a superseded scheduled meta is never
recorded.

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

- free space of the backup directory (`GetDiskStat`) against `meta.Size` (F3);
- `restic restore <id> --target <work> --include <Dest> [--include <sidecar>]`
  into `os.MkdirTemp(<backup dir>, ".archive-restore-")`, removed on return.
  The backup directory itself is never the target: `restoreSnapshotWithOptions`
  rewrites the permissions of the whole target tree (`setRestorePermissions`).
  Full paths are used, so no `<id>:<subfolder>` syntax (restic 0.16+) is needed;
- rename into place, sidecar first, on the same filesystem.

`archiveRestoreMutex` (per server) serializes fetches; a second caller finds
the file present.

All callers run off the monitor loop (`trackTickGoroutine`, `go RejoinMaster`,
API handlers), so a slow fetch from S3 never stalls a tick (F2).

### Lifetime of a fetched copy

The fetched original keeps `OriginalRemoved = true` and stays on disk. These
restore paths only read scheduled backups (fixed names, or
`resolveLogicalBackupPathFromMeta`, which skips ad-hoc), so the next scheduled
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
