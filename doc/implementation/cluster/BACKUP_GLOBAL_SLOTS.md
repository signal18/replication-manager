# Global backup slots (#2135)

`backup-concurrent-slots` (default 1, 0 = unlimited) limits the backups running at once across
every cluster of a replication-manager instance. The slots are a buffered channel shared by all
clusters (`ServerGlobals.BackupSemaphore`, cluster/server_globals.go): a backup takes a slot by
sending into it and gives it back by receiving from it. A backup that finds no free slot waits in
WARN0174 ("Waiting for global backup slot (n/n in use)").

## Who takes and gives back a slot

| Backup | Takes | Gives back |
|---|---|---|
| Logical (`JobBackupLogicalWithOptions`) | `waitForBackupSlot` | when WARN0175 resolves in the cluster's state processing (cluster/cluster.go) |
| Physical (`JobBackupPhysicalWithOptions`) | `waitForBackupSlot` | when WARN0073 resolves, or at once when it gives up before starting (cluster stopping, server down) |
| Verification (`backup_verification.go`) | `waitForBackupSlot` | at the end of the run (deferred) |

## Per-cluster count

The channel does not say which cluster holds a slot. Each cluster counts the slots its backups hold
(`Cluster.backupSlotsHeld`):

- `waitForBackupSlot` counts a slot as soon as it takes one;
- every release goes through `releaseBackupSlot`, which gives a slot back only when the cluster
  holds one. A WARN0073 / WARN0175 that resolves on a cluster holding no slot frees nothing, instead
  of freeing another cluster's;
- `Cluster.Stop` gives back every slot the cluster still holds (`releaseBackupSlots`). The release on
  state resolution runs in the cluster's monitor loop, which a stop ends: before #2135 a cluster
  deleted while its backup ran kept that slot until replication-manager restarted, and every
  other cluster's backups waited for it;
- a backup of a cluster that is stopping does not keep a slot: `waitForBackupSlot` counts the slot,
  then checks the stop, and gives it back when the cluster is stopping. Counted first, the slot
  is given back whether `Stop` drains the count before or after that check.

## Trade-off

`Stop` gives the slots back without waiting for the backups that hold them. A deleted cluster's
backup can run a few seconds more, until its servers are gone, while its slot is used by another
backup: for that time one more backup than `backup-concurrent-slots` can run.

A configuration reload keeps the `Cluster` object (`ReloadConfig`), so its count. A `Cluster` is
created only for a name not running (startup, add, import, git sync of a new name).
