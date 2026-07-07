# Chaos isolation — reproducing split brain without touching the network

A test tool that severs, on a running instance, the individual communication
links a replication-manager depends on, so the split-brain protection can be
exercised end to end. Implementation: `cluster/cluster_chaos.go` (per-cluster
`db` and `arbitrator` cuts), `server/server.go` (`ChaosCutPeer`, the
server-level `peer` cut), and the `/api/heartbeat` + `HeartbeatPeerSplitBrain`
+ `GetNewDBConn` + `arbitratorElection`/`isActiveArbitration` gates.

Runtime state only, never a config key (a config key would replicate to the
peer through the config event log and isolate both sides). Always
auto-restores (default 120s, max 15min). Requires the `cluster-test` grant.
`WARN0181` shows the active cuts on the cluster every tick while armed.

## The three cuts

| cut | level | effect when severed |
|------|-------|---------------------|
| `arbitrator` | per-cluster | this node's `arbitratorElection` + `isActiveArbitration` fail — it cannot confirm authority |
| `peer` | server-level | `/api/heartbeat` goes dark **and** this node's `HeartbeatPeerSplitBrain` treats the peer as unreachable |
| `db` | per-cluster | this node's database connections for the cluster fail — it loses sight of the master |

## API

```
POST /api/clusters/{cluster}/actions/chaos-isolate-start?cut=arbitrator,peer&duration=180
POST /api/clusters/{cluster}/actions/chaos-isolate-stop
```

`cut` is any comma-separated subset of `arbitrator,peer,db` (default
`arbitrator,peer`). `duration` in seconds, bounded.

## The rule the isolated node must obey

Losing the arbitrator means it can no longer confirm it is the authority, so
it must **fail-safe: never act unilaterally** — whatever its master's state.
That single rule produces the two partitions below.

## The two partitions (armed on the ACTIVE node)

Both isolate the active repman from its peer and the arbitrator; they differ
only in whether its master is still alive.

### Partition 1 — master OK

```
POST /api/clusters/{cluster}/actions/chaos-isolate-start?cut=arbitrator,peer
```

The node still has a live master but cannot confirm authority. It must **step
down / fence its master** (not keep writing) — the surviving peer, which
still reaches the arbitrator, takes over. **Guards against dual-master.**

### Partition 2 — master FAILED

```
POST /api/clusters/{cluster}/actions/chaos-isolate-start?cut=arbitrator,peer,db
```

The `db` cut makes the master unreachable too. The node wants to fail over
but cannot reach the arbitrator, so it must **not promote a new master**.
**Guards against a second failover.**

There is no third case: if the arbitrator were still reachable it would not
be a partition — the node would simply act on the arbitrator's verdict (the
normal failover path).

## What the isolated node's test proves — and what it does not

Armed on the active node, both partitions verify that node **fail-safes**
(goes standby, does not fail over, recovers on restore). They do **not**
assert that the peer takes over — that happens on the other instance and is
observed cross-instance.

## Simulating master location across two DCs

The `db` cut means "this repman lost network access to the master." To
reproduce "the node carrying repman + master fell off the network," arm the
`db` cut on the **surviving** repman via its own API — it loses the master
(that lived on the dead node) while keeping its arbitrator, so it wins the
election and fails over. Compose by DC:

- **Lost node = active repman + master:** on the surviving standby,
  `cut=db,peer`; optionally on the lost active, `cut=arbitrator,peer`.
- **Lost node = standby repman + master:** on the surviving active,
  `cut=db,peer`; optionally on the lost standby, `cut=arbitrator,peer`.

## Regtest

`testChaosIsolationArbitration` runs both partitions on the local instance
(P1 then P2), asserting fail-safe + recovery for each, then a final phase:
a real master stop must still fail over automatically (the restored 3.0
gate — the arbitrator, not the active/standby status, gates failover) with
the old master rejoining as a slave. Skipped as passed when arbitration is
not configured.
