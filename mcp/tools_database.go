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
	"github.com/signal18/replication-manager/config"
)

// getServerHelper retrieves a cluster and ServerMonitor by name.
func (s *MCPServer) getServerHelper(clusterName, serverName string) (*cluster.Cluster, *cluster.ServerMonitor, error) {
	if clusterName == "" {
		return nil, nil, fmt.Errorf("cluster_name is required")
	}
	if serverName == "" {
		return nil, nil, fmt.Errorf("server_name is required")
	}
	cl := s.repman.GetClusterByName(clusterName)
	if cl == nil {
		return nil, nil, fmt.Errorf("cluster not found: %s", clusterName)
	}
	node := resolveServer(cl, serverName)
	if node == nil {
		return nil, nil, fmt.Errorf("server not found: %s in cluster %s (use the name, host or id from get-cluster-topology)", serverName, clusterName)
	}
	return cl, node, nil
}

// registerDatabaseReadTools registers read-only database/server tools.
func (s *MCPServer) registerDatabaseReadTools() {
	s.addTool(
		mcp.NewTool("get-server-status",
			mcp.WithDescription("Get all SHOW STATUS variables for a specific server. Key fields: Seconds_Behind_Master (replication lag), Slave_IO_Running/Slave_SQL_Running (replication threads), Threads_running (active queries), Questions (query rate), Com_select/insert/update/delete (query type breakdown), Handler_read_rnd_next (table scan indicator), Innodb_buffer_pool_read_requests vs reads (buffer pool hit rate). Use server_name in host:port format (e.g. db1:3306)."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetStatus())), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-server-variables",
			mcp.WithDescription("Get all SHOW VARIABLES for a specific server. Key variables to review: innodb_buffer_pool_size (should be ~70% of RAM), slave_parallel_workers (parallel replication), sync_binlog and innodb_flush_log_at_trx_commit (durability vs performance), long_query_time (slow query threshold), max_connections, tmp_table_size/max_heap_table_size (temp table limits). Useful for tuning analysis and comparing configuration across nodes."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetVariables(false))), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-server-processlist",
			mcp.WithDescription("Get the current process list (SHOW FULL PROCESSLIST) for a server: all active connections with query text, state, user, database, and execution time. Use to identify long-running queries, blocked connections, or replication threads. Returns the process ID needed for server-kill-query. Check this first when a server appears slow or overloaded."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetProcessList())), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-server-slow-queries",
			mcp.WithDescription("Get slow query digest entries from Performance Schema (events_statements_summary_by_digest). Returns normalized query patterns with total/average execution time, call count, rows examined, and rows sent. Use to identify the top queries contributing to latency. Requires performance_schema=ON on the server. More useful than the raw slow log for identifying query patterns."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetSlowLog())), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-server-error-log",
			mcp.WithDescription("Get the MariaDB/MySQL error log buffer for a specific server. Contains startup/shutdown events, InnoDB recovery messages, replication errors, crash information, and plugin errors. Use when a server behaves unexpectedly or after a restart to see what happened. This is the database server's own log, not the replication-manager orchestrator log (use get-cluster-logs for that)."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetErrorLog().Buffer)), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-server-tables",
			mcp.WithDescription("Get the list of all user tables across all databases on a server, with metadata (engine, row count, data size, index size). Useful for identifying large tables, MyISAM tables that should be InnoDB, or tables lacking primary keys (required for row-based replication). Compare across master and replicas to detect schema drift."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(node.GetTables())), nil
		},
	)

	s.addTool(
		mcp.NewTool("check-server-is-master",
			mcp.WithDescription("Check whether a specific server is currently the active master (read-write primary) for the cluster. Returns is_master=true/false. Use to confirm which server is writable before directing traffic, or to verify a switchover/failover completed correctly."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			master := cl.GetMaster()
			isMaster := master != nil && master.URL == node.URL
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"server":    req.GetString("server_name", ""),
				"is_master": isMaster,
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("check-server-is-slave",
			mcp.WithDescription("Check whether a specific server is currently an active replica (read-only, replicating from master). Returns is_slave=true/false. Use to confirm replication topology after a switchover/failover, or to verify a server is replicating before adding it to a read replica pool."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"server":   req.GetString("server_name", ""),
				"is_slave": node.IsSlave,
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("check-server-is-late",
			mcp.WithDescription("Check if a replica has replication lag above the failover-max-slave-delay threshold. Returns is_late=true/false and seconds_behind (current lag in seconds). A lagging replica cannot be safely promoted during failover. Use this to identify which replicas are eligible for promotion, or to monitor lag trends. 0 seconds means fully in sync."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"server":         req.GetString("server_name", ""),
				"is_late":        node.IsSlaveLate(),
				"seconds_behind": node.GetReplicationDelay(),
			})), nil
		},
	)
}

// registerDatabaseWriteTools registers write/action database tools.
func (s *MCPServer) registerDatabaseWriteTools() {
	s.addTool(
		mcp.NewTool("server-start",
			mcp.WithDescription("Start a stopped database server in the cluster. replication-manager will issue the start command via the configured service manager (systemd, OpenSVC, Docker, etc.). After starting, the monitoring loop will detect the server and attempt to rejoin it to replication.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go cl.StartDatabaseService(node)
			return mcp.NewToolResultText(`{"status":"server start initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-stop",
			mcp.WithDescription("Stop a running database server. If the server is the current master, this will trigger failure detection and potentially automatic failover depending on failover-mode. Consider using server-set-maintenance first to prevent unwanted failover, or use cluster-switchover to safely move the master role before stopping.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go cl.StopDatabaseService(node)
			return mcp.NewToolResultText(`{"status":"server stop initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-restart",
			mcp.WithDescription("Restart a specific database server. Queues a restart in the monitoring loop (sets restart cookie). For a safe restart of the current master without downtime, use cluster-rolling-restart instead.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			_ = cl
			node.SetRestartCookie()
			return mcp.NewToolResultText(`{"status":"server restart queued"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-backup-physical",
			mcp.WithDescription("Trigger a physical backup on a specific server (not necessarily the master). Preferred target is a replica to avoid I/O impact on the master. Uses Mariabackup or xtrabackup as configured. Use cluster-physical-backup to target the master automatically.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go node.JobBackupPhysical()
			return mcp.NewToolResultText(`{"status":"physical backup initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-backup-logical",
			mcp.WithDescription("Trigger a logical backup (mysqldump or mydumper, as configured by backup-logical-type) on a specific server, preferably a replica. Runs in the background; follow it with list-backups. Needs the db-backup grant."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name, host or id as shown by get-cluster-topology")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go node.JobBackupLogical(context.Background())
			return mcp.NewToolResultText(`{"status":"logical backup initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-logical-backup-splitdump",
			mcp.WithDescription("Trigger a logical mysqldump backup in splitdump format (one directory with one file per table, mydumper-like, restorable in parallel) on a specific server. Requires backup-logical-type=mysqldump and backup-mysqldump-splitdump=true on the cluster; refused otherwise, with the setting to change. Runs in the background; follow it with list-backups. Needs the db-backup grant."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name, host or id as shown by get-cluster-topology")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if cl.Conf.BackupLogicalType != config.ConstBackupLogicalTypeMysqldump {
				return mcp.NewToolResultErrorf("splitdump needs backup-logical-type=mysqldump, the cluster uses %s", cl.Conf.BackupLogicalType), nil
			}
			if !cl.Conf.BackupMysqldumpSplitDump {
				return mcp.NewToolResultError("splitdump is off: switch backup-mysqldump-splitdump on the cluster first (cluster-switch-setting, needs cluster-settings)"), nil
			}
			go node.JobBackupLogical(context.Background())
			return mcp.NewToolResultText(`{"status":"logical splitdump backup initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-restore-logical-backup",
			mcp.WithDescription("Restore (reseed) a server from the cluster's last logical backup: the server is rebuilt from the dump and re-attached to replication. Destructive for the target server's current data. Use only on a replica that is broken or diverged. Runs in the background; follow it with get-cluster-topology and get-cluster-logs. Needs the db-restore grant."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name, host or id as shown by get-cluster-topology")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go func() {
				if err := node.JobReseedLogicalBackup(context.Background(), "default"); err != nil {
					s.logger.Errorf("MCP server-restore-logical-backup %s: %v", node.URL, err)
				}
			}()
			return mcp.NewToolResultText(`{"status":"logical restore initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-restore-physical-backup",
			mcp.WithDescription("Restore (reseed) a server from the cluster's last physical backup (Mariabackup/xtrabackup): the server's data directory is replaced and replication re-attached. Destructive for the target server's current data. Use only on a replica that is broken or diverged. Runs in the background; follow it with get-cluster-topology and get-cluster-logs. Needs the db-restore grant."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name, host or id as shown by get-cluster-topology")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if err := node.JobReseedPhysicalBackup("default"); err != nil {
				return mcp.NewToolResultErrorf("physical restore refused: %v", err), nil
			}
			return mcp.NewToolResultText(`{"status":"physical restore initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-optimize",
			mcp.WithDescription("Run OPTIMIZE TABLE on all user tables on a specific server. Reclaims fragmented InnoDB space and rebuilds indexes. Run on replicas first during off-peak hours to minimize impact. Avoid running on the master during peak traffic.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			go node.JobOptimize()
			return mcp.NewToolResultText(`{"status":"optimize initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-set-maintenance",
			mcp.WithDescription("Toggle maintenance mode for a server (on/off). A server in maintenance state is excluded from HA logic: replication-manager will not trigger failover if this server goes down, and will not try to reconfigure it. Use before stopping or patching a server to prevent unwanted failover. Call again to exit maintenance mode. Returns the new maintenance state.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			node.SwitchMaintenance()
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"status":      "maintenance toggled",
				"maintenance": node.IsMaintenance,
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-set-read-only",
			mcp.WithDescription("Set a server to read-only mode (SET GLOBAL read_only=ON). Prevents writes on a replica that may have been accidentally set writable, or prepares the current master for a manual switchover. Note: replication-manager's monitoring loop manages read_only automatically — this may be overridden on the next monitoring tick.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db2:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			_, _ = node.SetReadOnly()
			return mcp.NewToolResultText(`{"status":"server set to read-only"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-set-read-write",
			mcp.WithDescription("Set a server to read-write mode (SET GLOBAL read_only=OFF). Use only when you intend this server to accept writes — typically only the master should be read-write. Setting a replica to read-write while replication is running risks data inconsistency. For safe master promotion, use cluster-switchover instead.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			node.SetReadWrite()
			return mcp.NewToolResultText(`{"status":"server set to read-write"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("server-kill-query",
			mcp.WithDescription("Kill a specific query or connection on a server by process ID (KILL QUERY <id>). Use get-server-processlist first to find the process ID of the query to kill. Useful for terminating long-running queries, blocked transactions, or idle connections. Killing the replication SQL thread process_id will break replication — avoid killing system processes.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("Server name in host:port format (e.g. db1:3306)")),
			mcp.WithString("process_id", mcp.Required(), mcp.Description("Process ID from get-server-processlist to kill")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			_, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			processID := req.GetString("process_id", "")
			if processID == "" {
				return mcp.NewToolResultError("process_id is required"), nil
			}
			node.KillThread(processID)
			return mcp.NewToolResultText(toJSON(map[string]string{
				"status":     "query kill initiated",
				"process_id": processID,
			})), nil
		},
	)
}
