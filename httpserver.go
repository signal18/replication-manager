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
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
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
		RepMan.currentCluster.LogPrintf("ERROR", "Dashboard app.html file missing - will not start http server %s", err)
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
		router.HandleFunc("/noauth/page", handlerApp)
		// login page
		router.HandleFunc("/login", handlerApp)
		// function for processing a request for logout (via POST method)
		router.HandleFunc("/stats", handlerStats)
	} else {
		router.HandleFunc("/", handlerApp)
	}
	// main page

	// page to view which does not need authorization
	router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
	router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
	router.HandleFunc("/data", handlerMuxReplicationManager)
	router.HandleFunc("/settings", handlerSettings)
	router.HandleFunc("/log", handlerLog)
	router.HandleFunc("/agents", handlerAgents)

	router.HandleFunc("/setcluster", handlerSetCluster)
	router.HandleFunc("/heartbeat", handlerHeartbeat)
	router.HandleFunc("/template", handlerOpenSVCTemplate)
	router.HandleFunc("/tests", handlerTests)
	router.HandleFunc("/repocomp/current", handlerRepoComp) // needed to point opensvc agent compliance
	router.HandleFunc("/rolling", handlerRollingUpgrade)    // ? work it out

	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/actions/maintenance", handlerMuxServerMaintenance)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/actions/stop", handlerMuxServerStop)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/actions/start", handlerMuxServerStart)

	router.HandleFunc("/clusters/{clusterName}/topology/crashes", handlerMuxCrashes)
	router.HandleFunc("/clusters/{clusterName}/topology/alerts", handlerMuxAlerts)
	router.HandleFunc("/clusters/{clusterName}/topology/servers", handlerMuxServers)
	router.HandleFunc("/clusters/{clusterName}/topology/slaves", handlerMuxSlaves)
	router.HandleFunc("/clusters/{clusterName}/topology/master", handlerMuxMaster)
	router.HandleFunc("/clusters/{clusterName}/topology/proxies", handlerMuxProxies)

	router.HandleFunc("/clusters/{clusterName}/sphinx-indexes", handlerMuxSphinxIndexes)
	router.HandleFunc("/clusters/{clusterName}/tests/actions/run/{testName}", handlerMuxOneTest)

	router.HandleFunc("/clusters/{clusterName}/actions/switchover", handlerMuxSwitchover)
	router.HandleFunc("/clusters/{clusterName}/actions/failover", handlerMuxFailover)
	router.HandleFunc("/clusters/{clusterName}/actions/reset-failover-counter", handlerMuxClusterResetFailoverControl)
	router.HandleFunc("/clusters/{clusterName}/actions/sysbench", handlerMuxClusterSysbench)
	router.HandleFunc("/clusters/{clusterName}/services/actions/bootstrap", handlerMuxServicesBootstrap)
	router.HandleFunc("/clusters/{clusterName}/services/actions/unprovision", handlerMuxServicesUnprovision)
	router.HandleFunc("/clusters/{clusterName}/settings/actions/switch/{settingName}", handlerMuxSwitchSettings)
	router.HandleFunc("/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", handlerMuxSetSettings)

	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/master-status", handlerMuxServersMasterStatus)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/slave-status", handlerMuxServersSlaveStatus)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/processlist", handlerMuxProcesslist)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/{serverPort}/master-status", handlerMuxServersPortMasterStatus)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/{serverPort}/slave-status", handlerMuxServersPortSlaveStatus)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/actions/optimize", handlerMuxServerOptimize)
	router.HandleFunc("/clusters/{clusterName}/servers/{serverName}/actions/backup-physical", handlerMuxServerBackupPhysical)

	if confs[currentClusterName].Verbose {
		log.Printf("Starting http monitor on port " + confs[currentClusterName].HttpPort)
	}
	log.Fatal(http.ListenAndServe(confs[currentClusterName].BindAddr+":"+confs[currentClusterName].HttpPort, router))
}

func handlerSetCluster(w http.ResponseWriter, r *http.Request) {
	mycluster := r.URL.Query().Get("cluster")
	RepMan.currentCluster = RepMan.Clusters[mycluster]
	currentClusterName = mycluster
	for _, gl := range cfgGroupList {
		RepMan.Clusters[gl].SetCfgGroupDisplay(mycluster)
	}
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

func handlerRollingUpgrade(w http.ResponseWriter, r *http.Request) {
	RepMan.currentCluster.LogPrintf("INFO", "Rest API request rolling upgrade cluster: %s", RepMan.currentCluster.GetName())
	RepMan.currentCluster.RollingUpgrade()
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

func handlerSettings(w http.ResponseWriter, r *http.Request) {
	s := new(Settings)
	s.Enterprise = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().Enterprise)
	s.Interactive = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().Interactive)
	s.RplChecks = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().RplChecks)
	s.FailSync = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().FailSync)
	s.SwitchSync = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().SwitchSync)
	s.Rejoin = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().Autorejoin)
	s.RejoinBackupBinlog = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().AutorejoinBackupBinlog)
	s.RejoinSemiSync = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().AutorejoinSemisync)
	s.RejoinFlashback = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().AutorejoinFlashback)
	s.RejoinDump = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().AutorejoinMysqldump)
	s.RejoinUnsafe = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().FailRestartUnsafe)
	s.RejoinPseudoGTID = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().AutorejoinSlavePositionalHearbeat)
	s.MaxDelay = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().FailMaxDelay)
	s.FailoverCtr = fmt.Sprintf("%d", RepMan.currentCluster.GetFailoverCtr())
	s.Faillimit = fmt.Sprintf("%d", RepMan.currentCluster.GetConf().FailLimit)
	s.MonHearbeats = fmt.Sprintf("%d", RepMan.currentCluster.GetStateMachine().GetHeartbeats())
	s.Uptime = RepMan.currentCluster.GetStateMachine().GetUptime()
	s.UptimeFailable = RepMan.currentCluster.GetStateMachine().GetUptimeFailable()
	s.UptimeSemiSync = RepMan.currentCluster.GetStateMachine().GetUptimeSemiSync()
	s.Test = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().Test)
	s.Heartbeat = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().Heartbeat)
	s.Status = fmt.Sprintf("%v", RepMan.Status)
	s.IsActive = fmt.Sprintf("%v", RepMan.currentCluster.IsActive())
	s.ConfGroup = fmt.Sprintf("%s", currentClusterName)
	s.MonitoringTicker = fmt.Sprintf("%d", RepMan.currentCluster.GetConf().MonitoringTicker)
	s.FailResetTime = fmt.Sprintf("%d", RepMan.currentCluster.GetConf().FailResetTime)
	s.ToSessionEnd = fmt.Sprintf("%d", RepMan.currentCluster.GetConf().SessionLifeTime)
	s.HttpAuth = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().HttpAuth)
	s.HttpBootstrapButton = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().HttpBootstrapButton)
	s.GraphiteMetrics = fmt.Sprintf("%v", RepMan.currentCluster.GetConf().GraphiteMetrics)
	s.Clusters = cfgGroupList
	s.Scheduler = RepMan.currentCluster.GetCron()
	regtest := new(regtest.RegTest)
	s.RegTests = regtest.GetTests()
	if RepMan.currentCluster.GetLogLevel() > 0 {
		s.Verbose = fmt.Sprintf("%v", true)
	} else {
		s.Verbose = fmt.Sprintf("%v", false)
	}
	if RepMan.currentCluster.GetFailoverTs() != 0 {
		t := time.Unix(RepMan.currentCluster.GetFailoverTs(), 0)
		s.LastFailover = t.String()
	} else {
		s.LastFailover = "N/A"
	}
	s.Topology = RepMan.currentCluster.GetTopology()
	s.Version = fmt.Sprintf("%s %s %s %s", FullVersion, Build, GoOS, GoArch)
	s.DBTags = RepMan.currentCluster.GetDatabaseTags()
	s.ProxyTags = RepMan.currentCluster.GetProxyTags()
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}

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

func handlerTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	regtest := new(regtest.RegTest)
	res := regtest.RunAllTests(RepMan.currentCluster, "ALL")
	RepMan.currentCluster.LogPrintf("INFO", "Some tests failed %s", res)

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
	if u == RepMan.currentCluster.GetDbUser() && p == RepMan.currentCluster.GetDbPass() {
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
