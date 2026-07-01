# Heartbeat and Arbitration Technical Documentation

## Overview

Replication-manager uses a two-level heartbeat and arbitration system to handle high-availability between two repman instances (active/DR). The system detects network partitions (split brain) and uses an external arbitrator service to elect which instance manages each cluster.

## Architecture: Two Levels

### Server Level — Peer Split Brain Detection

**File**: `server/server.go` — `Heartbeat()` and `HeartbeatPeerSplitBrain()`

The server-level heartbeat runs periodically (every `monitoring-ticker` seconds) and pings each peer repman instance via HTTP GET to `/api/heartbeat`. Its sole job is determining **split brain** — whether the peer is reachable or not.

- **Peer reachable** → `SplitBrain = false` → "In Majority"
- **Peer unreachable** → `SplitBrain = true` → "Split Brain"

The server level does NOT determine Active/Standby. It only knows about peer reachability. The `SplitBrain` state is propagated to all clusters: `cl.IsSplitBrain = repman.SplitBrain`.

**Heartbeat exchange**: Each repman exposes `/api/heartbeat` (handler in `server/http.go`) which returns:
```json
{"uuid": "...", "id": N, "secret": "...", "status": "A|S"}
```
The `status` field is `repman.Status` — currently set at startup and by the manual toggle API. The `Heartbeat` struct also has `Cluster`, `Master`, `Hosts`, `Failed` fields but these are NOT populated at the server level (only used at cluster level for arbitrator communication).

### Cluster Level — Arbitrator Reporting and Election

**File**: `cluster/cluster_split.go` — `Heartbeat()` (misleading name — should be `ArbitratorHandler` or similar)

Each cluster has its own heartbeat function that runs per-cluster in the monitor loop. **It does NOT ping the peer.** It only communicates with the **external arbitrator service**.

It runs only when `IsSplitBrain == true`:

1. **Eligibility check**: `IsEligibleForArbitration()` — requires `Cloud18GitUser != ""` AND a paid plan (support/partner/support-services). If not eligible, the cluster skips arbitration entirely and sets `IsSplitBrainBck = IsSplitBrain` (consuming the transition).

2. **Report to arbitrator**: `SetArbitratorReport()` (`cluster/cluster_set.go`) — HTTP POST to the arbitrator's `/heartbeat` endpoint with:
   - `uuid`, `secret`, `cluster`, `master` URL
   - `id` (ArbitrationSasUniqueId — identifies this repman instance)
   - `status` (cluster Active/Standby)
   - `hosts` (server count), `failed` (failed server count)

3. **Election** (only on split brain transition): `ArbitratorElection()` — HTTP POST to arbitrator's `/arbitrator` endpoint. The arbitrator runs `RequestArbitration()` which:
   - Checks if another instance is already elected (status='E' with recent heartbeat)
   - Compares failed node counts
   - Returns "winner" or "loser"
   
   Winner: `cluster.SetActiveStatus("A")` — cluster becomes Active, manages databases  
   Loser: `cluster.SetActiveStatus("S")` — cluster becomes Standby, triggers failover if master is on this side

**Election fires only once** — on the `IsSplitBrain` transition (false→true). If all 3 retry attempts fail, the election is not retried on subsequent ticks because `IsSplitBrainBck` is updated to match `IsSplitBrain`.

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

Called when a cluster reports its heartbeat. Upserts the row and checks for split brain resolution:

- Counts `DISTINCT master` in the last 10 seconds
- `count > 1` → masters diverge (true split brain) → resets all `'E'` status to `'U'`
- `count <= 1` → no divergence → keeps elected status intact

### RequestArbitration

Called when a cluster requests an election. Uses a transaction with `FOR UPDATE` (MySQL) for row-level locking:

1. Checks if another instance has status `'E'` with `date > 10 seconds ago` (filters stale rows from dead instances)
2. If no other elected instance: checks if any other instance has fewer failed nodes
3. If this instance wins: upserts row with `status='E'`

**Key fix**: The `date > 10 seconds ago` filter on the `'E'` check was added to prevent stale rows from dead instances blocking elections indefinitely.

## Known Issues and Race Conditions

### Hosts=0 Bug (Data Race)

In the cluster monitor loop, `TopologyDiscover()` and `Heartbeat()` run as **parallel goroutines**:

```go
go cluster.TopologyDiscover(wg)
go cluster.Heartbeat(wg)
```

`TopologyDiscover` calls `newServerList()` which recreates `cluster.Servers` every tick:
```go
cluster.Servers = make([]*ServerMonitor, len(cluster.hostList))
```

`Heartbeat` → `SetArbitratorReport` reads `len(cl.GetServers())` without acquiring the cluster lock. This race causes the arbitrator to receive `hosts=0` for clusters that actually have servers.

### Election Is One-Shot

`ArbitratorElection()` only fires when `IsSplitBrainBck != IsSplitBrain`. After the first tick, `IsSplitBrainBck` is updated to match, so subsequent ticks skip the election. If the election fails on all 3 retries, it's never retried until the next split brain transition (must go false→true again).

### Naming Confusion

`cluster.Heartbeat()` does NOT heartbeat — it handles arbitrator communication. The actual heartbeat (peer pinging) is `repman.HeartbeatPeerSplitBrain()` at the server level. The cluster function should be renamed to reflect its real purpose (e.g., `ArbitratorHandler()`).

## Multi-Cluster Split Brain Scenarios

When multiple clusters are monitored and a split brain occurs:

- Each cluster runs its own independent election at the arbitrator
- A cluster whose master is on the **same partition** as this repman instance → wins election → stays Active → no failover
- A cluster whose master is on the **other partition** → loses election → becomes Standby → failover triggers

This means different clusters on the same repman instance can have different Active/Standby states depending on where their masters are located relative to the network partition.

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

## Flow Diagram

```
Server Heartbeat (every tick)
    │
    ├── For each peer:
    │   └── HTTP GET /api/heartbeat
    │       ├── Success → SplitBrain = false
    │       └── Failure → SplitBrain = true
    │
    ├── Log split brain transition if changed
    │
    └── Propagate SplitBrain to all clusters
            │
            ▼
Cluster Monitor Loop (every tick, per cluster)
    │
    ├── TopologyDiscover() ──┐
    │                        ├── parallel
    └── Heartbeat()  ────────┘
         │
         └── if IsSplitBrain:
              │
              ├── Check IsEligibleForArbitration()
              │   └── No → skip, consume transition
              │
              ├── SetArbitratorReport()
              │   └── POST /heartbeat to arbitrator
              │
              └── if transition (IsSplitBrainBck != IsSplitBrain):
                   │
                   ├── Sleep 5s (let arbitrator collect both sides)
                   │
                   └── ArbitratorElection() (3 retries)
                       └── POST /arbitrator
                           ├── "winner" → SetActiveStatus("A")
                           └── "loser"  → SetActiveStatus("S") → failover
```
