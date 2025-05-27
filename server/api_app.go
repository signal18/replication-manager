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
	"sort"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
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
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/drop/{deployName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropDeployment)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/index/{index}/{key}/modify", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxModifyDeploymentField)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddDeploymentFieldRow)),
	))
	router.Handle("/api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/index/{index}/drop", negroni.New(
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
// @Success 200 {array} config.Deployment "Server details retrieved successfully"
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
			deployments := make([]*config.Deployment, 0)
			for _, dep := range node.GetDeploymentConfigs() {
				deployments = append(deployments, dep)
			}

			sort.Sort(config.DeploymentSorter(deployments))

			depls, err := json.MarshalIndent(deployments, "", "\t")
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}

			for idx, d := range deployments {
				for gidx := range d.GitClones {
					depls, err = jsonparser.Set(depls, []byte(`"*****"`), fmt.Sprintf("[%d]", idx), "gitClones", fmt.Sprintf("[%d]", gidx), "pass")
					if err != nil {
						mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error maskin secrets JSON: ", err)
						http.Error(w, "Encoding error", 500)
						return
					}
				}
			}

			w.Write(depls)
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

// @Summary Add Deployment
// @Description Add a deployment for a given cluster and app
// @Tags Apps
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param appName path string true "App Name"
// @Param deployment body config.Deployment true "Deployment object"
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
				http.Error(w, "Error decoding JSON:"+err.Error(), 500)
				return
			}

			appcnf := node.GetAppConfig()
			if appcnf.Deployments == nil {
				appcnf.Deployments = make(map[string]*config.Deployment)
			}

			if deployment.GitClones == nil {
				deployment.GitClones = make([]config.GitClone, 0)
			}

			deployment.Order = len(appcnf.Deployments) + 1 // Set order based on current deployments count

			appcnf.Deployments[deployment.Name] = &deployment
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
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/drop/{deployName} [post]
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
			appcnf := node.GetAppConfig()
			if _, ok := appcnf.Deployments[vars["deployName"]]; !ok {
				http.Error(w, "Deployment not found", 500)
				return
			}
			delete(appcnf.Deployments, vars["deployName"])
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
// @Param deployName path string true "Deployment Name"
// @Param field path string true "Field to modify"
// @Param index path string true "Index of the field to modify"
// @Param key path string true "Key of the field to modify"
// @Param value body object{value=string} true "New value for the field"
// @Success 200 {string} string "Deployment field modified"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/index/{index}/{key}/modify [post]
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
			appcnf := node.GetAppConfig()
			dep, ok := appcnf.Deployments[vars["deployName"]]
			if !ok {
				http.Error(w, "Deployment not found", 500)
				return
			}

			type FieldValue struct {
				Value string `json:"value"`
			}
			var body FieldValue
			err := json.NewDecoder(r.Body).Decode(&body)
			if err != nil {
				http.Error(w, "Error decoding JSON: "+err.Error(), 500)
				return
			}

			newValue := body.Value

			switch vars["field"] {
			// fields which are arrays of objects
			case "gitClones", "variables", "path", "ports":
				if vars["index"] == "undefined" {
					http.Error(w, "Index not provided", 500)
					return
				}

				if vars["key"] == "undefined" && vars["field"] != "ports" {
					// For gitClones, variables, and path, key is required
					http.Error(w, "Key not provided", 500)
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

				if vars["field"] == "ports" {
					if index >= int64(len(dep.Ports)) {
						http.Error(w, "Index out of range for ports", 500)
						return
					}

					// check if the port is a valid port number [[0-65535] or the port is in the format "hostPort:containerPort"
					parts := strings.Split(newValue, ":")
					if len(parts) > 2 {
						http.Error(w, "Invalid port format, expected hostPort:containerPort", 500)
						return
					}

					for _, part := range parts {
						port, err := strconv.Atoi(part)
						if err != nil || port < 0 || port > 65535 {
							http.Error(w, "Invalid port number: "+part, 500)
							return
						}
					}

					// Modify field based on key
					dep.Ports[index] = newValue
				} else if vars["field"] == "gitClones" {
					if index >= int64(len(dep.GitClones)) {
						http.Error(w, "Index out of range for gitClones", 500)
						return
					}

					// Modify field based on key
					switch vars["key"] {
					case "repo":
						dep.GitClones[index].GitRepo = newValue
					case "branch":
						dep.GitClones[index].GitBranch = newValue
					case "dest":
						dep.GitClones[index].Dest = newValue
					case "pass":
						dep.GitClones[index].GitPass = newValue
					case "user":
						dep.GitClones[index].GitUser = newValue
					default:
						http.Error(w, "Invalid key for gitClones", 500)
						return
					}
				} else if vars["field"] == "variables" {
					if index >= int64(len(dep.Variables)) {
						http.Error(w, "Index out of range for variables", 500)
						return
					}

					// Modify field based on key
					switch vars["key"] {
					case "name":
						dep.Variables[index].Name = newValue
					case "value":
						dep.Variables[index].Value = newValue
					case "type":
						dep.Variables[index].Type = newValue
					default:
						http.Error(w, "Invalid key for variables", 500)
						return
					}
				} else if vars["field"] == "path" {
					if index >= int64(len(dep.Path)) {
						http.Error(w, "Index out of range for path", 500)
						return
					}

					// Modify field based on key
					switch vars["key"] {
					case "volumedir":
						dep.Path[index].VolumeDir = newValue
					case "from":
						dep.Path[index].From = newValue
					case "to":
						dep.Path[index].To = newValue
					case "type":
						dep.Path[index].Type = newValue

					default:
						http.Error(w, "Invalid key for path", 500)
						return
					}
				} else {
					http.Error(w, "Invalid field", 500)
					return
				}
			default:
				// fields which are simple strings
				switch vars["field"] {
				case "name":
					dep.Name = newValue
				case "dockerImg":
					dep.DockerImg = newValue
				case "dockerRunCmd":
					dep.DockerRunCmd = newValue
				default:
					http.Error(w, "Invalid field", 500)
					return
				}
			}

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
// @Param deployName path string true "Deployment Name"
// @Param field path string true "Field to add a row to (ports, gitClones, variables, path)"
// @Param body body any true "Array of objects depending on field:
//   - ports: []string
//   - gitClones: []GitClone
//   - variables: []VariableMapping
//   - path: []PathMapping"
//
// @Success 200 {string} string "Deployment field row added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/add [post]
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

	appcnf := node.GetAppConfig()
	dep, ok := appcnf.Deployments[vars["deployName"]]
	if !ok {
		http.Error(w, "Deployment not found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	var affected bool

	switch field {
	case "ports":
		var body []string
		if err := decodeBody(r, &body, "port", w); err != nil {
			return
		}

		for _, row := range body {
			if row == "" {
				continue
			}
			if !isValidPortFormat(row) {
				http.Error(w, "Invalid port format. Expected hostPort[:containerPort] with valid port numbers", http.StatusInternalServerError)
				return
			}
			dep.Ports = append(dep.Ports, row)
			affected = true
		}

	case "gitClones":
		var body []config.GitClone
		if err := decodeBody(r, &body, "git clone", w); err != nil {
			return
		}
		dep.GitClones = append(dep.GitClones, body...)
		affected = true

	case "variables":
		var body []config.VariableMapping
		if err := decodeBody(r, &body, "variable", w); err != nil {
			return
		}
		dep.Variables = append(dep.Variables, body...)
		affected = true

	case "path":
		var body []config.PathMapping
		if err := decodeBody(r, &body, "path", w); err != nil {
			return
		}
		dep.Path = append(dep.Path, body...)
		affected = true

	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}

	if !affected {
		http.Error(w, "No rows added to the field", http.StatusInternalServerError)
		return
	}

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
// @Param deployName path string true "Deployment Name"
// @Param field path string true "Field to drop a row from (ports, gitClones, variables, path)"
// @Param index path string true "Index of the row to drop"
// @Success 200 {string} string "Deployment field row removed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error decoding JSON" "Server Not Found" "Deployment not found" "Index out of range" "No cluster"
// @Router /api/clusters/{clusterName}/apps/{appName}/deployments/{deployName}/field/{field}/row/{index}/drop [post]
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

	appcnf := node.GetAppConfig()
	dep, ok := appcnf.Deployments[vars["deployName"]]
	if !ok {
		http.Error(w, "Deployment not found", http.StatusInternalServerError)
		return
	}

	field := vars["field"]
	indexStr := vars["index"]
	if indexStr == "undefined" {
		http.Error(w, "Index not provided", http.StatusInternalServerError)
		return
	}

	index, err := strconv.Atoi(indexStr)
	if err != nil || index < 0 {
		http.Error(w, "Invalid index: "+indexStr, http.StatusInternalServerError)
		return
	}

	switch field {
	case "ports":
		if index >= len(dep.Ports) {
			http.Error(w, "Index out of range for ports", http.StatusInternalServerError)
			return
		}
		dep.Ports = append(dep.Ports[:index], dep.Ports[index+1:]...)
	case "gitClones":
		if index >= len(dep.GitClones) {
			http.Error(w, "Index out of range for gitClones", http.StatusInternalServerError)
			return
		}
		dep.GitClones = append(dep.GitClones[:index], dep.GitClones[index+1:]...)
	case "variables":
		if index >= len(dep.Variables) {
			http.Error(w, "Index out of range for variables", http.StatusInternalServerError)
			return
		}
		dep.Variables = append(dep.Variables[:index], dep.Variables[index+1:]...)
	case "path":
		if index >= len(dep.Path) {
			http.Error(w, "Index out of range for path", http.StatusInternalServerError)
			return
		}
		dep.Path = append(dep.Path[:index], dep.Path[index+1:]...)
	default:
		http.Error(w, "Invalid field", http.StatusInternalServerError)
		return
	}
	// If we reach here, the row was successfully removed
	w.Write([]byte("Deployment field row removed"))
}
