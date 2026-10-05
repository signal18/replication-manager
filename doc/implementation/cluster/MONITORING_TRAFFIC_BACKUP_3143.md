# 3.1.43 operations visibility: internal network, traffic marker, backup progress (one feature)

Stéphane, 2026-10-05: "all the feature goes together: monitoring, fix traffic and backup". The three
landed on feat/bku-backup-unit (PR #1827) in that order, each one found by the previous:

| step | issue | what | doc |
|---|---|---|---|
| 1 | #1883 | internal network per database, proxy and app, in and out, from the jobs scripts' pod counters; stacked per service on the Graphs page | INTERNAL_NETWORK.md |
| 1a | #1886 | found by 1: belair db1 sent 50 Mb/s flat for 11 months -- the active and the standby both presented replica server-id 12000 to the security binlog scanner, MariaDB killed each other's Binlog Dump every 5 s and re-streamed the current binlog. Fix: replica server-id pool leased per consumer and per instance, standby never streams, backoff + WARN0227/0228 | BINLOG_CONSUMERS_SERVER_ID.md |
| 2 | #1891 | found by 1: a dump on belair did not move the chart because the cluster tick was frozen -- the pseudo-GTID DDL traffic marker waited on the dump's metadata lock inside the tick's wait group. Fix (Stéphane's design): the marker runs in the background, never re-entered while one is stuck (WARN0229); WARN0230 while the DDL marker is in use; `inject-traffic-mode` defaults to `dml` | this file, below |
| 3 | #1892 | asked next: "do we have a pill for backup progress" -- none existed. Stéphane's fallback ladder: running, bytes against the previous backup's size, per-table from the dump's verbose stream; navbar Backup pill | this file, below |

The common thread is the monitoring law: we watch, and nothing a client or an operator does
on a cluster -- a dump, a lock, a second monitor -- may stop the watching. Each fix is a
tracked state composed into a signal, never a reconstruction from logs.

## Traffic marker (#1891)

`cluster/cluster_inject_traffic.go`. The tick calls `startInjectProxiesTraffic` instead of
waiting for `InjectProxiesTraffic` in its wait group: an atomic in-flight flag is taken
(`tryStartInjectTraffic`), the injection runs through `trackTickGoroutine` (tracked for the
reload drain, not awaited per tick) and releases the flag when it returns. A tick that finds
the flag held starts nothing and opens WARN0229 with the start time of the stuck injection and
the likely cause, a metadata lock held by a backup. Belair 2026-10-05: with the old code the
heartbeat stalled from the dump request (09:04:19Z) to its end (09:16:35Z); the first sample
after it showed the dump at 55.9 Mb/s, proof the sensor was fine and the tick was not.

WARN0230 is set on every tick while `injectTrafficUsesDDL()` holds (force-slave-no-gtid-mode
or inject-traffic-mode=ddl): the DDL view marker is for tests and positional rejoin only and
every marker is a binlog event flashback cannot reverse; the text names `inject-traffic-mode`
as the variable to change. The flag default moved from `ddl` to `dml` (a single-row REPLACE).
The old help text called dml experimental pending the topology matrix: rejoin and flashback on
the test clusters are the places to watch.

## Backup progress (#1892)

`cluster/cluster_backup_progress.go`, one `BackupProgress` per running backup, keyed
server/kind, stored on the cluster and snapshotted each tick into `cluster.backupsInProgress`
(web group). Levels, each one only when its data exists:

| level | source | gives |
|---|---|---|
| running | the hook in JobBackupLogicalWithOptions, JobBackupPhysicalWithOptions, JobBackupBinlog | kind, task, since when |
| bytes | bytes counted where they land (the dump's writer next to the stall watchdog counter) against `PreviousSize` = the newest completed BackupMetaMap entry of the same Source and BackupMethod | percent (capped at 99 until the job says done), rate, ETA; first run of a kind = bytes and rate, no percent |
| schema | the dump's `--verbose` stderr through `spawnLogCopierHook` (same reader as the log): "Retrieving table structure for table X" marks a boundary; sizes from the schema monitor's DictTables (data + index) of the dump source | Σ tables done over Σ all tables, current table, tables done/total, per-table throughput in a 64-entry ring, emitted at the end as `backup.<cluster>.<server>.<schema_table>.bytes_per_s` |
| archive | `ResticManager.CurrentTaskProgress()` (restic's own percent_done / bytes_done / total_bytes) | an "archive" row while restic pushes |

GUI: Navbar teal "Backup N%" pill (count = running backups) with a tooltip line per backup:
kind, server, percent or bytes, rate, ETA, current table and count, since, level. It is a
report: nothing reads it to decide anything.

Live run, belair 2026-10-05 09:56Z, logical dump of 116 tables, previous completed dump 806.8 MB
in the catalog: the first sample already sat at level schema (table 31/116), the percentage
advanced table by table (2.0 → 26.0 % in 3 min 15 s), rate steady at 10 MB/s, ETA converging
from a wild first estimate (35396 s on one table) to 524 s. Bytes written passed the previous
size (1.9 GB against 806.8 MB, the previous dump was gzip-compressed, this one is not): the
schema level owned the percentage, as designed, and the bytes level alone would have shown
99 %. The heartbeat supervision logged nothing during the dump: the traffic marker fix holds.

Not done yet: physical level bytes/schema (the SST receiver's byte count, mariabackup's
"Copying" lines), mydumper boundaries, a modal instead of the tooltip.
