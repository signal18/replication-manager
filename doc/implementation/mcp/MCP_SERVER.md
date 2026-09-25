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
  `run-sysbench`, `sysbench-cleanup`,
  `last-crash-lost-event`, `server-backup-logical`, `server-logical-backup-splitdump`,
  `server-restore-logical-backup`, `server-restore-physical-backup`). There is no global
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
  `POST apps/<app>/actions/provision` for each app (`ProvisionServices` does not provision
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
