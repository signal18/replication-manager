# partialRestore test harness

Exercises `partialRestore()` from `share/scripts/dbjobs_new.sh` against real
servers in throwaway Docker containers: a real physical backup, a real
`--prepare --export`, then the hot restore, verified by per-table
`CHECKSUM TABLE` against the backup-time values and by checks on views,
triggers, routines, events, foreign keys, sequences and non-InnoDB tables.

The functions under test are extracted from the script on every run (the
block between `pr_log()` and `jobsCheck()`), so the harness always tests the
current code.

## Scenarios

| Scenario | Target state before the restore |
|---|---|
| `keep-schema` | Tables exist with diverged data (normal reseed/flashback) |
| `no-schema` | The restored databases do not exist on the target |
| `leftovers` | Debris of an earlier failed restore (orphan `.ibd`, stub `.frm`) |
| `exportcheck` (MySQL only) | Runs only the definition export and verifies the backup files are unchanged (SHA-256) |

## MariaDB (mariadb-backup)

```bash
# one run
./run_mariadb.sh 10.11 keep-schema
# full matrix: VERSIONS and SCENARIOS are optional
VERSIONS="10.5 10.6 10.11 11.4 11.8" ./matrix_mariadb.sh
```

## MySQL / Percona Server (xtrabackup)

xtrabackup is not shipped in the server images, so the server and a
`percona/percona-xtrabackup` container of the same version share the datadir
volume and network namespace.

```bash
./run_mysql.sh 8.0.35 8.0.35 keep-schema    # <server tag> <xtrabackup tag> <scenario>
```

Per-run working folders go to `.work/` next to these scripts; containers and
volumes are removed at the end of each run.

## Failure paths (Phase 1)

`scenarios.sh` adds six scenarios where a hot copy cannot be done or goes
wrong. Each run ends with one line, `RESULT <scenario> PASS` or
`RESULT <scenario> FAIL: <reasons>`; the success scenarios above print it too.

| Scenario | What the test does | Expected result (checked automatically) |
|---|---|---|
| `prepare-failed` | Skips `--prepare`; the log has no `completed OK!` | rc≠0, "aborted before any change"; target identical to before (tables, objects, files); no temporary server started |
| `subpart` | Adds a `SUBPARTITION BY HASH` table before the backup | rc≠0, "is subpartitioned", "aborted before any change"; target identical to before |
| `bad-cfg` | Overwrites `prt_a/gen.cfg` in the prepared backup with random bytes | rc≠0; `prt_a.gen` absent (rolled back), its files back in the backup, none in the datadir; the 8 other tables identical; "SKIPPED prt_a.gen" and "VERIFY FAILED for prt_a" logged |
| `pw-changed` | Changes root's password after the backup | Temporary server falls back to `--skip-grant-tables`; all tables identical; view, trigger, function restored; the event restored (MySQL 8 shows events in that mode, rc=0) or reported "SKIPPED EVENT prt_a.ev_tick" (MariaDB, rc≠0), never lost silently |
| `replica` | Configures (not starts) a replication source on the target | rc=0; all identical; `ev_tick` status `SLAVESIDE_DISABLED` / `REPLICA_SIDE_DISABLED` |
| `interrupted` | Kills the job (SIGKILL) while the temporary server runs, then runs it again | The rerun logs "left by an earlier restore" and stops that server; rc=0; all identical |

Every scenario also checks that no temporary server is still running, no
`.system/mrm_defs_*` folder is left and no `mrm_pivo*` staging table exists.

```bash
# everything: MariaDB 10.5 10.11 11.8, Percona 8.0.35 and 8.4 (about 25 minutes)
./failure_matrix.sh
# a subset
MARIADB="10.11" MYSQL="" SCENARIOS="bad-cfg interrupted" ./failure_matrix.sh
# one run with its full output
./run_mariadb.sh 11.8 bad-cfg
./run_mysql.sh 8.4 8.4 pw-changed
```

The matrix keeps each run's output in `.work/fail-<server>-<scenario>.log`;
the restore log itself (`reseed.out`) is under the run's `.work/<run>/out/`
folder, whose path the log's last line gives. A FAIL names what did not
match; the log's `--- error summary` and `--- skipped/reported` sections
usually show why.

To test a modified script without touching `dbjobs_new.sh`, pass its path:
`./failure_matrix.sh /path/to/dbjobs_new.sh`.

## OpenSVC lab (real reseed through repman)

The local runs use the same functions against real servers; the lab adds
repman, the jobs container and the transfer. db1 (master) is on
opensvc-node1, db2 (replica) on opensvc-node2.

1. Build and deploy repman (the dbjobs script is embedded in it):
   ```bash
   make pro
   scp build/binaries/replication-manager-pro opensvc-node1:/tmp/replication-manager.new
   ssh opensvc-node1 'C=repmanlab..repman.container.repman
     sudo docker cp /tmp/replication-manager.new $C:/usr/bin/replication-manager
     sudo docker exec -u root $C chown repman:repman /usr/bin/replication-manager
     rm -f /tmp/replication-manager.new; sudo docker restart $C'
   ```
   Do not copy the running binary over
   `/usr/bin/replication-manager.local-backup-enc`: that is the saved
   backup-encryption build.
2. Wait until both jobs containers have the new script (repman pushes it):
   `ssh opensvc-node2 'sudo docker exec repmanlab..db2.container.jobs grep -c pr_stop_stale_definition_server /docker-entrypoint-initdb.d/dbjobs_new'`
   must print 1 (same on node1 with db1).
3. Success cycle: `./lab_reseed_cycle.sh`. It needs two helpers on
   opensvc-node1, copied from this folder (a node reboot wipes `/tmp`):
   `scp lab_rmapi.sh opensvc-node1:/tmp/rmapi.sh; scp lab_dbstate.sh opensvc-node1:/tmp/dbstate.sh`. Pass: "Partial restore completed successfully", db2 back to
   `Slave`, every table identical except `prt_lab.e_memory` (MEMORY, empty
   by nature), object counts and GTID equal.
4. Interrupted run (scenario `interrupted`, by hand): start a reseed of db2
   (step 3 of the cycle script, or the GUI), then on opensvc-node2 wait for
   the temporary server and kill the job script, not the server:
   ```bash
   sudo docker exec repmanlab..db2.container.jobs sh -c 'ls /var/lib/mysql/.system/mrm_defs_run/mariadbd.sock'
   sudo docker exec repmanlab..db2.container.jobs ps -eo pid,args | grep '[d]bjobs_new'
   sudo docker exec repmanlab..db2.container.jobs kill -9 <every pid running .../dbjobs_new>
   ```
   (`lab_kill_during_defs.sh`, copied to opensvc-node2, does both steps:
   `scp lab_kill_during_defs.sh opensvc-node2:/tmp/ && ssh opensvc-node2 /tmp/lab_kill_during_defs.sh`.)
   db2 stays out of replication (`SlaveErr`) with its data untouched. Within
   about two minutes the next dbjobs run finds the job's `.run` lock whose
   owner PID is gone and ends the job as failed; repman logs "Job
   reseedmariabackup ended with ERROR: Job reseedmariabackup was
   interrupted ..." and the jobs row reads `done=1, state=5`. The same run
   logs "Stopping 1 temporary server(s) left by an earlier restore" in
   `reseedmariabackup.out`. No cancel is needed: run `./lab_reseed_cycle.sh`
   again; it must pass
   as in step 3. Afterwards `ps -eo args | grep -c '[m]rm_defs_ro'` in the
   jobs container must print 0.
