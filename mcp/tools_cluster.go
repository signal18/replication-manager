// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
)

// registerReadOnlyTools registers all read-only MCP tools (Phase 1).
func (s *MCPServer) registerReadOnlyTools() {
	s.registerClusterReadTools()
	s.registerDatabaseReadTools()
	s.registerBackupReadTools()
	s.registerProxyReadTools()
}

// registerWriteTools registers all write/action MCP tools (Phase 2).
func (s *MCPServer) registerWriteTools() {
	s.registerClusterWriteTools()
	s.registerDatabaseWriteTools()
	s.registerBackupWriteTools()
	s.registerProxyWriteTools()
}

// registerClusterReadTools registers read-only cluster tools.
func (s *MCPServer) registerClusterReadTools() {
	s.addTool(
		mcp.NewTool("list-clusters",
			mcp.WithDescription("List the names of all database clusters currently monitored by replication-manager. Always call this first if you do not already know the cluster name."),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Only the clusters the caller may see (account there, token scope).
			return mcp.NewToolResultText(toJSON(s.visibleClusterNames(ctx))), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-health",
			mcp.WithDescription("Get the high-level health status of a cluster. Returns: isDown (no master), isMasterDown (master unreachable), isFailable (a replica can be promoted), isProvisioned (replication bootstrapped). Use this as the first diagnostic step."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetPeerHealth())), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-topology",
			mcp.WithDescription("Get the full topology of a cluster: master server, slave replicas, and proxies. Each server includes state (Master/Slave/Suspect/Failed), replication threads (slaveIoRunning, slaveSqlRunning), lag (secondsBehindMaster), GTID position, and error details. Use this to understand who is master, check replication health, and identify lag or errors."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"master":  cl.GetMaster(),
				"slaves":  cl.GetSlaves(),
				"proxies": cl.GetProxies(),
				"servers": cl.GetServers(),
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-settings",
			mcp.WithDescription("Get the full configuration for a cluster. Useful for verifying current settings such as failover-mode (manual/automatic), replication topology, backup schedule, proxy configuration."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.Conf)), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-alerts",
			mcp.WithDescription("Get all currently active errors and warnings for a cluster. Errors indicate critical problems (e.g. ERR00012=no master, ERR00021=cluster down, ERR00076=replication stopped). Warnings indicate non-critical issues (e.g. WARN0108=default password, WARN0111=no logical backup). Always check this when diagnosing a problem."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"errors":   cl.GetStateMachine().GetOpenErrors(),
				"warnings": cl.GetStateMachine().GetOpenWarnings(),
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-logs",
			mcp.WithDescription("Get recent orchestrator log entries for a cluster. Useful for seeing what replication-manager has been doing: topology changes, failover attempts, replication corrections, backup jobs. Complements get-cluster-alerts which shows current state rather than history."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.Log.Buffer)), nil
		},
	)

	s.addTool(
		mcp.NewTool("get-cluster-crashes",
			mcp.WithDescription("Get the history of crash and failover events for a cluster. Each entry records when the master was lost, which replica was promoted, and the GTID state at the time. Use this to understand past incidents and assess data loss risk."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetCrashes())), nil
		},
	)

	s.addTool(
		mcp.NewTool("last-crash-lost-event",
			mcp.WithDescription("Get the lost events of the last crash of a server: the transactions the old master had committed but that never reached the promoted replica, captured as a binlog delta at rejoin and decoded to SQL. Returns the crash record (when, who was promoted, GTID positions, delta counters: transactions, row events, DDL, statement DML, flashable), the decoded delta text (first page) and the rejoin methods available. Use it to assess data loss after a failover and decide between replaying or flashing back the delta. No crash record means no data was lost on that server."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("server_name", mcp.Required(), mcp.Description("The old master: server name, host or id as shown by get-cluster-crashes")),
			mcp.WithString("file", mcp.Description("\"delta\" (default) for the lost events, \"flashback\" for their inverse")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, node, err := s.getServerHelper(req.GetString("cluster_name", ""), req.GetString("server_name", ""))
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			crash := cl.GetLatestCrashForServer(node.URL)
			if crash == nil {
				return mcp.NewToolResultText(`{"crash":null,"message":"no crash record for this server: nothing was lost"}`), nil
			}
			path := crash.DeltaDecoded
			if req.GetString("file", "") == "flashback" {
				path = crash.DeltaFlashbackDecoded
			}
			var page *cluster.LostEventsPage
			if path != "" {
				page, err = cluster.ReadLostEventsPage(path, 0, 64*1024)
				if err != nil {
					return mcp.NewToolResultErrorf("could not read decoded lost events: %v", err), nil
				}
			}
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"crash":         crash,
				"file":          path,
				"page":          page,
				"rejoinMethods": cl.RejoinMethodsStatus(),
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("check-cluster-error-state",
			mcp.WithDescription("Check whether a specific error or warning code is currently active for a cluster. Returns active=true/false. Useful for scripted checks or confirming a specific issue is resolved. Common codes: ERR00010 (no slave), ERR00012 (no master), ERR00021 (cluster down), ERR00041 (replication lag), ERR00076 (replication stopped)."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("error_code", mcp.Required(), mcp.Description("Error or warning code to check (e.g. ERR00012, WARN0108)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			code := req.GetString("error_code", "")
			active := cl.GetStateMachine().IsInState(code)
			return mcp.NewToolResultText(toJSON(map[string]interface{}{
				"cluster":    req.GetString("cluster_name", ""),
				"error_code": code,
				"active":     active,
			})), nil
		},
	)
}

// registerClusterWriteTools registers write/action cluster tools.
func (s *MCPServer) registerClusterWriteTools() {
	s.addTool(
		mcp.NewTool("cluster-failover",
			mcp.WithDescription("Trigger an emergency failover: promotes the best available replica to master when the current master is unreachable or failed. This is destructive and may involve minimal data loss depending on replication mode. ALWAYS prefer cluster-switchover when the master is still accessible. Only use failover when the master is confirmed Failed or unreachable.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.MasterFailover(true)
			return mcp.NewToolResultText(`{"status":"failover initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-switchover",
			mcp.WithDescription("Perform a planned, zero-data-loss master change. The current master is gracefully demoted to replica, and the best available replica (or the one specified in preferred_master) is promoted to master. Use this for planned maintenance, host rotation, or hardware moves. Requires the cluster to be healthy and replicas to be in sync.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("preferred_master", mcp.Description("Optional: preferred replica host:port to promote (e.g. db2:3306). If omitted, replication-manager picks the best candidate.")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			if cl.IsMasterFailed() {
				return mcp.NewToolResultError("master is failed, cannot initiate switchover"), nil
			}
			savedPrefMaster := cl.GetPreferedMasterList()
			prefMaster := req.GetString("preferred_master", "")
			if prefMaster != "" && cl.IsInHostList(prefMaster) {
				cl.SetPrefMaster(prefMaster)
			}
			cl.MasterFailover(false)
			cl.SetPrefMaster(savedPrefMaster)
			return mcp.NewToolResultText(`{"status":"switchover initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-rolling-restart",
			mcp.WithDescription("Restart all database nodes in the cluster one at a time, preserving availability. Replicas are restarted first, then a switchover is performed before restarting the current master. Use after applying OS patches or configuration changes that require a restart.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.RollingRestart()
			return mcp.NewToolResultText(`{"status":"rolling restart initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-optimize",
			mcp.WithDescription("Run OPTIMIZE TABLE on all user tables across all nodes in the cluster. Reclaims fragmented space in InnoDB tablespaces and rebuilds indexes. Safe to run on replicas without interrupting replication. Long-running on large databases.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			go cl.RollingOptimize()
			return mcp.NewToolResultText(`{"status":"optimize initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-rotate-passwords",
			mcp.WithDescription("Rotate all internal database account passwords (replication user, monitoring user, etc.) across the cluster. Updates replication-manager's configuration and reconfigures replication with new credentials. Use for periodic security rotation or after a credential compromise.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			go cl.RotatePasswords()
			return mcp.NewToolResultText(`{"status":"password rotation initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-reset-failover-control",
			mcp.WithDescription("Reset the failover counter and cooldown timer for the cluster. replication-manager limits automatic failovers via failover-limit (max count) and failover-time-limit (cooldown). If these limits are reached, automatic failover stops until reset. Use this to re-enable automatic failover after the limits have been reached.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.ResetFailoverCtr()
			return mcp.NewToolResultText(`{"status":"failover control reset"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-reset-sla",
			mcp.WithDescription("Reset the SLA (Service Level Agreement) uptime counters for the cluster. replication-manager tracks availability time since last failover. Reset this after a planned maintenance window or after resolving an incident to start a fresh uptime measurement.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.SetEmptySla()
			return mcp.NewToolResultText(`{"status":"SLA reset"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-start-traffic",
			mcp.WithDescription("Re-enable application traffic to the cluster by opening the proxy backends. Use this after resolving an issue that required traffic to be stopped, or after a maintenance window. Proxies (ProxySQL, MaxScale, HAProxy) will resume routing connections to the master.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.SetTraffic(true)
			return mcp.NewToolResultText(`{"status":"traffic started"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-stop-traffic",
			mcp.WithDescription("Halt application traffic to the cluster by draining the proxy backends. No new connections will be routed until cluster-start-traffic is called. Use for emergency traffic isolation, maintenance windows, or before a disruptive operation. Does not stop the database servers themselves.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.SetTraffic(false)
			return mcp.NewToolResultText(`{"status":"traffic stopped"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-physical-backup",
			mcp.WithDescription("Trigger a physical (binary-level) backup on the master node using the configured backup tool (Mariabackup, xtrabackup). Physical backups are faster to restore than logical backups for large datasets. The backup is stored in the configured backup directory and tracked in the backup registry.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			master := cl.GetMaster()
			if master == nil {
				return mcp.NewToolResultError("no master found in cluster"), nil
			}
			go master.JobBackupPhysical()
			return mcp.NewToolResultText(`{"status":"physical backup initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-checksum-tables",
			mcp.WithDescription("Run CHECKSUM TABLE on all user tables across the cluster to verify data consistency between master and replicas. Useful for detecting replication drift or data corruption. Results are logged. This is a read-intensive operation — schedule during low-traffic periods on large databases.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			go cl.CheckAllTableChecksum()
			return mcp.NewToolResultText(`{"status":"checksum initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-set-setting",
			mcp.WithDescription("Set a named configuration key to a specific value for a cluster. Use get-cluster-settings first to see available keys and their current values. Common examples: failover-mode=automatic/manual, failover-max-slave-delay=30, db-servers-prefered-master=host:port. Changes take effect immediately without restart.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("setting_name", mcp.Required(), mcp.Description("Configuration key name (e.g. failover-mode, failover-max-slave-delay)")),
			mcp.WithString("setting_value", mcp.Required(), mcp.Description("New value for the setting")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			key := req.GetString("setting_name", "")
			val := req.GetString("setting_value", "")
			if key == "" {
				return mcp.NewToolResultError("setting_name is required"), nil
			}
			err := s.repman.SetClusterSetting(cl, key, val)
			if err != nil {
				return mcp.NewToolResultErrorf("failed to set setting: %v", err), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{
				"status": "setting updated",
				"key":    key,
				"value":  val,
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-switch-setting",
			mcp.WithDescription("Toggle a boolean configuration key for the cluster (true→false or false→true). Use for boolean settings like failover-at-sync, replication-use-ssl, monitoring-pause. Use cluster-set-setting for non-boolean values.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("setting_name", mcp.Required(), mcp.Description("Boolean configuration key name to toggle (e.g. failover-at-sync, replication-use-ssl)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			key := req.GetString("setting_name", "")
			if key == "" {
				return mcp.NewToolResultError("setting_name is required"), nil
			}
			err := s.repman.SwitchClusterSetting(cl, key)
			if err != nil {
				return mcp.NewToolResultErrorf("failed to switch setting: %v", err), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{
				"status": "setting toggled",
				"key":    key,
			})), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-bootstrap-replication",
			mcp.WithDescription("Configure replication between cluster nodes for the first time, or after a cluster-cleanup-replication. Sets up the server marked as db-servers-prefered-master as master and connects all other servers as replicas. Pass clean=true to stop and reset existing replication before reconfiguring. Use this when servers are running but replication is not yet configured.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("clean",
				mcp.Description("Set to 'true' to stop and reset existing replication before bootstrapping (default: false)"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			clean := req.GetString("clean", "false") == "true"
			err := cl.BootstrapReplication(clean, false)
			if err != nil {
				return mcp.NewToolResultErrorf("bootstrap failed: %v", err), nil
			}
			return mcp.NewToolResultText(`{"status":"replication bootstrap initiated"}`), nil
		},
	)

	s.addTool(
		mcp.NewTool("cluster-cleanup-replication",
			mcp.WithDescription("Remove all replication configuration from all cluster nodes: stops replication threads, resets slave/master status, and clears replication credentials. IRREVERSIBLE — use only when you intend to rebuild the cluster topology from scratch with cluster-bootstrap-replication.."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			err := cl.BootstrapReplicationCleanup(false)
			if err != nil {
				return mcp.NewToolResultErrorf("cleanup failed: %v", err), nil
			}
			return mcp.NewToolResultText(`{"status":"replication cleanup initiated"}`), nil
		},
	)
}
