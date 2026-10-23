# Monitoring through MariaDB `extra_port` (#2150)

## Why

`db-servers-hosts` used to give one port to two jobs: repman's own SQL connection and the
port repman writes into the proxies. An operator who moved the monitoring to MariaDB's
`extra_port`, so that repman can still connect when `max_connections` is reached, also
moved the application traffic there:
- In HAProxy runtimeapi mode, Refresh() repoints the `leader` slot to `master.Host:master.Port` at every tick.
- The read backend, still on the service port, no longer matched any server (`GetServerFromURL` matches on `host:port` exactly), so it vanished from the GUI.

`extra_port` has its own small connection limit (`extra_max_connections`) and no thread
pool. It isn't meant for application load.

## Design

`db-servers-hosts` keeps the **service port**, and `server.Port` stays that port everywhere:
- proxies (HAProxy, ProxySQL, MaxScale, ShardProxy, external)
- `CHANGE MASTER ... MASTER_PORT`, rejoin and reseed
- the backup tools
- server identity: `Id`, `URL`, graphs, backup folders

The new setting `db-servers-monitor-extra-port` (int, default 0 = unused) only changes the
port of repman's own SQL connections.

| Piece | Where |
|---|---|
| Port choice | `ServerMonitor.monitorExtraPort()`: the extra port when set, else `Port`. Not for PostgreSQL, not through `TunnelHost`. |
| DSN | `SetDSN` builds `DSN` on `monitorExtraPort()` and, when it differs from `Port`, `serviceDSN` on `Port`. |
| Connect | `GetNewDBConn` → `getNewDBConnPort`. It connects on `DSN` with the existing TLS retries (`connectMonitorDSN`) and returns the port it used. |
| Fallback | If the extra port fails with a network error or 1040 (`extra_max_connections` reached), repman connects on `serviceDSN` and raises WARN0267. Another server error, an access denied first, would be the same on the service port and is returned as is. |
| Retry window | After a failure, repman stays on the service port for `monitorExtraPortRetry` (1 min) before trying the extra port again. A filtered port costs one connect timeout per minute, not one per tick, so the failure detection of a node that is down isn't slowed (F2). |
| Global pool | `server.Conn` is only replaced by Ping when Refresh fails. `Ping` therefore compares the port of its new connection with `poolPort` and, when it differs, makes that connection the pool (`moveGlobalPool`). The old pool is closed after `replacedPoolGrace`. This covers a live change of the setting and the start or end of a fallback. |
| Live setting | `server/api_cluster.go` `db-servers-monitor-extra-port` → `Cluster.SetMonitorExtraPort`, which rebuilds the servers' DSN. The next Ping moves the pool. |
| GUI | Settings → Monitoring → "Database Servers Monitor Extra Port". The server table and grid show `host:port (monitored on :<port>)` from `ServerMonitor.MonitorPort`, the port of the last Ping connection. |

## Limits

- The fallback connection on the service port skips the TLS retries of `connectMonitorDSN`
  (3159 auto-TLS, rotated key). It uses the same TLS parameters as the DSN, and the next
  attempt on the extra port runs the retries.
- `extra_max_connections` must be sized for repman's connections: the global pool, one
  connection per Ping, and the job connections. Otherwise 1040 sends repman to the
  service port, with WARN0267.
- Only `check-type = tcp` uses it. The `agent` check doesn't open a SQL connection.
