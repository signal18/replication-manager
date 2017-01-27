// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
)

type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

type settings struct {
	Interactive         string `json:"interactive"`
	FailoverCtr         string `json:"failoverctr"`
	MaxDelay            string `json:"maxdelay"`
	Faillimit           string `json:"faillimit"`
	LastFailover        string `json:"lastfailover"`
	MonHearbeats        string `json:"monheartbeats"`
	Uptime              string `json:"uptime"`
	UptimeFailable      string `json:"uptimefailable"`
	UptimeSemiSync      string `json:"uptimesemisync"`
	RplChecks           string `json:"rplchecks"`
	FailSync            string `json:"failsync"`
	Test                string `json:"test"`
	Heartbeat           string `json:"heartbeat"`
	Status              string `json:"runstatus"`
	ConfGroup           string `json:"confgroup"`
	MonitoringTicker    string `json:"monitoringticker"`
	FailResetTime       string `json:"failresettime"`
	ToSessionEnd        string `json:"tosessionend"`
	HttpAuth            string `json:"httpauth"`
	HttpBootstrapButton string `json:"httpbootstrapbutton"`
}

func httpserver() {

	// before starting the http server, check that the dashboard is present
	if err := testFile("app.html"); err != nil {
		logprint("ERROR: Dashboard app.html file missing - will not start http server", err)
		return
	}
	if err := testFile("dashboard.js"); err != nil {
		logprint("ERROR: dashboard.js file missing - will not start http server")
		return
	}

	if conf.HttpAuth {
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
			logprint("auth guard init error: %v\n", err)
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
			MaxAge:   conf.SessionLifeTime, // 60 seconds
			HTTPOnly: true,

			SessionName:     "test-session",
			SessionLifeTime: conf.SessionLifeTime, // 60 seconds
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
			logprint("gelada init error: %v\n", err)
			return
		}

		// create handler manager
		hm := &HandlerManager{
			Gelada:    g,
			AuthGuard: ag,
		}

		// create mux router
		router := mux.NewRouter()

		// main page
		router.HandleFunc("/", hm.HandleMainPage)
		// page to view which does not need authorization
		router.HandleFunc("/noauth/page", hm.HandleLoginFreePage)
		// login page
		router.HandleFunc("/login", hm.HandleLoginPage).Methods("GET")
		// function for processing a request for authorization (via POST method)
		router.HandleFunc("/login", g.AuthHandler).Methods("POST")
		// function for processing a request for logout (via POST method)
		router.HandleFunc("/stats", handlerStats)
		//http.HandleFunc("/", handlerApp)
		router.HandleFunc("/logout", g.LogoutHandler).Methods("POST")
		router.HandleFunc("/servers", handlerServers)
		router.HandleFunc("/master", handlerMaster)
		router.HandleFunc("/log", handlerLog)
		router.HandleFunc("/switchover", handlerSwitchover)
		router.HandleFunc("/failover", handlerFailover)
		router.HandleFunc("/interactive", handlerInteractiveToggle)
		router.HandleFunc("/settings", handlerSettings)
		router.HandleFunc("/resetfail", handlerResetFailoverCtr)
		router.HandleFunc("/rplchecks", handlerRplChecks)
		router.HandleFunc("/bootstrap", handlerBootstrap)
		router.HandleFunc("/failsync", handlerFailSync)
		router.HandleFunc("/tests", handlerTests)
		router.HandleFunc("/sysbench", handlerSysbench)
		router.HandleFunc("/setactive", handlerSetActive)
		router.HandleFunc("/dashboard.js", handlerJS)

		// wrap around our router
		http.Handle("/", g.GlobalAuth(router))
	} else {
		http.HandleFunc("/", handlerApp)
		http.HandleFunc("/stats", handlerStats)
		http.HandleFunc("/servers", handlerServers)
		http.HandleFunc("/master", handlerMaster)
		http.HandleFunc("/log", handlerLog)
		http.HandleFunc("/switchover", handlerSwitchover)
		http.HandleFunc("/failover", handlerFailover)
		http.HandleFunc("/interactive", handlerInteractiveToggle)
		http.HandleFunc("/settings", handlerSettings)
		http.HandleFunc("/resetfail", handlerResetFailoverCtr)
		http.HandleFunc("/rplchecks", handlerRplChecks)
		http.HandleFunc("/bootstrap", handlerBootstrap)
		http.HandleFunc("/failsync", handlerFailSync)
		http.HandleFunc("/tests", handlerTests)
		http.HandleFunc("/sysbench", handlerSysbench)
		http.HandleFunc("/setactive", handlerSetActive)
		http.HandleFunc("/dashboard.js", handlerJS)
	}
	http.Handle("/static/", http.FileServer(http.Dir(conf.HttpRoot)))
	if conf.Verbose {
		logprint("INFO : Starting http monitor on port " + conf.HttpPort)
	}

	log.Fatal(http.ListenAndServe(conf.BindAddr+":"+conf.HttpPort, nil))
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, conf.HttpRoot+"/app.html")
}

func handlerJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, conf.HttpRoot+"/dashboard.js")
}

func handlerServers(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(servers)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMaster(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(master)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSettings(w http.ResponseWriter, r *http.Request) {
	s := new(settings)
	s.Interactive = fmt.Sprintf("%v", conf.Interactive)
	s.RplChecks = fmt.Sprintf("%v", conf.RplChecks)
	s.FailSync = fmt.Sprintf("%v", conf.FailSync)
	s.MaxDelay = fmt.Sprintf("%v", conf.MaxDelay)
	s.FailoverCtr = fmt.Sprintf("%d", failoverCtr)
	s.Faillimit = fmt.Sprintf("%d", conf.FailLimit)
	s.MonHearbeats = fmt.Sprintf("%d", sme.GetHeartbeats())
	s.Uptime = sme.GetUptime()
	s.UptimeFailable = sme.GetUptimeFailable()
	s.UptimeSemiSync = sme.GetUptimeSemiSync()
	s.Test = fmt.Sprintf("%v", conf.Test)
	s.Heartbeat = fmt.Sprintf("%v", conf.Heartbeat)
	s.Status = fmt.Sprintf("%v", runStatus)
	s.ConfGroup = fmt.Sprintf("%s", cfgGroup)
	s.MonitoringTicker = fmt.Sprintf("%d", conf.MonitoringTicker)
	s.FailResetTime = fmt.Sprintf("%d", conf.FailResetTime)
	s.ToSessionEnd = fmt.Sprintf("%d", conf.SessionLifeTime)
	s.HttpAuth = fmt.Sprintf("%v", conf.HttpAuth)
	s.HttpBootstrapButton = fmt.Sprintf("%v", conf.HttpBootstrapButton)
	if failoverTs != 0 {
		t := time.Unix(failoverTs, 0)
		s.LastFailover = t.String()
	} else {
		s.LastFailover = "N/A"
	}
	e := json.NewEncoder(w)
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
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
	if master.State == stateFailed {
		logprint("ERROR: Master failed, cannot initiate switchover")
		http.Error(w, "Master failed", http.StatusBadRequest)
		return
	}
	swChan <- true
	return
}

func handlerSetActive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	getActiveStatus()
	return
}

func handlerFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	masterFailover(true)
	return
}

func handlerInteractiveToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	toggleInteractive()
	return
}

func handlerRplChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprint("INFO: Force to ignore conditions %v", conf.RplChecks)
	conf.RplChecks = !conf.RplChecks
	return
}

func handlerFailSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprintf("INFO: Force failover on status sync %v", conf.FailSync)
	conf.FailSync = !conf.FailSync
	return
}

func handlerResetFailoverCtr(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	failoverCtr = 0
	failoverTs = 0
	return
}

func handlerBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	cleanall = true
	if err := bootstrap(); err != nil {
		logprint("ERROR: Could not bootstrap replication")
		logprint(err)
	}
	return
}

func handlerTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	err := runAllTests()
	if err == false {
		logprint("ERROR: Some tests failed")
	}
	return
}

func handlerSysbench(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	go runSysbench()
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
	indexTmpl, _ = indexTmpl.ParseFiles(conf.HttpRoot + "/app.html")

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
	if u == dbUser && p == dbPass {
		return true
	}
	return false
}

// test if file exists
func testFile(fn string) error {
	f, err := os.Open(conf.HttpRoot + "/" + fn)
	if err != nil {
		return err
	}
	f.Close()
	return nil
}
