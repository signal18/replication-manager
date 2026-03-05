// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
)

// proxyContext holds a resolved cluster and proxy pair.
type proxyContext struct {
	cl  *cluster.Cluster
	prx cluster.DatabaseProxy
}

// getProxyHelper retrieves a cluster and DatabaseProxy by name.
func (s *MCPServer) getProxyHelper(clusterName, proxyName string) (*proxyContext, error) {
	if clusterName == "" {
		return nil, fmt.Errorf("cluster_name is required")
	}
	if proxyName == "" {
		return nil, fmt.Errorf("proxy_name is required")
	}
	cl := s.repman.GetClusterByName(clusterName)
	if cl == nil {
		return nil, fmt.Errorf("cluster not found: %s", clusterName)
	}
	prx := cl.GetProxyFromName(proxyName)
	if prx == nil {
		return nil, fmt.Errorf("proxy not found: %s in cluster %s", proxyName, clusterName)
	}
	return &proxyContext{cl: cl, prx: prx}, nil
}

// registerProxyReadTools registers read-only proxy tools.
func (s *MCPServer) registerProxyReadTools() {
	s.mcp.AddTool(
		mcp.NewTool("list-proxies",
			mcp.WithDescription("List all load balancers and routers configured for a cluster (ProxySQL, MaxScale, HAProxy, etc.). Returns each proxy's name, type, host:port, state, and backend configuration. replication-manager automatically updates proxy backends when the topology changes (failover, switchover). Use this to verify proxies are routing to the correct master after a topology change."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetProxies())), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("get-proxy",
			mcp.WithDescription("Get detailed configuration and status for a specific proxy. Includes backend server list, current write host, read host pool, connection counts, and state. Use list-proxies first to find the proxy_name. The proxy_name is typically in host:port format (e.g. proxysql:6032 or maxscale:3306)."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("proxy_name", mcp.Required(), mcp.Description("Proxy name from list-proxies (typically host:port)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc, err := s.getProxyHelper(req.GetString("cluster_name", ""), req.GetString("proxy_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(pc.prx)), nil
		},
	)
}

// registerProxyWriteTools registers write/action proxy tools.
func (s *MCPServer) registerProxyWriteTools() {
	s.mcp.AddTool(
		mcp.NewTool("proxy-start",
			mcp.WithDescription("Start a stopped proxy service. replication-manager will start the proxy process via the configured service manager and then reconfigure its backends to match the current topology. Use list-proxies to find the proxy_name. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("proxy_name", mcp.Required(), mcp.Description("Proxy name from list-proxies")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc, err := s.getProxyHelper(req.GetString("cluster_name", ""), req.GetString("proxy_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go pc.cl.StartProxyService(pc.prx)
			return mcp.NewToolResultText(`{"status":"proxy start initiated"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("proxy-stop",
			mcp.WithDescription("Stop a running proxy service. This will immediately cut all application connections routed through this proxy. Use cluster-stop-traffic for a safer drain-first approach. Use list-proxies to find the proxy_name. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("proxy_name", mcp.Required(), mcp.Description("Proxy name from list-proxies")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc, err := s.getProxyHelper(req.GetString("cluster_name", ""), req.GetString("proxy_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go pc.cl.StopProxyService(pc.prx)
			return mcp.NewToolResultText(`{"status":"proxy stop initiated"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("proxy-provision",
			mcp.WithDescription("Provision a proxy service from scratch: deploy the proxy process via the configured orchestrator (Docker, OpenSVC) and configure its backends for the current cluster topology. Use for initial setup or after proxy-unprovision. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("proxy_name", mcp.Required(), mcp.Description("Proxy name from list-proxies")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc, err := s.getProxyHelper(req.GetString("cluster_name", ""), req.GetString("proxy_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go pc.cl.InitProxyService(pc.prx)
			return mcp.NewToolResultText(`{"status":"proxy provision initiated"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("proxy-unprovision",
			mcp.WithDescription("Remove a provisioned proxy service: stop the proxy and tear down its deployment (container, service unit). Use proxy-stop if you only want to stop the process. Use this only when decommissioning a proxy permanently. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("proxy_name", mcp.Required(), mcp.Description("Proxy name from list-proxies")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			pc, err := s.getProxyHelper(req.GetString("cluster_name", ""), req.GetString("proxy_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go pc.cl.UnprovisionProxyService(pc.prx)
			return mcp.NewToolResultText(`{"status":"proxy unprovision initiated"}`), nil
		},
	)
}
