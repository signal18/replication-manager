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
2. **Drop the binary log of an asynchronous old master** (`flashbackDropPlan` in
   `cluster/srv_rejoin_flashback_drop.go`, then `positionAfterFlashback`), only after the replay
   ended complete (both processes exited 0), with `autorejoin-async-flashback-drop-binlog` true
   (default), when `gtid_binlog_state` is past the target, and only when **every guard** holds,
   each observed right before the drop (F5). The decision (`flashbackDropFacts.check`) reads only
   the facts gathered:

   | Guard | Holds when | Observed from |
   | ----- | ---------- | ------------- |
   | `semi-sync` | the old master was not in semi-sync at the failover | `Crash.FailoverSemiSyncMasterStatus`: its `Rpl_semi_sync_master_status` as last read before the failover (the elected replica's `Rpl_semi_sync_slave_status` stays ON until its IO thread restarts after the master's semi-sync was turned off: not used) |
   | `copy` | the lost events were copied first | the copy the flashback read (`autorejoin-backup-binlog`), not empty |
   | `replay` | the replay applied exactly the saved transactions | the row transactions counted on the replay's input (`flashbackTxCounter`) equal the saved GTIDs |
   | `max-transactions` | no more lost transactions than the cap | `autorejoin-async-flashback-drop-binlog-max-transactions`, default 10000 (0 = default, never unbounded): a failover under load loses hundreds; beyond ten thousand a reseed is the repair. The binlog size is not read: the transaction count bounds the replay, the size of a binlog says little about it |
   | `domain` | one GTID domain | `gtid_binlog_state` (live) and the target carry one domain (no multi-source, no other cluster's writes) |
   | `lost-transactions` | past the target, only what was rewound | in `gtid_binlog_state` only the old master's `server_id` is past the target; the saved GTIDs (the GTID events the analysis of the copy recorded, `Crash.DeltaGTIDs`; for a rejoin re-armed after a restart, those of the decoded copy `Crash.DeltaDecoded`) are exactly target+1 .. head of that server, their count the head minus the target |
   | `readers` | nobody reads that binary log | no binlog dump thread on the old master (live), no replication of any monitored cluster (native channel, CDC feed, child cluster) whose source is it |

   A guard that fails keeps the binary log, logs it and opens **WARN0365** (server, guard,
   values) for a day; the rejoin goes on as before (with `gtid_strict_mode` the position is refused
   and the rejoin falls back to a reseed). When all hold: `RESET MASTER` (`server.ResetMaster`)
   and `SET GLOBAL gtid_binlog_state = '<target>'` (`dbhelper.SetGTIDBinlogState`), and only then
   `SET GLOBAL gtid_slave_pos = '<target>'` (`ReplicationLink().SetPosition`). The server's next
   binlogged event follows the target in strict mode.

   Seen on a test failover: target `-1354480`, binary log head `-1354615`, the copy (from the
   failover's binlog position) held `-1354482` to `-1354615`: 134 saved for 135 past the target,
   `-1354481` in neither the copy nor the new master. The `lost-transactions` guard refuses that
   drop: the transaction left out of the copy was not rewound either. Why the failover position
   and its GTID disagree by one transaction is still to be found.
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

The drop deletes that server's binlogs. It opens **WARN0364** on the server (the time, the
number of lost transactions, the GTID it was set to, where their copy is, the binlogs that were
there), checked every tick by
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
redacted; each guard passing and refusing alone; the saved GTIDs read from a decoded copy; the
replay's transaction count; the cap's default; the order binlog position read, drop,
`gtid_binlog_state`, `gtid_slave_pos`; no drop keeps the binlog; a refused `RESET MASTER` never
positions; WARN0364 opens and the next backup closes it. Regtests:
`testFailoverAssyncAutoRejoinFlashback`, `testFailoverSemisyncAutoRejoinFlashback`.
