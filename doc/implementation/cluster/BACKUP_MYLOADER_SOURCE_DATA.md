# myloader and the replication source (`--source-data=0` on restore)

## Problem

`GetMyDumperCompatibleOptions()` adds `--source-data=2` to every mydumper backup that
configures neither `--source-data` nor `--replica-data`, so that the binary log file,
position and GTID set land in the backup's `metadata` (`applyMyLoaderReplicaPosition`
reads them). With `2`, mydumper also writes `myloader_exec_change_source = 1`
(bits: 1 reset replica, 2 change source, 3 both, 0 neither).

The restore passed no `--source-data` to myloader, which therefore followed the metadata
and sent `CHANGE REPLICATION SOURCE TO SOURCE_PORT=…, SOURCE_LOG_FILE=…, SOURCE_LOG_POS=…`
on its own. What happened without the option (the baseline), as measured:

- MariaDB (10.6, 10.11, 11.8, 12.2): no such statement, error 1064. What myloader does next
  depends on its release. Tool level, MariaDB 12.2, the 33 releases from 0.18.1 to 1.0.19 that
  could take a backup with repman's dump arguments: 0.18.1 and 0.19.1 to 0.19.3 log the error and
  complete; every release from 0.20.1 on (1.0.1 included) exits 1 after the data is fully
  loaded. With exit 1, `jobReseedMyLoader` returns an error and `applyMyLoaderReplicaPosition`
  never runs: the reseed is reported failed.
- MySQL/Percona: on a node without a channel the statement is accepted and leaves no
  replication configuration behind. On a replica that already has a channel with
  `SOURCE_AUTO_POSITION` active (a reseed, a rejoin by logical backup) the server refuses it with
  error 1776 ("SOURCE_LOG_FILE ... cannot be set when SOURCE_AUTO_POSITION is active") and
  myloader 1.0.1 exits 1. Controlled comparison (MySQL 8.4.13, Percona 8.0.46, myloader 1.0.1-1,
  same backup artifact): the reseed is reported failed, the replica's data differs from the
  primary's, it is not positioned, replication is stopped before catch-up, and the channel
  definition is kept.

## Change

`myLoaderNoSourceChangeArgs` (`cluster/cluster_tool.go`) appends `--source-data=0` to the
myloader arguments in `jobReseedMyLoader`, the single place myloader is executed. Three entry
points reach it: `ProcessReseedLogical` (a logical reseed) and `JobFlashbackLogicalBackup` (a
rejoin by logical backup), both through `JobReseedMyLoader`, and `ReseedFromParentCluster`
(seeding a child from its parent cluster), through `JobReseedMyLoaderFromParent`. The option
overrides the metadata flag and leaves the recorded position in the metadata. It is done on the restore, not only on the dump,
because the backups already taken carry the mark.

Not added when:

- the arguments returned by `GetMyLoaderCompatibleOptions()` (built from
  `backup-myloader-options`) already hold `--source-data` or `--source-data=...`: the user's
  value wins and is preserved;
- the myloader that runs is older than 0.18.1 (0.17.1 rejects the option: "Unknown option
  --source-data=0" fails the restore);
- the myloader version can not be read in time.

The version is read from the myloader that the restore runs (`myLoaderVersion`:
`GetMyLoaderPath() --version`), not from the detected mydumper version: repman detects only
mydumper's, and `backup-myloader-path` or the PATH can hold another release. Run with
mydumper 0.18.1 detected and a 0.17.1 myloader, a decision taken from mydumper's version
failed the reseed on MariaDB 12.2; reading myloader's own version restores the behavior of
`develop` there (no option added).

The probe is bounded (`readMyLoaderVersion`): 5 seconds (`exec.CommandContext`), so a hung
binary can not hold the reseed job. `WaitDelay` (2 seconds) bounds the wait for the output
pipes once the deadline has killed the process, so a child that inherited them can not keep
the probe blocked; it does not terminate the descendants of the probe, which may outlive it. A failure, a timeout or an output without a version
returns nil, logs one warning in the task log with the path and the reason (no credentials are
involved), and the restore goes on without the option, as before this change.

## Replication positioning is not changed

This change suppresses myloader's own, independent replication source statement. It does not
modify repman's channel positioning, STOP/START or GTID reset operations. The entry points are
separate and each keeps its own positioning:

- `ProcessReseedLogical` (the logical reseed the monitor runs for a reseed request, and the one
  `RefreshStaging` prepares to reload its standalone from the staging master's own backup during
  a rotation) and `JobFlashbackLogicalBackup` (rejoin by logical backup) each call
  `JobReseedMyLoader` and then `applyMyLoaderReplicaPosition` themselves. After the load repman
  positions the replica from the metadata (MariaDB: `RESET MASTER` and `gtid_slave_pos`;
  MySQL/Percona: `RESET MASTER` or `RESET BINARY LOGS AND GTIDS` and `GTID_PURGED` set to the
  backup's executed set). Measured for the logical reseed in the lifecycle runs and in the
  controlled comparison: the position equals the backup's and the replica catches up with writes
  made after the backup. The rejoin by logical backup shares the helper; its own runs are the
  repman-level lifecycle and negative tests listed below.
- `ReseedFromParentCluster` (parent-to-child seeding: the staging bootstrap) is a different
  path: it calls `JobReseedMyLoaderFromParent`, which loads the data only (no system schemas, no
  account) and does not call `applyMyLoaderReplicaPosition`. The same myloader statement is
  suppressed there, and its positioning code is not touched here. Measured on the staging route
  (MariaDB 12.2, MySQL 8.4): the child master equals the parent, replicates from it and receives
  the parent's later transactions, with the same result on the baseline and the candidate;
  neither side received a repman position statement on the child master, so this is evidence of
  convergence, not of an explicit post-restore position. Read in the code, not run: the MyDumper
  branch sets no position for MySQL/Percona and the MariaDB position branch is conditional.
  Direct child bootstrap (`SeedChildFromParent`) was not run.

## Verification

Legend: repman-level is through the repman API call (backup, then reseed by logical backup) on
real databases in Docker; tool-level drives mydumper/myloader directly with repman's real
argument lists. Actual options are repman's default `backup-myloader-options`; adjusted options
are ones the loader under test accepts (diagnostic).

Candidate (with `--source-data=0`), as measured:

- Repman-level, myloader 1.0.1 (and 1.0.11 on MariaDB 12.2 and MySQL 8.4), MariaDB 10.6, 10.11, 11.8, 12.2, MySQL 8.0, 8.4,
  Percona 8.0, 8.4: no replication command sent by myloader, exit 0, rows and checksum equal, the
  replica positioned from the metadata GTID catches up with writes made after the backup.
  Also replica-origin backups, repeated reseed, negative tests (missing GTID, positioning failure,
  killed myloader) and the `testBackupDiscovery` mydumper steps (MariaDB 12.2, 11.8, 10.11, MySQL
  8.4).
- Controlled repman-level comparison, MySQL 8.4.13 and Percona 8.0.46 (adjusted options, same
  myloader binaries, same backup artifact, same configuration, B1 on both sides): the baseline
  fails with error 1776 as above; the candidate succeeds with data equal to the primary,
  `GTID_PURGED` equal to the backup's set, the channel definition preserved and a later
  transaction reaching the replica.
- Tool-level, MariaDB 12.2, 118 valid plain-versus-flag pairs (identical binary, artifact and
  options): 47 improved, 24 passed in both modes, 47 failed in both modes with an identical
  failure mode, none worse. All 71 successful flagged restores met the data and catch-up
  assertions.
- Version guard run on all 47 MyDumper releases from 0.16.1 to 1.0.19: 33 get the flag, the 14
  below 0.18.1 do not.

Limits: the default `backup-myloader-options` already fail on loaders from 0.19.1 on in the
configuration tested (MariaDB 12.2 same-version controls), with and without the option; the
passes above for those loaders use adjusted options. The Go tests
(`cluster/cluster_tool_test.go`) pin the arguments and the bounded version probe only. The
Docker runs above, including the `testBackupDiscovery` steps, are the evidence for T13; the full
regtest suite was not run for this change.

## Managed default options on newer MyLoader releases (#2433)

The shipped `backup-myloader-options` hold `--overwrite-tables` and `--innodb-optimize-keys=skip`. Upstream (read at the release tags):

- `--innodb-optimize-keys` was renamed `--optimize-keys` in 0.19.1 (same callback, same variables, same consumers). The old name is unknown in 0.19.1-1/-2, refused with a fatal "deprecated" error in 0.19.1-3 to 0.21.4 and unknown from 1.0.0.
- `--overwrite-tables` is accepted but has no effect from 0.20.1: commit 0ae8ffb9 moved the DROP mode to `--drop-table` and `purge_mode` is no longer derived from it, so the restore stops on the first existing table (exit 133, nothing loaded). Unknown from 1.0.0. `--drop-table=DROP` runs the same `DROP TABLE IF EXISTS` / `DROP VIEW IF EXISTS` of each object of the backup.

`jobReseedMyLoader` therefore builds the arguments with `myLoaderRestoreArgs`: the version of the myloader that runs (bounded probe, read once per restore, also used by the `--source-data=0` guard) selects the adaptation:

| MyLoader | shipped `--overwrite-tables` | shipped `--innodb-optimize-keys=skip` |
|---|---|---|
| up to 0.18.1 | kept | kept |
| 0.19.1 to 0.19.3 | kept | `--optimize-keys=SKIP` |
| 0.20.1 and later | `--drop-table=DROP` | `--optimize-keys=SKIP` |

Scope and safeguards:

- Opt-in, on the shared logical replica reseed path. `JobReseedMyLoaderReplica` is the one entry that asks for the adaptation; `JobReseedMyLoader` (the rejoin by logical backup, `JobFlashbackLogicalBackup`) and `JobReseedMyLoaderFromParent` keep their arguments, as does a restore on the master (which adds `--enable-binlog`, refused or ignored by some releases). `ProcessReseedLogical` opts in unless the reliable execution context says otherwise (`isOrdinaryMyLoaderReseed`): a point-in-time restore (`PointInTimeMeta.IsInPITR`), the staging refresh (`Cluster.IsRefreshStaging`) and the configured staging server (`IsActiveStagingServer`) are excluded.
- Accepted scope, not an exclusive one: the adaptation is not limited to operator-initiated reseeds. A logical reseed armed by the automatic rejoin (`autorejoin-logical-backup`) or by the logical-backup cookie reaches `ProcessReseedLogical` through the same task as the operator's request, and the task carries no origin (`reseedFromRejoin` is set only after the arming returns). Those callers cannot be told apart there and receive the adaptation. The automatic seed of a fresh node (`autoseed`) was validated at runtime (MariaDB 12.2, MyLoader 1.0.1-1). A synthetic attempt on the logical-backup cookie handler remained inconclusive (WARN0111 never opened, so the handler was not exercised); no other automatic caller was validated separately. Recording the reseed origin to exclude them is a change of the recovery path and is not part of this fix.
- Only R0 (`--source-data=0`), R1 and R2 read the version of the myloader that runs. `GetMyLoaderCompatibleOptions` keeps its legacy rule and runs first; it is keyed on the detected MyDumper version (`VersionsMap` "mydumper", the executable found on the monitor host), not necessarily the version that produced the backup, and removes `--innodb-optimize-keys=skip` for a MyDumper before 0.16.7.
- Only the exact shipped default value is adapted, and only when no configuration source set it: neither the immutable map (config file, environment, flag) nor the dynamic map has the key (read through `GetImmuableFlag` / `GetDynamicFlag`). String equality is an additional guard, never a substitute for provenance. Any other value is left as it is, with a warning for each option the release rejects. Nothing is written to the persistent configuration. Setting `backup-myloader-options` explicitly is the way to opt out.
- Unknown version (probe failure): nothing is adapted, as before.
- Known limitation: a value set through the API is recorded in neither map (the framework does not track it), so one identical to the shipped default cannot be told from an untouched default and is adapted; a different API value is left as it is.
- Not changed: MyDumper arguments, replication positioning, GTID handling, metadata, failover, rejoin, staging workflows.

Verification: unit tests (`cluster/cluster_tool_test.go`) for the boundaries, mixed dumper and loader versions, explicit and custom values, unknown versions and one probe per restore. Exclusions: call-path tests (the real restore function with a recording myloader) cover the rejoin by logical backup (`JobReseedMyLoader`) and the staging refresh (`IsRefreshStaging`) against the ordinary reseed; the master, parent seeding, point-in-time restore and configured staging server are covered by helper-level tests only. Runtime: reseeds through the API on MariaDB 12.2, MySQL 8.4 and Percona 8.0 (see the PR), an automatic seed of a fresh node, and the existing `testBackupDiscovery` regtest, whose MyDumper checks pass on the three engines (its mysqldump-tool checks have failures unrelated to myloader on MySQL 8.4 and Percona 8.0, see the PR).
