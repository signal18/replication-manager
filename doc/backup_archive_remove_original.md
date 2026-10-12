# Remove the original backup once it is archived

With a restic archive configured (`backup-archive-mode`), every logical and
physical backup is kept twice: the original in the server backup directory
(`<working-dir>/backups/<cluster>/<host>_<port>/`) and its copy in the restic
snapshot. `backup-archive-remove-original` keeps only the archived copy.

Setting: `backup-archive-remove-original` (default `false`), per cluster.
Dashboard: **Settings → Archive → Restic repository config → Remove original
after archive**. The switch is disabled while no archive repository type is
active.

```toml
[mycluster]
backup-archive-mode = "restic-aws"
backup-archive-remove-original = true
```

## When the original is removed

After each archive push of a logical or physical backup, replication-manager
removes the original only when all of these hold:

- the backup completed;
- the restic push returned a snapshot, and the snapshot ID is recorded in the
  backup metadata;
- listing that snapshot shows the original, and for an encrypted backup its
  integrity sidecar (`.hmac`);
- the original is inside the server backup directory.

Otherwise the original stays where it is, and the cluster raises **WARN0238**
with the reason (for example a snapshot that does not hold the file, or a
restic listing error). The state clears once a later backup of the same
server is removed.

Only the original artifact (and its `.hmac` sidecar) is removed. The backup
metadata file (`<tool>.meta.json`) stays: it records the snapshot and
`"originalRemoved": true`. The server keeps its backup status, so the
"valid backup" checks and the automatic reseed still see the backup.

Binary log copies are never removed.

## Restoring

A reseed or flashback that needs the backup fetches it back from the archive
first, to the place and name it had, then runs as usual. This covers:

- logical and physical reseed (from the GUI, the API, the automatic reseed
  and the point-in-time reseed);
- logical and physical flashback;
- PostgreSQL reseed from a backup.

Before fetching, replication-manager checks that the backup directory has free
space for the backup's recorded size. The fetched copy then stays until the
next backup of the same server and method replaces it; that backup's own
archive push removes it again. Disk use is therefore never more than with the
setting off.

If the fetch fails, the restore fails with the usual "no backup file" error,
the reason is in the task log, and the backup status is kept so a later
attempt can fetch it again.

## Requirements

- A working restic archive (`backup-archive-mode` is `restic-local`,
  `restic-aws` or `restic-sftp`). With `restic-local` the archive sits on the
  replication-manager host too, but restic deduplicates and compresses, so it
  still saves space.
- Restic's own retention (`backup-keep-*`) decides how long an archived backup
  stays restorable: once its snapshot is forgotten, a removed original cannot
  be fetched back.
