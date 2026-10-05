# Plan: finish fix/dbjobs-mariabackup-partial-restore

As of 2026-09-30. Mirrors the shared plan doc on claude.ai.

## Current state

The hot-copy restore is rebuilt and proven: a real reseed on the OpenSVC lab restored
82 of 82 tables with content identical to the master and an equal GTID. Nothing is
committed or pushed.

**What changed in `share/scripts/dbjobs_new.sh`**

- The mariabackup backup command passes explicit connection flags and its target
  directory (the old command only worked by accident).
- `partialRestore` no longer patches `.frm` files into BLACKHOLE stubs. A temporary
  read-only server started on the prepared backup exports the exact definitions of every
  table, view, routine, event and trigger; tables are created from them and their
  tablespaces imported (partitions via `EXCHANGE PARTITION`).
- No database or table changes until the prepare succeeded and every definition was
  read (the unpacked backup and the temporary definition server use the volume before). A final check compares what exists on the server with the backup and fails the
  job if anything is missing.
- Safe handling of: generated columns, foreign keys, partitioned tables, encoded table
  names, FULLTEXT, Aria (`aria_chk --zerofill`), MEMORY tables (no errant GTID), MySQL 8
  (no `.frm`, empty redo log).

**Where it is proven**

| Where | Versions | Result |
| --- | --- | --- |
| Local harness, 3 scenarios each | MariaDB 10.5, 10.6, 10.11, 11.4, 11.8 | 9/9 tables identical, all objects restored |
| Local harness | Percona Server 8.0.35 (3 scenarios), 8.4.11 (1 scenario) | 9/9 tables identical, all objects restored |
| OpenSVC lab, real repman reseed of db2 | MariaDB 10.11.19 | 82/82 tables; 81 identical by content, the MEMORY table empty by nature; GTID equal to db1 |

**Lab state**

- repman runs the build from this branch; the backup-encryption build is saved in the
  container as `/usr/bin/replication-manager.local-backup-enc`.
- Test databases `prt_lab` and `prt_lab_ref` exist on db1 and db2.
- Both nodes got `opensvc-pod-forward.service` after a reboot cut the pod network
  (Docker's FORWARD policy).
- The test harness lives in `share/scripts/tests/partial_restore/` (untracked).

## Phase 1: failure-path tests

Prove that when a hot copy cannot be done or fails, the restore stops or rolls back
cleanly: no wrong data, and the node never goes back into replication incomplete. So far
only successful restores are tested.

| # | Scenario | How the harness causes it | Pass criteria |
| --- | --- | --- | --- |
| 1.1 | Prepare failed | Truncate the prepare log, or corrupt the backup before the restore | Aborts before any change; target databases unchanged by content hash; job fails |
| 1.2 | Subpartitioned table | Add a `SUBPARTITION BY HASH` table to the schema | Aborts before any change; reason logged |
| 1.3 | Import refused partway | Corrupt one table's `.cfg` in the prepared backup | That table rolled back (no orphan `.ibd`, no stub); the others restored; final check fails; job reports errors |
| 1.4 | Root password changed since the backup | Change root's password after taking the backup | Temporary server falls back to skip-grants; tables, views and routines restored; events restored (MySQL 8) or reported as not restored (MariaDB), never lost silently |
| 1.5 | Events on a replica | Make the harness target a replica | Events created `DISABLE ON SLAVE` (MariaDB) or `DISABLE ON REPLICA` (MySQL) |
| 1.6 | Interrupted run | Kill the job while the temporary server runs, then run again | The rerun cleans up `mrm_defs_*` and succeeds |

- **Where:** `share/scripts/tests/partial_restore/scenarios.sh`, run by
  `failure_matrix.sh` on MariaDB 10.5, 10.11, 11.8 and Percona 8.0.35, 8.4
  (step-by-step instructions, including the lab, in that folder's README).
- **Lab:** one success cycle (`lab_reseed_cycle.sh`) on the new build, plus the
  interrupted run by hand (README, "OpenSVC lab"). 1.1 and 1.3 cannot be forced
  through repman without a test build; the local runs cover them with real
  servers and the same code.
- **Done when:** every scenario meets its criteria on every version; any bug found is
  fixed and the scenario rerun.

**Lab result (2026-09-30, build 11:33):** success cycle passed (82/82 tables, 81
identical, the MEMORY table empty by nature; objects and GTID equal; db2's events
`SLAVESIDE_DISABLED`). Interrupted run: job killed while the temporary server ran;
db2 untouched; after a forced `job-cancel` the next reseed logged "Stopping 1
temporary server(s) left by an earlier restore" and passed the same comparison.

**Found on the lab and fixed on this branch:**

| Problem | Fix |
| --- | --- |
| A dead dbjobs job (killed, OOM, timeout) kept the server "reseeding" in repman forever; every new reseed was refused ("Concurrent reseed blocked") until an operator force-cancelled it (`reseed-cancel` does not: it only cancels jobs not yet started) | A job's `.run` lock now holds its owner PID; `recoverDeadJobs` at the start of every dbjobs run ends a job whose owner is gone as failed, through the usual channel (jobs table in SQL mode, job-state `error` in API mode), so repman clears it like any failed job; it also removes the job's log lock file (else the next run of the job does not stream its log: "Lock file ... exists. Exiting."); for a reseed/flashback it also stops the leftover temporary server |
| `JobInsertTask` (`cluster/srv_job.go`) refused a task whose previous run was still open with "Failed to retrieve data on jobs table: <nil>" | The message now says the task is still open in the jobs table, with its id and state |

**Bugs found and fixed in `dbjobs_new.sh` (2026-09-30)**

| Scenario | Bug | Fix |
| --- | --- | --- |
| 1.6 interrupted | A killed job left the temporary server running forever (memory, open files) | `pr_stop_stale_definition_server`: the next restore stops any server whose `--datadir` is the temporary folder |
| 1.1 prepare failed | The temporary server was started on the unprepared backup before the abort | `pr_prepare_ok` runs before anything else |
| 1.4 password changed | MySQL 8: the skip-grants fallback read `mysql.event`, which MySQL 8 does not have; the whole restore aborted | Events are read in skip-grants mode when the server has no `mysql.event` (MySQL 8 shows them there); MariaDB answers with no rows, so there they are still reported as not restored |
| 1.5 replica | Percona 8.0.35 refuses `DISABLE ON REPLICA`; the event was lost | Both spellings are tried in turn (`DISABLE ON SLAVE`, then `DISABLE ON REPLICA`) |
| any, MariaDB 11.8 (intermittent) | The temporary server's self-generated TLS certificate was refused by its own client ("certificate is not yet valid"); the restore aborted safely, and the server only stopped by `kill -9` after 60 s | Temporary server and its client run with `--loose-skip-ssl` (socket only, no TLS needed); the server is always also stopped by signal |

## Dropped: full-reseed fallback

Decided 2026-09-30: no fallback that stops `container#db` and moves the whole backup
back. When a hot copy is impossible, the reseed aborts before any change and says why;
the node can then be rebuilt with a logical reseed.

## Phase 2: wrap-up for review

Decided 2026-09-30: commit the harness (only `.work/` ignored), add the disk-space
safeguards, keep databases that exist only on the target. The user commits.

- [x] Disk space: the transfer is capped (free space minus 10% of the volume minus an
      allowance of twice the redo log size, at least 512 MiB) and watched, and the floor is checked again after the prepare
      and after the definitions were read (`receiveBackup`, `pr_disk_ok`); the backup is removed after a successful
      restore; quarantine folders bounded to the newest three. An up-front check
      is not possible: repman only knows the compressed size.
- [x] `doc/implementation/share/scripts/PARTIAL_RESTORE.md` (T8).
- [x] User documentation draft: `PARTIAL_RESTORE_USER_DOC.md`, to publish on
      docs.signal18.io.
- [x] Harness made self-contained (lab helpers `lab_rmapi.sh`, `lab_dbstate.sh`,
      `lab_kill_during_defs.sh`); its `.gitignore` keeps only `.work/` out.
- [x] GitHub issue draft: `PARTIAL_RESTORE_ISSUE_DRAFT.md` (filed by the user,
      then deleted).
- [x] Final check (build 12:21): local subset 12/12 (MariaDB 10.5, 11.8, Percona 8.4:
      keep-schema, leftovers, bad-cfg, interrupted); lab cycle 82/82, GTID equal, backup
      folder removed after success, datadir volume 1.2 GB free instead of 819 MB.
- [ ] Lab clean-up.
- [ ] The user commits.

## Known limits

These are documented rather than fixed on this branch; each either fails cleanly or is
harmless.

| Limit | Effect | Why it is accepted |
| --- | --- | --- |
| Users and grants (`mysql` schema) not restored | The target keeps its own accounts | Replicas receive accounts through replication |
| Databases that exist only on the target are not dropped | The node is not an exact copy | Dropping them is destructive; decided 2026-09-30 to keep them |
| MEMORY tables come back empty | No rows | Their rows never exist in any backup; a restart empties them too |
| Database names stored encoded (e.g. with `-`) | The pre-check aborts before any change | Rare; use a logical reseed |
| Subpartitioned tables | The pre-check aborts before any change | `EXCHANGE PARTITION` cannot hot-replace subpartitions; use a logical reseed |
| Rare cases (non-ASCII partition names, SPATIAL-only edge cases) | Fail cleanly and are reported | Not worth special work |

## Order and checkpoints

Phases run in order, with a check-in after each one before starting the next.

1. **Phase 1, failure-path tests:** about an hour. Checkpoint: the scenario results
   table, plus any fix it needed.
2. **Phase 2, wrap-up:** under an hour. Checkpoint: docs, issue draft and a clean final
   run for review and commit.
