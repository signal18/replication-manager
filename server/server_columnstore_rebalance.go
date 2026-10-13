// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
)

// ColumnstoreRebalanceRequest is the body of POST /api/clusters/{c}/actions/columnstore-rebalance (#2008).
type ColumnstoreRebalanceRequest struct {
	Schema  string `json:"schema"` // optional: one schema only
	Confirm bool   `json:"confirm"`
}

// handlerMuxClusterColumnstoreRebalance spreads the ColumnStore tables over every dbroot (#2008).
//
// @Summary Rebalance the ColumnStore tables over every dbroot
// @Description Plans (confirm false) or runs (confirm true) the rebuild of every ColumnStore table, optionally of one schema, whose extents are not on every dbroot (a PM added since it was written). Columnstore topology only. Each table is copied into schema.table__rebalance under FLUSH TABLES ... WITH READ LOCK (its writes wait, its reads go on), then swapped by an atomic RENAME and the original dropped. Runs on the UM in the background, one table at a time, stopping at the first error: progress CINF0016, stop WARN0241. Needs db-optimize.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param request body ColumnstoreRebalanceRequest true "schema, confirm"
// @Success 200 {object} cluster.ColumnstoreRebalancePlan "The plan (confirm false)"
// @Success 202 {object} cluster.ColumnstoreRebalancePlan "Rebalance started (confirm true)"
// @Failure 400 {string} string "Refused: the reason (not a columnstore cluster, a rebalance running, nothing to rebalance...)"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/columnstore-rebalance [post]
func (repman *ReplicationManager) handlerMuxClusterColumnstoreRebalance(w http.ResponseWriter, r *http.Request) {
	cl, ok := repman.engineSwitchCluster(w, r)
	if !ok {
		return
	}
	var req ColumnstoreRebalanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	plan, err := cl.PlanColumnstoreRebalance(req.Schema, "")
	repman.columnstoreRebalanceAnswer(w, cl, plan, err, req.Confirm)
}

// ColumnstoreRebalanceTableRequest is the body of the one-table rebalance.
type ColumnstoreRebalanceTableRequest struct {
	Confirm bool `json:"confirm"`
}

// handlerMuxTableColumnstoreRebalance spreads one ColumnStore table over every dbroot (#2008).
//
// @Summary Rebalance one ColumnStore table over every dbroot
// @Description Plans (confirm false) or runs (confirm true) the rebuild of schemaName.tableName so that its extents spread over every dbroot: a copy under a table read lock (writes wait, reads go on), an atomic RENAME, the original dropped. Refused outside the columnstore topology, for a table already on every dbroot, or with a copy of a previous rebalance left. Runs in the background: CINF0016, WARN0241 on a stop. Needs db-optimize.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param request body ColumnstoreRebalanceTableRequest false "confirm"
// @Success 200 {object} cluster.ColumnstoreRebalancePlan "The plan (confirm false)"
// @Success 202 {object} cluster.ColumnstoreRebalancePlan "Rebalance started (confirm true)"
// @Failure 400 {string} string "Refused: the reason"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/columnstore-rebalance [post]
func (repman *ReplicationManager) handlerMuxTableColumnstoreRebalance(w http.ResponseWriter, r *http.Request) {
	cl, ok := repman.engineSwitchCluster(w, r)
	if !ok {
		return
	}
	var req ColumnstoreRebalanceTableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	plan, err := cl.PlanColumnstoreRebalance(vars["schemaName"], vars["tableName"])
	if err == nil && len(plan.Tables) == 0 && len(plan.Skipped) > 0 {
		err = errors.New(plan.Skipped[0].Reason)
	}
	repman.columnstoreRebalanceAnswer(w, cl, plan, err, req.Confirm)
}

func (repman *ReplicationManager) columnstoreRebalanceAnswer(w http.ResponseWriter, cl *cluster.Cluster, plan cluster.ColumnstoreRebalancePlan, err error, confirm bool) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !confirm {
		repman.jsonResponse(plan, w)
		return
	}
	if err := cl.RunColumnstoreRebalance(plan); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(plan)
}
