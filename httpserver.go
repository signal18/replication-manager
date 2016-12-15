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
	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
	"html/template"
	"log"
	"net/http"
	"os"
	"time"
)

type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

type settings struct {
	Interactive      string `json:"interactive"`
	FailoverCtr      string `json:"failoverctr"`
	MaxDelay         string `json:"maxdelay"`
	Faillimit        string `json:"faillimit"`
	LastFailover     string `json:"lastfailover"`
	MonHearbeats     string `json:"monheartbeats"`
	Uptime           string `json:"uptime"`
	UptimeFailable   string `json:"uptimefailable"`
	UptimeSemiSync   string `json:"uptimesemisync"`
	RplChecks        string `json:"rplchecks"`
	FailSync         string `json:"failsync"`
	Test             string `json:"test"`
	Heartbeat        string `json:"heartbeat"`
	Status           string `json:"runstatus"`
	ConfGroup        string `json:"confgroup"`
	MonitoringTicker string `json:"monitoringticker"`
	FailResetTime    string `json:"failresettime"`
	ToSessionEnd     string `json:"tosessionend"`
}

func httpserver() {

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
		router.HandleFunc("/setactive", handlerSetActive)
		router.HandleFunc("/dashboard.js", handlerJS)

		// wrap around our router
		http.Handle("/", g.GlobalAuth(router))
	} else {
		http.HandleFunc("/", handlerApp)
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
