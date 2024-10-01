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
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/buger/jsonparser"
	"github.com/iancoleman/strcase"
	log "github.com/sirupsen/logrus"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/auth"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (repman *ReplicationManager) GetClusterPublicRoutes() []Route {

	return []Route{
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/status", repman.handlerMuxClusterStatus},
		{auth.PublicPermission, config.GrantNone, "/api/clusters/{clusterName}/actions/master-physical-backup", repman.handlerMuxClusterMasterPhysicalBackup},
	}
}

func (repman *ReplicationManager) GetClusterProtectedRoutes() []Route {

	return []Route{
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}", repman.handlerMuxCluster},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/settings", repman.handlerMuxClusterSettings},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/tags", repman.handlerMuxClusterTags},
		{auth.ClusterPermission, config.GrantClusterProcess, "/api/clusters/{clusterName}/jobs", repman.handlerMuxClusterGetJobEntries},

		{auth.ClusterPermission, config.GrantClusterShowBackups, "/api/clusters/{clusterName}/backups", repman.handlerMuxClusterBackups},

		{auth.ClusterPermission, config.GrantClusterShowCertificates, "/api/clusters/{clusterName}/certificates", repman.handlerMuxClusterCertificates},

		{auth.ClusterPermission, config.GrantClusterShowRoutes, "/api/clusters/{clusterName}/queryrules", repman.handlerMuxClusterQueryRules},
		{auth.ClusterPermission, config.GrantClusterProcess, "/api/clusters/{clusterName}/top", repman.handlerMuxClusterTop},

		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/shardclusters", repman.handlerMuxClusterShardClusters},

		{auth.ClusterPermission, config.GrantClusterSharing, "/api/clusters/{clusterName}/shared", repman.handlerMuxClusterShared},

		{auth.ClusterPermission, config.GrantClusterVault, "/api/clusters/{clusterName}/send-vault-token", repman.handlerMuxClusterSendVaultToken},

		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/reload", repman.handlerMuxSettingsReload},

		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/switch/{settingName}", repman.handlerMuxSwitchSettings},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", repman.handlerMuxSetSettings},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/set-cron/{settingName}/{settingValue:.*}", repman.handlerMuxSetCron},

		{auth.ClusterPermission, config.GrantDBConfigFlag, "/api/clusters/{clusterName}/settings/actions/add-db-tag/{tagValue}", repman.handlerMuxAddTag},
		{auth.ClusterPermission, config.GrantDBConfigFlag, "/api/clusters/{clusterName}/settings/actions/drop-db-tag/{tagValue}", repman.handlerMuxDropTag},
		{auth.ClusterPermission, config.GrantProxyConfigFlag, "/api/clusters/{clusterName}/settings/actions/add-proxy-tag/{tagValue}", repman.handlerMuxAddProxyTag},
		{auth.ClusterPermission, config.GrantProxyConfigFlag, "/api/clusters/{clusterName}/settings/actions/drop-proxy-tag/{tagValue}", repman.handlerMuxDropProxyTag},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/actions/reset-failover-control", repman.handlerMuxClusterResetFailoverControl},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/discover", repman.handlerMuxSetSettingsDiscover},
		{auth.ClusterPermission, config.GrantDBConfigFlag, "/api/clusters/{clusterName}/settings/actions/apply-dynamic-config", repman.handlerMuxClusterApplyDynamicConfig},

		{auth.ClusterPermission, config.GrantProvCluster, "/api/clusters/{clusterName}/actions/add/{clusterShardingName}", repman.handlerMuxClusterShardingAdd},

		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/actions/switchover", repman.handlerMuxSwitchover},
		{auth.ClusterPermission, config.GrantClusterFailover, "/api/clusters/{clusterName}/actions/failover", repman.handlerMuxFailover},

		{auth.ClusterPermission, config.GrantClusterCertificatesRotate, "/api/clusters/{clusterName}/settings/actions/certificates-rotate", repman.handlerMuxRotateKeys},
		{auth.ClusterPermission, config.GrantClusterCertificatesReload, "/api/clusters/{clusterName}/settings/actions/certificates-reload", repman.handlerMuxClusterReloadCertificates},

		{auth.ClusterPermission, config.GrantClusterResetSLA, "/api/clusters/{clusterName}/actions/reset-sla", repman.handlerMuxResetSla},

		{auth.ClusterPermission, config.GrantClusterReplication, "/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}", repman.handlerMuxBootstrapReplication},
		{auth.ClusterPermission, config.GrantClusterReplication, "/api/clusters/{clusterName}/actions/replication/cleanup", repman.handlerMuxBootstrapReplicationCleanup},

		{auth.ClusterPermission, config.GrantProvDBProvision, "/api/clusters/{clusterName}/services/actions/provision", repman.handlerMuxServicesProvision},
		{auth.ClusterPermission, config.GrantProvDBProvision, "/api/clusters/{clusterName}/services/actions/unprovision", repman.handlerMuxServicesUnprovision},

		{auth.ClusterPermission, config.GrantClusterRolling, "/api/clusters/{clusterName}/actions/cancel-rolling-restart", repman.handlerMuxServicesCancelRollingRestart},
		{auth.ClusterPermission, config.GrantClusterRolling, "/api/clusters/{clusterName}/actions/cancel-rolling-reprov", repman.handlerMuxServicesCancelRollingReprov},

		{auth.ClusterPermission, config.GrantClusterTraffic, "/api/clusters/{clusterName}/actions/stop-traffic", repman.handlerMuxStopTraffic},
		{auth.ClusterPermission, config.GrantClusterTraffic, "/api/clusters/{clusterName}/actions/start-traffic", repman.handlerMuxStartTraffic},

		{auth.ClusterPermission, config.GrantClusterRolling, "/api/clusters/{clusterName}/actions/optimize", repman.handlerMuxClusterOptimize},

		{auth.ClusterPermission, config.GrantClusterTest, "/api/clusters/{clusterName}/actions/sysbench", repman.handlerMuxClusterSysbench},

		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/actions/waitdatabases", repman.handlerMuxClusterWaitDatabases},

		{auth.ClusterPermission, config.GrantClusterCreateMonitor, "/api/clusters/{clusterName}/actions/addserver/{host}/{port}", repman.handlerMuxServerAdd},
		{auth.ClusterPermission, config.GrantClusterCreateMonitor, "/api/clusters/{clusterName}/actions/addserver/{host}/{port}/{type}", repman.handlerMuxServerAdd},

		{auth.ClusterPermission, config.GrantClusterDropMonitor, "/api/clusters/{clusterName}/actions/dropserver/{host}/{port}", repman.handlerMuxServerDrop},
		{auth.ClusterPermission, config.GrantClusterDropMonitor, "/api/clusters/{clusterName}/actions/dropserver/{host}/{port}/{type}", repman.handlerMuxServerDrop},

		{auth.ClusterPermission, config.GrantClusterRolling, "/api/clusters/{clusterName}/actions/rolling", repman.handlerMuxRollingRestart},

		{auth.ClusterPermission, config.GrantClusterRotatePasswords, "/api/clusters/{clusterName}/actions/rotate-passwords", repman.handlerRotatePasswords},

		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table", repman.handlerMuxClusterSchemaReshardTable},
		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/reshard-table/{clusterList}", repman.handlerMuxClusterSchemaReshardTable},
		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/move-table/{clusterShard}", repman.handlerMuxClusterSchemaMoveTable},
		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/universal-table", repman.handlerMuxClusterSchemaUniversalTable},
		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema/{schemaName}/{tableName}/actions/checksum-table", repman.handlerMuxClusterSchemaChecksumTable},

		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/actions/checksum-all-tables", repman.handlerMuxClusterSchemaChecksumAllTable},

		{auth.ClusterPermission, config.GrantClusterSharding, "/api/clusters/{clusterName}/schema", repman.handlerMuxClusterSchema},

		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/graphite-filterlist", repman.handlerMuxClusterGraphiteFilterList},

		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/set-graphite-filterlist/{filterType}", repman.handlerMuxClusterSetGraphiteFilterList},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/reload-graphite-filterlist", repman.handlerMuxClusterReloadGraphiteFilterList},
		{auth.ClusterPermission, config.GrantClusterSettings, "/api/clusters/{clusterName}/settings/actions/reset-graphite-filterlist/{template}", repman.handlerMuxClusterResetGraphiteFilterList},

		// PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY
		{auth.ClusterPermission, config.GrantClusterCreate, "/api/clusters/actions/add/{clusterName}", repman.handlerMuxClusterAdd},
		{auth.ClusterPermission, config.GrantClusterDelete, "/api/clusters/actions/delete/{clusterName}", repman.handlerMuxClusterDelete},

		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/servers", repman.handlerMuxServers},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/master", repman.handlerMuxMaster},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/slaves", repman.handlerMuxSlaves},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/logs", repman.handlerMuxLog},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/proxies", repman.handlerMuxProxies},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/alerts", repman.handlerMuxAlerts},
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/topology/crashes", repman.handlerMuxCrashes},

		// PROTECTED ENDPOINTS FOR TESTS
		{auth.ClusterPermission, config.GrantClusterTest, "/api/clusters/{clusterName}/tests/actions/run/all", repman.handlerMuxTests},
		{auth.ClusterPermission, config.GrantClusterTest, "/api/clusters/{clusterName}/tests/actions/run/{testName}", repman.handlerMuxOneTest},

		// PROTECTED ENDPOINTS FOR Cluster.DiffVariables
		{auth.ClusterPermission, config.GrantAny, "/api/clusters/{clusterName}/diffvariables", repman.handlerDiffVariables},

		{auth.ClusterPermission, config.GrantClusterUsers, "/api/clusters/{clusterName}/actions/user-add", repman.handlerMuxAddUser},
		{auth.ClusterPermission, config.GrantClusterUsers, "/api/clusters/{clusterName}/actions/user-drop/{username}", repman.handlerMuxDropClusterUser},
	}
}

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

func (repman *ReplicationManager) handlerMuxRotateKeys(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.KeyRotation()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxResetSla(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.SetEmptySla()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.MasterFailover(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterShardingAdd(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		repman.AddCluster(vars["clusterShardingName"], vars["clusterName"])
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.RollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.SetTraffic(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.SetTraffic(false)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		repman.bootstrapTopology(mycluster, vars["topology"])
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

func (repman *ReplicationManager) bootstrapTopology(mycluster *cluster.Cluster, topology string) {
	switch topology {
	case "master-slave":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoMasterSlave
	case "master-slave-no-gtid":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(true)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoMasterSlave
	case "multi-master":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(true)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoMultiMaster
	case "multi-tier-slave":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(true)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoMultiTierSlave
	case "maxscale-binlog":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(true)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoBinlogServer
	case "multi-master-ring":
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterRing(true)
		mycluster.SetMultiMasterWsrep(false)
		mycluster.Topology = config.TopoMultiMasterRing
	case "multi-master-wsrep":
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
		mycluster.SetMultiMasterRing(false)
		mycluster.SetMultiMasterWsrep(true)
		mycluster.Topology = config.TopoMultiMasterWsrep
	}
}

func (repman *ReplicationManager) handlerMuxServicesBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxServicesProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxServicesUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.Unprovision()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxServicesCancelRollingRestart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.CancelRollingRestart()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxServicesCancelRollingReprov(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.CancelRollingReprov()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxSetSettingsDiscover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.ResetFailoverCtr()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterBackups(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterShardClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterQueryRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterTop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }

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

func (repman *ReplicationManager) handlerMuxSwitchSettings(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		setting := vars["settingName"]
		mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "API receive switch setting %s", setting)
		repman.switchSettings(mycluster, setting)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) switchSettings(mycluster *cluster.Cluster, setting string) {
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
	case "cloud18":
		mycluster.SwitchCloud18()
	case "cloud18-shared":
		mycluster.SwitchCloud18Shared()
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
		mycluster.SwitchMailSmtpTlsSkipVerify()
	}
}

func (repman *ReplicationManager) handlerMuxSetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		setting := vars["settingName"]
		if valid, user := repman.IsValidClusterACL(r, mycluster); valid {
			//Set server scope
			if config.IsScope(setting, "server") {
				URL := strings.Replace(r.URL.Path, "/api/clusters/"+vars["clusterName"], "/api/clusters/%s", 1)
				mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Option '%s' is a shared values between clusters", setting)
				repman.setServerSetting(user, URL, setting, vars["settingValue"])
			} else {
				err := repman.setSetting(mycluster, setting, vars["settingValue"])
				if err != nil {
					http.Error(w, "Setting Not Found", 501)
					return
				}
			}
		} else {
			http.Error(w, fmt.Sprintf("User doesn't have required ACL for %s in cluster %s", setting, vars["clusterName"]), 403)
		}
		return
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) handlerMuxSetCron(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		setting := vars["settingName"]
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		cronValue, err := url.QueryUnescape(vars["settingValue"])
		if err != nil {
			http.Error(w, "Bad cron pattern", http.StatusBadRequest)
		}
		repman.setSetting(mycluster, setting, cronValue)
		return
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func (repman *ReplicationManager) setSetting(mycluster *cluster.Cluster, name string, value string) error {
	//not immutable
	if !mycluster.IsVariableImmutable(name) {
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
		mycluster.SetLogGitLevel(val)
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
		mycluster.SetApiTokenTimeout(val)
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
		mycluster.SetMailSmtpAddr(value)
	case "mail-smtp-password":
		mycluster.SetMailSmtpPassword(value)
	case "mail-smtp-user":
		mycluster.SetMailSmtpUser(value)
	case "scheduler-alert-disable-time":
		val, _ := strconv.Atoi(value)
		mycluster.SetSchedulerAlertDisableTime(val)
	default:
		return errors.New("Setting not found")
	}
	return nil
}

func (repman *ReplicationManager) setServerSetting(user string, URL string, name string, value string) {
	for cname, cl := range repman.Clusters {
		//Don't print error with no valid ACL
		if cl.IsURLPassACL(user, fmt.Sprintf(URL, cname), false) {
			repman.setSetting(cl, name, value)
		}
	}
}

func (repman *ReplicationManager) handlerMuxAddTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.AddDBTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxAddProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxDropTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.DropDBTag(vars["tagValue"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxDropProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.SwitchReadOnly()
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

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

func (repman *ReplicationManager) handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }

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

func (repman *ReplicationManager) handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	repman.InitConfig(repman.Conf)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		//mycluster.ReloadConfig(repman.Confs[vars["clusterName"]])
		mycluster.ReloadConfig(mycluster.Conf)
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

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

func (repman *ReplicationManager) handlerMuxServerDrop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

// swagger:operation GET /api/clusters/{clusterName}/status clusterStatus
// Shows the status for that specific named cluster
//
// ---
// parameters:
//   - name: clusterName
//     in: path
//     description: cluster to filter by
//     required: true
//     type: string
//
// responses:
//
//	'200':
//	  "$ref": "#/responses/status"
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

// swagger:operation GET /api/clusters/{clusterName}/actions/master-physical-backup master-physical-backup
//
//
// ---
// parameters:
// - name: clusterName
//   in: path
//   description: cluster to filter by
//   required: true
//   type: string
// produces:
//  - text/plain
// responses:
//   '200':
//     description: OK
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '400':
//     description: No cluster found
//     schema:
//       type: string
//     examples:
//       text/plain: No cluster found:cluster_1
//     headers:
//       Access-Control-Allow-Origin:
//         type: string
//   '403':
//     description: No valid ACL
//     schema:
//       type: string
//     examples:
//       text/plain: No valid ACL
//     headers:
//       Access-Control-Allow-Origin:
//         type: string

func (repman *ReplicationManager) handlerMuxClusterMasterPhysicalBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		w.WriteHeader(http.StatusOK)
		mycluster.GetMaster().JobBackupPhysical()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func (repman *ReplicationManager) handlerMuxClusterOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		mycluster.SSTCloseReceiver(port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func (repman *ReplicationManager) handlerMuxClusterSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		if r.URL.Query().Get("threads") != "" {
			mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Sysbench threads to %s", r.URL.Query().Get("threads"))
			mycluster.SetSysbenchThreads(r.URL.Query().Get("threads"))
		}
		go mycluster.RunSysbench()
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterApplyDynamicConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.SetDBDynamicConfig()
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterReloadCertificates(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.ReloadCertificates()
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterWaitDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		err := mycluster.WaitDatabaseCanConn()
		if err != nil {
			http.Error(w, err.Error(), 403)
			return
		}
	}
	return
}

func (repman *ReplicationManager) handlerMuxCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterShared(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.Conf.Cloud18Shared)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}

}

func (repman *ReplicationManager) handlerMuxClusterSendVaultToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.SendVaultTokenByMail(mycluster.Conf)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumAllTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.CheckAllTableChecksum()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.CheckTableChecksum(vars["schemaName"], vars["tableName"])
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return

}

func (repman *ReplicationManager) handlerMuxClusterSchemaUniversalTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterSchemaReshardTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterSchemaMoveTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerMuxClusterSchema(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerDiffVariables(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
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

func (repman *ReplicationManager) handlerRotatePasswords(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		go mycluster.RotatePasswords()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

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

func (repman *ReplicationManager) handlerMuxClusterGetJobEntries(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		// Not used anymore
		// if valid, _ := repman.IsValidClusterACL(r, mycluster); !valid {
		// 	http.Error(w, "No valid ACL", 403)
		// 	return
		// }
		entries, _ := mycluster.JobsGetEntries()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(entries)
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
}
