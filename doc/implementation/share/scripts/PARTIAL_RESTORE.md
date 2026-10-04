# Physical reseed and flashback: partial restore

`partialRestore()` in `share/scripts/dbjobs_new.sh` applies a physical backup
(mariabackup or xtrabackup) to a database server **while that server keeps
running**. It serves the physical reseed (`reseedmariabackup`,
`reseedxtrabackup`) and flashback (`flashbackmariabackup`,
`flashbackxtrabackup`) jobs.

It is a hot replace by design: under OpenSVC the database container must not
be stopped. When a hot replace is impossible, the job stops before changing
anything and says why; the node can then be rebuilt with a logical reseed.
There is no fallback that stops the database container.

## Why it was rebuilt

The earlier version patched each table's `.frm` into a BLACKHOLE stub,
discarded and imported the tablespace, then patched the engine back. It
failed on generated columns (BLACKHOLE refuses them: ERROR 1910), reused one
stub name so one failure cascaded, mishandled partitioned tables, never
restored foreign keys (they live in the InnoDB dictionary, not the `.frm`),
and did not work on MySQL 8 (no `.frm` files at all).

## How it works

```
receive -> prepare -> read definitions -> pre-check -> per database: recreate, import -> routines -> views -> verify -> clean up
```

1. **Receive** (`receiveBackup`). The stream repman sends is unpacked into
   `$DATADIR/.system/backup`, on the datadir volume. The job aims to leave 10%
   of the volume free (`RECEIVE_MIN_FREE_PCT`). The stream is capped with
   `head -c` at the free space above that reserve minus an allowance for what
   the job writes after the stream (prepare/export files, the definition
   server's redo and Aria logs). The allowance is twice the redo log size of
   the local server (`innodb_redo_log_capacity`, else `innodb_log_file_size`;
   the backup's own configuration is unknown before it arrives and is normally
   the same), at least 512 MiB (`RECEIVE_RESERVE_MIB`). When the redo log
   size cannot be read (each query is bounded to 15 s), the transfer is not
   started and the job fails with that reason. It is measured when
   the stream starts, so no transfer speed can overshoot it. Free space is also
   checked every second while the stream arrives, and again after the prepare
   and after the definitions were read (`pr_disk_ok`). This is a checked
   bound, not a reservation: other writers share the volume, and a backup whose
   redo log is larger than the local server's can use more than the allowance
   while the prepare runs; the check right after the prepare then stops the job
   before any database is changed. The floor can therefore still be crossed
   between two checks. When a check stops the job, the backup is removed and
   the job fails with the reason. repman only knows the compressed backup
   size, so the unpacked size cannot be checked up front. The allowance has
   not been measured on a large production layout.
2. **Prepare**: `--prepare --export` (the `.cfg` files IMPORT needs).
3. **Prepare check** (`pr_prepare_ok`): no `completed OK!` in the prepare
   log, no further step. Checked before anything is started on the backup.
4. **Read definitions** (`pr_export_definitions`). A temporary read-only
   server (`mariadbd`/`mysqld` from the jobs container) is started on the
   prepared backup and asked, like any server, for the exact definition of
   every table, view, routine, event and trigger (`SHOW CREATE ...`). This is
   where the structure comes from: `.frm`/`.par` files, the InnoDB dictionary
   in `ibdata1`, MySQL 8's data dictionary in `mysql.ibd`.
   - Its own folders on the datadir volume: `.system/mrm_defs_ro` (a copy of
     `mysql/`, links to everything else) and `.system/mrm_defs_run` (socket,
     pid, error log, tmpdir, copies of the Aria logs). Nothing goes to the
     container's `/tmp`, which is not a volume.
   - `--innodb-read-only --read-only --skip-networking --skip-log-bin
     --event-scheduler=OFF --loose-skip-ssl`, the backup's `backup-my.cnf`
     with every key made `loose-`.
   - Logs in with the backup's own accounts. If none of our credentials
     works (root's password changed since the backup), it restarts with
     `--skip-grant-tables`. MySQL 8 still shows events then; MariaDB does not
     (it answers with no rows, not an error), so there events are reported as
     not restored.
   - MySQL 8 prepared backups have no redo log, which a read-only server
     cannot create: a first "priming" start on copies of the small system
     tablespaces creates it, and the user databases are linked in only for
     the read-only start.
   - Stopped with `SHUTDOWN` and by signal; its folders are removed.
5. **Pre-check** (`pr_preflight`): every database has a folder of that name,
   every table has a definition, no table is subpartitioned. Any failure: the
   job stops and **no database or table of the server was changed**. Before this
   point the job did write in the datadir volume: the ownership of the
   unpacked backup, and the temporary definition server's folders under
   `.system` (removed when it stops).
6. **Per database** (`pr_recreate_database`): `DROP DATABASE`, then
   `CREATE DATABASE`. Files the server does not know (leftovers of an earlier
   failed restore) make the drop fail; once the server lists no table left in
   that database, they are moved to `.system/orphan-quarantine-<run>/` and the
   drop retried. Never for `#` or `.` folders, system schemas, links, or a
   name that is not a database of the backup.
7. **Per table**, created from its definition in a session with
   `sql_log_bin=0` and `foreign_key_checks=0`:
   - InnoDB (`pr_restore_innodb_table`): create, find the file name the server
     gave it (`pr_create_and_locate`: names can be stored encoded, e.g.
     `n-dash` as `n@002ddash`), `DISCARD`, move `.ibd`/`.cfg` in, `IMPORT`.
     Partitioned tables: each partition through a staging table and
     `EXCHANGE PARTITION` (MariaDB cannot import a partitioned table whole).
     A refused import is rolled back: files moved back into the backup, table
     dropped, reported.
   - Aria, MyISAM and other file engines (`pr_restore_file_table`): MariaDB
     moves the backup's own files in (the `.frm` included), then
     `aria_chk --zerofill` for Aria (an Aria table from another server is
     otherwise reported corrupt); MySQL 8 keeps the created table and replaces
     its data files. `CHECK TABLE` must pass.
   - MEMORY: created empty (its rows are never in a backup), opened once in
     the no-binlog session: MariaDB logs an implicit `DELETE` on first open,
     which would be an errant transaction on a replica.
8. **Routines, events, triggers** from their exported SQL with their original
   `sql_mode`. On a replica, events are created disabled there, as
   replication does: `DISABLE ON SLAVE`, else `DISABLE ON REPLICA` (MariaDB
   and MySQL 8.0 know only the first, newer MySQL the second).
9. **Views** last, in up to five passes (a view can use another view).
10. **Verify** (`pr_verify_restore`): every table, view, routine, event and
    trigger of the backup must exist on the server; if not, the job fails and
    repman does not put the node back into replication.
11. **GTID**: read from the backup's binlog info and reported to repman,
    which resets and applies it and restarts replication.
12. **Clean up** after success: the backup folder is removed; the three
    newest quarantine folders are kept. After a failure the backup is kept to
    investigate (the next reseed removes it before receiving).

## Dead jobs

A dbjobs run can die without ending its job (SIGKILL, OOM kill, timeout).
Each job's `.run` lock folder holds the PID of the run that owns it; at the
start of every run, `recoverDeadJobs` ends a job whose owner is gone as
failed, through the usual channel (the jobs table in SQL mode, the job-state
API in API mode), so repman clears it like any failed job instead of refusing
every new run ("Concurrent reseed blocked"). It also removes the job's log
lock file (else the next run does not stream its log) and stops a temporary
definition server left running (`pr_stop_stale_definition_server`, which
recognises it by its `--datadir`, so the live server is never touched).

## Space on the datadir volume

The peak is the target's current data plus the whole unpacked backup, which
is about the source data directory without binlogs (including `ibdata1`,
undo and the copied redo log). Imports move files within the volume, so they
need no more; the temporary server adds tens of MB (MySQL 8: about the size
of its system tablespaces plus a redo log). On the lab: 122 MB compressed,
about 400 MB unpacked.

## Limits

| Limit | Behaviour |
| --- | --- |
| Users and grants (`mysql` schema) | Not restored; the target keeps its own (replicas receive accounts through replication). The account and grant tables are also skipped on layouts where the `mysql` schema still has MyISAM tables (MariaDB <= 10.3); other MyISAM tables of `mysql` are taken from the backup |
| Databases that exist only on the target | Kept, not dropped |
| MEMORY tables | Come back empty |
| Subpartitioned tables | The job stops before any change |
| Database names stored encoded (e.g. with `-`) | The job stops before any change |
| Events after a root password change, MariaDB | Reported as not restored; the job fails |

## Pitfalls met on the way

- Databases must be listed from the backup's server, never from its folders:
  MySQL 8 has `#innodb_redo/` there, and a quarantine step once moved the
  live redo log.
- `CHECKSUM TABLE` is not stable for tables with STORED generated columns
  (it changes when the table is reopened): verify content with sorted-row
  hashes.
- MariaDB >= 11.4 uses TLS even over a socket and its client verifies the
  certificate the server generated for itself; a small clock step made it
  "not yet valid".
- The `mariabackup` job used to run in `--innobackupex` mode, which needs its target directory as a positional argument;
  the old command only worked because `-h<host>` was taken for it, and failed with `Missing argument` once the connection
  options were long options. It now runs the native `--backup --target-dir` form (`MARIABACKUP_NATIVE_BACKUP.md`).
- Lab servers run `innodb_force_primary_key=ON`.

## Tests

`share/scripts/tests/partial_restore/` (see its README): a local harness that
runs the restore functions against real servers in Docker (MariaDB 10.5 to
11.8, Percona Server 8.0 and 8.4), with success scenarios and seven failure
scenarios (prepare failed, subpartitioned table, unsafe object name, corrupt `.cfg`, root password
changed, replica target, job killed during the definition read), plus the
OpenSVC lab cycle and kill test. `all_matrix.sh` runs the whole local matrix
(15 success + 35 failure runs = 50, preceded by `unit_checks.sh`, which checks
the free-space floor, the dead-job report retry, the object-name guard and the
configuration filter without a database) and exits 0 only when all pass.
The lab cycle restored 82/82 tables with equal content, objects and GTID.
