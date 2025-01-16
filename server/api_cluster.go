// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/codegangsta/negroni"
	"github.com/iancoleman/strcase"
	log "github.com/sirupsen/logrus"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
)

func (repman *ReplicationManager) apiClusterUnprotectedHandler(router *mux.Router) {
	router.Handle("/api/clusters/{clusterName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/master-physical-backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterMasterPhysicalBackup)),
	))

}

func (repman *ReplicationManager) apiClusterProtectedHandler(router *mux.Router) {

	router.Handle("/api/clusters/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxCluster)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS ACTIONS
	router.Handle("/api/clusters/{clusterName}/settings", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSettings)),
	))

	router.Handle("/api/clusters/{clusterName}/tags", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterTags)),
	))

	router.Handle("/api/clusters/{clusterName}/jobs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterGetJobEntries)),
	))

	router.Handle("/api/clusters/{clusterName}/backups", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterBackups)),
	))

	router.Handle("/api/clusters/{clusterName}/certificates", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterCertificates)),
	))

	router.Handle("/api/clusters/{clusterName}/queryrules", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterQueryRules)),
	))
	router.Handle("/api/clusters/{clusterName}/top", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterTop)),
	))
	router.Handle("/api/clusters/{clusterName}/shardclusters", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterShardClusters)),
	))
	router.Handle("/api/clusters/{clusterName}/send-vault-token", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSendVaultToken)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchGlobalSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/settings/actions/set/{settingName}/{settingValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetGlobalSettings)),
	))
	router.Handle("/api/clusters/settings/actions/clear/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetGlobalSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/clear/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettings)),
	))
	router.Handle("/api/clusters/settings/actions/reload-clusters-plans", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReloadPlans)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set-cron/{settingName}/{settingValue:.*}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetCron)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/add-db-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/drop-db-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/add-proxy-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddProxyTag)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/drop-proxy-tag/{tagValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropProxyTag)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/reset-failover-control", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterResetFailoverControl)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/discover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettingsDiscover)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/apply-dynamic-config", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterApplyDynamicConfig)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/add/{clusterShardingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterShardingAdd)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/switchover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/failover", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxFailover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/certificates-rotate", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRotateKeys)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/certificates-reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterReloadCertificates)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/reset-sla", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxResetSla)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxBootstrapReplication)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/cleanup", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxBootstrapReplicationCleanup)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/provision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/unprovision", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/cancel-rolling-restart", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesCancelRollingRestart)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/cancel-rolling-reprov", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServicesCancelRollingReprov)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStopTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStartTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/sysbench", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSysbench)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/waitdatabases", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterWaitDatabases)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/dropserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDrop)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/dropserver/{host}/{port}/{type}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerDrop)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/rolling", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRolling)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/rotate-passwords", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerRotatePasswords)),
	))

	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaReshardTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table/{clusterList}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaReshardTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/move-table/{clusterShard}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaMoveTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/universal-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaUniversalTable)),
	))
	router.Handle("/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-table", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumTable)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/checksum-all-tables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchemaChecksumAllTable)),
	))

	router.Handle("/api/clusters/{clusterName}/schema", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSchema)),
	))

	router.Handle("/api/clusters/{clusterName}/graphite-filterlist", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterGraphiteFilterList)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/set-graphite-filterlist/{filterType}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSetGraphiteFilterList)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/reload-graphite-filterlist", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterReloadGraphiteFilterList)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reset-graphite-filterlist/{template}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterResetGraphiteFilterList)),
	))
	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/actions/add/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAdd)),
	))

	router.Handle("/api/clusters/actions/delete/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterDelete)),
	))

	router.Handle("/api/clusters/{clusterName}/topology/servers", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/servers/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/master", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxMaster)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaves)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlavesCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaveIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}/attr/{attrName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaveAttributeByIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/standalones", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetStandaloneServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/standalones/count", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetStandaloneServersCount)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/standalones/index/{index}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetStandaloneServerByIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/standalones/index/{index}/attr/{attrName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGetStandaloneAttributeByIndex)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/logs", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/proxies", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxProxies)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/alerts", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAlerts)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/crashes", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxCrashes)),
	))
	//PROTECTED ENDPOINTS FOR TESTS

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/all", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxTests)),
	))
	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxOneTest)),
	))

	// endpoint to fetch Cluster.DiffVariables
	router.Handle("/api/clusters/{clusterName}/diffvariables", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerDiffVariables)),
	))

	router.Handle("/api/clusters/{clusterName}/users/add", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAddClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/update", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxUpdateClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/drop", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxDropClusterUser)),
	))

	router.Handle("/api/clusters/{clusterName}/users/send-credentials", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSendCredentials)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/accept-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAcceptSubscription)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/refuse-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRejectSubscription)),
	))

	router.Handle("/api/clusters/{clusterName}/sales/end-subscription", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRemoveSponsor)),
	))

	router.Handle("/api/clusters/{clusterName}/subscribe", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterSubscribe)),
	))

	router.Handle("/api/clusters/{clusterName}/unsubscribe", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRejectSubscription)),
	))
}

// @Summary Retrieve servers for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/servers [get]
func (repman *ReplicationManager) handlerMuxServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	var err error

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		type ServersContainer struct {
			servers []map[string]interface{}
		}

		res := ServersContainer{
			servers: make([]map[string]interface{}, 0),
		}
		for _, srv := range mycluster.GetServers() {
			var cont map[string]interface{}
			data, _ := json.Marshal(srv)
			list, _ := json.Marshal(srv.BinaryLogFiles.ToNewMap())
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
			res.servers = append(res.servers, cont)
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(res.servers)
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

// @Summary Return number of servers for that specific named cluster
// @Description Return number of servers for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Number of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/servers/count [get]
func (repman *ReplicationManager) handlerMuxServersCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(len(mycluster.Servers))))
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Retrieve all standalone server for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} cluster.ServerMonitor "Standalone Server"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/standalones [get]
func (repman *ReplicationManager) handlerMuxGetStandaloneServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	var err error

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		type ServersContainer struct {
			servers []map[string]interface{}
		}

		res := ServersContainer{
			servers: make([]map[string]interface{}, 0),
		}
		for _, srv := range mycluster.GetStandaloneServers() {
			var cont map[string]interface{}
			data, _ := json.Marshal(srv)
			list, _ := json.Marshal(srv.BinaryLogFiles.ToNewMap())
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
			res.servers = append(res.servers, cont)
		}

		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(res.servers)
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

// @Summary Return number of servers for that specific named cluster
// @Description Return number of servers for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Number of servers"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/standalones/count [get]
func (repman *ReplicationManager) handlerMuxGetStandaloneServersCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		counter := 0
		for _, srv := range mycluster.Servers {
			if srv.IsStandAlone() {
				counter++
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(counter)))
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Retrieve first standalone server for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param index path string true "Index"
// @Success 200 {object} cluster.ServerMonitor "Standalone Server"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/standalones/index/{index} [get]
func (repman *ReplicationManager) handlerMuxGetStandaloneServerByIndex(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		index, err := strconv.Atoi(vars["index"])
		if err != nil {
			http.Error(w, "Invalid index", 500)
			return
		}

		srv, err := mycluster.GetStandaloneServerByIndex(index)
		if srv == nil {
			http.Error(w, err.Error(), 500)
			return
		}

		data, _ := json.Marshal(srv)
		list, _ := json.Marshal(srv.BinaryLogFiles.ToNewMap())
		data, err = jsonparser.Set(data, list, "binaryLogFiles")
		if err != nil {
			http.Error(w, "Encoding error: "+err.Error(), 500)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(data)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Retrieve first standalone server for a specific cluster
// @Description This endpoint retrieves the servers for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param index path string true "Index"
// @Param attrName path string true "Attribute Name with dot notation"
// @Success 200 {object} cluster.ServerMonitor "Standalone Server (partial based on attrName)"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/standalones/index/{index}/attr/{attrName} [get]
func (repman *ReplicationManager) handlerMuxGetStandaloneAttributeByIndex(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		index, err := strconv.Atoi(vars["index"])
		if err != nil {
			http.Error(w, "Invalid index", 500)
			return
		}

		srv, err := mycluster.GetStandaloneServerByIndex(index)
		if srv == nil {
			http.Error(w, err.Error(), 500)
			return
		}

		var value []byte
		var valtype jsonparser.ValueType
		if vars["attrName"] == "binaryLogFiles" {
			value, _ = json.Marshal(srv.BinaryLogFiles.ToNewMap())
		} else if strings.HasPrefix(vars["attrName"], "binaryLogFiles.") {
			data, _ := json.Marshal(srv.BinaryLogFiles.ToNewMap())
			value, valtype, _, _ = jsonparser.Get(data, strings.Split(vars["attrName"], ".")[1:]...)
		} else {
			data, _ := json.Marshal(srv)
			value, valtype, _, _ = jsonparser.Get(data, strings.Split(vars["attrName"], ".")...)
		}

		if valtype == jsonparser.NotExist {
			http.Error(w, "Attribute not found", 500)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(value)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "A list of slaves"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves [get]
func (repman *ReplicationManager) handlerMuxSlaves(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetSlaves())
		var srvs []*cluster.ServerMonitor

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		for i := range srvs {
			srvs[i].Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srvs)
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

// @Summary Return number of slaves for that specific named cluster
// @Description Return number of slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Number of slaves"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/count [get]
func (repman *ReplicationManager) handlerMuxSlavesCount(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(strconv.Itoa(len(mycluster.GetSlaves()))))
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param slaveIndex path string true "Slave Index (start from 0)"
// @Success 200 {object} cluster.ServerMonitor "Slave Data"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/index/{slaveIndex} [get]
func (repman *ReplicationManager) handlerMuxSlaveIndex(w http.ResponseWriter, r *http.Request) {
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

		index, err := strconv.Atoi(vars["slaveIndex"])
		if err != nil {
			http.Error(w, "Invalid index", 500)
			return
		}

		slave := mycluster.GetSlaveByIndex(index)
		if slave == nil {
			http.Error(w, "Slave not found", 500)
			return
		}

		data, _ := json.Marshal(slave)
		var srv cluster.ServerMonitor

		err = json.Unmarshal(data, &srv)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}

		srv.Pass = "XXXXXXXX"
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srv)
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

// @Summary Shows the slaves for that specific named cluster
// @Description Shows the slaves for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param slaveIndex path string true "Slave Index (start from 0)"
// @Param attrName path string true "Attribute Name (using json path notation split by dot)"
// @Success 200 {object} cluster.ServerMonitor "Slave Attribute (partial based on attrName)"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/slaves/index/{slaveIndex}/attr/{attrName} [get]
func (repman *ReplicationManager) handlerMuxSlaveAttributeByIndex(w http.ResponseWriter, r *http.Request) {
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

		index, err := strconv.Atoi(vars["slaveIndex"])
		if err != nil {
			http.Error(w, "Invalid index", 500)
			return
		}

		slave := mycluster.GetSlaveByIndex(index)
		if slave == nil {
			http.Error(w, "Slave not found", 500)
			return
		}

		var data, value []byte
		var valtype jsonparser.ValueType
		// get the value from the json path
		// if the attribute is binaryLogFiles, we need to convert the map to json
		// if the attribute is binaryLogFiles.*, we need to convert the map to json and get the value from the json path
		// otherwise, we just get the value from the json path
		if vars["attrName"] == "binaryLogFiles" {
			value, _ = json.Marshal(slave.BinaryLogFiles.ToNewMap())
		} else if strings.HasPrefix(vars["attrName"], "binaryLogFiles.") {
			data, _ = json.Marshal(slave.BinaryLogFiles.ToNewMap())
			value, valtype, _, _ = jsonparser.Get(data, strings.Split(vars["attrName"], ".")[1:]...)
		} else {
			data, _ = json.Marshal(slave)
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

// @Summary Shows the proxies for that specific named cluster
// @Description Shows the proxies for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "A list of proxies"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/proxies [get]
func (repman *ReplicationManager) handlerMuxProxies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetProxies())
		var prxs []*cluster.Proxy
		err := json.Unmarshal(data, &prxs)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(prxs)
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

// @Summary Shows the alerts for that specific named cluster
// @Description Shows the alerts for that specific named cluster
// @Tags ClusterTopology
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.Alerts "A list of alerts"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/alerts [get]
func (repman *ReplicationManager) handlerMuxAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	a := new(cluster.Alerts)
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		a.Errors = mycluster.GetStateMachine().GetOpenErrors()
		a.Warnings = mycluster.GetStateMachine().GetOpenWarnings()
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(a)
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

// @Summary Rotate keys for a specific cluster
// @Description Rotate the keys for the specified cluster
// @Tags ClusterCertificates
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Keys rotated successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/certificates-rotate [post]
func (repman *ReplicationManager) handlerMuxRotateKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.KeyRotation()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// @Summary Reset SLA for a specific cluster
// @Description Reset the SLA for the specified cluster
// @Tags ClusterActions
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "SLA reset successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/reset-sla [post]
func (repman *ReplicationManager) handlerMuxResetSla(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.SetEmptySla()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxFailover handles the failover process for a given cluster.
// @Summary Handles the failover process for a given cluster.
// @Description This endpoint triggers a master failover for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered failover"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/failover [post]
func (repman *ReplicationManager) handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.MasterFailover(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterShardingAdd handles the addition of a sharding cluster to an existing cluster.
// @Summary Add a sharding cluster to an existing cluster
// @Description This endpoint adds a sharding cluster to an existing cluster and triggers a rolling restart.
// @Tags ClusterTopology
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param clusterShardingName path string true "Cluster Sharding Name"
// @Success 200 {string} string "Sharding cluster added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/add/{clusterShardingName} [post]
func (repman *ReplicationManager) handlerMuxClusterShardingAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		repman.AddCluster(vars["clusterShardingName"], vars["clusterName"])
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxRolling handles the rolling restart process for a given cluster.
// @Summary Handles the rolling restart process for a given cluster.
// @Description This endpoint triggers a rolling restart for the specified cluster.
// @Tags ClusterMaintenance
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered rolling restart"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/rolling [post]
func (repman *ReplicationManager) handlerMuxRolling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxStartTraffic handles the start traffic process for a given cluster.
// @Summary Start traffic for a specific cluster
// @Description This endpoint starts traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully started traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/start-traffic [post]
func (repman *ReplicationManager) handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.SetTraffic(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxStopTraffic handles the stop traffic process for a given cluster.
// @Summary Stop traffic for a specific cluster
// @Description This endpoint stops traffic for the specified cluster.
// @Tags ClusterTraffics
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully stopped traffic"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/stop-traffic [post]
func (repman *ReplicationManager) handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.SetTraffic(false)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxBootstrapReplicationCleanup handles the cleanup process for replication bootstrap.
// @Summary Cleanup replication bootstrap for a specific cluster
// @Description This endpoint triggers the cleanup process for replication bootstrap for the specified cluster.
// @Tags ClusterReplication
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cleaned up replication bootstrap"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/replication/cleanup [post]
func (repman *ReplicationManager) handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.BootstrapReplicationCleanup()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error Cleanup Replication: %s", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxBootstrapReplication handles the bootstrap replication process for a given cluster.
// @Summary Bootstrap replication for a specific cluster
// @Description This endpoint triggers the bootstrap replication process for the specified cluster.
// @Tags ClusterReplication
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param topology path string true "Topology"
// @Success 200 {string} string "Successfully bootstrapped replication"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/replication/bootstrap/{topology} [post]
func (repman *ReplicationManager) handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		mycluster.BootstrapTopology(vars["topology"])
		err := mycluster.BootstrapReplication(true)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error bootstraping replication %s", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxServicesBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.ProvisionServices()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error Bootstrap Micro Services: %s", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxServicesProvision handles the provisioning of services for a given cluster.
// @Summary Provision services for a specific cluster
// @Description This endpoint provisions services for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully provisioned services"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/services/actions/provision [post]
func (repman *ReplicationManager) handlerMuxServicesProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.Bootstrap()
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error Bootstrap Micro Services + replication: %s", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxServicesUnprovision handles the unprovisioning of services for a given cluster.
// @Summary Unprovision services for a specific cluster
// @Description This endpoint unprovisions services for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully unprovisioned services"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/services/actions/unprovision [post]
func (repman *ReplicationManager) handlerMuxServicesUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.Unprovision()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxServicesCancelRollingRestart handles the cancellation of rolling restart for a given cluster.
// @Summary Cancel rolling restart for a specific cluster
// @Description This endpoint cancels the rolling restart for the specified cluster.
// @Tags ClusterMaintenance
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cancelled rolling restart"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/cancel-rolling-restart [post]
func (repman *ReplicationManager) handlerMuxServicesCancelRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.CancelRollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxServicesCancelRollingReprov handles the cancellation of rolling reprovision for a given cluster.
// @Summary Cancel rolling reprovision for a specific cluster
// @Description This endpoint cancels the rolling reprovision for the specified cluster.
// @Tags ClusterProvision
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully cancelled rolling reprovision"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/cancel-rolling-reprov [post]
func (repman *ReplicationManager) handlerMuxServicesCancelRollingReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.CancelRollingReprov()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxSetSettingsDiscover handles the discovery of settings for a given cluster.
// @Summary Discover settings for a specific cluster
// @Description This endpoint triggers the discovery of settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully discovered settings"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/discover [post]
func (repman *ReplicationManager) handlerMuxSetSettingsDiscover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.ConfigDiscovery()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterResetFailoverControl handles the reset of failover control for a given cluster.
// @Summary Reset failover control for a specific cluster
// @Description This endpoint resets the failover control for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reset failover control"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/reset-failover-control [post]
func (repman *ReplicationManager) handlerMuxClusterResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.ResetFailoverCtr()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxSwitchover handles the switchover process for a given cluster.
// @Summary Handles the switchover process for a given cluster.
// @Description This endpoint triggers a master switchover for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param prefmaster formData string false "Preferred Master"
// @Success 200 {string} string "Successfully triggered switchover"
// @Failure 400 {string} string "Master failed"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/switchover [post]
func (repman *ReplicationManager) handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive switchover request")
		savedPrefMaster := mycluster.GetPreferedMasterList()
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if mycluster.IsMasterFailed() {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Master failed, cannot initiate switchover")
			http.Error(w, "Master failed", http.StatusBadRequest)
			return
		}
		r.ParseForm() // Parses the request body
		newPrefMaster := r.Form.Get("prefmaster")
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API force for prefered master: %s", newPrefMaster)
		if mycluster.IsInHostList(newPrefMaster) {
			mycluster.SetPrefMaster(newPrefMaster)
		} else {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Prefered master: not found in database servers %s", newPrefMaster)
		}
		mycluster.MasterFailover(false)
		mycluster.SetPrefMaster(savedPrefMaster)

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxMaster handles the HTTP request to retrieve the master of a specified cluster.
// @Summary Retrieve master of a cluster
// @Description This endpoint retrieves the master of a specified cluster and returns it in JSON format.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.ServerMonitor "Master server"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/master [get]
func (repman *ReplicationManager) handlerMuxMaster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		m := mycluster.GetMaster()
		var srvs *cluster.ServerMonitor
		if m != nil {
			data, _ := json.Marshal(m)

			err := json.Unmarshal(data, &srvs)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error decoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}
			srvs.Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(srvs)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterCertificates handles the retrieval of client certificates for a given cluster.
// @Summary Retrieve client certificates for a specific cluster
// @Description This endpoint retrieves the client certificates for the specified cluster.
// @Tags ClusterCertificates
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of client certificates"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/certificates [get]
func (repman *ReplicationManager) handlerMuxClusterCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		certs, err := mycluster.GetClientCertificates()
		if err != nil {
			if err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		err = e.Encode(certs)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterTags handles the retrieval of tags for a given cluster.
// @Summary Retrieve tags for a specific cluster
// @Description This endpoint retrieves the tags for the specified cluster.
// @Tags ClusterTags
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of tags"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/tags [get]
func (repman *ReplicationManager) handlerMuxClusterTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.Configurator.GetDBModuleTags())
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterBackups handles the retrieval of backups for a given cluster.
// @Summary Retrieve backups for a specific cluster
// @Description This endpoint retrieves the backups for the specified cluster.
// @Tags ClusterBackups
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of backups"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/backups [get]
func (repman *ReplicationManager) handlerMuxClusterBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetBackups())
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterShardClusters handles the retrieval of shard clusters for a given cluster.
// @Summary Retrieve shard clusters for a specific cluster
// @Description This endpoint retrieves the shard clusters for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of shard clusters"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/shardclusters [get]
func (repman *ReplicationManager) handlerMuxClusterShardClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.ShardProxyGetShardClusters())
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterQueryRules handles the retrieval of query rules for a given cluster.
// @Summary Retrieve query rules for a specific cluster
// @Description This endpoint retrieves the query rules for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of query rules"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/queryrules [get]
func (repman *ReplicationManager) handlerMuxClusterQueryRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetQueryRules())
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterTop handles the retrieval of top metrics for a given cluster.
// @Summary Retrieve top metrics for a specific cluster
// @Description This endpoint retrieves the top metrics for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param serverName query string false "Server Name"
// @Success 200 {object} map[string]interface{} "Top metrics"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/top [get]
func (repman *ReplicationManager) handlerMuxClusterTop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		svname := r.URL.Query().Get("serverName")
		if svname != "" {
			node := mycluster.GetServerFromName(svname)
			if node == nil {
				http.Error(w, "Not a Valid Server!", 500)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetTopMetrics(svname))
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxSwitchSettings handles the switching of settings for a given cluster.
// @Summary Switch settings for a specific cluster
// @Description This endpoint switches the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Success 200 {string} string "Successfully switched setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/switch/{settingName} [post]
func (repman *ReplicationManager) handlerMuxSwitchSettings(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	cName := vars["clusterName"]
	setting := vars["settingName"]

	// Should be handled with global settings
	serverScope := config.IsScope(setting, "server")
	if serverScope {
		r.URL.Path = strings.Replace(r.URL.Path, "/api/clusters/"+vars["clusterName"], "/api/clusters/", 1)
		repman.handlerMuxSwitchGlobalSettings(w, r)
		return
	}

	mycluster := repman.getClusterByName(cName)
	if mycluster != nil {
		valid, _ := repman.IsValidClusterACL(r, mycluster)
		if valid {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", setting)
			//Set server scope
			err := repman.switchClusterSettings(mycluster, setting)
			if err != nil {
				http.Error(w, "Setting Not Found", 501)
				return
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for %s in cluster %s", setting, vars["clusterName"]), 403)
			return
		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}

}

// handlerMuxSwitchGlobalSettings handles the switching of global settings for the server.
// @Summary Switch global settings for the server
// @Description This endpoint switches the global settings for the server.
// @Tags GlobalSetting
// @Accept json
// @Produce json
// @Param settingName path string true "Setting Name"
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Success 200 {string} string "Successfully switched setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/switch/{settingName} [post]
func (repman *ReplicationManager) handlerMuxSwitchGlobalSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	setting := vars["settingName"]
	serverScope := config.IsScope(setting, "server")
	if !serverScope {
		http.Error(w, "setting is not in global scope", 501)
		return
	}

	var mycluster *cluster.Cluster
	if cName, ok := vars["clusterName"]; ok {
		mycluster = repman.getClusterByName(cName)
	} else {
		for _, v := range repman.Clusters {
			if v != nil {
				mycluster = v
				break
			}
		}
	}

	if mycluster != nil {
		valid, user := repman.IsValidClusterACL(r, mycluster)
		if valid {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch global setting %s", setting)
			err := repman.switchServerSetting(user, r.URL.Path, setting)
			if err != nil {
				http.Error(w, fmt.Sprintf("Failed to set value for %s: %s", setting, err.Error()), 400)
				return
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s", setting), 403)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) switchClusterSettings(mycluster *cluster.Cluster, setting string) error {
	switch setting {
	case "verbose":
		mycluster.SwitchVerbosity()
	case "failover-mode":
		mycluster.SwitchInteractive()
	case "failover-readonly-state":
		mycluster.SwitchReadOnly()
	case "failover-restart-unsafe":
		mycluster.SwitchFailoverRestartUnsafe()
	case "failover-at-sync":
		mycluster.SwitchFailSync()
	case "force-slave-no-gtid-mode":
		mycluster.SwitchForceSlaveNoGtid()
	case "switchover-lower-release":
		mycluster.SwitchFailoverLowerRelease()
	case "failover-event-status":
		mycluster.SwitchFailoverEventStatus()
	case "failover-event-scheduler":
		mycluster.SwitchFailoverEventScheduler()
	case "delay-stat-capture":
		mycluster.SwitchDelayStatCapture()
	case "print-delay-stat":
		mycluster.SwitchPrintDelayStat()
	case "print-delay-stat-history":
		mycluster.SwitchPrintDelayStatHistory()
	case "failover-check-delay-stat":
		mycluster.SwitchFailoverCheckDelayStat()
	case "autorejoin":
		mycluster.SwitchRejoin()
	case "autoseed":
		mycluster.SwitchAutoseed()
	case "autorejoin-backup-binlog":
		mycluster.SwitchRejoinBackupBinlog()
	case "autorejoin-flashback":
		mycluster.SwitchRejoinFlashback()
	case "autorejoin-flashback-on-sync":
		mycluster.SwitchRejoinSemisync()
	case "autorejoin-flashback-on-unsync": //?????
	case "autorejoin-slave-positional-heartbeat":
		mycluster.SwitchRejoinPseudoGTID()
	case "autorejoin-zfs-flashback":
		mycluster.SwitchRejoinZFSFlashback()
	case "autorejoin-mysqldump":
		mycluster.SwitchRejoinDump()
	case "autorejoin-logical-backup":
		mycluster.SwitchRejoinLogicalBackup()
	case "autorejoin-physical-backup":
		mycluster.SwitchRejoinPhysicalBackup()
	case "autorejoin-force-restore":
		mycluster.SwitchRejoinForceRestore()
	case "switchover-at-sync":
		mycluster.SwitchSwitchoverSync()
	case "check-replication-filters":
		mycluster.SwitchCheckReplicationFilters()
	case "check-replication-state":
		mycluster.SwitchRplChecks()
	case "scheduler-db-servers-logical-backup":
		mycluster.SwitchSchedulerBackupLogical()
	case "scheduler-db-servers-physical-backup":
		mycluster.SwitchSchedulerBackupPhysical()
	case "scheduler-db-servers-logs":
		mycluster.SwitchSchedulerDatabaseLogs()
	case "scheduler-jobs-ssh":
		mycluster.SwitchSchedulerDbJobsSsh()
	case "scheduler-db-servers-logs-table-rotate":
		mycluster.SwitchSchedulerDatabaseLogsTableRotate()
	case "scheduler-rolling-restart":
		mycluster.SwitchSchedulerRollingRestart()
	case "scheduler-rolling-reprov":
		mycluster.SwitchSchedulerRollingReprov()
	case "scheduler-db-servers-optimize":
		mycluster.SwitchSchedulerDatabaseOptimize()
	case "scheduler-db-servers-analyze":
		mycluster.SwitchSchedulerDatabaseAnalyze()
	case "scheduler-alert-disable":
		mycluster.SwitchSchedulerAlertDisable()
	case "graphite-metrics":
		mycluster.SwitchGraphiteMetrics()
	case "graphite-embedded":
		mycluster.SwitchGraphiteEmbedded()
	case "graphite-whitelist":
		mycluster.SwitchGraphiteMetrics()
	case "graphite-blacklist":
		mycluster.SwitchGraphiteBlacklist()
	case "shardproxy-copy-grants":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-copy-grants":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-bootstrap-users":
		mycluster.SwitchProxysqlCopyGrants()
	case "proxysql-bootstrap-variables":
		mycluster.SwitchProxysqlBootstrapVariables()
	case "proxysql-bootstrap-hostgroups":
		mycluster.SwitchProxysqlBootstrapHostgroups()
	case "proxysql-bootstrap-servers":
		mycluster.SwitchProxysqlBootstrapServers()
	case "proxysql-bootstrap-query-rules":
		mycluster.SwitchProxysqlBootstrapQueryRules()
	case "proxysql-bootstrap":
		mycluster.SwitchProxysqlBootstrap()
	case "proxysql":
		mycluster.SwitchProxySQL()
	case "proxy-servers-read-on-master":
		mycluster.SwitchProxyServersReadOnMaster()
	case "proxy-servers-read-on-master-no-slave":
		mycluster.SwitchProxyServersReadOnMasterNoSlave()
	case "proxy-servers-backend-compression":
		mycluster.SwitchProxyServersBackendCompression()
	case "database-heartbeat":
		mycluster.SwitchTraffic()
	case "test":
		mycluster.SwitchTestMode()
	case "prov-net-cni":
		mycluster.SwitchProvNetCNI()
	case "prov-db-apply-dynamic-config":
		mycluster.SwitchDBApplyDynamicConfig()
	case "prov-docker-daemon-private":
		mycluster.SwitchProvDockerDaemonPrivate()
	case "backup-restic-aws":
		mycluster.SwitchBackupResticAws()
	case "backup-restic":
		mycluster.SwitchBackupRestic()
	case "backup-binlogs":
		mycluster.SwitchBackupBinlogs()
	case "compress-backups":
		mycluster.SwitchCompressBackups()
	case "monitoring-pause":
		mycluster.SwitchMonitoringPause()
	case "monitoring-save-config":
		mycluster.SwitchMonitoringSaveConfig()
	case "monitoring-queries":
		mycluster.SwitchMonitoringQueries()
	case "monitoring-scheduler":
		mycluster.SwitchMonitoringScheduler()
	case "monitoring-schema-change":
		mycluster.SwitchMonitoringSchemaChange()
	case "monitoring-capture":
		mycluster.SwitchMonitoringCapture()
	case "monitoring-innodb-status":
		mycluster.SwitchMonitoringInnoDBStatus()
	case "monitoring-variable-diff":
		mycluster.SwitchMonitoringVariableDiff()
	case "monitoring-processlist":
		mycluster.SwitchMonitoringProcesslist()
	case "force-slave-readonly":
		mycluster.SwitchForceSlaveReadOnly()
	case "force-binlog-row":
		mycluster.SwitchForceBinlogRow()
	case "force-slave-semisync":
		mycluster.SwitchForceSlaveSemisync()
	case "force-slave-Heartbeat":
		mycluster.SwitchForceSlaveHeartbeat()
	case "force-slave-gtid":
		mycluster.SwitchForceSlaveGtid()
	case "force-slave-gtid-mode-strict":
		mycluster.SwitchForceSlaveGtidStrict()
	case "force-slave-idempotent":
		mycluster.SwitchForceSlaveModeIdempotent()
	case "force-slave-strict":
		mycluster.SwitchForceSlaveModeStrict()
	case "force-slave-serialized":
		mycluster.SwitchForceSlaveParallelModeSerialized()
	case "force-slave-minimal":
		mycluster.SwitchForceSlaveParallelModeMinimal()
	case "force-slave-conservative":
		mycluster.SwitchForceSlaveParallelModeConservative()
	case "force-slave-optimistic":
		mycluster.SwitchForceSlaveParallelModeOptimistic()
	case "force-slave-aggressive":
		mycluster.SwitchForceSlaveParallelModeAggressive()
	case "force-binlog-compress":
		mycluster.SwitchForceBinlogCompress()
	case "force-binlog-annotate":
		mycluster.SwitchForceBinlogAnnotate()
	case "force-binlog-slow-queries":
		mycluster.SwitchForceBinlogSlowqueries()
	case "log-sql-in-monitoring":
		mycluster.SwitchLogSQLInMonitoring()
	case "log-writer-election":
		mycluster.SwitchLogWriterElection()
	case "log-sst":
		mycluster.SwitchLogSST()
	case "log-heartbeat":
		mycluster.SwitchLogHeartbeat()
	case "log-config-load":
		mycluster.SwitchLogConfigLoad()
	case "log-git":
		mycluster.SwitchLogGit()
	case "log-backup-stream":
		mycluster.SwitchLogBackupStream()
	case "log-orchestrator":
		mycluster.SwitchLogOrchestrator()
	case "log-vault":
		mycluster.SwitchLogVault()
	case "log-topology":
		mycluster.SwitchLogTopology()
	case "log-proxy":
		mycluster.SwitchLogProxy()
	case "proxysql-debug":
		mycluster.SwitchProxysqlDebug()
	case "haproxy-debug":
		mycluster.SwitchHaproxyDebug()
	case "proxyjanitor-debug":
		mycluster.SwitchProxyJanitorDebug()
	case "maxscale-debug":
		mycluster.SwitchMxsDebug()
	case "force-binlog-purge":
		mycluster.SwitchForceBinlogPurge()
	case "force-binlog-purge-on-restore":
		mycluster.SwitchForceBinlogPurgeOnRestore()
	case "force-binlog-purge-replicas":
		mycluster.SwitchForceBinlogPurgeReplicas()
	case "multi-master-concurrent-write":
		mycluster.SwitchMultiMasterConcurrentWrite()
	case "multi-master-ring-unsafe":
		mycluster.SwitchMultiMasterRingUnsafe()
	case "dynamic-topology":
		mycluster.SwitchDynamicTopology()
	case "replication-no-relay":
		mycluster.SwitchReplicationNoRelay()
	case "prov-db-force-write-config":
		mycluster.SwitchForceWriteConfig()
	case "backup-keep-until-valid":
		mycluster.SwitchBackupKeepUntilValid()
	case "mail-smtp-tls-skip-verify":
		mycluster.Conf.SwitchMailSmtpTlsSkipVerify()
	case "cloud18-shared":
		mycluster.Conf.SwitchCloud18Shared()
	case "cloud18-open-dbops":
		mycluster.SwitchCloud18OpenDbops()
	case "cloud18-subscribed-dbops":
		mycluster.SwitchCloud18SubscribedDbops()
	case "cloud18-open-sysops":
		mycluster.SwitchCloud18OpenSysops()
	default:
		return errors.New("Setting not found")
	}
	mycluster.Save()
	return nil
}

// handlerMuxSetSettings handles the setting of settings for a given cluster.
// @Summary Set settings for a specific cluster
// @Description This endpoint sets the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	cName := vars["clusterName"]
	setting := vars["settingName"]
	value := ""
	if settingValue, ok := vars["settingValue"]; ok {
		value = settingValue
	}

	// Should be handled with global settings
	serverScope := config.IsScope(setting, "server")
	if serverScope {
		repman.handlerMuxSetGlobalSettings(w, r)
		return
	}

	mycluster := repman.getClusterByName(cName)
	if mycluster != nil {
		valid, delegator := repman.IsValidClusterACL(r, mycluster)
		if valid {
			err := repman.setClusterSetting(mycluster, setting, value)
			if err != nil {
				errCode := 500
				if err.Error() == "Setting not found" {
					errCode = 501
				}

				http.Error(w, "Failed to set cluster setting: "+err.Error(), errCode)
				return
			}

			if setting == "cloud18-dba-user-credentials" {
				err = repman.SendDBACredentialsMail(mycluster, "dbops", delegator)
				if err != nil {
					http.Error(w, "Error sending email :"+err.Error(), 500)
					return
				}
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for %s in cluster %s", setting, vars["clusterName"]), 403)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxSetGlobalSettings handles the setting of global settings for the server.
// @Summary Set global settings for the server
// @Description This endpoint sets the global settings for the server.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param settingName path string true "Setting Name"
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set setting"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/set/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetGlobalSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	setting := vars["settingName"]
	serverScope := config.IsScope(setting, "server")
	if !serverScope {
		http.Error(w, "Setting Not Found", 501)
		return
	}
	value := ""
	if settingValue, ok := vars["settingValue"]; ok {
		value = settingValue
	}

	var mycluster *cluster.Cluster
	// path := r.URL.Path
	if cName, ok := vars["clusterName"]; ok {
		mycluster = repman.getClusterByName(cName)
		r.URL.Path = strings.Replace(r.URL.Path, "/api/clusters/"+vars["clusterName"], "/api/clusters", 1)
	} else {
		for _, v := range repman.Clusters {
			if v != nil {
				mycluster = v
				break
			}
		}
	}

	if mycluster != nil {
		valid, user := repman.IsValidClusterACL(r, mycluster)
		if valid {
			// || (user != "" && mycluster.IsURLPassACL(user, path, false)) {
			//Set server scope
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Option '%s' is a shared values between clusters", setting)
			err := repman.setServerSetting(user, r.URL.Path, setting, value)
			if err != nil {
				http.Error(w, err.Error(), 501)
				return
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s. path: %s", setting, r.URL.Path), 403)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxSetCron handles the setting of cron jobs for a given cluster.
// @Summary Set cron jobs for a specific cluster
// @Description This endpoint sets the cron jobs for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param settingName path string true "Setting Name"
// @Param settingValue path string true "Setting Value"
// @Success 200 {string} string "Successfully set cron job"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set-cron/{settingName}/{settingValue} [post]
func (repman *ReplicationManager) handlerMuxSetCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		setting := vars["settingName"]
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		cronValue, err := url.QueryUnescape(vars["settingValue"])
		if err != nil {
			http.Error(w, "Bad cron pattern", http.StatusBadRequest)
		}
		repman.setClusterSetting(mycluster, setting, cronValue)
		return
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) setClusterSetting(mycluster *cluster.Cluster, name string, value string) error {
	var err error
	//not immutable
	if !mycluster.Conf.IsVariableImmutable(name) {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive set setting %s", name)
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive set setting %s", name)
	}

	switch name {
	case "replication-credential":
		mycluster.SetReplicationCredential(value)
	case "failover-max-slave-delay":
		val, _ := strconv.ParseInt(value, 10, 64)
		mycluster.SetRplMaxDelay(val)
	case "switchover-wait-route-change":
		mycluster.SetSwitchoverWaitRouteChange(value)
	case "failover-limit":
		val, _ := strconv.Atoi(value)
		mycluster.SetFailLimit(val)
	case "backup-keep-hourly":
		mycluster.SetBackupKeepHourly(value)
	case "backup-keep-daily":
		mycluster.SetBackupKeepDaily(value)
	case "backup-keep-monthly":
		mycluster.SetBackupKeepMonthly(value)
	case "backup-keep-weekly":
		mycluster.SetBackupKeepWeekly(value)
	case "backup-keep-yearly":
		mycluster.SetBackupKeepYearly(value)
	case "backup-logical-type":
		mycluster.SetBackupLogicalType(value)
	case "backup-physical-type":
		mycluster.SetBackupPhysicalType(value)
	case "backup-binlog-type":
		mycluster.SetBackupBinlogType(value)
	case "backup-binlog-script":
		mycluster.SetBackupBinlogScript(value)
	case "binlog-parse-mode":
		mycluster.SetBinlogParseMode(value)
	case "db-servers-hosts":
		mycluster.SetDbServerHosts(value)
	case "db-servers-credential":
		mycluster.Conf.User = value
		mycluster.SetClusterMonitorCredentialsFromConfig()
		mycluster.ReloadConfig(mycluster.Conf)
		//mycluster.SetDbServersMonitoringCredential(value)
	case "prov-service-plan":
		mycluster.SetServicePlan(value)
	case "prov-net-cni-cluster":
		mycluster.SetProvNetCniCluster(value)
	case "prov-orchestrator-cluster":
		mycluster.SetProvOrchestratorCluster(value)
	case "prov-db-disk-size":
		mycluster.SetDBDiskSize(value)
	case "prov-db-cpu-cores":
		mycluster.SetDBCores(value)
	case "prov-db-memory":
		mycluster.SetDBMemorySize(value)
	case "prov-db-disk-iops":
		mycluster.SetDBDiskIOPS(value)
	case "prov-db-max-connections":
		mycluster.SetDBMaxConnections(value)
	case "prov-db-expire-log-days":
		mycluster.SetDBExpireLogDays(value)
	case "prov-db-agents":
		mycluster.SetProvDbAgents(value)
	case "prov-proxy-agents":
		mycluster.SetProvProxyAgents(value)
	case "prov-orchestrator":
		mycluster.SetProvOrchestrator(value)
	case "prov-sphinx-img":
		mycluster.SetProvSphinxImage(value)
	case "prov-db-image":
		mycluster.SetProvDBImage(value)
	case "prov-db-disk-type":
		mycluster.SetProvDbDiskType(value)
	case "prov-db-disk-fs":
		mycluster.SetProvDbDiskFS(value)
	case "prov-db-disk-pool":
		mycluster.SetProvDbDiskPool(value)
	case "prov-db-disk-device":
		mycluster.SetProvDbDiskDevice(value)
	case "prov-db-service-type":
		mycluster.SetProvDbServiceType(value)
	case "proxysql-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxySqlproxy)
	case "proxy-servers-backend-max-connections":
		mycluster.SetProxyServersBackendMaxConnections(value)
	case "proxy-servers-backend-max-replication-lag":
		mycluster.SetProxyServersBackendMaxReplicationLag(value)
	case "maxscale-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxyMaxscale)
	case "shardproxy-servers-credential":
		mycluster.SetProxyServersCredential(value, config.ConstProxySpider)
	case "prov-proxy-disk-size":
		mycluster.SetProxyDiskSize(value)
	case "prov-proxy-cpu-cores":
		mycluster.SetProxyCores(value)
	case "prov-proxy-memory":
		mycluster.SetProxyMemorySize(value)
	case "prov-proxy-docker-proxysql-img":
		mycluster.SetProvProxySQLImage(value)
	case "prov-proxy-docker-maxscale-img":
		mycluster.SetProvMaxscaleImage(value)
	case "prov-proxy-docker-haproxy-img":
		mycluster.SetProvHaproxyImage(value)
	case "prov-proxy-docker-shardproxy-img":
		mycluster.SetProvShardproxyImage(value)
	case "prov-proxy-disk-type":
		mycluster.SetProvProxyDiskType(value)
	case "prov-proxy-disk-fs":
		mycluster.SetProvProxyDiskFS(value)
	case "prov-proxy-disk-pool":
		mycluster.SetProvProxyDiskPool(value)
	case "prov-proxy-disk-device":
		mycluster.SetProvProxyDiskDevice(value)
	case "prov-proxy-service-type":
		mycluster.SetProvProxyServiceType(value)
	case "monitoring-address":
		mycluster.SetMonitoringAddress(value)
	case "scheduler-db-servers-logical-backup-cron":
		mycluster.SetSchedulerDbServersLogicalBackupCron(value)
	case "scheduler-db-servers-logs-cron":
		mycluster.SetSchedulerDbServersLogsCron(value)
	case "scheduler-db-servers-logs-table-rotate-cron":
		mycluster.SetSchedulerDbServersLogsTableRotateCron(value)
	case "scheduler-db-servers-optimize-cron":
		mycluster.SetSchedulerDbServersOptimizeCron(value)
	case "scheduler-db-servers-analyze-cron":
		mycluster.SetSchedulerDbServersAnalyzeCron(value)
	case "scheduler-db-servers-physical-backup-cron":
		mycluster.SetSchedulerDbServersPhysicalBackupCron(value)
	case "scheduler-rolling-reprov-cron":
		mycluster.SetSchedulerRollingReprovCron(value)
	case "scheduler-rolling-restart-cron":
		mycluster.SetSchedulerRollingRestartCron(value)
	case "scheduler-sla-rotate-cron":
		mycluster.SetSchedulerSlaRotateCron(value)
	case "scheduler-jobs-ssh-cron":
		mycluster.SetSchedulerJobsSshCron(value)
	case "scheduler-alert-disable-cron":
		mycluster.SetSchedulerAlertDisableCron(value)
	case "backup-binlogs-keep":
		mycluster.SetBackupBinlogsKeep(value)
	case "delay-stat-rotate":
		mycluster.SetDelayStatRotate(value)
	case "print-delay-stat-interval":
		mycluster.SetPrintDelayStatInterval(value)
	case "log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogLevel(val)
	case "log-writer-election-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogWriterElectionLevel(val)
	case "log-sst-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogSSTLevel(val)
	case "log-heartbeat-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogHeartbeatLevel(val)
	case "log-config-load-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogConfigLoadLevel(val)
	case "log-git-level":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.SetLogGitLevel(val)
	case "log-backup-stream-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogBackupStreamLevel(val)
	case "log-orchestrator-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogOrchestratorLevel(val)
	case "log-vault-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogVaultLevel(val)
	case "log-topology-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogTopologyLevel(val)
	case "log-proxy-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogProxyLevel(val)
	case "proxysql-log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetProxysqlLogLevel(val)
	case "haproxy-log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetHaproxyLogLevel(val)
	case "proxyjanitor-log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetProxyJanitorLogLevel(val)
	case "maxscale-log-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetMxsLogLevel(val)
	case "force-binlog-purge-total-size":
		val, _ := strconv.Atoi(value)
		mycluster.SetForceBinlogPurgeTotalSize(val)
	case "force-binlog-purge-min-replica":
		val, _ := strconv.Atoi(value)
		mycluster.SetForceBinlogPurgeMinReplica(val)
	case "log-graphite-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogGraphiteLevel(val)
	case "log-binlog-purge-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogBinlogPurgeLevel(val)
	case "graphite-whitelist-template":
		mycluster.SetGraphiteWhitelistTemplate(value)
	case "topology-target":
		mycluster.SetTopologyTarget(value)
	case "log-task-level":
		val, _ := strconv.Atoi(value)
		mycluster.SetLogTaskLevel(val)
	case "monitoring-ignore-errors":
		mycluster.SetMonitorIgnoreErrors(value)
	case "monitoring-capture-trigger":
		mycluster.SetMonitorCaptureTrigger(value)
	case "api-token-timeout":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.SetApiTokenTimeout(val)
	case "sst-send-buffer":
		val, _ := strconv.Atoi(value)
		mycluster.SetSSTBufferSize(val)
	case "alert-pushover-app-token":
		mycluster.SetAlertPushoverAppToken(value)
	case "alert-pushover-user-token":
		mycluster.SetAlertPushoverUserToken(value)
	case "alert-script":
		mycluster.SetAlertScript(value)
	case "alert-slack-channel":
		mycluster.SetAlertSlackChannel(value)
	case "alert-slack-url":
		mycluster.SetAlertSlackUrl(value)
	case "alert-slack-user":
		mycluster.SetAlertSlackUser(value)
	case "alert-teams-proxy-url":
		mycluster.SetAlertTeamsProxyUrl(value)
	case "alert-teams-state":
		mycluster.SetAlertTeamsState(value)
	case "alert-teams-url":
		mycluster.SetAlertTeamsUrl(value)
	case "monitoring-alert-trigger":
		mycluster.SetMonitoringAlertTriggerl(value)
	case "mail-smtp-addr":
		mycluster.Conf.SetMailSmtpAddr(value)
	case "mail-smtp-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.MailSMTPPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.MailSMTPPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("mail-smtp-password")
		mycluster.Conf.Secrets["mail-smtp-password"] = new_secret
	case "mail-smtp-user":
		mycluster.Conf.SetMailSmtpUser(value)
	case "mail-to":
		mycluster.Conf.SetMailTo(value)
	case "mail-from":
		mycluster.Conf.SetMailFrom(value)
	case "scheduler-alert-disable-time":
		val, _ := strconv.Atoi(value)
		mycluster.SetSchedulerAlertDisableTime(val)
	case "cloud18":
		mycluster.Conf.Cloud18 = (value == "true")
	case "cloud18-domain":
		mycluster.Conf.Cloud18Domain = value
	case "cloud18-sub-domain":
		mycluster.Conf.Cloud18SubDomain = value
	case "cloud18-sub-domain-zone":
		mycluster.Conf.Cloud18SubDomainZone = value
	case "cloud18-gitlab-user":
		mycluster.Conf.Cloud18GitUser = value
	case "cloud18-gitlab-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.Cloud18GitPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.Cloud18GitPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("cloud18-gitlab-password")
		mycluster.Conf.Secrets["cloud18-gitlab-password"] = new_secret
	case "cloud18-platform-description":
		mycluster.Conf.Cloud18PlatformDescription = value
	case "log-file-level":
		val, _ := strconv.Atoi(value)
		mycluster.Conf.LogFileLevel = val
	case "backup-restic-repository":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupResticRepository = string(val)
	case "backup-restic-aws-access-key-id":
		mycluster.Conf.BackupResticAwsAccessKeyId = value
	case "backup-restic-aws-access-secret":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupResticAwsAccessSecret = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.BackupResticAwsAccessSecret
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("backup-restic-aws-access-secret")
		mycluster.Conf.Secrets["backup-restic-aws-access-secret"] = new_secret
	case "backup-restic-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupResticPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = mycluster.Conf.BackupResticPassword
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("backup-restic-password")
		mycluster.Conf.Secrets["backup-restic-password"] = new_secret
	case "backup-mydumper-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupMyDumperOptions = string(val)
	case "backup-mydumper-regex":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupMyDumperRegex = string(val)
	case "backup-myloader-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupMyLoaderOptions = string(val)
	case "backup-mysqldump-options":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupMysqldumpOptions = string(val)
	case "cloud18-monthly-infra-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyInfraCost = val
	case "cloud18-monthly-license-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyLicenseCost = val
	case "cloud18-monthly-sysops-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlySysopsCost = val
	case "cloud18-monthly-dbops-cost":
		val, _ := strconv.ParseFloat(value, 64)
		mycluster.Conf.Cloud18MonthlyDbopsCost = val
	case "cloud18-cost-currency":
		mycluster.Conf.Cloud18CostCurrency = value
	case "cloud18-database-read-write-split-srv-record":
		mycluster.SetCloud18DatabaseReadWriteSplitSrvRecord(value)
	case "cloud18-database-read-srv-record":
		mycluster.SetCloud18DatabaseReadSrvRecord(value)
	case "cloud18-database-read-write-srv-record":
		mycluster.SetCloud18DatabaseReadWriteSrvRecord(value)
	case "cloud18-dba-user-credentials":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		cred := string(val)
		dbauser, dbapass := misc.SplitPair(cred)
		if dbauser != "" {
			if dbapass == "" {
				dbapass, _ = mycluster.GeneratePassword()
			}
			err = mycluster.SetDBAUserCredentials(dbauser, dbapass)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error setting dba user credentials: %s", err.Error())
				return err
			}
		}

		var new_secret config.Secret
		new_secret.Value = cred
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials")

		mycluster.Conf.Cloud18DbaUserCredentials = cred
		mycluster.Conf.Secrets["cloud18-dba-user-credentials"] = new_secret
	case "cloud18-sponsor-user-credentials":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}

		cred := string(val)
		suser, spass := misc.SplitPair(cred)
		if suser != "" {
			if spass == "" {
				spass, _ = mycluster.GeneratePassword()
			}
			err = mycluster.SetSponsorUserCredentials(suser, spass)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Error setting sponsor user credentials: %s", err.Error())
				return err
			}
		}

		var new_secret config.Secret
		new_secret.Value = cred
		new_secret.OldValue = mycluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials")

		mycluster.Conf.Cloud18SponsorUserCredentials = cred
		mycluster.Conf.Secrets["cloud18-sponsor-user-credentials"] = new_secret
	case "cloud18-cloud18-dbops":
		if value != "" && value != mycluster.Conf.Cloud18GitUser {
			dbops := repman.CreateDBOpsForm(value)
			if dbuser, ok := mycluster.APIUsers[value]; !ok {
				err = mycluster.AddUser(dbops, mycluster.Conf.Cloud18GitUser, true)
			} else {
				dbops.Grants = mycluster.AppendGrants(dbops.Grants, &dbuser)
				dbops.Roles = mycluster.AppendRoles(dbops.Roles, &dbuser)
				err = mycluster.UpdateUser(dbops, mycluster.Conf.Cloud18GitUser, true)
			}

			if err != nil {
				return err
			}

			mycluster.Conf.Cloud18DbOps = value
		}
	case "cloud18-external-sysops":
		if value != "" && value != mycluster.Conf.Cloud18GitUser {
			esys := repman.CreateExtSysopsForm(value)
			if euser, ok := mycluster.APIUsers[value]; !ok {
				err = mycluster.AddUser(esys, mycluster.Conf.Cloud18GitUser, true)
			} else {
				esys.Grants = mycluster.AppendGrants(esys.Grants, &euser)
				esys.Roles = mycluster.AppendRoles(esys.Roles, &euser)
				err = mycluster.UpdateUser(esys, mycluster.Conf.Cloud18GitUser, true)
			}

			if err != nil {
				return err
			}
			mycluster.Conf.Cloud18ExternalSysOps = value
		}
	case "cloud18-external-dbops":
		// If external dbops different from cloud18 dbops
		if mycluster.Conf.Cloud18ExternalDbOps != "" && mycluster.Conf.Cloud18ExternalDbOps != mycluster.Conf.Cloud18DbOps {
			edbops := repman.CreateExtDBOpsForm(mycluster.Conf.Cloud18ExternalDbOps)
			if edbuser, ok := mycluster.APIUsers[mycluster.Conf.Cloud18ExternalDbOps]; !ok {
				err = mycluster.AddUser(edbops, mycluster.Conf.Cloud18GitUser, true)
			} else {
				edbops.Grants = mycluster.AppendGrants(edbops.Grants, &edbuser)
				edbops.Roles = mycluster.AppendRoles(edbops.Roles, &edbuser)
				err = mycluster.UpdateUser(edbops, mycluster.Conf.Cloud18GitUser, true)
			}

			if err != nil {
				return err
			}
			mycluster.Conf.Cloud18ExternalDbOps = value
		}
	case "backup-save-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupSaveScript = string(val)
	case "backup-load-script":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		mycluster.Conf.BackupSaveScript = string(val)
	default:
		return errors.New("Setting not found")
	}
	mycluster.Save()
	return nil
}

func (repman *ReplicationManager) setRepmanSetting(name string, value string) error {
	var v int
	//not immutable
	if !repman.Conf.IsVariableImmutable(name) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive set setting %s", name)
	} else {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive set setting %s", name)
	}

	switch name {
	case "api-token-timeout":
		val, _ := strconv.Atoi(value)
		repman.Conf.SetApiTokenTimeout(val)
	case "cloud18":
		if value == "true" {
			if err := repman.InitGitConfig(&repman.Conf); err != nil {
				if strings.Contains(err.Error(), "invalid_grant") {
					return fmt.Errorf("invalid_grant")
				}
				return err
			}
		}
		repman.Conf.Cloud18 = (value == "true")
	case "cloud18-domain":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18Domain = value
	case "cloud18-sub-domain":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18SubDomain = value
	case "cloud18-sub-domain-zone":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18SubDomainZone = value
	case "cloud18-gitlab-user":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		repman.Conf.Cloud18GitUser = value
	case "cloud18-gitlab-password":
		if repman.Conf.Cloud18 {
			return errors.New("Unable to change setting when cloud18 is ON")
		}
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		repman.Conf.Cloud18GitPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.Cloud18GitPassword
		new_secret.OldValue = repman.Conf.GetDecryptedValue("cloud18-gitlab-password")
		repman.Conf.Secrets["cloud18-gitlab-password"] = new_secret
	case "api-bind":
		repman.Conf.APIBind = value
	case "api-port ":
		repman.Conf.APIPort = value
	case "api-public-url":
		repman.Conf.APIPublicURL = value
	case "arbitration-external-hosts":
		repman.Conf.ArbitrationSasHosts = value
	case "arbitration-external-secret":
		repman.Conf.ArbitrationSasSecret = value
	case "arbitration-external-unique-id":
		v, _ = strconv.Atoi(value)
		repman.Conf.ArbitrationSasUniqueId = v
	case "arbitration-failed-master-script":
		repman.Conf.ArbitrationFailedMasterScript = value
	case "arbitration-peer-hosts":
		repman.Conf.ArbitrationPeerHosts = value
	case "arbitration-read-timeout":
		v, _ = strconv.Atoi(value)
		repman.Conf.ArbitrationReadTimout = v
	case "git-acces-token":
		repman.Conf.GitAccesToken = value
	case "git-monitoring-ticker":
		v, _ = strconv.Atoi(value)
		repman.Conf.GitMonitoringTicker = v
	case "git-url":
		repman.Conf.GitUrl = value
	case "git-username":
		repman.Conf.GitUsername = value
	case "graphite-carbon-api-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonApiPort = v
	case "graphite-carbon-link-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonLinkPort = v
	case "graphite-carbon-host":
		repman.Conf.GraphiteCarbonHost = value
	case "graphite-carbon-pickle-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPicklePort = v
	case "graphite-carbon-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPort = v
	case "graphite-carbon-pprof-port ":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonPprofPort = v
	case "graphite-carbon-server-port":
		v, _ = strconv.Atoi(value)
		repman.Conf.GraphiteCarbonServerPort = v
	case "http-bind-address ":
		repman.Conf.BindAddr = value
	case "http-port":
		repman.Conf.HttpPort = value
	case "http-session-lifetime":
		v, _ = strconv.Atoi(value)
		repman.Conf.SessionLifeTime = v
	case "monitoring-address":
		repman.Conf.MonitorAddress = value
	case "prov-service-plan-registry":
		repman.Conf.ProvServicePlanRegistry = value
	case "prov-service-plan":
		repman.Conf.ProvServicePlan = value
	case "sysbench-binary-path":
		repman.Conf.SysbenchBinaryPath = value
	case "backup-mydumper-path":
		repman.Conf.BackupMyDumperPath = value
	case "backup-myloader-path ":
		repman.Conf.BackupMyLoaderPath = value
	case "backup-mysqlbinlog-path":
		repman.Conf.BackupMysqlbinlogPath = value
	case "backup-mysqlclient-path":
		repman.Conf.BackupMysqlclientPath = value
	case "backup-mysqldump-path":
		repman.Conf.BackupMysqldumpPath = value
	case "backup-restic-binary-path":
		repman.Conf.BackupResticBinaryPath = value
	case "haproxy-binary-path":
		repman.Conf.HaproxyBinaryPath = value
	case "maxscale-binary-pat":
		repman.Conf.MxsBinaryPath = value
	case "log-file-level":
		val, _ := strconv.Atoi(value)
		repman.Conf.LogFileLevel = val
		repman.UpdateFileHookLogLevel(repman.fileHook.(*s18log.RotateFileHook), val)
	case "log-git-level":
		val, _ := strconv.Atoi(value)
		repman.Conf.SetLogGitLevel(val)
	case "mail-smtp-addr":
		repman.Conf.SetMailSmtpAddr(value)
		repman.ReloadMailerConfig()
	case "mail-smtp-password":
		val, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return errors.New("Unable to decode")
		}
		repman.Conf.MailSMTPPassword = string(val)
		var new_secret config.Secret
		new_secret.Value = repman.Conf.MailSMTPPassword
		new_secret.OldValue = repman.Conf.GetDecryptedValue("mail-smtp-password")
		repman.Conf.Secrets["mail-smtp-password"] = new_secret
		repman.ReloadMailerConfig()
	case "mail-smtp-user":
		repman.Conf.SetMailSmtpUser(value)
		repman.ReloadMailerConfig()
	case "mail-to":
		repman.Conf.SetMailTo(value)
	case "mail-from":
		repman.Conf.SetMailFrom(value)
	default:
		return errors.New("Setting not found")
	}

	repman.Save()
	return nil
}

func (repman *ReplicationManager) switchRepmanSetting(name string) error {
	//not immutable
	if !repman.Conf.IsVariableImmutable(name) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", name)
	} else {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Overwriting an immutable parameter defined in config , please use config-merge command to preserve them between restart")
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", name)
	}

	switch name {
	case "cloud18-shared":
		repman.Conf.SwitchCloud18Shared()
	case "api-https-bind":
		repman.Conf.APIHttpsBind = !repman.Conf.APIHttpsBind
	case "api-server":
		repman.Conf.ApiServ = !repman.Conf.ApiServ
	case "api-swagger-enabled":
		repman.Conf.ApiSwaggerEnabled = !repman.Conf.ApiSwaggerEnabled
	case "arbitration-external ":
		repman.Conf.Arbitration = !repman.Conf.Arbitration
	case "graphite-embedded":
		repman.Conf.GraphiteEmbedded = !repman.Conf.GraphiteEmbedded
	case "graphite-blacklist  ":
		repman.Conf.GraphiteBlacklist = !repman.Conf.GraphiteBlacklist
	case "graphite-metrics ":
		repman.Conf.GraphiteMetrics = !repman.Conf.GraphiteMetrics
	case "http-server":
		repman.Conf.HttpServ = !repman.Conf.HttpServ
	case "http-use-react ":
		repman.Conf.HttpUseReact = !repman.Conf.HttpUseReact
	case "monitoring-save-config  ":
		repman.Conf.ConfRewrite = !repman.Conf.ConfRewrite
	case "sysbench-v1":
		repman.Conf.SysbenchV1 = !repman.Conf.SysbenchV1
	case "scheduler-db-servers-receiver-use-ssl":
		repman.Conf.SchedulerReceiverUseSSL = !repman.Conf.SchedulerReceiverUseSSL
	case "mail-smtp-tls-skip-verify":
		repman.Conf.SwitchMailSmtpTlsSkipVerify()
		repman.ReloadMailerConfig()
	default:
		return errors.New("Setting not found")
	}
	repman.Save()
	return nil
}

func (repman *ReplicationManager) setServerSetting(user string, URL string, name string, value string) error {
	err := repman.setRepmanSetting(name, value)
	if err != nil {
		return err
	}

	for _, cl := range repman.Clusters {
		//Don't print error with no valid ACL
		if cl.IsURLPassACL(user, URL, false) {
			repman.setClusterSetting(cl, name, value)
		}
	}

	return nil
}

func (repman *ReplicationManager) switchServerSetting(user string, URL string, name string) error {
	err := repman.switchRepmanSetting(name)
	if err != nil {
		return err
	}
	for cname, cl := range repman.Clusters {
		//Don't print error with no valid ACL
		if cl.IsURLPassACL(user, fmt.Sprintf(URL, cname), false) {
			repman.switchClusterSettings(cl, name)
		}
	}

	return nil
}

// handlerMuxReloadPlans handles the reloading of cluster plans.
// @Summary Reload cluster plans
// @Description This endpoint reloads the cluster plans for all clusters.
// @Tags ClusterActions
// @Success 200 {string} string "Successfully reloaded plans"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/settings/actions/reload-clusters-plans [post]
func (repman *ReplicationManager) handlerMuxReloadPlans(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var mycluster *cluster.Cluster
	for _, v := range repman.Clusters {
		if v != nil {
			mycluster = v
			break
		}
	}

	if mycluster != nil {
		valid, apiuser := repman.IsValidClusterACL(r, mycluster)
		if valid {
			repman.InitServicePlans()
			for _, cl := range repman.Clusters {
				//Don't print error with no valid ACL
				if cl.IsURLPassACL(apiuser, r.URL.Path, false) {
					cl.SetServicePlan(cl.Conf.ProvServicePlan)
				}
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for global setting: %s", r.URL.Path), 403)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxAddTag handles the addition of a tag to a given cluster.
// @Summary Add a tag to a specific cluster
// @Description This endpoint adds a tag to the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/add-db-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxAddTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.AddDBTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

// handlerMuxAddProxyTag handles the addition of a proxy tag to a given cluster.
// @Summary Add a proxy tag to a specific cluster
// @Description This endpoint adds a proxy tag to the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag added successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/add-proxy-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxAddProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		if vars["tagValue"] == "" {
			http.Error(w, "Empty tag value", 500)
			return
		}
		mycluster.AddProxyTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

// handlerMuxDropTag handles the removal of a tag from a given cluster.
// @Summary Remove a tag from a specific cluster
// @Description This endpoint removes a tag from the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag removed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/drop-db-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxDropTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.DropDBTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

// handlerMuxDropProxyTag handles the removal of a proxy tag from a given cluster.
// @Summary Remove a proxy tag from a specific cluster
// @Description This endpoint removes a proxy tag from the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param tagValue path string true "Tag Value"
// @Success 200 {string} string "Tag removed successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/drop-proxy-tag/{tagValue} [post]
func (repman *ReplicationManager) handlerMuxDropProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.DropProxyTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxSwitchReadOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.SwitchReadOnly()
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

// handlerMuxLog handles the retrieval of logs for a given cluster.
// @Summary Retrieve logs for a specific cluster
// @Description This endpoint retrieves the logs for the specified cluster.
// @Tags ClusterTopology
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of logs"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/topology/logs [get]
func (repman *ReplicationManager) handlerMuxLog(w http.ResponseWriter, r *http.Request) {
	var clusterlogs []string
	vars := mux.Vars(r)
	for _, slog := range repman.tlog.Buffer {
		if strings.Contains(slog, vars["clusterName"]) {
			clusterlogs = append(clusterlogs, slog)
		}
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(clusterlogs)
	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
}

// handlerMuxCrashes handles the retrieval of crashes for a given cluster.
// @Summary Retrieve crashes for a specific cluster
// @Description This endpoint retrieves the crashes for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of crashes"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/topology/crashes [get]
func (repman *ReplicationManager) handlerMuxCrashes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetCrashes())
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxOneTest handles the execution of a specific test for a given cluster.
// @Summary Run a specific test for a given cluster
// @Description This endpoint runs a specific test for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param testName path string true "Test Name"
// @Param provision formData string false "Provision the cluster before running the test"
// @Param unprovision formData string false "Unprovision the cluster after running the test"
// @Success 200 {object} cluster.Test "Test result"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/tests/actions/run/{testName} [post]
func (repman *ReplicationManager) handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		r.ParseForm() // Parses the request body
		if r.Form.Get("provision") == "true" {
			mycluster.SetTestStartCluster(true)
		}
		if r.Form.Get("unprovision") == "true" {
			mycluster.SetTestStopCluster(true)
		}

		res := repman.RunAllTests(mycluster, vars["testName"], "")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")

		if len(res) > 0 {
			err := e.Encode(res[0])
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}
		} else {
			var test cluster.Test
			test.Result = "FAIL"
			test.Name = vars["testName"]
			err := e.Encode(test)
			if err != nil {
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}

		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		mycluster.SetTestStartCluster(false)
		mycluster.SetTestStopCluster(false)
		return
	}
	mycluster.SetTestStartCluster(false)
	mycluster.SetTestStopCluster(false)
	return
}

// handlerMuxTests handles the execution of all tests for a given cluster.
// @Summary Run all tests for a given cluster
// @Description This endpoint runs all tests for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} cluster.Test "List of test results"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/tests/actions/run/all [post]
func (repman *ReplicationManager) handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}

		res := repman.RunAllTests(mycluster, "ALL", "")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(res)
		if err != nil {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

// handlerMuxSettingsReload handles the reloading of cluster settings.
// @Summary Reload cluster settings
// @Description This endpoint reloads the settings for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded settings"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/settings/actions/reload [post]
func (repman *ReplicationManager) handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	repman.InitConfig(repman.Conf, true)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		//mycluster.ReloadConfig(repman.Confs[vars["clusterName"]])
		mycluster.ReloadConfig(mycluster.Conf)
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

// handlerMuxServerAdd handles the addition of a server to a given cluster.
// @Summary Add a server to a specific cluster
// @Description This endpoint adds a server to the specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param host path string true "Host"
// @Param port path string true "Port"
// @Param type path string false "Type"
// @Success 200 {string} string "Monitor added"
// @Failure 403 {string} string "No valid ACL"
// @Failure 409 {string} string "Error adding new monitor"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type} [post]
// @Router /api/clusters/{clusterName}/actions/addserver/{host}/{port} [post]
func (repman *ReplicationManager) handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	var err error
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			w.WriteHeader(403)
			w.Write([]byte(`{"msg":"No valid ACL"}`))
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive new %s monitor to be added %s", vars["type"], vars["host"]+":"+vars["port"])
		if vars["type"] == "" {
			err = mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
		} else {
			if mycluster.MonitorType[vars["type"]] == "proxy" {
				err = mycluster.AddSeededProxy(vars["type"], vars["host"], vars["port"], "", "")
			} else if mycluster.MonitorType[vars["type"]] == "database" {
				switch vars["type"] {
				case "mariadb":
					mycluster.Conf.ProvDbImg = "mariadb:latest"
				case "percona":
					mycluster.Conf.ProvDbImg = "percona:latest"
				case "mysql":
					mycluster.Conf.ProvDbImg = "mysql:latest"
				}
				err = mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
			}
		}

		// This will only return duplicate error
		if err != nil {
			errStr := fmt.Sprintf("Error adding new %s monitor of %s: %s", vars["type"], vars["host"]+":"+vars["port"], err.Error())
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, errStr)
			w.WriteHeader(409)
			w.Write([]byte(`{"msg":"` + errStr + `"}`))
			return
		} else {
			w.WriteHeader(200)
			w.Write([]byte(`{"msg":"Monitor added"}`))
			return
		}
	} else {
		w.WriteHeader(500)
		w.Write([]byte(`{"msg":"Cluster Not Found"}`))
		return
	}

}

// handlerMuxServerDrop handles the HTTP request to drop a server monitor from a cluster.
//
// @Summary Drop a server monitor from a cluster
// @Description This endpoint allows dropping a server monitor or proxy monitor from a specified cluster.
// @Tags ClusterMonitor
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param type path string false "Monitor Type (proxy or database)"
// @Param host path string true "Host"
// @Param port path string true "Port"
// @Success 200 {string} string "Monitor dropped successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /cluster/{clusterName}/actions/dropserver/{host}/{port}/{type} [post]
// @Router /cluster/{clusterName}/actions/dropserver/{host}/{port} [post]
func (repman *ReplicationManager) handlerMuxServerDrop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rest API receive drop %s monitor command for %s", vars["type"], vars["host"]+":"+vars["port"])
		if vars["type"] == "" {
			mycluster.RemoveServerMonitor(vars["host"], vars["port"])
		} else {
			if mycluster.MonitorType[vars["type"]] == "proxy" {
				mycluster.RemoveProxyMonitor(vars["type"], vars["host"], vars["port"])
			} else if mycluster.MonitorType[vars["type"]] == "database" {
				mycluster.RemoveServerMonitor(vars["host"], vars["port"])
			}
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

// handlerMuxClusterStatus handles the HTTP request to retrieve the status of a specified cluster.
// @Summary Retrieve status of a cluster
// @Description This endpoint retrieves the status of a specified cluster and returns it in JSON format.
// @Tags Cluster
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} map[string]string "Cluster status"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/status [get]
func (repman *ReplicationManager) handlerMuxClusterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Content-Type", "application/json")
		if mycluster.GetStatus() {
			io.WriteString(w, `{"alive": "running"}`)
		} else {
			io.WriteString(w, `{"alive": "errors"}`)
		}
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterMasterPhysicalBackup handles the physical backup process for the master of a given cluster.
// @Summary Perform a physical backup for the master of a specific cluster
// @Description This endpoint triggers a physical backup for the master of the specified cluster.
// @Tags ClusterBackup
// @Accept json
// @Produce json
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered physical backup"
// @Failure 403 {string} string "No valid ACL"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/actions/master-physical-backup [post]
func (repman *ReplicationManager) handlerMuxClusterMasterPhysicalBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		w.WriteHeader(http.StatusOK)
		mycluster.GetMaster().JobBackupPhysical()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterOptimize handles the optimization process for a given cluster.
// @Summary Optimize a specific cluster
// @Description This endpoint triggers the optimization process for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered optimization"
// @Failure 403 {string} string "No valid ACL"
// @Failure 400 {string} string "No cluster found"
// @Router /api/clusters/{clusterName}/actions/optimize [post]
func (repman *ReplicationManager) handlerMuxClusterOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		w.WriteHeader(http.StatusOK)
		mycluster.RollingOptimize()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func (repman *ReplicationManager) handlerMuxClusterSSTStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	port, err := strconv.Atoi(vars["port"])
	w.WriteHeader(http.StatusOK)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.SSTCloseReceiver(port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

// handlerMuxClusterSysbench handles the execution of sysbench for a given cluster.
// @Summary Run sysbench for a specific cluster
// @Description This endpoint runs sysbench for the specified cluster.
// @Tags ClusterTest
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param threads query string false "Number of threads"
// @Success 200 {string} string "Successfully triggered sysbench"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/sysbench [post]
func (repman *ReplicationManager) handlerMuxClusterSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		if r.URL.Query().Get("threads") != "" {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Sysbench threads to %s", r.URL.Query().Get("threads"))
			mycluster.SetSysbenchThreads(r.URL.Query().Get("threads"))
		}
		go mycluster.RunSysbench()
	}
	return
}

// handlerMuxClusterApplyDynamicConfig handles the application of dynamic configuration for a given cluster.
// @Summary Apply dynamic configuration for a specific cluster
// @Description This endpoint applies dynamic configuration for the specified cluster.
// @Tags ClusterTags
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully applied dynamic configuration"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/apply-dynamic-config [post]
func (repman *ReplicationManager) handlerMuxClusterApplyDynamicConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.SetDBDynamicConfig()
	}
	return
}

// handlerMuxClusterReloadCertificates handles the reloading of client certificates for a given cluster.
// @Summary Reload client certificates for a specific cluster
// @Description This endpoint reloads the client certificates for the specified cluster.
// @Tags ClusterSettings
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded client certificates"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/certificates-reload [post]
func (repman *ReplicationManager) handlerMuxClusterReloadCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.ReloadCertificates()
	}
	return
}

// handlerMuxClusterWaitDatabases handles the waiting for databases to be ready for a given cluster.
// @Summary Wait for databases to be ready for a specific cluster
// @Description This endpoint waits for the databases to be ready for the specified cluster.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Databases are ready"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/waitdatabases [post]
func (repman *ReplicationManager) handlerMuxClusterWaitDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.WaitDatabaseCanConn()
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
	}
	return
}

// handlerMuxCluster handles the HTTP request to retrieve the details of a specified cluster.
// @Summary Retrieve details of a cluster
// @Description This endpoint retrieves the details of a specified cluster and returns it in JSON format.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} cluster.Cluster "Cluster details"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName} [get]
func (repman *ReplicationManager) handlerMuxCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		cl, err := json.Marshal(mycluster)
		if err != nil {
			http.Error(w, "Error Marshal", 500)
			return
		}

		for crkey, _ := range mycluster.Conf.Secrets {
			cl, err = jsonparser.Set(cl, []byte(`"*:*" `), "config", strcase.ToLowerCamel(crkey))
		}
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}

		list, _ := json.Marshal(mycluster.BackupMetaMap.ToNewMap())
		if len(list) > 0 {
			cl, err = jsonparser.Set(cl, list, "backupList")
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write(cl)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSettings handles the retrieval of settings for a given cluster.
// @Summary Retrieve settings for a specific cluster
// @Description This endpoint retrieves the settings for the specified cluster.
// @Tags ClusterSettings
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} config.Config "Cluster settings"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings [get]
func (repman *ReplicationManager) handlerMuxClusterSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.Conf)
		if err != nil {
			http.Error(w, "Encoding error in settings", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSendVaultToken sends the Vault token to the specified cluster via email.
// @Summary Send Vault token to a specific cluster
// @Description This endpoint sends the Vault token to the specified cluster via email.
// @Tags ClusterVault
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Vault token sent successfully"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/send-vault-token [post]
func (repman *ReplicationManager) handlerMuxClusterSendVaultToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.SendVaultTokenByMail(mycluster.Conf)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterSchemaChecksumAllTable handles the checksum calculation for all tables in a given cluster.
// @Summary Calculate checksum for all tables in a specific cluster
// @Description This endpoint triggers the checksum calculation for all tables in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for all tables"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/checksum-all-tables [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumAllTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.CheckAllTableChecksum()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSchemaChecksumTable handles the checksum calculation for a specific table in a given cluster.
// @Summary Calculate checksum for a specific table in a specific cluster
// @Description This endpoint triggers the checksum calculation for a specific table in the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully triggered checksum calculation for the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.CheckTableChecksum(vars["schemaName"], vars["tableName"])
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSchemaUniversalTable handles the setting of a universal table for a given cluster.
// @Summary Set a universal table for a specific cluster
// @Description This endpoint sets a universal table for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Success 200 {string} string "Successfully set universal table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/universal-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaUniversalTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pri := range mycluster.Proxies {
			if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
				go mycluster.ShardSetUniversalTable(pr, vars["schemaName"], vars["tableName"])
			}
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSchemaReshardTable handles the resharding of a table for a given cluster.
// @Summary Reshard a table for a specific cluster
// @Description This endpoint triggers the resharding of a table for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param clusterList path string false "Cluster List"
// @Success 200 {string} string "Successfully triggered resharding of the table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table/{clusterList} [post]
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaReshardTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pri := range mycluster.Proxies {
			if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
				clusters := mycluster.GetClusterListFromShardProxy(mycluster.Conf.MdbsProxyHosts)
				if vars["clusterList"] == "" {
					mycluster.ShardProxyReshardTable(pr, vars["schemaName"], vars["tableName"], clusters)
				} else {
					var clustersFilter map[string]*cluster.Cluster
					for _, c := range clusters {
						if strings.Contains(vars["clusterList"], c.GetName()) {
							clustersFilter[c.GetName()] = c
						}
					}
					mycluster.ShardProxyReshardTable(pr, vars["schemaName"], vars["tableName"], clustersFilter)
				}
			}
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerMuxClusterSchemaMoveTable handles the movement of a table to a different shard cluster.
// @Summary Move a table to a different shard cluster
// @Description This endpoint moves a table to a different shard cluster for the specified cluster.
// @Tags ClusterSchema
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param schemaName path string true "Schema Name"
// @Param tableName path string true "Table Name"
// @Param clusterShard path string true "Cluster Shard"
// @Success 200 {string} string "Successfully moved table"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/move-table/{clusterShard} [post]
func (repman *ReplicationManager) handlerMuxClusterSchemaMoveTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pri := range mycluster.Proxies {
			if pr, ok := pri.(*cluster.MariadbShardProxy); ok {
				if vars["clusterShard"] != "" {
					destcluster := repman.getClusterByName(vars["clusterShard"])
					if mycluster != nil {
						mycluster.ShardProxyMoveTable(pr, vars["schemaName"], vars["tableName"], destcluster)
						return
					}
				}
			}
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	http.Error(w, "Unrichable code", 500)
	return

}

// handlerMuxClusterSchema handles the retrieval of schema information for a given cluster.
// @Summary Retrieve schema information for a specific cluster
// @Description This endpoint retrieves the schema information for the specified cluster.
// @Tags ClusterSchema
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {object} map[string]interface{} "Schema information"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/schema [get]
func (repman *ReplicationManager) handlerMuxClusterSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		if mycluster.GetMaster() != nil {
			err := e.Encode(mycluster.GetMaster().GetDictTables())
			if err != nil {
				http.Error(w, "Encoding error in settings", 500)
				return
			}
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

// handlerDiffVariables handles the retrieval of variable differences for a given cluster.
// @Summary Retrieve variable differences for a specific cluster
// @Description This endpoint retrieves the variable differences for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} cluster.VariableDiff "List of variable differences"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/diffvariables [get]
func (repman *ReplicationManager) handlerDiffVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		vars := mycluster.DiffVariables
		if vars == nil {
			vars = []cluster.VariableDiff{}
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(vars)
		if err != nil {
			http.Error(w, "Encoding error for DiffVariables", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerRotatePasswords rotates the passwords for a given cluster.
// @Summary Rotate passwords for a specific cluster
// @Description This endpoint rotates the passwords for the specified cluster.
// @Tags ClusterActions
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully rotated passwords"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/actions/rotate-passwords [post]
func (repman *ReplicationManager) handlerRotatePasswords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.RotatePasswords()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterGraphiteFilterList handles the retrieval of Graphite filter list for a given cluster.
// @Summary Retrieve Graphite filter list for a specific cluster
// @Description This endpoint retrieves the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} string "List of Graphite filters"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/clusters/{clusterName}/graphite-filterlist [get]
func (repman *ReplicationManager) handlerMuxClusterGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.Header().Set("Content-Type", "application/json")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		list := mycluster.GetGraphiteFilterList()
		err := e.Encode(list)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

// handlerMuxClusterSetGraphiteFilterList sets the Graphite filter list for a given cluster.
// @Summary Set Graphite filter list for a specific cluster
// @Description This endpoint sets the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param filterType path string true "Filter Type"
// @Param body body cluster.GraphiteFilterList true "Graphite Filter List"
// @Success 200 {string} string "Filterlist updated"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/set-graphite-filterlist/{filterType} [post]
func (repman *ReplicationManager) handlerMuxClusterSetGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var gfilter cluster.GraphiteFilterList
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		err := json.NewDecoder(r.Body).Decode(&gfilter)
		if err != nil {
			http.Error(w, fmt.Sprintf("Decode error :%s", err.Error()), http.StatusInternalServerError)
			return
		}

		err = mycluster.SetGraphiteFilterList(vars["filterType"], gfilter)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Write([]byte("Filterlist updated"))
		return
	} else {

		http.Error(w, "No cluster", http.StatusInternalServerError)
		return
	}
}

// handlerMuxClusterReloadGraphiteFilterList handles the reloading of Graphite filter list for a given cluster.
// @Summary Reload Graphite filter list for a specific cluster
// @Description This endpoint reloads the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Successfully reloaded Graphite filter list"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/reload-graphite-filterlist [post]
func (repman *ReplicationManager) handlerMuxClusterReloadGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		go mycluster.ReloadGraphiteFilterList()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterResetGraphiteFilterList handles the reset of Graphite filter list for a given cluster.
// @Summary Reset Graphite filter list for a specific cluster
// @Description This endpoint resets the Graphite filter list for the specified cluster.
// @Tags ClusterGraphite
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param template path string true "Template"
// @Success 200 {string} string "Successfully reset Graphite filter list"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "No cluster"
// @Router /api/clusters/{clusterName}/settings/actions/reset-graphite-filterlist/{template} [post]
func (repman *ReplicationManager) handlerMuxClusterResetGraphiteFilterList(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
		mycluster.SetGraphiteWhitelistTemplate(vars["template"])
		if err := mycluster.ResetFilterListRegexp(); err != nil {
			http.Error(w, fmt.Sprintf("Error while reset filterlist: %s", err.Error()), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

// handlerMuxClusterGetJobEntries retrieves job entries for a specific cluster.
// @Summary Retrieve job entries for a specific cluster
// @Description This endpoint retrieves the job entries for the specified cluster.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} map[string]interface{} "List of job entries"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Cluster Not Found"
// @Router /api/clusters/{clusterName}/jobs [get]
func (repman *ReplicationManager) handlerMuxClusterGetJobEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", 403)
			return
		}
		entries, _ := mycluster.JobsGetEntries()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}

// handlerMuxAcceptSubscription handles the acceptance of a subscription for a given cluster.
// @Summary Accept a subscription for a specific cluster
// @Description This endpoint accepts a subscription for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Email sent to sponsor!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error accepting subscription"
// @Router /api/clusters/{clusterName}/sales/accept-subscription [post]
func (repman *ReplicationManager) handlerMuxAcceptSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", 500)
		return
	}

	if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	if mycluster.Conf.Cloud18DatabaseReadSrvRecord == "" {
		http.Error(w, "Empty Read Srv Record", http.StatusForbidden)
		return
	}

	if mycluster.Conf.Cloud18DatabaseReadWriteSrvRecord == "" {
		http.Error(w, "Empty Read-Write Srv Record", http.StatusForbidden)
		return
	}

	if mycluster.Conf.Cloud18DatabaseReadWriteSplitSrvRecord == "" {
		http.Error(w, "Empty Read-Write Split Srv Record", http.StatusForbidden)
		return
	}

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: "+err.Error())
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Processing sponsorship for %s with %s as sponsor", mycluster.Name, userform.Username)

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for sponsor of cluster %s", mycluster.Name)

	suser, spass := misc.SplitPair(mycluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"))
	if suser == "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No sponsor db credentials found. Generating sponsor db credentials")
		suser = "sponsor"
	}
	if spass == "" {
		spass, _ = mycluster.GeneratePassword()
	}

	err = repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte(suser+":"+spass)))
	if err != nil {
		http.Error(w, "Error setting sponsor db credentials :"+err.Error(), 500)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting up db credentials for dba of cluster %s", mycluster.Name)

	duser, dpass := misc.SplitPair(mycluster.Conf.GetDecryptedValue("cloud18-dba-user-credentials"))
	if duser == "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No dba database credentials found. Generating dba credentials")
		duser = "dba"
	}
	if dpass == "" {
		dpass, _ = mycluster.GeneratePassword()
	}

	err = repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte(duser+":"+dpass)))
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "The sponsorship process for %s is proceeding without creating a DBA user, as it does not impact the sponsor's operations", mycluster.Name)
	}

	err = repman.AcceptSubscription(userform, mycluster)
	if err != nil {
		// Reset sponsor credentials if failed
		repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte("")))
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error accepting subscription : %v", err)
		http.Error(w, "Error accepting subscription :"+err.Error(), 500)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "User %s registered as sponsor successfully", userform.Username)

	if repman.Conf.Cloud18SalesSubscriptionValidateScript != "" {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Executing script after sponsor validated")
		repman.BashScriptSalesSubscriptionValidate(mycluster, userform.Username, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No script to execute after sponsor validated")
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending sponsor activation email to user %s", userform.Username)

	err = repman.SendSponsorActivationMail(mycluster, userform)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send sponsor activation email to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), 500)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor activation email sent to %s", userform.Username)

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sending sponsor db credentials to user %s", userform.Username)

	err = repman.SendSponsorCredentialsMail(mycluster)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send sponsor db credentials to %s: %v", userform.Username, err)
		http.Error(w, "Error sending email :"+err.Error(), 500)
		return
	}

	err = repman.SendDBACredentialsMail(mycluster, "dbops", "admin")
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to send dba db credentials to dbops: %v", err)
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor DB credentials sent!")

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to sponsor!"))
}

// handlerMuxRejectSubscription handles the rejection of a subscription for a given cluster.
// @Summary Reject a subscription for a specific cluster
// @Description This endpoint rejects a subscription for the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Subscription removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing subscription"
// @Router /api/clusters/{clusterName}/sales/refuse-subscription [post]
func (repman *ReplicationManager) handlerMuxRejectSubscription(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", 500)
		return
	}

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: "+err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to reject
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}
	}

	err = repman.CancelSubscription(userform, mycluster)
	if err != nil {
		http.Error(w, "Error removing subscription :"+err.Error(), 500)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pending subscription for %s is rejected!")

	err = repman.SendPendingRejectionMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending rejection mail :"+err.Error(), 500)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Subscription removed!"))
}

// handlerMuxRemoveSponsor handles the removal of a sponsor from a given cluster.
// @Summary Remove a sponsor from a specific cluster
// @Description This endpoint removes a sponsor from the specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body cluster.UserForm true "User Form"
// @Success 200 {string} string "Sponsor subscription removed!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error removing sponsor subscription"
// @Router /api/clusters/{clusterName}/sales/end-subscription [post]
func (repman *ReplicationManager) handlerMuxRemoveSponsor(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", 500)
		return
	}

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: "+err.Error())
		return
	}

	// If user is not the submitter, check if he has the right to remove sponsor
	if uinfomap["User"] != userform.Username {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
			http.Error(w, "No valid ACL", http.StatusForbidden)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Ending subscription from sponsor %s for cluster %s by %s", userform.Username, mycluster.Name, uinfomap["User"])
	} else {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Ending subscription for cluster %s by %s", mycluster.Name, uinfomap["User"])
	}

	err = repman.EndSubscription(userform, mycluster)
	if err != nil {
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error removing sponsor subscription: %s", err)
		http.Error(w, "Error removing sponsor subscription :"+err.Error(), 500)
		return
	}

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Revoking db privileges from sponsor %s for cluster %s", userform.Username, mycluster.Name)
	mycluster.RevokeUserDBGrants(mycluster.Conf.GetDecryptedValue("cloud18-sponsor-user-credentials"), "%")

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Removing sponsor db credentials for cluster %s", mycluster.Name)
	repman.setClusterSetting(mycluster, "cloud18-sponsor-user-credentials", base64.StdEncoding.EncodeToString([]byte("")))

	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Changing dba credentials for cluster %s", mycluster.Name)
	dpass, _ := mycluster.GeneratePassword()
	repman.setClusterSetting(mycluster, "cloud18-dba-user-credentials", base64.StdEncoding.EncodeToString([]byte("dba:"+dpass)))

	if repman.Conf.Cloud18SalesUnsubscribeScript != "" {
		repman.BashScriptSalesUnsubscribe(mycluster, userform.Username, uinfomap["User"])
	}

	err = repman.SendSponsorUnsubscribeMail(mycluster, userform)
	if err != nil {
		http.Error(w, "Error sending rejection mail :"+err.Error(), 500)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Sponsor subscription removed!"))
}

type CredentialMailForm struct {
	Username       string `json:"username"`
	CredentialType string `json:"type"`
}

// handlerMuxSendCredentials sends the credentials to the specified user via email.
// @Summary Send credentials to a specific user
// @Description This endpoint sends the credentials to the specified user via email.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param body body CredentialMailForm true "Credential Mail Form"
// @Success 200 {string} string "Credentials sent to user!"
// @Failure 403 {string} string "No valid ACL"
// @Failure 500 {string} string "Error sending email"
// @Router /api/clusters/{clusterName}/users/send-credentials [post]
func (repman *ReplicationManager) handlerMuxSendCredentials(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", 500)
		return
	}

	valid, delegator := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	duser, ok := mycluster.APIUsers[delegator]
	if !ok {
		http.Error(w, "User does not exists", http.StatusBadRequest)
		return
	}

	var credForm CredentialMailForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&credForm)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	u, ok := mycluster.APIUsers[credForm.Username]
	if !ok {
		http.Error(w, "User does not exists", http.StatusBadRequest)
		return
	}

	to := u.User
	if to == "admin" {
		to = repman.Conf.Cloud18GitUser
	}

	switch credForm.CredentialType {
	case "db":
		if !duser.Roles[config.RoleDBOps] && !(duser.Roles[config.RoleExtDBOps] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}

		err = repman.SendDBACredentialsMail(mycluster, to, delegator)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), 500)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "DBA Credentials sent to %s. Delegator: %s", to, delegator)
	case "sys":
		if !duser.Roles[config.RoleSysOps] && !(duser.Roles[config.RoleExtSysOps] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}
		err = repman.SendSysAdmCredentialsMail(mycluster, to, delegator)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), 500)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "SysAdm Credentials sent to %s. Delegator: %s", to, delegator)
	case "sponsor":
		if !duser.Roles[config.RoleSysOps] && !(duser.Roles[config.RoleSponsor] && duser.User == u.User) {
			http.Error(w, "Delegator has no ACL to send DBA Credentials", http.StatusForbidden)
			return
		}

		err = repman.SendSponsorCredentialsMail(mycluster)
		if err != nil {
			http.Error(w, "Error sending email :"+err.Error(), 500)
			return
		}

		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Sponsor Credentials sent to %s. Delegator: %s", to, delegator)
	default:
		http.Error(w, "Invalid credential type :"+credForm.CredentialType, 500)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Credentials sent to user!"))
}
