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
	"os"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
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
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-restart", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-reprov", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedReprov)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-prov", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedProv)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-unprov", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedUnprov)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-start", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedStart)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-stop", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedStop)),
	))

	router.Handle("/api/clusters/{clusterName}/need-rolling-reprov", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedRollingReprov)),
	))

	router.Handle("/api/clusters/{clusterName}/need-rolling-restart", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedRollingRestart)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsSlaveStatus)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/config", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/write-log/{module}/{level}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersWriteLog)),
	))
}

func (repman *ReplicationManager) apiDatabaseProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortBackup)),
	))
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
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/switchover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/set-prefered", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSetPrefered)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/set-unrated", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSetUnrated)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/set-ignored", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSetIgnored)),
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

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/wait-innodb-purge", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerWaitInnoDBPurge)),
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
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reset-slave-all", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerResetSlaveAll)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/flush-logs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerFlushLogs)),
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

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/run-jobs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRunJobs)),
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			if vars["backupMethod"] == "logicalbackup" {
				node.JobReseedLogicalBackup()
			}
			if vars["backupMethod"] == "logicalmaster" {
				node.RejoinDirectDump()
			}
			if vars["backupMethod"] == "physicalbackup" {
				node.JobReseedPhysicalBackup()
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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

func (repman *ReplicationManager) handlerMuxServerSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive switchover request")
			savedPrefMaster := mycluster.GetPreferedMasterList()
			if mycluster.IsMasterFailed() {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Master failed, cannot initiate switchover")
				http.Error(w, "Leader is failed can not promote", http.StatusBadRequest)
				return
			}
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API force for prefered leader: %s", node.URL)
			mycluster.SetPrefMaster(node.URL)
			mycluster.MasterFailover(false)
			mycluster.SetPrefMaster(savedPrefMaster)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerSetPrefered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as prefered request")
			if !mycluster.IsInPreferedHosts(node) {
				savedPrefMaster := mycluster.GetPreferedMasterList()
				if savedPrefMaster == "" {
					mycluster.SetPrefMaster(node.URL)
				} else {
					mycluster.SetPrefMaster(savedPrefMaster + "," + node.URL)
				}
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

func (repman *ReplicationManager) handlerMuxServerSetUnrated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as unrated request")
			if mycluster.IsInPreferedHosts(node) {
				savedPrefMaster := mycluster.GetPreferedMasterList()
				if savedPrefMaster == node.URL {
					mycluster.SetPrefMaster("")
				} else {
					//Remove the prefered from list
					newPrefMaster := strings.Replace(savedPrefMaster, node.URL+",", "", -1)
					newPrefMaster = strings.Replace(newPrefMaster, ","+node.URL, "", -1)
					mycluster.SetPrefMaster(newPrefMaster)
				}
			}
			if mycluster.IsInIgnoredHosts(node) {
				savedIgnoredHost := mycluster.GetIgnoredHostList()
				if savedIgnoredHost == node.URL {
					mycluster.SetIgnoreSrv("")
				} else {
					//Remove the prefered from list
					newIgnoredHost := strings.Replace(savedIgnoredHost, node.URL+",", "", -1)
					newIgnoredHost = strings.Replace(newIgnoredHost, ","+node.URL, "", -1)
					mycluster.SetIgnoreSrv(newIgnoredHost)
				}
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

func (repman *ReplicationManager) handlerMuxServerSetIgnored(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as prefered request")
			if !mycluster.IsInIgnoredHosts(node) {
				savedIgnoredHost := mycluster.GetIgnoredHostList()
				if savedIgnoredHost == "" {
					mycluster.SetIgnoreSrv(node.URL)
				} else {
					mycluster.SetIgnoreSrv(savedIgnoredHost + "," + node.URL)
				}
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

func (repman *ReplicationManager) handlerWaitInnoDBPurge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.WaitInnoDBPurge()
			if err != nil {
				http.Error(w, err.Error(), 500)
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

func (repman *ReplicationManager) handlerMuxServerSwitchReadOnly(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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

func (repman *ReplicationManager) handlerMuxServerResetSlaveAll(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StopSlave()
			node.ResetSlave()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
func (repman *ReplicationManager) handlerMuxServerFlushLogs(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.FlushLogs()
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/is-master serverName-is-master
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//     schema:
//       type: string
//     examples:
//       text/plain: 200 -Valid Master!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Master
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Master!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServersIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}*/
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

func (repman *ReplicationManager) handlerMuxServerNeedRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasRestartCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No restart needed!"))
			http.Error(w, "No restart needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasReprovCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasReprovCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedProv(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasProvisionCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasProvisionCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedUnprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasUnprovisionCookie() {
				w.Write([]byte("200 -Need restart!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "Encoding error", 503)
		} else if proxy != nil {
			if proxy.HasUnprovisionCookie() {
				w.Write([]byte("200 -Need reprov!"))
				return
			}
			w.Write([]byte("503 -No reprov needed!"))
			http.Error(w, "No reprov needed", 503)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Server!"))
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			if node.HasWaitStartCookie() {
				w.Write([]byte("200 -Need start!"))
				node.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No start needed!"))

		} else if proxy != nil {
			if proxy.HasWaitStartCookie() {
				w.Write([]byte("200 -Need start!"))
				proxy.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No start needed!"))
			//http.Error(w, "No start needed", 501)
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
		}

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && node.IsDown() == false {
			if node.HasWaitStopCookie() {
				node.DelWaitStopCookie()
				w.Write([]byte("200 -Need stop!"))
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No stop needed!"))

		} else if proxy != nil {
			if proxy.HasWaitStopCookie() {
				w.Write([]byte("200 -Need stop!"))
				proxy.DelWaitStopCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No stop needed!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
		}

	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedRollingReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if mycluster.HasRequestDBRollingReprov() {
			w.Write([]byte("200 -Need rolling reprov!"))
			return
		}
		w.Write([]byte("503 -No rooling reprov needed!"))
		http.Error(w, "Encoding error", 503)

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxServerNeedRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if mycluster.HasRequestDBRollingRestart() {
			w.Write([]byte("200 -Need rolling restart!"))
			return
		}
		w.Write([]byte("503 -No rooling reprov restart!"))
		http.Error(w, "Encoding error", 503)

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
		/*	if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}*/
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

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/is-slave serverName-is-slave
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//     schema:
//       type: string
//     examples:
//       text/plain: 200 -Valid Slave!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Slave!
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Slave!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServersIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}*/
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXServersReadOnMaster)) {
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
		/*		if !repman.IsValidClusterACL(r, mycluster) {
				http.Error(w, "No valid ACL", 403)
				return
			}*/
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil && mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && ((node.IsSlave && node.HasReplicationIssue() == false) || (node.IsMaster() && node.ClusterGroup.Conf.PRXServersReadOnMaster)) {
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node.IsDown() == false && node.IsMaintenance == false {
			go node.JobBackupPhysical()
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
		if mycluster.Conf.APISecureConfig {
			if !repman.IsValidClusterACL(r, mycluster) {
				http.Error(w, "No valid ACL", 403)
				return
			}
		}
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			node.GetDatabaseConfig()
			data, err := os.ReadFile(string(node.Datadir + "/config.tar.gz"))
			if err != nil {
				r.URL.Path = r.URL.Path + ".tar.gz"
				w.WriteHeader(404)
				w.Write([]byte("404 Something went wrong reading : " + string(node.Datadir+"/config.tar.gz") + " " + err.Error() + " - " + http.StatusText(404)))
				return
			}
			w.Write(data)

		} else if proxy != nil {
			proxy.GetProxyConfig()
			data, err := os.ReadFile(string(proxy.GetDatadir() + "/config.tar.gz"))
			if err != nil {
				r.URL.Path = r.URL.Path + ".tar.gz"
				w.WriteHeader(404)
				w.Write([]byte("404 Something went wrong reading : " + string(proxy.GetDatadir()+"/config.tar.gz") + " " + err.Error() + " - " + http.StatusText(404)))

				return
			}
			w.Write(data)
		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}

func (repman *ReplicationManager) handlerMuxServersWriteLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// if mycluster.Conf.APISecureConfig {
		// 	if !repman.IsValidClusterACL(r, mycluster) {
		// 		http.Error(w, "No valid ACL", 403)
		// 		return
		// 	}
		// }

		// Decode the request body
		var msg struct {
			Message string `json:"message"`
		}
		err := json.NewDecoder(r.Body).Decode(&msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if mod := config.GetIndexFromModuleName(vars["module"]); mod > -1 {
			if config.IsValidLogLevel(vars["level"]) {
				node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
				proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
				if node != nil || proxy != nil {
					raddr := strings.Split(r.RemoteAddr, ":")
					var valid bool
					if node != nil && node.IP == raddr[0] {
						valid = true
					}
					if proxy != nil {
						if myip, err := dbhelper.CheckHostAddr(proxy.GetHost()); err == nil && myip == raddr[0] {
							valid = true
						}
					}

					if valid {
						messages := strings.Split(msg.Message, "\n")
						for _, m := range messages {
							// Prevent empty lines
							if m != "" {
								mycluster.LogModulePrintf(mycluster.Conf.Verbose, mod, vars["level"], m)
							}
						}
						w.Header().Set("Content-Type", "application/json")
						json.NewEncoder(w).Encode(ApiResponse{Data: "Message logged", Success: true})
					} else {
						http.Error(w, "The requester address is not same with node/proxy address", 500)
					}
				} else {
					http.Error(w, "No server", 500)
				}
			} else {
				http.Error(w, "Invalid Log Module Level", 500)
			}
		} else {
			http.Error(w, "Invalid Log Module Name", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}

func (repman *ReplicationManager) handlerMuxServerProcesslist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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

func (repman *ReplicationManager) handlerMuxRunJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.JobRunViaSSH("all")
			if err != nil {
				http.Error(w, "Encoding running job", 500)
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil && node.IsDown() == false {
			e := json.NewEncoder(w)
			e.SetIndent("", "\t")
			l, _, _ := node.GetSchemas()
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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

// swagger:operation GET /api/clusters/{clusterName}/servers/{serverName}/master-status serverName-master-status
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// - name: serverName
//   in: path
//   description: server to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//   '403':
//     description: No valid ACL
//     schema:
//       type: string
//     examples:
//       text/plain: No valid ACL
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: Encoding error
//     schema:
//       type: string
//     examples:
//       text/plain: Encoding error
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '500':
//     description: No cluster
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '503':
//     description: Not a Valid Server!
//     schema:
//       type: string
//     examples:
//       text/plain: 503 -Not a Valid Server!
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxServerMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
