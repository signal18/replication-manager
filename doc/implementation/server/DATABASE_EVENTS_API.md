# Server: Database Events (read-only)

## Scope
This document covers the read-only view of scheduled events (`CREATE EVENT`) across
the servers of a cluster: one API endpoint, one CLI getter and the **Events** section
of the cluster **Schema** tab. It only reads what the servers hold: it does not turn the
event scheduler on or off and does not change an event's status.

## Two sources: monitored status, on-demand definitions
- **Status (monitored).** Every monitoring tick, `ServerMonitor.EventStatus` is read by
  `dbhelper.GetEventStatus` (`cluster/srv.go`) and published in the server JSON as
  `eventStatus` (schema, name, definer, numeric status), next to `eventScheduler`
  (`HaveEventScheduler`). This collection is unchanged. The status matrix is built in the browser from
  these two fields of every server; no new collection was added.
- **Definitions (on demand).** An event body can be large and is only needed when an
  operator looks at it, so it is never collected by the monitoring loop.
  `dbhelper.ListEvents` returns a bounded metadata page (identity, definer, schedule and
  body size, but no SQL body); `dbhelper.GetEventDefinition` returns one named event with
  its body after checking its byte size. Its body query reads no more than the configured
  byte cap plus one byte, even if the event changes after the metadata query. Both use bound parameters and
  `scanContext(defaultSchemaScanTimeout)` (5 s), so a metadata lock cannot hold a request.

## API
`GET /api/clusters/{clusterName}/servers/{serverName}/events[?schema=<schema>[&name=<event>][&limit=<n>&offset=<n>]]`

Without `name`, it returns a bounded metadata page and sets `X-Total-Count` to the number
of matching events. `schema` limits the page to one schema. `schema` + `name` returns one
event including its SQL body. The GUI always asks for one event, so the size of a server's
event list does not matter to a click.

List counts and pages are independent, live reads rather than a transaction snapshot. If an
event is created, dropped or renamed while a client follows offsets, a later page can change,
skip or repeat an event; the CLI still terminates on an empty page or once it reaches the
reported total.

| Case | Response |
|---|---|
| list success | `200`, ordered JSON array of `dbhelper.EventDefinition` metadata `{db, name, definer, definitionBytes, eventType, executeAt, intervalValue, intervalField, starts, ends, onCompletion, lastExecuted, timeZone, comment}` and `X-Total-Count`; `definition` is empty |
| named-event success | `200`, one matching event with the same metadata and its SQL `definition` body |
| `name` without `schema` | `400 name requires schema` |
| invalid `limit` / `offset`, or `limit` over `monitoring-event-status-max-definitions` | `400` |
| a method other than `GET` | `405 Method Not Allowed` |
| named event body larger than `monitoring-event-status-max-definition-bytes` | `413`; the full body is never read or returned (a concurrent update is capped at the limit plus one byte) |
| `monitoring-event-status` off for the cluster | `403 monitoring-event-status is disabled for this cluster` |
| caller lacks `db-show-status` | `403 No valid ACL` |
| unknown cluster / server | `500 Cluster Not Found` / `Server Not Found` |
| server down | `503 Server is down` |
| query error or timeout | `500 Could not read events: ...` (also logged through `LogSQL`) |

Handler: `handlerMuxServerEvents` in `server/api_database.go`; server methods
`ServerMonitor.ListEventDefinitions` and `ServerMonitor.GetEventDefinition` in
`cluster/srv_get.go`. The feature gate is checked
right after the cluster ACL, before the server is looked up or any query runs.

### ACL
`cluster/cluster_acl_rules.go` maps the full path
`/api/clusters/*/servers/*/events` to `db-show-status` (the grant that already shows server
status). Rules without `*` retain their legacy substring matching; this rule uses full
`path.Match` semantics, so it cannot grant a future `/events/actions/...` route.
`TestEventsACL` covers both the intended path and non-matching sibling paths.

### CLI
`replication-manager-cli server --cluster=<cluster> --id=<server id> --get=events` follows
the bounded list pages and streams one JSON array to stdout without retaining the whole
result in memory: same ACL, same feature gate, no second data path.

## Feature switch (T14)
`monitoring-event-status` (`MonitorEventStatus`, cluster scope, default `true`, reloadable,
switchable from the Monitoring settings page or `settings/actions/switch/monitoring-event-status`).

When off:
- the Events section is not rendered (absent, not just empty);
- the definitions endpoint and the CLI getter return 403.

It does **not** stop the `eventStatus` collection: the flag gates the feature added here,
not the existing monitoring.

The companion bounds are cluster-scoped and reloadable:

- `monitoring-event-status-max-definitions`: maximum list rows per response (default 100);
- `monitoring-event-status-max-definition-bytes`: maximum SQL body for one named event
  (default 1 MiB).

Both fall back to their safe defaults for a zero or negative legacy/TOML value; zero never
means unbounded.

## Status values and labels
`GetEventStatus` returns the ordinal of the status enum (`status+0`):

| Value | MariaDB (`mysql.event.status`) | MySQL / Percona 8 (`information_schema.EVENTS.STATUS`) |
|---|---|---|
| 1 | `ENABLED` | `ENABLED` |
| 2 | `DISABLED` | `DISABLED` |
| 3 | `SLAVESIDE_DISABLED` | `REPLICA_SIDE_DISABLED` |

On MySQL 8, `information_schema.EVENTS.STATUS` is reported as `varchar(21)` but the view
is over the data-dictionary enum, so `status+0` still returns 1/2/3. This was verified on
MySQL-family 8.4 and MariaDB 10.11 before relying on it; no SQL change was needed. The API
and the JSON keep the numbers; the GUI shows the label each server family uses
(`eventsMatrix.js`, `eventStatusLabel`, chosen by `dbVersion.flavor`).

## GUI: Events section of the Schema tab
`share/dashboard_react/src/Pages/Shards/Events/` (rendered by `Pages/Shards`, the cluster
Schema tab), shown to users with `db-show-status` while the switch is on.

- `eventsMatrix.js` (pure, unit-tested by `npm run test:events-matrix`) builds one row per
  event seen on any server and one column per server (master first), with a cell per
  server and the row's observations.
- Observations are derived only from the monitored data:
  - **missing**: other servers have the event, this one does not;
  - **enabled-on-replica**: `ENABLED` on a replica whose scheduler is OFF (it would run if
    the scheduler were turned on);
  - **running-on-replica**: `ENABLED` on a replica whose scheduler is ON (it runs there).
  A `SLAVESIDE_DISABLED` / `REPLICA_SIDE_DISABLED` event on a replica is the expected
  state and is not flagged, whatever the scheduler.
- The banner is informational when there are only missing / enabled-on-replica
  observations, and a warning only when an event runs on a replica.
- Long lists: the matrix is paginated (`DataTable`, 20 rows per page by default) and
  filtered by schema (select), status (ENABLED, DISABLED, replica-side disabled, missing on
  a server), a search on schema or event name, and "observations only"
  (`filterEventRows`, `eventSchemas` in `eventsMatrix.js`).
- A definition is fetched when the operator clicks a server's cell, for that event only
  (`?schema=&name=`); nothing is fetched on tab open or by the page refresh loop, and
  nothing is cached in the Redux store.
- The modal shows the status on that server, the schedule as one line (`eventSchedule`:
  "Once, at …" or "Every n UNIT, starting …, until …"), the last execution, what happens on
  completion (`eventOnCompletion`), the time zone, the definer and the comment, then the
  body. The body is rendered by React as text in a `<pre>` (`DefinitionModal`), never as
  HTML: an event body is SQL written by database users.

## Why "missing" is not a fault
Events replicate like other DDL, so servers usually hold the same set, but a difference
can be legitimate: an event created with `sql_log_bin=0` on purpose, a replica outside
the replicated schemas, an event dropped on one node on purpose. The page therefore reports
a difference without raising a cluster state or alert.

## Tests
- Regtest `testEventsReadOnlyAPI` (`regtest/test_events_readonly_api.go`), run by name on a
  real cluster: `/api/clusters/<cluster>/tests/actions/run/testEventsReadOnlyAPI`. It goes
  through repman's own API (hooks in `server/regtest_api.go`: a token issued as
  `/api/login` does for a local API user, HTTPS on the API address) and the real CLI.
  It creates two schemas, one with a quote in its name, and three events scheduled at
  the end of 2037 on the master, and checks:
   - list pages omit SQL bodies, carry `X-Total-Count`, and use `limit`/`offset`; the named
     event response includes its definition and schedule (one-time, at 2037-12-31, ON
     COMPLETION PRESERVE);
   - `?schema=`, `?schema=&name=`, paging, the quoted schema (bound parameters), an unknown
     schema (`200 []`), `?name=` alone (`400`), over-limit pages (`400`) and non-GET
     requests (`405`);
  - the server JSON carries `eventScheduler` and `eventStatus` (master 1/2, replica 3 for
    the enabled event);
  - `replication-manager-cli server --get events` returns the same events as the API;
  - with `monitoring-event-status` off the endpoint answers 403 and the CLI fails with the
    same message; switched back on, the endpoint answers again.
  The schemas are dropped and the switch restored whatever happens. It changes no event,
  scheduler or topology. Passed on MariaDB 10.11 and Percona Server 8.4.
- `utils/dbhelper`: `TestListEvents` and `TestGetEventDefinition` (paged list metadata,
  schedules, named bodies, missing events and size limit).
- `cluster`: `TestEventsACL`.
- `share/dashboard_react`: `npm run test:events-matrix` (labels per flavor, observations,
  matrix build, filters, a 5000-event matrix, schedule and on-completion text).
