# Failover by catch-up: the new master takes what the old one had

Status: **REJECTED by the maintainer (2026-11-27). Not to be coded.** Kept so the idea does not come back without this reason.

**Why it is rejected:** replaying the old master's transactions into a database that kept living is
dangerous. Example, a payment: the client pays, the old master commits and dies before any replica
received it; the client gets an error or no answer and pays again on the new master; the catch-up
then brings the first payment back: the client is charged twice. Only the application or a human
knows whether a lost transaction was already redone, compensated or cancelled. A partitioned old
master that is not dead may also keep writing, and the catch-up would merge both sides.

repman's rule stays the one of UNIFIED_REJOIN_DESIGN.md: the new master is the truth; the old master
rejoins by rewinding what no one else has (flashback, or reseed); the lost transactions are captured
before the rejoin (autorejoin-backup-binlog), shown in the lost-events viewer with the exact command to
replay them by hand (F11), and the operator decides (F1). REJOIN_FLASHBACK.md is that rule.

## The problem

After a failover, the old master can hold transactions that no replica received: always in
asynchronous replication, and in semi-sync too (MariaDB's default wait point is AFTER_COMMIT and
repman does not set it; with AFTER_SYNC, a transaction fully written to the binary log but not
acknowledged is still committed by crash recovery, so the old master is ahead as well).

Today repman's answer, when the old master comes back, is to throw those transactions away:
the rejoin by flashback rewinds them (REJOIN_FLASHBACK.md), and for an asynchronous old master its
binary log is dropped to the rejoin position. Those transactions were committed and, in
asynchronous replication, acknowledged to the client. The rewind is a controlled data loss.

## What MariaDB's replication developers say

- Monty (MDEV-21117, 2024-02): "All fully written transactions in the binary log should roll
  forwards. The half or not written one should do rollback." Only a half-written transaction may be
  cut from the binary log.
- Monty (MDEV-33465, 2024-03): truncating the binary log is right only when the old master was a
  semi-sync master and is changed to a slave, and must not be automatic: "the server cannot
  automatically know if it should continue as a master or as a slave. This decision needs to be done
  by other means, like by MaxScale or a human". repman is that other means.
- Kristian Nielsen (MDEV-34878, 2025-06): fix an invariant beforehand, either "slave is always ahead
  of master" (always fail over) or "master is always ahead of slave" (always wait for the master);
  the manager restarts the crashed master with the right configuration once it has decided.
- Kristian Nielsen (MDEV-34878, 2025-07): rolling back the binary log and replaying events backwards
  (flashback) is "much more complicated than it needs to be"; with existing server features only:
  every server starts read_only=1; the manager picks a new master A; for every other node B it
  reads `@@gtid_current_pos` on B and runs `START SLAVE UNTIL <that gtid>` on A; A ends ahead of
  every node and is opened for writes (`read_only=0`); the others join it as replicas.
- MDEV-34878 was closed Won't Fix (2026-05): the server will not decide this itself.

## The design

A failover mode in which the new master catches up from every node, the old master included, before
it takes writes. Nothing committed anywhere is thrown away.

1. **Servers start read-only.** The configurator sets `read_only=1` in every server's config (the
   existing read-only tags), so a crashed master that restarts never takes writes by itself. repman
   opens writes only on the server it has made master (it already sets read_only on the replicas and
   clears it on the master).
2. **Election as today.** The candidate is chosen by the existing election (most advanced replica,
   preferred hosts, guards). It is promoted (`Promote`: the edge to the old master ends) but stays
   read-only.
3. **Catch-up.** For every other server B that answers (the other replicas, and the old master when
   it is back): read B's `@@gtid_current_pos`; if B holds GTIDs the candidate does not have, point
   the candidate at B (`ChangeSource`) and `START SLAVE UNTIL master_gtid_pos=<B's position>`; wait
   until it is reached (`WaitCaughtUp`); then the next server. At the end the candidate holds the
   union.
4. **Open writes.** `read_only=0` on the new master, then every other server is repointed to it
   (`ChangeSource`) and started, as today. The old master rejoins as a plain replica: nothing to
   rewind, no flashback, no binary log dropped.
5. **A bounded wait for the old master.** The catch-up from the old master needs it back. A setting
   bounds how long the failover waits for it before opening writes without it
   (`failover-catch-up-wait`, a duration; 0 = do not wait for an unreachable old master, catch up
   only from the servers that answer). Past that, writes open, and if the old master returns later
   holding transactions the new master lacks, the existing rejoin applies (flashback with its
   guards, or reseed), with a state saying transactions were lost and which.

## In the replication plugin interface

- A verb for the bounded apply: `StartUntil(at Position, reason)` on the `Replication` object (MariaDB
  and MySQL: `START SLAVE UNTIL master_gtid_pos` / `SQL_AFTER_GTIDS`; PostgreSQL: not supported).
  dbhelper has no helper for it today: one to add, with its PostgreSQL side decided (T1, T23).
- `Position` comparisons go through the plugin (the GTID abstraction, gtid.NewMySQLList for MySQL).
- The catch-up loop lives in the failover code, over `Replication` objects, never in a plugin.

## Laws

- T27: the candidate's link is repointed to each source in turn, then to nothing; the other links are
  repointed as in a failover today. Every change of a link opens its state (WARN0352).
- F7: states for "catching up from <server>", "old master not back after <wait>, writes opened
  without it", and "transactions of the old master lost: <GTID range>".
- T14: the mode is a setting, off by default (`failover-catch-up`); today's failover stays the default
  until this one has passed the failover series.
- T13: the failover series on dev4 (infra1), network cuts included: async and semi-sync, the old
  master back before and after the wait, a replica more advanced than the candidate.

## Questions for the maintainer

1. Off by default, or on by default once tested?
2. The default wait for the old master (`failover-catch-up-wait`).
3. Does the catch-up apply to switchover too (it is not needed: a switchover waits for the replicas),
   or failover only?
