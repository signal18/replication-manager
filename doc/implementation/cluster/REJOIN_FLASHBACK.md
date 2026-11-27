# Flashback rejoin: the old master's binary log

How the flashback rejoin (`rejoinMasterFlashBack`, `cluster/srv_rejoin.go`) rewinds a failed
MariaDB master and positions it under the elected one, and what it does to the old master's
binary log. See `UNIFIED_REJOIN_DESIGN.md` for where this path sits in the rejoin cascade.

## The failure it fixes

Observed on a test cluster (MariaDB 11.8, `gtid_strict_mode=ON`) under sysbench load: the
failover elected a replica that had received the old master's domain up to `1-<old>-1203979`.
The old master had committed 132 more transactions (to `1204112`). The rejoin saved them and
replayed `mariadb-binlog --flashback` through the client; the rewind was written to the old
master's own binary log under new GTIDs (`1204113` to `1204244`), and
`SET GLOBAL gtid_slave_pos = '1-<old>-1203979'` was refused, error 1947. The rejoin fell back to
no method and the server stayed in SlaveErr.

Two facts, both checked on the saved crash binlog and on a throwaway MariaDB 11.8.9:

1. **The flashback output carries no GTID.** `mariadb-binlog --flashback` prints the reversed
   row events as `START TRANSACTION ... COMMIT` blocks, without GTID events and without
   `SET @@session.gtid_domain_id / server_id / gtid_seq_no`. Replayed with the binary log on, each
   block takes the next sequence of the server's own domain (5 reversed transactions moved
   `gtid_binlog_pos` from `-7` to `-12`). MariaDB's own tests replay it the same way
   (`mysql-test/suite/binlog/t/flashback.test`, binlog on); see the KB page *Flashback*.
2. **The lost transactions stay in the binary log.** Even when the replay writes nothing
   (`sql_log_bin=0`: `gtid_binlog_pos` unchanged, data rewound), the binary log still holds the
   lost transactions, written before the crash, past the target. With `gtid_strict_mode=ON`,
   `SET GLOBAL gtid_slave_pos` to the target fails with 1947; with it OFF the same statement is
   accepted with warning 1947, but `gtid_current_pos` stays on the lost transactions (a
   `CURRENT_POS` link would skip the new master's events with those sequences), and turning strict
   mode on again makes the server refuse to log them (1950).

## The fix

1. **Replay out of the binary log.** The client of the replay runs
   `--init-command='SET SESSION sql_log_bin=0'` (`dbhelper.SessionNoBinlog`,
   `flashbackReplayArgs`). Always, whatever the switch below: it deletes nothing. The
   `mariadb-binlog` process is waited for; a decoder that died half-way fed the client part of the
   rewind and the server is never positioned after it.
2. **Drop the binary log of an asynchronous old master** (`flashbackDropsBinlog`,
   `positionAfterFlashback`), only after the replay ended complete, and only when:
   - `autorejoin-async-flashback-drop-binlog` is true (default);
   - the old master was **not** in semi-sync at the failover: `Crash.FailoverSemiSyncSlaveStatus`,
     the elected replica's `Rpl_semi_sync_slave_status` read when it was promoted;
   - its lost events were copied first (`autorejoin-backup-binlog`, the file the flashback read,
     not empty);
   - `gtid_binlog_pos` is past the target in a domain (`parentBackupBehind`).
   Then `RESET MASTER` (`server.ResetMaster`) and `SET GLOBAL gtid_binlog_state = '<target>'`
   (`dbhelper.SetGTIDBinlogState`), and only then `SET GLOBAL gtid_slave_pos = '<target>'`
   (`ReplicationLink().SetPosition`). The server's next binlogged event follows the target in
   strict mode.
3. **Then the link**, as before: `SetReplicationGTIDSlavePosFromServer` (stop, `CHANGE MASTER` to
   the elected master with `MASTER_USE_GTID=SLAVE_POS`), start.

With the switch false the binary log is kept (the behaviour before): with `gtid_strict_mode`
the position is refused (1947) and the rejoin falls back to the other methods (reseed).

**Why not in semi-sync.** A semi-sync master commits only what a replica acknowledged
(`AFTER_SYNC`), or binlogs before the acknowledgement (`AFTER_COMMIT`, MariaDB's default and what
repman leaves: its moduleset does not set `rpl_semi_sync_master_wait_point`). MariaDB 10.6+
truncates the unacknowledged transactions from the binary log at crash recovery when the server
restarts with `rpl_semi_sync_slave_enabled=ON` (MDEV-21117), so its binary log is not past the
position. If it still is, the binary log is kept and the rejoin falls back.

## State and binlog chain (F7, F12)

The drop deletes that server's binlogs. It opens **WARN0364** on the server (the time, the GTID it
was set to, the binlogs that were there), checked every tick by
`checkFlashbackBinlogResetState` and closed by the server's next completed backup (logical or
physical) started after the drop. When a backup archive is attached, the drop is also a
**binlog gap** (`noteBinlogGap`, WARN0259, persisted in `binlog-gaps.json`), cleared by the next
backup in the archive like any other gap. WARN0364 is in memory: a repman restart forgets it, the
archive gap stays.

The lost transactions stay saved with the crash (`crash-bin-<ts>/`: the binlog copy, its `.sql`
and `.flashback.sql`). Older binlogs of the old master are kept only if the binlog backup copied
them.

## Logged and reproducible (F11)

Logged: the replay as one pipeline the operator can run again, each word shell-quoted, the
password replaced with `XXXX`; the drop decision and its reason; `RESET MASTER`,
`SET GLOBAL gtid_binlog_state`, `SET GLOBAL gtid_slave_pos` through `LogSQL`.

## Channels (T27)

The change stops, repoints and resets no replication channel. `RESET MASTER` resets the server's
own binary log, not a link; `gtid_slave_pos` is the position as before. The managed channel is
repointed by the same code as before, after the positioning.

## Tests

`cluster/srv_rejoin_flashback_test.go`: the replay arguments carry `sql_log_bin=0` and log
redacted; the drop decision (switch, semi-sync, copy); the order binlog position read, drop,
`gtid_binlog_state`, `gtid_slave_pos`; no drop keeps the binlog; a refused `RESET MASTER` never
positions; WARN0364 opens and the next backup closes it. Regtests:
`testFailoverAssyncAutoRejoinFlashback`, `testFailoverSemisyncAutoRejoinFlashback`.
