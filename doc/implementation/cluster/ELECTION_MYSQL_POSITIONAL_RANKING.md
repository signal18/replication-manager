# MySQL and Percona election ranking (#2451)

## Problem

`electFailoverCandidate` and `electSwitchoverCandidate` rank each candidate with a "sequence": the sum of
the sequence numbers of the master's GTID domain, parsed by `gtid.NewList`, the MariaDB `domain-server-seq`
parser. A MySQL or Percona GTID is a `uuid:interval` set. Parsed as a MariaDB list, the dash-separated
groups of the UUID are read as `domain-server-seq`: the value does not depend on the transactions. When the
third group of the source UUID is decimal-only (a v4 UUID, or a `server_uuid` written in `auto.cnf`), every
replica of that source got the same positive value, the sequence tied whatever each replica had received,
and the preferred but less advanced replica was elected and promoted without the last committed
transactions. When that group has a letter, the value is 0 and the election already ranked by position.

The parsed value is used on two paths:

| Path | Metric | MySQL / Percona |
|---|---|---|
| master failed | `Gtid_IO_Pos` (received) | MySQL and Percona 8.4+ only (`ImportFromReplicaStatus` maps `Retrieved_Gtid_Set`); empty below 8.4 |
| master not failed (switchover, monitoring-time `FoundCandidateMaster`, failover before the master is marked failed) | `SlaveGtid` (executed) | every MySQL or Percona 5.7+ with GTID |

## Change

`cluster/cluster_fail.go`:

- `electionSeqNos(sl, ss, errss)`, shared by both election functions. A MySQL or Percona candidate with a
  replication status contributes no scalar sequence (`Seq` 0). When no candidate has a positive sequence, the
  existing positional ranking decides (see Behaviour for the cases where it does not).
  MariaDB and PostgreSQL keep the sequence of the master's GTID domain. A candidate without status keeps
  the synthetic default (`1-1-1`, `failover-restart-unsafe`).
- `binlogRank(file, pos)`, shared by both functions: the numeric suffix of the binlog file name is read
  after the **last** dot (`db1.example.com-bin.000007`), then the position padded to 12 digits. Before, the
  segment after the first dot was read (`example`: `ParseUint` error, rank 0). The `ParseUint` error is
  still ignored, as before.

Not changed: `ImportFromReplicaStatus` and the status mappings; `ServerMonitor.SlaveGtid`, `CurrentGtid`,
`GTIDBinlogPos`, `GTIDExecuted`, `Replications` (the election never assigns them: the unit test
`TestElectionDoesNotMutateGTIDFields` compares deep copies before and after each election); the GTID shown
by the API, React, AngularJS and the CLI; eligibility, errant-transaction and semi-sync checks; the
catch-up (`ReadAllRelayLogs`), the rejoin and `MASTER_AUTO_POSITION`; the failover and switchover policies
(tiers, preferred host, ignored hosts, switchover's early return for a preferred host).

`Seq` 0 in the election matrix is a local election value. It never means GTID is disabled or unavailable.

## Behaviour

- The positional rank is the same on both election paths, failed primary or not: it is built from
  `Master_Log_File` and `Read_Master_Log_Pos` of the candidate's replication channel, the **received**
  coordinate in the binlog of the server the candidate reads (the I/O thread position, not the position the SQL
  thread has applied). It is not an executed-statement coordinate. The parsed sequence that is removed for MySQL and
  Percona was the only input that read the failed-primary received set (`Gtid_IO_Pos`) or the executed set (`SlaveGtid`).
- When every candidate is a MySQL or Percona server with a replication status, nobody has a positive sequence and the
  election ranks by that positional value, whatever the UUID: this is what a cluster whose UUID has a letter already did.
  It is not true of every election: a candidate without a replication status under `failover-restart-unsafe` keeps the
  synthetic default (sequence 1) and MariaDB or PostgreSQL candidates in the same list keep their sequence, so a positive
  sequence can still decide the branch.
- On a decimal-UUID cluster the selected candidate can change: a preferred replica no longer wins over a
  more advanced one; an ineligible replica that is strictly the most advanced now gives "no candidate"
  (`ERR00032`, automatic failover blocked) where a less advanced replica was promoted; a candidate without
  status under `failover-restart-unsafe` makes the election refuse instead of electing.
- A valid binlog file name whose basename contains dots (`db1.example.com-bin.000007`) ranks correctly (MariaDB positional
  ranking too): the suffix is read after the last dot. This could not be reproduced on MySQL 8.4.13 or Percona 8.4: with
  `--log-bin=db1.example.com-bin` the server reports `@@GLOBAL.log_bin_basename` as `/var/lib/mysql/db1`, and
  `SHOW BINARY LOGS`, `SHOW BINARY LOG STATUS` and the replica's `Source_Log_File` all show `db1.example`, a name without a
  numeric suffix that every rotation reuses (the documentation says a numeric suffix is appended; this was not observed
  for that basename). The election ranks 0 on that name before and after the change. The correction is therefore
  defensive and covered by the unit test only.
- MariaDB GTID sequence ranking and PostgreSQL are unchanged.

## Limits (unchanged by this fix)

- Received coordinates measure progress through one source's binlog stream. They are comparable only for
  replicas of the same source. A multi-tier cluster has replicas that read different servers' binlogs (the relay
  reads the primary's, the replicas below it read the relay's) and the election still compares their file numbers and
  positions. That limit exists before the patch for every cluster that ranked positionally; it is not removed by it, and
  it is not harmless: the election can promote a replica that is behind. What the patch changes is its operational
  impact on a cluster whose parsed sequence used to tie the candidates (decimal-only third UUID group): develop kept
  the preferred host or the list order, the corrected ranking compares the coordinates across the two streams. The
  multi-tier regtest characterises it.
- The refresh before the election is attempted, not guaranteed; a replica that cannot be read keeps its
  last status.
- A received transaction may be partial or unapplied. The catch-up waits for the SQL thread to reach the
  received position, logs a failure and continues, and resets the relay logs afterwards.
- `GTID_ONLY` after a restart gives unavailable source coordinates; repman never sets it.
- Native GTID-set ranking (`GTID_SUBSET`, `GTID_SUBTRACT`, the union of `gtid_executed` and the received
  set) is a possible later refinement, not part of this fix.

## Tests

- `cluster/cluster_fail_election_test.go`: election decisions of the real election functions on fixture
  servers (preferred replica behind and at equal progress, most advanced ineligible, lagging ineligible,
  missing status with `failover-restart-unsafe`, dotted basename, non-GTID MySQL, MariaDB and PostgreSQL
  sequence unchanged, switchover non-preferred path and preferred host, the existing safety checks, non-mutation
  of the GTID fields, `binlogRank`).
- Native regtests, T13 (`regtest/test_election_gtid_*.go`), registered in `regtest/regtest.go` with their
  `TestTarget` and dispatched in `server/regtest.go`:

  | Test | Target | What it runs |
  |---|---|---|
  | `testElectionGTIDPositionalFailover` | master-slave, 3 nodes, mysql/percona | failover with a preferred replica that is behind |
  | `testElectionGTIDPositionalRefusal` | same | the most advanced replica is ineligible: nothing is promoted |
  | `testElectionGTIDPositionalSwitchover` | same | switchover on the non-preferred ranking path |
  | `testElectionGTIDPositionalErrant` | same, `check-replication-errant-trx` | an errant transaction still holds the automatic failover |
  | `testElectionMMSwitchSingleWriter`, `testElectionMMSwitchConcurrent`, `testElectionMMFailover` (by name) | multi-master, 2 nodes | one-candidate election, writer policy through a non-privileged account, marker propagation |
  | `testElectionRingSwitch`, `testElectionRingFailover` (by name) | multi-master-ring, 3 nodes | the virtual election, what `CloseRing` does, and the shared `FoundCandidateMaster` gate, each on a fresh ring |
  | `testElectionMultiTier` (by name) | multi-tier-slave, 3 nodes | election among replicas that read different servers' binlogs (a limitation, characterised) |

  The four master-slave tests are in the ALL list. The multi-master, ring and multi-tier tests are run by name only
  (not in ALL, no TestTarget): they switch over, stop and rejoin servers and the multi-tier one may expose a limit; each
  checks its own applicability (topology-target, servers, engine) and a healthy start and is SKIP otherwise. The
  temporary schema and the application account carry a random suffix, are created without IF NOT EXISTS, and only what a
  run created is dropped.

  The master-slave tests need a primary whose `server_uuid` has a decimal-only third group; otherwise they are
  SKIP before any change. A scenario whose parsed-sequence premise is absent (for example the failed-primary metric of MySQL or Percona below
  8.4, where the received set is empty) runs as a CONTROL: the path is positional on both builds, the expected result is PASS
  on both, and the SUMMARY says `class=CONTROL`. A premise that cannot be built (for example the replica below a relay is not
  ahead, or the candidate order is not controlled) is `class=UNESTABLISHED` and a SKIP.
  its own, that the primary accepts writes and that no server holds a global read lock, and any failure there fails the
  test; a test refuses to start from a cluster that is not healthy (SKIP with the reason). The test names stay short:
  the runner formats `<cluster>/<test>` in 60 columns.
- `regtest/test_election_gtid_harness_test.go`: the harness itself (deadlines against a mock and, with
  `ELECTION_TEST_DSN`, against a real server under a global read lock; cleanup failures reach the verdict).
