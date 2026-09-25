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
- 59 tools: 23 cluster, 18 database, 12 backup, 6 proxy, all registered. There is no global
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

## Not done

- The standalone `sse` transport stays plain HTTP: bind it to localhost, or use the default
  `api` transport which rides the HTTPS listener.
- The gRPC API and the web terminal remain login-JWT only, as noted in
  `doc/implementation/security/API_TOKENS.md`.

## Tests

`mcp/acl_test.go` (fake provider: mapping completeness, URL building, tools/call refused
without principal or grant and passed with it, visibility of clusters, middleware 401 and
security event); `server/api_token_test.go` `TestMCPAuthenticateAndAuthorize` (token
authenticates, scope and grants applied, revoked token refused).
