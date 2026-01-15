// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
)

// API handler for executing SQL scripts
func (repman *ReplicationManager) handlerMuxExecuteSQLScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	mycluster := repman.Clusters[clusterName]
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	var req struct {
		ScriptPath     string `json:"scriptPath"`
		ScriptContent  string `json:"scriptContent"`
		TargetDatabase string `json:"targetDatabase"`
		TargetServer   string `json:"targetServer"`
		Timeout        int    `json:"timeout"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate that either scriptPath or scriptContent is provided
	if req.ScriptPath == "" && req.ScriptContent == "" {
		http.Error(w, "Either scriptPath or scriptContent must be provided", http.StatusBadRequest)
		return
	}

	result, err := mycluster.JobExecuteSQLScript(req.ScriptPath, req.ScriptContent, req.TargetDatabase, req.TargetServer, req.Timeout)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error":  err.Error(),
			"result": result,
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(result)
}

// API handler for saving SQL script jobs
func (repman *ReplicationManager) handlerMuxSaveSQLScriptJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	mycluster := repman.Clusters[clusterName]
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	var job cluster.SQLScriptJob
	if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
		http.Error(w, "Invalid request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Set creation time if not set
	if job.Created.IsZero() {
		job.Created = time.Now()
	}

	if err := mycluster.SaveSQLScriptJob(&job); err != nil {
		http.Error(w, "Failed to save job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Job saved successfully",
		"job":     job,
	})
}

// API handler for loading SQL script jobs
func (repman *ReplicationManager) handlerMuxLoadSQLScriptJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	mycluster := repman.Clusters[clusterName]
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	jobs, err := mycluster.LoadSQLScriptJobs()
	if err != nil {
		http.Error(w, "Failed to load jobs: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(jobs)
}

// API handler for deleting SQL script jobs
func (repman *ReplicationManager) handlerMuxDeleteSQLScriptJob(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobName := vars["jobName"]

	mycluster := repman.Clusters[clusterName]
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	if err := mycluster.DeleteSQLScriptJob(jobName); err != nil {
		http.Error(w, "Failed to delete job: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "Job deleted successfully",
	})
}

// API handler for triggering scheduled SQL scripts manually
func (repman *ReplicationManager) handlerMuxTriggerScheduledSQLScripts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	mycluster := repman.Clusters[clusterName]
	if mycluster == nil {
		http.Error(w, "Cluster not found", http.StatusNotFound)
		return
	}

	// Execute in goroutine to not block the API response
	go mycluster.ExecuteScheduledSQLScripts()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "success",
		"message": "SQL scripts execution triggered",
	})
}
