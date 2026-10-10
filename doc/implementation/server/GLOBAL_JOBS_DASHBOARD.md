# Server: Global Jobs Dashboard

## Scope
This document covers the read-only global jobs aggregate added for the dashboard. It exposes one server endpoint and one GUI section that let an operator see cross-cluster job activity without opening every cluster's Maintenance page.

## Why this exists
- Operators needed a fleet-wide view of what is active now and what just finished.
- The per-cluster Maintenance page already had the data, but only one cluster at a time.
- Frontend fan-out to every cluster would create many HTTP requests every refresh and scale poorly as cluster count grows.

## Backend API
- New endpoint: `GET /api/global/jobs`
- File: `server/api_global.go`
- Route registration: `server/http.go`

The response contains three arrays:
- `runningJobs`
- `recentCompletedJobs`
- `resticCurrentTasks`

## Aggregation model
The endpoint snapshots `repman.Clusters` under the repman lock, then iterates the snapshot. This avoids races with dynamic cluster add/remove while the global dashboard polls.

For each cluster, the handler:
1. resolves the caller identity from the JWT once,
2. checks whether the caller could already access the equivalent per-cluster endpoint,
3. includes only the sections the caller is allowed to see,
4. reuses existing cluster data paths rather than introducing new job/restic execution logic.

## ACL behavior
Global visibility is gated by `global-admin-show`, but that grant alone does **not** authorize every cluster's job data.

Per cluster, the aggregate probes the same access the caller would need for:
- `/api/clusters/{cluster}/jobs`
- `/api/clusters/{cluster}/restic/task-current`

Expected denials are checked with `IsValidACLQuiet`, which preserves the normal ACL decision but suppresses log noise during regular dashboard polling.

## Job semantics
### Active jobs
The dashboard intentionally treats these DB job states as active:
- `0` = Init
- `1` = Running
- `2` = Halted

This matches the operational goal: show work that is still in flight or needs attention, not only tasks already executing.

### Recently done jobs
The dashboard treats these states as completed history:
- `3` = Done
- `4` = Success
- `5` = Error
- `6` = PTError

Rules:
- keep the last `5` completed DB jobs per cluster,
- then sort the combined cross-cluster list by `end DESC`,
- so the final view is globally newest-first.

### Stable row order
`entries.Servers` is a map, so active-job iteration order is not stable by default. The aggregate sorts `runningJobs` by:
1. `clusterName`
2. `serverUrl`
3. `task`

This avoids distracting row shuffling on each poll.

## Restic semantics
The dashboard exposes only the **current** restic task per cluster.

It does **not** claim durable recent restic history. The backend only retains a completed/failed current task briefly before clearing it, so a stable "recently done restic tasks" history would require separate persistence and was intentionally left out of this feature.

## Frontend wiring
- Service: `share/dashboard_react/src/services/globalClustersService.js`
- Redux state/thunk: `share/dashboard_react/src/redux/globalClustersSlice.js`
- Poll trigger: `share/dashboard_react/src/Pages/Home/index.jsx`
- GUI component: `share/dashboard_react/src/Pages/GlobalItems/GlobalJobs.jsx`

The global dashboard now fetches one aggregate payload instead of issuing one jobs request and one restic request per cluster from the browser.

## GUI sections
The dashboard renders three sections:
- **Active Jobs**
- **Recently Done**
- **Current Restic Tasks**

## Validation expectations
At minimum, validate:
- only authorized clusters appear,
- Active Jobs shows states `0/1/2`,
- Recently Done is globally newest-first,
- Current Restic Tasks matches the per-cluster Maintenance view,
- the endpoint remains one request per global dashboard refresh.
