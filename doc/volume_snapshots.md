# Volume snapshots

replication-manager can snapshot the volume that holds the files of a database server, keep
the latest snapshots, and put the server back to one of them (snapback). Two storage backends
come with it, open to every plan:

- **zfs**: a recursive snapshot of the dataset holding the server's files, rolled back with
  `zfs rollback`;
- **lvm**: a snapshot of the logical volume holding them (thin, or copy-on-write), merged back
  with `lvconvert --merge`.

You can replace them with your own script for every action (any storage: a SAN, a cloud
disk, an orchestrator of your own). Storage-array plugins come later as signed plugins of the
enterprise plans.

Volume snapshots are off by default (`backup-snapshot`).

## Where the commands run

The zfs and lvm commands run **on the database host**:

- with the **on-premise** orchestrator, over the SSH connection replication-manager already
  uses for that host (`onpremise-ssh`, `onpremise-ssh-credential`). The commands run as the
  SSH user: root runs them itself, another user through `sudo -n`: a sudoers entry without
  password for `zfs`, or for `lvs`, `lvcreate`, `lvremove`, `lvchange`, `lvconvert`, `umount`
  and `mount`, and for `fuser` in both cases (`findmnt` needs none). `fuser` (package psmisc)
  is required for a snapback;
- with the **localhost** orchestrator, on the replication-manager host itself.

On OpenSVC and Kubernetes the database host is not reachable that way: use your own script
(`backup-snapshot-script`). OpenSVC keeps its own scheduled ZFS snapshots of the database
services (`prov-db-disk-snapshot-prefered-master`).

Every command is written to the log before it runs (module `task`), and the GUI shows the
commands of the last action of each server: you can replay any of them by hand.

## Which volume

When `backup-snapshot-target` is empty, replication-manager finds the volume from the
server's own directories: its datadir, the InnoDB redo, undo and data directories, and the
directory of its binary logs.

- **zfs**: the deepest dataset that holds all of them, snapshotted recursively (`zfs snapshot
  -r`), so the datadir, the redo log and the binary logs are taken at the same instant even
  when they are separate datasets.
- **lvm**: the logical volume that holds all of them. When they are on several logical
  volumes, the snapshot is refused: a snapshot of one of them would not be consistent.

A dataset found this way is refused when it is a pool root, or when a dataset under it holds
none of the server's files (and is not mounted inside one of its directories): a snapback rolls
back every dataset under the one snapshotted, so it is never chosen on a guess. Set
`backup-snapshot-target` (a dataset `pool/dataset`, or `vg/lv`) to name it yourself. A target
you name goes through the same checks: never a pool root, it must hold every server directory,
and (ZFS) no dataset under it may hold something else; an LVM volume you name must be the one of
the server's directories.

So the server's directories must all sit under one dataset below the pool root (for example
`tank/db/data`, `tank/db/redo`, `tank/db/binlog` under `tank/db`) holding nothing else, or on one
LVM volume. A layout that spreads them over the pool (`tank/mysql/data` and `tank/binlog`) can not
be snapshotted by the built-in backends: move them, or use `backup-snapshot-script`.

An LVM snapshot of a thick volume has a fixed copy-on-write space (`backup-snapshot-lvm-size`):
when the writes after the snapshot fill it, LVM invalidates the snapshot and a snapback to it
fails. Size it for the writes of the time you keep it, or use a thin volume.

The lvm backend reads the creation time of a snapshot with GNU `date -d` (Linux coreutils);
without it the time shows as unknown, the retention still orders the snapshots by their name.

## Settings

| Setting | Default | |
|---|---|---|
| `backup-snapshot` | `false` | Volume snapshots on (the off-switch) |
| `backup-snapshot-plugin` | `zfs` | `zfs`, `lvm`, or an external plugin |
| `backup-snapshot-target` | empty | The dataset or `vg/lv`; empty finds it |
| `backup-snapshot-keep` | `3` | Snapshots taken by replication-manager kept per server, 1 to 64 |
| `backup-snapshot-lvm-size` | `20%ORIGIN` | Copy-on-write space of an LVM snapshot of a thick volume (`10G`, `20%ORIGIN`); a thin volume takes none |
| `backup-snapshot-script` | empty | Your script, run for every action in place of the plugin (instance admin only) |
| `backup-snapshot-timeout` | `1800` | Seconds an action may run before it is stopped |

In the GUI: cluster settings, Backups, Volume Snapshots.

## Consistency

A snapshot is **crash-consistent**: it is taken while the server runs, without a lock, as if
the host had lost power at that instant. InnoDB recovers it at the start after a snapback, as
after a crash. Tables of an engine without crash recovery (MyISAM, Aria without its log) may
need a repair. The snapshot does not record a replication position: on a replica the position
is the one its own volume holds (see Snapback).

## Taking, listing, deleting

From the server menu, Backup, **Volume Snapshots**: the list of the server's snapshots, a
**Take Snapshot** button, and per snapshot **Snapback** and **Delete**.

- A snapshot replication-manager takes is named `repman-<UTC time>` (`repman-20261126T101500Z`).
  After each one, the snapshots named `repman-*` beyond `backup-snapshot-keep` are deleted,
  the oldest first.
- A delete is recursive on ZFS, as the take: the snapshot of that name goes on every dataset
  under the target.
- Only that exact name counts as replication-manager's: a snapshot you name
  `repman-before-upgrade` is yours. Snapshots of another origin (an OpenSVC schedule, a
  snapshot you took by hand) are listed
  and can be the target of a snapback, but replication-manager **never deletes them**.

## Snapback

A snapback puts the server's volume back to a snapshot: everything written on the server
since the snapshot is lost. It is only ever started by an operator, from the GUI (you type the
server's host to confirm) or the API; replication-manager never starts one by itself.

It is refused on the master (switch over first), on a server that accepts writes (read_only off:
a writer of a multi-master or Galera topology, a replica left writable; set it read-only first),
and during a failover. Then:

1. the server goes into maintenance;
2. it is stopped through its orchestrator;
3. once no process uses the volume any more (a database still flushing after its port closed;
   checked with `fuser`, up to 2 minutes; a snapback without a usable `fuser` is refused), its
   volume is rolled back to the snapshot. The check covers the mount points of the volume and
   the server's directories; a zvol under a ZFS tree has no mount point and is not checked. An
   LVM volume with no mount point found (not mounted, or mounted through an alias findmnt does
   not resolve) is refused. The wait is a third of `backup-snapshot-timeout`, at most 2 minutes.
   A shell of the SSH user whose directory is on the volume counts as a use: the snapback is
   then refused, nothing changed. `fuser -m` checks the whole filesystem of a path: with a
   `backup-snapshot-target` that leaves a server directory on another filesystem (binary logs
   on `/`), any process there counts as a use and the snapback is refused; keep every server
   directory on the snapshotted volume. On ZFS, the snapshots taken **after** it are
   destroyed (ZFS rolls back to the latest snapshot only). On LVM the volume is unmounted,
   the snapshot merged into it (the snapshot is consumed by the merge), and mounted again (from
   its fstab entry; a volume without one comes back mounted with the default options);
4. the server is started;
5. it **stays in maintenance** (state WARN0331) until you take it out of maintenance.

The replication links of the server are not changed: the volume holds the replication
position of its own data, and the server resumes from there. The master must still have the
binary logs from that position: when it purged them, the replica stops on a replication
error and needs a reseed. Prefer a recent snapshot, and check the master's oldest binary log
before a snapback to an old one.

The server is checked again once stopped: a switchover or failover started meanwhile, or the
server become master, stops the snapback before the rollback.

A snapback stopped by `backup-snapshot-timeout` counts as changed (the command may still run
on the host): the server is left stopped. If the rollback fails before it changed anything, the server is started again and taken out
of the maintenance the snapback put it in. If it fails after it changed the volume (a ZFS
child dataset that could not be rolled back, a merge that failed), the server is **left
stopped and in maintenance** (state ERR00123): check the volume, the commands are in the
log, before you start it.

For a thin LVM volume the sequence is `lvchange -an`, `lvconvert --merge`, `lvchange -ay`, then
the mounts: when it stops after the deactivation, the volume is left inactive and unmounted.
Activate it (`lvchange -ay vg/lv`), mount it (`mount <mount point>`), check it, then start the
server. When it stops at a mount after the merge (thin or thick), the merge is done and only
the mounts are missing: mount them, check, start.

A mount point of the volume with a blank in its path is not supported: the snapback is refused,
nothing changed.

## States

| State | When |
|---|---|
| CINF0024 | A snapshot action runs on a server |
| WARN0330 | The last action of a server failed, until its next action ends well |
| WARN0331 | A snapback left the server in maintenance, until you take it out |
| ERR00123 | A snapback stopped after it changed the volume: the server is stopped |
| WARN0332 | `backup-snapshot` is on but its plugin can not run (unknown, refused by the plan, or no access to the database host) |

## API

| Method | Path | Grant |
|---|---|---|
| GET | `/api/clusters/{cluster}/servers/{server}/volume-snapshots` | cluster-show-backups or db-backup |
| POST | `/api/clusters/{cluster}/servers/{server}/actions/volume-snapshot-take` | db-backup |
| POST | `/api/clusters/{cluster}/servers/{server}/actions/volume-snapshot-delete/{snapshot}` | db-backup |
| POST | `/api/clusters/{cluster}/servers/{server}/actions/volume-snapback/{snapshot}` | db-restore and db-maintenance |

The take, the delete and the snapback start in the background: the list returns the server's
last action (`lastOp`) with its status, error and commands.

## Your own script

`backup-snapshot-script` replaces the plugin for every action, on any orchestrator. It runs on
the replication-manager host with:

- the action as its first argument: `take`, `list`, `delete`, `snapback`;
- the context in the environment: `REPMAN_SNAPSHOT_ACTION`, `REPMAN_SNAPSHOT_TYPE` (the value
  of `backup-snapshot-plugin`), `REPMAN_SNAPSHOT_TARGET`, `REPMAN_SNAPSHOT_PATHS` (the server's
  directories, colon separated), `REPMAN_SNAPSHOT_NAME` (the snapshot of take, delete and
  snapback), `REPMAN_SNAPSHOT_PREFIX` (`repman-`), `REPMAN_SNAPSHOT_LVM_SIZE`,
  `REPMAN_CLUSTER`, `REPMAN_SERVER_HOST`, `REPMAN_SERVER_PORT`, and the API variables every
  replication-manager script gets (`REPLICATION_MANAGER_URL`, ...).

It prints tab-separated records on its standard output (any other line is log text):

| Record | |
|---|---|
| `CMD<TAB><command>` | a command it ran, shown in the log and the GUI |
| `TARGET<TAB><volume>` | the volume it acted on |
| `SNAPSHOT<TAB><name><TAB><creation, unix time><TAB><size, bytes>` | a snapshot (list, and after take) |
| `CHANGED` | a snapback changed the volume: an error after it leaves the server stopped |

It exits 0 on success; on an error it exits non-zero with the reason as the last line of its
standard error. For a snapback, replication-manager stops the server before your script runs
and starts it after. The zfs and lvm backends follow the same contract: their script is
`share/scripts/volume_snapshot.sh`, a starting point for your own.
