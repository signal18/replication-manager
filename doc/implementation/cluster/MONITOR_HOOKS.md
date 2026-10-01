# Monitor lifecycle hooks (#1858)

Two client-overridable scripts, cluster scope, dynamic, one contract for the three monitor
kinds. They replace the per-kind add/drop hooks the product never had (the existing
`prov-db-*` / `prov-proxy-*` scripts are provisioning steps, the `*-state-change-script`
are state transitions). Decided 2026-10-01: "condense in 2 scripts with type app|proxy|database,
version, resources"; named under the `monitoring-` domain because a monitor is added on every
orchestrator, provisioned or not.

| Setting | When | Effect |
|---|---|---|
| `monitoring-add-monitor-script` | BEFORE a monitor is added | non-zero exit or 30 s timeout = the add is refused, the first output line (or the error) is the reason the API returns (409 on the server-add route, the same text in the GUI and MCP) |
| `monitoring-drop-monitor-script` | AFTER a monitor is dropped | informative: exit code logged, the drop stands |

## Contract (`cluster/monitor_hooks.go`)

- argv: `cluster type name version units`; type = `database|proxy|app`; name = `host:port`
  for a database or a proxy, the app's name at drop (host:port at add, no App object yet).
- env over `GetExecEnv()` (API URL + admin credentials): `REPMAN_CLUSTER`,
  `REPMAN_MONITOR_PHASE` (add|drop), `REPMAN_MONITOR_TYPE`, `REPMAN_MONITOR_NAME`,
  `REPMAN_MONITOR_HOST`, `REPMAN_MONITOR_PORT`, `REPMAN_MONITOR_VERSION`,
  `REPMAN_MONITOR_FLAVOR`, `REPMAN_RESOURCE_CORES`, `REPMAN_RESOURCE_MEMORY_MB`,
  `REPMAN_RESOURCE_DISK_GB`, `REPMAN_RESOURCE_IOPS`, `REPMAN_RESOURCE_UNIT` (dbu|apu),
  `REPMAN_RESOURCE_UNITS`.
- version: at add the tag of the declared image (`prov-db-image`, the proxy image of the
  type, `prov-app-docker-img`); at drop what the monitor observed (`DBVersion`, `GetVersion()`,
  `App.Version`), falling back to the declared tag.
- flavor: database = image repo at add (`mariadb`, `mysql`, ...) and the detected engine at
  drop; proxy = the proxy type; app = `prov-app-template` or the image repo.
- resources: the declared axes of the kind, `prov-db-cpu-cores/memory/disk-size/disk-iops`,
  `prov-proxy-cpu-cores/memory/disk-size`, `prov-app-cpu-cores/memory/disk-size/disk-iops`
  per agent. Units: `GetProvDbuFromConfigPerNode` for a database, the Compute profile
  (`computePlanAPUReading`) for a proxy or app.

## Fire points

- `AddSeededServer` (server add API, database) and `AddSeededProxy` (proxy add API) run the add
  script before touching the host lists; `AddSeededApp` before `appendConfAppIfAbsent`.
- `RemoveServerMonitor`, `RemoveProxyMonitor`, `RemoveAppMonitor` run the drop script after
  the monitor is out of the lists and the failover state is released.
- Not fired: failover, switchover, rejoin, provisioning, config reload (the monitor set is
  rebuilt from the same lists), `SetServicePlan` host reshaping (the plan is the signal there).

Tests: `TestMonitorHookScripts` (veto, first line, argv, env, drop phase),
`TestMonitorHookDescriptions`. GUI: Settings → Monitoring, two rows. Related gate on the
creation of a whole cluster: `cloud18-self-service-clusters-enabled-script` (REGISTRATION /
MCP_SERVER docs).
