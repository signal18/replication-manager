# Seed and reseed from the parent: the restore on the master (#2124)

How a staging or child cluster (`CHILD_CLUSTERS.md`) is loaded from its parent's logical
backup, at creation and at every reseed, and how the staging refresh reloads its standalone.
Validated on preprod s18 (parent `belair`, MariaDB 11.8; `belair-staging-test`, 11.4; an
858 MB splitdump backup).

Entry point: `seedFromParent` (`cluster/cluster_staging.go`), from the creation and from
`staging-reseed-from-parent`. In order:

1. `freshenParentBackup`: the parent backup is not behind the child master (below);
2. `ReseedFromParentCluster`: the child master loaded from the parent's backup with its
   binary log on, so its replicas receive the data, then positioned on the parent;
3. staging only: `waitStagingReplicaInSync` on the first replica, then `RefreshStaging`
   detaches it. A child waits for nothing: its replicas catch up through the master's
   binlog, their normal state, and a logical backup of the master starts in the background.

## Positioning (MariaDB)

- The splitdump restore sets `gtid_slave_pos` **before** the load (`restoreSplitdumpWithMysql`),
  whatever the monitored channel: it was skipped when `HaveMariaDBGTID` (read from the
  cluster's own channel, which a child master does not have) was false, and set after the
  load MariaDB refused it (1948): the restore writes in the child's own domain.
- The position is the backup's for the domains the backup has, the server's
  `gtid_current_pos` for the others (`reseedGTIDSlavePos`, `setSeedGTIDSlavePos`, through
  `dbhelper.SetGTIDSlavePos`): a child master replicates its parent's domain and writes its
  own.
- **Domain of the name** (`configurator.ReplicationDomainOfName`, CRC32): the parent's + 1 gave
  two children of one parent, or a child of a child, the same domain.
- MySQL / Percona: `GTID_PURGED` after the load, unchanged.

## A reseed never takes the parent's domain back (`freshenParentBackup`)

A child master that already replicated its parent holds later GTIDs of the parent's domain
than an older parent backup. Reloaded from it, the channel resumes before them and
`gtid_strict_mode` refuses to binlog them (1950: backup at `4-…-3245370`, db1 at
`4-…-3441701`). `RESET MASTER` would break the child's replicas. So:

- the parent channel is stopped first, the child's position stops moving;
- the child's `gtid_binlog_pos` is compared per domain with the backup's position, on the
  domains the backup has only (`parentBackupBehind`; the child's own domain does not count);
- behind: a fresh logical backup of the parent is taken (`JobBackupLogical` on the parent's
  backup server); still behind after it, the reseed is refused;
- `freshenParentBackup` says whether it stopped the channel. A refusal of
  `ReseedFromParentCluster` before it touches the target (`parentReseedRefused`: no backup,
  a dump it cannot position, no parent master, a reseed running) starts it again; a load
  that failed or was cancelled leaves it stopped, the data being incomplete, and marks it
  (WARN0252 "reseed again") until a reseed ends. Only a channel the reseed stopped is
  started again (law T27).

## The restore on the master (`cluster/logical_restore_gate.go`)

`startLogicalRestoreGate(ctx)`, for a restore with `sql_log_bin=1` (splitdump workers and the
single-file stream alike). The restore writes the whole backup to the master's binlog in a
few minutes; the replicas apply it far slower.

**Binlogs bounded.** `max_binlog_total_size` (MariaDB 11.4+) is written by the configurator
from `prov-db-binlog-total-size-pct` of the plan's disk (default 25 %, never under 2G,
`GetConfigMaxBinlogTotalSize`) under the dynamic config. During the restore it is held at 2G
on the master and its replicas (`holdBinlogTotalSizeForRestore`), replicas with
`slave_connections_needed_for_purge` 0 (a replica has no replica of its own: with the default 1
MariaDB never purges it). At the end the plan's value comes back under the dynamic config,
the value found otherwise; the servers are read again by URL (a reload rebuilds the
`ServerMonitor`). `ForcePurgeBinlogs` leaves it alone meanwhile
(`restoreHoldsBinlogTotalSize`). Before 11.4 there is no such variable: the gate purges.

**The gate.** `CheckBinaryLogs` calls `pauseLogicalRestoreOnBinlogRotation` on each rotation of
the master's binlog: the stream stops between two statements (`waitLogicalRestoreGate`, the
gate reader) until every replica taking part applied the rotated binlog
(`Relay_Master_Log_File` == current) **and** has no delay (`Seconds_Behind_Master` 0),
`logicalRestoreGateHolder`. A stopped replica (IO or SQL) or a delayed one (`hosts-delayed`,
`MASTER_DELAY`) does not take part. Binlog archiving running also holds it. No timer: WARN0254
names what holds it. One wait at a time (`logicalRestoreGatePurging`).

Built on the existing binlog monitoring and purge, no second watcher. The replicas' apply
speed sets the pace: 858 MB in about 12.5 min, 7 rotations.

**Semi-sync paused** on the master for the restore, every flavor
(`rpl_semi_sync_master_enabled`, `rpl_semi_sync_source_enabled` from MySQL 8.0.26), decided on
the setting read live (on a new master the status was still OFF when the restore started:
99233 commits then waited for an ack), put back only when it was on. 16.5 min → 12.5 min.

**The end.** The last transactions land after the last rotation: `stop` waits for every
replica taking part to reach the master's position read once (`waitReplicasAppliedAll`),
then lets the stream go, puts back the binlog hold and semi-sync.

**Cancel** (`job-cancel/reseedmysqldump`, #2134). The gate holds the restore's context: a
paused worker or stream returns its error, `stop` of a cancelled restore puts the settings
back without waiting for the replicas. The restores of `ProcessReseedLogical` and
`ReseedFromParentCluster` run on a context registered under the task (`registerJobCancel`);
the single-file client runs with `exec.CommandContext`. Not covered: the preparation before
the registration (the fresh parent backup included).

## Waits: `WaitSyncToTarget` (`cluster/srv_wait.go`)

A documented copy of `WaitSyncToMaster`, which stays untouched for its callers (switchover,
rolling operations): that one reads the master's position at each call (a master that keeps
writing, as a child master replicating its parent, is never reached) and returns nothing (a
timeout looks like a success). Here:

- the target is read once, live (`ReadReplicationTarget`: `gtid_binlog_pos`, or the binary
  log file and position); an empty one is refused (`MASTER_GTID_WAIT` of an empty GTID
  answers at once);
- `MASTER_GTID_WAIT` / `MASTER_POS_WAIT` / `SOURCE_POS_WAIT` in 30 s turns, the result read:
  -1 not reached, NULL an error;
- no timer around it (a load can take days). It stops on an SQL error of the wait, a broken
  replication, or a replica whose applied position (`AppliedPosition`) stayed still for 90 s
  (`replicaProgress`, measured in time: a turn answering at once must not count). One
  transaction of the restore (~290 MB) kept a replica's position still for about 40 s.

Used by the end of the restore and by the staging refresh before it detaches a replica (a
replica in state Slave at 2 s was detached 660 MB short).

## Staging refresh (`RefreshStaging`)

- The standalone is reloaded from the **staging master's own backup**, never the parent's:
  the parent's backup holds a position of the master before its own reload, unknown to the
  staging's binlogs (1236). After a reseed from the parent, the master's backup is taken
  first.
- The refresh prepares the reseed (`JobReseedLogicalBackup`) and waits for the one the
  monitor runs (`waitReseedEnd`, no timer); running it too made two restores write the same
  tables.
- The replica and the standalone are read again by URL after each long wait.
- A server with no channel (the standalone) answers 1617 to the reseed's STOP / RESET SLAVE:
  not an error (`noReplicationChannel`).

## Sizing (#2132, #2133)

- `AlignDBResourceToPlan` sizes the new cluster's DB resource from its per-node DBU before
  its servers are provisioned (`server_child_cluster.go`): `ChangePlanUnits` aligns only on a
  change, and a 2 DBU child kept the flag defaults (1 core, 4G, 300 IOPS,
  `innodb_io_capacity_max` 300).
- InnoDB read and write IO threads never under 4 (`innoDBMinIoThreads`): one write thread on
  a small plan throttled the load.

## Tests

- Unit (`-race`): `logical_restore_gate_test.go` (gate holder, the loop held and resumed, the
  hold 11.4+ / purge 10.11, a cancel releasing a paused stream, semi-sync on the setting),
  `cluster_staging_wait_test.go` (staging wait, no progress, SQL error, `parentBackupBehind`,
  `waitReseedEnd`), `replication_domain_test.go`, `semisync_test.go`,
  `srv_job_backup_splitdump_gtid_test.go`, `TestNoReplicationChannel`.
- Regtests `testCreateStagingSplitdump`, `testCreateChildSplitdump`
  (`regtest/test_child_cluster_create.go`).
- Preprod: staging creation from a splitdump and a single-file backup, reseed from the parent
  (fresh parent backup), refresh, cancel during the load and during a gate pause.
