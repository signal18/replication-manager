# Binlog capture, encryption and the crash delta archive

Facts established empirically on the cloud18 fleet (2026-07-09, MariaDB 11.8,
`encrypt_binlog=ON` + `log_bin_compress=ON` — the standard compliance profile),
by running `mariadb-binlog` capture variants against a live diverged old
master (belair/db1). They govern how the crash delta backup (`backupBinlog`,
`cluster/srv_rejoin.go`) and the continuous binlog backup
(`JobBackupBinlog`, `cluster/srv_job_backup.go`) behave on encrypted clusters.

## How encrypted binlogs behave for remote capture

- **The server dump thread decrypts.** `mariadb-binlog
  --read-from-remote-server` (raw or text) receives PLAINTEXT events from an
  encrypted master — encryption is at rest only; replication clients never
  need the key. A raw capture of an encrypted binlog is therefore a fully
  decodable, flashback-ready plaintext file.
- **Mid-file `--start-position` works — at a VALID event boundary.** The
  "encryption blocks mid-file dumps" theory is FALSE. Any valid boundary
  (an `# at NNN` offset) serves fine; an arbitrary byte offset fails with
  `binlog truncated in the middle of event` and leaves a header-only stub.
- **Slave-reported positions are valid boundaries.** `Read_Master_Log_Pos`
  from `SHOW SLAVE STATUS` was verified to be a valid dump boundary on the
  master's binlog, even with `log_bin_compress`. The crash anchor
  (`crash.FailoverMasterLogFile/Pos` = elected slave's IO position) is sound.
- **An empty delta produces a ~296-byte stub**: format description event +
  `Start_encryption` + EOF. This is a LEGITIMATE result when the old master
  has no events past the anchor (the app-level split simulator never cuts DB
  replication, so the elected slave usually received everything). It is
  byte-identical in shape to some failure stubs — which is why the capture
  is validated by artifact content (size > 512 = has events), and a real
  command failure asserts `WARN0182` + `canFlashBack=false`.
- **GTID `--start-position` also works** (MariaDB ≥10.8 client): text mode
  cleanly; raw mode produces a correct tail file but exits non-zero with a
  spurious `Binary logs never reached expected GTID state` — do not judge
  raw GTID captures by exit code. Not currently used (file+pos kept for
  compatibility with older releases).

## Security exposure — decrypted binlogs on disk

- The crash delta archive (`<workdir>/<cluster>/crash-bin-<ts>/`) holds the
  old master's divergent tail DECRYPTED (the server decrypts on dump).
  Deliberate choice: capture only the tail (from the failover anchor), never
  the whole file, to bound the exposure. Retention: 3 most recent archives
  per cluster (`purgeCrashBinArchives`).
- The continuous binlog backup in `client`/`mysqlbinlog` copy mode stores
  ENTIRE binlogs DECRYPTED in the backup directory and ships them to restic.
  In `ssh` copy mode the files stay encrypted at rest — but are then
  undecodable without the server key (useless for PITR outside the server).
  This trade-off is a per-deployment policy decision, unresolved 2026-07-09.

## Why the crash delta was never available before 2026-07-09 (fixed)

1. `backupBinlog` pre-cleaned its staging file with a RECURSIVE
   `filepath.Walk` over the whole working dir; archived copies under
   `crash-bin-*` keep the same filename, so every new capture deleted every
   past archive (16 months of empty `crash-bin` dirs; proven by dir mtimes).
   The staging clean exists for a good reason — after bootstrap/`RESET
   MASTER` the binlog sequence restarts, and a stale same-named staging file
   must never reach flashback (`60a8516ee`) — so it now cleans the working
   dir ROOT only.
2. `saveBinlog` ignored mkdir/rename errors: empty archive dirs that look
   like backups.
3. No artifact validation (see stub note above).
4. `docker/dev/run.sh` truncated the log on every restart, destroying the
   capture command lines needed for post-mortems (now appends).

## Known follow-up (open)

Crash records (`FailoverIOGtid` anchor, in `clusterstate.json`) and the
delta archives are INSTANCE-LOCAL. After an active handover (e.g. manual
relocate after a split), the new driver has no crash record
(`getCrashFromJoiner` → nil → `ERR00066`) and cannot flashback — even though
the delta often still sits in the old master's own binlog and could be
re-captured by any node holding the anchor. Options: replicate crash records
between peers (config git exchange / peer API), and/or gate the calm-period
active/standby toggle while an unresolved crash exists. Also: the crash
record is purged on an all-db-up flicker, losing the anchor prematurely.
