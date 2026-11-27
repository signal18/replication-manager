// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"net/http"

	"github.com/signal18/replication-manager/cluster"
)

// The CDC feed of a source of another engine (CDC_SOURCES.md): its new initial load, and the
// removal of what it created on its source.

// ReplicationFeedDropResult is what a removal ran on the source.
type ReplicationFeedDropResult struct {
	Statements []string `json:"statements"`
}

func writeReplFeedError(w http.ResponseWriter, err error) {
	if errors.Is(err, cluster.ErrNoReplicationPlugin) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handlerMuxReplicationPluginReinit restarts a feed from a new initial load.
// @Summary Start a CDC feed again from a new initial load
// @Description The rejoin of a feed whose source can no longer give its changes (ERR00140: its slot dropped at its bound, or lost): the plugin stops, its position and checkpoints are removed, and it starts again with a new initial load (its tables on the destination emptied, then copied from a new snapshot). What the feed created on the source is kept and replaced by the new initial load.
// @Tags ClusterReplicationPlugins
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param pluginName path string true "Plugin Name"
// @Success 200 {string} string "New initial load requested"
// @Failure 400 {string} string "Not a feed with a capture source"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No such plugin"
// @Router /api/clusters/{clusterName}/replication-plugin-actions/reinit/{pluginName} [post]
func (repman *ReplicationManager) handlerMuxReplicationPluginReinit(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.ReinitReplicationPlugin(vars["pluginName"]); err != nil {
			writeReplFeedError(w, err)
			return
		}
		w.Write([]byte("New initial load requested"))
	})
}

// handlerMuxReplicationPluginDropSourceObjects removes what a feed created on its source.
// @Summary Remove what a CDC feed created on its source
// @Description With the feed switched off: its replication slot and its publication are dropped on the source, only those the feed created (its state lists them), never another. The statements run are returned and logged.
// @Tags ClusterReplicationPlugins
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param pluginName path string true "Plugin Name"
// @Success 200 {object} ReplicationFeedDropResult
// @Failure 400 {string} string "The feed is on, or created nothing"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No such plugin"
// @Router /api/clusters/{clusterName}/replication-plugin-actions/drop-source-objects/{pluginName} [post]
func (repman *ReplicationManager) handlerMuxReplicationPluginDropSourceObjects(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		ran, err := mycluster.DropReplicationFeedSourceObjects(vars["pluginName"])
		if err != nil {
			writeReplFeedError(w, err)
			return
		}
		if ran == nil {
			ran = []string{}
		}
		writeArchiveJSON(w, ReplicationFeedDropResult{Statements: ran})
	})
}
