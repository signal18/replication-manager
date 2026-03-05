// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"strconv"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerBackupReadTools registers read-only backup/restic tools.
func (s *MCPServer) registerBackupReadTools() {
	s.mcp.AddTool(
		mcp.NewTool("list-backups",
			mcp.WithDescription("List all registered physical and logical backups for a cluster. Each entry includes backup type (physical/logical), start time, completion time, server it was taken from, size, and storage location. Use this to verify recent backups exist before a failover, or to find backup files for restore. Check for WARN0111/WARN0112 alerts if no backups appear."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetBackups())), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("get-backup-stats",
			mcp.WithDescription("Get aggregated backup statistics for a cluster: total backup count, last successful backup time, backup frequency, and retention compliance. Use to assess RPO (Recovery Point Objective) — how much data could be lost if recovery is needed now. Complements list-backups which shows individual entries."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetBackupStat())), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("list-restic-snapshots",
			mcp.WithDescription("List all Restic snapshots stored in the cluster's Restic repository. Each snapshot has a short ID, creation time, hostname, and tags. Restic provides encrypted, deduplicated backups stored locally or in S3. Use the snapshot ID with restic-purge to remove specific snapshots. Call restic-fetch first if the list appears empty or stale."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetSnapshots())), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("get-restic-stats",
			mcp.WithDescription("Get Restic repository statistics: total size on disk, deduplication ratio, number of blobs and snapshots. The deduplication ratio shows storage efficiency — a ratio of 5x means 5x the data is protected for the actual space used. Use this to monitor storage growth and plan capacity. A high repository size may require running restic-purge with an appropriate retention policy."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(cl.GetSnapshotStats())), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("get-restic-task-queue",
			mcp.WithDescription("Get the current Restic task queue for a cluster: pending, running, and recently completed tasks. Tasks include backup, fetch, purge, init, unlock, and restore operations. Use this to check if a backup is in progress, if tasks are queued or stalled, or if a previous task failed. Use restic-task-cancel to cancel a specific task by its ID."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			queue, err := cl.ResticGetQueue()
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(queue)), nil
		},
	)
}

// registerBackupWriteTools registers write/action backup tools.
func (s *MCPServer) registerBackupWriteTools() {
	s.mcp.AddTool(
		mcp.NewTool("restic-init",
			mcp.WithDescription("Initialize a new Restic repository for the cluster at the configured repository path (local or S3). Must be run once before any backup can be stored. The repository is encrypted with the password from backup-restic-password. If the repository already exists this will return an error — only call on first setup. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			if err := cl.ResticInitRepo(false); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(`{"status":"restic repository initialized"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-fetch",
			mcp.WithDescription("Refresh the local snapshot metadata cache from the Restic repository. Run this if list-restic-snapshots shows stale or missing data. replication-manager caches snapshot metadata locally; this forces a re-read from the actual repository (local disk or S3). Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			go cl.ResticFetchRepo()
			return mcp.NewToolResultText(`{"status":"restic fetch initiated"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-purge",
			mcp.WithDescription("Delete a specific Restic snapshot by ID and run 'restic prune' to reclaim disk space. Get the snapshot_id from list-restic-snapshots. Deletion is permanent and irreversible. The repository must not be locked (check get-restic-task-queue). Use to manually enforce retention or remove a corrupted snapshot. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("snapshot_id", mcp.Required(), mcp.Description("Restic snapshot short ID from list-restic-snapshots (e.g. a1b2c3d4)")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			snapshotID := req.GetString("snapshot_id", "")
			if snapshotID == "" {
				return mcp.NewToolResultError("snapshot_id is required"), nil
			}
			if err := cl.ResticPurgeSnapshot(snapshotID, false); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{
				"status":      "snapshot purge initiated",
				"snapshot_id": snapshotID,
			})), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-unlock",
			mcp.WithDescription("Remove stale lock files from the Restic repository. Restic locks the repository during operations; a crash or kill can leave a stale lock that blocks all subsequent operations. Only run this if you are certain no Restic process is actively running — check get-restic-task-queue first. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.ResticUnlockRepo()
			return mcp.NewToolResultText(`{"status":"restic repository unlocked"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-task-queue-pause",
			mcp.WithDescription("Pause the Restic task queue, preventing new backup tasks from starting. Currently running tasks will complete. Use before planned maintenance on the backup storage, or to temporarily stop scheduled backups without disabling the backup system entirely. Resume with restic-task-queue-resume. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			cl.ResticPauseQueue()
			return mcp.NewToolResultText(`{"status":"restic task queue paused"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-task-queue-resume",
			mcp.WithDescription("Resume the Restic task queue after it was paused with restic-task-queue-pause. Queued tasks will begin executing in order. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			go cl.ResticRunQueue()
			return mcp.NewToolResultText(`{"status":"restic task queue resumed"}`), nil
		},
	)

	s.mcp.AddTool(
		mcp.NewTool("restic-task-cancel",
			mcp.WithDescription("Cancel a pending or running Restic task by its integer task ID. Get the task ID from get-restic-task-queue. Cancelling a running backup task may leave the repository in an inconsistent state — run restic-unlock afterwards if subsequent operations report a lock error. Requires mcp-write-enabled=true."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("task_id", mcp.Required(), mcp.Description("Integer task ID from get-restic-task-queue")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			taskIDStr := req.GetString("task_id", "")
			if taskIDStr == "" {
				return mcp.NewToolResultError("task_id is required"), nil
			}
			taskID, err := strconv.Atoi(taskIDStr)
			if err != nil {
				return mcp.NewToolResultError("task_id must be a valid integer"), nil
			}
			if err := cl.ResticCancelTask(taskID); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(toJSON(map[string]string{
				"status":  "task cancelled",
				"task_id": taskIDStr,
			})), nil
		},
	)
}
