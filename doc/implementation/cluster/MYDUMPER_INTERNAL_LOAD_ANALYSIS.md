# Loading a mydumper backup with repman's own restore (#2124)

Analysis only: no code changes. It answers four questions raised on #2124:

1. Can repman load a mydumper backup with its own restore (the splitdump loader) instead of
   myloader, and what does that imply?
2. Does it work, and is it tested, today?
3. What does myloader do, and is it very similar to our splitdump restore? (In splitdump,
   each fragment carries the SQL a parallel load needs: time zone, FK checks off.)
4. Why: reloading a parent backup on a staging cluster broke with a disk full of binlog. This
   branch pauses the restore until the replicas catch up and the binlog is purged, so the
   restore stays within the binlog purge policy.

Sources:
- repman: this branch, `fix/splitdump-child-seed-gtid-2124` at 85bd017ce. Line numbers
  refer to it.
- myloader / mydumper: version 0.17.1-1, the one the images install (`docker/Dockerfile:19`),
  read from its source code.

Nothing here was run: no myloader run, no lab run. Statements marked *(from reading)* come
from the code and still need a test.

## Answers

1. **Possible: yes.** A pause driven by the replicas can only live in repman's own loader.
   myloader is an external program, and nothing in it can wait for replicas or a binlog
   purge. The two loaders work the same way (table below), so feeding mydumper files to the
   splitdump loader is a reader and naming job, not a new loader. The work is listed under
   [What loading mydumper files natively needs](#what-loading-mydumper-files-natively-needs).
2. **Working and tested today: no.**
   - A mydumper backup is loaded only by myloader. That covers both the reseed and the
     staging / child seed from the parent (`cluster/cluster_staging.go:611`).
   - On a master, repman runs myloader with `--enable-binlog` (`cluster/srv_job_backup.go:3488`).
     The whole backup goes to the binlog, and the binlog gate on this branch does not cover
     this path.
   - No test loads a mydumper backup through the splitdump loader.
3. **myloader is very similar to our splitdump restore.** Both have per-table files, parallel
   connections, FK checks off, schema then data then post-data, batched commits and binlog off
   unless asked. The SQL each fragment carries is the same in both formats:
   - Every mydumper file starts with `SET NAMES binary`, `FOREIGN_KEY_CHECKS=0`, `SQL_MODE`
     and `TIME_ZONE='+00:00'`.
   - myloader replays those header lines on each extra connection it opens for the same file.
   - The differences that matter are who controls the load, parallel loading inside one table,
     and the file names (tables below).
4. **The staging disk-full case is still open for a mydumper parent backup.** On this branch,
   the gate pauses a MariaDB restore with the binary log on, in two paths: the splitdump
   loader and the single-file mysqldump stream. A staging or child cluster seeded from a
   mydumper backup still writes the whole backup to its master's binlog with no pause.

## Restore paths on this branch

| Seed or reseed from | Loader | Binlog on the master | Binlog gate (#2124) | Tested |
|---|---|---|---|---|
| Single-file mysqldump | `mysql` client fed by repman (`JobReseedMysqldump`, `cluster/srv_job_backup.go:3547`) | On | Yes, MariaDB only. Pauses on the stream that feeds the client (`logicalRestoreGateReader`, `:3600`) | `TestLogicalRestoreGateHoldsStream`; regtests `testCreateStaging`, `testCreateChild` |
| Splitdump | repman's loader (`restoreSplitdumpWithMysql`, `:2852`) | On | Yes, MariaDB only. Pauses before single statements (`execSplitdumpSingle`, `:2322`); see [Gate points](#gate-points-to-check) | `TestLogicalRestoreGateWaitsReplicaThenPurges`; regtests `testCreateStagingSplitdump`, `testCreateChildSplitdump` (nothing in them forces a binlog rotation) |
| mydumper | myloader (`jobReseedMyLoader`, `:3432`) | On (`--enable-binlog`, `:3488`) | **No** | No |

The gate (`cluster/logical_restore_gate.go`):
- It starts when a MariaDB restore runs with `sql_log_bin=1` (`:2922`, `:3600`).
- The binlog monitoring calls it on each rotation of the master's binlog
  (`cluster/srv_binlog.go:369`).
- It waits, with no timer, until every replica's `Master_Log_File` is the current binlog and
  binlog archiving is idle.
- It then purges the master's binlogs before the current one, and each replica's own, and
  resumes. WARN0254 is raised while it is paused.

## myloader 0.17.1-1 compared with the splitdump restore

How each loader runs when repman uses it, with the default options. The last column says
whether the two are the same, close, or different.

### Who runs it and how

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| What runs it | External program started with `exec.Command` and waited on (`cluster/srv_job_backup.go:3506`) | Go code inside repman (`restoreSplitdumpWithMysql`, `:2852`) | Different |
| Options repman passes | `--overwrite-tables --verbose=3 --innodb-optimize-keys=skip --max-threads-for-schema-creation=1 --max-threads-for-index-creation=1` (`server/server.go:1082`), plus `--threads`, `--directory` and credentials. All are valid in 0.17.1-1 | Settings in `config.Config`: `backup-logical-load-threads`, `backup-splitdump-create-databases`, `backup-restore-definer-strict` | n/a |
| Where repman uses it | Reseed of a mydumper backup; staging / child seed from a mydumper parent (`cluster/cluster_staging.go:611`) | Reseed of a splitdump backup (`reseedMysqldumpWithSplitdump`, `:1713`); staging / child seed from a splitdump parent; backup verification | n/a |

### Input files

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| How it sorts files | By file-name suffix | By file-name suffix (`classifyFile`, `utils/splitdump/restore.go:406`), or from the `manifest` if present (`:738`) | Same idea |
| Table schema | `db.table-schema.sql` | `db.table-schema.sql.gz` | Same |
| Data | `db.table.NNNNN.sql`, chunks sized by mydumper | `db.table.NNNNN.sql.gz`, a new shard every `backup-splitdump-file-size` (default 1G, `server/server.go:1094`) | Same naming |
| Database creation | `db-schema-create.sql`; myloader does not support a restore without it | No such file; `CREATE DATABASE IF NOT EXISTS` per schema (`backup-splitdump-create-databases`, `cluster/srv_job_backup.go:2985`) | Different |
| Views | `-schema-view.sql` | `-schema-view.sql.gz` | Same |
| Triggers | `-schema-triggers.sql` (plural) | `-schema-trigger.sql.gz` (singular) | Different |
| Routines and events | `db-schema-post.sql` | `db.__routines-schema-routine`, `db.__events-schema-event` | Different |
| System tables and accounts | Ordinary table files. On a fresh node or a parent seed, repman excludes `mysql` and `sys` with `--regex` (`:3501`) and renames account files to `.skip` (`:3455`) | A separate `mysql.system-all` file, replayed through its own path (`restoreSystemCatalog`, `:1833`); left out when users are not restored (`utils/splitdump/restore.go:428`) | Different |
| `metadata` file | Ini file with a `[source]` section, plus per-table sections (checksums, `is_view`) | `[source]` with `File`, `Position`, `Executed_Gtid_Set` only (`utils/splitdump/split.go:653`) | Close |

### Load order

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Order | Schema (databases, tables) → data → indexes → post-load: per-table constraints and triggers, then routines and events, then views | Schema (tables ordered by foreign keys, `mysql.system-all`, routines, views) → data → triggers and events (`utils/splitdump/restore.go:557-598`, `utils/splitdump/fkorder.go:40`) | Close |
| Views and routines | Last, after the data | With the schema, before the data | Different |
| Table order for data | Largest tables first, unless `--skip-table-sorting` | Order on disk, one table per worker | Different |

### Parallelism and commits

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Threads | Own default 4. Repman passes `--threads=backup-logical-load-threads`, default 2 (`cluster/srv_job_backup.go:3447`, `server/server.go:960`) | `backup-logical-load-threads`, default 2 (`cluster/srv_job_backup.go:2935`) | Same budget |
| One table over several threads | Yes: `--max-threads-per-table`, capped at `--threads`; it can also open extra connections on the same file | No: one worker per table, its shards in sequence (`groupSplitdumpDataFiles`, `utils/splitdump/restore.go:508`) | Different |
| Statement splitting | A statement ends when the buffer ends with `;\n` | A statement ends at a line ending with the delimiter (`forEachSplitdumpStatement`, `cluster/srv_job_backup.go:2051`) | Same approach |
| Rows per statement | mydumper writes one row per line inside an INSERT; `--rows` can re-split | Executes the mysqldump INSERT as written, one line per statement | Close |
| INSERT size | mydumper `--statement-size`, default 1,000,000 bytes | mariadb-dump `net-buffer-length` 1046528 (11.8 client); MySQL mysqldump not checked | Same, about 1 MB |
| Commit | Every 1000 statements (`--queries-per-transaction`) and at the end of each file | Every 500 INSERT/REPLACE statements (`splitdumpBatchStatements`, `:1759`); other statements autocommit | Close |
| Largest transaction | About 1000 × 1 MB | About 500 × 1 MB | Both large |

### Session settings

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Per connection | `FOREIGN_KEY_CHECKS=0`, `SQL_LOG_BIN=0` unless `--enable-binlog`, `AUTOCOMMIT=0`, `WAIT_TIMEOUT` and `NET_WRITE_TIMEOUT` 2147483, `SET NAMES binary` | `long_query_time=10`, `FOREIGN_KEY_CHECKS=0`, `UNIQUE_CHECKS=0` (`prepareRestoreConn`, `:1800`); binlog off for a replica, on for a master (`:2963`, `:2967`) | Close |
| Header inside the files | Every file: `SET NAMES binary`, `FOREIGN_KEY_CHECKS=0`, `SQL_MODE`, `TIME_ZONE='+00:00'`; replayed on each extra connection for the file | Data files only: the dump's `/*!40` lines, including `TIME_ZONE='+00:00'`, `SQL_MODE='NO_AUTO_VALUE_ON_ZERO'`, FK and UNIQUE off (`utils/splitdump/split.go:438`, written at `:408` and `:506`) | Same |
| `UNIQUE_CHECKS` | Not turned off | Off | Different |
| End-of-dump statements | None | Skipped: `SET … = @OLD_*`, `START SLAVE` (`dumpFooterStatements`, `cluster/srv_job_backup.go:2017`) | n/a |

### Binlog, indexes, constraints

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Binlog | Off unless `--enable-binlog`, which repman passes on the master (`:3488`) | Off for a replica reseed, after one binlog reset; on for a master restore (`buildLogicalRestorePreamble`, `:2823`) | Same |
| Binlog gate (#2124) | None | MariaDB master restore: waits before single statements (`:2322`) | Different |
| Secondary indexes | Created with the table: `--innodb-optimize-keys=skip` turns deferral off | Created with the table | Same |
| Foreign keys | Kept inside `CREATE TABLE`; split out only with optimize-keys, `--skip-constraints` or `--skip-indexes` | Kept inside `CREATE TABLE`; tables created parents first (`orderTableSchemasByForeignKeys`, `utils/splitdump/fkorder.go:228`) | Close |

### Replication position

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Can set it itself | `--set-gtid-purged`; change source and start replica from keys in `metadata` | No | Different |
| What repman does | Reseed: `applyMyLoaderReplicaPosition` after the load (`cluster/srv_job_backup.go:3373`). Staging / child seed: MariaDB only, `gtid_slave_pos` = the monitor's `gtid_slave_pos` merged with the backup's (`cluster/cluster_staging.go:618-627`) | MariaDB: `setSeedGTIDSlavePos`, the backup's domains plus the live `gtid_current_pos` (`cluster/srv_job_backup.go:3045`, `cluster/cluster_staging.go:443`). MySQL: `GTID_PURGED` (`:3052`) | Close |

### Control, errors, progress

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Pause | SIGINT: each thread parks before its next file and the program waits for Y/N on stdin; "N" resumes. Repman starts it with no stdin, so a SIGINT from repman would leave it paused *(from reading)* | MariaDB master restore: the #2124 gate; otherwise none | Different |
| Stop and resume | SIGTERM, or SIGINT then "Y": current files finish, a `resume` file lists the rest; `--resume` continues | Context cancel stops it; no resume | Different |
| Throttle | None in 0.17.1-1 (newer versions add `--throttle` on a target status variable such as `Threads_running`) | None | Same |
| Statement error | Retries once, reconnecting if needed, then a critical error stops the run; `--ignore-errors` downgrades listed codes. Repman keeps the last 3 CRITICAL lines (`cluster/srv_fresh.go:129`) | Retries the batch on lock contention, up to 8 times with backoff (`cluster/srv_job_backup.go:1758`, `execSplitdumpBatch` `:2190`); other errors stop the restore | Different |
| DEFINER | `--skip-definer` (off by default) | Retries without DEFINER unless `backup-restore-definer-strict` | Close |
| Checksum after load | `--checksum`, default `fail`, against checksums in `metadata` | None | Different |
| Progress | stderr lines, logged at DBG | Bytes loaded out of the total, shown during the reseed (WARN0189, `:2906`) | Different |

## What loading mydumper files natively needs

- **A mydumper file set for the loader:**
  - mydumper names: `-schema-create`, `-schema-triggers`, `-schema-post`, `-schema-view`,
    `-schema-sequence`, uncompressed or compressed;
  - myloader's order: views after the data;
  - the `metadata` file for the position (`JobMyLoaderParseMeta` already parses it).
- **Session settings matching myloader's:** the file header already carries NAMES, FK,
  SQL_MODE and TIME_ZONE. `SET NAMES binary` and `AUTOCOMMIT` behaviour have to be matched.
- **LOAD DATA:** backups taken with mydumper's LOAD DATA format (`.dat` files with
  `LOAD DATA` statements) need a path the loader does not have. Repman's default
  `backup-mydumper-options` do not use that format.
- **Optional, parallel chunks of one table:** without it, a large table loads on one worker,
  slower than myloader.
- **The same GTID positioning as the splitdump seed** (`setSeedGTIDSlavePos`). The mydumper
  seed today merges with `gtid_slave_pos`, not `gtid_current_pos`
  (`cluster/cluster_staging.go:618-627`).

## Implications

- **Reload time:** a restore with the gate lasts as long as the slowest replica takes to read
  the binlog. A replica that does not replicate keeps it paused, with no timer (WARN0254).
- **Commit size:** each commit is hundreds of MB in both loaders (500 or 1000 statements of
  about 1 MB). The binlog grows in steps of that size, and a replica applies each one whole.
- **Retention:** while the gate runs, the master's binlog retention gives way. Every binlog
  before the current one is purged, including binlogs written before the restore, so
  point-in-time recovery over that span is lost (F12; F7 for tracking the lost continuity).

## Gate points to check

All from reading, not run:

- **Splitdump INSERT batches do not wait on the gate.** `execSplitdumpBatch` (`:2190`) has no
  `waitLogicalRestoreGate`; only `execSplitdumpSingle` (`:2322`) does.
  - A data file starts with its header `/*!40…` statements, which run as single statements.
    So a worker passes the gate once per data file, then loads that file's INSERTs.
  - After a rotation, each worker can still load up to one shard (`backup-splitdump-file-size`,
    default 1G) before it stops.
  - The commit message of 1ba428acf says "before each statement of the splitdump loader".
- **The gate waits on what the replicas read, not what they applied.**
  - The measure is `SlaveStatus.MasterLogFile` (`cluster/cluster_topo.go:844`): the I/O
    thread's file, `Master_Log_File`.
  - A replica whose SQL thread lags keeps the backlog in its relay logs, on its own disk.
  - WARN0254 says "waits for the replicas to apply it".
- **MariaDB only:** `startLogicalRestoreGate` returns without a gate on MySQL and Percona.
- **Rotations are seen once per monitor tick:** `CheckBinaryLogs` runs from the refresh
  (`cluster/srv.go:1525`). The restore runs on between two ticks.
- **No test forces a rotation during a seed:** the regtests seed from a splitdump but do not
  force one.
- **Expected result of a splitdump backup restored by myloader** *(from reading, a test to
  run)*: tables and data may load. Triggers, routines and events probably do not (other file
  names), and there is no `-schema-create` file.
