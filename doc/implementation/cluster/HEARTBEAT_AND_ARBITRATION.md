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

**File**: `cluster/cluster_split.go` — `ArbitratorHandler()`

Each cluster runs its own arbitrator handler per-cluster in the monitor loop. **It does NOT ping the peer.** It only communicates with the **external arbitrator service** and only when `IsSplitBrain == true`.

#### Flow when IsSplitBrain is true:

1. **Eligibility check**: `IsEligibleForArbitration()` — requires `Cloud18GitUser != ""` AND a paid plan (support/partner/support-services). If not eligible, sets `IsSplitBrainBck = IsSplitBrain` (consuming the transition) and returns.

2. **Heartbeat**: `SetArbitratorReport()` (`cluster/cluster_set.go`):
   - Computes `IsLostMajority` via `LostMajority()` — checks if more than half of database servers in the cluster are failed
   - HTTP POST to arbitrator's `/heartbeat` endpoint with: `uuid`, `secret`, `cluster`, `master` URL, `id` (ArbitrationSasUniqueId), `status` (cluster Active/Standby), `hosts` (server count), `failed` (failed server count)
   - On failure: sets `IsFailedArbitrator = true`
   - On success: sets `IsFailedArbitrator = false`

3. **Election**: `arbitratorElection()` — runs **every tick** during split-brain (not just on transition):
   - HTTP POST to arbitrator's `/arbitrator` endpoint with: `uuid`, `secret`, `cluster`, `master`, `id`, `status`, `hosts`, `failed`
   - On failure: sets `IsFailedArbitrator = true`
   - On "winner": `SetActiveStatus("A")` — cluster becomes Active
   - On "loser": `SetActiveStatus("S")` — cluster becomes Standby, calls `LostArbitration()` if master differs from winner's master (reattaches failed master to winner's master via GTID rejoin)

4. Updates `IsSplitBrainBck = IsSplitBrain`

**Important**: Heartbeat runs BEFORE election every tick. This matters because `WriteHeartbeat` (via INSERT OR REPLACE without status column) resets `status='E'` to `status='U'`. The election then re-evaluates and writes `status='E'` for the winner. If both instances report the same master, `WriteHeartbeat` preserves the existing election status. If masters diverge (`count(distinct master) > 1`), it resets all elections to force a new one.

#### Why election runs every tick (not one-shot)

During split-brain, the election must run continuously because:
- **Master comparison**: Each tick re-checks which master each side sees — critical for detecting split-brain resolution
- **Status recovery**: If one repman instance is stopped, the other needs to re-elect on the next tick to take over as Active
- **Heartbeat destroys election status**: `WriteHeartbeat` uses INSERT OR REPLACE which resets `status='E'` back to default `'U'`, so the election must re-assert the winner every tick

#### Election transfer on peer stop

When one repman instance stops (e.g., for maintenance), the remaining instance's `ArbitratorHandler` continues running every tick during split-brain. The stopped instance's heartbeat row goes stale (date > 10 seconds ago). The `RequestArbitration` algorithm filters stale rows, so the remaining instance wins the election and becomes Active on all its clusters.

**Critical path**: `CheckFailed()` in the monitoring loop gates on `!cluster.IsActive()` — it returns early for Standby clusters. This means `isActiveArbitration()` (called from `CheckFailed`) only runs for already-Active clusters. The `ArbitratorHandler` is the path that handles election for Standby clusters during split-brain — it runs unconditionally when `IsSplitBrain == true`, regardless of Active/Standby state.

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

## Known Issues (Resolved)

### ~~Hosts=0 in Arbitrator Stats~~ (Fixed)

`isActiveArbitration()` in `CheckFailed` was posting to `/arbitrator` without `hosts` and `failed` fields, causing them to default to 0 in the arbitrator DB. Fixed by adding `hosts` and `failed` to the JSON payload.

### ~~Hosts=0 Data Race~~ (Fixed)

Previously `TopologyDiscover()` and `Heartbeat()` ran as parallel goroutines, causing `SetArbitratorReport` to read `Servers` while `newServerList()` was recreating it. Fixed by restructuring the monitor loop into phases: `TopologyDiscover` runs alone in Phase 1, `ArbitratorHandler` runs in Phase 2 after topology is stable.

### ~~Status Flapping Active/Standby~~ (Fixed)

Two writers to the same arbitrator row caused flapping: `WriteHeartbeat` (POST `/heartbeat`) resets `status` to default `'U'` via INSERT OR REPLACE (omits status column), then `RequestArbitration` (POST `/arbitrator`) sets `status='E'` for the winner. When these ran in different order or from different code paths, the status flapped. Fixed by ensuring `ArbitratorHandler` always runs heartbeat THEN election every tick, so election writes last.

### ~~Election One-Shot Per Transition~~ (Fixed)

Previously `ArbitratorElection()` only fired on the split-brain transition (`IsSplitBrainBck != IsSplitBrain`). After the first tick, `IsSplitBrainBck` was updated to match, so subsequent ticks skipped the election. This broke election transfer when one repman was stopped. Fixed: election now runs every tick during split-brain via `arbitratorElection()`.

## Known Issues (Current)

### INSERT OR REPLACE Destroys Election Status

`WriteHeartbeat` uses INSERT OR REPLACE without the `status` column, which deletes the old row and inserts a new one with `status DEFAULT 'U'`. This destroys the `status='E'` set by `RequestArbitration`. Both instances can end up with `status='E'` (stale rows) — the arbitrator stats handle this by filtering stale heartbeats (date > 10 seconds ago) to determine the real winner.

### Two Code Paths Call /arbitrator

Both `ArbitratorHandler` (via `arbitratorElection()`, runs during split-brain) and `CheckFailed` (via `isActiveArbitration()`, runs for Active clusters every tick) POST to the arbitrator's `/arbitrator` endpoint. This is by design: `ArbitratorHandler` handles elections during split-brain for both Active and Standby clusters, while `isActiveArbitration` re-validates the Active status every tick after split-brain resolves.

### Server-Level Status Not Derived from Quorum

`repman.Status` is set to Standby at startup (when arbitration enabled) and is only changed by the manual toggle API. It does not reflect the actual quorum state. The GUI Navbar shows split brain state (`"In Majority"` / `"Split Brain"`) based on `repman.SplitBrain`, but this only checks peer reachability — not arbitrator reachability. A full lost-majority state (peer AND arbitrator unreachable) is not surfaced at the server level.

### Split-Brain Badge Scope

The split-brain badge in the navbar only shows on top-level views (cluster list, global settings). When inside a cluster view, the Active/Standby badge on the cluster card is the relevant indicator — the split-brain badge is hidden to avoid stale global state confusing the per-cluster view.

## Arbitrator Stats Page

The `/stats` endpoint displays per-cluster election status with auto-refresh (10s).

### Winner Determination

The stats page mirrors the `RequestArbitration` algorithm to determine the winner:

1. **Filter stale instances**: heartbeat date older than 10 seconds is excluded (same threshold as `RequestArbitration`)
2. **Check elected status**: among non-stale instances, if one has `status='E'`, it is the winner
3. **Fewest failures fallback**: if no instance is elected, the non-stale instance with the fewest `failed` count would win (same as the `failed < ?` check in `RequestArbitration`)

### Display

- **Per-cluster header**: cluster name, "Same Master: Yes/No" (compares master URLs across instances — does NOT reveal hostnames in the public HTML), "Winner: UID X"
- **Per-instance rows**: UID, Winner/Looser status, last heartbeat timestamp, hosts count, failed count
- **Winner row**: highlighted with green accent background
- **JSON output** (`/stats?format=json`): includes master URLs and arbitration_date for programmatic use

### Security

The `/stats` endpoint is public (no auth). Master hostnames are intentionally excluded from the HTML view to avoid exposing internal infrastructure. They are included in the JSON output which can be access-controlled at the network level.

## Multi-Cluster Split Brain Scenarios

When multiple clusters are monitored and a split brain occurs:

- Each cluster runs its own independent election at the arbitrator
- A cluster whose master is on the **same partition** as this repman instance → wins election → stays Active → no failover
- A cluster whose master is on the **other partition** → loses election → becomes Standby → `LostArbitration()` rejoins master to winner

Different clusters on the same repman instance can have different Active/Standby states depending on where their masters are located relative to the network partition.

### Design Decision: Election Stays Per Cluster (2026-07-03)

A server-level election (elect once per repman pair, push the result down to
all clusters) was considered and REJECTED. What arbitration protects is each
cluster's **master**: during a partition, different masters legitimately sit
on different sides, and only a per-cluster election lets each cluster stay
active where its master is healthy. A server-level election would force
whole-side failovers on clusters whose masters are fine. Accepted
consequences: `repman.Status` is inferred from cluster outcomes, and
arbitrator election traffic scales with cluster count during split brain.

### Design Decision: Role-Free Git Config Sync (2026-07-03)

Per-cluster elections are incompatible with *role-based* git sync ("active
pushes, standby pulls"): with a mixed active/standby cluster set there is no
server-level role to gate on. The sync is therefore role-free and
change-driven:

- **Push on change, from wherever the change happened.** `GitPush` is
  ungated; since secrets keep a stable ciphertext across saves, nodes with no
  real edits push nothing.
- **Pull everywhere.** `PullActiveConfig` runs on both peers whenever
  arbitration is enabled. The pull lands in an **isolated clone**
  `WorkingDir/.config/` (same principle as `.pull/` for the BO repo) — the
  live working dir is never force-reset by a pull, so a node that is active
  for some clusters cannot lose locally saved state.
- **Apply per cluster.** Files (`<name>.toml`, `overwrite.toml`) are copied
  from `.config/<name>/` into the live tree only for clusters where this node
  is standby (`!IsActive() && GitConfigSyncStandby`) and only when the bytes
  actually differ; `ReloadStandbyConfigsFromDisk` then reloads just those
  clusters. Active clusters' files are never touched by the pull path.

The old implementation ran `git checkout --force` + `pull --force` directly
on the live working dir, gated by `HasStandbyClusterWithGitSync` (any single
standby cluster). That was destructive on mixed-role nodes and, combined with
secrets being re-encrypted on every save, produced a push/pull/reload
ping-pong between the peers every git tick.

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

See [MONITOR_LOOP_FLOW.md](MONITOR_LOOP_FLOW.md) for the full phased execution diagram of the cluster monitor tick.
