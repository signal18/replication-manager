# Monitoring Architecture

Replication-manager continuously monitors database clusters and manages
high-availability through a multi-level heartbeat and arbitration system.

## Monitor Loop

Each cluster runs a monitor tick every `monitoring-ticker` seconds (default 2s).
The tick is organized into synchronized phases to ensure data consistency.

### Phase 1 — Topology Discovery

Rebuilds the list of database servers, detects master/slave roles, and checks
replication health. All subsequent phases depend on this completing first.

### Phase 2 — Parallel Topology Reads

Once the server list is stable, these tasks run concurrently:

- **Arbitrator handler** — reports cluster state to the external arbitrator
  and triggers elections during split brain
- **Proxy refresh** — updates proxy backends (ProxySQL, HAProxy, MaxScale)
- **App refresh** — updates application service state
- **Schema diff monitoring** — detects table/column changes (every 10 ticks)
- **Restic fetch** — refreshes backup repository metadata (every 30 ticks)
- **Variable monitoring** — detects server variable changes (every 30 ticks)

### Phase 3 — Dependent Checks

After Phase 2 completes, these tasks run concurrently:

- **Query rule monitoring** — syncs ProxySQL query rules (needs proxy refresh)
- **Traffic injection** — heartbeat/test traffic through proxies
- **Backup purge** — applies retention policy to restic snapshots (needs fetch)
- **Binlog purge** — identifies oldest binlog needed by slaves
- **Housekeeping** — credential checks, config validation, compliance,
  tool versions, disk usage, graphite metrics

### Background Tasks

Long-running I/O operations run fire-and-forget to avoid blocking the tick:

- **Schema monitoring** — full `INFORMATION_SCHEMA` scan on master/replicas
- **Orchestrator node discovery** — HTTP to OpenSVC/K8S/SlapOS APIs
- **Credential rotation** — rotates replication/monitoring passwords
- **Template refresh** — fetches app template checksums from orchestrator
- **Snapshot reconciliation** — reconciles backup metadata with disk state

Each background task has a reentry guard preventing concurrent execution.

### State Machine

At the end of every tick:

1. **StateProcessing** — compares previous and current states to detect
   resolved and newly raised alerts
2. **CheckAlert** — fires notifications (email, Slack, webhook) for
   state transitions
3. **ClearState** — rotates current state to old state, preparing for
   the next tick

Alerts raised by checks use `SetState()`. For checks that run periodically
(every N ticks), `PreserveState()` keeps their alerts visible on intermediate
ticks.

## Split Brain Detection

Split brain detection operates at two levels:

### Server Level

Each repman instance pings its peers via HTTP (`/api/heartbeat`). If the peer
is unreachable, `SplitBrain` is set to `true` and propagated to all clusters.

The Navbar displays the server-level state:
- **In Majority** (green) — peer is reachable
- **Split Brain** (red, blinking) — peer is unreachable

### Cluster Level

When split brain is detected, each cluster independently communicates with the
external arbitrator service to determine which repman instance manages it.

The system forms a **3-node quorum**: this repman, peer repman, and the
external arbitrator. Majority requires 2 out of 3 reachable.

| Peer | Arbitrator | State | Action |
|------|-----------|-------|--------|
| Reachable | Reachable | In Majority | Normal operation |
| Reachable | Unreachable | In Majority | No election possible |
| Unreachable | Reachable | Split Brain | Arbitrator elects Active/Standby |
| Unreachable | Unreachable | Lost Majority | Unsafe to manage |

Per-cluster alerts:

| Alert | Meaning |
|-------|---------|
| WARN0079 | Peer repman unreachable (split brain) |
| WARN0080 | More than half of database servers failed (lost majority) |
| WARN0090 | Arbitrator service unreachable |

## Arbitrator Elections

When a split brain transition is detected (peer becomes unreachable):

1. Each cluster reports its state to the arbitrator (server count, failed count,
   current master URL)
2. After a 5-second grace period (letting both sides report), the cluster
   requests an election
3. The arbitrator picks the instance with **fewer failed nodes** as winner
4. Winner becomes Active, loser becomes Standby
5. If the loser's master differs from the winner's, the loser's master is
   rejoined to the winner's master via GTID

Elections are one-shot per split brain transition — if all retry attempts fail,
the election is not retried until the next transition.

## Configuration

| Setting | Description |
|---------|-------------|
| `monitoring-ticker` | Monitor loop interval in seconds (default 2) |
| `arbitration` | Enable arbitration (bool) |
| `arbitration-external` | Use external arbitrator service (bool) |
| `arbitration-peer-hosts` | Peer repman addresses |
| `arbitration-sas-hosts` | Arbitrator service URL |
| `arbitration-sas-secret` | Shared secret for arbitrator auth |
| `arbitration-sas-unique-id` | Unique ID for this repman instance |
| `arbitration-failed-master-script` | Script called on lost arbitration |

See [HEARTBEAT_AND_ARBITRATION.md](implementation/cluster/HEARTBEAT_AND_ARBITRATION.md)
for implementation details and [MONITOR_LOOP_FLOW.md](implementation/cluster/MONITOR_LOOP_FLOW.md)
for the full phased execution diagram.
