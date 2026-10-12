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

// EngineSwitchRequest is the body of POST /api/clusters/{c}/actions/alter-engine (#1999).
type EngineSwitchRequest struct {
	EngineFrom string `json:"engineFrom"`
	EngineTo   string `json:"engineTo"`
	Schema     string `json:"schema"` // optional: one schema only
	Confirm    bool   `json:"confirm"`
}

// handlerMuxClusterAlterEngine switches every table of one engine to another (#1999).
//
// @Summary Switch the tables of one storage engine to another
// @Description Plans (confirm false) or runs (confirm true) ALTER TABLE ... ENGINE=engineTo on the master for every user table on engineFrom, optionally of one schema; binlog on, the replicas follow. Tables of any other engine (Aria, MEMORY, MyISAM...) are not touched; a table engineTo cannot hold (a foreign key, only InnoDB enforces them) is skipped with its reason. engineTo must be available on the master. Runs in the background, one table at a time, stopping at the first error: progress CINF0015, stop WARN0240. Needs db-optimize.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param request body EngineSwitchRequest true "engineFrom, engineTo, schema, confirm"
// @Success 200 {object} cluster.EngineSwitchPlan "The plan (confirm false)"
// @Success 202 {object} cluster.EngineSwitchPlan "Switch started (confirm true)"
// @Failure 400 {string} string "Refused: the reason (engine not available, a switch running, nothing to alter...)"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/alter-engine [post]
func (repman *ReplicationManager) handlerMuxClusterAlterEngine(w http.ResponseWriter, r *http.Request) {
	cl, ok := repman.engineSwitchCluster(w, r)
	if !ok {
		return
	}
	var req EngineSwitchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	plan, err := cl.PlanEngineSwitch(req.EngineFrom, req.EngineTo, req.Schema, "")
	repman.engineSwitchAnswer(w, cl, plan, err, req.Confirm)
}

// EngineSwitchTableRequest is the body of the one-table switch.
type EngineSwitchTableRequest struct {
	Confirm bool `json:"confirm"`
}

// handlerMuxTableAlterEngine switches one table to another storage engine (#1999).
//
// @Summary Switch one table to another storage engine
// @Description Plans (confirm false) or runs (confirm true) ALTER TABLE schemaName.tableName ENGINE=engine on the master, binlog on, the replicas follow. Refused when the engine is not available on the master or the table has a foreign key and the engine is not InnoDB. Runs in the background: CINF0015, WARN0240 on a stop. Needs db-optimize.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param engine path string true "Target engine"
// @Param request body EngineSwitchTableRequest false "confirm"
// @Success 200 {object} cluster.EngineSwitchPlan "The plan (confirm false)"
// @Success 202 {object} cluster.EngineSwitchPlan "Switch started (confirm true)"
// @Failure 400 {string} string "Refused: the reason"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/alter-engine/{engine} [post]
func (repman *ReplicationManager) handlerMuxTableAlterEngine(w http.ResponseWriter, r *http.Request) {
	cl, ok := repman.engineSwitchCluster(w, r)
	if !ok {
		return
	}
	var req EngineSwitchTableRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}
	vars := mux.Vars(r)
	plan, err := cl.PlanEngineSwitch("", vars["engine"], vars["schemaName"], vars["tableName"])
	if err == nil && len(plan.Tables) == 0 && len(plan.Skipped) > 0 {
		err = errors.New(plan.Skipped[0].Reason)
	}
	repman.engineSwitchAnswer(w, cl, plan, err, req.Confirm)
}

func (repman *ReplicationManager) engineSwitchCluster(w http.ResponseWriter, r *http.Request) (*cluster.Cluster, bool) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	cl := repman.getClusterByName(mux.Vars(r)["clusterName"])
	if cl == nil {
		http.Error(w, "No cluster", http.StatusNotFound)
		return nil, false
	}
	if valid, _ := repman.IsValidClusterACL(r, cl); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return nil, false
	}
	return cl, true
}

func (repman *ReplicationManager) engineSwitchAnswer(w http.ResponseWriter, cl *cluster.Cluster, plan cluster.EngineSwitchPlan, err error, confirm bool) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if !confirm {
		repman.jsonResponse(plan, w)
		return
	}
	if err := cl.RunEngineSwitch(plan); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(plan)
}
