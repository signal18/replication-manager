# Binlog consumers: which calls stream a primary's binlog, when they overlap, and the replica server-id each presents (#1886)

A MariaDB/MySQL primary accepts ONE Binlog Dump thread per replica `server_id`: when a second
consumer connects with an id already in use, the server kills the first thread ("A slave with the
same server_id is already connected"). Every consumer below is such a replica from the primary's
point of view. Two of them overlapping in time with the same id means a kill, a reconnect, and --
for a consumer that restarts from the head of the file -- a full re-stream. That is what belair db1
did 781,703 times between 2025-11-28 and 2026-10-05 (50 Mb/s flat, 1.7 TB in five days).

## The pool (fix branch, Stéphane: "a helper that manages concurrency on this, from 10000 to 10010")

`cluster/cluster_binlog_serverid.go`: every consumer below LEASES its id from one pool per
cluster, `check-binlog-server-id .. +10` (10000..10010 by default), shifted by a per-instance
block (FNV-1a of the hostname, 180 blocks of 11: 10000..11979) so the active and the standby hold
distinct blocks. A lease lives as long as the stream and is released when it closes (the event
scanner releases in `CloseBinlogEventSyncer`, the bounded fetches with `defer`). An exhausted
pool refuses the lease and the caller skips its run; nothing ever steals an id in use. The
startup log line says "Replica server-id pool for this instance: N..N+10". The table's
"server-id presented" column is therefore history: today every row but 1 and 7 reads "leased
from the pool, purpose X".

`base` below is `check-binlog-server-id` (default 10000); the per-instance salt of 645ff0f8f
(FNV-1a(hostname) mod 1999, added to a fixed offset) was the first step and is replaced by the
pool.

## The consumers

| # | consumer | code | trigger | lifetime | server-id presented | runs on |
|---|---|---|---|---|---|---|
| 1 | real replicas | provisioned `server_id = server.Id[2:10]` (prov_opensvc_db.go) | always | permanent | 8 digits of the server id, distinct per node | the database nodes |
| 2 | security event scanner (go-mysql) | `ScanBinlogQueryEvents`, srv_binlog.go | every monitoring tick when `monitoring-binlog-events` is on, master only | persistent, one stream per primary per instance | pool lease `event-scanner` (was 12000 everywhere) | active only since the fix branch; before: every instance, standby included |
| 3 | binlog metadata (go-mysql) | `RefreshBinlogMetaGoMySQL` | a binlog file without known start timestamp: first discovery, rotation, purge trim (`binlog-parse-mode = gomysql`) | closes after the first event (FORMAT_DESCRIPTION) | pool lease `binlog-meta` (was 10000) | every instance that monitors the cluster |
| 4 | point-in-time position lookup (go-mysql) | `GetBinlogPositionFromTimestamp`, called by `ReadAndExecBinaryLogsWithinRange` (restore / flashback, srv_bck.go) | a restore or flashback with a time boundary | bounded by the boundary | pool lease `restore-lookup` | the instance running the restore |
| 5 | binlog metadata (mysqlbinlog) | `RefreshBinlogMetaMySQL` | same as 3 with `binlog-parse-mode = mysqlbinlog` | bounded (`--start-position/--stop-position`) | pool lease `binlog-meta` (was `--server-id=base`) | every instance |
| 6 | timestamp search (mysqlbinlog) | `FindLogPositionForTimestamp`, `FindNearestLogPosition` | restore / flashback | bounded (`--start-datetime/--stop-datetime`) | pool lease `restore-lookup` (was `--server-id=base`) | the instance running the restore |
| 7 | apply a binlog range (mysqlbinlog) | `ReadAndApplyBinaryLogsWithinRange` | restore / flashback apply | bounded | `--server-id=<the SOURCE server's own ServerID>` ("binlog owner", so the apply keeps the origin id) | the instance running the restore |
| 8 | binlog backup copy (mysqlbinlog) | `JobBackupBinlog`, srv_job_backup.go (`backup-binlogs`, method mysqlbinlog) | at every binlog rotation (`CheckBinaryLogs`) and at purge time, one rotated file per run | bounded (`--raw` of one file) | pool lease `binlog-backup` (was `--server-id=10000` hard-coded) | every instance where the job runs |
| 9 | crash rejoin fetch (mysqlbinlog) | `backupBinlog(crash)`, srv_rejoin.go | a rejoin after a crash failover, fetching the old master's last file | bounded (one file, `--raw`) | pool lease `rejoin-fetch` (was 10000 hard-coded) via `--stop-never-slave-server-id` / `--connection-server-id` | the instance performing the rejoin |

The DB jobs script (`dbjobs_new.sh`) and `binlog_copy.sh` open no remote binlog stream.

## What can overlap on the same primary

With the pool, two consumers of ONE instance can never present the same id: each holds its own
lease while it streams (eleven at once at most; the twelfth is refused and skips its run). Two
INSTANCES hold distinct blocks unless their hostnames hash to the same block (1 in 180, visible
in the startup line; set `check-binlog-server-id` differently on one of them). What remains:

| overlap | collision |
|---|---|
| real replicas (1) vs the pool | none in practice; a provisioned replica id inside 10000..11979 would collide, nothing checks it |
| apply of a binlog range (7) | presents the SOURCE server's own id on purpose; untouched |
| twelve concurrent consumers on one instance | the twelfth is refused, logged, retried on its next run |

## Open

* Reserve 10000..11979 away from provisioned replica ids, or check at provisioning time.
* Expose the leases (purpose, since) in the API/GUI next to the WARN0227 state.
* State WARN0227 (fix branch) pauses the scanner on repeated resets and names the id it presented;
  it cannot tell WHICH of the consumers above held it. The primary's error log can
  (`Start binlog_dump to slave_server(ID)` lines carry the id, not the host).
