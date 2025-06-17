// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/githelper"
)

func (repman *ReplicationManager) apiAppProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR APPS
	router.Handle("/api/clusters/{clusterName}/apps/{appName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxApp)),
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
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/settings/actions/set/{setting}/{value}", negroni.New(
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
	router.Handle("/api/clusters/{clusterName}/apps/{appId}/git/{volumedir}/actions/get-repo-tree", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGitRepoTree)),
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
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start [post]
// @Router /api/clusters/{clusterName}/apps/{appName}/actions/start/{node} [post]
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
			mycluster.OpenSVCRestartAppService(app, vars["node"])
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
			err := mycluster.OpenSVCProvisionAppService(node)
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

			for gidx, v := range node.AppConfig.Deployment.Variables {
				if v.Type == "secret" {
					dep, err = jsonparser.Set(dep, []byte(`"*****"`), "variables", fmt.Sprintf("[%d]", gidx), "value")
					if err != nil {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
						http.Error(w, "Encoding error", 500)
						return
					}
				}
			}

			for gidx := range node.AppConfig.Deployment.GitClones {
				dep, err = jsonparser.Set(dep, []byte(`"*****"`), "gitClones", fmt.Sprintf("[%d]", gidx), "pass")
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
// @Param value body object{value=string} true "New value for the field"
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

		replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ".", "_")

		node := mycluster.GetAppFromName(vars["appName"])
		if node != nil {
			var newValue string
			var condValue []config.AgentVariable
			if vars["field"] == "variables" && vars["key"] == "conditional" {
				type ConditionalValue struct {
					Value []config.AgentVariable `json:"value"`
				}
				var body ConditionalValue
				err := json.NewDecoder(r.Body).Decode(&body)
				if err != nil {
					http.Error(w, "Error decoding JSON: "+err.Error(), 500)
					return
				}

				condValue = body.Value
				sort.Sort(config.AVSorter(condValue))
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

			index, err := strconv.ParseInt(vars["index"], 10, 64)
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
				if index >= int64(len(node.AppConfig.Deployment.Routes)) {
					http.Error(w, "Index out of range for routes", 500)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "cname":
					node.AppConfig.Deployment.Routes[index].CName = newValue
				case "port":
					node.AppConfig.Deployment.Routes[index].Port = newValue
				case "protocol":
					if newValue != "tcp" && newValue != "https" {
						http.Error(w, "Invalid protocol. Must be 'tcp' or 'https'", 500)
						return
					}
					node.AppConfig.Deployment.Routes[index].Protocol = newValue
				default:
					http.Error(w, "Invalid key for routes", 500)
					return
				}
			case "gitClones":
				if index >= int64(len(node.AppConfig.Deployment.GitClones)) {
					http.Error(w, "Index out of range for gitClones", 500)
					return
				}
				row := node.AppConfig.Deployment.GitClones[index]
				prefix := "GIT_CODE"
				if row.VolumeDir == "etc" {
					prefix = "GIT_CONFIG"
				}
				prefix = strings.ToUpper(prefix + "_" + replacer.Replace(row.Dest))

				var v *config.VariableMapping
				// Modify field based on key
				switch vars["key"] {
				case "dest":
					node.AppConfig.Deployment.GitClones[index].Dest = newValue
				case "repo":
					// strip protocol if present
					if strings.HasPrefix(newValue, "http://") || strings.HasPrefix(newValue, "https://") {
						newValue = strings.TrimPrefix(newValue, "http://")
						newValue = strings.TrimPrefix(newValue, "https://")
					}
					node.AppConfig.Deployment.GitClones[index].GitRepo = newValue
					v = node.AppConfig.GetDeploymentVariables(prefix + "_URL")
				case "branch":
					node.AppConfig.Deployment.GitClones[index].GitBranch = newValue
					v = node.AppConfig.GetDeploymentVariables(prefix + "_BRANCH")
				case "pass":
					node.AppConfig.Deployment.GitClones[index].GitPass = newValue
					v = node.AppConfig.GetDeploymentVariables(prefix + "_PASSWORD")
				case "user":
					node.AppConfig.Deployment.GitClones[index].GitUser = newValue
					v = node.AppConfig.GetDeploymentVariables(prefix + "_USER")
				default:
					http.Error(w, "Invalid key for gitClones", 500)
					return
				}

				v.Value = newValue
			case "variables":
				if index >= int64(len(node.AppConfig.Deployment.Variables)) {
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
					node.AppConfig.Deployment.Variables[index].Name = newValue
				case "value":
					node.AppConfig.Deployment.Variables[index].Value = newValue
				case "type":
					node.AppConfig.Deployment.Variables[index].Type = newValue
				case "conditional":
					// Check if the conditional is a valid JSON
					node.AppConfig.Deployment.Variables[index].Conditional = condValue
				default:
					http.Error(w, "Invalid key for variables", 500)
					return
				}
			case "paths":
				if index >= int64(len(node.AppConfig.Deployment.Paths)) {
					http.Error(w, "Index out of range for path", 500)
					return
				}

				// Modify field based on key
				switch vars["key"] {
				case "volumedir":
					node.AppConfig.Deployment.Paths[index].VolumeDir = newValue
				case "from":
					node.AppConfig.Deployment.Paths[index].From = newValue
				case "to":
					node.AppConfig.Deployment.Paths[index].To = newValue
				case "type":
					node.AppConfig.Deployment.Paths[index].Type = newValue

				default:
					http.Error(w, "Invalid key for path", 500)
					return
				}

			default:
				http.Error(w, "Invalid field", 500)
				return
			}

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
	replacer := strings.NewReplacer(" ", "_", "/", "_", "\\", "_", ":", "_", ".", "_")

	switch field {
	case "routes":
		var body []config.Route
		if err := decodeBody(r, &body, "routes", w); err != nil {
			http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
			return
		}

		for _, row := range body {
			if !isValidPortFormat(row.Port) {
				http.Error(w, "Invalid port format. Expected hostPort[:containerPort] with valid port numbers", http.StatusInternalServerError)
				return
			}
			node.AppConfig.Deployment.Routes = append(node.AppConfig.Deployment.Routes, row)
			affected = true
		}

	case "gitClones":
		var body []config.GitClone
		if err := decodeBody(r, &body, "git clone", w); err != nil {
			return
		}

		for _, row := range body {
			if row.GitRepo == "" || row.GitBranch == "" || row.Dest == "" {
				http.Error(w, "Git clone requires repo, branch, and dest fields", http.StatusInternalServerError)
				return
			}
			if row.GitPass != "" && row.GitUser == "" {
				http.Error(w, "Git clone requires user field when pass is provided", http.StatusInternalServerError)
				return
			}
			// Strip protocol if present
			if strings.HasPrefix(row.GitRepo, "http://") || strings.HasPrefix(row.GitRepo, "https://") {
				row.GitRepo = strings.TrimPrefix(row.GitRepo, "http://")
				row.GitRepo = strings.TrimPrefix(row.GitRepo, "https://")
			}
			// Only allow alphanumeric for dest
			regexpDest := regexp.MustCompile(`^[a-zA-Z0-9]+$`)
			if !regexpDest.MatchString(row.Dest) {
				http.Error(w, "Invalid dest format. Only alphanumeric characters are allowed for directory name", http.StatusInternalServerError)
				return
			}

			node.AppConfig.Deployment.GitClones = append(node.AppConfig.Deployment.GitClones, row)
			prefix := "GIT_CODE"
			if row.VolumeDir == "etc" {
				prefix = "GIT_CONFIG"
			}
			prefix = strings.ToUpper(prefix + "_" + replacer.Replace(row.Dest))
			branch := config.VariableMapping{Name: prefix + "_BRANCH", Value: row.GitBranch, Type: "env", Locked: true}
			url := config.VariableMapping{Name: prefix + "_URL", Value: row.GitRepo, Type: "env", Locked: true}
			gituser := config.VariableMapping{Name: prefix + "_USER", Value: row.GitUser, Type: "env", Locked: true}
			gitpassword := config.VariableMapping{Name: prefix + "_PASSWORD", Value: row.GitPass, Type: "secret", Locked: true}
			node.AppConfig.Deployment.Variables = append(node.AppConfig.Deployment.Variables, branch, url, gituser, gitpassword)
		}
		affected = true

	case "variables":
		var body []config.VariableMapping
		if err := decodeBody(r, &body, "variable", w); err != nil {
			return
		}
		for _, row := range body {
			old := node.AppConfig.GetDeploymentVariables(row.Name)
			if old != nil {
				http.Error(w, "Cannot duplicate variable with same name", 400)
				return
			}
			node.AppConfig.Deployment.Variables = append(node.AppConfig.Deployment.Variables, row)
		}
		affected = true

	case "paths":
		var body []config.PathMapping
		if err := decodeBody(r, &body, "path", w); err != nil {
			return
		}
		node.AppConfig.Deployment.Paths = append(node.AppConfig.Deployment.Paths, body...)
		affected = true

	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	if !affected {
		http.Error(w, "No rows added to the field", http.StatusInternalServerError)
		return
	}

	mycluster.ConfigManager.SaveConfig(mycluster, false)
	json.NewEncoder(w).Encode(map[string]string{"message": "Deployment field row added"})
}

func decodeBody[T any](r *http.Request, out *[]T, typename string, w http.ResponseWriter) error {
	err := json.NewDecoder(r.Body).Decode(out)
	if err != nil {
		http.Error(w, "Error decoding JSON: "+err.Error(), http.StatusInternalServerError)
		return err
	}
	if len(*out) == 0 {
		http.Error(w, "No "+typename+" provided", http.StatusInternalServerError)
		return fmt.Errorf("empty %s", typename)
	}
	return nil
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
	case "gitClones":
		if index >= len(node.AppConfig.Deployment.GitClones) {
			http.Error(w, "Index out of range for gitClones", http.StatusInternalServerError)
			return
		}
		gc := node.AppConfig.Deployment.GitClones[index]
		node.AppConfig.Deployment.GitClones = append(node.AppConfig.Deployment.GitClones[:index], node.AppConfig.Deployment.GitClones[index+1:]...)
		node.AppConfig.DropGitVariables(gc)
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Git %s on app %s deleted with linked variables", gc.Dest, node.Name)
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
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}
	// If we reach here, the row was successfully removed
	mycluster.ConfigManager.SaveConfig(mycluster, false)
	w.Write([]byte("Deployment field row removed"))
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
// @Param volumedir path string true "Volume Directory"
// @Success 200 {object} treehelper.FileTreeCache "Git repository tree structure"
// @Failure 400 {string} string "Invalid Git repository URL"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error creating Git client" or "Error getting repository tree"
// @Router /api/clusters/{clusterName}/apps/{appId}/git/{volumedir}/actions/get-repo-tree [get]
func (repman *ReplicationManager) handlerMuxGitRepoTree(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		app := mycluster.GetAppFromName(vars["appId"])
		if app == nil {
			http.Error(w, "Server Not Found", 500)
			return
		}

		gc := app.GetGitCloneFromVolumeDir(vars["volumedir"])
		if gc == nil {
			http.Error(w, "Git Clone Not Found", 500)
			return
		}

		var err error
		var gClient githelper.GitClientInterface
		var baseURL, projectID string
		if strings.Contains(gc.GitRepo, "github") {
			_, projectID, err = githelper.ParseGitHubURL(gc.GitRepo)
			if err != nil {
				http.Error(w, "Invalid GitHub repository URL: "+err.Error(), 400)
				return
			}
			gClient, err = githelper.NewGithubClient(gc.GitPass)
		} else {
			baseURL, projectID, err = githelper.ParseGitLabURL(gc.GitRepo)
			if err != nil {
				http.Error(w, "Invalid GitLab repository URL: "+err.Error(), 400)
				return
			}
			gClient, err = githelper.NewGitlabClient(baseURL, gc.GitPass)
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
		tree, err := gClient.GetRepositoryTree(cacheDir, projectID, gc.GitBranch, timeout)
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
