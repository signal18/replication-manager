// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// Per-user authorization of MCP tools (issue #1838).
//
// Every tool mirrors a REST endpoint. Before a tool runs, the caller's principal
// (resolved from the bearer by the server: an interactive login JWT or a
// user-issued API token, #1835) is checked against the cluster ACL for the REST
// URL the tool mirrors, through RepmanProvider.AuthorizeMCP. So a user, or a
// token narrowed to a subset of the user's grants and to some clusters, can do
// over MCP exactly what it can do over REST, nothing more, and every denial goes
// to the security log. There is no separate MCP permission model to keep in sync.
//
// Off-switch: mcp-auth-enabled=false runs every tool unrestricted, as before,
// with a startup warning; it is the only way to use the stdio transport, which
// carries no bearer.

// Principal is the authenticated caller of an MCP request.
type Principal struct {
	User       string // account name (the token owner for an API token)
	AuthMethod string // "password", "oidc" or "token"
	TokenID    string // API token id, when AuthMethod is "token"
	TokenLabel string
	Remote     string // client address, for the security log
	// Auth carries what the server needs to re-run the cluster ACL for this
	// principal (the login's password claim, or the token record). Opaque here.
	Auth any
}

// String renders the principal for logs.
func (p *Principal) String() string {
	if p == nil {
		return "anonymous"
	}
	if p.AuthMethod == "token" {
		return fmt.Sprintf("%s (token %s %q)", p.User, p.TokenID, p.TokenLabel)
	}
	return p.User
}

type principalKey struct{}

// withPrincipal stores the principal in the request context; the MCP library
// hands that context to every tool and resource handler of the request.
func withPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// principalFrom returns the principal of a handler context, nil when none.
func principalFrom(ctx context.Context) *Principal {
	p, _ := ctx.Value(principalKey{}).(*Principal)
	return p
}

// injectPrincipal is the SSE context hook: it resolves the bearer of the HTTP
// request into a principal for the handlers. The auth middleware already
// rejected requests without a valid bearer, so a nil principal here only
// happens with mcp-auth-enabled=false.
func (s *MCPServer) injectPrincipal(ctx context.Context, r *http.Request) context.Context {
	if !s.conf.MCPAuthEnabled {
		return ctx
	}
	p, err := s.repman.AuthenticateMCP(r)
	if err != nil || p == nil {
		return ctx
	}
	return withPrincipal(ctx, p)
}

// toolACLPaths maps every tool to the REST path it mirrors, relative to
// /api/clusters/{cluster}. Placeholders: {server}, {proxy}, {setting}, {value},
// {snapshot}, {task}, {topology}, filled from the tool arguments. A tool absent
// from this map is refused when authentication is on (fail closed), so adding a
// tool means adding its line here.
var toolACLPaths = map[string]string{
	// cluster, read. The REST topology endpoints (/topology/servers, alerts,
	// logs, crashes, proxies) are served by the unprotected handler group: any
	// account with access to the cluster reads them. "" mirrors that: the
	// cluster's own public URL, which still applies account membership and token
	// scope.
	"list-clusters":             "", // filtered per cluster in the handler
	"get-cluster-health":        "",
	"get-cluster-topology":      "",
	"get-cluster-settings":      "",
	"get-cluster-alerts":        "",
	"get-cluster-logs":          "",
	"get-cluster-crashes":       "",
	"check-cluster-error-state": "",
	"last-crash-lost-event":     "", // REST /servers/{s}/lost-events is served without a specific grant
	// cluster, write
	"cluster-failover":               "/actions/failover",
	"cluster-switchover":             "/actions/switchover",
	"cluster-rolling-restart":        "/actions/rolling/restart",
	"cluster-optimize":               "/actions/optimize",
	"cluster-rotate-passwords":       "/actions/rotate-passwords",
	"cluster-reset-failover-control": "/actions/reset-failover-control",
	"cluster-reset-sla":              "/actions/reset-sla",
	"cluster-start-traffic":          "/actions/start-traffic",
	"cluster-stop-traffic":           "/actions/stop-traffic",
	"cluster-physical-backup":        "/actions/master-physical-backup",
	"cluster-checksum-tables":        "/actions/checksum-all-tables",
	"run-sysbench":                   "/actions/sysbench",
	"sysbench-cleanup":               "/actions/sysbench-cleanup",
	"cluster-set-setting":            "/settings/actions/set/{setting}/{value}",
	"cluster-switch-setting":         "/settings/actions/switch/{setting}",
	"cluster-bootstrap-replication":  "/actions/replication/bootstrap/{topology}",
	"cluster-cleanup-replication":    "/actions/replication/cleanup",
	// database, read
	"get-server-status":       "/servers/{server}/status",
	"get-server-variables":    "/servers/{server}/variables",
	"get-server-processlist":  "/servers/{server}/processlist",
	"get-server-slow-queries": "/servers/{server}/slow-queries",
	"get-server-error-log":    "/servers/{server}/errorlog",
	"get-server-tables":       "/servers/{server}/tables",
	"check-server-is-master":  "/servers/{server}/is-master",
	"check-server-is-slave":   "/servers/{server}/is-slave",
	"check-server-is-late":    "/servers/{server}/is-slave-late",
	// database, write
	"server-start":                    "/servers/{server}/actions/start",
	"server-stop":                     "/servers/{server}/actions/stop",
	"server-restart":                  "/servers/{server}/actions/restart",
	"server-backup-physical":          "/servers/{server}/actions/backup-physical",
	"server-backup-logical":           "/servers/{server}/actions/backup-logical",
	"server-logical-backup-splitdump": "/servers/{server}/actions/backup-logical",
	"server-restore-logical-backup":   "/servers/{server}/actions/reseed/logicalbackup",
	"server-restore-physical-backup":  "/servers/{server}/actions/reseed/physicalbackup",
	"server-optimize":                 "/servers/{server}/actions/optimize",
	"server-set-maintenance":          "/servers/{server}/actions/maintenance",
	"server-set-read-only":            "/servers/{server}/actions/toggle-read-only",
	"server-set-read-write":           "/servers/{server}/actions/toggle-read-only",
	"server-kill-query":               "/servers/{server}/actions/kill-query",
	// backup, read
	"list-backups":          "/backups",
	"get-backup-stats":      "/backups/stats",
	"list-restic-snapshots": "/restic/snapshots",
	"get-restic-stats":      "/restic/stats",
	"get-restic-task-queue": "/restic/task-queue",
	// backup, write
	"restic-init":              "/restic/init",
	"restic-fetch":             "/restic/fetch",
	"restic-purge":             "/restic/purge/{snapshot}",
	"restic-unlock":            "/restic/unlock",
	"restic-task-queue-pause":  "/restic/task-queue/pause",
	"restic-task-queue-resume": "/restic/task-queue/resume",
	"restic-task-cancel":       "/restic/task-queue/cancel/{task}",
	// proxy
	"list-proxies":      "",
	"get-proxy":         "/proxies/{proxy}",
	"proxy-start":       "/proxies/{proxy}/actions/start",
	"proxy-stop":        "/proxies/{proxy}/actions/stop",
	"proxy-provision":   "/proxies/{proxy}/actions/provision",
	"proxy-unprovision": "/proxies/{proxy}/actions/unprovision",
}

// aclURL builds the REST URL a tool call mirrors, from its arguments.
func aclURL(clusterName string, template string, req mcp.CallToolRequest) string {
	url := "/api/clusters/" + clusterName + template
	repl := strings.NewReplacer(
		"{server}", req.GetString("server_name", ""),
		"{proxy}", req.GetString("proxy_name", ""),
		"{setting}", req.GetString("setting_name", ""),
		"{value}", req.GetString("setting_value", ""),
		"{snapshot}", req.GetString("snapshot_id", ""),
		"{task}", req.GetString("task_id", ""),
		"{topology}", req.GetString("topology", ""),
	)
	return repl.Replace(url)
}

// authorize runs the cluster ACL for a principal on a REST URL. With
// authentication off it always passes.
func (s *MCPServer) authorize(ctx context.Context, clusterName string, url string) (bool, string) {
	if !s.conf.MCPAuthEnabled {
		return true, ""
	}
	p := principalFrom(ctx)
	if p == nil {
		return false, "unauthenticated: no principal on this request (stdio transport needs mcp-auth-enabled=false)"
	}
	if s.repman.AuthorizeMCP(p, clusterName, url) {
		return true, ""
	}
	return false, fmt.Sprintf("forbidden: %s may not %s", p.String(), url)
}

// addTool registers a tool behind the ACL of the REST path it mirrors.
func (s *MCPServer) addTool(tool mcp.Tool, handler mcpserver.ToolHandlerFunc) {
	template, mapped := toolACLPaths[tool.Name]
	s.mcp.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !s.conf.MCPAuthEnabled {
			return handler(ctx, req)
		}
		if !mapped {
			return mcp.NewToolResultErrorf("forbidden: tool %s has no ACL mapping", tool.Name), nil
		}
		clusterName := req.GetString("cluster_name", "")
		if clusterName == "" {
			// Cluster-less tools (list-clusters) filter per cluster themselves.
			if p := principalFrom(ctx); p == nil {
				return mcp.NewToolResultError("unauthenticated: no principal on this request"), nil
			}
			return handler(ctx, req)
		}
		if ok, reason := s.authorize(ctx, clusterName, aclURL(clusterName, template, req)); !ok {
			return mcp.NewToolResultError(reason), nil
		}
		return handler(ctx, req)
	})
}

// clusterVisible reports whether the caller may see a cluster at all (the
// cluster's own public REST endpoint, which still applies account and token scope).
func (s *MCPServer) clusterVisible(ctx context.Context, clusterName string) bool {
	ok, _ := s.authorize(ctx, clusterName, "/api/clusters/"+clusterName)
	return ok
}

// visibleClusterNames lists the clusters the caller may see.
func (s *MCPServer) visibleClusterNames(ctx context.Context) []string {
	names := make([]string, 0)
	for name := range s.repman.GetClusters() {
		if s.clusterVisible(ctx, name) {
			names = append(names, name)
		}
	}
	return names
}

// addResourceTemplate registers a cluster resource template behind the cluster's
// visibility check: resources expose the same read data as the public cluster
// endpoint, so that is the gate.
func (s *MCPServer) addResourceTemplate(tpl mcp.ResourceTemplate, handler mcpserver.ResourceTemplateHandlerFunc) {
	s.mcp.AddResourceTemplate(tpl, func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
		if s.conf.MCPAuthEnabled {
			clusterName := extractClusterFromURI(req.Params.URI)
			if clusterName == "" {
				return nil, fmt.Errorf("forbidden: cannot resolve the cluster of %s", req.Params.URI)
			}
			if ok, reason := s.authorize(ctx, clusterName, "/api/clusters/"+clusterName); !ok {
				return nil, fmt.Errorf("%s", reason)
			}
		}
		return handler(ctx, req)
	})
}
