# Schema Monitor Progress Plan

## Implementation Status (Current)
- Implemented on cluster payload: `cluster.schemaMonitorProgress` with phases and counters.
- Implemented fast table list + slow per-table metadata loading (columns/indexes) with timeout and delay.
- Implemented schema cache persistence/reload per server under `monitoring-datadir/<cluster>/schema_cache/`.
- Implemented checksum-all-tables concurrency guard (REST 409 when already running).
- Implemented per-table checksum chunk progress in dict:
  - `checksum_chunk_total`
  - `checksum_chunk_current`
  - `checksum_chunk_percent`
- Implemented repair metadata storage on checksum mismatch:
  - `repair_chunk_ids`
  - `repair_where`
  - `repair_updated_at`
  - `needs_repair`
- Implemented manual REST repair action:
  - `POST /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/repair-table`
- Implemented gRPC repair action enum and handling (`REPAIR_TABLE`), currently using `server.host = "schema.table"` as request carrier.
- Current UX decision: `needs_repair` is sticky across full checksum reset and is cleared on successful checksum.
- Note: proto regeneration was not available in the current environment, so the gRPC request carrier is a temporary compatibility path.

## Goals
- Expose schema monitoring progress on the cluster payload (not state machine).
- Show table list quickly, then process columns/indexes in a slow, rate-limited background path.
- Mark the cluster data dictionary as ready only after master + optional slaves complete.
- Populate shard metadata as part of the schema monitor flow.
- Prevent concurrent checksum-all-tables API calls per cluster (return 409).

## Data Model
- Add `SchemaMonitorProgress` to `cluster.Cluster` with `json:"schemaMonitorProgress" groups:"web"`.
- Fields (suggested):
  - `Status`: idle/running/done/error
  - `Phase`: list/columns/indexes/shards/slaves
  - `Master`: processedTables, totalTables, processedBytes, totalBytes, percent
  - `Slaves`: aggregate processedTables, totalTables, processedBytes, totalBytes, percent
  - `CurrentSlave`: server URL/name
  - `StartTime`, `EndTime`, `LastError`

## Flow Changes
1. Initialize/reset progress in `Cluster.MonitorSchema()`.
   - Set `Status=running`, `Phase=list`, clear counters, set `StartTime`.

2. Fast list, slow metadata.
   - Keep the fast list query (`getAllTables`).
   - Load table columns and indexes per table with timeout (`monitoring-schema-scan-timeout`) and delay (`monitoring-schema-scan-delay-ms`).

3. Update master progress in `MonitorMasterTableSchema()`.
   - After list fetch, set totals (table count + size bytes).
   - While loading columns: update counters + percent, set `Phase=columns`.
   - While loading indexes: update counters + percent, set `Phase=indexes`.

4. Shard population phase.
   - After metadata is ready, set `Phase=shards` and run shard metadata updates.

5. Slaves (aggregate progress).
   - If `monitoring-schema-on-replicas`:
     - Set `Phase=slaves`.
     - Set `CurrentSlave` while iterating.
     - Aggregate slave progress counters and percent.

6. Completion.
   - On success: `Status=done`, `Phase=idle`, `EndTime`.
   - On error: `Status=error`, set `LastError`, `EndTime`.

## Checksum All Tables Guard
- Add a per-cluster guard (atomic or mutex-protected) for checksum-all-tables.
- In `handlerMuxClusterSchemaChecksumAllTable`:
  - If guard is set, return `409 Conflict` with a JSON message.
  - If not, set guard, run checksum in a goroutine, clear guard on completion.

## Checksum and Repair Metadata
- Full checksum run starts with a view reset for per-table sync/progress fields.
- On mismatch, table dict is flagged with `needs_repair=true` and stores failed chunk ids + predicate.
- Repair action re-checks stored chunks and updates status/progress.
- Repair-mode progress uses filtered chunk totals (selected chunks), not full table chunk count.
- `needs_repair` remains sticky across reset and is cleared only after successful checksum verification.

## UI Contract
- UI reads `cluster.schemaMonitorProgress` from existing cluster API payloads.
- Display `n of n` from `processedTables/totalTables` and percent based on bytes.

## Files to Touch
- `cluster/cluster.go` (Cluster struct, schema monitor flow)
- `cluster/cluster_schema_progress.go` (progress helpers)
- `cluster/cluster_schema_loader.go` (slow per-table schema loading)
- `cluster/cluster_schema_cache.go` (save/load table dict cache)
- `cluster/srv_schema_cache.go` (cache load hook during refresh)
- `cluster/cluster_chk.go` (checksum guard, chunk progress, repair metadata, manual repair)
- `utils/dbhelper/schema.go` (per-table metadata loaders)
- `utils/dbhelper/types.go` (repair/chunk progress fields)
- `server/api_cluster.go` (checksum 409 + repair-table endpoint)
- `server/repmanv3.go` and `repmanv3/messages.pb.go` (gRPC repair action)
