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
- repman: this branch, `fix/splitdump-child-seed-gtid-2124`, code at b7b946211. Line numbers
  refer to it.
- myloader / mydumper: version 0.17.1-1, the one the images install (`docker/Dockerfile:19`),
  read from its source code.
- The test run in `MYDUMPER_SPLITDUMP_LOAD_TEST_REPORT.md`: a mydumper backup loaded with both
  splitdump loaders and with myloader, on MariaDB 11.4.13 and 10.11.19, in local Docker.
  Results from that run are marked *(tested)*. Points still read from the code only are marked
  *(from reading)*.

## Answers

1. **Possible: yes, with work on the loader.** A pause driven by the replicas can only live in
   repman's own loader: myloader is an external program, and nothing in it can wait for
   replicas or a binlog purge. The two loaders work the same way (tables below).
   *(tested)* mydumper's data files already load identically through repman's Go loader:
   multi-row INSERTs, raw binary values, chunked tables, the per-file session header. What
   fails is listed under
   [What loading mydumper files natively needs](#what-loading-mydumper-files-natively-needs).
2. **Working today: no.** *(tested)* On MariaDB 11.4 and 10.11:
   - **CLI (`splitrestore`):** loads the data but loses triggers, routines and events, with
     exit 0.
   - **Go loader:** stops the whole restore on those files, before any data.
   - **Position:** neither reads the backup's position.
   - **Production path:** a mydumper backup is still loaded only by myloader, both for a reseed
     and for a staging / child seed from the parent (`cluster/cluster_staging.go:611`). On a
     master, repman runs myloader with `--enable-binlog` (`cluster/srv_job_backup.go:3490`): the
     whole backup goes to the binlog, and the binlog gate of this branch does not cover this path.
3. **myloader is very similar to our splitdump restore.** Both have per-table files, parallel
   connections, FK checks off, schema then data then post-data, batched commits and binlog off
   unless asked. The SQL each fragment carries is the same in both formats:
   - every mydumper file starts with `SET NAMES binary`, `FOREIGN_KEY_CHECKS=0`, `SQL_MODE` and
     `TIME_ZONE='+00:00'`;
   - myloader replays those header lines on each extra connection it opens for the same file.

   The differences that matter are who controls the load, parallel loading inside one table,
   the file names, and how trigger and routine bodies are written (tables below).
4. **The staging disk-full case is still open for a mydumper parent backup.** On this branch,
   the gate pauses a MariaDB restore with the binary log on, in two paths: the splitdump loader
   (every batch and single statement since b7b946211) and the single-file mysqldump stream. A
   staging or child cluster seeded from a mydumper backup still writes the whole backup to its
   master's binlog with no pause.

## Restore paths on this branch

| Seed or reseed from | Loader | Binlog on the master | Binlog gate (#2124) | Tested |
|---|---|---|---|---|
| Single-file mysqldump | `mysql` client fed by repman (`JobReseedMysqldump`, `cluster/srv_job_backup.go:3549`) | On | Yes, MariaDB only. Pauses on the stream that feeds the client (`logicalRestoreGateReader`, `:3602`) | `TestLogicalRestoreGateHoldsStream`; regtests `testCreateStaging`, `testCreateChild` |
| Splitdump | repman's loader (`restoreSplitdumpWithMysql`, `:2854`) | On | Yes, MariaDB only. Pauses before every batch and every single statement (`streamSplitdumpStatements`, `:2175`, waits at `:2180` and `:2184`) | `TestLogicalRestoreGateHoldsSplitdumpBatches`, `TestLogicalRestoreGateWaitsReplicaThenPurges`; regtests `testCreateStagingSplitdump`, `testCreateChildSplitdump` (nothing in them forces a binlog rotation) |
| mydumper | myloader (`jobReseedMyLoader`, `:3434`) | On (`--enable-binlog`, `:3490`) | **No** | No |

The gate (`cluster/logical_restore_gate.go`):
- It starts when a MariaDB restore runs with `sql_log_bin=1` (`:2924`, `:3602`).
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
| What runs it | External program started with `exec.Command` and waited on (`cluster/srv_job_backup.go:3508`) | Go code inside repman (`restoreSplitdumpWithMysql`, `:2854`) | Different |
| Options repman passes | `--overwrite-tables --verbose=3 --innodb-optimize-keys=skip --max-threads-for-schema-creation=1 --max-threads-for-index-creation=1` (`server/server.go:1082`), plus `--threads`, `--directory` and credentials. All are valid in 0.17.1-1 | Settings in `config.Config`: `backup-logical-load-threads`, `backup-splitdump-create-databases`, `backup-restore-definer-strict` | n/a |
| Where repman uses it | Reseed of a mydumper backup; staging / child seed from a mydumper parent (`cluster/cluster_staging.go:611`) | Reseed of a splitdump backup (`reseedMysqldumpWithSplitdump`, `:1713`); staging / child seed from a splitdump parent; backup verification | n/a |

### Input files

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| How it sorts files | By file-name suffix | By file-name suffix (`classifyFile`, `utils/splitdump/restore.go:406`), or from the `manifest` if present (`:738`); an unknown suffix is data | Same idea |
| Table schema | `db.table-schema.sql` | `db.table-schema.sql.gz` | Same |
| Data | `db.table.NNNNN.sql`, chunks sized by mydumper | `db.table.NNNNN.sql.gz`, a new shard every `backup-splitdump-file-size` (default 1G, `server/server.go:1094`) | Same naming |
| Database creation | `db-schema-create.sql`; myloader does not support a restore without it | No such file; `CREATE DATABASE IF NOT EXISTS` per schema (`backup-splitdump-create-databases`, `cluster/srv_job_backup.go:2987`) | Different |
| Views | `-schema-view.sql`, plus a placeholder `-schema.sql` (MEMORY table) | `-schema-view.sql.gz` | Same |
| Triggers | `-schema-triggers.sql` (plural) | `-schema-trigger.sql.gz` (singular) | Different |
| Routines and events | `db-schema-post.sql` | `db.__routines-schema-routine`, `db.__events-schema-event` | Different |
| Trigger and routine bodies | No `DELIMITER`: statements inside a body end in `"; "`, the last `END;` at the line break | `DELIMITER ;;` fences, as mysqldump writes them | Different |
| System tables and accounts | Ordinary table files. On a fresh node or a parent seed, repman excludes `mysql` and `sys` with `--regex` (`:3503`) and renames account files to `.skip` (`:3457`) | A separate `mysql.system-all` file, replayed through its own path (`restoreSystemCatalog`, `:1833`); left out when users are not restored (`utils/splitdump/restore.go:428`) | Different |
| `metadata` file | Ini file: `[source]` with `executed_gtid_set`, `SOURCE_LOG_FILE`, `SOURCE_LOG_POS`, plus `[config]`, `[myloader_session_variables]` and per-table sections | `[source]` with `File`, `Position`, `Executed_Gtid_Set` (`utils/splitdump/split.go:653`). *(tested)* mydumper's file is rejected ("missing binlog file") | Different |

### Load order

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Order | Schema (databases, tables) → data → indexes → post-load: per-table constraints and triggers, then routines and events, then views | Schema (tables ordered by foreign keys, `mysql.system-all`, routines, views) → data → triggers and events (`utils/splitdump/restore.go:557-598`, `utils/splitdump/fkorder.go:40`) | Close |
| Views and routines | Last, after the data | With the schema, before the data | Different |
| Table order for data | Largest tables first, unless `--skip-table-sorting` | File-name order, one table per worker (`sortSplitdumpDataFiles`). *(tested)* With mydumper's names, `db-schema-post` and `db-schema-triggers` sort before every data file | Different |

### Parallelism and commits

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Threads | Own default 4. Repman passes `--threads=backup-logical-load-threads`, default 2 (`cluster/srv_job_backup.go:3449`, `server/server.go:960`) | `backup-logical-load-threads`, default 2 (`cluster/srv_job_backup.go:2937`) | Same budget |
| One table over several threads | Yes: `--max-threads-per-table`, capped at `--threads`; it can also open extra connections on the same file | No: one worker per table, its shards in sequence (`groupSplitdumpDataFiles`, `utils/splitdump/restore.go:508`) | Different |
| Statement splitting | A statement ends when the buffer ends with `;\n` | A statement ends at a line that ends with the delimiter once trimmed (`forEachSplitdumpStatement`, `cluster/srv_job_backup.go:2051`). *(tested)* Correct for mydumper's INSERTs; it splits mydumper's trigger and routine bodies at their first `;` | Different for bodies |
| Rows per statement | mydumper writes one row per line inside an INSERT; `--rows` can re-split | Executes each INSERT as written | Close |
| INSERT size | mydumper `--statement-size`, default 1,000,000 bytes. *(tested)* 1.87 MB statements on this data | mariadb-dump `net-buffer-length` 1046528 (11.8 client); MySQL mysqldump not checked | Same, about 1–2 MB |
| Commit | Every 1000 statements (`--queries-per-transaction`) and at the end of each file | Every 500 INSERT/REPLACE statements (`splitdumpBatchStatements`, `:1759`), or at the next non-INSERT statement or the end of the file; other statements autocommit | Close |
| Largest transaction *(tested)* | 39.5 MB (11.4), 40.9 MB (10.11) | 56.8 MB: a whole mydumper data file (32 INSERTs) in one commit, with `max-binlog-size=16M` | Both large |

### Session settings

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Per connection | `FOREIGN_KEY_CHECKS=0`, `SQL_LOG_BIN=0` unless `--enable-binlog`, `AUTOCOMMIT=0`, `WAIT_TIMEOUT` and `NET_WRITE_TIMEOUT` 2147483, `SET NAMES binary` | `long_query_time=10`, `FOREIGN_KEY_CHECKS=0`, `UNIQUE_CHECKS=0` (`prepareRestoreConn`, `:1800`); binlog off for a replica, on for a master (`:2965`, `:2969`) | Close |
| Header inside the files | Every file: `SET NAMES binary`, `FOREIGN_KEY_CHECKS=0`, `SQL_MODE`, `TIME_ZONE='+00:00'`; replayed on each extra connection for the file | Data files only: the dump's `/*!40` lines, including `TIME_ZONE='+00:00'`, `SQL_MODE='NO_AUTO_VALUE_ON_ZERO'`, FK and UNIQUE off (`utils/splitdump/split.go:438`, written at `:408` and `:506`). *(tested)* mydumper's header runs correctly through the Go loader | Same |
| `UNIQUE_CHECKS` | Not turned off | Off | Different |
| End-of-dump statements | None | Skipped: `SET … = @OLD_*`, `START SLAVE` (`dumpFooterStatements`, `cluster/srv_job_backup.go:2017`) | n/a |

### Binlog, indexes, constraints

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Binlog | Off unless `--enable-binlog`, which repman passes on the master (`:3490`) | Off for a replica reseed, after one binlog reset; on for a master restore (`buildLogicalRestorePreamble`, `:2825`) | Same |
| Binlog gate (#2124) | None | MariaDB master restore: waits before every batch and single statement (`:2180`, `:2184`) | Different |
| Secondary indexes | Created with the table: `--innodb-optimize-keys=skip` turns deferral off | Created with the table | Same |
| Foreign keys | Kept inside `CREATE TABLE`; split out only with optimize-keys, `--skip-constraints` or `--skip-indexes` | Kept inside `CREATE TABLE`; tables created parents first (`orderTableSchemasByForeignKeys`, `utils/splitdump/fkorder.go:228`) | Close |

### Replication position

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Can set it itself | `--set-gtid-purged`; change source and start replica from keys in `metadata`. *(tested)* On MariaDB, its `CHANGE MASTER TO SOURCE_LOG_FILE …` fails with 1064, is logged, and myloader still exits 0 | No | Different |
| What repman does | Reseed: `applyMyLoaderReplicaPosition` after the load (`cluster/srv_job_backup.go:3375`). Staging / child seed: MariaDB only, `gtid_slave_pos` = the monitor's `gtid_slave_pos` merged with the backup's (`cluster/cluster_staging.go:618-627`) | MariaDB: `setSeedGTIDSlavePos`, the backup's domains plus the live `gtid_current_pos` (`cluster/srv_job_backup.go:3047`, `cluster/cluster_staging.go:443`). MySQL: `GTID_PURGED` (`:3054`). *(tested)* Nothing applied from a mydumper `metadata` (rejected) | Close |

### Control, errors, progress

| Aspect | myloader | splitdump restore | |
|---|---|---|---|
| Pause | SIGINT: each thread parks before its next file and the program waits for Y/N on stdin; "N" resumes. Repman starts it with no stdin, so a SIGINT from repman would leave it paused *(from reading)* | MariaDB master restore: the #2124 gate, between two transactions; otherwise none | Different |
| Stop and resume | SIGTERM, or SIGINT then "Y": current files finish, a `resume` file lists the rest; `--resume` continues | Context cancel stops it; no resume | Different |
| Throttle | None in 0.17.1-1 (newer versions add `--throttle` on a target status variable such as `Threads_running`) | None | Same |
| Statement error | Retries once, reconnecting if needed, then a critical error stops the run; `--ignore-errors` downgrades listed codes. Repman keeps the last 3 CRITICAL lines (`cluster/srv_fresh.go:129`) | Retries the batch on lock contention, up to 8 times with backoff (`cluster/srv_job_backup.go:1758`, `execSplitdumpBatch` `:2194`); any other error stops every worker. *(tested)* One trigger file stopped the whole restore. The CLI runs each file through `mysql --force` and exits 0 on SQL errors | Different |
| DEFINER | `--skip-definer` (off by default) | Retries without DEFINER unless `backup-restore-definer-strict` | Close |
| Checksum after load | `--checksum`, default `fail`, against checksums in `metadata` | None | Different |
| Progress | stderr lines, logged at DBG | Bytes loaded out of the total, shown during the reseed (WARN0189, `:2908`) | Different |

## Test results

From `MYDUMPER_SPLITDUMP_LOAD_TEST_REPORT.md`. Setup:
- the backup was taken with repman's mydumper arguments;
- the loads used a copy without the `mysql` and `sys` schemas, as repman does for a seed;
- the results are the same on MariaDB 11.4.13 and 10.11.19.

| Load | Table data | Trigger, routines, event | Position | Largest commit |
|---|---|---|---|---|
| `splitrestore` CLI | Identical | Lost, exit 0 | Not read | 1.87 MB |
| Go loader, seed copy as is | 0 rows: stops with `Error 1064` after the schema phase | Not created | Not read | n/a |
| Go loader, without the trigger and routine files | Identical | Not loaded | Not read | 56.8 MB |
| myloader (control) | Identical | All present | Its own `CHANGE MASTER` fails on MariaDB, exit 0 | 39.5–40.9 MB |

Before any load, mydumper 0.17.1-1 crashed (exit 139) on MariaDB 11.4.13 with tables in the
server's default collation `utf8mb4_uca1400_ai_ci`, even with mydumper's own defaults.

- **Cause:** its character-set lookup reads `information_schema.COLLATIONS`, which on 11.4 lists
  that collation only as `uca1400_ai_ci`, and it reads the result without checking for a row.
- **Workaround for the test:** with `utf8mb4_general_ci` the dump ran.
- **10.11.19:** the original data dumped fine; its default is `utf8mb4_general_ci`.

## What loading mydumper files natively needs

From the code and the test run:

1. **Trigger, routine and event bodies:** a splitter for mydumper's object files that ends a
   statement at `;` followed by a line break, not at a trimmed line ending in `;`.
2. **Load order:**
   - `-schema-triggers` and `-schema-post` in the post-data phase, after all data, so triggers
     do not exist while the data loads;
   - `-schema-create` before the tables, or the databases created first
     (`backup-splitdump-create-databases`).
3. **Position:** reading mydumper's `[source]` keys (`executed_gtid_set`, `SOURCE_LOG_FILE`,
   `SOURCE_LOG_POS`); `JobMyLoaderParseMeta` already parses the file. Then the same positioning
   as the splitdump seed (`setSeedGTIDSlavePos`). The mydumper seed today merges with
   `gtid_slave_pos`, not `gtid_current_pos` (`cluster/cluster_staging.go:618-627`).
4. **Commit size:** a limit per transaction in bytes. One mydumper data file is one commit
   today: 56.8 MB in the test, and up to about 1 GB with repman's `--chunk-filesize=1000`. The
   #2124 gate can pause only between commits, so after a rotation each worker can still write
   one whole data file.
5. **System schemas:** leaving out `mysql` and `sys` for a seed, as repman does for myloader.
6. **Session settings:** the file header already carries NAMES, FK, SQL_MODE and TIME_ZONE.
   `AUTOCOMMIT` behaviour has to match myloader's.
7. **LOAD DATA:** backups taken with mydumper's LOAD DATA format (`.dat` files with `LOAD DATA`
   statements) need a path the loader does not have. Repman's default `backup-mydumper-options`
   do not use that format.
8. **Optional, parallel chunks of one table:** without it, a large table loads on one worker,
   slower than myloader.

On the backup side, mydumper 0.17.1-1 cannot dump MariaDB 11.4+ tables in the default
`utf8mb4_uca1400_ai_ci` collation (test results above), whichever loader is used.

## Implications

- **Reload time:** a restore with the gate lasts as long as the slowest replica takes to read
  the binlog. A replica that does not replicate keeps it paused, with no timer (WARN0254).
- **Commit size:** each commit is tens to hundreds of MB in both loaders. The binlog grows in
  steps of that size, and a replica applies each one whole.
- **Retention:** while the gate runs, the master's binlog retention gives way. Every binlog
  before the current one is purged, including binlogs written before the restore, so
  point-in-time recovery over that span is lost (F12; F7 for tracking the lost continuity).

## Gate points to check

- **Fixed in b7b946211:** splitdump INSERT batches now wait on the gate too. Before it, only
  single statements did, and on preprod (belair-staging-test) the batches went on while
  WARN0254 was open and db3 filled its disk.

Still open, from reading, not run:

- **The gate waits on what the replicas read, not what they applied.**
  - The measure is `SlaveStatus.MasterLogFile` (`cluster/cluster_topo.go:844`): the I/O
    thread's file, `Master_Log_File`.
  - A replica whose SQL thread lags keeps the backlog in its relay logs, on its own disk.
  - WARN0254 says "waits for the replicas to apply it".
- **MariaDB only:** `startLogicalRestoreGate` returns without a gate on MySQL and Percona.
- **Rotations are seen once per monitor tick:** `CheckBinaryLogs` runs from the refresh
  (`cluster/srv.go:1525`). The restore runs on between two ticks.
- **No test forces a rotation during a seed:** the regtests seed from a splitdump but do not
  force one, and no monitor ran in the Go loader test of the report.
