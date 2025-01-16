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
	"os"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
)

func (repman *ReplicationManager) apiDatabaseUnprotectedHandler(router *mux.Router) {

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-failed", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsFailedStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-failed", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsFailedStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave-error", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveErrorStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave-error", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveErrorStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave-stopped", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveStopStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave-stopped", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveStopStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave-late", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveLateStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave-late", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsSlaveLateStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-standalone", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsStandAloneStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-standalone", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerIsStandAloneStatus)),
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
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-config-change", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedConfigChange)),
	))
	router.Handle("/api/clusters/{clusterName}/need-rolling-reprov", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedRollingReprov)),
	))

	router.Handle("/api/clusters/{clusterName}/need-rolling-restart", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerNeedRollingRestart)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/config", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/write-log/{task}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersWriteLog)),
	))

}

func (repman *ReplicationManager) apiDatabaseProtectedHandler(router *mux.Router) {
	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServer)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/attr/{attrName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAttribute)),
	))
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
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/set-maintenance", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerSetMaintenance)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/del-maintenance", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDelMaintenance)),
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

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/pitr", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerPITR)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reseed-cancel", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerReseedCancel)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/job-cancel/{task}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersTaskCancel)),
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
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetLongQueryTime)),
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
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxQueryAnalyzeSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/write-log/{task}", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersWriteLog)),
	))
}

// handlerMuxServer handles the HTTP request to get the server details within a cluster.
// @Summary Get server details
// @Description Retrieves the details of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} cluster.ServerMonitor "Server details retrieved successfully"
// @Failure 500 {string} string "No cluster" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName} [get]
func (repman *ReplicationManager) handlerMuxServer(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	var err error

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", 500)
			return
		}

		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			http.Error(w, "Server Not Found", 500)
			return
		}

		var cont map[string]interface{}
		data, _ := json.Marshal(node)
		list, _ := json.Marshal(node.BinaryLogFiles.ToNewMap())
		data, err = jsonparser.Set(data, list, "binaryLogFiles")
		if err != nil {
			http.Error(w, "Encoding error: "+err.Error(), 500)
			return
		}
		err = json.Unmarshal(data, &cont)
		if err != nil {
			http.Error(w, "Encoding error: "+err.Error(), 500)
			return
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(cont)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxServer handles the HTTP request to get the server details within a cluster.
// @Summary Get server details
// @Description Retrieves the details of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param attrName path string true "Attribute Name (using json path notation split by dot)"
// @Success 200 {object} cluster.ServerMonitor "Server Attribute (partial based on attrName)"
// @Failure 500 {string} string "No cluster" or "Server Not Found" or "Attribute not found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/attr/{attrName} [get]
func (repman *ReplicationManager) handlerMuxServerAttribute(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		uname := repman.GetUserFromRequest(r)
		if _, ok := mycluster.APIUsers[uname]; !ok {
			http.Error(w, "No Valid ACL", 500)
			return
		}

		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			http.Error(w, "Server Not Found", 500)
			return
		}

		var data, value []byte
		var valtype jsonparser.ValueType
		// get the value from the json path
		// if the attribute is binaryLogFiles, we need to convert the map to json
		// if the attribute is binaryLogFiles.*, we need to convert the map to json and get the value from the json path
		// otherwise, we just get the value from the json path
		if vars["attrName"] == "binaryLogFiles" {
			value, _ = json.Marshal(node.BinaryLogFiles.ToNewMap())
		} else if strings.HasPrefix(vars["attrName"], "binaryLogFiles.") {
			data, _ = json.Marshal(node.BinaryLogFiles.ToNewMap())
			value, valtype, _, _ = jsonparser.Get(data, strings.Split(vars["attrName"], ".")[1:]...)
		} else {
			data, _ = json.Marshal(node)
			value, valtype, _, _ = jsonparser.Get(data, strings.Split(vars["attrName"], ".")...)
		}

		// if the value is not found, return an error
		if valtype == jsonparser.NotExist {
			http.Error(w, "Attribute not found", 500)
			return
		}

		// Write the value to the response
		w.WriteHeader(http.StatusOK)
		w.Write(value)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxServerIsFailedStatus handles the HTTP request to check if a server is failed within a cluster.
// @Summary Check if a server is failed
// @Description Checks if a specified server within a cluster is in a failed state.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string false "Server Port"
// @Success 200 {string} string "200 -Server is failed!"
// @Failure 500 {string} string "500 -Server is not Failed!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-failed [get]
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-failed [get]
func (repman *ReplicationManager) handlerMuxServerIsFailedStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
			return
		}

		if node.IsFailed() {
			w.Write([]byte("200 -Server is failed!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Server is not Failed!"))
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

// handlerMuxServerIsSlaveErrorStatus handles the HTTP request to check if a server is in slave error state within a cluster.
// @Summary Check if a server is in slave error state
// @Description Checks if a specified server within a cluster is in a slave error state.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string false "Server Port"
// @Success 200 {string} string "200 -Server is in Slave Error state!"
// @Failure 500 {string} string "500 -Server is not in Slave Error state!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-slave-error [get]
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave-error [get]
func (repman *ReplicationManager) handlerMuxServerIsSlaveErrorStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
			return
		}

		if node.IsSlaveError() {
			w.Write([]byte("200 -Server is in Slave Error state!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Server is not in Slave Error state!"))
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

// handlerMuxServerIsSlaveStopStatus handles the HTTP request to check if a server replication is in OFF state for both IO and SQL thread within a cluster.
// @Summary Check if a server is in slave Stop state
// @Description Checks if a specified server within a cluster is in a slave Stop state.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string false "Server Port"
// @Success 200 {string} string "200 -Server is in Slave Stop state!"
// @Failure 500 {string} string "500 -Server is not in Slave Stop state!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-slave-Stop [get]
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave-Stop [get]
func (repman *ReplicationManager) handlerMuxServerIsSlaveStopStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
			return
		}

		if _, err := node.GetSlaveStatus(node.ReplicationSourceName); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Replication not found!"))
			return
		}

		if !node.IsSQLThreadRunning() && !node.IsIOThreadRunning() {
			w.Write([]byte("200 -Server IO and SQL Threads are stopped!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Server replication still has thread running!"))
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

// handlerMuxServerIsSlaveLateStatus handles the HTTP request to check if a server is in slave late state within a cluster.
// @Summary Check if server is in Slave Late state
// @Description Checks if the specified server within the cluster is in a "Slave Late" state.
// @Tags replication
// @Produce plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string false "Server Port"
// @Success 200 {string} string "200 -Server is in Slave Late state!"
// @Failure 500 {string} string "500 -No valid server!" "500 -Server is not in Slave Late state!" "500 -No cluster!"
// @Router /replication/{clusterName}/{serverName}/slave-late-status [get]
func (repman *ReplicationManager) handlerMuxServerIsSlaveLateStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
			return
		}

		if node.IsSlaveLate() {
			w.Write([]byte("200 -Server is in Slave Late state!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Server is not in Slave Late state!"))
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

// handlerMuxServerIsStandAloneStatus handles the HTTP request to check if a server is in standalone state within a cluster.
// @Summary Check if a server is in standalone state
// @Description Checks if a specified server within a cluster is in a standalone state.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string false "Server Port"
// @Success 200 {string} string "200 -Server is in Standalone state!"
// @Failure 500 {string} string "500 -Server is not in Standalone state!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-standalone [get]
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-standalone [get]
func (repman *ReplicationManager) handlerMuxServerIsStandAloneStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var node *cluster.ServerMonitor
		if v, ok := vars["serverPort"]; ok && v != "" {
			node = mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		} else {
			node = mycluster.GetServerFromName(vars["serverName"])
		}
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No valid server!"))
			return
		}

		if node.IsStandAlone() {
			w.Write([]byte("200 -Server is in Standalone state!"))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -Server is not in Standalone state!"))
		}
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("500 -No cluster!"))
		return
	}
}

// handlerMuxQueryKillQuery handles the HTTP request to kill a query on a specific server within a cluster.
// @Summary Kill a query on a server
// @Description Kills a query identified by its digest on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {string} string "Query killed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-query [get]
func (repman *ReplicationManager) handlerMuxQueryKillQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxQueryKillThread handles the HTTP request to kill a thread on a specific server within a cluster.
// @Summary Kill a thread on a server
// @Description Kills a thread identified by its digest on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {string} string "Query killed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/kill-thread [get]
func (repman *ReplicationManager) handlerMuxQueryKillThread(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxQueryExplainPFS handles the HTTP request to explain a query using PFS on a specific server within a cluster.
// @Summary Explain a query using PFS on a server
// @Description Explains a query identified by its digest on a specified server within a cluster using PFS.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {object} map[string]interface{} "Query explained successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-pfs [get]
func (repman *ReplicationManager) handlerMuxQueryExplainPFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxQueryExplainSlowLog handles the HTTP request to explain a query using the slow log on a specific server within a cluster.
// @Summary Explain a query using the slow log on a server
// @Description Explains a query identified by its digest on a specified server within a cluster using the slow log.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {object} map[string]interface{} "Query explained successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/explain-slowlog [get]
func (repman *ReplicationManager) handlerMuxQueryExplainSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxQueryAnalyzePFS handles the HTTP request to analyze a query using PFS on a specific server within a cluster.
// @Summary Analyze a query using PFS on a server
// @Description Analyzes a query identified by its digest on a specified server within a cluster using PFS.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {string} string "Query analyzed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-pfs [get]
func (repman *ReplicationManager) handlerMuxQueryAnalyzePFS(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxQueryAnalyzeSlowLog handles the HTTP request to analyze a query using the slow log on a specific server within a cluster.
// @Summary Analyze a query using the slow log on a server
// @Description Analyzes a query identified by its digest on a specified server within a cluster using the slow log.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryDigest path string true "Query Digest"
// @Success 200 {string} string "Query analyzed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/queries/{queryDigest}/actions/analyze-slowlog [get]
func (repman *ReplicationManager) handlerMuxQueryAnalyzeSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.GetQueryAnalyzeSlowLog(vars["queryDigest"])
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerStop handles the HTTP request to stop a server within a cluster.
// @Summary Stop a server
// @Description Stops a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server stopped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/stop [get]
func (repman *ReplicationManager) handlerMuxServerStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerBackupPhysical handles the HTTP request to perform a physical backup on a specific server within a cluster.
// @Summary Perform a physical backup on a server
// @Description Initiates a physical backup on a specified server within a cluster.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Backup initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/backup-physical [get]
func (repman *ReplicationManager) handlerMuxServerBackupPhysical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerBackupLogical handles the HTTP request to perform a logical backup on a specific server within a cluster.
// @Summary Perform a logical backup on a server
// @Description Initiates a logical backup on a specified server within a cluster.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Backup initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/backup-logical [get]
func (repman *ReplicationManager) handlerMuxServerBackupLogical(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerOptimize handles the HTTP request to optimize a server within a cluster.
// @Summary Optimize a server
// @Description Optimizes a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server optimized successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/optimize [get]
func (repman *ReplicationManager) handlerMuxServerOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerReseed handles the HTTP request to reseed a server within a cluster.
// @Summary Reseed a server
// @Description Reseeds a specified server within a cluster using the specified backup method.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param backupMethod path string true "Backup Method"
// @Success 200 {string} string "Reseed initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Error reseed logical backup" or "Error reseed physical backup"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/reseed/{backupMethod} [get]
func (repman *ReplicationManager) handlerMuxServerReseed(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			if vars["backupMethod"] == "logicalbackup" {
				err := node.JobReseedLogicalBackup("default")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "logical reseed restore failed %s", err)
					http.Error(w, "Error reseed logical backup", 500)
					return
				}
			}
			if vars["backupMethod"] == "logicalmaster" {
				err := node.RejoinDirectDump()
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "direct reseed restore failed %s", err)
				}
			}
			if vars["backupMethod"] == "physicalbackup" {
				err := node.JobReseedPhysicalBackup("default")
				if err != nil {
					mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "physical reseed restore failed %s", err)
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

// handlerMuxServerPITR handles the HTTP request to perform a point-in-time recovery (PITR) on a specific server within a cluster.
// @Summary Perform a point-in-time recovery on a server
// @Description Initiates a point-in-time recovery on a specified server within a cluster.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} ApiResponse "PITR initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Decode error" or "PITR error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/pitr [post]
func (repman *ReplicationManager) handlerMuxServerPITR(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			var formPit config.PointInTimeMeta
			// This will always true for making standalone
			formPit.IsInPITR = true
			err := json.NewDecoder(r.Body).Decode(&formPit)
			if err != nil {
				http.Error(w, fmt.Sprintf("Decode error :%s", err.Error()), http.StatusInternalServerError)
				return
			}

			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Requesting PITR on node %s", node.URL)

			err = node.ReseedPointInTime(formPit)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR on %s failed, err: %s", node.URL, err.Error())
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "PITR on %s failed, err: %s", node.URL, err.Error())
				http.Error(w, fmt.Sprintf("PITR error :%s", err.Error()), http.StatusInternalServerError)
				return
			} else {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "PITR on %s finished successfully", node.URL)
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "PITR on %s finished successfully", node.URL)
			}

			marshal, err := json.MarshalIndent(formPit, "", "\t")
			if err != nil {
				http.Error(w, fmt.Sprintf("Encode error :%s", err.Error()), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ApiResponse{Data: string(marshal), Success: true})
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerReseedCancel handles the HTTP request to cancel a reseed task on a specific server within a cluster.
// @Summary Cancel a reseed task on a server
// @Description Cancels a reseed task identified by its name on a specified server within a cluster.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param task path string true "Task Name"
// @Success 200 {string} string "Task canceled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Error canceling task"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/reseed-cancel/{task} [get]
func (repman *ReplicationManager) handlerMuxServerReseedCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			tasks := []string{"reseedmariabackup", "reseedxtrabackup", "flashbackmariabackup", "flashbackxtrabackup"}
			err := node.JobsCancelTasks(false, tasks...)
			if err != nil {
				http.Error(w, fmt.Sprintf("Error canceling %s task: %s", vars["task"], err.Error()), 500)
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

// handlerMuxServerBackupErrorLog handles the HTTP request to perform a backup of the error log on a specific server within a cluster.
// @Summary Perform a backup of the error log on a server
// @Description Initiates a backup of the error log on a specified server within a cluster.
// @Tags DatabaseLogs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Backup initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/backup-error-log [get]
func (repman *ReplicationManager) handlerMuxServerBackupErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerBackupSlowQueryLog handles the HTTP request to perform a backup of the slow query log on a specific server within a cluster.
// @Summary Perform a backup of the slow query log on a server
// @Description Initiates a backup of the slow query log on a specified server within a cluster.
// @Tags DatabaseLogs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Backup initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/backup-slowquery-log [get]
func (repman *ReplicationManager) handlerMuxServerBackupSlowQueryLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerMaintenance handles the HTTP request to toggle maintenance mode on a specific server within a cluster.
// @Summary Toggle maintenance mode on a server
// @Description Toggles the maintenance mode on a specified server within a cluster.
// @Tags DatabaseMaintenance
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Maintenance mode toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/maintenance [get]
func (repman *ReplicationManager) handlerMuxServerMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSetMaintenance handles the HTTP request to set a server to maintenance mode.
// @Summary Set a server to maintenance mode
// @Description Sets a specified server within a cluster to maintenance mode.
// @Tags DatabaseMaintenance
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server set to maintenance mode successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/set-maintenance [get]
func (repman *ReplicationManager) handlerMuxServerSetMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.SetMaintenance()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerDelMaintenance handles the HTTP request to delete maintenance mode on a specific server within a cluster.
// @Summary Delete maintenance mode on a server
// @Description Deletes the maintenance mode on a specified server within a cluster.
// @Tags DatabaseMaintenance
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Maintenance mode deleted successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/del-maintenance [get]
func (repman *ReplicationManager) handlerMuxServerDelMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.DelMaintenance()
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerSwitchover handles the HTTP request to perform a switchover on a specific server within a cluster.
// @Summary Perform a switchover on a server
// @Description Initiates a switchover on a specified server within a cluster.
// @Tags DatabaseTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Switchover initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Master failed, cannot initiate switchover"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/switchover [get]
func (repman *ReplicationManager) handlerMuxServerSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSetPrefered handles the HTTP request to set a server as preferred within a cluster.
// @Summary Set a server as preferred
// @Description Sets a specified server within a cluster as preferred.
// @Tags DatabaseTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server set as preferred successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/set-prefered [get]
func (repman *ReplicationManager) handlerMuxServerSetPrefered(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as prefered request")
			mycluster.AddPrefMaster(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerSetUnrated handles the HTTP request to set a server as unrated within a cluster.
// @Summary Set a server as unrated
// @Description Sets a specified server within a cluster as unrated.
// @Tags DatabaseTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server set as unrated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/set-unrated [get]
func (repman *ReplicationManager) handlerMuxServerSetUnrated(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive set node as unrated request")
			mycluster.RemovePrefMaster(node)
			mycluster.RemoveIgnoreSrv(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerSetIgnored handles the HTTP request to set a server as ignored within a cluster.
// @Summary Set a server as ignored
// @Description Sets a specified server within a cluster as ignored.
// @Tags DatabaseTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server set as ignored successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/set-ignored [get]
func (repman *ReplicationManager) handlerMuxServerSetIgnored(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive request: set node as ignored")
			mycluster.AddIgnoreSrv(node)
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerWaitInnoDBPurge handles the HTTP request to wait for InnoDB purge on a specific server within a cluster.
// @Summary Wait for InnoDB purge on a server
// @Description Waits for InnoDB purge on a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "InnoDB purge completed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Error waiting for InnoDB purge"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/wait-innodb-purge [get]
func (repman *ReplicationManager) handlerWaitInnoDBPurge(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSwitchReadOnly handles the HTTP request to toggle read-only mode on a specific server within a cluster.
// @Summary Toggle read-only mode on a server
// @Description Toggles the read-only mode on a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Read-only mode toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-read-only [get]
func (repman *ReplicationManager) handlerMuxServerSwitchReadOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSwitchMetaDataLocks handles the HTTP request to toggle metadata locks on a specific server within a cluster.
// @Summary Toggle metadata locks on a server
// @Description Toggles the metadata locks on a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Metadata locks toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-meta-data-locks [get]
func (repman *ReplicationManager) handlerMuxServerSwitchMetaDataLocks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSwitchQueryResponseTime handles the HTTP request to toggle query response time on a specific server within a cluster.
// @Summary Toggle query response time on a server
// @Description Toggles the query response time on a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Query response time toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-query-response-time [get]
func (repman *ReplicationManager) handlerMuxServerSwitchQueryResponseTime(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSwitchSqlErrorLog handles the HTTP request to toggle SQL error log on a specific server within a cluster.
// @Summary Toggle SQL error log on a server
// @Description Toggles the SQL error log on a specified server within a cluster.
// @Tags DatabaseLogs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "SQL error log toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-sql-error-log [get]
func (repman *ReplicationManager) handlerMuxServerSwitchSqlErrorLog(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerStartSlave handles the HTTP request to start the slave on a specific server within a cluster.
// @Summary Start the slave on a server
// @Description Starts the slave on a specified server within a cluster.
// @Tags DatabaseReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slave started successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/start-slave [get]
func (repman *ReplicationManager) handlerMuxServerStartSlave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerStopSlave handles the HTTP request to stop the slave on a specific server within a cluster.
// @Summary Stop the slave on a server
// @Description Stops the slave on a specified server within a cluster.
// @Tags DatabaseReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slave stopped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/stop-slave [get]
func (repman *ReplicationManager) handlerMuxServerStopSlave(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerResetSlaveAll handles the HTTP request to reset all slaves on a specific server within a cluster.
// @Summary Reset all slaves on a server
// @Description Resets all slaves on a specified server within a cluster.
// @Tags DatabaseReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slaves reset successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/reset-slave-all [get]
func (repman *ReplicationManager) handlerMuxServerResetSlaveAll(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			node.StopSlave()
			node.ResetSlave()
			node.SetSuspect() // For refreshing state
		} else {
			http.Error(w, "Server Not Found", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxServerFlushLogs handles the HTTP request to flush logs on a specific server within a cluster.
// @Summary Flush logs on a server
// @Description Flushes the logs on a specified server within a cluster.
// @Tags DatabaseLogs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Logs flushed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/flush-logs [get]
func (repman *ReplicationManager) handlerMuxServerFlushLogs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerResetMaster handles the HTTP request to reset the master on a specific server within a cluster.
// @Summary Reset the master on a server
// @Description Resets the master on a specified server within a cluster.
// @Tags DatabaseReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Master reset successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/reset-master [get]
func (repman *ReplicationManager) handlerMuxServerResetMaster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerResetPFSQueries handles the HTTP request to reset PFS queries on a specific server within a cluster.
// @Summary Reset PFS queries on a server
// @Description Resets PFS queries on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "PFS queries reset successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/reset-pfs-queries [get]
func (repman *ReplicationManager) handlerMuxServerResetPFSQueries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSwitchSlowQueryCapture handles the HTTP request to toggle slow query capture on a specific server within a cluster.
// @Summary Toggle slow query capture on a server
// @Description Toggles the slow query capture on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slow query capture toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-capture [get]
func (repman *ReplicationManager) handlerMuxSwitchSlowQueryCapture(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSwitchPFSSlowQuery handles the HTTP request to toggle PFS slow query capture on a specific server within a cluster.
// @Summary Toggle PFS slow query capture on a server
// @Description Toggles the PFS slow query capture on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "PFS slow query capture toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-pfs-slow-query [get]
func (repman *ReplicationManager) handlerMuxSwitchPFSSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSwitchSlowQuery handles the HTTP request to toggle slow query on a specific server within a cluster.
// @Summary Toggle slow query on a server
// @Description Toggles the slow query on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slow query toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query [get]
func (repman *ReplicationManager) handlerMuxSwitchSlowQuery(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSwitchSlowQueryTable handles the HTTP request to toggle slow query table mode on a specific server within a cluster.
// @Summary Toggle slow query table mode on a server
// @Description Toggles the slow query table mode on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Slow query table mode toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-slow-query-table [get]
func (repman *ReplicationManager) handlerMuxSwitchSlowQueryTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSetLongQueryTime handles the HTTP request to set the long query time on a specific server within a cluster.
// @Summary Set long query time on a server
// @Description Sets the long query time on a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param queryTime path string true "Query Time"
// @Success 200 {string} string "Long query time set successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/set-long-query-time/{queryTime} [get]
func (repman *ReplicationManager) handlerMuxSetLongQueryTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerStart handles the HTTP request to start a server within a cluster.
// @Summary Start a server
// @Description Starts a specified server within a cluster.
// @Tags DatabaseActions
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server started successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/start [get]
func (repman *ReplicationManager) handlerMuxServerStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerProvision handles the HTTP request to provision a server within a cluster.
// @Summary Provision a server
// @Description Provisions a specified server within a cluster.
// @Tags DatabaseProvision
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server provisioned successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/provision [get]
func (repman *ReplicationManager) handlerMuxServerProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerUnprovision handles the HTTP request to unprovision a server within a cluster.
// @Summary Unprovision a server
// @Description Unprovisions a specified server within a cluster.
// @Tags DatabaseProvision
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Server unprovisioned successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/unprovision [get]
func (repman *ReplicationManager) handlerMuxServerUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersIsMasterStatus handles the HTTP request to check if a server is a master within a cluster.
// @Summary Check if a server is a master
// @Description Checks if a specified server within a cluster is a master.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "200 -Valid Master!"
// @Failure 500 {string} string "No cluster"
// @Failure 503 {string} string "503 -Not a Valid Master!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-master [get]
func (repman *ReplicationManager) handlerMuxServersIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerNeedRestart handles the HTTP request to check if a server needs a restart.
// @Summary Check if a server needs a restart
// @Description Checks if a specified server within a cluster needs a restart.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need restart!"
// @Failure 500 {string} string "503 -Not a Valid Server!"
// @Failure 503 {string} string "503 -No restart needed!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-restart [get]
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

// handlerMuxServerNeedReprov handles the HTTP request to check if a server needs re-provisioning.
// @Summary Check if a server needs re-provisioning
// @Description Checks if a specified server within a cluster needs re-provisioning.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need reprov!"
// @Failure 500 {string} string "503 -Not a Valid Server!"
// @Failure 503 {string} string "503 -No reprov needed!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-reprov [get]
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

// handlerMuxServerNeedProv handles the HTTP request to check if a server needs provisioning.
// @Summary Check if a server needs provisioning
// @Description Checks if a specified server within a cluster needs provisioning.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need provisioning!"
// @Failure 500 {string} string "503 -Not a Valid Server!"
// @Failure 503 {string} string "503 -No provisioning needed!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-prov [get]
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

// handlerMuxServerNeedUnprov handles the HTTP request to check if a server needs unprovisioning.
// @Summary Check if a server needs unprovisioning
// @Description Checks if a specified server within a cluster needs unprovisioning.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need unprov!"
// @Failure 500 {string} string "503 -Not a Valid Server!"
// @Failure 503 {string} string "503 -No unprov needed!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-unprov [get]
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

// handlerMuxServerNeedStart handles the HTTP request to check if a server needs to start.
// @Summary Check if a server needs to start
// @Description Checks if a specified server within a cluster needs to start.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need start!"
// @Failure 500 {string} string "500 -No start needed!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-start [get]
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

// handlerMuxServerNeedStop handles the HTTP request to check if a server needs to stop.
// @Summary Check if a server needs to stop
// @Description Checks if a specified server within a cluster needs to stop.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need stop!"
// @Failure 500 {string} string "500 -No stop needed!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-stop [get]
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

// handlerMuxServerNeedConfigChange handles the HTTP request to check if a server needs a config change.
// @Summary Check if a server needs a config change
// @Description Checks if a specified server within a cluster needs a config change.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Need config change!"
// @Failure 500 {string} string "500 -No config change needed!" or "500 -No valid server!" or "500 -No cluster!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/need-config-change [get]
func (repman *ReplicationManager) handlerMuxServerNeedConfigChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		proxy := mycluster.GetProxyFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			if node.HasConfigCookie() {
				w.Write([]byte("200 -Need config change!"))
				node.DelConfigCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No config change needed!"))

		} else if proxy != nil {
			if proxy.HasConfigCookie() {
				w.Write([]byte("200 -Need config change!"))
				proxy.DelWaitStartCookie()
				return
			}
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("500 -No config change needed!"))
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

// handlerMuxServerNeedRollingReprov handles the HTTP request to check if a cluster needs a rolling reprovision.
// @Summary Check if a cluster needs a rolling reprovision
// @Description Checks if a specified cluster needs a rolling reprovision.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "200 -Need rolling reprov!"
// @Failure 500 {string} string "503 -No rolling reprov needed!" or "500 -No cluster"
// @Router /api/clusters/{clusterName}/need-rolling-reprov [get]
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

// handlerMuxServerNeedRollingRestart handles the HTTP request to check if a cluster needs a rolling restart.
// @Summary Check if a cluster needs a rolling restart
// @Description Checks if a specified cluster needs a rolling restart.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "200 -Need rolling restart!"
// @Failure 500 {string} string "503 -No rolling restart needed!" or "500 -No cluster"
// @Router /api/clusters/{clusterName}/need-rolling-restart [get]
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

// handlerMuxServersPortIsMasterStatus handles the HTTP request to check if a server port is a master within a cluster.
// @Summary Check if a server port is a master
// @Description Checks if a specified server port within a cluster is a master.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Valid Master!"
// @Failure 500 {string} string "No cluster"
// @Failure 503 {string} string "503 -Not a Valid Master!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master [get]
func (repman *ReplicationManager) handlerMuxServersPortIsMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersIsSlaveStatus handles the HTTP request to check if a server is a slave within a cluster.
// @Summary Check if a server is a slave
// @Description Checks if a specified server within a cluster is a slave.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "200 -Valid Slave!"
// @Failure 500 {string} string "No cluster"
// @Failure 503 {string} string "503 -Not a Valid Slave!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/is-slave [get]
func (repman *ReplicationManager) handlerMuxServersIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersPortIsSlaveStatus handles the HTTP request to check if a server port is a slave within a cluster.
// @Summary Check if a server port is a slave
// @Description Checks if a specified server port within a cluster is a slave.
// @Tags Database
// @Produce text/plain
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "200 -Valid Slave!"
// @Failure 500 {string} string "No cluster"
// @Failure 503 {string} string "503 -Not a Valid Slave!"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave [get]
func (repman *ReplicationManager) handlerMuxServersPortIsSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		/*		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersPortBackup handles the HTTP request to perform a physical backup on a specific server port within a cluster.
// @Summary Perform a physical backup on a server port
// @Description Initiates a physical backup on a specified server port within a cluster.
// @Tags DatabaseBackup
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {string} string "Backup initiated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup [get]
func (repman *ReplicationManager) handlerMuxServersPortBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersPortConfig handles the HTTP request to get the configuration of a specific server port within a cluster.
// @Summary Get server port configuration
// @Description Retrieves the configuration of a specified server port within a cluster.
// @Tags Database
// @Produce application/octet-stream
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Success 200 {file} file "Configuration file"
// @Failure 403 {string} string "No valid ACL"
// @Failure 404 {string} string "File not found"
// @Failure 500 {string} string "No cluster" or "No server"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/config [get]
func (repman *ReplicationManager) handlerMuxServersPortConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if mycluster.Conf.APISecureConfig {
			if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

type DecodedData struct {
	Data string `json:"data"`
}

// handlerMuxServersWriteLog handles the HTTP request to write logs for a specific server within a cluster.
// @Summary Write logs for a server
// @Description Writes logs for a specified server within a cluster.
// @Tags DatabaseTasks
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param serverPort path string true "Server Port"
// @Param task path string true "Task"
// @Param data body DecodedData true "Log Data"
// @Success 200 {object} ApiResponse "Message logged"
// @Failure 400 {string} string "Bad request: Task is not registered" or "Decode reading body" or "Decode body"
// @Failure 500 {string} string "No cluster" or "No server" or "Error decrypting data"
// @Router /api/clusters/{clusterName}/servers/{serverName}/{serverPort}/write-log/{task} [post]
func (repman *ReplicationManager) handlerMuxServersWriteLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		var mod int
		switch vars["task"] {
		case "mariabackup", "xtrabackup", "reseedxtrabackup", "reseedmariabackup", "flashbackxtrabackup", "flashbackmariadbackup":
			mod = config.ConstLogModBackupStream
		case "error", "slowquery", "zfssnapback", "optimize", "reseedmysqldump", "flashbackmysqldump", "stop", "restart", "start":
			mod = config.ConstLogModTask
		default:
			http.Error(w, "Bad request: Task is not registered", http.StatusBadRequest)
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

		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node != nil {
			// Decrypt the encrypted data
			key := crypto.GetSHA256Hash(node.Pass)
			iv := crypto.GetMD5Hash(node.Pass)

			err := node.WriteJobLogs(mod, decodedData.Data, key, iv, vars["task"])
			if err != nil {
				http.Error(w, "Error decrypting data : "+err.Error(), http.StatusInternalServerError)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(ApiResponse{Data: "Message logged", Success: true})

		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}

// handlerMuxServerProcesslist handles the HTTP request to get the process list of a specific server within a cluster.
// @Summary Get process list of a server
// @Description Retrieves the process list of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Process list retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/processlist [get]
func (repman *ReplicationManager) handlerMuxServerProcesslist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerMetaDataLocks handles the HTTP request to get metadata locks of a specific server within a cluster.
// @Summary Get metadata locks of a server
// @Description Retrieves the metadata locks of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Metadata locks retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/meta-data-locks [get]
func (repman *ReplicationManager) handlerMuxServerMetaDataLocks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerQueryResponseTime handles the HTTP request to get query response time of a specific server within a cluster.
// @Summary Get query response time of a server
// @Description Retrieves the query response time of a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Query response time retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/query-response-time [get]
func (repman *ReplicationManager) handlerMuxServerQueryResponseTime(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerErrorLog handles the HTTP request to get the error log of a specific server within a cluster.
// @Summary Get error log of a server
// @Description Retrieves the error log of a specified server within a cluster.
// @Tags DatabaseLogs
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Error log retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/errorlog [get]
func (repman *ReplicationManager) handlerMuxServerErrorLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerSlowLog handles the HTTP request to get the slow log of a specific server within a cluster.
// @Summary Get slow log of a server
// @Description Retrieves the slow log of a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Slow log retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/slow-queries [get]
func (repman *ReplicationManager) handlerMuxServerSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerPFSStatements handles the HTTP request to get PFS statements of a specific server within a cluster.
// @Summary Get PFS statements of a server
// @Description Retrieves the PFS statements of a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "PFS statements retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/digest-statements-pfs [get]
func (repman *ReplicationManager) handlerMuxServerPFSStatements(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerPFSStatementsSlowLog handles the HTTP request to get PFS statements from the slow log of a specific server within a cluster.
// @Summary Get PFS statements from the slow log of a server
// @Description Retrieves the PFS statements from the slow log of a specified server within a cluster.
// @Tags DatabaseQueries
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "PFS statements from slow log retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/digest-statements-slow [get]
func (repman *ReplicationManager) handlerMuxServerPFSStatementsSlowLog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerVariables handles the HTTP request to get the variables of a specific server within a cluster.
// @Summary Get variables of a server
// @Description Retrieves the variables of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Variables retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/variables [get]
func (repman *ReplicationManager) handlerMuxServerVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerStatus handles the HTTP request to get the status of a specific server within a cluster.
// @Summary Get status of a server
// @Description Retrieves the status of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Status retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/status [get]
func (repman *ReplicationManager) handlerMuxServerStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerStatusDelta handles the HTTP request to get the status delta of a specific server within a cluster.
// @Summary Get status delta of a server
// @Description Retrieves the status delta of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Status delta retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/status-delta [get]
func (repman *ReplicationManager) handlerMuxServerStatusDelta(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerTables handles the HTTP request to get the tables of a specific server within a cluster.
// @Summary Get tables of a server
// @Description Retrieves the tables of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Tables retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/tables [get]
func (repman *ReplicationManager) handlerMuxServerTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerVTables handles the HTTP request to get the virtual tables of a specific server within a cluster.
// @Summary Get virtual tables of a server
// @Description Retrieves the virtual tables of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Virtual tables retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/vtables [get]
func (repman *ReplicationManager) handlerMuxServerVTables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxRunJobs handles the HTTP request to run jobs on a specific server within a cluster.
// @Summary Run jobs on a server
// @Description Runs jobs on a specified server within a cluster.
// @Tags DatabaseTasks
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Jobs run successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Error running job"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/run-jobs [get]
func (repman *ReplicationManager) handlerMuxRunJobs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.JobRunViaSSH()
			if err != nil {
				http.Error(w, "Error running job: "+err.Error(), 500)
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

// handlerMuxServerSchemas handles the HTTP request to get the schemas of a specific server within a cluster.
// @Summary Get schemas of a server
// @Description Retrieves the schemas of a specified server within a cluster.
// @Tags DatabaseSchema
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Schemas retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/schemas [get]
func (repman *ReplicationManager) handlerMuxServerSchemas(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerInnoDBStatus handles the HTTP request to get the InnoDB status of a specific server within a cluster.
// @Summary Get InnoDB status of a server
// @Description Retrieves the InnoDB status of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "InnoDB status retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/status-innodb [get]
func (repman *ReplicationManager) handlerMuxServerInnoDBStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerAllSlavesStatus handles the HTTP request to get the status of all slaves of a specific server within a cluster.
// @Summary Get status of all slaves of a server
// @Description Retrieves the status of all slaves of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Status of all slaves retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/all-slaves-status [get]
func (repman *ReplicationManager) handlerMuxServerAllSlavesStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServerMasterStatus handles the HTTP request to get the master status of a specific server within a cluster.
// @Summary Get master status of a server
// @Description Retrieves the master status of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {object} map[string]interface{} "Master status retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Encoding error"
// @Router /api/clusters/{clusterName}/servers/{serverName}/master-status [get]
func (repman *ReplicationManager) handlerMuxServerMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSkipReplicationEvent handles the HTTP request to skip a replication event on a specific server within a cluster.
// @Summary Skip a replication event on a server
// @Description Skips a replication event on a specified server within a cluster.
// @Tags DatabaseReplication
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Replication event skipped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/skip-replication-event [get]
func (repman *ReplicationManager) handlerMuxSkipReplicationEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxSetInnoDBMonitor handles the HTTP request to toggle InnoDB monitor on a specific server within a cluster.
// @Summary Toggle InnoDB monitor on a server
// @Description Toggles the InnoDB monitor on a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "InnoDB monitor toggled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/toogle-innodb-monitor [get]
func (repman *ReplicationManager) handlerMuxSetInnoDBMonitor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxGetDatabaseServiceConfig handles the HTTP request to get the database service configuration of a specific server within a cluster.
// @Summary Get database service configuration of a server
// @Description Retrieves the database service configuration of a specified server within a cluster.
// @Tags Database
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Success 200 {string} string "Database service configuration retrieved successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found"
// @Router /api/clusters/{clusterName}/servers/{serverName}/service-opensvc [get]
func (repman *ReplicationManager) handlerMuxGetDatabaseServiceConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
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

// handlerMuxServersTaskCancel handles the HTTP request to cancel a task on a specific server within a cluster.
// @Summary Cancel a task on a server
// @Description Cancels a task identified by its name on a specified server within a cluster.
// @Tags DatabaseTasks
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName path string true "Server Name"
// @Param task path string true "Task Name"
// @Success 200 {string} string "Task canceled successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found" or "Server Not Found" or "Error canceling task"
// @Router /api/clusters/{clusterName}/servers/{serverName}/actions/job-cancel/{task} [get]
func (repman *ReplicationManager) handlerMuxServersTaskCancel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			err := node.JobsCancelTasks(true, vars["task"])
			if err != nil {
				http.Error(w, fmt.Sprintf("Error canceling %s task: %s", vars["task"], err.Error()), 500)
			}
		} else {
			http.Error(w, "No server", 500)
		}
	} else {
		http.Error(w, "No cluster", 500)
	}
}
