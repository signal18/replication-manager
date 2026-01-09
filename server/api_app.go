// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/tidwall/sjson"
)

func (repman *ReplicationManager) apiAppProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR APPS
	router.Handle("/api/clusters/{clusterName}/apps/{appName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxApp)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/service-opensvc", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetAppServiceConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/substitution", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSubstitutionVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/resolve-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppResolveTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppDeployments)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/{key}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxModifyDeploymentField)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddDeploymentFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropDeploymentFieldRow)),
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
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/stop/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStop)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/start/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppStart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/restart/{node}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/actions/need-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppNeedReprov)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/set/{setting}/{value:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSetSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/switch/{setting}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSwitchSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/clear/{setting}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppClearSetting)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppResetFromTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSaveAsTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template/{templateName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppSaveAsTemplate)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/{key}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxModifyStorageField)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddStorage)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropStorageFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitRepoTree)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree/{force}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitRepoTree)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appHost}/{appPort}/actions/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAppDropByName)),
	))
}

// @Summary Shows the apps for that specific named cluster
// @Description Shows the apps for that specific named cluster
// @Tags Apps
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.App "Server details retrieved successfully"
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
			var app cluster.App
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
// @Param node path string false "Node Name (optional, if not provided, will start default node). Can use ALL or * to start on all nodes."
// @Success 200 {string} string "App Service Started"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start/{node} [post]
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

		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			mycluster.OpenSVCStartAppService(app, vars["node"])
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
// @Param node path string false "Node Name (optional, if not provided, will stop default node). Can use ALL or * to stop on all nodes."
// @Success 200 {string} string "App Service Stopped"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/stop [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/stop/{node} [post]
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

		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			mycluster.OpenSVCStopAppService(app, vars["node"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// @Summary Restart App Service
// @Description Restart the app service for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param node path string false "Node Name (optional, if not provided, will restart default node). Can use ALL or * to restart on all nodes."
// @Success 200 {string} string "App Service Restarted"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/restart [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/restart/{node} [post]
func (repman *ReplicationManager) handlerMuxAppRestart(w http.ResponseWriter, r *http.Request) {
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

		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			rid := r.URL.Query().Get("rid")
			mycluster.OpenSVCRestartAppService(app, vars["node"], rid)
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
			err := mycluster.InitAppService(node)
			if err != nil {
				http.Error(w, "Failed to provision app service: "+err.Error(), 500)
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
// @Success 200 {array} config.Deployment "Server details retrieved successfully"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment [get]
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
			dep, err := json.MarshalIndent(node.AppConfig.Deployment, "", "\t")
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}

			for vidx, v := range node.AppConfig.Deployment.Variables {
				if v.Type == "secret" {
					dep, err = sjson.SetBytes(dep, fmt.Sprintf("variables.%d.value", vidx), "*****")
					if err != nil {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
						http.Error(w, "Encoding error", 500)
						return
					}
				}
			}

			for gidx := range node.AppConfig.Deployment.Storages.GitClones {
				dep, err = sjson.SetBytes(dep, fmt.Sprintf("storages.gitClones.%d.pass", gidx), "*****")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
					http.Error(w, "Encoding error", 500)
					return
				}
			}

			for midx := range node.AppConfig.Deployment.Storages.S3Mounts {
				dep, err = sjson.SetBytes(dep, fmt.Sprintf("storages.s3Mounts.%d.secretkey", midx), "*****")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
					http.Error(w, "Encoding error", 500)
					return
				}
			}

			w.Write(dep)
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

// @Summary Modify Deployment Field
// @Description Modify a specific field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to modify"
// @Param index path string true "Index of the field to modify"
// @Param key path string true "Key of the field to modify"
// @Param value body object{value=any} true "New value for the field"
// @Success 200 {string} string "Deployment field modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/{key}/modify [post]
func (repman *ReplicationManager) handlerMuxModifyDeploymentField(w http.ResponseWriter, r *http.Request) {
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
			var newValue string
			var condValue config.AVSlice
			if vars["field"] == "variables" && vars["key"] == "conditional" {
				type ConditionalValue struct {
					Value config.AVSlice `json:"value"`
				}
				var body ConditionalValue
				err := json.NewDecoder(r.Body).Decode(&body)
				if err != nil {
					http.Error(w, "Error decoding JSON: "+err.Error(), 500)
					return
				}

				condValue = body.Value
				sort.Sort(condValue)
			} else {
				type FieldValue struct {
					Value string `json:"value"`
				}
				var body FieldValue
				err := json.NewDecoder(r.Body).Decode(&body)
				if err != nil {
					http.Error(w, "Error decoding JSON: "+err.Error(), 500)
					return
				}

				newValue = body.Value
			}

			if vars["index"] == "" || vars["index"] == "undefined" {
				http.Error(w, "Index not provided", 500)
				return
			}

			index, err := strconv.Atoi(vars["index"])
			if err != nil {
				http.Error(w, "Error parsing index: "+err.Error(), 500)
				return
			}

			if index < 0 {
				http.Error(w, "Index cannot be negative", 500)
				return
			}

			if vars["key"] == "" || vars["key"] == "undefined" {
				// For gitClones, variables, and path, key is required
				http.Error(w, "Key not provided", 500)
				return
			}

			switch vars["field"] {
			// fields which are arrays of objects
			case "routes":
				if index >= len(node.AppConfig.Deployment.Routes) {
					http.Error(w, "Index out of range for routes", 500)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "cname":
					if node.AppConfig.Deployment.Routes[index].CName == newValue {
						http.Error(w, "CNAME unchanged", 500)
						return
					}
					for _, existing := range node.AppConfig.Deployment.Routes {
						if existing.CName == newValue {
							http.Error(w, "Cannot duplicate route with same CName", http.StatusInternalServerError)
							return
						}
					}
					node.AppConfig.Deployment.Routes[index].CName = newValue
				case "port":
					if node.AppConfig.Deployment.Routes[index].Port == newValue {
						http.Error(w, "Port unchanged", 500)
						return
					}
					node.AppConfig.Deployment.Routes[index].Port = newValue
				case "protocol":
					if node.AppConfig.Deployment.Routes[index].Protocol == newValue {
						http.Error(w, "Protocol unchanged", 500)
						return
					}

					if newValue != "tcp" && newValue != "https" {
						http.Error(w, "Invalid protocol. Must be 'tcp' or 'https'", 500)
						return
					}
					node.AppConfig.Deployment.Routes[index].Protocol = newValue
				default:
					http.Error(w, "Invalid key for routes", 500)
					return
				}
			case "variables":
				if index >= len(node.AppConfig.Deployment.Variables) {
					http.Error(w, "Index out of range for variables", 500)
					return
				}

				row := node.AppConfig.Deployment.Variables[index]
				if row.Locked {
					http.Error(w, "Unable to change name of locked variable. Please change the source of the variable instead.", 500)
					return
				}
				// Modify field based on key
				switch vars["key"] {
				case "name":
					if row.Name == newValue {
						http.Error(w, "Variable name unchanged", 500)
						return
					}
					row.Name = newValue
				case "value":
					if row.Value == newValue {
						http.Error(w, "Variable value unchanged", 500)
						return
					}
					row.Value = newValue
				case "type":
					if row.Type == newValue {
						http.Error(w, "Variable type unchanged", 500)
						return
					}
					row.Type = newValue
				case "conditional":
					old := row.Conditional
					// addFunc := func(new config.AgentVariable) config.AgentVariable {
					// 	// new.Value, _ = node.ClusterGroup.ParseAppTemplate(new.Value, node.AppClusterSubstitute)
					// 	return new
					// }
					// updateFunc := func(old, new config.AgentVariable) config.AgentVariable {
					// 	// new.Value, _ = node.ClusterGroup.ParseAppTemplate(new.Value, node.AppClusterSubstitute)
					// 	return new
					// }

					if row.Type == config.VariableTypeSecret {
						for i, v := range condValue {
							// Decrypt the value if it's a secret
							condValue[i].Value = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name+"@"+v.Agent, v.Value))
						}
					}

					row.Conditional = old.Merge(condValue, nil, nil)
				default:
					http.Error(w, "Invalid key for variables", 500)
					return
				}
				if row.Type == config.VariableTypeSecret {
					row.Value = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, row.Value))
				}
				node.AppConfig.Deployment.Variables[index] = row
			case "paths":
				if index >= len(node.AppConfig.Deployment.Paths) {
					http.Error(w, "Index out of range for paths", 500)
					return
				}

				deployment := node.AppConfig.Deployment
				if index >= len(deployment.Paths) {
					http.Error(w, "Index out of range for paths", 500)
					return
				}

				pm := deployment.Paths[index]

				// Modify field based on key
				switch vars["key"] {
				case "name":
					http.Error(w, "Cannot change name of path. Please drop the path and recreate it with the new name.", 500)
					return
				case "parent":
					if pm.ParentName == newValue {
						http.Error(w, "Parent unchanged", 500)
						return
					}

					if newValue == "" {
						pm.ParentName = ""
						pm.Parent = nil
					} else {
						parent, err := deployment.GetPathMapping(newValue)
						if err != nil {
							http.Error(w, "Parent path not found: "+err.Error(), 500)
							return
						}

						pm.ParentName = newValue
						pm.Parent = parent
					}

				case "dockerpath":
					if pm.DockerPath == newValue {
						http.Error(w, "Docker path unchanged", 500)
						return
					}

					pm.DockerPath = newValue
				case "srctype":
					newSourceType := config.SourceType(newValue)
					if pm.SourceType == newSourceType {
						http.Error(w, "Source type unchanged", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "Source type cannot be empty", 500)
						return
					}

					switch newValue {
					case string(config.SourceGit), string(config.SourceS3), string(config.SourceVolume):
						// Reset source name and path if type changes
						pm.SourceType = newSourceType
						pm.SourceName = "" // Reset source name if type changes
						if node.HasProvisionCookie() {
							node.SetReprovCookie()
						}
					default:
						http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", 500)
						return
					}
				case "srcname":
					if pm.SourceName == newValue {
						http.Error(w, "Source name unchanged", 500)
						return
					}

					if newValue == "" && pm.SourceType != "" {
						http.Error(w, "Source name cannot be empty when source type is set", 500)
						return
					}

					switch pm.SourceType {
					case config.SourceGit:
						source, err := deployment.GetGitClone(newValue)
						if err == nil {
							pm.SourcePath = source.GetSourcePath()
						}
					case config.SourceS3:
						source, err := deployment.GetS3Mount(newValue)
						if err == nil {
							pm.SourcePath = source.GetSourcePath()
						}
					case config.SourceVolume:
						source, err := deployment.GetVolumeByName(newValue)
						if err == nil {
							pm.SourcePath = source.GetSourcePath()
						}
					default:
						http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", 500)
						return
					}

					pm.SourceName = newValue

				case "srcpath":
					if pm.SourcePath == newValue {
						http.Error(w, "Source path unchanged", 500)
						return
					}

					if newValue != "" && newValue != "/" {
						pm.SourcePath = newValue
					} else if pm.SourceName != "" {
						switch pm.SourceType {
						case config.SourceGit:
							source, err := deployment.GetGitClone(newValue)
							if err == nil {
								pm.SourcePath = source.GetSourcePath()
							}
						case config.SourceS3:
							source, err := deployment.GetS3Mount(newValue)
							if err == nil {
								pm.SourcePath = source.GetSourcePath()
							}
						case config.SourceVolume:
							source, err := deployment.GetVolumeByName(newValue)
							if err == nil {
								pm.SourcePath = source.GetSourcePath()
							}
						default:
							http.Error(w, "Invalid source type. Must be 'git', 's3', or 'volume'", 500)
							return
						}
					} else {
						http.Error(w, "Source path cannot be empty", 500)
						return
					}

				default:
					http.Error(w, "Invalid key for path", 500)
					return
				}

				err := deployment.ResolvePath(pm)
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error resolving path after modification: ", err)
				}
			default:
				http.Error(w, "Invalid field", 500)
				return
			}

			mycluster.EnqueueRefreshAppTemplateMD5(node)

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Deployment field modified"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Add Deployment Field Row
// @Description Add a new row to a specific field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to add a row to (routes, gitClones, variables, path)"
// @Param body body any true "Array of objects depending on field: - routes: []config.Route - gitClones: []config.GitClone - variables: []config.VariableMapping - path: []config.PathMapping"
// @Success 200 {string} string "Deployment field row added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/add [post]
func (repman *ReplicationManager) handlerMuxAddDeploymentFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	var affected bool

	switch field {
	case "routes":
		var body []config.Route
		body, err := decodeSlice[config.Route](r, w, "route")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, row := range body {
			if !isValidPortFormat(row.Port) {
				http.Error(w, "Invalid port format. Expected hostPort with valid port numbers", http.StatusInternalServerError)
				return
			}

			if row.CName == "" {
				http.Error(w, "CName must be provided for route", http.StatusInternalServerError)
				return
			}

			if row.Protocol != "tcp" && row.Protocol != "https" {
				http.Error(w, "Invalid protocol. Must be 'tcp' or 'https'", http.StatusInternalServerError)
				return
			}

			// Check for duplicate route
			for _, existing := range node.AppConfig.Deployment.Routes {
				if existing.CName == row.CName {
					http.Error(w, "Cannot duplicate route with same CName", http.StatusInternalServerError)
					return
				}
			}

			node.AppConfig.Deployment.Routes = append(node.AppConfig.Deployment.Routes, row)
			affected = true
		}

	case "variables":
		var body []config.VariableMapping
		body, err := decodeSlice[config.VariableMapping](r, w, "variable")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, row := range body {
			old := node.AppConfig.GetDeploymentVariables(row.Name)
			if old != nil {
				http.Error(w, "Cannot duplicate variable with same name", 400)
				return
			}
			// row.Value, _ = mycluster.ParseAppTemplate(row.Value, node.AppClusterSubstitute)
			mycluster.SetAppVariableValue(node, row)
		}
		affected = true

	case "paths":
		var body []config.PathMapping
		body, err := decodeSlice[config.PathMapping](r, w, "path")
		if err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		errors := make([]error, 0)
		deployment := node.AppConfig.Deployment
		for _, row := range body {
			if row.DockerPath == "" || row.SourceName == "" || row.SourcePath == "" {
				http.Error(w, "All fields (destPath, sourceName, sourcePath) must be provided for path", http.StatusInternalServerError)
				return
			}
			if row.Name == "" {
				// Generate a unique name for the path if not provided
				row.Name = fmt.Sprintf("path-%s-%s", row.SourceType, row.SourceName)
			}
			err := deployment.InsertPath(row)
			if err != nil {
				errors = append(errors, err)
			} else {
				affected = true
			}
		}

		if affected {
			node.AppConfig.Deployment.SortPaths()
		}

		if len(errors) > 0 {
			errorMessages := make([]string, len(errors))
			for i, err := range errors {
				errorMessages[i] = err.Error()
			}
			http.Error(w, "Error adding paths: "+strings.Join(errorMessages, ", "), http.StatusInternalServerError)
			return
		}

	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	if !affected {
		http.Error(w, "No rows added to the field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	json.NewEncoder(w).Encode(map[string]string{"message": "Deployment field row added"})
}

func decodeStruct[T any](r *http.Request, w http.ResponseWriter, typename string) (*T, error) {
	var out T
	err := json.NewDecoder(r.Body).Decode(&out)
	if err != nil {
		http.Error(w, "Error decoding "+typename+": "+err.Error(), http.StatusBadRequest)
		return nil, err
	}
	return &out, nil
}

func decodeSlice[T any](r *http.Request, w http.ResponseWriter, typename string) ([]T, error) {
	var out []T
	err := json.NewDecoder(r.Body).Decode(&out)
	if err != nil {
		http.Error(w, "Error decoding "+typename+": "+err.Error(), http.StatusBadRequest)
		return nil, err
	}
	if len(out) == 0 {
		http.Error(w, "No "+typename+" provided", http.StatusBadRequest)
		return nil, fmt.Errorf("empty %s", typename)
	}
	return out, nil
}

func isValidPortFormat(value string) bool {
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return false
	}
	for _, part := range parts {
		p, err := strconv.Atoi(part)
		if err != nil || p < 0 || p > 65535 {
			return false
		}
	}
	return true
}

// @Summary Drop Deployment Field Row
// @Description Drop a specific row from a field in a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to drop a row from (routes, gitClones, variables, path)"
// @Param index path string true "Index of the row to drop"
// @Success 200 {string} string "Deployment field row removed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "Index out of range" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployment/{field}/index/{index}/drop [post]
func (repman *ReplicationManager) handlerMuxDropDeploymentFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	indexStr := vars["index"]
	if indexStr == "" || indexStr == "undefined" {
		http.Error(w, "Index not provided", http.StatusInternalServerError)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.Error(w, "Invalid index: "+indexStr, http.StatusInternalServerError)
		return
	}

	switch field {
	case "routes":
		if index >= len(node.AppConfig.Deployment.Routes) {
			http.Error(w, "Index out of range for routes", http.StatusInternalServerError)
			return
		}
		node.AppConfig.Deployment.Routes = append(node.AppConfig.Deployment.Routes[:index], node.AppConfig.Deployment.Routes[index+1:]...)
	case "variables":
		if index >= len(node.AppConfig.Deployment.Variables) {
			http.Error(w, "Index out of range for variables", http.StatusInternalServerError)
			return
		}
		if node.AppConfig.Deployment.Variables[index].Locked {
			http.Error(w, "Unable to drop locked variable. Please drop the source of the variable instead.", http.StatusInternalServerError)
			return
		}
		node.AppConfig.Deployment.Variables = append(node.AppConfig.Deployment.Variables[:index], node.AppConfig.Deployment.Variables[index+1:]...)
	case "paths":
		if index >= len(node.AppConfig.Deployment.Paths) {
			http.Error(w, "Index out of range for path", http.StatusInternalServerError)
			return
		}

		node.AppConfig.Deployment.Paths = append(node.AppConfig.Deployment.Paths[:index], node.AppConfig.Deployment.Paths[index+1:]...)
		node.AppConfig.Deployment.SortPaths()
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	// If we reach here, the row was successfully removed
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Deployment field row removed"))
}

// @Summary Add Storage to Deployment
// @Description Add a new storage to the deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to add storage to (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param body body any true "Array of objects depending on field: - git: []config.GitClone - local: []config.Volume - shared: []config.Volume - s3: []config.S3Mapping"
// @Success 200 {string} string "Storage added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/add [post]
// This endpoint adds a new storage to the deployment for a given cluster and app.
func (repman *ReplicationManager) handlerMuxAddStorage(w http.ResponseWriter, r *http.Request) {
	var err error
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}
	deployment := node.AppConfig.Deployment
	switch vars["field"] {
	case "gitClones":
		var row *config.GitClone
		row, err = decodeStruct[config.GitClone](r, w, "git clone")
		if err != nil {
			http.Error(w, "Error decoding git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}

		old, _ := node.GetGitClone(row.Name)
		if old != nil {
			http.Error(w, "Cannot duplicate git clone with same name", http.StatusInternalServerError)
			return
		}

		if row.GitPass != "" {
			row.GitPass = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, row.GitPass))
		}

		err := deployment.InsertGitClone(row)
		if err != nil {
			http.Error(w, "Error inserting git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "volumes":
		var row *config.Volume
		row, err = decodeStruct[config.Volume](r, w, "volume mapping")
		if err != nil {
			http.Error(w, "Error decoding volume mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}

		err := deployment.InsertVolume(row)
		if err != nil {
			http.Error(w, "Error inserting volume mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "s3Mounts":
		var row *config.S3Mount
		row, err = decodeStruct[config.S3Mount](r, w, "S3 directory")
		if err != nil {
			http.Error(w, "Error decoding S3 directory: "+err.Error(), http.StatusInternalServerError)
			return
		}

		s3node, _ := mycluster.GetAppByURL(row.Endpoint)
		if s3node == nil {
			http.Error(w, "S3 endpoint app not found: "+row.Endpoint, http.StatusInternalServerError)
			return
		}

		if row.Name == "" && row.Bucket != "" && row.Endpoint != "" {
			// Generate a unique name for the S3 mount if not provided
			row.Name = fmt.Sprintf("s3-%s-%s", s3node.Name, row.Bucket)
		}

		acckey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_USER", false)
		if err != nil || acckey == nil {
			http.Error(w, "S3 endpoint app does not have MINIO_ROOT_USER variable set", http.StatusInternalServerError)
			return
		}
		row.AccessKey = acckey.Value

		secretkey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_PASSWORD", false)
		if err != nil || secretkey == nil {
			http.Error(w, "S3 endpoint app does not have MINIO_ROOT_PASSWORD variable set", http.StatusInternalServerError)
			return
		}

		row.SecretKey = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(row.Name, secretkey.Value))

		region, _ := s3node.AppConfig.Deployment.GetVariableByName("REGION", false)
		if region != nil {
			row.Region = region.Value
		}

		if row.VolumeName == "" {
			row.Volume = mycluster.SetAppLocalMountVolume(node)
			row.VolumeName = row.Volume.Name
			row.VolumeDir = filepath.Join(row.Volume.VolumeDir, row.Name)
		}

		err = deployment.InsertS3Mount(row)
		if err != nil {
			http.Error(w, "Error inserting S3 mapping: "+err.Error(), http.StatusInternalServerError)
			return
		}
	default:
		http.Error(w, "Invalid storage type", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Storage added successfully"))
	return
}

// @Summary Modify Storage Field
// @Description Modify a specific field in a storage for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to modify (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param index path string true "Index of the field to modify"
// @Param key path string true "Key of the field to modify"
// @Param value body object{value=any} true "New value for the field"
// @Success 200 {string} string "Storage field modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/{key}/modify [post]
// This endpoint modifies a specific field in a storage for a given cluster and app.
func (repman *ReplicationManager) handlerMuxModifyStorageField(w http.ResponseWriter, r *http.Request) {
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
			var newValue string

			type FieldValue struct {
				Value string `json:"value"`
			}

			var body FieldValue
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				http.Error(w, "Error decoding JSON: "+err.Error(), 500)
				return
			}

			newValue = body.Value

			if vars["index"] == "" || vars["index"] == "undefined" {
				http.Error(w, "Index not provided", 500)
				return
			}

			index, err := strconv.Atoi(vars["index"])
			if err != nil {
				http.Error(w, "Error parsing index: "+err.Error(), 500)
				return
			}

			if index < 0 {
				http.Error(w, "Index cannot be negative", 500)
				return
			}

			if vars["key"] == "" || vars["key"] == "undefined" {
				// For gitClones, variables, and path, key is required
				http.Error(w, "Key not provided", 500)
				return
			}

			deployment := node.AppConfig.Deployment

			switch vars["field"] {
			// fields which are arrays of objects
			case "gitClones":
				if index >= len(deployment.Storages.GitClones) {
					http.Error(w, "Index out of range for gitClones", 500)
					return
				}

				// Get the git clone at the specified index
				gc := deployment.Storages.GitClones[index]
				if gc == nil {
					http.Error(w, "Git clone not found at index "+vars["index"], 500)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "name":
					if gc.Name == newValue {
						http.Error(w, "Name is the same as the current name", 500)
						return
					}
					if newValue == "" {
						http.Error(w, "Name cannot be empty", 500)
						return
					}
					old, _ := node.GetGitClone(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate git clone with same name", http.StatusInternalServerError)
						return
					}

					paths := deployment.GetGitPaths(gc.Name)
					gc.Name = newValue
					for _, p := range paths {
						p.SourceName = newValue
						deployment.ResolvePath(p)
					}

				case "repo":
					newValue = strings.TrimSuffix(newValue, ".git")
					if gc.GitRepo == newValue {
						http.Error(w, "Repo is the same as the current repo", 500)
						return
					}
					if newValue == "" {
						http.Error(w, "Repo cannot be empty", 500)
						return
					}
					gc.GitRepo = newValue

				case "branch":
					if gc.GitBranch == newValue {
						http.Error(w, "Branch is the same as the current branch", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "Branch cannot be empty", 500)
						return
					}

					gc.GitBranch = newValue

				case "volumename":
					if gc.VolumeName == newValue {
						http.Error(w, "VolumeName is the same as the current volume name", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "VolumeName cannot be empty", 500)
						return
					}

					newvol, err := deployment.GetVolumeByName(newValue)
					if err != nil {
						http.Error(w, "Error getting volume by name: "+err.Error(), 500)
						return
					}
					// Check for duplicate git volume path
					if deployment.HasDuplicateGitVolumePath(gc.Name, newValue, gc.VolumeDir) {
						gc.VolumeDir = filepath.Join(newvol.VolumeDir, gc.Name)
					}
					gc.VolumeName = newValue
					gc.Volume = newvol

					deployment.ResolveGitPaths(gc.Name)

				case "volumedir":
					if newValue == "" {
						curvol, err := deployment.GetVolumeByName(gc.VolumeName)
						if err != nil {
							http.Error(w, "Error getting volume by name: "+err.Error(), 500)
							return
						}
						newValue = filepath.Join(curvol.VolumeDir, gc.Name)
					}

					if gc.VolumeDir == newValue {
						http.Error(w, "VolumeDir is the same as the current volume dir", 500)
						return
					}

					if deployment.HasDuplicateGitVolumePath(gc.Name, gc.VolumeName, newValue) {
						http.Error(w, "Duplicate value for git volume path", 500)
						return
					}
					gc.VolumeDir = newValue

					deployment.ResolveGitPaths(gc.Name)
				case "user":
					if gc.GitUser == newValue {
						http.Error(w, "User is the same as the current user", 500)
						return
					}
					gc.GitUser = newValue
				case "pass":
					if mycluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass) == newValue {
						http.Error(w, "Password is the same as the current password", 500)
						return
					}

					gc.GitPass = mycluster.Conf.GetEncryptedString(newValue)
				default:
					http.Error(w, "Invalid key for gitClones", 500)
					return
				}
			case "volumes":
				if index >= len(deployment.Storages.Volumes) {
					http.Error(w, "Index out of range for volumes", 500)
					return
				}

				// Get the volume at the specified index
				vol := deployment.Storages.Volumes[index]
				if vol == nil {
					http.Error(w, "Volume not found at index "+vars["index"], 500)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "name":
					if vol.Name == newValue {
						http.Error(w, "Name is the same as the current name", 500)
						return
					}

					old, _ := deployment.GetVolumeByName(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate volume with same name", 500)
						return
					}

					paths := deployment.GetVolumePaths(vol.Name)
					vol.Name = newValue
					for _, p := range paths {
						p.SourceName = vol.Name
						deployment.ResolvePath(p)
					}

				case "poolname":
					if vol.PoolName == newValue {
						http.Error(w, "PoolName is the same as the current pool name", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "PoolName cannot be empty", 500)
						return
					}
					vol.PoolName = newValue

				case "volumedir":
					if vol.VolumeDir == newValue {
						http.Error(w, "VolumeDir is the same as the current volume dir", 500)
						return
					}
					if newValue == "" {
						http.Error(w, "VolumeDir cannot be empty", 500)
						return
					}
					vol.VolumeDir = newValue
					deployment.ResolveVolumePaths(vol.Name)

				default:
					http.Error(w, "Invalid key for volumes", 500)
					return
				}
			case "s3Mounts":
				if index >= len(deployment.Storages.S3Mounts) {
					http.Error(w, "Index out of range for s3Mounts", 500)
					return
				}

				s3Mount := deployment.Storages.S3Mounts[index]
				if s3Mount == nil {
					http.Error(w, "S3 mount not found at index "+vars["index"], 500)
					return
				}

				switch vars["key"] {
				case "name":
					if s3Mount.Name == newValue {
						http.Error(w, "Name is the same as the current name", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "Name cannot be empty", 500)
						return
					}

					old, _ := deployment.GetS3Mount(newValue)
					if old != nil {
						http.Error(w, "Cannot duplicate volume with same name", 500)
						return
					}

					paths := deployment.GetS3MountPaths(s3Mount.Name)
					s3Mount.Name = newValue
					for _, p := range paths {
						p.SourceName = newValue
						deployment.ResolvePath(p)
					}

				case "endpoint":
					if s3Mount.Endpoint == newValue {
						http.Error(w, "Endpoint is the same as the current endpoint", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "Endpoint cannot be empty", 500)
						return
					}

					s3node, _ := mycluster.GetAppByURL(newValue)
					if s3node == nil {
						http.Error(w, "S3 endpoint app not found: "+newValue, http.StatusInternalServerError)
						return
					}

					acckey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_USER", false)
					if err != nil || acckey == nil {
						http.Error(w, "S3 endpoint app does not have MINIO_ROOT_USER variable set", http.StatusInternalServerError)
						return
					}

					secretkey, err := s3node.AppConfig.Deployment.GetVariableByName("MINIO_ROOT_PASSWORD", false)
					if err != nil || secretkey == nil {
						http.Error(w, "S3 endpoint app does not have MINIO_ROOT_PASSWORD variable set", http.StatusInternalServerError)
						return
					}

					s3Mount.AccessKey = acckey.Value
					s3Mount.SecretKey = mycluster.Conf.GetEncryptedString(mycluster.Conf.GetDecryptedPassword(s3Mount.Name, secretkey.Value))

					region, _ := s3node.AppConfig.Deployment.GetVariableByName("REGION", false)
					if region != nil {
						s3Mount.Region = region.Value
					} else {
						s3Mount.Region = ""
					}

					s3Mount.Endpoint = newValue

				case "bucket":
					if s3Mount.Bucket == newValue {
						http.Error(w, "Bucket is the same as the current bucket", 500)
						return
					}
					if newValue == "" {
						http.Error(w, "Bucket cannot be empty", 500)
						return
					}
					s3Mount.Bucket = newValue
				case "volumename":
					if s3Mount.VolumeName == newValue {
						http.Error(w, "VolumeName is the same as the current volume name", 500)
						return
					}

					if newValue == "" {
						http.Error(w, "VolumeName cannot be empty", 500)
						return
					}

					newvol, err := deployment.GetVolumeByName(newValue)
					if err != nil {
						http.Error(w, "Error getting volume by name: "+err.Error(), 500)
						return
					}

					if deployment.HasDuplicateS3VolumePath(newValue, s3Mount.VolumeDir) {
						s3Mount.VolumeDir = filepath.Join(newvol.VolumeDir, s3Mount.Name)
					}
					s3Mount.VolumeName = newValue

					deployment.ResolveS3MountPaths(s3Mount.Name)
				case "volumedir":
					if s3Mount.VolumeDir == newValue {
						http.Error(w, "VolumeDir is the same as the current volume dir", 500)
						return
					}

					if newValue == "" {
						curvol, err := deployment.GetVolumeByName(s3Mount.VolumeName)
						if err != nil {
							http.Error(w, "Error getting volume by name: "+err.Error(), 500)
							return
						}
						newValue = filepath.Join(curvol.VolumeDir, s3Mount.Name)
					}

					if deployment.HasDuplicateS3VolumePath(s3Mount.VolumeName, newValue) {
						http.Error(w, "Duplicate value for S3 volume path", 500)
						return
					}
					s3Mount.VolumeDir = newValue

					deployment.ResolveS3MountPaths(s3Mount.Name)
				default:
					http.Error(w, "Invalid key for s3Mounts", 500)
					return
				}
			default:
				http.Error(w, "Invalid field", 500)
				return
			}

			mycluster.EnqueueRefreshAppTemplateMD5(node)

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Storage field modified"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Drop Storage Field Row
// @Description Drop a specific row from a field in a storage for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param field path string true "Field to drop a row from (gitClones, localDirectories, sharedDirectories, s3Mounts)"
// @Param index path string true "Index of the row to drop"
// @Success 200 {string} string "Storage field row removed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found"
// @Router /api/clusters/{clusterName}/apps/{appName}/storages/{field}/index/{index}/drop [post]
// This endpoint drops a specific row from a field in a storage for a given cluster and app.
func (repman *ReplicationManager) handlerMuxDropStorageFieldRow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	node := mycluster.GetAppFromName(vars["appName"])
	if node == nil {
		http.Error(w, "Server Not Found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	indexStr := vars["index"]
	if indexStr == "" || indexStr == "undefined" {
		http.Error(w, "Index not provided", http.StatusInternalServerError)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.Error(w, "Invalid index: "+indexStr, http.StatusInternalServerError)
		return
	}

	deployment := node.AppConfig.Deployment
	switch field {
	case "gitClones":
		if index >= len(deployment.Storages.GitClones) {
			http.Error(w, "Index out of range for gitClones", http.StatusInternalServerError)
			return
		}
		err := deployment.DropGitClone(deployment.Storages.GitClones[index])
		if err != nil {
			http.Error(w, "Error dropping git clone: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "volumes":
		if index >= len(deployment.Storages.Volumes) {
			http.Error(w, "Index out of range for volumes", http.StatusInternalServerError)
			return
		}
		err := deployment.DropVolume(deployment.Storages.Volumes[index])
		if err != nil {
			http.Error(w, "Error dropping volume: "+err.Error(), http.StatusInternalServerError)
			return
		}
	case "s3Mounts":
		if index >= len(deployment.Storages.S3Mounts) {
			http.Error(w, "Index out of range for s3Mounts", http.StatusInternalServerError)
			return
		}
		err := deployment.DropS3Mount(deployment.Storages.S3Mounts[index])
		if err != nil {
			http.Error(w, "Error dropping S3 mount: "+err.Error(), http.StatusInternalServerError)
			return
		}
	// Add more cases for other storage fields as needed
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	mycluster.EnqueueRefreshAppTemplateMD5(node)

	// If we reach here, the row was successfully removed
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Storage field row removed"))
}

// @Summary Set App Setting
// @Description Set a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to set"
// @Param value path string true "Value to set for the setting"
// @Success 200 {string} string "Setting updated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/set/{setting}/{value} [post]
func (repman *ReplicationManager) handlerMuxAppSetSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting and value
			if vars["setting"] == "" || vars["value"] == "" {
				http.Error(w, "Setting and value must be provided", 400)
				return
			}

			setting := vars["setting"]
			value := vars["value"]
			err := node.SetSetting(setting, value)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error setting %s: %s", setting, err.Error()), 500)
				return
			}

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting updated successfully"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Switch App Setting
// @Description Switch a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to switch"
// @Success 200 {string} string "Setting switched successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/switch/{setting} [post]
func (repman *ReplicationManager) handlerMuxAppSwitchSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting and value
			if vars["setting"] == "" {
				http.Error(w, "Setting must be provided", 400)
				return
			}

			setting := vars["setting"]
			err := node.SwitchSetting(setting)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error switch setting %s: %s", setting, err.Error()), 500)
				return
			}
			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting switched successfully"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Clear App Setting
// @Description Clear a specific setting for a given app in a cluster
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param setting path string true "Setting to clear"
// @Success 200 {string} string "Setting cleared successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appId}/settings/actions/clear/{setting} [post]
func (repman *ReplicationManager) handlerMuxAppClearSetting(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		node := mycluster.GetAppFromName(vars["appId"])
		if node != nil {
			// Validate setting
			if vars["setting"] == "" {
				http.Error(w, "Setting must be provided", 400)
				return
			}

			setting := vars["setting"]
			err := node.SetSetting(setting, "")
			if err != nil {
				http.Error(w, fmt.Sprintf("Error clearing setting %s: %s", setting, err.Error()), 500)
				return
			}
			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("Setting cleared successfully"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxGitRepoTree handles the HTTP request to get the tree structure of a Git repository.
// @Summary Get Git Repository Tree
// @Description Retrieves the tree structure of a specified Git repository.
// @Tags GitRepository
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Param gitName path string true "Git Name"
// @Param force path string false "Force refresh of the repository tree"
// @Success 200 {object} treehelper.FileTreeCache "Git repository tree structure"
// @Failure 400 {string} string "Invalid Git repository URL"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating Git client" or "Error getting repository tree"
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree [get]
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{gitName}/actions/get-repo-tree/{force} [get]
func (repman *ReplicationManager) handlerMuxGitRepoTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		force := strings.ToLower(vars["force"]) == "force"

		app := mycluster.GetAppFromName(vars["appId"])
		if app == nil {
			http.Error(w, "Server Not Found", 500)
			return
		}

		gc, _ := app.GetGitClone(vars["gitName"])
		if gc == nil {
			http.Error(w, "Git Clone Not Found", 500)
			return
		}

		var err error
		var gClient githelper.GitClientInterface
		var baseURL, projectID string
		gitpass := mycluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass)
		if strings.Contains(gc.GitRepo, "github") {
			_, projectID, err = githelper.ParseGitHubURL(gc.GitRepo)
			if err != nil {
				http.Error(w, "Invalid GitHub repository URL: "+err.Error(), 400)
				return
			}
			gClient, err = githelper.NewGithubClient(gitpass)
		} else {
			baseURL, projectID, err = githelper.ParseGitLabURL(gc.GitRepo)
			if err != nil {
				http.Error(w, "Invalid GitLab repository URL: "+err.Error(), 400)
				return
			}
			gClient, err = githelper.NewGitlabClient(baseURL, gitpass)
		}
		if err != nil {
			http.Error(w, "Error creating Git client: "+err.Error(), 500)
			return
		}

		// Get the repository tree
		cacheDir := filepath.Join(mycluster.WorkingDir, ".cache", "git", "repos")
		timeout := time.Duration(gc.Timeout) * time.Second
		if gc.Timeout <= 0 {
			timeout = 15 * time.Second // Default timeout if not specified
		}
		tree, err := gClient.GetRepositoryTree(cacheDir, projectID, gc.GitBranch, timeout, force)
		if err != nil {
			http.Error(w, "Error getting repository tree: "+err.Error(), 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(tree)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Get App Service Config
// @Description Retrieves the OpenSVC service configuration for a specific app.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "OpenSVC service configuration"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating OpenSVC config template" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/service-opensvc [get]
func (repman *ReplicationManager) handlerMuxGetAppServiceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			res, err := mycluster.OpenSVCGetAppTemplateV2(app)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC config template  %s", err)
				http.Error(w, "Error creating OpenSVC config template: "+err.Error(), 500)
				return
			}
			w.Write(res)
		} else {
			http.Error(w, "Not a valid app", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Get App Substitution Variables
// @Description Retrieves the substitution variables for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "Substitution variables for the app"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No substitution variables defined for this app" or "Not a valid app" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/substitution [get]
func (repman *ReplicationManager) handlerMuxAppSubstitutionVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		app := mycluster.GetAppFromName(vars["appName"])
		if app != nil {
			if app.AppClusterSubstitute == "" {
				http.Error(w, "No substitution variables defined for this app", 500)
				return
			}

			jsondata, _ := sjson.DeleteBytes([]byte(app.AppClusterSubstitute), "config.apps")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(jsondata)
		} else {
			http.Error(w, "Not a valid app", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Save App to Template
// @Description Saves the app configuration to a template directory for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param templateName path string true "Template Name"
// @Success 200 {string} string "App template saved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/settings/actions/save-as-template/{templateName} [post]
// This endpoint saves the app configuration to a template directory for a specific app in a cluster.
func (repman *ReplicationManager) handlerMuxAppSaveAsTemplate(w http.ResponseWriter, r *http.Request) {
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
			template := vars["templateName"]
			if template == "" {
				template = node.Name
			}
			templatePath := filepath.Join(mycluster.Conf.WorkingDir, ".templates", "apps", mycluster.Name, template+".toml")
			_, err := mycluster.SaveApp(node, templatePath)
			if err != nil {
				http.Error(w, "Error saving app template: "+err.Error(), 500)
				return
			}
			w.Write([]byte("App template saved successfully"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Reset App from Template
// @Description Reloads the app template configuration for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Success 200 {string} string "App template reloaded successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/settings/actions/reset-from-template [post]
func (repman *ReplicationManager) handlerMuxAppResetFromTemplate(w http.ResponseWriter, r *http.Request) {
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
			//Get the request body {template: value}
			var body struct {
				Template string `json:"template"`
			}
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error decoding request body: %v", err), 400)
				return
			}
			if body.Template == "" {
				http.Error(w, "Template name must be provided", 400)
				return
			}

			node.AppConfig.ProvAppTemplate = body.Template
			// Get the template content
			content, err := mycluster.GetTemplateContent(node.AppConfig.ProvAppTemplate)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error getting template content: %v", err), 500)
				return
			}

			parsedContent, _ := mycluster.ParseTemplateContent(node, content)

			// Load the new template into the Viper instance
			newViper, err := mycluster.LoadTemplateToViper(parsedContent)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error loading template: %v", err), 500)
				return
			}

			// Update the app configuration with the new Viper instance
			// Unmarshal the parsed content into the app configuration
			err = newViper.Unmarshal(node.AppConfig)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn, "Error unmarshalling parsed template file %s: %s", body.Template, err)
				http.Error(w, fmt.Sprintf("Error unmarshalling template: %v", err), 500)
				return
			}

			node.AppConfig.ProvAppTemplate = body.Template

			mycluster.ConfigManager.SaveConfig(mycluster, false)
			w.Write([]byte("App template reloaded successfully"))
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Resolve App Template Variable Values
// @Description Resolves the template variables for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param body body DecodedData true "Data to resolve in the template"
// @Success 200 {string} string "Resolved template variables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error parsing template" or "Server Not Found" or "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/resolve-template [post]
// This endpoint resolves the template variables for a specific app in a cluster.
func (repman *ReplicationManager) handlerMuxAppResolveTemplate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		var decodedData DecodedData

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode reading body :%s", err.Error()), http.StatusBadRequest)
			return
		}

		err = json.Unmarshal(body, &decodedData)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode body :%s. Err: %s", string(body), err.Error()), http.StatusBadRequest)
			return
		}

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			newData, err := mycluster.ParseAppTemplate(decodedData.Data, node.AppClusterSubstitute)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error parsing template: %s", err), 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			err = json.NewEncoder(w).Encode(newData)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error encoding response: %s", err.Error()), 500)
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

// @Summary Refresh App Template from Repo
// @Description Retrieves the tree structure of the application template repository for a specific cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} treehelper.FileTreeCache "Application template repository tree structure"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error getting repository tree" or "No cluster"
// @Router /api/clusters/{clusterName}/actions/refresh-apps-template [get]
// This endpoint retrieves the tree structure of the application template repository for a specific cluster.
func (repman *ReplicationManager) handlerMuxAppRefreshTemplateFromRepo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		repman.GetAppTemplates()

		jsondata, err := json.Marshal(repman.ServiceTemplates)
		if err != nil {
			http.Error(w, "Error getting repository tree: "+err.Error(), 500)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(jsondata)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Drop App Monitor
// @Description Drops the monitoring configuration for a specific app in a cluster.
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appId path string true "App ID"
// @Success 200 {string} string "App monitor dropped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No app found with the provided app ID" or "Cluster Not Found"
// @Router /api/clusters/{clusterName}/apps/{appHost}/{appPort}/actions/drop [post]
func (repman *ReplicationManager) handlerMuxAppDropByName(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if vars["appHost"] == "" || vars["appPort"] == "" {
			http.Error(w, "No app host or port provided", 400)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive drop app monitor for %s:%s", vars["appHost"], vars["appPort"])
		err := mycluster.RemoveAppMonitor(vars["appHost"], vars["appPort"])
		if err != nil {
			http.Error(w, "Error dropping app monitor: "+err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}
