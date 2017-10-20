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
	"fmt"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/regtest"
	log "github.com/sirupsen/logrus"
)

type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

func httpserver() {

	// before starting the http server, check that the dashboard is present
	if err := testFile("app.html"); err != nil {
		currentCluster.LogPrintf("ERROR", "Dashboard app.html file missing - will not start http server %s", err)
		return
	}
	if err := testFile("dashboard.js"); err != nil {
		currentCluster.LogPrintf("ERROR", "Dashboard dashboard.js file missing - will not start http server")
		return
	}
	// create mux router
	router := mux.NewRouter()

	if confs[currentClusterName].HttpAuth {
		// set authguard options
		agOptions := authguard.Options{
			Attempts:              3,
			LockoutDuration:       30,
			MaxLockouts:           3,
			BanDuration:           60,
			AttemptsResetDuration: 30,
			LockoutsResetDuration: 30,
			BindMethod:            authguard.BindToUsernameAndIP,
			SyncAfter:             10,
			// Exceptions:            []string{"192.168.1.1"},
			// Store:                 "users.gob",
			// ProxyIPHeaderName:     "X-Real-IP",
			Store:          "::memory::",
			LogLevel:       authguard.LogLevelNone,
			LogDestination: os.Stdout,
		}

		// get authguard
		ag, err := authguard.New(agOptions)
		if err != nil {
			log.Printf("auth guard init error: %v\n", err)
			return
		}

		// create session keys
		sessionKeys := [][]byte{
			[]byte("261AD9502C583BD7D8AA03083598653B"),
			[]byte("E9F6FDFAC2772D33FC5C7B3D6E4DDAFF"),
		}

		// create exception for "no auth" zone
		exceptions := []string{"/noauth/.*"}

		// set options
		options := gelada.Options{
			Path:     "/",
			MaxAge:   confs[currentClusterName].SessionLifeTime, // 60 seconds
			HTTPOnly: true,

			SessionName:     "test-session",
			SessionLifeTime: confs[currentClusterName].SessionLifeTime, // 60 seconds
			SessionKeys:     sessionKeys,

			BindUserAgent: true,
			BindUserHost:  true,

			LoginUserFieldName:     "login",
			LoginPasswordFieldName: "password",
			LoginRoute:             "/login",
			LogoutRoute:            "/logout",

			AuthProvider: checkAuth,

			Exceptions: exceptions,

			AuthGuard: ag,
		}

		// get Gelada
		g, err := gelada.New(options)

		if err != nil {
			log.Printf("gelada init error: %v\n", err)
			return
		}

		// create handler manager
		hm := &HandlerManager{
			Gelada:    g,
			AuthGuard: ag,
		}
		router.Handle("/", g.GlobalAuth(router))
		router.HandleFunc("/", hm.HandleMainPage)

		router.HandleFunc("/noauth/page", hm.HandleLoginFreePage)
		// login page
		router.HandleFunc("/login", hm.HandleLoginPage).Methods("GET")
		// function for processing a request for authorization (via POST method)
		router.HandleFunc("/login", g.AuthHandler).Methods("POST")
		// function for processing a request for logout (via POST method)
		router.HandleFunc("/stats", handlerStats)
		//http.HandleFunc("/", handlerApp)
		router.HandleFunc("/logout", g.LogoutHandler).Methods("POST")
	} else {
		router.HandleFunc("/", handlerApp)
	}
	// main page

	// page to view which does not need authorization

	router.HandleFunc("/servers", handlerServers)
	router.HandleFunc("/stop", handlerStopServer)
	router.HandleFunc("/start", handlerStartServer)
	router.HandleFunc("/maintenance", handlerMaintenanceServer)
	router.HandleFunc("/setcluster", handlerSetCluster)
	router.HandleFunc("/runonetest", handlerSetOneTest)
	router.HandleFunc("/master", handlerMaster)
	router.HandleFunc("/slaves", handlerSlaves)
	router.HandleFunc("/agents", handlerAgents)
	router.HandleFunc("/proxies", handlerProxies)
	router.HandleFunc("/crashes", handlerCrashes)
	router.HandleFunc("/log", handlerLog)
	router.HandleFunc("/switchover", handlerSwitchover)
	router.HandleFunc("/failover", handlerFailover)
	router.HandleFunc("/interactive", handlerInteractiveToggle)
	router.HandleFunc("/settings", handlerSettings)
	router.HandleFunc("/alerts", handlerAlerts)
	router.HandleFunc("/resetfail", handlerResetFailoverCtr)
	router.HandleFunc("/rplchecks", handlerRplChecks)
	router.HandleFunc("/bootstrap", handlerBootstrap)
	router.HandleFunc("/failsync", handlerFailSync)
	router.HandleFunc("/switchsync", handlerSwitchSync)
	router.HandleFunc("/setrejoin", handlerRejoin)
	router.HandleFunc("/setrejoinbackupbinlog", handlerRejoinBackupBinlog)
	router.HandleFunc("/setrejoinsemisync", handlerRejoinSemisync)
	router.HandleFunc("/setrejoinflashback", handlerRejoinFlashback)
	router.HandleFunc("/setrejoindump", handlerRejoinDump)
	router.HandleFunc("/setrejoinpseudogtid", handlerRejoinPseudoGTID)
	router.HandleFunc("/settest", handlerSetTest)
	router.HandleFunc("/tests", handlerTests)
	router.HandleFunc("/sysbench", handlerSysbench)
	router.HandleFunc("/setactive", handlerSetActive)
	router.HandleFunc("/dashboard.js", handlerJS)
	router.HandleFunc("/heartbeat", handlerMrmHeartbeat)
	router.HandleFunc("/setverbosity", handlerVerbosity)
	router.HandleFunc("/template", handlerOpenSVCTemplate)
	router.HandleFunc("/repocomp/current", handlerRepoComp)
	router.HandleFunc("/unprovision", handlerUnprovision)
	router.HandleFunc("/rolling", handlerRollingUpgrade)
	router.HandleFunc("/toggletraffic", handlerTraffic)
	router.Handle("/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersSlaveStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortSlaveStatus)),
	))

	router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))

	if confs[currentClusterName].Verbose {
		log.Printf("Starting http monitor on port " + confs[currentClusterName].HttpPort)
	}
	log.Fatal(http.ListenAndServe(confs[currentClusterName].BindAddr+":"+confs[currentClusterName].HttpPort, router))
}

func handlerSetCluster(w http.ResponseWriter, r *http.Request) {
	mycluster := r.URL.Query().Get("cluster")
	currentCluster = clusters[mycluster]
	currentClusterName = mycluster
	for _, gl := range cfgGroupList {
		clusters[gl].SetCfgGroupDisplay(mycluster)
	}
}

func handlerSetOneTest(w http.ResponseWriter, r *http.Request) {
	regtest := new(regtest.RegTest)
	regtest.RunAllTests(currentCluster, r.URL.Query().Get("test"))
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, confs[currentClusterName].HttpRoot+"/app.html")
}

func handlerJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, confs[currentClusterName].HttpRoot+"/dashboard.js")
}

func handlerServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	data, _ := json.Marshal(currentCluster.GetServers())
	var srvs []*cluster.ServerMonitor

	err := json.Unmarshal(data, &srvs)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
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
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerCrashes(w http.ResponseWriter, r *http.Request) {

	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(currentCluster.GetCrashes())
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
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

func handlerTraffic(w http.ResponseWriter, r *http.Request) {

	currentCluster.SetTraffic(!currentCluster.GetTraffic())

}

func handlerStopServer(w http.ResponseWriter, r *http.Request) {
	currentCluster.LogPrintf("INFO", "Rest API request stop server-id: %s", r.URL.Query().Get("server"))
	srv := r.URL.Query().Get("server")

	node := currentCluster.GetServerFromName(srv)
	currentCluster.StopDatabaseService(node)
}

func handlerStartServer(w http.ResponseWriter, r *http.Request) {
	currentCluster.LogPrintf("INFO", "Rest API request start server-id: %s", r.URL.Query().Get("server"))
	srv := r.URL.Query().Get("server")

	node := currentCluster.GetServerFromName(srv)
	currentCluster.StartDatabaseService(node)
}

func handlerMaintenanceServer(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Rest API request start server-id: %s", r.URL.Query().Get("server"))
	srv := r.URL.Query().Get("server")
	node := currentCluster.GetServerFromName(srv)
	if node != nil {
		currentCluster.SwitchServerMaintenance(node.ServerID)
	}
}

func handlerUnprovision(w http.ResponseWriter, r *http.Request) {
	currentCluster.LogPrintf("INFO", "Rest API request unprovision cluster: %s", currentCluster.GetName())
	currentCluster.Unprovision()
}

func handlerRollingUpgrade(w http.ResponseWriter, r *http.Request) {
	currentCluster.LogPrintf("INFO", "Rest API request rolling upgrade cluster: %s", currentCluster.GetName())
	currentCluster.RollingUpgrade()
}

func handlerSlaves(w http.ResponseWriter, r *http.Request) {
	data, _ := json.Marshal(currentCluster.GetSlaves())
	var srvs []*cluster.ServerMonitor

	err := json.Unmarshal(data, &srvs)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
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
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerAgents(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(agents)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}
func handlerOpenSVCTemplate(w http.ResponseWriter, r *http.Request) {
	svc := currentCluster.OpenSVCConnect()
	servers := currentCluster.GetServers()
	var iplist []string
	var portlist []string
	for _, s := range servers {
		iplist = append(iplist, s.Host)
		portlist = append(portlist, s.Port)

	}

	agts := svc.GetNodes()
	var clusteragents []opensvc.Host

	for _, node := range agts {
		currentCluster.LogPrintf("INFO", "hypervisors for cluster: %s %s", svc.ProvAgents, node.Node_name)
		if strings.Contains(svc.ProvAgents, node.Node_name) {
			currentCluster.LogPrintf("INFO", "hypervisors Found")

			clusteragents = append(clusteragents, node)
		}
	}
	res, err := currentCluster.GetServers()[0].GenerateDBTemplate(svc, iplist, portlist, clusteragents, "", svc.ProvAgents)
	if err != nil {
		log.Println("HTTP Error ", err)
		http.Error(w, "Encoding error", 500)
		return
	}

	w.Write([]byte(res))
}

func handlerProxies(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(currentCluster.GetProxies())
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMaster(w http.ResponseWriter, r *http.Request) {
	m := currentCluster.GetMaster()
	var srvs *cluster.ServerMonitor
	if m != nil {

		data, _ := json.Marshal(m)

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		srvs.Pass = "XXXXXXXX"
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(srvs)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerAlerts(w http.ResponseWriter, r *http.Request) {
	a := new(cluster.Alerts)
	a.Errors = currentCluster.GetStateMachine().GetOpenErrors()
	a.Warnings = currentCluster.GetStateMachine().GetOpenWarnings()
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(a)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSettings(w http.ResponseWriter, r *http.Request) {
	s := new(Settings)
	s.Enterprise = fmt.Sprintf("%v", currentCluster.GetConf().Enterprise)
	s.Interactive = fmt.Sprintf("%v", currentCluster.GetConf().Interactive)
	s.RplChecks = fmt.Sprintf("%v", currentCluster.GetConf().RplChecks)
	s.FailSync = fmt.Sprintf("%v", currentCluster.GetConf().FailSync)
	s.SwitchSync = fmt.Sprintf("%v", currentCluster.GetConf().SwitchSync)
	s.Rejoin = fmt.Sprintf("%v", currentCluster.GetConf().Autorejoin)
	s.RejoinBackupBinlog = fmt.Sprintf("%v", currentCluster.GetConf().AutorejoinBackupBinlog)
	s.RejoinSemiSync = fmt.Sprintf("%v", currentCluster.GetConf().AutorejoinSemisync)
	s.RejoinFlashback = fmt.Sprintf("%v", currentCluster.GetConf().AutorejoinFlashback)
	s.RejoinDump = fmt.Sprintf("%v", currentCluster.GetConf().AutorejoinMysqldump)
	s.RejoinUnsafe = fmt.Sprintf("%v", currentCluster.GetConf().FailRestartUnsafe)
	s.RejoinPseudoGTID = fmt.Sprintf("%v", currentCluster.GetConf().AutorejoinSlavePositionalHearbeat)
	s.MaxDelay = fmt.Sprintf("%v", currentCluster.GetConf().FailMaxDelay)
	s.FailoverCtr = fmt.Sprintf("%d", currentCluster.GetFailoverCtr())
	s.Faillimit = fmt.Sprintf("%d", currentCluster.GetConf().FailLimit)
	s.MonHearbeats = fmt.Sprintf("%d", currentCluster.GetStateMachine().GetHeartbeats())
	s.Uptime = currentCluster.GetStateMachine().GetUptime()
	s.UptimeFailable = currentCluster.GetStateMachine().GetUptimeFailable()
	s.UptimeSemiSync = currentCluster.GetStateMachine().GetUptimeSemiSync()
	s.Test = fmt.Sprintf("%v", currentCluster.GetConf().Test)
	s.Heartbeat = fmt.Sprintf("%v", currentCluster.GetConf().Heartbeat)
	s.Status = fmt.Sprintf("%v", runStatus)
	s.ConfGroup = fmt.Sprintf("%s", currentClusterName)
	s.MonitoringTicker = fmt.Sprintf("%d", currentCluster.GetConf().MonitoringTicker)
	s.FailResetTime = fmt.Sprintf("%d", currentCluster.GetConf().FailResetTime)
	s.ToSessionEnd = fmt.Sprintf("%d", currentCluster.GetConf().SessionLifeTime)
	s.HttpAuth = fmt.Sprintf("%v", currentCluster.GetConf().HttpAuth)
	s.HttpBootstrapButton = fmt.Sprintf("%v", currentCluster.GetConf().HttpBootstrapButton)
	s.Clusters = cfgGroupList
	regtest := new(regtest.RegTest)
	s.RegTests = regtest.GetTests()
	if currentCluster.GetLogLevel() > 0 {
		s.Verbose = fmt.Sprintf("%v", true)
	} else {
		s.Verbose = fmt.Sprintf("%v", false)
	}
	if currentCluster.GetFailoverTs() != 0 {
		t := time.Unix(currentCluster.GetFailoverTs(), 0)
		s.LastFailover = t.String()
	} else {
		s.LastFailover = "N/A"
	}
	s.Topology = currentCluster.GetTopology()
	s.Version = fmt.Sprintf("%s %s %s %s", FullVersion, Build, GoOS, GoArch)
	e := json.NewEncoder(w)
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMrmHeartbeat(w http.ResponseWriter, r *http.Request) {
	var send heartbeat
	send.UUID = runUUID
	send.UID = conf.ArbitrationSasUniqueId
	send.Secret = conf.ArbitrationSasSecret
	send.Status = runStatus
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}
}

func handlerLog(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(tlog.Buffer)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if currentCluster.IsMasterFailed() {
		currentCluster.LogPrintf("ERROR", " Master failed, cannot initiate switchover")
		http.Error(w, "Master failed", http.StatusBadRequest)
		return
	}
	currentCluster.LogPrintf("INFO", "Rest API receive Switchover request")
	currentCluster.SwitchOver()
	return
}

func handlerSetActive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//currentCluster.GetActiveStatus()
	return
}

func handlerFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.MasterFailover(true)
	return
}

func handlerInteractiveToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.ToggleInteractive()
	return
}

func handlerRplChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Force to ignore conditions %v", currentCluster.GetRplChecks())
	currentCluster.SetRplChecks(!currentCluster.GetRplChecks())
	return
}

func handlerSwitchSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Force swithover on status sync %v", currentCluster.GetFailSync())

	currentCluster.SetSwitchSync(!currentCluster.GetSwitchSync())
	return
}

func handlerVerbosity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Verbosity %v", currentCluster.GetFailSync())
	if currentCluster.GetLogLevel() > 0 {
		currentCluster.SetLogLevel(0)
	} else {
		currentCluster.SetLogLevel(4)
	}
	return
}

func handlerRejoin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin %v", currentCluster.GetRejoin())
	currentCluster.SetRejoin(!currentCluster.GetRejoin())
	return
}

func handlerRejoinBackupBinlog(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin Backup Binlog %v", currentCluster.GetRejoinBackupBinlog())
	currentCluster.SetRejoinBackupBinlog(!currentCluster.GetRejoinBackupBinlog())
	return
}

func handlerRejoinFlashback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin Flashback %v", currentCluster.GetRejoinFlashback())
	currentCluster.SetRejoinFlashback(!currentCluster.GetRejoinFlashback())
	return
}
func handlerRejoinDump(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin Dump %v", currentCluster.GetRejoinDump())
	currentCluster.SetRejoinDump(!currentCluster.GetRejoinDump())
	return
}

func handlerRejoinSemisync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin Semisync SYNC %v", currentCluster.GetRejoinSemisync())
	currentCluster.SetRejoinSemisync(!currentCluster.GetRejoinSemisync())
	return
}

func handlerRejoinPseudoGTID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change Auto Rejoin Pseudo GTID")
	currentCluster.SwitchPseudoGTID()
	return
}

func handlerSetTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Change test/prod mode %v", currentCluster.GetFailSync())
	currentCluster.SetTestMode(!currentCluster.GetTestMode())
	return
}

func handlerFailSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.LogPrintf("INFO", "Force failover on status sync %v", currentCluster.GetFailSync())

	currentCluster.SetFailSync(!currentCluster.GetFailSync())
	return
}

func handlerResetFailoverCtr(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.ResetFailoverCtr()

	return
}

func handlerBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	currentCluster.SetCleanAll(true)
	if err := currentCluster.Bootstrap(); err != nil {
		currentCluster.LogPrintf("ERROR", "Could not bootstrap replication %s", err)

	}
	return
}

func handlerTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	regtest := new(regtest.RegTest)
	res := regtest.RunAllTests(currentCluster, "ALL")
	currentCluster.LogPrintf("INFO", "Some tests failed %s", res)

	return
}

func handlerSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	go currentCluster.RunSysbench()
	return
}

func handlerStats(res http.ResponseWriter, req *http.Request) {

	var statsPage = template.Must(template.New("").Parse(`
	<html><head><title>REPLICATION-MANAGER</title>
	<meta charset="utf-8">
	<style>

	body {
	  font-family: "Helvetica Neue", Helvetica, sans-serif;
	  margin: 30px auto;
	  width: 1280px;
	  position: relative;
	}

	header {
	  padding: 6px 0;
	}

	.group {
	  margin-bottom: 1em;
	}

	.axis {
	  font: 10px sans-serif;
	  position: fixed;
	  pointer-events: none;
	  z-index: 2;
	}

	.axis text {
	  -webkit-transition: fill-opacity 250ms linear;
	}

	.axis path {
	  display: none;
	}

	.axis line {
	  stroke: #000;
	  shape-rendering: crispEdges;
	}

	.axis.top {
	  background-image: linear-gradient(top, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -o-linear-gradient(top, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -moz-linear-gradient(top, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -webkit-linear-gradient(top, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -ms-linear-gradient(top, #fff 0%, rgba(255,255,255,0) 100%);
	  top: 0px;
	  padding: 0 0 24px 0;
	}

	.axis.bottom {
	  background-image: linear-gradient(bottom, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -o-linear-gradient(bottom, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -moz-linear-gradient(bottom, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -webkit-linear-gradient(bottom, #fff 0%, rgba(255,255,255,0) 100%);
	  background-image: -ms-linear-gradient(bottom, #fff 0%, rgba(255,255,255,0) 100%);
	  bottom: 0px;
	  padding: 24px 0 0 0;
	}

	.horizon {
	  border-bottom: solid 1px #000;
	  overflow: hidden;
	  position: relative;
	}

	.horizon {
	  border-top: solid 1px #000;
	  border-bottom: solid 1px #000;
	}

	.horizon + .horizon {
	  border-top: none;
	}

	.horizon canvas {
	  display: block;
	}

	.horizon .title,
	.horizon .value {
	  bottom: 0;
	  line-height: 30px;
	  margin: 0 6px;
	  position: absolute;
	  text-shadow: 0 1px 0 rgba(255,255,255,.5);
	  white-space: nowrap;
	}

	.horizon .title {
	  left: 0;
	}

	.horizon .value {
	  right: 0;
	}

	.line {
	  background: #000;
	  z-index: 2;
	}

	</style>

<script src="/static/d3.v2.js" charset="utf-8"></script>
<script src="/static/cubism.v1.min.js"></script>
</head>
<body>
<center id="body">
<div id="graph1"></div>
</center>
<script>
var context = cubism.context(), // a default context
    graphite = context.graphite("http://127.0.0.1:10002");
		context.serverDelay(10*1000) // allow 30 seconds of collection lag
		context.step(5*1000) // five sec per value
	  context.size(1000);
		var delay = graphite.metric("server5012.replication.delay");
		var select = graphite.metric("server5012.status.select");
		var selectlast =  select.shift(5*1000);
		var selectdiff = selectlast.subtract(select);
		//var horizon =context.horizon().title("server5012").metric([delay,selectdiff]);
		d3.select("#graph1").call(function(div) {

  div.append("div")
      .attr("class", "axis")
      .call(context.axis().orient("top"));

  div.selectAll(".horizon")
      .data([delay,selectdiff])
    .enter().append("div")
      .attr("class", "horizon")
      .call(context.horizon().extent([-20, 20]));

  div.append("div")
      .attr("class", "rule")
      .call(context.rule());

});

</script>
</body>
</html>`))
	statsPage.Execute(res, nil)
}
func handlerStatsGraphite(res http.ResponseWriter, req *http.Request) {

	var statsPage = template.Must(template.New("").Parse(`
	<html><head><title>REPLICATION-MANAGER</title>
	<script src="https://ajax.googleapis.com/ajax/libs/jquery/3.1.1/jquery.min.js"></script>
	<script src="/static/jquery.graphite.js"></script>
  <link rel="stylesheet" href="/static/bootstrap.min.css" integrity="sha384-1q8mTJOASx8j1Au+a5WDVnPi2lkFfwwEAa8hDDdjZlpLegxhjVME1fgjWPGmkzs7" crossorigin="anonymous" >
	<style>
	td {
			white-space: nowrap;
	}
	.sectionheader {
		font-size: 12px;
		font-weight: 700;
		margin-bottom: 0;
		padding-left: 0;
		border: none;
		background-color: transparent;
		letter-spacing: 0.02em;
		text-transform: uppercase;
	}
	</style>
	</head>
		<body ng-app="dashboard" ng-controller="DashboardController">
	<center>

<img id="graph">
<script>
$("#graph").graphite({
	from: "-24hours",
	target: [
			"server5012.replication.delay",
	],
	url:  "http://127.0.0.1:10002/render/",
	width: "450",
	height: "300",
	format: "json",
});
</script>
</center></body>
</html>`))
	statsPage.Execute(res, nil)
}

// HandleMainPage - main page.
func (hm *HandlerManager) HandleMainPage(w http.ResponseWriter, r *http.Request) {
	// get session client
	user, err := hm.Gelada.GetClient(r)
	if err != nil {
		fmt.Fprintf(w, "server side error: %v\n", err)
		return
	}

	// create struct for our main page with some additional data
	pageData := struct {
		User         *gelada.Client // client
		ToSessionEnd int            // seconds to end of session
		LogoutRoute  string         // route for logout button
	}{
		User:         user,
		ToSessionEnd: user.TimeToEndOfSession(),
		LogoutRoute:  "/logout",
	}
	indexTmpl := template.New("app.html").Delims("{{%", "%}}")
	indexTmpl, _ = indexTmpl.ParseFiles(confs[currentClusterName].HttpRoot + "/app.html")

	indexTmpl.Execute(w, pageData)

}

// HandleLoginPage - login page.
func (hm *HandlerManager) HandleLoginPage(res http.ResponseWriter, req *http.Request) {
	type pageData struct {
		User         *gelada.Client     // client
		Visitor      *authguard.Visitor // visitor
		LockDuration int
	}

	// create struct for our login page with some additional data
	data := pageData{}

	user, err := hm.Gelada.GetClient(req)
	if err != nil {
		fmt.Fprintf(res, "server side error: %v\n", err)
		return
	}
	data.User = user

	visitor, ok := hm.AuthGuard.GetVisitor("gelada", req)
	if ok {
		data.Visitor = visitor
	} else {
		data.Visitor = &authguard.Visitor{Attempts: 0, Lockouts: 0, Username: "gelada"}
	}

	if data.Visitor.Lockouts >= 1 {
		data.LockDuration = data.Visitor.LockRemainingTime()
	}

	var loginPage = template.Must(template.New("").Parse(`
		<html><head><title>REPLICATION-MANAGER</title>  <link rel="stylesheet" href="/static/bootstrap.min.css" integrity="sha384-1q8mTJOASx8j1Au+a5WDVnPi2lkFfwwEAa8hDDdjZlpLegxhjVME1fgjWPGmkzs7" crossorigin="anonymous" >
	    <style>
	  td {
	      white-space: nowrap;
	  }
	  .sectionheader {
	    font-size: 12px;
	    font-weight: 700;
	    margin-bottom: 0;
	    padding-left: 0;
	    border: none;
	    background-color: transparent;
	    letter-spacing: 0.02em;
	  	text-transform: uppercase;
	  }
	  </style>
	  </head>
	    <body ng-app="dashboard" ng-controller="DashboardController">
		<center>
		<script>
		var sessionTimer = document.getElementById("sessionTimer");
		function startTimer(duration, display) {
		    var timer = duration, minutes, seconds;
		    var tick = setInterval(function() {
		        minutes = parseInt(timer / 60, 10);
		        seconds = parseInt(timer % 60, 10);
		        minutes = minutes < 10 ? "0" + minutes : minutes;
		        seconds = seconds < 10 ? "0" + seconds : seconds;
		        display.textContent = minutes + ":" + seconds;
		        if (--timer < 0) {
		            //timer = duration;
					clearInterval(tick);
		        }
		    }, 1000);
		}
		window.onload = function () {
		    var display = document.querySelector('#timer');
		    startTimer("{{.LockDuration}}", display);
		};
		</script>

		<form id="login_form" action="/login" method="POST" style="padding-top:8%;">

			<table><tr><td>
			<img width="80" src="static/logo.png"/>
			</td><td>
			<h1>REPLICATION-MANAGER
		  </h1></td></tr></table>
			<span>Login: Database user | Password: <b>Database password</b><br>



			<hr style='width:50%;'><br>
			<input type="text" name="login" placeholder="Login" autofocus><br>
			<input type="password" placeholder="Password" name="password"><br>
			<br>
			<input class="btn btn-primary"  type="submit" value="LOGIN">
		</form>
		<hr style='width:50%;'>
		<h3>"Stats for your IP</h3>
		{{if .Visitor.Ban}}
			<h4>status: <font color="red"><b>baned</b></font></h4>
		{{else}}
			{{if ge .Visitor.Lockouts 1}}
				<h4>status: <font color="blue"><b>locked</b></font></h4>
			{{else}}
				<h4>status: <font color="green"><b>no locks</b></font></h4>
			{{end}}
		{{end}}
		<table style='text-align:left;border: 0px solid black;width:25%;'>
			<tr><th>Action</th><th>Max</th><th>Current</th></tr>
			<tr><td>Login attepts to lockout</td><td>3</td><td>{{.Visitor.Attempts}}</td></tr>
			<tr><td>Lockouts to ban</td><td>3</td><td>{{.Visitor.Lockouts}}</td></tr>
			{{if ge .Visitor.Lockouts 1}}
				{{if .Visitor.Ban}}
					<tr><td>Time before reset ban</td><td>01:00</td><td id='timer'>00:00</td></tr>
				{{else}}
					<tr><td>Time before reset lockout</td><td>00:30</td><td id='timer'>00:00</td></tr>
				{{end}}
			{{end}}
		</table>
		</center></body>
		</html>`),
	)
	loginPage.Execute(res, data)
}

// HandleLoginFreePage - auth-free page.
func (hm *HandlerManager) HandleLoginFreePage(res http.ResponseWriter, req *http.Request) {
	var freePage = template.Must(template.New("").Parse(`
		<html><head><title>REPLICATION MANAGER</title></head><body>
		<center>
		<h2 style="padding-top:15%;">Free zone :)</h2><br>
		Auth has no power here!<br>
		<a href='/'>Back</a> to root.
		</html>`),
	)
	freePage.Execute(res, nil)
}

// auth provider function
func checkAuth(u, p string) bool {
	if u == currentCluster.GetDbUser() && p == currentCluster.GetDbPass() {
		return true
	}
	return false
}

// test if file exists
func testFile(fn string) error {

	f, err := os.Open(conf.HttpRoot + "/" + fn)
	if err != nil {
		log.Printf("error no file %s", conf.HttpRoot+"/"+fn)
		return err
	}
	f.Close()
	return nil
}
