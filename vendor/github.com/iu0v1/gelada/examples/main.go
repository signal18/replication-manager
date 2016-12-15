package main

import (
	"fmt"
	"html/template"
	"net/http"
	"os"

	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
)

func main() {
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
		fmt.Printf("auth guard init error: %v\n", err)
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
		MaxAge:   60, // 60 seconds
		HTTPOnly: true,

		SessionName:     "test-session",
		SessionLifeTime: 60, // 60 seconds
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
		fmt.Printf("gelada init error: %v\n", err)
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
	router.HandleFunc("/logout", g.LogoutHandler).Methods("POST")

	// wrap around our router
	http.Handle("/", g.GlobalAuth(router))

	// start net/http server at 8082 port
	fmt.Println("start server at 127.0.0.1:8082")
	if err := http.ListenAndServe("127.0.0.1:8082", nil); err != nil {
		panic(err)
	}
}

// HandlerManager need for manage handlers and share some staff beetween them.
type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

// HandleMainPage - main page.
func (hm *HandlerManager) HandleMainPage(res http.ResponseWriter, req *http.Request) {
	// get session client
	user, err := hm.Gelada.GetClient(req)
	if err != nil {
		fmt.Fprintf(res, "server side error: %v\n", err)
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

	mainPage := template.Must(template.New("").Parse(`
		<html><head><title>Gelada login DEMO</title></head><body>
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
		    var display = document.querySelector('#time');
		    startTimer("{{.ToSessionEnd}}", display);
		};
		</script>
		<center>
		<h1 style="padding-top:15%;">HELLO {{.User.Username}}!</h1><br>
		<div><span id="time">00:00</span> minutes to end of this session</div><br>
		<form action="{{.LogoutRoute}}" method="post">
			<button type="submit">Logout</button>
		</form>
		</center></body>
		</html>`),
	)
	mainPage.Execute(res, pageData)
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
		<html><head><title>Gelada login DEMO</title></head><body>
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
			<h1><a href='https://github.com/iu0v1/gelada'>Gelada</a> DEMO</h1>
			<span>Login: <b>gelada</b> | Password: <b>qwerty</b><br>
			Try to login with wrong password :)<br>
			Or go to <a href="/noauth/page">login-free zone</a>.</span><br>
			<hr style='width:50%;'><br>
			<input type="text" name="login" placeholder="Login" autofocus><br>
			<input type="password" placeholder="Password" name="password"><br>
			<input type="submit" value="LOGIN">
		</form>
		<hr style='width:50%;'>
		<h3>"gelada" user stats for your IP</h3>
		{{if .Visitor.Ban}}
			<h4>status: <font color="red"><b>baned</b></font></h4>
		{{else}}
			{{if ge .Visitor.Lockouts 1}}
				<h4>status: <font color="blue"><b>locked</b></font></h4>
			{{else}}
				<h4>status: <font color="green"><b>no locks</b></font></h4>
			{{end}}
		{{end}}
		<table style='text-align:center;border: 1px solid black;width:25%;'>
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
		<html><head><title>Gelada login DEMO</title></head><body>
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
	if u == "gelada" && p == "qwerty" {
		return true
	}
	return false
}
