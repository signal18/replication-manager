# failover-at-sync: the candidate check (ERR00043), #2438

## What the check does

`isSlaveElectable` (`cluster/cluster_fail.go`) skips a candidate with ERR00043 when
`failover-at-sync` and `check-replication-state` are both on and the candidate is
`IsSemiSyncReplicaOutOfSync()` (`cluster/srv_has.go`):

```
(HaveSemiSync || IsSemiSyncReplicaEnabled()) && !SemiSyncSlaveStatus
```

- `SemiSyncSlaveStatus` is the live replica status (`Rpl_semi_sync_slave_status` /
  `Rpl_semi_sync_replica_status`): ON while the replica is enabled and its IO thread runs.
- `HaveSemiSync` is the live status of either side (`HasSemiSync()`): unchanged, still what the
  enforcement (`force-slave-semisync`), WARN0048 / WARN0060 and the API read.
- `IsSemiSyncReplicaEnabled()` is the configured replica side, read from `server.Variables`
  (`RPL_SEMI_SYNC_SLAVE_ENABLED` on MariaDB and MySQL / Percona before 8.0.26,
  `RPL_SEMI_SYNC_REPLICA_ENABLED` from 8.0.26). Either name ON counts; a missing variable reads as
  not enabled.

Both semi-sync election tests are opt-in: `failover-at-sync` and `switchover-at-sync` default to false; `check-replication-state` defaults to true.

## Why

Since #454 (2022) `HaveSemiSync` is derived from the live status. A replica whose IO thread stopped has
its status OFF, so `HaveSemiSync` was false and the check was skipped: the replica stayed electable
(#2438). The enabled variable does not move with the IO thread, so the check keeps its candidate.
The enabled test is added to the former condition, never substituted for it: every candidate the
former condition refused is still refused (`TestIsSemiSyncReplicaOutOfSyncKeepsFormerBlocks`).

## Behaviour, and what it does not cover

- A replica with semi-sync enabled and status OFF at election is skipped (ERR00043, one state per refused replica). If no
  candidate is left the failover does not happen and the failed master stays in the failed state. In automatic mode the cluster
  shows ERR00032 ("No candidates found") next to the ERR00043 of each refused replica; ERR00002 ("Waiting for a user manual
  failover") is the manual-mode state. A manual failover runs the same election and is refused the same way.
- The refusal does not change the election ranking. `electFailoverCandidate` computes, over all replicas it examined (refused ones included),
  the sequence number of each replica in the master's GTID domain (`maxseq`). When `maxseq` is positive the election requires a replica at that highest sequence:
  among the replicas at that value it takes a preferred one first, otherwise the first one that is electable. Only when `maxseq` is zero does it
  use the highest binlog position (`maxpos`) in the same way. A refused replica is skipped in favour of another replica at the same value; if
  the refused replica is the only one at the highest value there is no candidate, and the election does not choose a less advanced replica.
  (`electSwitchoverCandidate` returns a preferred replica that passes the electability checks before ranking; a refused preferred replica is
  skipped and the others are examined.) The ranking functions are identical to the baseline.
- An ordinary asynchronous replica, with neither observed semi-sync activity nor replica-side semi-sync enabled, is not rejected by this check
  (asynchronous replication, or a mixed cluster: such a replica can be elected when the semi-sync ones are refused). The previous
  `HaveSemiSync`-based rejection remains in effect.
- Not covered: a primary that fell back to asynchronous replication while the replicas keep reporting status ON
  (for instance their IO thread reconnecting). The status is "enabled and IO thread running", not an
  acknowledgement. The check does not guarantee that no transaction is lost.
- Behaviour change for a planned switchover: `electSwitchoverCandidate` runs `isSlaveElectableForSwitchover`
  (`switchover-at-sync`, unchanged) and then `isSlaveElectable`, so a switchover meets this check with `failover-at-sync`.
  With `failover-at-sync` and `check-replication-state` on, a switchover candidate whose semi-sync replica side is enabled but
  whose replica status is OFF (for instance a stopped IO thread) is now refused (ERR00039 for the candidate, ERR00043 for the
  reason); this test did not refuse it before. With the master alive a stopped IO thread was already refused by ERR00087, so
  the difference is for a replica that is enabled and replicating without reporting status ON. Turning `failover-at-sync`
  off removes the test from the switchover as well; rolling restarts already turn it off for their duration.
- Scope boundary: the separate `switchover-at-sync` test in `isSlaveElectableForSwitchover` (`cluster/cluster_chk.go`) is unchanged and still reads `HaveSemiSync`. A replica that is enabled with status OFF while neither side reports status ON passes it. Accepting a candidate is an election decision only: a switchover freezes the old master and waits for the candidate to catch up, which is separate from this check.
- `isFoundCandidateMaster` evaluates the election every monitoring tick: ERR00043 can be raised on a healthy cluster for an enabled replica whose
  status is OFF, like ERR00087 for a stopped IO thread. By source, a state is raised on its transition (OPENED) and closed on its resolution
  (RESOLV), not once per tick, and an alert notification is sent from those transitions only for the error keys listed in
  `monitoring-alert-trigger` (`CheckAlert`); ERR00043 is not in the default list. Observed once, on MariaDB, over a window of about 1.5 minutes with
  default logging: one OPENED per refused replica and no repetition per tick, no ERR00043 notification, and no repeated election warning lines (those
  appear with verbose logging). Not tested: the notification path with ERR00043 added to the trigger list, a flapping replica, other engines.
- The monitor reads the variables and then the status in one refresh, one query after the other: that is not an atomic snapshot
  of the server. The per-tick evaluation and the states it raises use the values of the last refresh, so a replica whose
  enabled variable or status changed since can be judged on the previous values until the next tick. The failover election
  refreshes every replica right before it decides (`cluster_fail.go`), so it reads fresh values, still read one query after
  the other.
- On MySQL 8.4 the replica IO thread stops reconnecting to a lost primary after `SOURCE_RETRY_COUNT` attempts (default 10, interval
  `SOURCE_CONNECT_RETRY`, default 60 s); once it has stopped its replica status is OFF and the check refuses the replica. The elapsed time until then
  is not the product of the two values and was not established: in a diagnostic run with 2 attempts and a 5 s
  interval the IO threads were still reconnecting 34 s after the primary was stopped, at the time of the election.

## Operator override and its risk

While the election refuses every replica the failover does not happen; it can proceed if a candidate becomes eligible again (for instance its IO thread
restarts and its replica status turns ON), or if the operator relaxes the test. The narrower override is to switch `failover-at-sync` off: the GUI
switch "Failover Only on Semi-Sync State Sync" (Settings, Failover) or the settings API it calls,
`/api/clusters/<cluster>/settings/actions/switch/failover-at-sync`. This is a cluster-wide configuration change that stays in force until it is
switched back; it is not an override of one election. It relaxes only the semi-sync election test. It does not guarantee a promotion: the other
eligibility checks still apply (for example the replica status must be readable, the replica prerequisites must hold, a server in maintenance or in
the ignored list is never a candidate, and the ranking still needs an eligible replica at the highest sequence or position).

`check-replication-state = false` also disables this test, because the guard requires it, but it is broader: it also bypasses the other replication-state
checks of the election (an IO thread stopped while the master is alive, replication delay above `failover-max-slave-delay`, a stopped SQL thread). It does
not bypass every election check: the child-cluster exclusion, the replica prerequisites, maintenance, ignored servers and the ranking rules above stay.

The override may restore availability by allowing an otherwise eligible replica to be promoted, but it does not recover missing transactions.
Transactions the failed master committed after it fell back to asynchronous replication, and that no
replica received, are not on the promoted master. They exist only on the failed master (its data and its binary logs) if that server can still be
reached, and the former master then holds transactions the promoted master lacks. Whether they can be recovered, and how, depends on the state of that
server; nothing in this check or in the override guarantees it. The operator must assess the data-integrity risk before using the override, and
switch `failover-at-sync` back on once the cluster is stable.

## Tests

- `cluster/srv_has_semisync_test.go`: predicate (legacy / new names, enabled / disabled, missing, contradiction),
  the superset property (no candidate the former test refused is accepted), flag semantics.
- `regtest/test_failover_semisync_replica_out_of_sync.go`, run through the regtest framework
  (`POST /api/clusters/<cluster>/tests/actions/run/<test>`), dispatched in `server/regtest.go`:

  | Test | Scenario | Expected | In ALL |
  |---|---|---|---|
  | `testFailoverSemisyncReplicasOutOfSyncBlocked` | semi-sync healthy, both IO threads stopped, master falls back to async, rows only on the master, master stopped, `failover-at-sync` and `check-replication-state` ON | no promotion, no writable replica, ERR00043 per replica, no failover counted, master in the failed state | no, named-only |
  | `testFailoverSemisyncInSyncAllowed` | semi-sync healthy, master stopped | promotion, writable master, rows present, replicas recover | yes |
  | `testFailoverSemisyncDisabledAllowed` | semi-sync disabled, master stopped | ordinary asynchronous failover | yes |
  | `testFailoverSemisyncOutOfSyncAtSyncOff` | replicas out of sync (rows still on every replica), `failover-at-sync` OFF | promotion (override) | yes |
  | `testFailoverSemisyncOutOfSyncNoRplChecks` | replicas out of sync (rows still on every replica), `check-replication-state` OFF | promotion (override) | yes |

- Named-only: the blocked test commits rows the replicas lack. On a build that promotes a replica (the bug it shows) it ends
  with a promoted master and a former master holding rows the promoted master lacks; the test does not repair that. Like
  `testK8SSingleWriterLifecycle` it is not in the list ALL runs (`regtest/regtest.go`) and is run by name. Because
  `testTargets` only holds the tests of that list, it checks its own targets (master-slave from 2 servers) and reports SKIP
  before anything is changed.
- Isolation of these tests: each starts by validating a clean state (one master, every server up and healthy, every replica
  replicating, no schema left by an earlier run) before the configuration, the failover counter, the failover mode or a
  database is touched, and refuses to run otherwise (FAIL, with the reason): a refusal changes nothing and cannot enable or
  trigger a failover. Each restores what it changed in a deferred step, also after a failed assertion: stopped servers,
  semi-sync settings, replication, the first master, the test schema. The framework restores the configuration after every
  test (`RestoreConf`). A test FAILS when the cluster cannot be put back as found, and says it must be discarded.
- The scenarios stop the master, like the existing failover regtests: run them on a disposable cluster. That is documented,
  not verified: the framework has no marker of a disposable cluster and the tests do not enforce it. A test that refuses or
  fails does not stop the following tests of an ALL run; the other tests of this family refuse a cluster that is not clean, the
  other regtests have no start validation.
