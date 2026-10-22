# Binlog budget (#2142)

`force-binlog-purge` purges the primary's binlogs past a total size: `max_binlog_total_size` on
MariaDB 11.4+ (`SetMaxBinlogTotalSize`), the master purge job (`JobBinlogPurgeMaster`) before.
That size is the **binlog budget** (`ServerMonitor.BinlogBudget`, `cluster/srv_binlog_budget.go`).

## The budget
`force-binlog-purge-total-size` > 0 (GB) is the budget. 0, the default since 3.1.45 (was 30), makes
it follow the disk, first known wins:

| Known | Budget |
|---|---|
| the plan (`prov-db-disk-size`, OpenSVC / Kubernetes) and the database size | (plan disk - data) / 2 |
| the datadir's free space | binlogs now + free / 2 |
| nothing | 30 GB |

- **Database size**: data + index length of the schema monitor's dictionary (`DictTables`).
- **Free space**: the jobs script's DBU report (`diskFreeBytes`, `df -P -B1 $DATADIR`, used when
  younger than 15 min: on premise only while SSH runs the script), else `information_schema.DISKS`
  (the longest disk path holding `DATADIR`, KB).
- Half, not all: the other half is room for the data, temporary files and a restore. A fixed
  default could not fit both a 5G test disk (30G filled dev3's, InnoDB error 28) and a 2T one.
- **Floor**: two binlog files (2 x `max_binlog_size`); below, every closed binlog is purged at once
  with its copy and replicas. WARN0264 while at the floor.

Recomputed each time it is applied, so it shrinks as the data grows and comes back after a resize.
Under the dynamic config the plan owns `max_binlog_total_size` (`prov-db-binlog-total-size-pct` of
`prov-db-disk-size`, `binlogTotalSizeTarget`), and repman now sets it live on every MariaDB 11.4+
server, `force-binlog-purge` on or not: before, it was only written in the rendered config, and a
running server kept no limit (dev3 2026-10-22: 0 on the primary, 30G on a replica, a 9G plan).

Live check (dev3 2026-10-22, dynamic config off, plan 9G): a rotation set db2 to 4G
((9G - data) / 2, whole binlogs) and MariaDB purged the three oldest binlogs.

## Lagging replicas
- Before 11.4 the purge job stops at the replicas' oldest master binlog (WARN0107): nothing lost.
- 11.4+: the server purges by size; a binlog a connected replica's dump thread reads is never
  purged, so a connected replica, even lagging, is safe. A disconnected one (IO thread not running)
  needing a binlog older than the oldest the budget keeps (`oldestBinlogInBudget`: the newest
  binlogs adding up to the budget) gets WARN0265: space wins, it needs a reseed unless it
  reconnects first.
