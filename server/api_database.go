// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
)

func (repman *ReplicationManager) apiDatabaseUnprotectedHandler(router *mux.Router) {

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortBackup)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/config", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortConfig)),
	))

}

func (repman *ReplicationManager) apiDatabaseProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/processlist", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerProcesslist)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/variables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/status", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/status-delta", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStatusDelta)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/errorlog", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerErrorLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/slow-queries", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/digest-statements-pfs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerPFSStatements)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/digest-statements-slow", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerPFSStatementsSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/tables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerTables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/vtables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerVTables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/schemas", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSchemas)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/status-innodb", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerInnoDBStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/all-slaves-status", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAllSlavesStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/service-opensvc", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetDatabaseServiceConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/meta-data-locks", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerMetaDataLocks)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/query-response-time", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerQueryResponseTime)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/start", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStart)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/stop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStop)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/maintenance", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerMaintenance)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerProvision)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-physical", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerBackupPhysical)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-logical", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerBackupLogical)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-error-log", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerBackupErrorLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-slowquery-log", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerBackupSlowQueryLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reseed/{backupMethod}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerReseed)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-innodb-monitor", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetInnoDBMonitor)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-capture", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSlowQueryCapture)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSlowQueryTable)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSlowQuery)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-pfs-slow-query", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchPFSSlowQuery)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/set-long-query-time/{queryTime}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSetLongQueryTime)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-read-only", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSwitchReadOnly)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-meta-data-locks", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSwitchMetaDataLocks)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-query-response-time", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSwitchQueryResponseTime)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-sql-error-log", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSwitchSqlErrorLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reset-master", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerResetMaster)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reset-pfs-queries", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerResetPFSQueries)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/start-slave", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStartSlave)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/stop-slave", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerStopSlave)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/skip-replication-event", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSkipReplicationEvent)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-thread", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryKillThread)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-query", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryKillQuery)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-pfs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryExplainPFS)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-slowlog", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryExplainSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-pfs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryAnalyzePFS)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-slowlog", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryAnalyzePFS)),
	))
}

func (repman *ReplicationManager) handlerMuxQueryKillQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.KillQuery(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryKillThread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.KillThread(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxQueryExplainPFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {

			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _ := node.GetQueryExplainPFS(vars["queryDigest"])
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
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

func (repman *ReplicationManager) handlerMuxQueryExplainSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _ := node.GetQueryExplainSlowLog(vars["queryDigest"])
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
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

func (repman *ReplicationManager) handlerMuxQueryAnalyzePFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.GetQueryAnalyzePFS(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.StopDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupPhysical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupPhysical()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupLogical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			go node.JobBackupLogical()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobOptimize()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerReseed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			if vars["backupMethod"] == "logicalbackup" {
				node.JobReseedMysqldump()
			}
			if vars["backupMethod"] == "logicalmaster" {
				node.RejoinMasterDump()
			}
			if vars["backupMethod"] == "physicalbackup" {
				node.JobReseedXtraBackup()
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

func (repman *ReplicationManager) handlerMuxServerBackupErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupErrorLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerBackupSlowQueryLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.JobBackupSlowQueryLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.SwitchServerMaintenance(node.ServerID)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchReadOnly(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchReadOnly()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchMetaDataLocks(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchMetaDataLocks()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchQueryResponseTime(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchQueryResponseTime()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSwitchSqlErrorLog(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSqlErrorLog()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStartSlave(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StartSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStopSlave(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StopSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerResetMaster(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.ResetMaster()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerResetPFSQueries(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.ResetPFSQueries()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQueryCapture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCapture()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
func (repman *ReplicationManager) handlerMuxSwitchPFSSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCapturePFS()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQuery()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSlowQueryTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SwitchSlowQueryCaptureMode()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSwitchSetLongQueryTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SetLongQueryTime(vars["queryTime"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.StartDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.InitDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.UnprovisionDatabaseService(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return
		} else {

			w.Write([]byte("503 -Not a Valid Master!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Node not Found!"))
			return
		}
		if node != nil && mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Master!"))
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXReadOnMaster)) {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Slave!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXReadOnMaster)) {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			//	w.WriteHeader(http.StatusInternalServerError)
			http.Error(w, "-Not a Valid Slave!", 503)
			//	w.Write([]byte("503 -Not a Valid Slave!"))
			return
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node.IsDown() == false && node.IsMaintenance == false {
			node.JobBackupPhysical()
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("503 -Not a Valid Slave! Cluster IsActive=%t IsDown=%t IsMaintenance=%t HasReplicationIssue=%t ", mycluster.IsActive(), node.IsDown(), node.IsMaintenance, node.HasReplicationIssue())))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServersPortConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			node.GetMyConfig()
			data, err := ioutil.ReadFile(string(node.Datadir + "/config.tar.gz"))
			if err != nil {
				r.URL.Path = r.URL.Path + ".tar.gz"
				w.WriteHeader(404)
				w.Write([]byte("404 Something went wrong - " + http.StatusText(404)))
				return
			}
			w.Write(data)

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(fmt.Sprintf("503 -Not a Valid Slave! Cluster IsActive=%t IsDown=%t IsMaintenance=%t HasReplicationIssue=%t ", mycluster.IsActive(), node.IsDown(), node.IsMaintenance, node.HasReplicationIssue())))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerProcesslist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetProcessList()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerMetaDataLocks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetMetaDataLocks()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerQueryResponseTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			prl := node.GetQueryResponseTime()
			err := e.Encode(prl)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetErrorLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetSlowLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerPFSStatements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetPFSStatements()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerPFSStatementsSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetPFSStatementsSlowLog()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetVariables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerStatusDelta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetStatusDelta()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetTables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerVTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetVTables()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSchemas(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _ := node.GetSchemas()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerInnoDBStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetInnoDBStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerAllSlavesStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetAllSlavesStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l := node.GetMasterStatus()
			err := e.Encode(l)
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSkipReplicationEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			node.SkipReplicationEvent()
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSetInnoDBMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			node.SetInnoDBMonitor()
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxGetDatabaseServiceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			res := mycluster.GetDatabaseServiceConfig(node)
			w.Write([]byte(res))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}
