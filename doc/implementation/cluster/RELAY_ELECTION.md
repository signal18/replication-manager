# Relay replicas: election and no-relay repair

Issues: #2027 (election), #2028 (repair).

## What a relay is

`cluster_topo.go` flags `IsRelay` on any replica that has a replica of its own
(`sl.IsSlave && sl.HasSlaves(cluster.slaves)`), and `srv.go` flags a MaxScale
binlog server as a relay too. Two very different things share the flag:

| Relay kind | Holds the data | Can lead |
|---|---|---|
| MaxScale binlog server (`IsMaxscale`) | no | never |
| Database replica feeding another replica | yes, full copy of the leader | yes |

## Election

`electSwitchoverCandidate`, `electFailoverCandidate` and `isSlaveElectable` skip
only `IsMaxscale` (ERR00036 now reads "is a MaxScale binlog relay"). A database
relay goes through the same checks as any replica: binlog (ERR00013), delay,
SQL/IO threads, errant transactions, divergence, filters, preferred hosts.

Before, every relay was skipped. A three-node cluster where the only healthy
replica fed a broken one had no candidate at all (ERR00032) while a full,
in-sync copy of the leader was available. Its downstream replica is already
attached to it, so promoting it needs no extra move.

Unchanged: `isOneSlaveHeartbeatIncreasing` still ignores replicas whose source
is a relay, since they do not see the leader heartbeat directly.

## Promoting a relay

- `SetMaster` clears `IsRelay` (except MaxScale): the flag is recomputed for
  replicas only, so a promoted relay kept it and skipped the read-only clear
  (`cluster_topo.go`, `srv_rejoin.go`), the ProxySQL writer swap
  (`prx_proxysql.go`) and the privilege checks (`srv_chk.go`).
- `SwitchSlavesToMaster` skips the candidate (already `stateMaster`) and now also
  any replica whose source is already the new master: the relay's own replica
  keeps its stream instead of a pointless `CHANGE MASTER`, and on a non-GTID
  cluster without positional heartbeat it is no longer put in maintenance.
- The old master rejoins the new master like after any switchover or failover.

## No-relay repair

With `replication-master-slave-never-relay`, `CheckTopology` raises ERR00064 for
a replica not under the leader and calls `RejoinFixRelay` -> `rejoinSlave`. On
the GTID path, `rejoinSlave` required a crash record electing the current
leader (ERR00065) although the record is not used for the repoint
(`MASTER_USE_GTID=SLAVE_POS` to the leader).

A relay chain exists without any local failover after a failover run by the
peer replication-manager, a leader restart or a manual change. The repair then
stayed armed and did nothing at every tick.

Now the record is not required when the replica's source is a relay that
replicates from the current leader: the replica already applies the leader's
own stream. Any other source (an old leader after a split brain) still needs
the record, so ERR00065 keeps guarding the divergent case.

## Test

`regtest/test_relay_electable_and_repaired.go` (`testRelayElectableAndRepaired`),
gated on a leader with two GTID replicas: chains B under A, checks over three
ticks that A stays a candidate with B stopped, hides the crash records electing
the leader, enables never-relay and expects B back under the leader, then chains
again and switches over: A must be elected, lose its relay flag, keep B and get
the old leader as replica.

`cluster.Crashes` is written without a lock by the test, as by `ResetCrashes`
and every failover path: no lock guards that slice today.
