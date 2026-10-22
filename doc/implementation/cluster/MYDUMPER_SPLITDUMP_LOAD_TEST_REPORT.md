# Test report: a mydumper backup loaded with repman's splitdump loader (#2124)

Follow-up to `MYDUMPER_INTERNAL_LOAD_ANALYSIS.md`. A mydumper backup taken with repman's own
mydumper arguments was loaded three ways:

- **A:** with the `splitrestore` CLI;
- **B:** with repman's Go loader (`restoreSplitdumpWithMysql`);
- **C:** with myloader, as a control.

## Result

**Repman's splitdump loader cannot restore a mydumper backup today.** The table data loads
identically, but:

- triggers, routines and events are lost. The CLI loses them silently (exit 0); the Go loader
  stops the whole restore on them, before any data;
- the backup's position is not read, so none is applied;
- the Go loader commits a whole data file as one transaction.

myloader restores the same backup completely.

The results are the same on MariaDB 11.4.13 and 10.11.19; see
[MariaDB 10.11 rerun](#mariadb-1011-rerun). On 11.4 only, mydumper 0.17.1-1 itself crashed
before the test: see
[mydumper 0.17.1-1 segfaults on MariaDB 11.4](#mydumper-0171-1-segfaults-on-mariadb-114).

## Environment

| Item | Value |
|---|---|
| Engine | MariaDB 11.4.13 (`mariadb:11.4` image) and MariaDB 10.11.19 (`mariadb:10.11`, the lab's version), local Docker, not the lab |
| Servers | One source and three targets, each a fresh container: `--log-bin`, `--binlog-format=ROW`, `--max-binlog-size=16M`, `--event-scheduler=ON` |
| mydumper / myloader | 0.17.1-1, the version `docker/Dockerfile:19` installs; bookworm `.deb`, built against MySQL 8.0.39 client |
| repman code | `fix/splitdump-child-seed-gtid-2124` at b7b946211; `replication-manager-cli` built from it (`go build --tags clients`) |
| Load threads | 2 (`backup-logical-load-threads` default) |

The sections below give the MariaDB 11.4 run in detail; the 10.11 rerun is compared with it
in [MariaDB 10.11 rerun](#mariadb-1011-rerun). MySQL and Percona were not run.

## Source data

Two databases, `mdt` and `mdt2`:

| Object | Content |
|---|---|
| `big` | 200,000 rows: VARBINARY from SHA2, TIMESTAMP inserted with `time_zone='+05:00'`, TEXT with utf8mb4 (emoji, ü), a newline, strings ending in `;` and containing `);`, quotes, backslashes, NULL. mydumper split it into two data files |
| `parent` / `child` | Foreign key `fk_child_parent`; 3 and 2000 rows |
| `audit` | 2000 rows written by trigger `trg_child_ai` (AFTER INSERT ON child). If the trigger fires during a restore, extra rows show up |
| `nopk` | 500 rows, no primary key |
| `gen` | 100 rows, a VIRTUAL generated column |
| `mdt2.small` | 2 rows |
| Other objects | View `v_child`, procedure `p_count`, function `f_double`, event `ev_noop` |

Checks, the same on every server:

- **Rows:** per table, `COUNT(*)` and `SUM(CRC32(CONCAT_WS('#', every column)))`, with TIMESTAMP
  as `UNIX_TIMESTAMP`, binary as `HEX`;
- **Objects:** counts from `information_schema`;
- **Position:** `gtid_binlog_pos`, `gtid_current_pos`, `gtid_slave_pos`, and the binary logs.

Source after the seed:

| Table | Rows | Hash |
|---|---|---|
| big | 200000 | 430537153026174 |
| parent | 3 | 3033579832 |
| child | 2000 | 4289072074136 |
| audit | 2000 | 4291020360962 |
| nopk | 500 | 1095736821015 |
| gen | 100 | 217537557689 |
| mdt2.small | 2 | 4910485523 |

Objects: 7 tables, 1 view, 1 trigger, 2 routines, 1 event, 1 foreign key.

Position: `gtid_binlog_pos` `0-1-20`, binlog `mysql-bin.000002:809`.

## Backup

Repman's arguments (`server/server.go:1083`, `cluster/srv_job_backup.go:4888-4919`, `cluster/cluster_tool.go:9`):

```
mydumper --chunk-filesize=1000 --compress --less-locking --verbose=3 --triggers --routines \
  --events --trx-consistency-only --kill-long-queries --source-data=2 --clear \
  --outputdir /bk/mydumper --threads 2 --host <source> --port 3306 --user root --password ...
```

- **Exit status:** 0.
- **Files:** `big` in two data files (`.00000`, `.00001`); each view as a placeholder
  `-schema.sql` (MEMORY table) plus `-schema-view.sql`; per database `-schema-create`,
  `-schema-triggers` and `-schema-post`.
- **System schemas:** `mysql` (43 files, including `global_priv` and the `mysql.user` view) and
  `sys` (207 files). Repman passes no `--regex`.
- **`metadata`:**

```
[source]
executed_gtid_set = "0-1-20"
SOURCE_LOG_FILE = "mysql-bin.000002"
SOURCE_LOG_POS = 809
myloader_exec_change_source = 1
```

  plus `[config]`, `[myloader_session_variables]` and one section per table.
- **Position:** the backup's position equals the source's.

The loads used a copy without the `mysql.*`, `sys.*`, `mysql-schema-*` and `sys-schema-*` files
(23 files). That is what repman does for a seed: it gives myloader
`--regex=^(?!(mysql|sys)\.)` (`cluster/srv_job_backup.go:3503`).

What is inside the files:

- **Header:** every file starts with `SET NAMES binary`, `FOREIGN_KEY_CHECKS=0`, `SQL_MODE` and
  `TIME_ZONE='+00:00'`.
- **Data:** INSERTs with one row per line (`,(...)`), a final `;` on its own line, and raw binary
  bytes in double-quoted strings with `\n`, `\0`, `\r` and quotes escaped.
- **Trigger and routine bodies:** written without `DELIMITER`. The statements inside a body end
  in `"; "` (a space after the semicolon); only the final `END;` ends at the line break.

## How splitdump classifies the files

| mydumper file | splitdump category (`classifyFile`, `utils/splitdump/restore.go:406`) |
|---|---|
| `db.t-schema.sql.gz` | Schema |
| `db.v-schema-view.sql.gz` | Schema (after the tables) |
| `db.t.NNNNN.sql.gz` | Data, table `db.t`, shard NNNNN |
| `db-schema-create.sql.gz` | **Data** |
| `db-schema-triggers.sql.gz` | **Data** |
| `db-schema-post.sql.gz` (routines, events) | **Data** |
| `metadata` | Not loaded; read for the position (`splitdump.ReadMetadata`) |

Totals: 9 schema files, 13 data files, 0 post-data files.

Data files are loaded in name order (`sortSplitdumpDataFiles`). `mdt-schema-post` and
`mdt-schema-triggers` sort before `mdt.audit`, so they run before any table data.

## Load A: `splitrestore` CLI

```
replication-manager-cli splitrestore --input-dir /bk/md_seed --mysql mariadb \
  --mysql-args "--force --batch -h <target> -uroot -p..." --parallel 2 --splitdump-create-databases
```

**Exit status 0 in 3.5 s.** The log says:

```
[WARN] Splitdump metadata malformed; continuing without binlog info: splitdump metadata invalid: missing binlog file
[INFO] Splitdump restore files listed (schema=9 data=13 post=0)
```

| Check | Result |
|---|---|
| 7 tables (rows, hash) | Identical to the source |
| View, foreign key | Present |
| Trigger, routines, event | **0, 0, 0** |
| Audit rows | 2000, as the source: no trigger existed during the load |
| Position before → after | empty → `gtid_binlog_pos` `0-2-66`; `gtid_slave_pos` empty |
| Binlog | 5 files, 67 transactions; the largest 1.87 MB (one INSERT each: the mysql client autocommits) |

Why the trigger and routines are missing:

- The trigger file, run alone through the mysql client without `--force`, fails with
  `ERROR 1064 (42000) at line 11 ... near '' at line 3`. The client ends the
  `CREATE TRIGGER ... BEGIN INSERT ...;` statement at the first `;` inside the body.
- With `--force` (the CLI default, `clients/client_splitrestore.go:49`), the client exits 0 and
  the CLI reports nothing.

## Load B: repman's Go loader

A temporary live test, deleted after the run and never committed, called
`server.restoreSplitdumpWithMysql(ctx, dir, false)`:

- the target is the cluster master, so `sql_log_bin=1`, as on a staging or child master;
- `BinaryLogFile` is set, so the #2124 gate is started;
- `backup-logical-load-threads` 2, `backup-splitdump-create-databases` on.

### Run 1: the seed copy as is

The call returned in 0.14 s with:

```
Error 1064 (42000): You have an error in your SQL syntax; ... near '' at line 4
```

| Check | Result |
|---|---|
| Schema phase | Done: 7 tables and the view created |
| Data | **0 rows in every table** |
| Trigger, routines, event | 0 |
| Position before → after | empty → `0-3-15` |

The data phase started with the trigger and routine files (name order). Their bodies were
split at the first `;` (`forEachSplitdumpStatement`, which ends a statement at any line that
ends in the delimiter once trimmed). The first error cancelled every worker
(`utils/splitdump/restore.go`, `setErr`).

### Run 2: the same copy without the trigger and routine files

Without `mdt-schema-post`, `mdt-schema-triggers` and `mdt2-schema-triggers` (20 files), on the
same target after `DROP DATABASE` of `mdt` and `mdt2` and `RESET MASTER`:

| Check | Result |
|---|---|
| 7 tables (rows, hash) | Identical to the source: multi-row INSERTs, raw binary values, the two-chunk table, TIMESTAMPs, `;` in strings, the table without a primary key, the generated column |
| View, foreign key | Present |
| Position before → after | empty → `0-3-23`; **`gtid_slave_pos` empty**: the metadata was rejected (`ErrMetadataInvalid`), so no GTID was applied |
| Binlog | 23 transactions. **The largest is 56.8 MB**, the whole `mdt.big.00000` file in one commit: 32 INSERTs, below the 500-statement batch (`splitdumpBatchStatements`). `mysql-bin.000001` grew to 57 MB with `max-binlog-size=16M` |

After the load, the test's hand-built `ServerMonitor` panicked in the restore's final
`server.Refresh()` (`cluster/srv_job_backup.go:3071` → `CheckVersion`). That is the test setup,
not the loader: the load had finished and the data checks pass.

## Control C: myloader

The same seed copy, with repman's myloader options and `--enable-binlog` (a master):

```
myloader --overwrite-tables --verbose=3 --innodb-optimize-keys=skip \
  --max-threads-for-schema-creation=1 --max-threads-for-index-creation=1 --enable-binlog \
  --host <target> --user root --password ... --directory=/bk/md_seed --threads=2
```

**Exit status 0.**

| Check | Result |
|---|---|
| 7 tables (rows, hash) | Identical to the source |
| View, trigger, routines, event, foreign key | 1, 1, 2, 1, 1: all present |
| Audit rows | 2000: the trigger was created after the data |
| Position before → after | empty → `0-4-45`; `gtid_slave_pos` empty |
| Binlog | The largest transaction is 39.5 MB (myloader commits every 1000 statements and at the end of each file) |

myloader also ran the replication statement from `metadata`
(`myloader_exec_change_source = 1`, written because of `--source-data=2`):

```
CHANGE MASTER TO SOURCE_LOG_FILE = "mysql-bin.000002", SOURCE_LOG_POS = 809 FOR CHANNEL ''
```

MariaDB refused that syntax with `ERROR 1064`. myloader logged it under "Errors found" and
still exited 0. Repman sets the position itself after the load.

## MariaDB 10.11 rerun

The same test on MariaDB 10.11.19:

- fresh containers;
- the original seed, with the server's default collation `utf8mb4_general_ci` (on 11.4 the
  databases had to be recreated with that collation);
- repman's mydumper arguments, the same seed copy without `mysql` and `sys` (23 files);
- the same three loads, and the same commands.

| | MariaDB 11.4.13 | MariaDB 10.11.19 |
|---|---|---|
| Source position | `0-1-20`, `mysql-bin.000002:809` | `0-1-20`, `mysql-bin.000003:809` |
| mydumper dump | Segfault with the default collation; exit 0 with `utf8mb4_general_ci` | **Exit 0 with the default collation** |
| Backup `metadata` position | Equals the source | Equals the source |
| System schema files | `mysql` 43, `sys` 207 | `mysql` 44, `sys` 202 |
| **A: CLI**, exit status | 0 | 0 |
| A: table data | Identical | Identical |
| A: trigger, routines, event | 0, 0, 0 | 0, 0, 0 |
| A: metadata | "malformed", no position | "malformed", no position |
| A: position after | `0-2-66` | `0-2-66` |
| A: transactions, largest | 67, 1.87 MB | 65, 1.87 MB |
| **B: Go loader, run 1** (seed copy as is) | `Error 1064`; tables and view created, 0 rows; `0-3-15` | `Error 1064`; tables and view created, 0 rows; `0-3-15` |
| **B: run 2** (without trigger/routine files) | Data identical; `0-3-23`, `gtid_slave_pos` empty | Data identical; `0-3-23`, `gtid_slave_pos` empty |
| B: run 2 transactions, largest | 23, 56.8 MB | 23, 56.8 MB |
| B: harness panic in final `Refresh` (`:3071`) | Yes, after the load | Yes, after the load |
| **C: myloader**, exit status | 0 | 0 |
| C: data and every object | Identical, all present | Identical, all present |
| C: `CHANGE MASTER TO SOURCE_LOG_FILE …` | `ERROR 1064`, logged, exit 0 | `ERROR 1064`, logged, exit 0 |
| C: position after | `0-4-45` | `0-4-45` |
| C: transactions, largest | Not counted, 39.5 MB | 44, 40.9 MB |

Every finding of the 11.4 run holds on 10.11. The only difference is that mydumper does not
crash.

## mydumper 0.17.1-1 segfaults on MariaDB 11.4

The first dump of the source crashed with exit 139:

| Run | Result |
|---|---|
| Repman's arguments | Exit 139 |
| Without `sys` | Exit 139 |
| Without triggers, routines and events | Exit 139 |
| One database (`-B mdt`) | Exit 139 |
| Without `--source-data` | Exit 139 |
| mydumper's own defaults | Exit 139 |

gdb shows where it crashes:

```
get_character_set_from_collation (collation="utf8mb4_uca1400_ai_ci")  mydumper_working_thread.c:1052
new_db_table (table="child", table_collation="utf8mb4_uca1400_ai_ci")
```

mydumper looks up a table's character set with:

```
SELECT CHARACTER_SET_NAME FROM INFORMATION_SCHEMA.COLLATIONS WHERE collation_name='<table collation>'
```

and reads the first row without checking it. On MariaDB 11.4:

- `information_schema.COLLATIONS` has `uca1400_ai_ci` with CHARACTER_SET_NAME NULL, and no row
  for `utf8mb4_uca1400_ai_ci`;
- the full name is only in `COLLATION_CHARACTER_SET_APPLICABILITY`;
- `utf8mb4_uca1400_ai_ci` is the server's default (`character_set_collations` =
  `utf8mb4=utf8mb4_uca1400_ai_ci`), so every table created without an explicit collation has it.

Only the user tables used that collation; the system schemas did not. Recreating `mdt` and
`mdt2` with `utf8mb4_general_ci` (same data, same hashes) let the dump run.

This affects repman's mydumper backups of MariaDB 11.4+ servers whose tables use the default
collation.

On MariaDB 10.11.19 the same dump of the original seed (default collation
`utf8mb4_general_ci`) ran with exit 0 ([MariaDB 10.11 rerun](#mariadb-1011-rerun)). MySQL and
Percona were not run.

## What loading mydumper backups with repman's loader would need

From this run:

1. **Trigger, routine and event bodies:** a splitter for mydumper's object files that ends a
   statement at `;` followed by a line break, not at a trimmed line ending in `;`.
2. **Load order:** `-schema-triggers` and `-schema-post` in the post-data phase, after all data,
   so triggers do not exist while the data loads. `-schema-create` before the tables, or the
   databases created first (`backup-splitdump-create-databases`, as in this run).
3. **Position:** reading mydumper's `[source]` keys (`executed_gtid_set`, `SOURCE_LOG_FILE`,
   `SOURCE_LOG_POS`); `JobMyLoaderParseMeta` already parses the file.
4. **Commit size:** a limit per transaction in bytes. One mydumper data file is one commit
   today (56.8 MB here; with repman's `--chunk-filesize=1000`, up to about 1 GB). The #2124
   gate can pause only between commits.
5. **System schemas:** leaving out `mysql` and `sys` for a seed, as repman does for myloader.

Already working through the Go loader: mydumper's data files (multi-row INSERTs, raw binary
values in double-quoted strings, chunked tables, the per-file session header), the view
placeholders, and table creation.

## Not covered

- MySQL 8.4 and Percona.
- The full backup with the `mysql` and `sys` schemas, through either loader.
- A binlog rotation pausing the #2124 gate: no monitor ran in the Go loader test.
- The lab: reserved for other work; nothing ran there.
