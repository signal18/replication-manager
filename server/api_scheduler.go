package server

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

func (repman *ReplicationManager) handlerSchedulerJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		repman.listJobs(w, r)
	case "POST":
		repman.createJob(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (repman *ReplicationManager) handlerSchedulerJob(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		repman.getJob(w, r)
	case "PUT":
		repman.updateJob(w, r)
	case "DELETE":
		repman.deleteJob(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (repman *ReplicationManager) handlerSchedulerJobEnable(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repman.enableJob(w, r)
}

func (repman *ReplicationManager) handlerSchedulerJobDisable(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	repman.disableJob(w, r)
}

func (repman *ReplicationManager) listJobs(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	jobs := cluster.JobScheduler.ListJobsAsInterface()
	repman.respondJSON(w, http.StatusOK, jobs)
}

func (repman *ReplicationManager) createJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	// Read the JSON body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		repman.respondError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	job, err := cluster.JobScheduler.CreateJobFromJSON(body)
	if err != nil {
		repman.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	repman.respondJSON(w, http.StatusCreated, job)
}

func (repman *ReplicationManager) getJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobID := vars["id"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	job, err := cluster.JobScheduler.GetJobAsInterface(jobID)
	if err != nil {
		repman.respondError(w, http.StatusNotFound, err.Error())
		return
	}
	repman.respondJSON(w, http.StatusOK, job)
}

func (repman *ReplicationManager) updateJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobID := vars["id"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	// Read the JSON body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		repman.respondError(w, http.StatusBadRequest, "Failed to read request body")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	job, err := cluster.JobScheduler.UpdateJobFromJSON(jobID, body)
	if err != nil {
		repman.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	repman.respondJSON(w, http.StatusOK, job)
}

func (repman *ReplicationManager) deleteJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobID := vars["id"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	if err := cluster.JobScheduler.UnscheduleJob(jobID); err != nil {
		repman.respondError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (repman *ReplicationManager) enableJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobID := vars["id"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	if err := cluster.JobScheduler.EnableJob(jobID); err != nil {
		repman.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, err := cluster.JobScheduler.GetJobAsInterface(jobID)
	if err != nil {
		repman.respondError(w, http.StatusNotFound, err.Error())
		return
	}
	repman.respondJSON(w, http.StatusOK, job)
}

func (repman *ReplicationManager) disableJob(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	clusterName := vars["clusterName"]
	jobID := vars["id"]

	cluster := repman.getClusterByName(clusterName)
	if cluster == nil {
		repman.respondError(w, http.StatusNotFound, "cluster not found")
		return
	}

	if cluster.JobScheduler == nil {
		repman.respondError(w, http.StatusInternalServerError, "job scheduler not initialized")
		return
	}

	if err := cluster.JobScheduler.DisableJob(jobID); err != nil {
		repman.respondError(w, http.StatusBadRequest, err.Error())
		return
	}

	job, err := cluster.JobScheduler.GetJobAsInterface(jobID)
	if err != nil {
		repman.respondError(w, http.StatusNotFound, err.Error())
		return
	}
	repman.respondJSON(w, http.StatusOK, job)
}

// Helper functions
func (repman *ReplicationManager) respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

func (repman *ReplicationManager) respondError(w http.ResponseWriter, code int, message string) {
	repman.respondJSON(w, code, map[string]string{"error": message})
}
