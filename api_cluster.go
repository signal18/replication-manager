// +build server

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/regtest"
)

func handlerMuxServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := RepMan.getClusterByName(vars["clusterName"])

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

func handlerMuxSlaves(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
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

func handlerMuxProxies(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
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

func handlerMuxAlerts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	a := new(cluster.Alerts)
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
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

func handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.MasterFailover(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SetTraffic(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SetTraffic(false)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {

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
		err := mycluster.BootstrapReplication()
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

func handlerMuxServicesBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxServicesProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxServicesUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.Unprovision()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxClusterResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.ResetFailoverCtr()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxMaster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
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

func handlerMuxSwitchSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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
		case "autorejoin-backup-binlog":
			mycluster.SwitchRejoinBackupBinlog()
		case "autorejoin-flashback":
			mycluster.SwitchRejoinFlashback()
		case "autorejoin-flashback-on-sync":
			mycluster.SwitchRejoinSemisync()
		case "autorejoin-flashback-on-unsync": //?????
		case "autorejoin-slave-positional-hearbeat":
			mycluster.SwitchRejoinPseudoGTID()
		case "autorejoin-zfs-flashback":
			mycluster.SwitchRejoinZFSFlashback()
		case "autorejoin-mysqldump":
			mycluster.SwitchRejoinDump()
		case "switchover-at-sync":
			mycluster.SwitchSwitchoverSync()
		case "check-replication-filters":
			mycluster.SwitchCheckReplicationFilters()
		case "check-replication-state":
			mycluster.SwitchRplChecks()
		case "scheduler-db-servers-logical-backup":
			mycluster.SwitchSchedulerBackupLogical()
		case "scheduler-db-servers-logs":
			mycluster.SwitchSchedulerDatabaseLogs()
		case "scheduler-db-servers-optimize":
			mycluster.SwitchSchedulerDatabaseOptimize()
		case "scheduler-db-servers-physical-backup":
			mycluster.SwitchSchedulerBackupPhysical()
		case "graphite-metrics":
			mycluster.SwitchGraphiteMetrics()
		case "graphite-embedded":
			mycluster.SwitchGraphiteEmbedded()
		case "shardproxy-copy-grants":
		case "monitoring-queries":
			mycluster.SwitchMonitoringQueries()
		case "monitoring-scheduler":
			mycluster.SwitchMonitoringScheduler()
		case "monitoring-schema-change":
			mycluster.SwitchMonitoringSchemaChange()
		case "proxysql-copy-grants":
			mycluster.SwitchProxysqlCopyGrants()
		case "proxysql-bootstrap":
			mycluster.SwitchProxysqlBootstrap()
		case "database-hearbeat":
			mycluster.SwitchTraffic()
		case "test":
			mycluster.SwitchTestMode()
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSetSettings(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		setting := vars["settingName"]
		mycluster.LogPrintf("INFO", "API receive switch setting %s", setting)
		switch setting {
		case "failover-max-slave-delay":
			val, _ := strconv.ParseInt(vars["settingValue"], 10, 64)
			mycluster.SetRplMaxDelay(val)
		case "failover-limit":
			val, _ := strconv.Atoi(vars["settingValue"])
			mycluster.SetFailLimit(val)
		case "backup-keep-hourly":
		case "backup-keep-daily":
		case "backup-keep-monthly":
		case "backup-keep-weekly":
		case "backup-keep-yearly":
		case "db-servers-hosts":
		case "db-servers-credential":
			mycluster.SetClusterCredential(vars["settingValue"])
		case "replication-credential":
			mycluster.SetReplicationCredential(vars["settingValue"])
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSwitchReadOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchReadOnly()
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}
	return
}

func handlerMuxLog(w http.ResponseWriter, r *http.Request) {
	var clusterlogs []string
	vars := mux.Vars(r)
	for _, slog := range RepMan.tlog.Buffer {
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

func handlerMuxCrashes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
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

func handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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

func handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		initConfig()
		mycluster.ReloadConfig(confs[vars["clusterName"]])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

func handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.LogPrintf(cluster.LvlInfo, "Rest API receive new server to be added %s", vars["host"]+":"+vars["port"])
		mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
	} else {
		http.Error(w, "Cluster Not Found", 500)
		return
	}

}

func handlerMuxClusterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if mycluster.GetStatus() {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "errors"}`)
	}
}

func handlerMuxClusterMasterPhysicalBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		mycluster.GetMaster().JobBackupPhysical()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func handlerMuxClusterOptimize(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		w.WriteHeader(http.StatusOK)
		mycluster.Optimize()
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func handlerMuxClusterSSTStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	port, err := strconv.Atoi(vars["port"])
	w.WriteHeader(http.StatusOK)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if mycluster != nil {
		mycluster.SSTCloseReceiver(port)
	} else {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, "No cluster found:"+vars["clusterName"])
	}
}

func handlerMuxClusterSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		go mycluster.RunSysbench()
	}
	return
}

func handlerMuxCluster(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	vars := mux.Vars(r)
	mycluster := RepMan.getClusterByName(vars["clusterName"])
	if mycluster != nil {
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
