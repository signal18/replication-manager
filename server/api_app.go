// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"net/http"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) apiAppProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR APPS
	router.Handle("/api/clusters/{clusterName}/apps/{appName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxApp)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppDeployments)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddDeployment)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropDeployment)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/stop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStop)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedReprov)),
	))
	// router.Handle("/api/terminal/connect/clusters/{clusterName}/apps/{serverName}", negroni.New(
	// 	negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	// ))
	// router.Handle("/api/terminal/connect/clusters/{clusterName}/apps/{serverName}/{command}", negroni.New(
	// 	negroni.Wrap(http.HandlerFunc(repman.handlerTerminal)),
	// ))
}

// @Summary Shows the apps for that specific named cluster
// @Description Shows the apps for that specific named cluster
// @Tags Apps
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} app.AppInterface "Server details retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/apps/{appName} [get]
func (repman *ReplicationManager) handlerMuxApp(w http.ResponseWriter, r *http.Request) {
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

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			data, _ := json.Marshal(node)
			var app app.App
			err := json.Unmarshal(data, &app)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			err = e.Encode(app)
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

// @Summary Start App Service
// @Description Start the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start [post]
func (repman *ReplicationManager) handlerMuxAppStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", 500)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			mycluster.OpenSVCStartAppService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Stop App Service
// @Description Stop the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Stopped"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/stop [post]
func (repman *ReplicationManager) handlerMuxAppStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", 500)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			mycluster.OpenSVCStopAppService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Provision App Service
// @Description Provision the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Provisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/provision [post]
func (repman *ReplicationManager) handlerMuxAppProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", 500)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			mycluster.OpenSVCProvisionAppService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Unprovision App Service
// @Description Unprovision the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App Service Unprovisioned"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/unprovision [post]
func (repman *ReplicationManager) handlerMuxAppUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if mycluster.GetOrchestrator() != "opensvc" {
			http.Error(w, "Orchestrator not supported", 500)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			mycluster.OpenSVCUnprovisionAppService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Check if App Needs Restart
// @Description Check if the app service for a given cluster and app needs a restart
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Need restart!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No restart needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/need-restart [get]
func (repman *ReplicationManager) handlerMuxAppNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil && node.IsDown() == false {
			if node.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "Encoding error", 503)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Check if App Needs Reprovision
// @Description Check if the app service for a given cluster and app needs reprovisioning
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Need reprov!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 503 {string} string "No reprov needed!" "Not a Valid Server!"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/need-reprov [get]
func (repman *ReplicationManager) handlerMuxAppNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil && node.IsDown() == false {
			if node.HasReprovCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Shows the deployments for that specific named app
// @Description Shows the deployments for that specific named app
// @Tags Apps
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {object} config.Deployments "Deployments retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments [get]
func (repman *ReplicationManager) handlerMuxAppDeployments(w http.ResponseWriter, r *http.Request) {
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

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			err := e.Encode(node.Deployments)
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

// @Summary Add Deployment
// @Description Add a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Deployment added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/add [post]
func (repman *ReplicationManager) handlerMuxAddDeployment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			deployment := config.Deployment{}
			err := json.NewDecoder(r.Body).Decode(&deployment)
			if err != nil {
				http.Error(w, "Error decoding JSON", 500)
				return
			}
			node.Deployments = append(node.Deployments, deployment)
			mycluster.SetIsNeedGitPush(true)
			w.Write([]byte("Deployment added"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Drop Deployment
// @Description Drop a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param deployName path string true "Deployment Name"
// @Success 200 {string} string "Deployment dropped"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/drop [post]
func (repman *ReplicationManager) handlerMuxDropDeployment(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			deployment := config.Deployment{}
			err := json.NewDecoder(r.Body).Decode(&deployment)
			if err != nil {
				http.Error(w, "Error decoding JSON", 500)
				return
			}
			for i, d := range node.Deployments {
				if d.Name == deployment.Name {
					node.Deployments = append(node.Deployments[:i], node.Deployments[i+1:]...)
					mycluster.SetIsNeedGitPush(true)
					w.Write([]byte("Deployment dropped"))
					return
				}
			}
			http.Error(w, "Deployment not found", 500)
			return
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}
