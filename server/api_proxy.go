// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/auth"
	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) GetProxyProtectedRoutes() []Route {
	//PROTECTED ENDPOINTS FOR PROXIES
	return []Route{
		{auth.ClusterPermission, config.GrantProvProxyUnprovision, "/api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision", repman.handlerMuxProxyUnprovision},
		{auth.ClusterPermission, config.GrantProvProxyProvision, "/api/clusters/{clusterName}/proxies/{proxyName}/actions/provision", repman.handlerMuxProxyProvision},
		{auth.ClusterPermission, config.GrantProxyStop, "/api/clusters/{clusterName}/proxies/{proxyName}/actions/stop", repman.handlerMuxProxyStop},
		{auth.ClusterPermission, config.GrantProxyStart, "/api/clusters/{clusterName}/proxies/{proxyName}/actions/start", repman.handlerMuxProxyStart},
	}

}

func (repman *ReplicationManager) handlerMuxProxyStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxProxyStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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
func (repman *ReplicationManager) handlerMuxProxyProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxProxyUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxSphinxIndexes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxProxyNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetProxyFromName(vars["proxyName"])
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

func (repman *ReplicationManager) handlerMuxProxyNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		node := mycluster.GetProxyFromName(vars["proxyName"])
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
