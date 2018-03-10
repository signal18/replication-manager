// +build server

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
	"github.com/signal18/replication-manager/opensvc"
	log "github.com/sirupsen/logrus"
)

type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

func testFile(fn string) error {

	f, err := os.Open(conf.HttpRoot + "/" + fn)
	if err != nil {
		log.Printf("error no file %s", conf.HttpRoot+"/"+fn)
		return err
	}
	f.Close()
	return nil
}

func httpserver() {

	// before starting the http server, check that the dashboard is present
	if err := testFile("app.html"); err != nil {
		RepMan.currentCluster.LogPrintf("ERROR", "Dashboard app.html file missing - will not start http server %s", err)
		return
	}

	initKeys()
	//PUBLIC ENDPOINTS
	router := mux.NewRouter()
	router.HandleFunc("/", handlerApp)
	// page to view which does not need authorization
	router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
	router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
	router.HandleFunc("/api/login", loginHandler)
	router.Handle("/api/clusters", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusters)),
	))
	router.Handle("/api/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxStatus)),
	))
	router.Handle("/api/timeout", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxTimeout)),
	))
	router.Handle("/api/clusters/{clusterName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterStatus)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/master-physical-backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterMasterPhysicalBackup)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/processlist", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerProcesslist)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/variables", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/errorlog", negroni.New(

		negroni.Wrap(http.HandlerFunc(handlerMuxServerErrorLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/slowlog", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/tables", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerTables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/schemas", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerSchemas)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/innodb-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerInnoDBStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/all-slaves-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerAllSlavesStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServerMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsSlaveStatus)),
	))
	// handle API 2.0 compatibility for external checks
	router.Handle("/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsSlaveStatus)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortBackup)),
	))

	//PROTECTED ENDPOINTS FOR SETTINGS
	router.Handle("/api/monitor", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxReplicationManager)),
	))

	router.Handle("/api/clusters/{clusterName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxCluster)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/reload", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSetSettings)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS ACTIONS

	router.Handle("/api/clusters/{clusterName}/actions/reset-failover-control", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterResetFailoverControl)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/switchover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/failover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxFailover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxBootstrapReplication)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/cleanup", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxBootstrapReplicationCleanup)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServicesProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServicesUnprovision)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStopTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStartTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/sysbench", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterSysbench)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/actions/add/{clusterName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterAdd)),
	))

	router.Handle("/api/clusters/{clusterName}/topology/servers", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/master", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxMaster)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSlaves)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/logs", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/proxies", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxies)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/alerts", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxAlerts)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/crashes", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxCrashes)),
	))

	//PROTECTED ENDPOINTS FOR TESTS

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/all", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxTests)),
	))
	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxOneTest)),
	))

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxOneTest)),
	))

	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerAdd)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/start", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerStart)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/stop", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerStop)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/maintenance", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerMaintenance)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerProvision)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-physical", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupPhysical)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-error-log", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupErrorLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-slowquery-log", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupSlowQueryLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/action/toogle-innodb-monitor", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSetInnoDBMonitor)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/action/skip-replication-eventr", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSkipReplicationEvent)),
	))
	//PROTECTED ENDPOINTS FOR PROXIES

	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyProvision)),
	))

	// create mux router
	router.HandleFunc("/repocomp/current", handlerRepoComp)
	router.HandleFunc("/heartbeat", handlerHeartbeat)
	router.HandleFunc("/template", handlerOpenSVCTemplate)

	if confs[currentClusterName].Verbose {
		log.Printf("Starting http monitor on port " + confs[currentClusterName].HttpPort)
	}
	log.Fatal(http.ListenAndServe(confs[currentClusterName].BindAddr+":"+confs[currentClusterName].HttpPort, router))
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, confs[currentClusterName].HttpRoot+"/app.html")
}

func handlerRepoComp(w http.ResponseWriter, r *http.Request) {

	data, err := ioutil.ReadFile(string(conf.ShareDir + "/opensvc/current"))

	if err != nil {
		w.WriteHeader(404)
		w.Write([]byte("404 Something went wrong - " + http.StatusText(404)))
		return
	}
	w.Write(data)

}

func handlerAgents(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(RepMan.Agents)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerOpenSVCTemplate(w http.ResponseWriter, r *http.Request) {
	svc := RepMan.currentCluster.OpenSVCConnect()
	servers := RepMan.currentCluster.GetServers()
	var iplist []string
	var portlist []string
	for _, s := range servers {
		iplist = append(iplist, s.Host)
		portlist = append(portlist, s.Port)

	}

	agts := svc.GetNodes()
	var clusteragents []opensvc.Host

	for _, node := range agts {
		RepMan.currentCluster.LogPrintf("INFO", "hypervisors for cluster: %s %s", svc.ProvAgents, node.Node_name)
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			RepMan.currentCluster.LogPrintf("INFO", "hypervisors Found")

			clusteragents = append(clusteragents, node)
		}
	}
	res, err := RepMan.currentCluster.GetServers()[0].GenerateDBTemplate(svc, iplist, portlist, clusteragents, "", svc.ProvAgents)
	if err != nil {
		log.Println("HTTP Error ", err)
		http.Error(w, "Encoding error", 500)
		return
	}

	w.Write([]byte(res))
}

func handlerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var send heartbeat
	send.UUID = RepMan.UUID
	send.UID = conf.ArbitrationSasUniqueId
	send.Secret = conf.ArbitrationSasSecret
	send.Status = RepMan.Status
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}
}

func handlerLog(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	values := r.URL.Query()
	off := values.Get("offset")
	if off == "" {
		off = "1000"
	}
	noff, _ := strconv.Atoi(off)
	err := e.Encode(RepMan.Logs.Buffer[:noff])
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}
