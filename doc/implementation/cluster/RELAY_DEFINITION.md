# Relay: only a master fed from outside the cluster

Issues: #2027 (labelling), #2028 (repair without a crash record).

## Definition

A **relay** is a master: the cluster's master whose replication on the cluster channel
(`replication-source-name`) comes from a host outside the cluster. A replica is never a
relay. A replica with replicas under it is an **intermediate replica**:

- intended with `replication-multi-tier-slave` (the tree is the target topology);
- otherwise a misplaced replica sits under it, and the fault belongs to that replica
  (ERR00064 "not a slave of declared master"), repaired by the never-relay path.

The MaxScale binlog server keeps its relay flag (`srv.go`, with `MxsBinlogOn`); every check
already pairs it with `IsMaxscale`.

## What changed

| Place | Before | Now |
|---|---|---|
| `TopologyDiscover` (`cluster_topo.go`) | any replica with a replica in the cluster got `IsRelay` and state Relay | no replica gets it; a stale flag is cleared (MaxScale excepted). `hasRelay` still counts an intermediate, so topology inference is unchanged |
| `GetRelayServer` (`cluster_get.go`, multi-tier / binlog server failover and rejoin) | the server flagged relay | the MaxScale relay, or with multi-tier the intermediate replica |
| Physical restore guard (`srv_job_backup.go`) | blocked on a relay | blocked on a relay or a server with replicas (a downstream depends on it) |
| Heartbeat false-positive check (`cluster_chk.go`) | skipped replicas of a relay | skips replicas whose source is a relay or a replica: only a direct replica sees the master's heartbeat |
| Positional no-relay repair (`srv_rejoin.go`) | source must be flagged relay | source must be a replica |
| GTID no-relay repair (`rejoinSlave`) | stopped at ERR00065 without a crash electing the master | proceeds when the source is a replica of the current master; ERR00065 kept for any other source |

Unchanged: the election skips (`IsRelay`, ERR00036), the master guards (`!master.IsRelay`
before clearing read-only, ProxySQL writer), ERR00064 detection.

## Why

On a 3-node cluster a failover run by the peer replication-manager left one replica (db1)
under another (db3) while the local master never changed. db3, a healthy replica of the
master, was labelled Relay and skipped by the election; db1's SQL thread was stopped, so
the cluster had no candidate (ERR00032). The repair of db1 (ERR00064 -> `RejoinFixRelay`)
stopped at every tick on ERR00065: no failover had elected the master, so no crash record.

## Not covered

A master fed from outside on the cluster channel is still seen as a replica (`IsSlave`), so
such a cluster has no master (ERR00012). Detecting it as master + relay is a separate change.

## Tests

- `regtest/test_misplaced_replica_not_relay.go` (`testMisplacedReplicaNotRelay`): misplaced
  replica raises ERR00064, the replica above stays Slave and electable over three ticks,
  then the repair moves it to the master with the crash records hiding the master's election.
- `regtest/test_failover_assync_autorejoin_relay.go`: the multi-tier rejoin target is an
  intermediate replica, not a server flagged relay.
