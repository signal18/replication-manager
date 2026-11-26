// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The host (VM) of a database server, on an orchestrator that drives the hosts (the
// HostOrchestrator facet, doc/implementation/cluster/ORCHESTRATOR_INTERFACE.md section 10.4).
// Another orchestrator answers 501.

// hostActionsInFlight holds the servers whose host action is running: one at a time per
// server, a second request answers 409.
var hostActionsInFlight sync.Map

func (repman *ReplicationManager) apiDatabaseHostHandlers(router *mux.Router) {
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/host-stop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerHostStop)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/host-start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerHostStart)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/host-reboot", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerHostReboot)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/host-status", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerHostStatus)),
	)).Methods("GET")
}

// hostRequest resolves the cluster and the server of a host request, checks the ACL, and
// answers the error itself; ok false: the answer is written.
func (repman *ReplicationManager) hostRequest(w http.ResponseWriter, r *http.Request) (*cluster.Cluster, *cluster.ServerMonitor, bool) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return nil, nil, false
	}
	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"})
		return nil, nil, false
	}
	node := mycluster.GetServerFromName(vars["serverName"])
	if node == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Server Not Found"})
		return nil, nil, false
	}
	orch := mycluster.Orchestrator()
	if !orch.Traits().HasHost {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "orchestrator " + orch.Name() + " (" + orch.Type() + ") does not drive the hosts"})
		return nil, nil, false
	}
	return mycluster, node, true
}

// hostAction runs one host action in the background (a reboot waits for the hypervisor's
// task, minutes) and answers 202; the result is logged in the orchestrator module.
func (repman *ReplicationManager) hostAction(w http.ResponseWriter, r *http.Request, action string, run func(cluster.Orchestrator, *cluster.ServerMonitor) error) {
	mycluster, node, ok := repman.hostRequest(w, r)
	if !ok {
		return
	}
	key := mycluster.Name + "/" + node.URL
	if _, busy := hostActionsInFlight.LoadOrStore(key, action); busy {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "a host action is already running on " + node.URL})
		return
	}
	orch := mycluster.Orchestrator()
	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "API host %s of %s", action, node.URL)
	go func() {
		defer hostActionsInFlight.Delete(key)
		if err := run(orch, node); err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Host %s of %s failed: %s", action, node.URL, err)
		}
	}()
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"message": "host " + action + " started", "server": node.Name})
}

// handlerMuxServerHostStop shuts down the VM of a server.
// @Summary Shut down the host (VM) of a database server
// @Description Asks the hypervisor for a clean shutdown of the VM under the server (never a hard stop), in the background. Only an orchestrator that drives the hosts (proxmox) supports it; the VM is the server's entry in prov-db-host-vmids. The database inside the VM is not stopped first: stop it before, or use the rolling upgrade.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 202 {object} map[string]string "Host shutdown started"
// @Failure 403 {object} map[string]string "No valid ACL"
// @Failure 404 {object} map[string]string "Cluster Not Found or Server Not Found"
// @Failure 409 {object} map[string]string "A host action is already running on the server"
// @Failure 501 {object} map[string]string "The orchestrator does not drive the hosts"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/host-stop [post]
func (repman *ReplicationManager) handlerMuxServerHostStop(w http.ResponseWriter, r *http.Request) {
	repman.hostAction(w, r, "stop", func(o cluster.Orchestrator, s *cluster.ServerMonitor) error { return o.StopHost(s) })
}

// handlerMuxServerHostStart starts the VM of a server.
// @Summary Start the host (VM) of a database server
// @Description Starts the VM under the server through the hypervisor, in the background. Only an orchestrator that drives the hosts (proxmox) supports it.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 202 {object} map[string]string "Host start started"
// @Failure 403 {object} map[string]string "No valid ACL"
// @Failure 404 {object} map[string]string "Cluster Not Found or Server Not Found"
// @Failure 409 {object} map[string]string "A host action is already running on the server"
// @Failure 501 {object} map[string]string "The orchestrator does not drive the hosts"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/host-start [post]
func (repman *ReplicationManager) handlerMuxServerHostStart(w http.ResponseWriter, r *http.Request) {
	repman.hostAction(w, r, "start", func(o cluster.Orchestrator, s *cluster.ServerMonitor) error { return o.StartHost(s) })
}

// handlerMuxServerHostReboot reboots the VM of a server.
// @Summary Reboot the host (VM) of a database server
// @Description Asks the hypervisor for a clean reboot of the VM under the server (a stopped VM is started), in the background. Only an orchestrator that drives the hosts (proxmox) supports it.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 202 {object} map[string]string "Host reboot started"
// @Failure 403 {object} map[string]string "No valid ACL"
// @Failure 404 {object} map[string]string "Cluster Not Found or Server Not Found"
// @Failure 409 {object} map[string]string "A host action is already running on the server"
// @Failure 501 {object} map[string]string "The orchestrator does not drive the hosts"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/host-reboot [post]
func (repman *ReplicationManager) handlerMuxServerHostReboot(w http.ResponseWriter, r *http.Request) {
	repman.hostAction(w, r, "reboot", func(o cluster.Orchestrator, s *cluster.ServerMonitor) error { return o.RebootHost(s) })
}

// hostStatusResponse is the host status as the API serves it.
type hostStatusResponse struct {
	Node          string `json:"node"`
	VMID          string `json:"vmid"`
	State         string `json:"state"`
	UptimeSeconds int64  `json:"uptimeSeconds"`
	ActionRunning string `json:"actionRunning,omitempty"`
}

// handlerMuxServerHostStatus answers what the hypervisor says of the VM of a server.
// @Summary Status of the host (VM) of a database server
// @Description Answers the hypervisor node, the vmid, the state (running, stopped, paused, unknown) and the uptime of the VM under the server, and the host action running on it if any. Only an orchestrator that drives the hosts (proxmox) supports it.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} hostStatusResponse "The host status"
// @Failure 403 {object} map[string]string "No valid ACL"
// @Failure 404 {object} map[string]string "Cluster Not Found or Server Not Found"
// @Failure 501 {object} map[string]string "The orchestrator does not drive the hosts"
// @Failure 502 {object} map[string]string "The hypervisor did not answer"
// @Router /api/clusters/{clusterName}/servers/{serverName}/host-status [get]
func (repman *ReplicationManager) handlerMuxServerHostStatus(w http.ResponseWriter, r *http.Request) {
	mycluster, node, ok := repman.hostRequest(w, r)
	if !ok {
		return
	}
	st, err := mycluster.Orchestrator().HostStatus(node)
	if err != nil {
		code := http.StatusBadGateway
		if errors.Is(err, cluster.ErrOrchestratorNotSupported) {
			code = http.StatusNotImplemented
		}
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	resp := hostStatusResponse{Node: st.Node, VMID: st.VMID, State: st.State, UptimeSeconds: int64(st.Uptime.Seconds())}
	if action, busy := hostActionsInFlight.Load(mycluster.Name + "/" + node.URL); busy {
		resp.ActionRunning, _ = action.(string)
	}
	json.NewEncoder(w).Encode(resp)
}
