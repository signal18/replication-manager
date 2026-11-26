# Volume snapshots: the snapshot plugin interface (#2443)

User documentation: `doc/volume_snapshots.md`.

## What it is

A snapshot of the volume that holds a database server's files: take, list, delete, snapback
(roll the volume back). One Go interface, several backends (T7); ZFS and LVM are the built-in
open-source backends, a client script replaces them (F6), external signed plugins add more
(storage arrays, enterprise tier, private, later).

Not the same thing as the archive snapback (`cluster_archive_snapback.go`, #2091), which puts
a server's *backup directory* back to an archive snapshot. Every identifier here says
`Volume` to keep the two apart.

## Files

| File | What |
|---|---|
| `config/snapshot.go` | Plugin manifests and registry, tier check, keep bounds, validators |
| `cluster/srv_volume_snapshot.go` | The interface, the executors, the actions, the states |
| `cluster/srv_volume_snapshot_plugin.go` | Loading of the external plugins (`snapshotplugin-*`) |
| `share/scripts/volume_snapshot.sh` | The helper that runs the zfs and lvm commands (embedded) |
| `server/api_volume_snapshot.go` | The four endpoints |
| `share/dashboard_react/src/components/Modals/VolumeSnapshotsModal` | The GUI |

## Design

### One contract, three places to run it

Every plugin speaks one contract: the action as the first argument, the context in
`REPMAN_SNAPSHOT_*` variables, tab-separated records on stdout (`CMD`, `TARGET`, `SNAPSHOT`,
`CHANGED`), the error as the last line of stderr with a non-zero exit. The built-in helper,
a client's script and an external plugin all speak it; only where the program runs differs.
That is the whole interface between the Go side and the storage:

- `VolumeSnapshotter` (Take, List, Delete, Snapback, Manifest) is implemented once,
  `contractSnapshotter`, over a `snapshotExecutor`;
- `sshSnapshotExecutor`: the helper piped into `sh -s` on the database host, over the
  on-premise SSH connection (`OnPremiseConnect`, the one the on-premise orchestrator already
  uses). `sh`, not the login shell: the helper is POSIX sh and needs no file on the host;
- `localSnapshotExecutor`: the helper through `sh -s` on this host, for the localhost
  orchestrator;
- `programSnapshotExecutor`: a program on this host, the client's `backup-snapshot-script`
  (environment of every repman script, `GetExecEnv`) or an external plugin (nothing of this
  process, `archivePluginBaseEnv`, as for the archive plugins).

The policy is in Go (which snapshot to prune, refusals, maintenance, stop/start, states); the
commands are in the helper. Retention is computed in Go from `list` then applied with one
`delete` per snapshot, so a client script only implements four primitive actions.

### Why not the dbjobs mechanism

The dbjobs script runs in the jobs container on OpenSVC and Kubernetes: no `zfs`, no `lvm`, no
`/dev/zfs`, no access to the host's volume manager. On-premise, the SSH connection reaches the
host itself and returns the output synchronously, which `list` needs. The existing
`zfssnapback` dbjob (an OpenSVC naming convention, no volume known to repman) is left as it is.

### Plugins and tiers

`config.SnapshotPluginManifest` follows the archive plugin manifest (`config/archive.go`): name,
description, tier, actions. Built in: `zfs`, `lvm` (run on the database host) and `script`
(the override, chosen whenever `backup-snapshot-script` is set, F6). `RegisterSnapshotPlugin`
adds an external one; `LoadSnapshotPlugins` finds `snapshotplugin-<name>` programs in the
plugin directories (`archivePluginDirs`), checks their signature (`archivePluginSigned`) and
reads their manifests from `--manifest` (a JSON array). `SnapshotPluginAllowed` refuses an
enterprise-tier plugin without `EnterprisePluginsPlan()`, the archive plugins' rule.

### Volume resolution

The helper finds the volume from the server's directories (`volumeSnapshotPaths`: DATADIR,
INNODB_LOG_GROUP_HOME_DIR, INNODB_UNDO_DIRECTORY, INNODB_DATA_HOME_DIR, the directory of
LOG_BIN_BASENAME; relative ones under the datadir), with `findmnt -T`:

- ZFS: the deepest common ancestor of their datasets, snapshotted with `-r`. A layout with the
  redo log and the binary logs on sibling datasets (`<vol>/data`, `<vol>/redo`, `<vol>/binlog`)
  is snapshotted at its parent atomically.
- LVM: their logical volume; several LVs are refused (an LVM snapshot is per LV, not atomic
  across LVs).

`backup-snapshot-target` names it explicitly.

### Retention (F1, F4)

Only snapshots named `repman-*` (`ConstSnapshotPrefix`) are pruned and deletable through the
API: replication-manager removes only what it created. `backup-snapshot-keep` is bounded
(1 to 64, 0 or less is the default 3, never unlimited). The prune runs after each successful
take.

### Snapback

Never automatic: only the API/GUI action starts it, and `snapbackRefusal` refuses the master
and a running failover. `runVolumeSnapback`: maintenance on, `StopDatabaseService` (the
orchestrator dispatch), wait for the port to close, the snapback action, `StartDatabaseService`,
wait for the port. The server stays in maintenance (WARN0331, cleared when the operator takes
it out).

The helper prints `CHANGED` before its first destructive command, after its checks (every ZFS
child holds the snapshot; the LV unmounted). An error after `CHANGED` is half-way: the server
is not started (ERR00123, status `halfway`). An error before it: the server is started again
and the maintenance this run set is removed.

Re-checked after the stop (`snapbackRefusal`): a switchover or failover started during the
stop, or the server made master, ends the run before the rollback. A server in maintenance is no
failover candidate, so the window is the stop itself. A snapback stopped by its deadline, or
whose output was cut (`limitedBuffer.Truncated`), counts as changed: the remote program may
still run (`ssh.SIGKILL` is sent, not every SSH server honours it), the server is not started.
The start is confirmed through `WaitDatabaseStart` (Ping + Refresh, the monitor's SQL path);
the stop by the port closing.

The snapshot is crash-consistent (no lock, no flush): InnoDB recovers it at the start.

Replication links (T27): the snapback stops the server and starts it; it issues no
`CHANGE MASTER`, `RESET SLAVE` or `STOP SLAVE`. The volume carries the replica's own position
(GTID state, relay log info) consistent with its data, and the replica resumes from it.

### States (F7, T3)

From memory every tick (`checkVolumeSnapshotStates`), the last action of each server
(`VolumeSnapshotOp`, one per server, bounded): CINF0024 running, WARN0330 failed, ERR00123
half-way, WARN0331 left in maintenance, WARN0332 the plugin can not run. Cluster state machine,
`ErrFrom: BACKUP`, server URL set, like the archive snapback states.

### Bounds (F2, F3, F4)

- Every run has a deadline (`backup-snapshot-timeout`, 2 minutes for a list asked by the API);
  at the deadline the SSH connection is closed or the process killed (`WaitDelay` for children
  holding the pipes). No action runs on the monitor tick; take, delete and snapback run in
  their own goroutine, one action per server at a time.
- Output kept: the first 1 MiB of stdout (`limitedBuffer`, the records come first), the last
  4 KiB of stderr (`capBuffer`, the error is the last line), 512 commands/snapshots/log lines
  per run; `CHANGED` is read wherever it is.

### Security

- Snapshot names and targets are validated (`ValidSnapshotName`, `ValidSnapshotTarget`) and
  passed to the helper as quoted variables (`shellQuote`), never spliced into a command line.
- `backup-snapshot-script` is a program run on the replication-manager host: setting it through
  the API needs the global-settings grant (exclusive ACL rule), not cluster-settings.
- Snapback needs both db-restore and db-maintenance.

## Not in this step

- Running the zfs/lvm backends on OpenSVC or Kubernetes services: needs a host-side runner (an
  orchestrator task resource, for instance) and a result channel; a client script covers it
  meanwhile.
- A schedule for snapshots; snapshots as entries of the backup catalogue (`BackupMetaMap`), with
  the GTID read around the take.
- Storage-array plugins (enterprise, private repository).

## Tests

- `config/snapshot_test.go`: plugin choice, script override, enterprise tier, keep bounds,
  validators.
- `cluster/srv_volume_snapshot_test.go`: output parsing and its bounds, prune selection, paths,
  executor choice per orchestrator, refusals, one action per server, the states, and the
  embedded helper end to end against fake `zfs`/`findmnt` (common dataset, recursive take,
  snapback refusal when a child lacks the snapshot, half-way detection, delete, prune).
