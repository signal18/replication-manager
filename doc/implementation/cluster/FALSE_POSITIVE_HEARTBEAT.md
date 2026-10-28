# False-positive heartbeat check (#2208)

`failover-falsepositive-heartbeat` (on by default) cancels an automatic failover while a
replica still receives from the master: the master is alive, only repman lost it. The check
is `isOneSlaveHeartbeatIncreasing` (`cluster/cluster_chk.go`), evaluated by `CheckFailed`
on each tick while the master is failed.

## Why it never fired before

It read the global status `SLAVE_RECEIVED_HEARTBEATS` twice, the timeout apart:

- on MariaDB the global counter covers only the default connection
  (`@@default_master_connection`): with `replication-source-name` set, it stays 0 while the
  named channel receives a heartbeat every period;
- on MySQL 5.7+ it does not exist (`performance_schema.global_status`);
- a busy master sends events, not heartbeats: the counter stays flat;
- the default timeout (3 s) equalled repman's heartbeat period (3 s).

Found by `testFailoverManual` (renamed `testCancelFailoverReplicationHeartbeatDetection`), which forces a live master to failed and expects no
failover: on MariaDB 11.8 with channel `bench`, the channel counter went 12531 → 12542 in
35 s, the global counter stayed 0, and the failover happened.

## Design: observe the last receive, never wait

A sleep inside the check stalls the cluster tick for up to one heartbeat period (F2), and
measures a window the check chose. Each replica instead records when its managed channel
last received from its source (`cluster/srv_heartbeat.go`, `refreshReplicationLastReceived`,
called by `Refresh` after the slave status read):

| Engine | Source | Cost |
| --- | --- | --- |
| MariaDB | no timestamp exists. `Slave_received_heartbeats` and the IO position (`Gtid_IO_Pos`, `Master_Log_File:Read_Master_Log_Pos`) from `SHOW ALL SLAVES STATUS`, which the tick already reads. The tick that sees either move stamps `ReplicationLastReceivedAt`. The counter going down is a restarted channel, not a receive. | none |
| MySQL / Percona 5.7+ | `dbhelper.GetReplicationLastReceived`: `performance_schema.replication_connection_status` `LAST_HEARTBEAT_TIMESTAMP` (8.0: and `LAST_QUEUED_TRANSACTION_END_QUEUE_TIMESTAMP`), aged with the replica's `NOW(6)`; period from `replication_connection_configuration.HEARTBEAT_INTERVAL`. Compared as `UNIX_TIMESTAMP`: MySQL 8 refuses the zero timestamp of a channel that never received in a comparison (error 1525), and `GREATEST` over a zero and a valid TIMESTAMP is not the valid one | one query per replica per tick |
| MySQL < 5.7, PostgreSQL | none: no last receive, the check never cancels | none |

`SHOW SLAVE '<name>' STATUS` has no heartbeat columns on MariaDB: only `SHOW ALL SLAVES
STATUS` does. Nothing is read when neither the check nor `force-slave-heartbeat` is on.

The check cancels the failover (ERR00028, naming the replica, the age and the window) when a
direct replica of the master (`isDirectReplicaOfMaster`: not a replica of a relay or of an
intermediate replica) received within:

    heartbeat period + monitoring-ticker + failover-falsepositive-heartbeat-timeout

An idle master sends one heartbeat per period, repman sees it at the next tick, the timeout
is the margin. A dead master stops sending: once the age passes the window, the next tick
lets the failover go. With repman's 3 s period and a 2 s ticker the window is 8 s, usually
already spent by the fail count; with MariaDB's default period (`slave_net_timeout / 2`,
30 s) the failover decision waits up to ~35 s, hence WARN0050.

## States

| State | Level | When |
| --- | --- | --- |
| ERR00028 | cluster | the check cancels the failover |
| CINF0022 | server (replica) | master suspect or failed: how long ago the replica last received, its period. Not in peace time: the age changes each tick |
| WARN0050 | server | check on, the period is above `force-slave-heartbeat-time` or 0 (off), and `force-slave-heartbeat` did not set it |
| CINF0023 | server | `force-slave-heartbeat` set the period: server, channel, old and new period (T27) |
| WARN0268 | server | `force-slave-heartbeat` could not set it |

## Remediation: `force-slave-heartbeat`

Off by default: it is the client's consent to change their link (F1, T27).
`checkReplicationHeartbeatPeriod` (from `CheckSlaveSettings`, active monitor only) sets the
managed channel's period to `force-slave-heartbeat-time` (was a hard-coded 1 s):

- by channel name: `SetSlaveHeartbeat` sent `CHANGE MASTER TO` with no channel, which on a
  named MariaDB connection went to the default one, and `CHANGE REPLICA SOURCE` on MySQL 8.4
  (a syntax error);
- only while both threads run, as the change is STOP SLAVE / CHANGE / START SLAVE and must
  not start a thread someone stopped;
- once: the next tick reads the new period. A failure is not retried while the period stays
  the same (no STOP/START every tick);
- never on an ignored server (`IsIgnored`) or one in maintenance;
- a failed CHANGE still starts the channel again (it was running); a failed START says the
  channel is left stopped, in WARN0268.

The tracking fields are written by `Refresh` and read by the failover check, CINF0022 and
WARN0050 under `replicationReceivedMutex` (`replicationReceived()` is the consistent read);
the MySQL query runs before the lock is taken, on the monitoring connection and its
`read-timeout`. MySQL before 5.7 and PostgreSQL are skipped with no query.

`force-slave-heartbeat-time` is set from the GUI (Settings, Replication, Replication
Heartbeat Period) through `setClusterSetting`.

## Side fix: the period read

`SlaveStatus.SlaveHeartbeatPeriod` was tagged `Slave_Heartbeat_Period`; the column is
`Slave_heartbeat_period`, so it always read 0. That kept the WARN0050 / enforcement code
dormant, and made the multi-source move (`cluster_fail.go`) set a moved channel's heartbeat
period to 0, turning its heartbeats off. The PostgreSQL status queries alias the same name.

## Tests

- Unit: `utils/dbhelper/replication_heartbeat_test.go` (per-engine queries and statements),
  `cluster/srv_heartbeat_test.go` (tracking, guard, CINF0022, WARN0050, stopped threads).
- MariaDB 11.8, named channel, on a test cluster: `testCancelFailoverReplicationHeartbeatDetection` (a live master forced
  to failed) PASS, cancelled by ERR00028 (received 2.3 s ago, window 8 s); a real failover
  (`testFailoverSemisyncAutoRejoinFlashback`, busy master stopped) went on with no ERR00028;
  `force-slave-heartbeat-time` 2 raised WARN0050, `force-slave-heartbeat` set the channel to
  2 s once (CINF0023).
- MySQL 5.7.44, 8.0.41, 8.4.13 containers, channel `bench`: the query reads ~0.15 s idle,
  the event's time after one, and grows after the source stops; the heartbeat statements
  set the named channel.
