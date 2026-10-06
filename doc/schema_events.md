# Events (Schema tab)

## What it shows
Scheduled events (`CREATE EVENT`) run SQL on a timer. In a replicated cluster you usually
want them to run on the primary only. The **Events** section shows, for every server of
the cluster:
- whether its event scheduler is ON or OFF,
- the status of every event it holds,
- the definition of an event when you ask for it.

It is read-only: it reports what the servers hold and changes nothing.

## Where to find it
Open a cluster, go to the **Schema** tab, and expand the **Events** section (below the
tables, above Cluster Logs).

You need the `db-show-status` grant, and the cluster setting `monitoring-event-status`
must be on (it is by default).

## Reading the section

### Event scheduler
The badges next to **Event scheduler** show each server's `event_scheduler`, with its role
(master or replica). The same information is under each server's column heading.

### Event status per server
One row per event, one column per server (the master first). Each cell shows the event's
status on that server, in the server's own words:

| Shown | Meaning |
|---|---|
| ✓ `ENABLED` | the event runs when the server's scheduler is ON |
| ✗ `DISABLED` | the event never runs |
| ⏸ `SLAVESIDE_DISABLED` (MariaDB) / `REPLICA_SIDE_DISABLED` (MySQL, Percona) | the event came from replication and does not run on this replica; this is the normal state on a replica |
| Missing | the server does not have this event |

`SLAVESIDE_DISABLED` and `REPLICA_SIDE_DISABLED` are the same state; MariaDB and MySQL
just name it differently.

### Finding events in a long list
The table shows 20 events per page (the page size can be changed below it). To narrow it:
- **Schema**: only the events of one schema;
- **Status**: events that are ENABLED, DISABLED or replica-side disabled on at least one
  server, or missing on at least one server;
- **Search**: part of a schema or event name;
- **Show observations only**: only the events with an observation.

The line above the table tells how many events match.

### Observations
The **Observations** column and the banner above the table point out differences:
- **missing**: another server has the event, this one does not. This can be intended (an
  event created on one server only), so it is reported, not raised as an alert.
- **ENABLED on a replica (scheduler OFF)**: the event does not run now, but it would as soon
  as the replica's scheduler is turned on.
- **ENABLED on a replica (scheduler ON)**: the event runs on the replica. This is shown in
  red and turns the banner into a warning, because it usually means the event writes on
  a replica.

A replica with its scheduler ON is not a problem by itself: events that are
`SLAVESIDE_DISABLED` / `REPLICA_SIDE_DISABLED` do not run there.

### Definitions
Click an event's status in a server's column to read it on that server. Only that event is
read, from the server at that moment (it is not part of the monitoring). The window shows:
- **Status** on that server;
- **Schedule**: when it runs, e.g. "Once, at 2030-06-01 00:00:00" or "Every 1 DAY, starting
  2030-01-01 00:00:00, until 2031-01-01 00:00:00";
- **Last executed** ("never" if it has not run on that server);
- **On completion**: whether the event is kept or dropped after its last run;
- **Time zone**, **Definer** and **Comment**;
- the event body (SQL). Use **Copy to clipboard** to copy the body.

Status and scheduler state come from the monitoring and refresh on every monitoring tick.

## From the command line
```
replication-manager-cli server --cluster=<cluster> --id=<server id> --get=events
```
prints all the events of that server with their definer and schedule (JSON). It follows safe
server-side pages while writing, so it does not need to hold the full list in memory. It uses
the same permission (`db-show-status`) and the same setting as the page. The API returns a
page of metadata for `?schema=<schema>` (or no filter), with `X-Total-Count`; add
`limit=<n>&offset=<n>` to page explicitly. `?schema=<schema>&name=<event>` reads one event,
including its SQL body.

The server never returns more than its configured `monitoring-event-status-max-definitions`
rows per list response (default 100), and refuses a named SQL body larger than
`monitoring-event-status-max-definition-bytes` (default 1 MiB) instead of truncating it.
List pages are a live view, not a transaction snapshot: if an event is created, dropped or
renamed while you page through the list, a later page can change, skip or repeat an event.

## Turning it off
Set `monitoring-event-status = false` for the cluster (or switch **Monitoring Event Status**
off in **Settings > Monitoring**). The Events section disappears and the definitions API
and CLI call are refused. This gates those user/API surfaces only; the monitoring of event
status continues with the same monitoring work.

## Read-only
The section, the API endpoint and the CLI getter only read. They do not turn the event
scheduler on or off and do not change an event's status.
