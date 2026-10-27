# The .system folder on a server that does not ignore it

Issue #2202.

## Why

The database jobs run over SSH (`scheduler-jobs-ssh`, `JobRunViaSSH`) keep their logs,
checkpoints and locks in `GetJobDatadir()`, `<datadir>/.system/jobs`. What keeps the server from
listing `.system` as a database is `ignore_db_dirs=.system` in `default_path.cnf`, which the
configurator renders. A server configured by its owner (configurator off) does not have it.

Observed on throwaway containers (MariaDB 10.11 and 11.4, MySQL 5.7), with an empty `.system/jobs`
in the datadir:

| Server | `SHOW DATABASES` | `--all-databases` dump |
|---|---|---|
| MariaDB 10.11, 11.4 | `#mysql50#.system` listed | exit 2, `Got error: 1102: "Incorrect database name '#mysql50#.system'" when selecting the database` |
| MySQL 5.7 | not listed (hidden folders skipped) | OK |

MySQL 8.0 lists no folder as a database (data dictionary). `mysqlcheck --all-databases` fails the
same way on MariaDB. With the default `backup-mysqldump-options` (`--all-databases`), every logical
backup and every mysqldump reseed from such a server failed. mydumper already ran: its
`#mysql50#` errors are not counted (`myDumperCopyLogs`). The physical dbjobs backup passes
`--databases-exclude=.system`.

## Design

- `IsSystemDirIgnored()` (`cluster/srv_cnf.go`): false only for a MariaDB server whose
  `IGNORE_DB_DIRS` variable does not name `.system`. MySQL, Percona and PostgreSQL, and a server
  whose version or variables are not read yet, answer true: an unknown answer keeps the
  configurator's layout, which a provisioned container needs (the datadir is its volume).
- `GetJobDatadir()` returns `/var/lib/replication-manager-jobs`, the `nosplitpath` folder, when
  `.system` is not ignored. The path reaches the script through `%%ENV:SVC_CONF_ENV_JOBS_DATADIR%%`.
- The script is rendered by `GetDatabaseConfig()`, also at topology load, before the variables are
  read. `JobRunViaSSH` re-renders it when its `readonly TMP_DIR="..."` is not the current
  `GetJobDatadir()`, so the first job after the variables are read already uses the right folder.
  A custom `onpremise-ssh-db-job-script` is never touched.
- `dumpSelectableDatabases()` (`cluster/cluster_get.go`), at the end of `getMysqlDumpOptions`
  (local backup, remote backup, reseed): when the options select `--all-databases` (or `-A`) and the
  server lists a `#mysql50#` database, `--all-databases` is replaced by `--databases` and the
  databases `--all-databases` would select (all but `information_schema`, `performance_schema`,
  `sys`, read by `dbhelper.GetAllDatabasesDumpSchemas`). Otherwise the options are unchanged. A dump
  with named databases writes the same `CREATE DATABASE` / `USE` statements; checked with
  `mariadb-dump` 11.4 and `mysqldump` 5.7, options and names interleaved.

## Restores: a .system the restore creates is removed

The physical reseed and flashback (and the partial restore they run) stage in `<datadir>/.system`:
`backup` (the received backup, moved into the datadir file by file, so it must be on the datadir's
disk), `mrm_defs_ro` / `mrm_defs_run` (the definitions server), `orphan-quarantine-*`. These stay
in the datadir. What changes is who removes `.system` (`share/scripts/dbjobs_new.sh`):

- `reset_backupdir`, the first step of every restore job, calls `system_dir_claim`: when `.system`
  does not exist, the run creates it with the cookie `.system/.mrm_created` holding its process
  group (`proc_pgid`, field 5 of `/proc/$$/stat`). A `.system` found in place (the configurator's,
  holding binlogs, redo and logs) gets no cookie and is never removed.
- At the end of the restore job, success or failure, `system_dir_release self` removes the
  `.system` this run owns, whole (`rm -rf`). A failed restore leaves a clean datadir: the staged
  backup is a copy of a backup repman still holds, and the quarantine goes with it (moving it out of
  the datadir would need disk nobody sized).
- At the start of every run, after `recoverDeadJobs` (which stops the definitions server of a killed
  restore), `system_dir_release` removes a `.system` left by a killed run, only once no process of
  its process group is left (`kill -0 -<pgid>`). A process still writing there (mbstream, mariabackup
  --prepare) would otherwise keep writing into deleted files: the disk is not freed until it closes
  them, and its next file fails. A reused group number only delays the removal.
- `pr_paths_ok` guards both: with an empty or relative datadir nothing is created or removed.

So `.system` exists on such a server only while a restore runs, and the existing `.system` folder of
a server is never removed (F1).

## Tests

- The claim and release (`share/scripts/dbjobs_new.sh` functions), run on a Linux host on a fake
  datadir: a restore removes the `.system` it created; a `.system` in place with a binlog is kept; a
  killed run whose child is alive keeps it, then removes it once the group is gone; an empty datadir
  creates nothing at `/`.

- `utils/dbhelper/schema_dump_test.go`: the split of the listed databases, PostgreSQL sends no query.
- `cluster/srv_system_dir_test.go`: `IsSystemDirIgnored` per flavor and `ignore_db_dirs` value;
  `dumpSelectableDatabases` with and without a `#mysql50#` database.
