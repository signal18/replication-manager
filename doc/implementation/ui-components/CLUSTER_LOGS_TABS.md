# Cluster logs in tabs

A cluster's logs are shown in one **Logs** section, one tab per log, instead of one
collapsible section per log. The instance's logs are shown the same way (see "Instance
logs" below). Shared component: `share/dashboard_react/src/components/LogsTabs/index.jsx`
(tabs, badges, remembered tab, gear); the cluster's tabs:
`share/dashboard_react/src/Pages/Dashboard/components/ClusterLogsTabs/index.jsx`; the
instance's: `GlobalLogsTabs` in `share/dashboard_react/src/Pages/GlobalItems/index.jsx`.

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

The badge on a tab is the number of entries its in-memory buffer holds, bounded by the
server ring size, shown when it is above zero. The server ring comes pre-filled with
empty slots (its `buffer` always has the ring's length), so the count is the entries with
a timestamp, the rule the `Logs` component uses to skip the empty slots; never
`buffer.length`. It counts neither older history loaded with "Load older" nor the level or
text filters; its tooltip says so.

The badge is a `CountPill` (`components/AlertBadge`): the count bubble of the header's
domain pills, same round shape, font and white text, in the colour of the pill of the
log's domain. The colours live in one place, `utility/domainPills.js`, which the header
(Navbar, `HealthAlertBadge`) and the security score modal use too:

| Tab | Colour of | Colour |
|-----|-----------|--------|
| Cluster Logs | Cluster health pill | red on a blocker, orange on a warning, green |
| Job Logs | running-backup pill | teal (jobs have no state machine) |
| Security Logs | Security pill | the security grade's colour while states are open, gray |
| Workload Logs | Workload pill | purple while states are open, gray |
| DDL Change Logs, Schema Logs | Schema pill | red on an ERROR state, cyan, gray |
| Variable Change Logs | Config pill | red on ERROR, yellow on WARNING, blue, gray |

`alertCount` is one of the classes the global span colour override in
`styles/_global.scss` excludes, so the white text holds. The tab label itself uses divs,
not spans, so its text follows the selected tab's colour.

## Remembered tab

The selected tab is stored by key in `localStorage` (`clusterLogsTab`), like the
Maintenance sub-tab (`maintenanceTab`): a refresh, or coming back to the Dashboard,
reopens it. The Dashboard and Monitoring > Schema share it. An unknown or missing value
falls back to Cluster Logs.

## Empty local cluster list

Same GUI pass (client request). A user who sees no local cluster on the instance (a
Cloud18 SSO user on a monitor where none of its clusters live: `/api/clusters` returns
`[]`) used to be moved by `Pages/Home` from Clusters Local to Clusters Peer, then to
Clusters For Sale when the peers were empty too. That automatic jump is gone: the user
stays on Clusters Local, and `Pages/ClusterList` shows "No local cluster on this
instance". When the user has the Peer or For Sale tab (cloud18 on, the tab not
disabled), a link under the message opens the first of them (`emptyLink`, built by
Home); no link otherwise. A search that matches nothing still says "No cluster found!".

## Instance logs

The instance view (Clusters Local and the instance Dashboard) shows its logs as tabs of
one Logs section (`GlobalLogsTabs`, remembered as `globalLogsTab`), Global Logs first and
the default:

| Tab | API | Badge colour |
|-----|-----|--------------|
| Global Logs | `GET /api/global/http-logs` (unchanged) | the monitor health pill: red on a blocker, orange on a warning, green |
| Security Logs | `GET /api/global/security-logs` | orange (no header pill of its own) |
| Access Logs | `GET /api/global/access-logs` | blue |

The three are fetched by the instance tick of `Pages/Home` (next to `getGlobalLogs`) into
`state.globalClusters.globalLogs.{general,security,access}`. The two new endpoints answer
in the shape of `/api/global/http-logs` (`{ general: { buffer, len, dropped } }`) to the
users with the `global-admin-show` grant, as `/api/global/http-logs` does (the handler
checks it; the `/api/global/*` routes are not in the cluster ACL rule tables). Swagger:
`docs/swagger.{json,yaml}`.

Server side (`server/server_instance_logs.go`):

- **Rings.** Each log is an `s18log.HttpLog` ring: a hard max of entries from TOML
  (`log-security-length`, default 200; `log-api-access-length`, default 500; 0 = the
  default), drop-oldest in a fresh backing array, and the number of entries dropped since
  start counted (`HttpLog.AddReportDrop`) and returned as `dropped` (T18). The sizes are
  server-scope global settings, changed live (the ring is resized, keeping its newest
  entries). The drop count is not raised as a state: a display ring wraps all the time on
  a busy instance (the access ring at the GUI's polling rate), a state would stay open for
  good and say nothing; the full history is in the log files. Nothing is written to the
  config repo (T17).
- **Security.** `logSecurityEvent` (API logins, refused logins and tokens, lockouts,
  refused global settings, terminal refusals), the writer of the security log file, also
  adds the event to the ring: `[security] <event>: <message> (user "<u>", from <addr>)`,
  INFO for a `*_success` event, WARN otherwise.
- **Access.** A router middleware (`apiAccessLogMiddleware`, on both the HTTP and the
  HTTPS router) records the `/api/` calls that matter, after they ran: every call that
  changes something (any method but GET, HEAD, OPTIONS) and every failed call (status
  400 and above). A successful read is not recorded: the GUI polls dozens per refresh
  and would push the refusals out of the ring within minutes. An entry: method, path,
  status, duration, user, remote address; level ERROR for a 5xx, WARN for a 4xx. The path
  is built from the route template, never from the raw URL: no query string, and only an
  allowlist of variables is logged by value (names ending in `Name` or `Port`, and
  `action`, `task`, `level`, `logType`, `topology`, `state`, `index`, `field`, `month`);
  any other variable stays as its placeholder (`.../set/db-servers-credential/{settingValue}`,
  `/api/tokens/{tokenID}`), so a new route carrying a secret is never logged in clear.
  Control characters are dropped from every field. No header, body or token is ever read
  into it (F10). The user is read from the token only for a recorded call, and a panic in
  the recording is recovered: the access log never costs a request. A path that matches
  no route (a 404 before any handler) is not recorded: a mux middleware runs on matched
  routes only. The jobs containers' polling routes (`.../needs/{taskname}`,
  `.../jobs-log-level/{task}/{level}`) answer "no" with a 500 "false" on purpose (the
  compatibility contract of older dbjobs scripts), several times a second: they are
  recorded at DEBUG under the task module's verbosity only (`log-task-level` at debug,
  `IsEligibleForPrinting(ConstLogModTask, DEBUG)`), with the task name, and leave no trace
  at the default level; a refusal (401, 403) on them is recorded like any other.
  `need-config-refresh` is polled by the same containers but answers "nothing to do" with a
  204 and no body (a 500 "No config refresh needed!" up to v3.1.52): a successful GET, it is
  never recorded; its 500 is kept for a real error (no cluster, no server). Identical
  entries in a row (same method, path, status, user and host, whatever the source port)
  collapse into one, "(repeated N times since <first time>)" with the time of the last,
  so a caller in a loop never floods the ring. Only strictly consecutive repeats collapse
  (A, B, A, B stays four entries): it is not a general de-duplication. Each logged field
  is cut at 300 bytes. A bounded display ring can still be flooded: a client sending refused calls in a
  loop pushes the older entries out; the security log file keeps the refused logins, and
  the full history is in the log files. Off-switch:
  `log-api-access` (T14), a global setting, live.
- Tests: `utils/s18log/httplog_ring_test.go` (bound, drop count, empty slots, resize),
  `server/server_instance_logs_test.go` (path redaction, middleware: no secret in an
  entry, bound and drops, off-switch, paths outside `/api/`; security ring).
