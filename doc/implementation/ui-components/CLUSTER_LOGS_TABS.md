# Cluster logs in tabs

A cluster's logs are shown in one **Logs** section, one tab per log, instead of one
collapsible section per log. Component: `share/dashboard_react/src/Pages/Dashboard/components/ClusterLogsTabs/index.jsx`.

## Where it renders

- **Dashboard** tab of a cluster (`Pages/Dashboard/index.jsx`): replaces the seven
  sections Cluster Logs, Job Logs, Security Logs, Workload Logs, DDL Change Logs,
  Schema Logs and Variable Change Logs, at the same place (after Application Servers).
- **Monitoring > Schema** (`Pages/Shards/index.jsx`): replaces its two sections Cluster
  Logs and Job Logs with the same tabs.

Left as they are, on purpose:
- **Maintenance > Job Logs** keeps its own sub-tab: the job log next to the backups and
  jobs it describes.
- **Slideshow** keeps its own layout (it picks logs per slide).
- **Global Logs** (instance level) is not a cluster log.

## Tabs

The tab titles, order and help texts are the ones of the sections they replace:

| Tab | `clusterLogs` key (API `logType`) | Component | Gear opens |
|-----|-----------------------------------|-----------|------------|
| Cluster Logs (first, default) | `general` | `GeneralLogs` | Log settings |
| Job Logs | `task` | `TaskLogs` | Maintenance > Scheduler |
| Security Logs | `security` | `SecurityLogs` | Plugin settings |
| Workload Logs | `workload` | `WorkloadLogs` | Plugin settings |
| DDL Change Logs | `ddl` | `DDLLogs` | Monitoring settings |
| Schema Logs | `schema` | `SchemaLogs` | Plugin settings |
| Variable Change Logs | `variable-change` | `VariableChangeLogs` | Monitoring settings |

Each tab reuses the existing component of its log (`Pages/Dashboard/components/Logs`),
so search, level filters and on-disk history ("Load older", Cluster Logs and Job Logs
only) behave as before. The section's gear follows the selected tab. Monitoring > Schema
passes the same four openers (Log settings, the Scheduler, Plugin settings, Monitoring
settings), so its gears match the Dashboard's; its Job Logs gear, Log settings before, now
opens the Scheduler like the Dashboard's. A gear whose opener a page does not pass is
hidden.

Not shown, no cluster API today: the maintenance log (`maintenance.log`, written by
`MaintenanceLogrus`, file only) and a config log (the config domain has no log yet, see
law T3). `sysbench` is returned by the same endpoint but is benchmark history, read by
the bench compare modal, not a log view.

## Data and loading

No new endpoint. All these buffers already arrive in **one** call the cluster tick makes
every refresh (`getClusterLogs`, `GET /api/clusters/{cluster}/topology/http-logs`, stored
in `state.cluster.clusterLogs`). The tabs add no request. `TabItems` renders only the
selected tab's content, so a tab's component mounts (and filters its buffer) only when
it is opened.

The badge on a tab is the number of entries its in-memory buffer holds
(`clusterLogs[key].buffer.length`, bounded by the server ring size), shown when it is
above zero. It counts neither older history loaded with "Load older" nor the level or
text filters; its tooltip says so. It is a `TagPill` (`tagpill` class), which the global span colour override
in `styles/_global.scss` excludes. The tab label itself uses divs, not spans, so its
text follows the selected tab's colour.

## Remembered tab

The selected tab is stored by key in `localStorage` (`clusterLogsTab`), like the
Maintenance sub-tab (`maintenanceTab`): a refresh, or coming back to the Dashboard,
reopens it. The Dashboard and Monitoring > Schema share it. An unknown or missing value
falls back to Cluster Logs.
