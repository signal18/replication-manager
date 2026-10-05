# Binlog consumers: which calls stream a primary's binlog, when they overlap, and the replica server-id each presents (#1886)

A MariaDB/MySQL primary accepts ONE Binlog Dump thread per replica `server_id`: when a second
consumer connects with an id already in use, the server kills the first thread ("A slave with the
same server_id is already connected"). Every consumer below is such a replica from the primary's
point of view. Two of them overlapping in time with the same id means a kill, a reconnect, and --
for a consumer that restarts from the head of the file -- a full re-stream. That is what belair db1
did 781,703 times between 2025-11-28 and 2026-10-05 (50 Mb/s flat, 1.7 TB in five days).

`base` below is `check-binlog-server-id` (default 10000). `salt` is the per-instance component
added since 645ff0f8f: FNV-1a(hostname) mod 1999, logged at startup
("Binlog syncer replica server-ids for this instance: metadata N, event scanner M").

## The consumers

| # | consumer | code | trigger | lifetime | server-id presented | runs on |
|---|---|---|---|---|---|---|
| 1 | real replicas | provisioned `server_id = server.Id[2:10]` (prov_opensvc_db.go) | always | permanent | 8 digits of the server id, distinct per node | the database nodes |
| 2 | security event scanner (go-mysql) | `ScanBinlogQueryEvents`, srv_binlog.go | every monitoring tick when `monitoring-binlog-events` is on, master only | persistent, one stream per primary per instance | base + 2000 + salt (was 12000 everywhere) | active only since the fix branch; before: every instance, standby included |
| 3 | binlog metadata (go-mysql) | `RefreshBinlogMetaGoMySQL` | a binlog file without known start timestamp: first discovery, rotation, purge trim (`binlog-parse-mode = gomysql`) | closes after the first event (FORMAT_DESCRIPTION) | base + 0 + salt (was 10000) | every instance that monitors the cluster |
| 4 | point-in-time position lookup (go-mysql) | `GetBinlogPositionFromTimestamp`, called by `ReadAndExecBinaryLogsWithinRange` (restore / flashback, srv_bck.go) | a restore or flashback with a time boundary | bounded by the boundary | base + 0 + salt | the instance running the restore |
| 5 | binlog metadata (mysqlbinlog) | `RefreshBinlogMetaMySQL` | same as 3 with `binlog-parse-mode = mysqlbinlog` | bounded (`--start-position/--stop-position`) | `--server-id=base`, NO salt | every instance |
| 6 | timestamp search (mysqlbinlog) | `FindLogPositionForTimestamp`, `FindNearestLogPosition` | restore / flashback | bounded (`--start-datetime/--stop-datetime`) | `--server-id=base`, NO salt | the instance running the restore |
| 7 | apply a binlog range (mysqlbinlog) | `ReadAndApplyBinaryLogsWithinRange` | restore / flashback apply | bounded | `--server-id=<the SOURCE server's own ServerID>` ("binlog owner", so the apply keeps the origin id) | the instance running the restore |
| 8 | binlog backup copy (mysqlbinlog) | `JobBackupBinlog`, srv_job_backup.go (`backup-binlogs`, method mysqlbinlog) | at every binlog rotation (`CheckBinaryLogs`) and at purge time, one rotated file per run | bounded (`--raw` of one file) | `--server-id=10000` HARD-CODED, ignores base and salt | every instance where the job runs |
| 9 | crash rejoin fetch (mysqlbinlog) | `backupBinlog(crash)`, srv_rejoin.go | a rejoin after a crash failover, fetching the old master's last file | bounded (one file, `--raw`) | `--stop-never-slave-server-id=10000` (MariaDB / MySQL < 8.4) or `--connection-server-id=10000` HARD-CODED | the instance performing the rejoin |

The DB jobs script (`dbjobs_new.sh`) and `binlog_copy.sh` open no remote binlog stream.

## What can overlap on the same primary

| overlap | ids | collision today |
|---|---|---|
| scanner (2) with anything | base+2000+salt vs the rest | none: offset 2000 is reserved to the scanner and the salt stays below 1999 |
| the two go-mysql offset-0 users of ONE instance: metadata (3) while a restore lookup (4) runs | both base+0+salt | YES, same id: a new binlog file discovered during a restore kills the restore's stream. Rare, bounded, worth its own offset |
| metadata (3/5) on the active and on the standby | go-mysql: base+salt_a vs base+salt_b -- mysqlbinlog: base vs base | go-mysql: distinct since the salt. mysqlbinlog mode: IDENTICAL on both instances, kill on overlap (short runs, both react to the same rotation at the same second) |
| binlog backup (8) on two instances, or backup (8) while a rejoin fetch (9) runs | 10000 vs 10000 | YES, hard-coded on both sides; a rejoin after a crash failover is exactly when a rotation-triggered backup is likely |
| any salted go-mysql consumer vs the hard-coded 10000 | base+salt vs 10000 | only on an instance whose salt is 0 (1 chance in 1999) |
| real replicas (1) vs anything | 8-digit ids vs 10000..13999 | none in practice; a replica id in 10000..13999 would collide, nothing checks it |

Hostname salt collisions between two instances are possible (1 in 1999) and visible in the startup
log line; `check-binlog-server-id` set differently per instance removes the chance.

## Open

* One allocator for every consumer: base + purpose offset (0 metadata, 2000 scanner, 4000 restore
  lookups, 6000 binlog backup, 8000 rejoin fetch) + instance salt, passed to the mysqlbinlog flags
  (`--server-id`, `--connection-server-id`, `--stop-never-slave-server-id`) as well -- today only the
  go-mysql syncers are salted and the mysqlbinlog paths carry 10000 or `base`.
* Reserve 10000..19999 away from provisioned replica ids, or check at provisioning time.
* State WARN0227 (fix branch) pauses the scanner on repeated resets and names the id it presented;
  it cannot tell WHICH of the consumers above held it. The primary's error log can
  (`Start binlog_dump to slave_server(ID)` lines carry the id, not the host).
