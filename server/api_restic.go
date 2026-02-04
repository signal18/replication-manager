// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// RegisterResticRoutes registers all restic-related API routes
func (repman *ReplicationManager) RegisterResticRoutes(router *mux.Router) {
	router.Handle("/api/clusters/{clusterName}/actions/restic-mount-toggle", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticMountToggle)),
	))
	router.Handle("/api/clusters/{clusterName}/restic/mount-status", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResticMountStatus)),
	))
}

// ResticMountToggleRequest represents the request body for mount toggle
type ResticMountToggleRequest struct {
	Action  string                       `json:"action"`            // "mount" or "unmount"
	Options *backupmgr.ResticMountOption `json:"options,omitempty"` // Mount options (only for "mount" action)
}

// ResticMountStatusResponse represents the response for mount status
type ResticMountStatusResponse struct {
	IsMounted   bool     `json:"is_mounted"`
	MountPath   string   `json:"mount_path,omitempty"`
	RefCount    int      `json:"ref_count"`
	ActiveUsers []string `json:"active_users,omitempty"`
}

// handlerMuxResticMountToggle handles mounting and unmounting of restic repository
// @Summary Toggle restic mount on/off
// @Description Mounts or unmounts the restic repository for the cluster. Mount is persistent until explicitly unmounted. Supports filtering by host, tag, path and various mount options.
// @Tags Restic
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body ResticMountToggleRequest true "Mount toggle request with optional mount options"
// @Success 200 {object} ResticMountStatusResponse "Mount status after operation"
// @Failure 400 {string} string "Invalid request"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "Mount already active at different path"
// @Failure 500 {string} string "Operation failed"
// @Router /api/clusters/{clusterName}/actions/restic-mount-toggle [post]
func (repman *ReplicationManager) handlerMuxResticMountToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	if mycluster.ResticManager == nil {
		http.Error(w, "Restic manager not available for this cluster", http.StatusInternalServerError)
		return
	}

	// Parse request body
	body, readErr := io.ReadAll(r.Body)
	if readErr != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", readErr), http.StatusBadRequest)
		return
	}

	var req ResticMountToggleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request body: %v", err), http.StatusBadRequest)
		return
	}

	var optionsPresence map[string]json.RawMessage
	if len(body) > 0 {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(body, &raw); err == nil {
			if rawOptions, ok := raw["options"]; ok {
				_ = json.Unmarshal(rawOptions, &optionsPresence)
			}
		}
	}

	action := strings.ToLower(strings.TrimSpace(req.Action))
	if action != "mount" && action != "unmount" {
		http.Error(w, "Invalid action: must be 'mount' or 'unmount'", http.StatusBadRequest)
		return
	}

	clusterName := strings.TrimSpace(mycluster.GetClusterName())
	if clusterName == "" {
		http.Error(w, "Cluster name is empty", http.StatusInternalServerError)
		return
	}

	// Construct mount directory path
	resticMountBaseDir := "/mnt/restic"
	mountDir := filepath.Join(resticMountBaseDir, clusterName)
	if mycluster.Conf.BackupResticMountDir != "" {
		mountDir = mycluster.Conf.BackupResticMountDir
	}

	var err error
	var response ResticMountStatusResponse

	switch action {
	case "mount":
		// Prepare mount options
		var mountOpt backupmgr.ResticMountOption
		allowOtherDefault := mycluster.Conf.BackupResticMountAllowOther
		if req.Options != nil {
			mountOpt = *req.Options
			// Ensure TargetDir is set
			if mountOpt.TargetDir == "" {
				mountOpt.TargetDir = mountDir
			}
			// Apply defaults only when not provided
			if _, ok := optionsPresence["no_lock"]; !ok {
				mountOpt.NoLock = true
			}
			if _, ok := optionsPresence["allow_other"]; !ok {
				mountOpt.AllowOther = allowOtherDefault
			}
		} else {
			// Use default options if not provided
			mountOpt = backupmgr.NewResticMountOption(mountDir)
			mountOpt.AllowOther = allowOtherDefault
		}

		// Check if already mounted
		if mycluster.ResticManager.IsMounted() {
			existingPath := mycluster.ResticManager.GetMountPath()
			if existingPath == mountOpt.TargetDir {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlInfo,
					"Restic mount already active at %s", existingPath)
			} else {
				http.Error(w, fmt.Sprintf("Restic mount already active at different path: %s (requested: %s)", existingPath, mountOpt.TargetDir), http.StatusConflict)
				return
			}
		} else {
			// Perform mount
			mycluster.LogModulePrintf(mycluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Mounting restic repository at %s with options: host=%v, tag=%v, path=%v, allow-other=%v, owner-root=%v, no-lock=%v, verbose=%d, quiet=%v",
				mountOpt.TargetDir, mountOpt.Host, mountOpt.Tag, mountOpt.Path,
				mountOpt.AllowOther, mountOpt.OwnerRoot, mountOpt.NoLock, mountOpt.Verbose, mountOpt.Quiet)

			err = mycluster.ResticManager.MountRepoWithOptions(mountOpt)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose,
					config.ConstLogModRestic,
					config.LvlErr,
					"Failed to mount restic repository: %v", err)
				http.Error(w, fmt.Sprintf("Failed to mount: %v", err), http.StatusInternalServerError)
				return
			}

			mycluster.LogModulePrintf(mycluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlInfo,
				"Successfully mounted restic repository at %s", mountOpt.TargetDir)
		}

	case "unmount":
		// Check if mounted
		if !mycluster.ResticManager.IsMounted() {
			http.Error(w, "No restic mount is currently active", http.StatusBadRequest)
			return
		}

		// Check if there are active users
		refCount := mycluster.ResticManager.GetMountRefCount()
		if refCount > 0 {
			users := mycluster.ResticManager.GetMountUsers()
			mycluster.LogModulePrintf(mycluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlWarn,
				"Unmount requested but %d active users: %v - will wait for them to finish", refCount, users)
		}

		// Perform unmount (will wait for active users to finish)
		mycluster.LogModulePrintf(mycluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Unmounting restic repository")

		err = mycluster.ResticManager.UnmountRepo()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose,
				config.ConstLogModRestic,
				config.LvlErr,
				"Failed to unmount restic repository: %v", err)
			http.Error(w, fmt.Sprintf("Failed to unmount: %v", err), http.StatusInternalServerError)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose,
			config.ConstLogModRestic,
			config.LvlInfo,
			"Successfully unmounted restic repository")
	}

	// Build response with current status
	response.IsMounted = mycluster.ResticManager.IsMounted()
	response.MountPath = mycluster.ResticManager.GetMountPath()
	response.RefCount = mycluster.ResticManager.GetMountRefCount()
	response.ActiveUsers = mycluster.ResticManager.GetMountUsers()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}

// handlerMuxResticMountStatus returns the current mount status
// @Summary Get restic mount status
// @Description Returns the current mount status including path, ref count, and active users
// @Tags Restic
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} ResticMountStatusResponse "Current mount status"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Cluster not found"
// @Failure 500 {string} string "Restic manager not available"
// @Router /api/clusters/{clusterName}/restic/mount-status [get]
func (repman *ReplicationManager) handlerMuxResticMountStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	if mycluster.ResticManager == nil {
		http.Error(w, "Restic manager not available for this cluster", http.StatusInternalServerError)
		return
	}

	response := ResticMountStatusResponse{
		IsMounted:   mycluster.ResticManager.IsMounted(),
		MountPath:   mycluster.ResticManager.GetMountPath(),
		RefCount:    mycluster.ResticManager.GetMountRefCount(),
		ActiveUsers: mycluster.ResticManager.GetMountUsers(),
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
