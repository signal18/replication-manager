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

## Why

Since #454 (2022) `HaveSemiSync` is derived from the live status. A replica whose IO thread stopped has
its status OFF, so `HaveSemiSync` was false and the check was skipped: the replica stayed electable
(#2438). The enabled variable does not move with the IO thread, so the check keeps its candidate.
The enabled test is added to the former condition, never substituted for it: every candidate the
former condition refused is still refused (`TestIsSemiSyncReplicaOutOfSyncKeepsFormerBlocks`).

## Behaviour, and what it does not cover

- A replica with semi-sync enabled and status OFF at election is skipped. If no candidate is left the
  automatic failover waits (ERR00032 / ERR00002), a manual failover meets the same election.
- A replica with semi-sync not enabled is not skipped, as before (asynchronous replication, or a mixed
  cluster: such a replica can be elected when the semi-sync ones are refused).
- Not covered: a primary that fell back to asynchronous replication while the replicas keep reporting status ON
  (for instance their IO thread reconnecting). The status is "enabled and IO thread running", not an
  acknowledgement. The check does not guarantee that no transaction is lost.
- `electSwitchoverCandidate` runs `isSlaveElectableForSwitchover` (`switchover-at-sync`) and then
  `isSlaveElectable`: a switchover also meets this check, with `failover-at-sync`.
- `isFoundCandidateMaster` evaluates the election every monitoring tick: ERR00043 can be raised on a healthy
  cluster for an enabled replica whose status is OFF, like ERR00087 for a stopped IO thread.
- On MySQL 8.4 the IO thread stops retrying the lost primary after `SOURCE_RETRY_COUNT` (default 10) times
  `SOURCE_CONNECT_RETRY` (default 60 s); its status then turns OFF and the check refuses it.

## Tests

- `cluster/srv_has_semisync_test.go`: predicate (legacy / new names, enabled / disabled, missing, contradiction),
  the superset property (no candidate the former test refused is accepted), flag semantics.
- `regtest/test_failover_semisync_replica_out_of_sync.go`, run through the regtest framework
  (`POST /api/clusters/<cluster>/tests/actions/run/<test>`), registered in `regtest/regtest.go` and dispatched in
  `server/regtest.go`:

  | Test | Scenario | Expected |
  |---|---|---|
  | `testFailoverSemisyncReplicasOutOfSyncBlocked` | semi-sync healthy, both IO threads stopped, master falls back to async, rows only on the master, master stopped, `failover-at-sync` and `check-replication-state` ON | no promotion, no writable replica, ERR00043 per replica, no failover counted, master in the failed state |
  | `testFailoverSemisyncInSyncAllowed` | semi-sync healthy, master stopped | promotion, writable master, rows present, replicas recover |
  | `testFailoverSemisyncDisabledAllowed` | semi-sync disabled, master stopped | ordinary asynchronous failover |
  | `testFailoverSemisyncOutOfSyncAtSyncOff` | replicas out of sync, `failover-at-sync` OFF | promotion (override) |
  | `testFailoverSemisyncOutOfSyncNoRplChecks` | replicas out of sync, `check-replication-state` OFF | promotion (override) |

- Isolation rules of these tests: each starts by validating a clean state (one master, every server up and healthy, every
  replica replicating, no schema left by an earlier run) and refuses to run otherwise, before any setting is touched. Each
  restores what it changed in a deferred step (also after a failed assertion): stopped servers, semi-sync settings,
  replication, the first master, the test schema; the framework restores the configuration after every test (`RestoreConf`).
  A test FAILS when the cluster cannot be put back as found, and says it must be discarded. The override tests keep every
  row on the replicas, so their promotion loses nothing and the former master rejoins; only the blocked test, on a build
  that promotes (the bug it shows), leaves a diverged former master, and the cluster is not repaired. The scenarios stop
  the master, like the existing failover regtests: run them on a disposable cluster.
