# myloader and the replication source (`--source-data=0` on restore)

## Problem

`GetMyDumperCompatibleOptions()` adds `--source-data=2` to every mydumper backup that
configures neither `--source-data` nor `--replica-data`, so that the binary log file,
position and GTID set land in the backup's `metadata` (`applyMyLoaderReplicaPosition`
reads them). With `2`, mydumper also writes `myloader_exec_change_source = 1`
(bits: 1 reset replica, 2 change source, 3 both, 0 neither).

The restore passed no `--source-data` to myloader, which therefore followed the metadata
and sent `CHANGE REPLICATION SOURCE TO SOURCE_PORT=…, SOURCE_LOG_FILE=…, SOURCE_LOG_POS=…`
on its own:

- MariaDB (every version tested: 10.6, 10.11, 11.8, 12.2) has no such statement: error 1064.
  With myloader 1.0.1 it exits 1 after the data is fully loaded, so `jobReseedMyLoader`
  returned an error and `applyMyLoaderReplicaPosition` never ran. With myloader 0.18.1 the
  error is logged and the restore completes (both measured; the versions in between were
  not tested).
- MySQL/Percona: on a node without a channel the statement is accepted and leaves no
  replication configuration behind. On a replica that already has a channel with
  SOURCE_AUTO_POSITION active (a reseed, a flashback rejoin) the server refuses it with
  error 1776 ("SOURCE_LOG_FILE ... cannot be set when SOURCE_AUTO_POSITION is active") and
  myloader exits 1 (MySQL 8.4 and Percona 8.0, myloader 1.0.1): the same failure as on MariaDB.

## Change

`myLoaderNoSourceChangeArgs` (`cluster/cluster_tool.go`) appends `--source-data=0` to the
myloader arguments in `jobReseedMyLoader`, the single place myloader is executed (reseed,
flashback, seeding from a parent cluster). It overrides the metadata flag and leaves the
recorded position in the metadata. It is done on the restore, not only on the dump,
because the backups already taken carry the mark.

Not added when:

- `backup-myloader-options` already holds `--source-data` (the user's value wins);
- the myloader that runs is older than 0.18.1 (0.17.1 rejects the option: "Unknown option
  --source-data=0" fails the restore);
- the myloader version can not be read.

The version is read from the myloader that the restore runs (`myLoaderVersion`:
`GetMyLoaderPath() --version`), not from the detected mydumper version: repman detects only
mydumper's, and `backup-myloader-path` or the PATH can hold another release. Run with
mydumper 0.18.1 detected and a 0.17.1 myloader, a decision taken from mydumper's version
failed the reseed on MariaDB 12.2; reading myloader's own version restores the behavior of
`develop` there (no option added).

The replication position is still applied by `applyMyLoaderReplicaPosition`, which does
not depend on myloader's own change.

## Verification

Isolated Docker pairs (source + replica, binary log on, GTID on for MySQL/Percona), the
default dump (`--source-data=2`), myloader 1.0.1 and 1.0.11, MariaDB 10.6, 10.11,
11.8, 12.2, MySQL 8.0, 8.4, Percona 8.0, 8.4: without the option, error 1064 and
exit 1 on every MariaDB, error 1776 on a MySQL/Percona replica with an auto-position channel;
with it, no replication command, exit 0, rows and checksum equal,
and the replica positioned from the metadata GTID catches up with writes made after the
backup. The Go test (`cluster/cluster_tool_test.go`) pins the arguments only; the
regtest suite (T13) remains the gate.
