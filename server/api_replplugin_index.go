// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The indexes of an analytic child's denormalize mode (CDC_CORE.md "Denormalize"): one
// denormalizing query and the denormalized table it fills on the child, each.

// ReplicationPluginIndexList is the list and what the child's plugin says of it.
type ReplicationPluginIndexList struct {
	Indexes []config.ReplicationPluginIndex `json:"indexes"`
	// Ready: the indexes whose init is done (the plugin's status)
	Ready []string `json:"ready"`
	// Pinned: a config file sets the list, the API cannot change it
	Pinned bool `json:"pinned"`
}

func writeReplPluginIndexError(w http.ResponseWriter, err error) {
	if errors.Is(err, cluster.ErrReplPluginIndexesPinned) {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

// handlerMuxReplicationPluginIndexList lists the indexes of the cluster.
// @Summary List the indexes of the denormalize mode
// @Description The analytic child's indexes: each a denormalizing query, run on the source, and the denormalized table (slave-schema.slave-table) it fills on the child; ready lists those whose init is done.
// @Tags ClusterReplicationPlugins
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} ReplicationPluginIndexList
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/replication-plugin-index-list [get]
func (repman *ReplicationManager) handlerMuxReplicationPluginIndexList(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		out := ReplicationPluginIndexList{Indexes: mycluster.Conf.ReplicationPluginIndexList(), Ready: []string{}, Pinned: mycluster.ReplicationPluginIndexesPinned()}
		if out.Indexes == nil {
			out.Indexes = []config.ReplicationPluginIndex{}
		}
		for _, st := range mycluster.ReplicationPluginStatuses() {
			out.Ready = append(out.Ready, st.IndexesReady...)
		}
		writeArchiveJSON(w, out)
	})
}

// handlerMuxReplicationPluginIndexPut adds an index or replaces the one of its name.
// @Summary Add or change an index of the denormalize mode
// @Description Stores the index (name, sql, slave-schema, slave-table, max-keys-per-batch) and writes it into the analytic child's plugin configuration: the plugin restarts and a new or changed index starts with its init (the denormalized table made from the query's result, then filled on a consistent snapshot of the source). The query returns the primary key columns of every table it reads.
// @Tags ClusterReplicationPlugins
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body config.ReplicationPluginIndex true "the index"
// @Success 200 {object} map[string]string "name"
// @Failure 400 {string} string "Invalid index"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file"
// @Router /api/clusters/{clusterName}/replication-plugin-index-actions/put [post]
func (repman *ReplicationManager) handlerMuxReplicationPluginIndexPut(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		var idx config.ReplicationPluginIndex
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&idx); err != nil {
			http.Error(w, "Error decoding request body: "+err.Error(), http.StatusBadRequest)
			return
		}
		if err := mycluster.PutReplicationPluginIndex(idx); err != nil {
			writeReplPluginIndexError(w, err)
			return
		}
		writeArchiveJSON(w, map[string]string{"name": idx.Name})
	})
}

// handlerMuxReplicationPluginIndexRemove removes an index.
// @Summary Remove an index of the denormalize mode
// @Description The index leaves the plugin's configuration; its denormalized table stays on the child, to drop by hand.
// @Tags ClusterReplicationPlugins
// @Produce plain
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param indexName path string true "Index Name"
// @Success 200 {string} string "Index removed"
// @Failure 400 {string} string "Unknown index"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "The list is set by a config file"
// @Router /api/clusters/{clusterName}/replication-plugin-index-actions/remove/{indexName} [post]
func (repman *ReplicationManager) handlerMuxReplicationPluginIndexRemove(w http.ResponseWriter, r *http.Request) {
	repman.withResticCluster(w, r, false, func(mycluster *cluster.Cluster, vars map[string]string) {
		if err := mycluster.DeleteReplicationPluginIndex(vars["indexName"]); err != nil {
			writeReplPluginIndexError(w, err)
			return
		}
		w.Write([]byte("Index removed"))
	})
}
