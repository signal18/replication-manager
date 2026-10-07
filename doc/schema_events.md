# Scheduled database events in the schema drift detection

replication-manager compares the **scheduled database events** of a cluster
(the MySQL / MariaDB `EVENT` objects run by the event scheduler) between the
master and its replicas, as part of the schema drift detection. It answers one
question: *will the replicas run the same scheduled jobs as the master after a
failover?*

Setting: `monitoring-schema-events` (default `true`). It works inside the
schema monitoring: it needs `monitoring-schema-change`, and replicas are only
compared with `monitoring-schema-on-replicas`.

`monitoring-schema-events-page-size` controls how many event metadata rows one
database query can return (default `1000`, maximum `10000`).
`monitoring-schema-events-max` is the always-on maximum number of events kept
for one server (default and maximum `10000`). If another event exists after the
maximum, replication-manager discards the whole scan and marks the server
**unavailable** rather than comparing a partial list. A timeout or error on any
page has the same result. This bounds replication-manager memory and its
`eventschema.json` cache.


## What it reports

For each replica, compared with the master:

| Drift        | Meaning                                                               |
|--------------|-----------------------------------------------------------------------|
| `missing`    | the event exists on the master, not on the replica                    |
| `extra`      | the event exists on the replica, not on the master                    |
| `definition` | same name, different definition (schedule, body, SQL mode, time zone) |
| `definer`    | same name, different definer (the account the event runs as)          |
| `status`     | ENABLED on one side, DISABLED on the other                            |

A replica gives every replicated event the replica-side disabled status
(`SLAVESIDE_DISABLED` on MariaDB, `REPLICA_SIDE_DISABLED` on MySQL), whether the
event is ENABLED or DISABLED on the master: the replica cannot show the master's
status. A replica-side disabled event is therefore never a `status` drift; a
`status` drift is reported only when both servers carry an explicit status
that differs (for example ENABLED on the master, DISABLED on the replica). The
API shows the status as `active` (ENABLED), `disabled` (DISABLED),
`replica-side-disabled` or `unknown`.

Example:

```
db1 (master): app.cleanup_history, app.refresh_stats
db2 (replica): app.refresh_stats (a different body)
```

is reported as:

```
WARN0164 Cluster db2:3306 has schema differences between master and slaves:
Events differ on slave db2:3306 -> missing: app.cleanup_history; definition: app.refresh_stats
```

## Where it is reported

- **Schema warnings (`WARN0164`)**: the drift of a replica is one line of its
  schema-difference warning, next to the table differences, with at most ten
  event names per drift kind. This is the operator signal: alerts come from it.
- **Schema tab, "Scheduled Database Events (consistency)"**: a matrix of every
  event on every server, with its status and the start of its definition
  checksum, and the drift kinds per event. Filters by schema, name and drift.
- **API**: `GET /api/clusters/{clusterName}/schema/events` (below).

## When it is collected

With the rest of the schema: by the schema scan (`monitoring-schema-scheduler`
cron, or an on-demand schema scan), not on every monitoring tick. The
comparison is refreshed every 10 ticks from the last scan. The events of the
last scan are kept across a replication-manager restart (like the table
schema), with the time they were collected.

The event catalogue is read in ordered pages, not in a cross-page database
snapshot. An event created, dropped or altered while a scan is running can be
observed transiently; the next schema scan reconciles it.

## Not checked is not missing

A server whose events could not be read at the last scan (server down, access
denied, timeout) is
**unavailable**: it is not compared, and none of its events is reported missing
or extra. Until its first scan a server is **not checked**. The Schema tab shows
both, per server. An **unavailable** server also produces a warning in the
Scheduled Database Events matrix: its event consistency is unknown, not
drift-free.

## What it does not do

It is not a browser of database code. replication-manager never shows or
returns the body of an event, its schedule, its comment or its definer: the
definitions are compared by a checksum. The database computes the checksum of
each body itself (MD5), so the body never leaves the database server.

## API

`GET /api/clusters/{clusterName}/schema/events`, grant `db-show-schema` (the
grant of the other read-only schema data). A user with `cluster-sharding` also
reads it, through the `/schema` access rule.

```json
{
  "enabled": true,
  "servers": [
    { "id": "1234", "url": "db1:3306", "isMaster": true, "collection": "checked", "collectedAt": 1759800000 },
    { "id": "5678", "url": "db2:3306", "isMaster": false, "collection": "checked", "collectedAt": 1759800001, "comparison": "different" }
  ],
  "events": [
    {
      "db": "app",
      "name": "cleanup_history",
      "nodes": {
        "1234": { "present": true, "status": "active", "definitionCrc64": "9182736455463728190" },
        "5678": { "present": false }
      },
      "drifts": [ { "serverId": "5678", "drift": "missing" } ]
    }
  ]
}
```

- `collection`: `checked`, `unavailable`, `unsupported` (PostgreSQL) or
  `not-checked`.
- `comparison` (replicas): `consistent`, `different` or `not-checked`.
- `nodes` lists only the checked servers: a server absent from `nodes` was not
  checked.
- `definitionCrc64` is a string: a 64-bit value does not fit a JavaScript number.
- With `monitoring-schema-events` off: `{"enabled": false, "servers": [], "events": []}`.

## Engines and privileges

| Engine            | Support                                                    |
|-------------------|------------------------------------------------------------|
| MariaDB           | yes                                                        |
| MySQL, Percona    | yes                                                        |
| PostgreSQL        | not supported (`unsupported`): it has no `EVENT` objects; jobs of extensions such as pg_cron are not compared |

The events are read from `information_schema.EVENTS`. It only lists the events
of the schemas where the monitoring user has the `EVENT` privilege (granted
globally, or by `ALL PRIVILEGES`). Give the monitoring user the same privileges
on every server: an event it cannot see on one server looks absent there.

When a MariaDB and MySQL/Percona server use different `SQL_MODE` defaults, the
same event can correctly appear as a `definition` drift: event metadata retains
the creating session's SQL mode. In particular, MariaDB can include
`NO_AUTO_CREATE_USER` where MySQL/Percona does not. The exact database-rendered
`STARTS` and `EXECUTE_AT` values are also compared, so mixed-version formatting
differences can appear as definition drift. Create the event with matching
explicit SQL mode and verify the schedule representation on both sides when
cross-engine equality is required.

## Upgrade note

The previously proposed event-definition browser API, CLI commands and
`monitoring-event-status*` settings existed only on unreleased `develop`; they
were never part of a released compatibility surface. Existing TOML files that
still contain those old settings continue to load, but the keys are ignored.
