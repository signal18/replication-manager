# Heartbeat and Arbitration Technical Documentation

## Overview

Replication-manager uses a two-level heartbeat and arbitration system for high-availability between two repman instances (active/DR). The system detects network partitions (split brain), computes majority from a 3-node quorum (this repman, peer repman, external arbitrator), and uses the external arbitrator to elect which instance manages each cluster.

## Three-Node Quorum

The arbitration system forms a 3-node quorum:

1. **This repman instance**
2. **Peer repman instance** — checked via HTTP GET `/api/heartbeat`
3. **External arbitrator** — checked via HTTP POST `/heartbeat` and `/arbitrator`

Majority requires 2 out of 3 reachable. The code tracks three boolean flags per cluster:

| Flag | Meaning | Set by |
|------|---------|--------|
| `IsSplitBrain` | Peer repman unreachable | `server.HeartbeatPeerSplitBrain()` → propagated to clusters |
| `IsFailedArbitrator` | Arbitrator unreachable | `cluster.SetArbitratorReport()` and `cluster.arbitratorElection()` |
| `IsLostMajority` | More than half of database servers failed | `cluster.LostMajority()` called in `SetArbitratorReport()` |

Quorum states:

| Peer | Arbitrator | State | Action |
|------|-----------|-------|--------|
| Reachable | Reachable | In Majority | Normal operation, no election needed |
| Reachable | Unreachable | In Majority | No election possible but peer visible |
| Unreachable | Reachable | In Majority (split brain) | Election via arbitrator decides Active/Standby |
| Unreachable | Unreachable | Lost Majority | Should go Standby — isolated, unsafe to manage |

## Architecture: Two Levels

### Server Level — Peer Split Brain Detection

**File**: `server/server.go` — `Heartbeat()` and `HeartbeatPeerSplitBrain()`

The server-level heartbeat runs periodically (every `monitoring-ticker` seconds) and pings each peer repman instance via HTTP GET to `/api/heartbeat`.

- **Peer reachable** → `SplitBrain = false`
- **Peer unreachable** → `SplitBrain = true`

The `SplitBrain` state is propagated to all clusters: `cl.IsSplitBrain = repman.SplitBrain`.

**Heartbeat exchange**: Each repman exposes `/api/heartbeat` (handler in `server/http.go`) which returns:
```json
{"uuid": "...", "id": N, "secret": "...", "status": "A|S"}
```
The `status` field is `repman.Status`. The `Heartbeat` struct also has `Cluster`, `Master`, `Hosts`, `Failed` fields but these are NOT populated at the server level (only used at cluster level for arbitrator communication).

**Server-level state**: `repman.SplitBrain` (bool, exposed as `"splitBrain"` in `/api/monitor` JSON). The server level does not directly determine Active/Standby — that is computed at cluster level via elections.

### Cluster Level — Arbitrator Reporting and Election

**File**: `cluster/cluster_split.go` — `Heartbeat()` (misleading name — handles arbitrator communication, not peer heartbeating)

Each cluster runs its own arbitrator handler per-cluster in the monitor loop. **It does NOT ping the peer.** It only communicates with the **external arbitrator service** and only when `IsSplitBrain == true`.

#### Flow when IsSplitBrain is true:

1. **Eligibility check**: `IsEligibleForArbitration()` — requires `Cloud18GitUser != ""` AND a paid plan (support/partner/support-services). If not eligible, sets `IsSplitBrainBck = IsSplitBrain` (consuming the transition) and returns.

2. **Report to arbitrator**: `SetArbitratorReport()` (`cluster/cluster_set.go`):
   - Computes `IsLostMajority` via `LostMajority()` — checks if more than half of database servers in the cluster are failed
   - HTTP POST to arbitrator's `/heartbeat` endpoint with: `uuid`, `secret`, `cluster`, `master` URL, `id` (ArbitrationSasUniqueId), `status` (cluster Active/Standby), `hosts` (server count), `failed` (failed server count)
   - On failure: sets `IsFailedArbitrator = true`
   - On success: sets `IsFailedArbitrator = false`

3. **Election** (only on split brain transition `IsSplitBrainBck != IsSplitBrain`):
   - Waits 5 seconds to let arbitrator collect heartbeats from both sides
   - Calls `ArbitratorElection()` with 3 retries
   - HTTP POST to arbitrator's `/arbitrator` endpoint
   - On failure: sets `IsFailedArbitrator = true`
   - On "winner": `SetActiveStatus("A")` — cluster becomes Active
   - On "loser": `SetActiveStatus("S")` — cluster becomes Standby, calls `LostArbitration()` if master differs from winner's master (reattaches failed master to winner's master via GTID rejoin)

4. Updates `IsSplitBrainBck = IsSplitBrain`

#### State alerts in TopologyDiscover (`cluster_topo.go`):

| Alert | Condition | Meaning |
|-------|-----------|---------|
| WARN0079 | `IsSplitBrain` | Peer repman unreachable |
| WARN0080 | `IsLostMajority` | More than half database servers failed |
| WARN0090 | `IsFailedArbitrator` | Arbitrator unreachable |

## Arbitrator Service

**Binary**: built with `make arb` (build tag `arbitrator`)
**Database**: SQLite (production, tested for years) or MySQL/InnoDB (newer, less tested)
**Code**: `utils/dbhelper/arbitration.go`

### Heartbeat Table Schema

```sql
CREATE TABLE heartbeat (
    secret      VARCHAR(64),
    cluster     VARCHAR(128),
    uid         INT,           -- ArbitrationSasUniqueId (identifies repman instance)
    uuid        VARCHAR(128),
    master      VARCHAR(128),
    date        TIMESTAMP,
    arbitration_date TIMESTAMP,
    status      CHAR(1) DEFAULT 'U',  -- U=Unelected, E=Elected
    hosts       INT DEFAULT 0,
    failed      INT DEFAULT 0,
    PRIMARY KEY(secret, cluster, uid)
);
```

### WriteHeartbeat

Called when a cluster reports via `/heartbeat`. Upserts the row and checks for split brain resolution:

- Counts `DISTINCT master` in the last 10 seconds
- `count > 1` → masters diverge (true split brain) → resets all `'E'` status to `'U'`
- `count <= 1` → no divergence → keeps elected status intact

### RequestArbitration

Called when a cluster requests an election via `/arbitrator`. Uses a transaction with `FOR UPDATE` (MySQL) for row-level locking:

1. Checks if another instance has status `'E'` with `date > 10 seconds ago` (filters stale rows from dead instances)
2. If no other elected instance: checks if any other instance with status `'U'` has fewer failed nodes
3. If this instance wins: upserts row with `status='E'`

The `date > 10 seconds ago` filter prevents stale rows from dead instances blocking elections indefinitely.

### Election Decision Logic

The arbitrator decides the winner based on failed node count. The instance with **fewer failed nodes** wins. If both have the same failed count, the first to request wins (first-come-first-served within the transaction lock).

## LostArbitration — Failover on Election Loss

When a cluster loses the election AND its current master differs from the winner's master (`r.Master != mst`), `LostArbitration()` is called (`cluster/cluster.go`):

- If `ArbitrationFailedMasterScript` is configured: runs the external script with failed master host/port
- Otherwise: reattaches the failed master to the winner's master via GTID rejoin (`SetReplicationGTIDCurrentPosFromServer`)

This handles the case where the master is on the losing side of the partition — it gets demoted and rejoined to the winner's master.

## Known Issues

### Hosts=0 Data Race

In the cluster monitor loop, `TopologyDiscover()` and `Heartbeat()` run as **parallel goroutines**:

```go
go cluster.TopologyDiscover(wg)
go cluster.Heartbeat(wg)
```

`TopologyDiscover` calls `newServerList()` which recreates `cluster.Servers` every tick:
```go
cluster.Servers = make([]*ServerMonitor, len(cluster.hostList))
```

`Heartbeat` → `SetArbitratorReport` reads `len(cl.GetServers())` without acquiring the cluster lock. This race causes the arbitrator to receive `hosts=0` for clusters that actually have servers. The election logic uses the `failed` count to pick a winner, so incorrect `hosts=0` / `failed=0` can produce wrong election results.

### Election Is One-Shot Per Transition

`ArbitratorElection()` only fires when `IsSplitBrainBck != IsSplitBrain`. After the first tick, `IsSplitBrainBck` is updated to match, so subsequent ticks skip the election. If all 3 retry attempts fail, the election is not retried until the next split brain transition (must go false→true again). If `IsEligibleForArbitration()` returns false on the transition tick, the transition is consumed and the election never fires.

### Server-Level Status Not Derived from Quorum

`repman.Status` is set to Standby at startup (when arbitration enabled) and is only changed by the manual toggle API. It does not reflect the actual quorum state. The GUI Navbar shows split brain state (`"In Majority"` / `"Split Brain"`) based on `repman.SplitBrain`, but this only checks peer reachability — not arbitrator reachability. A full lost-majority state (peer AND arbitrator unreachable) is not surfaced at the server level.

### Naming Confusion

`cluster.Heartbeat()` does NOT heartbeat — it handles arbitrator communication. The actual heartbeat (peer pinging) is `repman.HeartbeatPeerSplitBrain()` at the server level.

## Multi-Cluster Split Brain Scenarios

When multiple clusters are monitored and a split brain occurs:

- Each cluster runs its own independent election at the arbitrator
- A cluster whose master is on the **same partition** as this repman instance → wins election → stays Active → no failover
- A cluster whose master is on the **other partition** → loses election → becomes Standby → `LostArbitration()` rejoins master to winner

Different clusters on the same repman instance can have different Active/Standby states depending on where their masters are located relative to the network partition.

## Configuration

| Setting | Description |
|---------|-------------|
| `arbitration` | Enable external arbitration (bool) |
| `arbitration-external` | Use external arbitrator service (bool) |
| `arbitration-peer-hosts` | Comma-separated peer repman addresses |
| `arbitration-sas-hosts` | Arbitrator service URL |
| `arbitration-sas-secret` | Shared secret for arbitrator auth |
| `arbitration-sas-unique-id` | Unique ID for this repman instance (int) |
| `arbitration-read-timeout` | Timeout for arbitrator HTTP calls (ms) |
| `arbitration-failed-master-script` | External script called on lost arbitration |

## Flow Diagram

```
Server Heartbeat (every tick)
    │
    ├── For each peer:
    │   └── HTTP GET /api/heartbeat
    │       ├── Success → SplitBrain = false (peer reachable)
    │       └── Failure → SplitBrain = true  (peer unreachable)
    │
    ├── Log split brain transition if changed
    │
    └── Propagate SplitBrain to all clusters
            │
            ▼
Cluster Monitor Loop (every tick, per cluster)
    │
    ├── TopologyDiscover() ──┐
    │   ├── newServerList()  │
    │   └── State alerts:    ├── parallel (race condition on Servers)
    │       WARN0079/80/90   │
    │                        │
    └── cluster.Heartbeat()──┘
         │
         └── if Arbitration enabled AND IsSplitBrain:
              │
              ├── IsEligibleForArbitration()?
              │   └── No → consume transition, return
              │
              ├── SetArbitratorReport()
              │   ├── Compute IsLostMajority (db server majority)
              │   ├── POST /heartbeat to arbitrator
              │   └── Set IsFailedArbitrator on failure
              │
              └── if transition (IsSplitBrainBck != IsSplitBrain):
                   │
                   ├── Sleep 5s (let arbitrator collect both sides)
                   │
                   └── ArbitratorElection() (3 retries)
                       └── POST /arbitrator to arbitrator
                           ├── "winner" → SetActiveStatus("A")
                           └── "loser"  → SetActiveStatus("S")
                                          └── if master differs:
                                              LostArbitration()
                                              (rejoin master to winner)
```
