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

// registerResources registers all MCP resources (static and dynamic) for read access.
func (s *MCPServer) registerResources() {
	s.registerStaticResources()
	s.registerClusterResourceTemplates()
	s.registerServerResourceTemplates()
}

// registerStaticResources registers global/static resources.
func (s *MCPServer) registerStaticResources() {
	s.mcp.AddResource(
		mcp.NewResource("repman://status", "Server Status",
			mcp.WithResourceDescription("Global replication-manager server status"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return jsonResource(req.Params.URI,
				map[string]string{
					"status":  s.repman.GetStatus(),
					"version": s.repman.GetVersion(),
				},
			), nil
		},
	)

	s.mcp.AddResource(
		mcp.NewResource("repman://version", "Server Version",
			mcp.WithResourceDescription("replication-manager version information"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return jsonResource(req.Params.URI,
				map[string]string{
					"version":     s.repman.GetVersion(),
					"fullVersion": s.repman.GetFullVersion(),
				},
			), nil
		},
	)

	s.mcp.AddResource(
		mcp.NewResource("repman://clusters", "All Clusters",
			mcp.WithResourceDescription("List of all monitored clusters"),
			mcp.WithMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			// Only the clusters the caller may see (account there, token scope).
			return jsonResource(req.Params.URI, s.visibleClusterNames(ctx)), nil
		},
	)
}

// resolveCluster gets a cluster from a URI, returning an error if not found.
func (s *MCPServer) resolveCluster(uri string) (*cluster.Cluster, error) {
	name := extractClusterFromURI(uri)
	if name == "" {
		return nil, fmt.Errorf("could not extract cluster name from URI: %s", uri)
	}
	cl := s.repman.GetClusterByName(name)
	if cl == nil {
		return nil, fmt.Errorf("cluster not found: %s", name)
	}
	return cl, nil
}

// resolveClusterAndServer gets a cluster and server from a URI.
func (s *MCPServer) resolveClusterAndServer(uri string) (*cluster.Cluster, *cluster.ServerMonitor, error) {
	params := extractParamsFromURI(uri, "repman://clusters/{clusterName}/servers/{serverName}")
	clName := params["clusterName"]
	srvName := params["serverName"]
	if clName == "" || srvName == "" {
		return nil, nil, fmt.Errorf("could not extract cluster/server from URI: %s", uri)
	}
	cl := s.repman.GetClusterByName(clName)
	if cl == nil {
		return nil, nil, fmt.Errorf("cluster not found: %s", clName)
	}
	node := resolveServer(cl, srvName)
	if node == nil {
		return nil, nil, fmt.Errorf("server not found: %s in cluster %s", srvName, clName)
	}
	return cl, node, nil
}

// registerClusterResourceTemplates registers per-cluster resource templates.
func (s *MCPServer) registerClusterResourceTemplates() {
	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}", "Cluster Details",
			mcp.WithTemplateDescription("Details and state of a specific cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/settings", "Cluster Settings",
			mcp.WithTemplateDescription("Configuration settings for a specific cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.Conf), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/tags", "Cluster Tags",
			mcp.WithTemplateDescription("Tags associated with a specific cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.Configurator.GetDBModuleTags()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/servers", "Cluster Servers",
			mcp.WithTemplateDescription("All database servers in the cluster topology"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetServers()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/master", "Cluster Master",
			mcp.WithTemplateDescription("Current master server of the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetMaster()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/slaves", "Cluster Replicas",
			mcp.WithTemplateDescription("Replica servers in the cluster topology"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetSlaves()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/proxies", "Cluster Proxies",
			mcp.WithTemplateDescription("Proxy servers in the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetProxies()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/logs", "Cluster Logs",
			mcp.WithTemplateDescription("Recent log entries for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.Log.Buffer), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/alerts", "Cluster Alerts",
			mcp.WithTemplateDescription("Open errors and warnings for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, map[string]interface{}{
				"errors":   cl.GetStateMachine().GetOpenErrors(),
				"warnings": cl.GetStateMachine().GetOpenWarnings(),
			}), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/topology/crashes", "Cluster Crashes",
			mcp.WithTemplateDescription("Recorded crash events for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetCrashes()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/health", "Cluster Health",
			mcp.WithTemplateDescription("Peer health status for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetPeerHealth()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/backups", "Cluster Backups",
			mcp.WithTemplateDescription("Backup metadata for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetBackups()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/backups/stats", "Backup Statistics",
			mcp.WithTemplateDescription("Backup statistics for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetBackupStat()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/restic/snapshots", "Restic Snapshots",
			mcp.WithTemplateDescription("Restic backup snapshots for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetSnapshots()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/restic/task-queue", "Restic Task Queue",
			mcp.WithTemplateDescription("Restic task queue status for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			queue, _ := cl.ResticGetQueue()
			return jsonResource(req.Params.URI, queue), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/jobs", "Cluster Jobs",
			mcp.WithTemplateDescription("Scheduled and running jobs for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			entries, _ := cl.JobsGetEntries()
			return jsonResource(req.Params.URI, entries), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/queryrules", "Query Rules",
			mcp.WithTemplateDescription("Query routing rules for the cluster"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			cl, err := s.resolveCluster(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, cl.GetQueryRules()), nil
		},
	)
}

// registerServerResourceTemplates registers per-server resource templates.
func (s *MCPServer) registerServerResourceTemplates() {
	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/status", "Server Status",
			mcp.WithTemplateDescription("Status variables for a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetStatus()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/variables", "Server Variables",
			mcp.WithTemplateDescription("Configuration variables for a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetVariables(false)), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/processlist", "Server Processlist",
			mcp.WithTemplateDescription("Active queries and connections on a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetProcessList()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/slow-queries", "Slow Queries",
			mcp.WithTemplateDescription("Slow query log entries for a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetSlowLog()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/tables", "Server Tables",
			mcp.WithTemplateDescription("Table list for a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetTables()), nil
		},
	)

	s.addResourceTemplate(
		mcp.NewResourceTemplate("repman://clusters/{clusterName}/servers/{serverName}/error-log", "Server Error Log",
			mcp.WithTemplateDescription("Error log entries for a specific database server"),
			mcp.WithTemplateMIMEType("application/json"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			_, node, err := s.resolveClusterAndServer(req.Params.URI)
			if err != nil {
				return nil, err
			}
			return jsonResource(req.Params.URI, node.GetErrorLog().Buffer), nil
		},
	)
}

// jsonResource wraps a value as a JSON TextResourceContents slice.
func jsonResource(uri string, v interface{}) []mcp.ResourceContents {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      uri,
			MIMEType: "application/json",
			Text:     toJSON(v),
		},
	}
}
