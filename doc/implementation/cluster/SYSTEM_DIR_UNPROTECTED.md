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

## Not changed

- An existing `.system` folder is not removed: it is in the client's datadir (F1). The user doc
  says how to move it or hide it.
- The reseed restore still stages in `<datadir>/.system/backup` (`BACKUPDIR` in the script): its
  files are moved into the datadir, so they must stay on the datadir's filesystem. The dump fix
  covers a folder left there.

## Tests

- `utils/dbhelper/schema_dump_test.go`: the split of the listed databases, PostgreSQL sends no query.
- `cluster/srv_system_dir_test.go`: `IsSystemDirIgnored` per flavor and `ignore_db_dirs` value;
  `dumpSelectableDatabases` with and without a `#mysql50#` database.
