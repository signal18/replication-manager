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
| `config/snapshot_plugin_settings.go` | The settings of the external plugins (`[[snapshot-plugin]]`), their environment |
| `cluster/srv_volume_snapshot_plugin.go` | Loading of the external plugins (`snapshotplugin-*`), their settings for the API |
| `cluster/srv_volume_snapshot_host.go` | The host steps of an external plugin: devices, unmount and mount around its snapback |
| `share/scripts/volume_snapshot.sh` | The helper that runs the zfs and lvm commands and the host steps (embedded) |
| `server/api_volume_snapshot.go` | The endpoints |
| `share/dashboard_react/src/components/Modals/VolumeSnapshotsModal` | The GUI |
| `share/dashboard_react/src/components/SnapshotPluginSettings` | The settings form of an external plugin |

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

An auto-resolved ZFS target is refused when it is a pool root, or when a dataset of its tree is
neither a path dataset, an ancestor or descendant of one, nor mounted inside a server path
(`zfs_holds_server`): its snapback would roll back data of something else. `backup-snapshot-target`
names it explicitly (the operator's choice).

Every command of the helper has stdin closed (`x`, `q`): under `sh -s` the script itself is on
stdin. An LVM snapback whose second `umount` fails mounts back the first and reports nothing
changed; if that mount fails too, it prints `CHANGED` (half-way).

The snapback claims the server before its checks (`beginVolumeSnapshotOp`, released with
`releaseVolumeSnapshotOp` when they refuse), its list bounded as the API's. A stop that fails,
or a refusal found once stopped, starts the server again when it does not answer
(`snapbackUndoStop`). Every action runs in `goVolumeSnapshotOp`, which recovers a panic and
ends the op (half-way for a snapback).

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

### External plugins: settings, devices, unmount (#2456)

What an external plugin needs from replication-manager to drive a storage system, beyond the
4.0.1 contract:

- **Settings.** `SnapshotPluginManifest.Settings` is the archive plugins' `PluginSettings`
  (T2), read from `--manifest` and checked by `RegisterSnapshotPlugin`
  (`validSnapshotPluginSettings`: names the environment carries, known types).
  `Tested *bool` is shown in the GUI when false.
- **Storage (T16, F9, T25).** `config/snapshot_plugin_settings.go`: `[[snapshot-plugin]]`
  entries (`SnapshotPluginEntry`: plugin, settings by name), one per plugin, so switching
  `backup-snapshot-plugin` keeps each one's settings.
  - `PutSnapshotPluginSettings` applies a change with the shared `applyEntrySpec`: typed values,
    the secret encrypted by `typedSettingValue`, an empty secret kept. It refuses a secret when
    no key is loaded, so it is never stored in clear.
  - The save callback renders the entries with `writeEntryToml` (`SnapshotPluginEntriesToml`),
    unless a config file pins `snapshot-plugin`.
  - `MarshalJSON` uses `splitSecretSettings`: the API gets the names of the secrets only. A value
    held encrypted is hidden even when the plugin is not loaded.
- **Environment.** `SnapshotPluginEnv`: `REPMAN_SNAPSHOT_SETTING_<NAME>` and the decrypted
  `REPMAN_SNAPSHOT_SECRET_<NAME>`, added in `contractSnapshotter.runCtx` for an external plugin
  only (the client's script keeps `GetExecEnv`). The environment is never logged. The change
  log names the settings changed, never a value.
- **Devices.** `contractSnapshotter.host` is the helper's executor on the database host
  (`volumeSnapshotHostExecutor`: SSH or local, the same rules as the built-in plugins), nil
  without access.
  - The helper's `devices` action prints `DEVICE` records (`lsblk -s` from the mount source:
    every disk's WWN) or `UNRESOLVED`.
  - `hostDevices` passes them as `REPMAN_SNAPSHOT_DEVICES`, or nothing when one path is
    unresolved: never a partial list, which would let a plugin's "holds only the server's
    files" check pass on an incomplete set.
- **Unmount (T14: `backup-snapshot-unmount`).** `snapbackUnmounted` wraps the plugin's
  snapback in the helper's `unmount` and `mount` actions, under the action's deadline.
  - The mount gets a deadline of its own (`volumeSnapshotDownTimeout`): an unchanged volume is
    never left unmounted because the plugin used the whole deadline.
  - `unmount` refuses a system mount point. It waits for the volume to be unused (`wait_unused`),
    unmounts deepest first through `unmount_pairs` (shared with the LVM snapback, T2: on a
    failure it puts back what it unmounted, or says CHANGED), and prints the `MOUNT` records the
    `mount` action replays after `blockdev --flushbufs`.
  - Outcomes: an unchanged plugin failure is mounted back and unchanged. A changed failure is
    left unmounted, half-way. A failed mount counts as changed (ERR00123), so the server is not
    started.
- **API, ACL (T6).** `GET /api/clusters/{c}/volume-snapshot-plugins` (show-backups, db-backup or
  global-settings) lists the plugins with their entries and `allowed`.
  `POST .../volume-snapshot-plugin-actions/set/{plugin}` sets them: an exclusive rule
  (`pricingACLRules`) on global-settings, the grant of `backup-snapshot-script`, since the
  settings point a program on this host at a storage system and give it a credential.
- **GUI.** `components/SnapshotPluginSettings`: one field per declared setting, the secret a
  password field with its own Save (never in a confirmation text), badges for enterprise,
  "needs an enterprise plan" and "untested". BackupSettings lists the loaded plugins in the
  plugin dropdown.

The links (T27): the host steps unmount and mount file systems; they stop, repoint or reset no
replication channel.

## Not in this step

- Running the zfs/lvm backends on OpenSVC or Kubernetes services: needs a host-side runner (an
  orchestrator task resource, for instance) and a result channel; a client script covers it
  meanwhile.
- A schedule for snapshots; snapshots as entries of the backup catalogue (`BackupMetaMap`), with
  the GTID read around the take.
- Storage-array plugins: an enterprise, private plugin speaks this contract with the
  settings, devices and unmount above (#2456).

## Tests

- `config/snapshot_test.go`: plugin choice, script override, enterprise tier, keep bounds,
  validators.
- `cluster/srv_volume_snapshot_test.go`: output parsing and its bounds, prune selection, paths,
  executor choice per orchestrator, refusals, one action per server, the states, and the
  embedded helper end to end against fake `zfs`/`findmnt` (common dataset, recursive take,
  snapback refusal when a child lacks the snapshot, half-way detection, delete, prune).
- `config/snapshot_plugin_settings_test.go`: manifest settings checked, a round trip through
  the saved TOML (the secret encrypted, read back, decrypted only into the environment), the
  API JSON without the secret, a secret refused without a key, a secret hidden for a plugin
  not loaded.
- `cluster/srv_volume_snapshot_host_test.go`: the helper's host steps against fake
  `findmnt`/`lsblk`/`umount`/`mount`/`blockdev`/`fuser` and a fake external plugin: settings,
  secret and devices in its environment (none when a path is unresolved), the order unmount,
  restore, flush, mount, every refusal before a change (system mount point, busy, a failed
  unmount put back, a plugin refusal mounted back), the half-way cases, the switch off.
- `regtest/test_volume_snapshot_external_plugin.go`: a fake external plugin end to end
  through the API on a real cluster (T13).
