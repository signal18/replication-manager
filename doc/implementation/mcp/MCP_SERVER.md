# MCP server

Branch `feat/mcp-server` (Guillaume), authorization added for issue #1838 on top of the
user-issued API tokens of #1835.

## What it is

An MCP (Model Context Protocol) server embedded in the repman binary so an AI assistant
(Claude Code, Claude Desktop, any MCP client) can inspect and, when allowed, act on the
monitored clusters. Library `github.com/mark3labs/mcp-go`. Package `mcp/`
(`repmanmcp`).

- Started with `mcp-server=true`. Transport `mcp-transport`: `api` (default) mounts the SSE
  endpoints on the API listeners themselves, `/api/mcp/sse` and `/api/mcp/message` on both the
  HTTP (10001, behind haproxy) and HTTPS (10005) servers (`handlerMuxMCP`, `MCPServer.Handler`
  built with `WithStaticBasePath("/api/mcp")` and a relative message endpoint), so TLS, the
  public URL and the bearer handling are the API's own; `sse` keeps a standalone plain-HTTP
  listener on `mcp-bind-address:mcp-port` (default localhost:10007, advertised as
  `mcp-advertise-address`); `stdio`; `both` (sse + stdio).
- All `mcp-*` settings are server scope and applied live from the global settings GUI card
  "AI Assistant (MCP)" or the global settings API: a change stops and rebuilds the MCP server
  (`restartMCPServer`), auth and transport being fixed at creation.
- Tools call repman **in process** through the `RepmanProvider` interface (implemented by
  `*server.ReplicationManager` in `server/server_get.go`), not through the REST API.
- 66 tools: 26 cluster, 22 database, 12 backup, 6 proxy, all registered (added 2026-09-25:
  `cluster-sysbench-run`, `cluster-sysbench-cleanup`,
  `get-cluster-last-crash-lost-event`, `database-backup-logical`, `database-logical-backup-splitdump`,
  `database-restore-logical-backup`, `database-restore-physical-backup`). There is no global
  read-only switch (removed 2026-09-25): read-only versus read-write is a property of the
  account or token the assistant authenticates with, decided per tool by the cluster ACL.
- Resources `repman://status`, `repman://version`, `repman://clusters`,
  `repman://clusters/{name}/...`; prompts for common diagnostics.

## Authentication and authorization (#1838)

There is **no MCP-specific permission model**: every tool mirrors a REST endpoint and runs
under the caller's cluster ACL for that endpoint, exactly as the REST call would.

- **Bearer** on `/sse` and `/message`: an interactive login JWT (`POST /api/login`) or a
  user-issued API token (`replication-manager-cli token create`, #1835). Resolved by
  `ReplicationManager.AuthenticateMCP` into a `repmanmcp.Principal` {user, auth method,
  token id/label, remote}; the password claim or the token record rides in `Principal.Auth`
  so the ACL can be re-run. `authMiddleware` (`mcp/auth.go`) refuses anything else with 401
  and logs `mcp_auth_failure`.
- **Principal in the tool context**: the SSE server is built with
  `WithSSEContextFunc(injectPrincipal)`, so every tool and resource handler receives the
  principal of the HTTP request that carried the call.
- **Per-tool ACL** (`mcp/acl.go`): `toolACLPaths` maps every tool name to the REST path it
  mirrors, relative to `/api/clusters/{cluster}`, with `{server}`, `{proxy}`, `{setting}`,
  `{value}`, `{snapshot}`, `{task}`, `{topology}` filled from the arguments. `addTool`
  wraps each handler: it builds the URL and asks `ReplicationManager.AuthorizeMCP`, which
  runs `cluster.IsValidACLQuiet` with the login credentials, or the token principal
  (`tokenPrincipalFor`, scope through `tokenURLInScope`) for a token, so a token narrowed to
  `db-show` on one cluster sees over MCP exactly what it sees over REST. Denials are
  answered as tool errors and logged as `mcp_denied` in the security log.
- The REST topology endpoints (`/topology/servers`, alerts, logs, crashes, proxies) are
  served by the unprotected handler group, readable by any account with access to the
  cluster; the matching tools therefore map to the cluster's own URL (visibility), which
  keeps MCP at parity with REST rather than stricter.
- **Fail closed**: a tool without an entry in `toolACLPaths` is refused when authentication
  is on; `TestEveryToolHasAnACLMapping` keeps the map and the registry in sync.
- `list-clusters` and the `repman://clusters` resource only list clusters the caller may see
  (the cluster's public endpoint, which applies account membership and token scope);
  cluster resource templates are gated the same way.
- **Off-switch**: `mcp-auth-enabled=false` runs every tool unrestricted with a startup
  warning. It is the only way to use the `stdio` transport, which carries no bearer; with
  authentication on, `stdio` refuses to start.

## Review fixes (PR #1839 automated review)

- **Argument injection into the ACL URL**: `cluster.matchACLRules` matches rule patterns as
  substrings of the URL, so a tool argument such as `setting_value =
  "x/actions/rotate-passwords"` used to make the settings write pass with the
  rotate-passwords grant. `aclURL` now path-escapes every substituted argument and the
  cluster name (`aclArg`), so no argument can carry a `/`. Pinned by
  `TestACLURLEscapesArguments` (mcp) and `TestACLSubstringInjectionNeedsEscaping` (server,
  real cluster ACL). The REST side has the same substring design behind the
  `{settingValue:.*}` mux wildcard; anchoring `matchACLRules` is a separate change.
- **Cluster-less tools are declared, not inferred**: `list-clusters` is mapped with the
  `global:` form (any authenticated principal); every other tool is cluster-scoped and an
  empty `cluster_name` is refused before any handler runs (`TestClusterToolNeedsClusterName`).
- **Transport `both`** refuses to start with `mcp-auth-enabled` like `stdio` does (stdio
  carries no bearer); stdio is served through `NewStdioServer(...).Listen(ctx, ...)` so
  `Stop()`'s cancel really ends it.
- **Template/schema drift**: `cluster-bootstrap-replication` no longer references a
  `{topology}` argument the tool does not declare; `TestTemplatePlaceholdersAreToolArguments`
  checks every placeholder against the tool's input schema.
- Demo compose pulls `signal18/replication-manager:nightly` (no `mcp-server` tag is
  published).
- Regtest gate (T13): no `regtest/test_*.go` scenario drives the MCP write tools yet; the
  write paths were exercised by hand on dev3 (switchover refused/allowed by grant, cluster
  creation and provisioning). Tracked in #1837.

## Cloud18 tools (server_cloud18_mcp.go, mcp/tools_cloud18.go)

Repman-global tools mapped with the `global:<grant>` form in `toolACLPaths`: `addTool` asks
`AuthorizeMCPGlobal(principal, grant)` (grant held on at least one cluster, token narrowing
applied, a token needs the `*` scope; "" = any authenticated principal). Reads:
`get-cloud18-status`, `get-cloud18-register-status`, `get-cloud18-subscription`,
`list-cloud18-subscription-plans`, `list-cloud18-clusters-for-sale`,
`list-cloud18-infrastructures` (distinct `api-public-url` of the clusters for sale). Actions
(`global-admin-show`): `cloud18-register`, `cloud18-register-confirm`, `cloud18-unregister`,
`cloud18-change-subscription`. Prompt `cloud18-onboarding`.

The registration core was extracted from the REST handlers (`startCloud18Registration`,
`confirmCloud18Registration`) so both paths share it. From MCP the GitLab password is
generated server-side (`generateCloud18Password`), kept in `regPassword` for the confirm step
and stored by `applyCloudConnect` on success, never returned. The REST registration and
subscription endpoints now authorize with `isCloud18Admin` (`global-admin-show` on the
caller's effective grants, token narrowing applied; the literal `admin` login is still
accepted when no cluster is loaded) instead of the literal user name `admin`, which a narrowed
token of admin used to pass.

## Quote, options and creation of a cluster (server_cloud18_quote.go, server_cloud18_options.go, server_cloud18_infra.go)

Three MCP tools take the same request (`db_image`, `db_count`, `dbu` per database node,
`proxy`, `proxy_count`, `apps`, `apu`, and `topology` for PostgreSQL), normalized once by
`normalizeSpec`:

- `list-cloud18-cluster-options`: the possible values per dimension (flavor, image lines
  with LTS from `share/plugins/data/lts-versions.json`, topology per flavor, proxy, apps =
  the infrastructure's templates, DBU/APU range and free pool). The ranges are the constants
  `cloud18MaxDBCount`/`MaxDBU`/`MaxAPU`/`MaxProxyCount`, shared with the validation. An
  unreadable release table is said in `db_image_source`, never shown as a flavor without lines.
- `get-cloud18-cluster-quote`: one **choice per partner infrastructure** (from the for-sale
  list, `Cloud18Infrastructures`). Each choice carries the partner, its zone and its
  infrastructure description from peer.json (`cloud18InfraDefinitionOf`: platform, CPU, data
  centers, geo, bandwidth, certifications, SLA, dbops/sysops), then the quote from that
  infrastructure's `GET /api/cloud18/self-service`: units (DBU = db_count x dbu, APU, BKU),
  whether they fit the free pool, `canCreate` or the reason, and the monthly price at full
  capacity from its own unit prices. The status read keeps a fixed list of fields
  (`selfServiceStatusOfTimeout`); `prices` and `poolBlocked` are on it. Each infrastructure is
  asked in parallel (`accessParallel`) with a 10 s deadline (`accessStatusTimeout`); one that
  refuses the identity or does not answer stays a described choice with its reason and no
  price. Sorted creatable first, cheapest first.
- `cloud18-create-cluster`: the plan with its quote, and with `confirm=true` the creation.

**APU quoted = APU applied.** `prov-proxy-apu` is per proxy: each app holds 1 APU, the
proxies share the rest equally, rounded down, at least 1 each (`proxyAPUOf`).
`normalizeSpec` refuses an `apu` below apps + proxies and replaces it by what
`applyRequestedPlan` will reserve (`appliedAPU`), so the quote prices exactly that. Apps
whose template sizes above 1 APU are not counted yet.

**Pool verdict.** The infrastructure's `enabled=false` may come from its pool check, taken for
its default cluster; the quote re-takes it for the requested units. The status says so with
`poolBlocked` (machine-readable); an older infrastructure without the flag is read from its
wording (`reason == poolNote`). The per-user limit is checked from `remaining` when present,
and in every case by the partner itself at creation (`selfServiceCheck`).

**Creation steps** (`Cloud18CreateCluster`): add the cluster; apply the requested plan through
`change-plan-units` (the partner's ledger and plan-increase script decide); **a refused plan
deletes the cluster** (`DELETE /api/clusters/actions/delete/<name>`) so none is left on a plan
nobody asked for, the answer says `leftover` when the delete fails; then per flavor:
- MariaDB, MySQL, Percona: `prov-db-image`, `addserver dbN/3306`, proxies on 3306,
  phpMyAdmin by default.
- PostgreSQL, the way pg-stream / pg-logical were built: `topology-target` first
  (`master-slave-pg-stream` default, `master-slave-pg-logical`, `active-passive` for one node),
  db1 from `postgres/postgres` and the others from `postgres/postgres-standby` (stream) or
  `postgres/postgres-peer` (logical) on 5432, as engine apps that register themselves as
  monitored servers (`registerEngineAppAsServer`); HAProxy write 5432 / read 5433; ProxySQL
  refused (MySQL protocol only); Adminer by default; the image is the templates'.
Then the cluster provision (databases, proxies, engine members) and each app's provision, in
the background.

**Identity.** A partner is asked as the caller (`peerIdentityFor`): an OIDC session logs in as
the user, a local admin with the instance's Cloud18 identity, an API token is refused (it holds
no credential for another infrastructure). This instance is called over the loopback with the
caller's own credential.

Tests: `TestQuote*`, `TestApplyRequestedPlan` (fake infrastructure: per-unit difference,
refusal), `TestCloud18SpecPostgres`, `TestCloud18SpecMySQLFamily`, `TestInfraDefinitionOfPeer`.

## Self-service clusters on an infrastructure (server_selfservice.go, server_cloud18_infra.go)

Decided 2026-09-25: a Cloud18 user may create a cluster on a partner infrastructure directly,
without the subscription / email-acceptance chain; the partner is only informed. Two sides.

**Partner side** (the infrastructure hosting the cluster), `server/server_selfservice.go`:

- Off by default: `cloud18-self-service-clusters` (server scope, GUI Cloud settings), and only
  when registered with Cloud18 and `prov-orchestrator` is `opensvc` or `kube`
  (`selfServiceCapable`).
- Limit: `cloud18-self-service-max-clusters-per-user` (default 3), counted per SSO identity as
  the clusters where that identity holds the `sponsor` role (`countSponsoredClusters`), so a
  malicious user cannot flood the marketplace listing. Dropping a cluster frees a slot.
- Enabled-script: `cloud18-self-service-clusters-enabled-script` (server scope), run by
  `runSelfServiceEnabledScript` inside `selfServiceCheck` after `selfServiceCapable` and before
  the per-user limit and the pool, and again by `selfServiceStatusFor` so the status endpoint
  tells the truth. argv = identity, orchestrator; env REPMAN_IDENTITY/ORCHESTRATOR/
  SPONSORED_CLUSTERS/NEEDED_DBU/NEEDED_APU/FREE_DBU/FREE_APU/BORROW_DBU/BORROW_APU.
  Non-zero exit or a 30 s timeout = veto, reason = first output line (or the error). Can only
  refuse more than the switch.
- Can-borrow: `cloud18-self-service-clusters-can-borrow` (server scope). `selfServicePoolCheck`
  returns `(borrowNote, err)`: when the plan pot is short and the flag is on, `CanBorrow` on the
  Database profile for the DBU and the Compute profile for the APU decides; both ok = created on
  borrowed capacity, `SelfServiceStatus.Borrowed = true` and the note carries the figures; either
  short = refused with "cannot borrow either". The cluster still gets the default plan, so the
  ledger shows the plan pot further overdrawn: the honest figure.
- Born dynamic: `selfServiceBornDynamic` runs in `handlerMuxClusterAdd` on the self-service branch
  before the first `cl.Save()`: on OpenSVC `prov-db-docker-run-args-limit` off (PG slice governs,
  no WARN0214), on Kubernetes on (requests/limits pair = the in-place Pod resizer's opt-in), then
  `prov-db-apply-dynamic-config` and `prov-db-dynamic-resource` on. Other orchestrators untouched.
  Tests: `TestSelfServiceCanBorrow`, `TestSelfServiceEnabledScript`, `TestSelfServiceBornDynamic`.
- `POST /api/clusters/actions/add/{name}` (`handlerMuxClusterAdd`) used to accept any
  authenticated user with no grant and never added the creator to the cluster. Now a local
  account needs `cluster-create` or `prov-cluster`; an SSO identity (JWT `AuthType=SSO`,
  i.e. a peer logged in with its Cloud18 GitLab credentials) without them goes through
  `selfServiceCheck`. Denials are logged `cloud18_self_service_denied`.
- No service plan: the cluster starts on the instance defaults `prov-db-dbu`,
  `prov-service-plan-apu`, `prov-service-plan-bku` (the `plan` field of the form is ignored
  for self-service).
- The creator becomes the `sponsor` of the new cluster with `selfServiceSponsorGrants`
  (`cluster-create-monitor cluster-settings cluster-delete cluster-show prov app-deployment
  db show proxy grant-show extrole token-create`): enough to populate, provision, use and drop
  it, nothing on other clusters, no `cluster-create` (so no further clusters through that
  account). The account has an empty password (SSO-only) and is persisted through
  `api-users-acl-allow-external` + `SaveAcls()`.
- Partner informed: security event `cloud18_self_service_cluster`, cluster WARN log, mail to
  `mail-to` when SMTP is configured (`notifySelfServiceCluster`).
- **ResourceManager pool gate** (`selfServicePoolCheck`, `server_infra_pool.go`): a new
  cluster reserves the default plan of a master and a replica (2 × `prov-db-dbu`) plus
  `prov-service-plan-apu`; it is refused when the infrastructure pool cannot hold it. Pool =
  capacity (unique agents summed, `resource-manager-infra-*` overrides winning, binding axis
  through the DBU and APU profiles) × `resource-manager-infra-quota-pct` − Σ every cluster's
  plan (`GetPlanDbu`, `AppPlanByCluster`). The capacity inputs are shared with
  `/api/global/resources` (`infraCapacityInputs`). An unknown pool (no agent observed, no
  override declared) does not gate; the status says so (`poolNote`). Status carries
  `neededDbu`, `neededApu`, `pool{usable,planned,free}`, `poolOk`.
- `GET /api/cloud18/self-service` (both routers) answers `SelfServiceStatus` for the caller:
  enabled/reason, orchestrator, limit, used, cluster names, remaining, default units.

**Consumer side** (the instance of the user, driving the assistant),
`server/server_cloud18_infra.go`, tools in `mcp/tools_cloud18.go`:

- `cloud18-create-cluster` (infrastructure = `api-public-url` from
  `list-cloud18-infrastructures`, cluster_name, db_image default `mariadb:lts`, db_count
  default 2, proxy `haproxy`|`proxysql`|`none`, apps = template names, confirm). ACL
  `global:global-admin-show`.
- `peerLogin` logs into the infrastructure with this instance's Cloud18 GitLab credentials
  (same as the dashboard peer proxy, `PeerLogin`); the infrastructure only accepts URLs known
  to `PeerManager` (`HasPeerURL`). The bearer of that session is the SSO JWT the partner
  evaluates.
- `confirm=false` (default) is a dry run: normalized spec, planned services, and the
  infrastructure's `GET /api/cloud18/self-service` for this identity (refused reason or
  remaining slots). `confirm=true` runs: `POST clusters/actions/add/{name}` (no plan) →
  `settings/actions/set/prov-db-image/<image>` (best effort: a partner may pin the
  image as immutable, the result then carries `dbImageNote`) → `actions/addserver/dbN/3306`,
  `.../<proxy>1/3306/<proxy>`, `POST .../<app>N/80/app/<template>` with **short host names**
  (the infrastructure appends `.<cluster>.svc.<orchestrator cluster>` itself; a dotted name
  gets the suffix twice and never resolves, seen on dev3) → `services/actions/provision`,
  which is synchronous on the infrastructure (waits for the databases, bootstraps
  replication, minutes) and covers databases and proxies only, then
  `POST apps/<app id>/actions/provision` for each app (ids resolved from `topology/apps`, the app routes take the id, not the name) (`ProvisionServices` does not provision
  apps): all of it runs in a goroutine with a 20-minute timeout per call and the outcome
  goes to the log, the tool answers at once with the steps done. On an earlier failing step
  the result carries `failedStep`, so a partial creation is visible and can be dropped by
  the sponsor.
- Found while validating on dev3 and fixed in `handlerMuxClusterAdd`: the handler blanked
  the external ACL strings but kept the inherited external credentials, so the Cloud18 git
  user "existed" and went through `UpdateUser` (which cannot add an entry) and ended up a
  visitor with every grant discarded on the cluster it had just created. The external
  accounts are now reset (credentials, secret and ACL) before admin and the Cloud18 user are
  re-added. Also: a self-service sponsor is a **passwordless** account
  (`cluster.AddSSOOnlyUser`), because `IsLocalOnlyAccount` makes any password-protected
  account unusable by the oidc ACL check; and holding `prov-cluster` only as a sponsor does
  not open the ordinary creation path (`holdsProvClusterOutsideSponsorship`), or the limit
  would end after the first cluster.
- `get-cloud18-cluster` (infrastructure, cluster_name): flags, servers, proxies, apps through
  the same session, to follow the provisioning. ACL: any authenticated principal.
- `cloud18-create-cluster-token` (infrastructure, cluster_name, label, grants, expire_days):
  the answer to "how does the assistant operate the cluster it just created": tokens never
  cross infrastructures, so the sponsor mints one **on the infrastructure** (`POST
  /api/tokens` through the peer session, scope = that cluster, grants = the sponsor's ∩
  requested; the sponsor grants include `token-create` for this reason) and the tool returns
  the token once plus `mcpServerConfig` (the infrastructure's `/api/mcp/sse` with the bearer)
  to add as a second MCP server. ACL `global:global-admin-show`. Nothing is stored here.

Tests: `server/api_token_test.go` `TestSelfServiceRules` (off by default, orchestrator gate,
sponsor account shape, per-identity limit, status); `mcp/acl_test.go` mapping completeness.

## Not done

- The consumer tools are not covered by an end-to-end unit test (they need a live partner);
  validated by hand against dev3.

- The standalone `sse` transport stays plain HTTP: bind it to localhost, or use the default
  `api` transport which rides the HTTPS listener.
- The gRPC API and the web terminal remain login-JWT only, as noted in
  `doc/implementation/security/API_TOKENS.md`.

## Tests

`mcp/acl_test.go` (fake provider: mapping completeness, URL building, tools/call refused
without principal or grant and passed with it, visibility of clusters, middleware 401 and
security event); `server/api_token_test.go` `TestMCPAuthenticateAndAuthorize` (token
authenticates, scope and grants applied, revoked token refused).

## Tool naming rules (2026-10-01)

Set with Stéphane after three renames in a row ("we need rules"): (1) reads are
`<verb>-<domain>-<object>` with `list-` for several entries, `get-` for one object, `check-`
for a verdict; (2) actions are `<domain>-<verb>[-<object>]`; (3) domains are the API's,
`cluster`, `database` (never "server", to contrast with `proxy`), `proxy`, `cloud18`;
(4) product words only (`local` / `archive` backups, never `restic`); (5) parameters
`cluster_name`, `server_name` (host:port), `proxy_name`, `task_id`; (6) one tool = one REST
route in `acl.go` (`TestEveryToolHasAnACLMapping`). No aliases: the tools were not in a
release when renamed. The user doc states the same rules under 3.8.6.

## Alerts and logs per module (2026-10-01)

`get-cluster-alerts` reads the four state machines (`GetStateMachine()` = ha,
`WorkloadStateMachine`, `SecurityStateMachine`, `SchemaStateMachine`), `module` selects one or
`all` groups them; a nil machine answers empty lists. `list-cluster-logs` mirrors
`/topology/logs/{logType}` through `GetWebLogsByType` and filters in `filterLogEntries`:
minimum `level` (default warning; STATE/ALERT rank as warning so they are never hidden),
`module` by tag name (`config.GetTagsForLog`, exposed in each entry instead of the id),
`limit` newest first (the ring buffers hold the newest at index 0, empty slots skipped).
Why: the assistant's context is the scarce resource; a 200-line INFO dump hides the one
WARN that matters, and the module in an alert names the log to open next.
Test `TestFilterLogEntries`.

## Rolling tools and the release table (2026-10-01)

`cluster-rolling-reprov`, `cluster-rolling-jobs-upgrade` and `cluster-rolling-upgrade` join
`cluster-rolling-restart` on the `/actions/rolling/{action}` route; all four are asynchronous
like the API (the restart tool used to block the MCP call for the whole operation).
`cluster-rolling-upgrade` takes a `target`, a method of the configurator's image list:
`patch` (newest release of the current line), `next-minor`, `next-lts`, `next-major`,
`last-lts`, `version` (+ `version`), and `confirm`. Without confirm it answers
`Cluster.PlanRollingUpgrade` (the same plan as `GET /actions/rolling/upgrade/plan`): current
line from the master's running version, target release from the list (never a registry
lookup, `doc/implementation/cluster/DATABASE_IMAGE_PINNING.md`), what `prov-db-image`
declares afterwards, node order, warnings (major = mariadb-upgrade, no rolling way back,
non-LTS line, not in the list, pinned image, on-premise = script path) and the `steps`. With confirm it runs `Cluster.PrepareRollingUpgrade` (declare, pin the definitions on the
target release, push them node by node on OpenSVC; refused on an immutable pin) and starts
`RollingUpgrade`, exactly what `POST /actions/rolling/upgrade?target=` does. A rolling restart never changes the image (#1861). The
declared image is resolved to a release before the push (`ResolveDatabaseImage`, the service
definition never carries a pointer, `doc/implementation/cluster/DATABASE_IMAGE_PINNING.md`);
the plan reports `targetRelease` (what the target tag points at today) and `currentRelease`. The release table `utils/releases/lts-versions.json`
(lts + published lines per flavor) is shared with plugin-score-lts and overridable from
`<share>/plugins/data/lts-versions.json`. Tests `TestResolveTargets`, `TestRollingUpgradePlan`.
The doc tables are regenerated with `doc/implementation/mcp/gen_tool_tables.py`.

## App tools and phpMyAdmin by default (2026-10-02)

Domain `app`, one tool = one route: `list-app-templates` (`GET /templates/apps`: the
repository cache of `prov-app-template-repo` plus the cluster's local templates, names as
template paths such as `phpmyadmin/phpmyadmin`), `list-cluster-apps` (`GET /topology/apps`,
with each app's `url` and, when the app asked the cluster for a database (#1870), its `db` object), `app-add` (`POST /actions/addserver/{name}/{port}/app/{template}`,
a short name resolves against the list, an unknown template is refused, never turned into a
docker image), `app-provision` / `app-unprovision` (`/apps/{app}/actions/...`, OpenSVC,
asynchronous). `App.URL` (`GetPublicURL`): the protocol and CNAME of the primary route once
the app has one (`https://<app>.<cluster>.<subDomain>-<zone>.<domain>.cloud18.io/`), else
the internal `http://host:port/`; refreshed every tick, in the topology JSON for the GUI.
Lifecycle (2026-10-02): `app-start`, `app-stop`, `app-restart` (`POST /apps/{app}/actions/start|stop|restart`, optional node) and `app-resize` (`prov-app-units` through `/apps/{app}/settings/actions/set/prov-app-units/{value}`: the plan in whole units, cores/memory/disk at the ratio; the answer says a reprovision is needed to apply it, since `OpenSVCProvisionAppV3` reuses an existing service definition).

`cloud18-create-cluster` deploys `phpmyadmin` by default (`apps=none` to opt out), resolves
the app names against the infrastructure's `appTemplates` (the self-service status now
carries them with the identity `domain` / `subDomain` / `zone` / `gatewayDomain`), refuses
a template the infrastructure does not have, and answers `apps: [{name, template, url}]`,
the URL the template's primary route gives once provisioned.
