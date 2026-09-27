// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerPrompts registers pre-defined DBA prompt templates.
func (s *MCPServer) registerPrompts() {
	s.mcp.AddPrompt(
		mcp.NewPrompt("diagnose-cluster",
			mcp.WithPromptDescription("Guide through a systematic cluster health diagnosis"),
			mcp.WithArgument("cluster_name", mcp.ArgumentDescription("Name of the cluster to diagnose"), mcp.RequiredArgument()),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			clusterName := req.Params.Arguments["cluster_name"]
			if clusterName == "" {
				return nil, fmt.Errorf("cluster_name is required")
			}
			return mcp.NewGetPromptResult(
				"Cluster Health Diagnosis",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf(`Perform a complete health diagnosis for the MariaDB cluster "%s".

Follow these steps in order:

1. Call get-cluster-health to get the current health status and SLA information.
2. Call get-cluster-error-states to retrieve all active alerts and errors.
3. Call get-cluster-topology to understand the current server topology (master/slaves).
4. For each server in the topology:
   a. Call check-server-is-master or check-server-is-slave to confirm role.
   b. Call check-server-is-late to check replication lag.
   c. If lag exists, call get-server-status to look at Seconds_Behind_Master and IO/SQL thread states.
5. Call get-cluster-logs to review recent orchestrator log entries for warnings or errors.
6. Call get-cluster-crashes to check for recent failover events.

Summarize findings with:
- Current master and replica count
- Replication health (lag, thread states)
- Active alerts or error states
- Recent crash or failover events
- Overall health assessment and recommended actions`, clusterName)),
					),
				},
			), nil
		},
	)

	s.mcp.AddPrompt(
		mcp.NewPrompt("failover-checklist",
			mcp.WithPromptDescription("Pre-failover readiness checklist for a cluster"),
			mcp.WithArgument("cluster_name", mcp.ArgumentDescription("Name of the cluster"), mcp.RequiredArgument()),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			clusterName := req.Params.Arguments["cluster_name"]
			if clusterName == "" {
				return nil, fmt.Errorf("cluster_name is required")
			}
			return mcp.NewGetPromptResult(
				"Pre-Failover Checklist",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf(`Run a pre-failover readiness assessment for cluster "%s".

Check the following conditions before recommending or executing a failover:

1. Call get-cluster-health to verify cluster is in a state where failover is meaningful.
2. Call get-cluster-topology to identify the current master and available replicas.
3. For each replica:
   a. Call check-server-is-late to verify replication lag is within acceptable bounds.
   b. Call get-server-status to check IO_Running and SQL_Running thread states.
4. Call get-cluster-settings to review:
   - failover-mode (automatic vs. manual)
   - failover-max-slave-delay threshold
   - failover-limit and current failover count
5. Call list-backups to confirm a recent backup exists before the failover.
6. Call get-cluster-error-states to check for pre-existing alerts.

Report:
- Whether failover prerequisites are met
- Which replica is the best candidate (least lag, most advanced GTID)
- Any blocking conditions that must be resolved first
- Recommended failover type (switchover preferred if master is accessible)`, clusterName)),
					),
				},
			), nil
		},
	)

	s.mcp.AddPrompt(
		mcp.NewPrompt("backup-status-report",
			mcp.WithPromptDescription("Generate a backup status report for a cluster"),
			mcp.WithArgument("cluster_name", mcp.ArgumentDescription("Name of the cluster"), mcp.RequiredArgument()),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			clusterName := req.Params.Arguments["cluster_name"]
			if clusterName == "" {
				return nil, fmt.Errorf("cluster_name is required")
			}
			return mcp.NewGetPromptResult(
				"Backup Status Report",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf(`Generate a backup status report for cluster "%s".

Collect the following information:

1. Call list-backups to get all registered physical and logical backups.
2. Call get-backup-stats to get aggregated backup statistics.
3. Call list-restic-snapshots to get Restic snapshot inventory.
4. Call get-restic-stats to get repository size and deduplication stats.
5. Call get-restic-task-queue to check for pending or failed backup tasks.

Report should include:
- Date and type of the most recent successful backup
- Backup retention compliance (how many backups exist vs. expected)
- Restic repository health and total storage usage
- Any failed or queued tasks requiring attention
- RPO assessment: time since last successful backup
- Recommendations for backup gaps or retention issues`, clusterName)),
					),
				},
			), nil
		},
	)

	s.mcp.AddPrompt(
		mcp.NewPrompt("replication-lag-analysis",
			mcp.WithPromptDescription("Analyze replication lag across all replicas in a cluster"),
			mcp.WithArgument("cluster_name", mcp.ArgumentDescription("Name of the cluster"), mcp.RequiredArgument()),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			clusterName := req.Params.Arguments["cluster_name"]
			if clusterName == "" {
				return nil, fmt.Errorf("cluster_name is required")
			}
			return mcp.NewGetPromptResult(
				"Replication Lag Analysis",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf(`Perform a replication lag analysis for cluster "%s".

Steps:

1. Call get-cluster-topology to enumerate all replicas.
2. For each replica server:
   a. Call check-server-is-late to get current lag and seconds_behind value.
   b. Call get-server-status to retrieve:
      - Seconds_Behind_Master
      - Slave_IO_Running and Slave_SQL_Running states
      - Read_Master_Log_Pos vs. Exec_Master_Log_Pos gap
3. Call get-server-variables for each lagging replica to check:
   - slave_parallel_workers (parallel replication configuration)
   - innodb_flush_log_at_trx_commit (disk I/O impact)
   - sync_binlog
4. Call get-server-processlist on the master to identify long-running transactions.
5. Call get-server-slow-queries on the master to identify heavy write operations.

Analysis should cover:
- Which replicas are lagging and by how much
- Root cause hypothesis (I/O bound, CPU bound, long transactions, DDL)
- Whether parallel replication is configured and effective
- Actionable recommendations to reduce lag`, clusterName)),
					),
				},
			), nil
		},
	)

	s.mcp.AddPrompt(
		mcp.NewPrompt("query-performance-analysis",
			mcp.WithPromptDescription("Analyze query performance on a specific server"),
			mcp.WithArgument("cluster_name", mcp.ArgumentDescription("Name of the cluster"), mcp.RequiredArgument()),
			mcp.WithArgument("server_name", mcp.ArgumentDescription("Server name (host:port)"), mcp.RequiredArgument()),
		),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			clusterName := req.Params.Arguments["cluster_name"]
			serverName := req.Params.Arguments["server_name"]
			if clusterName == "" {
				return nil, fmt.Errorf("cluster_name is required")
			}
			if serverName == "" {
				return nil, fmt.Errorf("server_name is required")
			}
			return mcp.NewGetPromptResult(
				"Query Performance Analysis",
				[]mcp.PromptMessage{
					mcp.NewPromptMessage(
						mcp.RoleUser,
						mcp.NewTextContent(fmt.Sprintf(`Analyze query performance on server "%s" in cluster "%s".

Steps:

1. Call get-server-processlist for "%s" to see active queries and their duration.
2. Call get-server-slow-queries for "%s" to retrieve slow query log entries from Performance Schema.
3. Call get-server-status for "%s" to check:
   - Questions, Com_select, Com_insert, Com_update, Com_delete rates
   - Handler_read_* counters (table scan indicators)
   - Created_tmp_disk_tables (memory pressure)
   - Threads_running vs. Threads_connected
4. Call get-server-variables for "%s" to review:
   - query_cache_size (if applicable)
   - tmp_table_size and max_heap_table_size
   - innodb_buffer_pool_size utilization
   - long_query_time threshold

Report should include:
- Top 5 slowest query patterns with execution count and average duration
- Queries causing table scans (no index usage)
- Connection and concurrency pressure indicators
- Specific tuning recommendations for identified bottlenecks`, clusterName, serverName, serverName, serverName, serverName, serverName)),
					),
				},
			), nil
		},
	)
}
