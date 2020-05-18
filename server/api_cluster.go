// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/codegangsta/negroni"
	log "github.com/sirupsen/logrus"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/regtest"
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
	router.Handle("/api/clusters/{clusterName}/shardclusters", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterShardClusters)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/reload", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSetSettings)),
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
	router.Handle("/api/clusters/{clusterName}/actions/rotatekeys", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRotateKeys)),
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

	router.Handle("/api/clusters/{clusterName}/actions/rolling", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxRolling)),
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

	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/actions/add/{clusterName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/topology/servers", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/master", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxMaster)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSlaves)),
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

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxOneTest)),
	))
}

func (repman *ReplicationManager) handlerMuxServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])

	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetServers())
		var srvs []*cluster.ServerMonitor

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}

		for i := range srvs {
			if srvs[i] != nil {
				srvs[i].Pass = "XXXXXXXX"
			}
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srvs)
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(prxs)
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxResetSla(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterShardingAdd(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			repman.AddCluster(vars["clusterShardingName"], vars["clusterName"])
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

func (repman *ReplicationManager) handlerMuxRolling(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {

		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.BootstrapReplicationCleanup()
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error Cleanup Replication: %s", err)
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		switch vars["topology"] {
		case "master-slave":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "master-slave-no-gtid":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(true)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(true)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-tier-slave":
			mycluster.SetMultiTierSlave(true)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "maxscale-binlog":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(true)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master-ring":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterRing(true)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master-wsrep":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterRing(false)
			mycluster.SetMultiMasterWsrep(true)

		}
		err := mycluster.BootstrapReplication(true)
		mycluster.LogPrintf("ERROR", "Error bootstraping replication %", err)
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

func (repman *ReplicationManager) handlerMuxServicesBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.ProvisionServices()
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error Bootstrap Micro Services: ", err)
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		err := mycluster.Bootstrap()
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error Bootstrap Micro Services + replication ", err)
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
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.LogPrintf(cluster.LvlInfo, "Rest API receive switchover request")
		savedPrefMaster := mycluster.GetConf().PrefMaster
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if mycluster.IsMasterFailed() {
			mycluster.LogPrintf(cluster.LvlErr, "Master failed, cannot initiate switchover")
			http.Error(w, "Master failed", http.StatusBadRequest)
			return
		}
		r.ParseForm() // Parses the request body
		newPrefMaster := r.Form.Get("prefmaster")
		mycluster.LogPrintf(cluster.LvlInfo, "Was ask for prefered master: %s", newPrefMaster)
		if mycluster.IsInHostList(newPrefMaster) {
			mycluster.SetPrefMaster(newPrefMaster)
		} else {
			mycluster.LogPrintf(cluster.LvlInfo, "Prefered master: not found in database servers %s", newPrefMaster)
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
				mycluster.LogPrintf(cluster.LvlErr, "API Error decoding JSON: ", err)
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
		err := e.Encode(mycluster.GetClientCertificates())
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
		err := e.Encode(mycluster.GetDBModuleTags())
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
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterShardClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterQueryRules(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxSwitchSettings(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		setting := vars["settingName"]
		mycluster.LogPrintf("INFO", "API receive switch setting %s", setting)
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
		case "failover-event-status":
			mycluster.SwitchFailoverEventStatus()
		case "failover-event-scheduler":
			mycluster.SwitchFailoverEventScheduler()
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
		case "graphite-metrics":
			mycluster.SwitchGraphiteMetrics()
		case "graphite-embedded":
			mycluster.SwitchGraphiteEmbedded()
		case "shardproxy-copy-grants":
			mycluster.SwitchProxysqlCopyGrants()
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
		case "proxy-servers-backend-compression":
			mycluster.SwitchProxyServersBackendCompression()
		case "database-hearbeat":
			mycluster.SwitchTraffic()
		case "test":
			mycluster.SwitchTestMode()
		case "prov-net-cni":
			mycluster.SwitchProvNetCNI()
		case "prov-docker-daemon-private":
			mycluster.SwitchProvDockerDaemonPrivate()
		case "backup-restic":
			mycluster.SwitchBackupRestic()
		case "backup-binlogs":
			mycluster.SwitchBackupBinlogs()

		}

	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxSetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		setting := vars["settingName"]
		mycluster.LogPrintf("INFO", "API receive set setting %s", setting)
		switch setting {
		case "replication-credential":
			mycluster.SetReplicationCredential(vars["settingValue"])
		case "failover-max-slave-delay":
			val, _ := strconv.ParseInt(vars["settingValue"], 10, 64)
			mycluster.SetRplMaxDelay(val)
		case "switchover-wait-route-change":
			mycluster.SetSwitchoverWaitRouteChange(vars["settingValue"])
		case "failover-limit":
			val, _ := strconv.Atoi(vars["settingValue"])
			mycluster.SetFailLimit(val)
		case "backup-keep-hourly":
			mycluster.SetBackupKeepHourly(vars["settingValue"])
		case "backup-keep-daily":
			mycluster.SetBackupKeepDaily(vars["settingValue"])
		case "backup-keep-monthly":
			mycluster.SetBackupKeepMonthly(vars["settingValue"])
		case "backup-keep-weekly":
			mycluster.SetBackupKeepWeekly(vars["settingValue"])
		case "backup-keep-yearly":
			mycluster.SetBackupKeepYearly(vars["settingValue"])
		case "backup-logical-type":
			mycluster.SetBackupLogicalType(vars["settingValue"])
		case "backup-physical-type":
			mycluster.SetBackupPhysicalType(vars["settingValue"])
		case "db-servers-hosts":
			mycluster.SetDbServerHosts(vars["settingValue"])
		case "db-servers-credential":
			mycluster.SetDbServersCredential(vars["settingValue"])
		case "prov-service-plan":
			mycluster.SetServicePlan(vars["settingValue"])
		case "prov-net-cni-cluster":
			mycluster.SetProvNetCniCluster(vars["settingValue"])
		case "prov-db-disk-size":
			mycluster.SetDBDiskSize(vars["settingValue"])
		case "prov-db-cpu-cores":
			mycluster.SetDBCores(vars["settingValue"])
		case "prov-db-memory":
			mycluster.SetDBMemorySize(vars["settingValue"])
		case "prov-db-disk-iops":
			mycluster.SetDBDiskIOPS(vars["settingValue"])
		case "prov-db-max-connections":
			mycluster.SetDBMaxConnections(vars["settingValue"])
		case "prov-db-agents":
			mycluster.SetProvDbAgents(vars["settingValue"])
		case "prov-proxy-agents":
			mycluster.SetProvProxyAgents(vars["settingValue"])
		case "prov-orchestrator":
			mycluster.SetProvOrchestrator(vars["settingValue"])
		case "prov-sphinx-img":
			mycluster.SetProvSphinxImage(vars["settingValue"])
		case "prov-db-image":
			mycluster.SetProvDBImage(vars["settingValue"])
		case "prov-db-disk-type":
			mycluster.SetProvDbDiskType(vars["settingValue"])
		case "prov-db-disk-fs":
			mycluster.SetProvDbDiskFS(vars["settingValue"])
		case "prov-db-disk-pool":
			mycluster.SetProvDbDiskPool(vars["settingValue"])
		case "prov-db-disk-device":
			mycluster.SetProvDbDiskDevice(vars["settingValue"])
		case "prov-db-service-type":
			mycluster.SetProvDbServiceType(vars["settingValue"])
		case "proxysql-servers-credential":
			mycluster.SetProxyServersCredential(vars["settingValue"], config.ConstProxySqlproxy)
		case "proxy-servers-backend-max-connections":
			mycluster.SetProxyServersBackendMaxConnections(vars["settingValue"])
		case "proxy-servers-backend-max-replication-lag":
			mycluster.SetProxyServersBackendMaxReplicationLag(vars["settingValue"])
		case "maxscale-servers-credential":
			mycluster.SetProxyServersCredential(vars["settingValue"], config.ConstProxyMaxscale)
		case "shardproxy-servers-credential":
			mycluster.SetProxyServersCredential(vars["settingValue"], config.ConstProxySpider)
		case "prov-proxy-disk-size":
			mycluster.SetProxyDiskSize(vars["settingValue"])
		case "prov-proxy-cpu-cores":
			mycluster.SetProxyCores(vars["settingValue"])
		case "prov-proxy-memory":
			mycluster.SetProxyMemorySize(vars["settingValue"])
		case "prov-proxy-docker-proxysql-img":
			mycluster.SetProvProxySQLImage(vars["settingValue"])
		case "prov-proxy-docker-maxscale-img":
			mycluster.SetProvMaxscaleImage(vars["settingValue"])
		case "prov-proxy-docker-haproxy-img":
			mycluster.SetProvHaproxyImage(vars["settingValue"])
		case "prov-proxy-docker-shardproxy-img":
			mycluster.SetProvShardproxyImage(vars["settingValue"])
		case "prov-proxy-disk-type":
			mycluster.SetProvProxyDiskType(vars["settingValue"])
		case "prov-proxy-disk-fs":
			mycluster.SetProvProxyDiskFS(vars["settingValue"])
		case "prov-proxy-disk-pool":
			mycluster.SetProvProxyDiskPool(vars["settingValue"])
		case "prov-proxy-disk-device":
			mycluster.SetProvProxyDiskDevice(vars["settingValue"])
		case "prov-proxy-service-type":
			mycluster.SetProvProxyServiceType(vars["settingValue"])
		case "monitoring-address":
			mycluster.SetMonitoringAddress(vars["settingValue"])
		case "scheduler-db-servers-logical-backup-cron":
			mycluster.SetSchedulerDbServersLogicalBackupCron(vars["settingValue"])
		case "scheduler-db-servers-logs-cron":
			mycluster.SetSchedulerDbServersLogsCron(vars["settingValue"])
		case "scheduler-db-servers-logs-table-rotate-cron":
			mycluster.SetSchedulerDbServersLogsTableRotateCron(vars["settingValue"])
		case "scheduler-db-servers-optimize-cron":
			mycluster.SetSchedulerDbServersOptimizeCron(vars["settingValue"])
		case "scheduler-db-servers-physical-backup-cron":
			mycluster.SetSchedulerDbServersPhysicalBackupCron(vars["settingValue"])
		case "scheduler-rolling-reprov-cron":
			mycluster.SetSchedulerRollingReprovCron(vars["settingValue"])
		case "scheduler-rolling-restart-cron":
			mycluster.SetSchedulerRollingRestartCron(vars["settingValue"])
		case "scheduler-sla-rotate-cron":
			mycluster.SetSchedulerSlaRotateCron(vars["settingValue"])
		case "scheduler-jobs-ssh-cron":
			mycluster.SetSchedulerJobsSshCron(vars["settingValue"])
		case "backup-binlogs-keep":
			mycluster.SetBackupBinlogsKeep(vars["settingValue"])

		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func (repman *ReplicationManager) handlerMuxAddTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxAddProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.AddProxyTag(vars["tagValue"])
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
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxDropProxyTag(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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
		if !repman.IsValidClusterACL(r, mycluster) {
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
		if !repman.IsValidClusterACL(r, mycluster) {
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
		regtest := new(regtest.RegTest)
		res := regtest.RunAllTests(mycluster, vars["testName"])
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")

		if len(res) > 0 {
			err := e.Encode(res[0])
			if err != nil {
				mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
				mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		regtest := new(regtest.RegTest)

		res := regtest.RunAllTests(mycluster, "ALL")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(res)
		if err != nil {
			mycluster.LogPrintf(cluster.LvlErr, "API Error encoding JSON: ", err)
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
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		repman.InitConfig(repman.Conf)
		mycluster.ReloadConfig(repman.Confs[vars["clusterName"]])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

func (repman *ReplicationManager) handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		mycluster.LogPrintf(cluster.LvlInfo, "Rest API receive new %s monitor to be added %s", vars["type"], vars["host"]+":"+vars["port"])
		if vars["type"] == "" {
			mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
		} else {
			if mycluster.MonitorType[vars["type"]] == "proxy" {
				mycluster.AddSeededProxy(vars["type"], vars["host"], vars["port"], "", "")
			} else if mycluster.MonitorType[vars["type"]] == "database" {
				switch vars["type"] {
				case "mariadb":
					mycluster.Conf.ProvDbImg = "mariadb:latest"
				case "percona":
					mycluster.Conf.ProvDbImg = "percona:latest"
				case "mysql":
					mycluster.Conf.ProvDbImg = "mysql:latest"
				}
				mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
			}
		}
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

func (repman *ReplicationManager) handlerMuxClusterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if mycluster.GetStatus() {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "errors"}`)
	}
}

func (repman *ReplicationManager) handlerMuxClusterMasterPhysicalBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		go mycluster.RunSysbench()
	}
	return
}

func (repman *ReplicationManager) handlerMuxClusterWaitDatabases(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
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
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumAllTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterSchemaChecksumTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
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

func (repman *ReplicationManager) handlerMuxClusterSchemaUniversalTable(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pr := range mycluster.Proxies {
			if mycluster.Conf.MdbsProxyOn {
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pr := range mycluster.Proxies {
			if mycluster.Conf.MdbsProxyOn {
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
		if !repman.IsValidClusterACL(r, mycluster) {
			http.Error(w, "No valid ACL", 403)
			return
		}
		for _, pr := range mycluster.Proxies {
			if mycluster.Conf.MdbsProxyOn {
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
		if !repman.IsValidClusterACL(r, mycluster) {
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
