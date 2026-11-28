// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"errors"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The indexes of an analytic child's denormalize mode (CDC_CORE.md "Denormalize"), on a DuckDB
// child for instance: the three REST routes of replication-plugin-index-*, one tool each. A
// DuckDB child itself is created by cluster-create-child (destination duckdb).
func (s *MCPServer) registerAnalyticChildReadTools() {
	s.addTool(
		mcp.NewTool("list-cluster-analytic-indexes",
			mcp.WithDescription("List the indexes of an analytic child (a DuckDB child, for instance): each a denormalizing query run on the parent and the denormalized table it keeps up to date on the child (slaveSchema.slaveTable). ready names the indexes whose init is done (the table filled once from a consistent snapshot of the parent); pinned: a config file sets the list. Needs cluster-settings."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the analytic child cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			out := map[string]any{"indexes": cl.Conf.ReplicationPluginIndexList(), "ready": []string{}, "pinned": cl.ReplicationPluginIndexesPinned()}
			var ready []string
			for _, st := range cl.ReplicationPluginStatuses() {
				ready = append(ready, st.IndexesReady...)
			}
			if ready != nil {
				out["ready"] = ready
			}
			return mcp.NewToolResultText(toJSON(out)), nil
		},
	)
}

// registerAnalyticChildTools: the changes of the indexes, with the write tools.
func (s *MCPServer) registerAnalyticChildTools() {
	s.addTool(
		mcp.NewTool("cluster-set-analytic-index",
			mcp.WithDescription("Add an index to an analytic child, or replace the index of that name: a denormalizing query (a SELECT run on the parent, inner joins, every table with its schema, returning the primary key columns of every table it reads, each under one name) and the denormalized table it fills on the child. The child's plugin restarts; a new or changed index starts with its init (the table made from the query's result, then filled from a consistent snapshot of the parent), then each change on a table the query reads recomputes the rows of the changed keys. Nothing is changed on the parent. Needs cluster-settings."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the analytic child cluster")),
			mcp.WithString("index_name", mcp.Required(), mcp.Description("Name of the index: lower case letters, digits, - and _")),
			mcp.WithString("sql", mcp.Required(), mcp.Description("The denormalizing query, a SELECT run on the parent")),
			mcp.WithString("slave_schema", mcp.Required(), mcp.Description("Schema of the denormalized table on the child")),
			mcp.WithString("slave_table", mcp.Required(), mcp.Description("Name of the denormalized table on the child")),
			mcp.WithNumber("max_keys_per_batch", mcp.Description("Changed keys one recompute restricts the query to, more are split; 0 (default): 10000")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			idx := config.ReplicationPluginIndex{
				Name:            req.GetString("index_name", ""),
				SQL:             req.GetString("sql", ""),
				SlaveSchema:     req.GetString("slave_schema", ""),
				SlaveTable:      req.GetString("slave_table", ""),
				MaxKeysPerBatch: req.GetInt("max_keys_per_batch", 0),
			}
			if err := cl.PutReplicationPluginIndex(idx); err != nil {
				return analyticIndexError(err), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{"name": idx.Name})), nil
		},
	)
	s.addTool(
		mcp.NewTool("cluster-remove-analytic-index",
			mcp.WithDescription("Remove an index of an analytic child: its denormalized table is no longer kept up to date and stays on the child as it is. Needs cluster-settings."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the analytic child cluster")),
			mcp.WithString("index_name", mcp.Required(), mcp.Description("Name of the index")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			name := req.GetString("index_name", "")
			if err := cl.DeleteReplicationPluginIndex(name); err != nil {
				return analyticIndexError(err), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{"name": name})), nil
		},
	)
}

// analyticIndexError: a list a config file sets is said as such (the REST route's 409).
func analyticIndexError(err error) *mcp.CallToolResult {
	if errors.Is(err, cluster.ErrReplPluginIndexesPinned) {
		return mcp.NewToolResultErrorf("refused (pinned): %s", err)
	}
	return mcp.NewToolResultError(err.Error())
}
