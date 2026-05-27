// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) apiProxyProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR PROXIES
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxy)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/stop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyStop)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyStart)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/abort", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyAbort)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/clear", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyClear)),
	)).Methods("POST")
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/need-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyNeedRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/need-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxyNeedReprov)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/staging/{isStaging}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxySetStaging)),
	))
	router.Handle("/api/terminal/connect/clusters/{clusterName}/proxies/{serverName}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	))
	router.Handle("/api/terminal/connect/clusters/{clusterName}/proxies/{serverName}/{command}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	))
}

// @Summary Shows the proxies for that specific named cluster
// @Description Shows the proxies for that specific named cluster
// @Tags Proxies
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.Proxy "Server details retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/proxies/{proxyName} [get]
func (repman *ReplicationManager) handlerMuxProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", 500)
			return
		}

		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			data, _ := json.Marshal(node)
			var prx cluster.Proxy
			err := json.Unmarshal(data, &prx)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			err = e.Encode(prx)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Start Proxy Service
// @Description Start the proxy service for a given cluster and proxy
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Proxy Service Started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/start [post]
func (repman *ReplicationManager) handlerMuxProxyStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			mycluster.StartProxyService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Stop Proxy Service
// @Description Stop the proxy service for a given cluster and proxy
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Proxy Service Stopped"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/stop [post]
func (repman *ReplicationManager) handlerMuxProxyStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			mycluster.StopProxyService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxProxyAbort(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"})
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "Abort is only supported for OpenSVC orchestrator"})
		return
	}

	node := mycluster.GetProxyFromName(vars["proxyName"])
	if node == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Proxy Not Found"})
		return
	}

	err := mycluster.OpenSVCAbortProxyService(node)
	if err != nil {
		if errors.Is(err, cluster.ErrOpenSVCAbortNotSupported) {
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to abort proxy service: %s", err)})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Abort requested successfully",
		"proxy":   node.GetName(),
	})
}

func (repman *ReplicationManager) handlerMuxProxyClear(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Cluster Not Found"})
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "No valid ACL"})
		return
	}

	if mycluster.GetOrchestrator() != "opensvc" {
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "Clear is only supported for OpenSVC orchestrator"})
		return
	}

	node := mycluster.GetProxyFromName(vars["proxyName"])
	if node == nil {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "Proxy Not Found"})
		return
	}

	nodeParam := r.URL.Query().Get("node")
	err := mycluster.OpenSVCClearProxyInstanceState(node, nodeParam)
	if err != nil {
		if errors.Is(err, cluster.ErrOpenSVCClearNotSupported) {
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("Failed to clear proxy instance state: %s", err)})
		return
	}

	effectiveNode := nodeParam
	if effectiveNode == "" {
		effectiveNode = node.GetAgent()
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Instance monitor state cleared successfully",
		"proxy":   node.GetName(),
		"node":    effectiveNode,
	})
}

// @Summary Provision Proxy Service
// @Description Provision the proxy service for a given cluster and proxy
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Proxy Service Provisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/provision [post]
func (repman *ReplicationManager) handlerMuxProxyProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			mycluster.InitProxyService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Unprovision Proxy Service
// @Description Unprovision the proxy service for a given cluster and proxy
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Proxy Service Unprovisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision [post]
func (repman *ReplicationManager) handlerMuxProxyUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			mycluster.UnprovisionProxyService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Get Sphinx Indexes
// @Description Get the Sphinx indexes for a given cluster
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Sphinx Indexes"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "Something went wrong"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/sphinx-indexes [get]
func (repman *ReplicationManager) handlerMuxSphinxIndexes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		data, err := os.ReadFile(mycluster.GetConf().SphinxConfig)
		if err != nil {
			w.WriteHeader(404)
			w.Write([]byte("404 Something went wrong - " + http.StatusText(404)))
			return
		}
		w.Write(data)
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Check if Proxy Needs Restart
// @Description Check if the proxy service for a given cluster and proxy needs a restart
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Need restart!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No restart needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/need-restart [get]
func (repman *ReplicationManager) handlerMuxProxyNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil && !node.IsDown() {
			if node.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "Encoding error", http.StatusServiceUnavailable)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Check if Proxy Needs Reprovision
// @Description Check if the proxy service for a given cluster and proxy needs reprovisioning
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Success 200 {string} string "Need reprov!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No reprov needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/need-reprov [get]
func (repman *ReplicationManager) handlerMuxProxyNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil && !node.IsDown() {
			if node.HasReprovCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", http.StatusServiceUnavailable)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Set Staging
// @Description Set the proxy service for a given cluster and proxy to staging
// @Tags Proxies
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param proxyName path string true "Proxy Name"
// @Param isStaging path string true "Is Staging"
// @Success 200 {string} string "Proxy Service Set to Staging"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Failure 503 {string} string "Not a Valid Server!"
// @Router /api/clusters/{clusterName}/proxies/{proxyName}/actions/staging/{isStaging} [post]
func (repman *ReplicationManager) handlerMuxProxySetStaging(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		node := mycluster.GetProxyFromName(vars["proxyName"])
		if node != nil {
			if vars["isStaging"] == "true" {
				mycluster.AddProxyToStagingHosts(node)
			} else if vars["isStaging"] == "false" {
				mycluster.DelProxyFromStagingHosts(node)
			} else {
				http.Error(w, "Invalid staging value", 500)
				return
			}
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
