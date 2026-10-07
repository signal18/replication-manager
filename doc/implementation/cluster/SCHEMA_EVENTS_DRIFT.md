# Scheduled database events in the schema drift detection

Issue #1894. User doc: `doc/schema_events.md`.

The scheduled database events (MySQL/MariaDB `EVENT` objects) are another object
category of the schema diff: collected by the schema scan, compared master vs
replica, reported in `WARN0164` on `SchemaStateMachine`, and shown by an
observational view, `GET /api/clusters/{clusterName}/schema/events`.

## Flow

```
MonitorSchema (scheduler / on demand)
  └─ MonitorEventSchema                       cluster/schema_events.go
       └─ collectEventSchema(server)          master; replicas with monitoring-schema-on-replicas
            └─ dbhelper.GetEventChecksums     information_schema.EVENTS, body as server-side MD5
       → ServerMonitor.eventSchema            atomic.Pointer[EventSchema], immutable snapshot
       ↔ eventschema.json                     SaveInfos / ReloadSaveInfosVariables, as dicttables.json

MonitorTableSchemaDiff (every 10 ticks)
  └─ CompareSchemaBetweenMasterAndSlave       tables (unchanged)
  └─ eventSchemaDiffLines(sl)                 DBG log per drift, one aggregated line
       └─ CompareEventSchemaBetweenMasterAndSlave
            └─ compareEventSchema             the only implementation of the drift rules
  → WARN0164@replica (SchemaStateMachine, preserved on the other ticks)

GET /schema/events
  └─ GetEventSchemaView                       same CompareEventSchemaBetweenMasterAndSlave; no SQL, no state
```

## Decisions

- **One comparison (T2).** `compareEventSchema` is pure; `WARN0164` and the view
  both go through `CompareEventSchemaBetweenMasterAndSlave`. The UI only lays
  the view out (`Pages/Shards/Events/eventsMatrix.js`); it never compares.
- **Same domain and signal as tables (T3/T5).** No new state, state machine or
  key: the event drift is one more line in the replica's `WARN0164`, so the
  existing `PreserveState("WARN0164")` keeps it flap-free. Each line is
  aggregated per drift kind and capped at 10 names (`eventDriftMaxNames`) (T4).
- **Same exposure as tables.** Per-server observations stay internal
  (`eventSchema` is unexported, like `Tables`/`DictTables` are `json:"-"`); the
  API is a derived cluster-level view next to `/schema`, whose shape is
  unchanged.
- **Observation is explicit (F5).** `EventSchema.Collection` is `checked`,
  `unavailable` or `unsupported`; no snapshot is `not-checked`. A comparison
  with a side other than `checked` is `not-checked` with no drift, so a failed
  read never produces `missing`. `GetEventChecksums` returns no list on error,
  never an empty one.
- **Collection failures are logs, not states.** A failed read is logged WARN
  (`LogSQL`, general module as the rest of the schema scan) and the server is
  `unavailable` until the next scan. The table scan raises no collection-error
  state either; the scan runs on the schema scheduler, so it cannot flap per tick.
- **Kept across restarts, as tables.** `SaveInfos` writes the snapshot to
  `eventschema.json` next to `dicttables.json` (`SaveEventSchema`; the file
  is removed when there is no snapshot) and `ReloadSaveInfosVariables`
  restores it at startup (`ReloadEventSchema`), with its original
  `collectedAt`. The file keeps the definer (the API does not), so a reloaded
  snapshot compares with a fresh one without a false `definer` drift. Nothing
  is restored with `monitoring-schema-events` off. `eventschema.json` is in
  `.gitignore` with `dicttables.json`.
- **Concurrency.** The scan, the 10-tick diff and the API run in different
  goroutines: a snapshot is stored whole through `atomic.Pointer` and never
  modified afterwards.
- **Bounded pages and snapshot (F3/F4/T18).** Like `GetTables`, the scan keeps
  a short event summary only; the body is never read, only its MD5.
  `GetEventChecksums` fetches ordered `information_schema.EVENTS` rows with
  `(EVENT_SCHEMA, EVENT_NAME)` keyset pagination. The query page is bounded by
  `monitoring-schema-events-page-size` (default 1000, maximum 10000) and the
  complete retained snapshot by `monitoring-schema-events-max` (default and
  maximum 10000). Once the snapshot is full, one additional ordered row checks
  whether the server exceeds its cap. An over-limit server returns no list and
  is `unavailable`, never partially compared or saved. One
  `monitoring-schema-scan-timeout` context covers the entire loop; an error,
  scan error or deadline on any page has the same all-or-nothing outcome, so
  partial data can never create missing/extra drift or fill repman's disk.
- **Concurrent DDL.** `information_schema.EVENTS` does not provide one
  transaction snapshot across the keyset-page queries. The database column
  collation orders each `(EVENT_SCHEMA, EVENT_NAME)` page boundary; Go ordering
  is not involved. An event created, dropped or altered between pages can make
  one scan transiently inconsistent, and the next schema scan reconciles it.
- **Unavailable is visible, not drift.** `unavailable` remains outside
  `WARN0164`, because it means no trustworthy observation rather than a schema
  difference. The dashboard gives it a low-severity warning naming the affected
  servers and states that their consistency is unknown; the matrix headers and
  cells remain `not checked` rather than `missing`.
- **Off-switch (T14).** `monitoring-schema-events`: off, the scan drops the
  snapshots, the diff adds no line and the view answers `enabled: false`.

## Boundary with the failover event handling

The HA path is untouched and independent: `dbhelper.GetEventStatus` still fills
`ServerMonitor.EventStatus` with the raw status ordinal on every tick, whatever
`monitoring-schema-events` is; `failoverEnableEventScheduler` (and the vmaster
path) still read it to enable the status-3 events with `dbhelper.SetEventStatus`.
The schema path has its own query (`GetEventChecksums`), its own snapshot
(`eventSchema`) and its own status classes (`EventStatusClass`); neither path
reads the other's data, and no failover code depends on `EventSchema`.
Regression: `TestSchemaEventsLeaveFailoverEventHandlingAlone` (off, collection
failure, unsupported, drift: same raw `EventStatus`, same promotion SQL),
`TestGetEventStatusKeepsRawStatus`, and the regtest
`testSchemaEventsFailoverIsolation` (two live switchovers, schema events off then
on with a drift reported in WARN0164: the drift does not block the promotion,
and the promoted master enables the event and the scheduler). Its events use an
explicit `localhost` definer, which the failover event handling accepts.

## Hash

`dbhelper.HashEventDefinition`: CRC64 ECMA (the polynomial of `GetTables`) over,
in this order, NUL-separated:

`EVENT_TYPE`, `EXECUTE_AT`, `INTERVAL_VALUE`, `INTERVAL_FIELD`, `STARTS`, `ENDS`,
`ON_COMPLETION`, `SQL_MODE`, `TIME_ZONE`, and the body as
`MD5(CONVERT(EVENT_DEFINITION USING utf8mb4))`, computed by the server: the body
never leaves the database, and converting to utf8mb4 first makes MariaDB and
MySQL hash the same text alike. Every server runs the same query, so both sides
of a comparison use the same scheme.

Left out: `STATUS` (compared by class), `DEFINER` (compared on its own, so the
drift says *definer* rather than an opaque *definition*), `ORIGINATOR`
(server_id, differs on every node), `CREATED`, `LAST_ALTERED`, `LAST_EXECUTED`,
`EVENT_COMMENT`, `CHARACTER_SET_CLIENT`, `COLLATION_CONNECTION`,
`DATABASE_COLLATION` (client/session metadata). No SQL normalization: the
server stores the body as written and a dump reloads it as is; the NUL
separator keeps a value from shifting between fields. Map keys are a
`(schema, name)` struct, not `schema.name`, since either may contain a dot.

## Status classes

`dbhelper.EventStatusClass`: `ENABLED` → `active`; `DISABLED` → `disabled`;
`SLAVESIDE_DISABLED` (MariaDB) / `REPLICA_SIDE_DISABLED` (MySQL) →
`replica-side-disabled`; anything else → `unknown`.

A replica sets every replicated event to replica-side disabled whatever its
status on the master (MySQL documents it; lab-verified on MariaDB 10.11 and
Percona 8.4: an ENABLED and a DISABLED master event are both
`SLAVESIDE_DISABLED`/`REPLICA_SIDE_DISABLED` on the replica). The replica-side
class therefore carries no information about the master's status, and
`compareEventSchema` reports a `status` drift only when both sides have an
explicit class that differs: `replica-side-disabled` on either side is never a
drift. Consequence: the comparison cannot tell, from a replica, whether a
master event is ENABLED or DISABLED.

## Engines

`information_schema.EVENTS` on every MariaDB and MySQL/Percona version
(lab-verified on MariaDB 10.11 and Percona 8.4). It lists the events of the
schemas where the user has `EVENT`; no stronger privilege is needed.
PostgreSQL: `GetEventChecksums` returns `ErrEventsUnsupported` without a query;
the server is `unsupported` and never compared. pg_cron / pgAgent are not
modelled.

## ACL

`{"/schema/events", db-show-schema}` in `clusterACLRules`. `matchACLRules`
tries the most specific rule first and falls back to the shorter ones, so the
existing `{"/schema", cluster-sharding}` also grants it; this is intended and
tested (`TestSchemaEventsACL`): db-show-schema reads `/schema/events` but not
`/schema`; cluster-sharding reads both; neither grant is denied.

## Tests

- `utils/dbhelper/schema_test.go`: `TestGetEventChecksums` (status class,
  body only as server-side MD5, error returns no list, PostgreSQL unsupported),
  `TestGetEventChecksumsPages` (keyset traversal) and
  `TestGetEventChecksumsPageFailureReturnsNoList` (all-or-nothing pages),
  `TestGetEventChecksumsLimitExceededReturnsNoList` and
  `TestGetEventChecksumsExactLimitSucceeds` (bounded snapshot),
  `TestHashEventDefinition`, `TestEventStatusClass`.
- `config/config_events_legacy_test.go`: unreleased legacy
  `monitoring-event-status*` TOML keys are ignored while supported settings
  still decode.
- `server/api_cluster_test.go`: page-size dynamic-setting validation.
- `cluster/schema_events_test.go`: every drift kind, not-checked is never
  missing, `WARN0164` line format and cap, the view (no definer in the JSON,
  CRC as string), the off-switch, the save/reload round trip
  (`TestEventSchemaSaveReload`), dotted names (`TestCompareEventSchemaDottedNames`).
- `cluster/cluster_acl_test.go`: `TestSchemaEventsACL`.
- `eventsMatrix.test.js`: display helpers.
- `cluster/schema_events_failover_test.go`: the failover boundary (above).
- Regtest `testSchemaEventsFailoverIsolation` (run by name): the failover boundary, live.
- Regtest `testSchemaEventsDrift` (run by name): replicated events consistent,
  then replica-only drop / body change / disable / add → each drift kind in the
  view and in `WARN0164`, no body in either, off-switch.

## Limitations

- An event the monitoring user cannot see (no `EVENT` on its schema) is absent
  from the collection; with different privileges per server it is reported
  `missing`/`extra`.
- A replica is compared with the master only: replicas are not compared with
  each other, and servers outside the master/replicas are not shown.
- `unavailable` stays until the next schema scan.
- Across engines, the checksum inputs of the same event are identical
   (lab-verified, MariaDB 10.11 vs Percona 8.4: schedule formatting, time zone,
   body MD5), except `SQL_MODE`: an event takes the creating session's mode, and
   the engines' defaults differ (MariaDB adds `NO_AUTO_CREATE_USER`). Events
   created with each engine's default mode on a MariaDB/MySQL pair compare as a
   `definition` drift. The hash deliberately uses the exact `CAST(... AS CHAR)`
   values returned for `STARTS` and `EXECUTE_AT`; a mixed-version pair whose
   rendered schedule values differ can therefore also report a definition drift.
