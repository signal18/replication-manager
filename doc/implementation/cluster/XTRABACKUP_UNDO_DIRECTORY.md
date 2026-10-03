# xtrabackup backup of a MySQL 8 server that keeps its undo tablespaces in the datadir

## Problem

replication-manager's `default_path.cnf` (split-path layout) keeps the undo tablespaces of a MySQL 8.0 or 8.4 server in the
datadir (`innodb_undo_directory = ./`, through the version groups `[mysqld-8.0]` and `[mysqld-8.4]`; MySQL 8 does not scan
hidden folders such as `.system`, #1857) and gives another path (`./.system/innodb/undo`) in `[mysqld]`.

`xtrabackup` reads the `[mysqld]` group of `my.cnf` but not the version groups. It therefore looks for the undo
tablespaces in `./.system/innodb/undo`, where they are not, and the physical backup stops:

```
[InnoDB] Cannot create ./.system/innodb/undo/undo_001 because ./undo_001 already uses Space ID ...
```

This affects every `xtrabackup` physical backup job of a MySQL 8.0 or 8.4 server provisioned with that layout. The layout
comes from replication-manager's configuration, not from the image: it was reproduced on the stock MySQL and Percona Server
images; a custom image was not tested.

## Change

`share/scripts/dbjobs_new.sh`: `xtrabackup_undo_args` asks the running server for `@@version` and `@@innodb_undo_directory`
and fills the array `XB_UNDO_ARGS` with `--innodb-undo-directory=<value>` for 8.0 and 8.4 only; the `xtrabackup --backup`
command of the `xtrabackup` job gets `"${XB_UNDO_ARGS[@]}"`.

- The value is passed as the server reports it (`./` here), never made absolute: xtrabackup writes it into `backup-my.cnf`,
  and an absolute path would point a server started from that backup at the live server's undo files.
- It is passed as one array element: a space or a glob in the path is neither split nor expanded.
- The version is matched as `8.0.*` or `8.4.*`. Other series (5.7, the 8.1 to 8.3 innovation releases, 9.x) get no argument
  and behave as before; the layout has a version group for 8.0 and 8.4 only.
- When the server cannot be asked (connection, TLS or permission failure), or answers nothing (an empty version, or an empty
  `innodb_undo_directory` on 8.0 or 8.4), the backup runs as before, without the argument, and a warning with no command and
  no credential in it is posted to the `xtrabackup` job log, so the pre-existing undo error that may follow is traceable.
  A series that is not covered stays silent. No timeout is added: the query goes through the same client as the other queries of
  the script.

## Tests

- `share/scripts/tests/xtrabackup_undo/job_check.sh` (no Docker): the function with a fake client, for 8.0.x, 8.4.x, 5.7, 8.1,
  8.3, 9.1, `8.04`, an empty value, a stale array from a previous call, values with a space or a glob, and the failure of
  either query (no argument, a `WARN` posted to the `xtrabackup` job log, no credential in it).
- `share/scripts/tests/xtrabackup_undo/docker_check.sh` (real Docker). The server is a stock image started with the undo
  layout; the backup is run the way the job runs it: the argument array of the real `xtrabackup_undo_args`, asked to the live
  server, is handed to `xtrabackup` as separate argv entries (never through a shell string), from the job's working directory,
  with `--stream=xbstream`. Each case asserts, from the exit statuses and not only from the output: without the option
  `xtrabackup` exits non-zero on the undo path (`Cannot create .../undo_NNN because ... already` when the undo files are in the
  datadir, `no existing undo tablespaces found` when they are in another directory); with it, exit 0 and
  `completed OK`, the stream unpacks with `xbstream`, `backup-my.cnf` carries the option, and `xtrabackup --prepare` on that
  backup exits 0. Cases: `mysql:8.0`, `mysql:8.4`, `percona/percona-server:8.0` and `:8.4` (undo in the datadir), `mysql:8.4`
  with an undo directory whose path holds a space (the one-argument guarantee, end to end), and `mysql:5.7` (no argument).
  It checks `--prepare` of the backup this change creates; the restore of a server from that backup is not covered.
- Like the other `docker_check.sh` scripts of `share/scripts/tests/` (`db_runtime_uid_gid`, `mysql84_gtid_config`), it is a
  standalone script run by hand; no CI or make target runs them.

## Not covered

- The restore side (a server started from the backup) is not changed by this fix.
- MariaDB uses `mariabackup`, not this path.
