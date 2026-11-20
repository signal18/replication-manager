package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/config"
)

func TestAccessLogPathKeepsSecretsOut(t *testing.T) {
	cases := []struct {
		template string
		vars     map[string]string
		want     string
	}{
		{"/api/clusters/{clusterName}", map[string]string{"clusterName": "dev3"}, "/api/clusters/dev3"},
		{"/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue:.*}",
			map[string]string{"clusterName": "dev3", "settingName": "db-servers-credential", "settingValue": "root:s3cret"},
			"/api/clusters/dev3/settings/actions/set/db-servers-credential/{settingValue}"},
		{"/api/tokens/{tokenID}", map[string]string{"tokenID": "abc123"}, "/api/tokens/{tokenID}"},
		{"/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/secret-login",
			map[string]string{"clusterName": "c", "serverName": "db1", "serverPort": "3306"},
			"/api/clusters/c/servers/db1/3306/secret-login"},
		{"/api/x/{clusterName:[a-z]{1,3}}/{n}", map[string]string{"clusterName": "abc", "n": "42"}, "/api/x/abc/{n}"},
		// an allowlist: a variable not known to be safe is never logged by value
		{"/api/reveal/{hash}/{otp}", map[string]string{"hash": "h4sh", "otp": "123456"}, "/api/reveal/{hash}/{otp}"},
		{"", nil, "/api/raw"},
	}
	for _, c := range cases {
		if got := accessLogPath(c.template, c.vars, "/api/raw"); got != c.want {
			t.Errorf("accessLogPath(%q) = %q, want %q", c.template, got, c.want)
		}
	}
}

func TestAPIAccessLogMiddlewareRedactsAndIsBounded(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.Conf.LogAPIAccess = true
	repman.APIAccessLogs = newInstanceLogRing(2, DefaultAPIAccessLogLength)

	router := mux.NewRouter()
	router.Use(repman.apiAccessLogMiddleware)
	router.HandleFunc("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue:.*}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	router.HandleFunc("/api/clusters/{clusterName}/topology/servers", func(w http.ResponseWriter, r *http.Request) {})
	router.HandleFunc("/static/x", func(w http.ResponseWriter, r *http.Request) {})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/clusters/dev3/settings/actions/set/db-servers-credential/root:s3cret?token=qwerty", nil)
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.bearer-secret.sig")
		req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i+1) // distinct callers: no collapse
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	// not recorded: a successful read (the GUI's polling), and paths outside /api/
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/clusters/dev3/topology/servers", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/static/x", nil))

	snap := repman.APIAccessLogs.snapshot()
	if len(snap.Buffer) != 2 {
		t.Fatalf("want 2 entries kept (the hard max), got %d", len(snap.Buffer))
	}
	if snap.Dropped != 3 {
		t.Fatalf("want 3 dropped, got %d", snap.Dropped)
	}
	for _, e := range snap.Buffer {
		for _, secret := range []string{"s3cret", "qwerty", "bearer-secret", "Bearer", "token="} {
			if strings.Contains(e.Text, secret) {
				t.Fatalf("access entry leaks %q: %s", secret, e.Text)
			}
		}
		if !strings.Contains(e.Text, "GET /api/clusters/dev3/settings/actions/set/db-servers-credential/{settingValue} 403") || e.Level != "WARN" {
			t.Fatalf("unexpected access entry %s %s", e.Level, e.Text)
		}
	}

	// a successful write is recorded; a newline in a path never reaches the entry
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/clusters/dev3/topology/servers", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/clusters/forged%0aline/topology/servers", nil))
	snap = repman.APIAccessLogs.snapshot()
	if !strings.Contains(snap.Buffer[1].Text, "POST /api/clusters/dev3/topology/servers 200") || snap.Buffer[1].Level != "INFO" {
		t.Fatalf("a successful write must be recorded, got %s %s", snap.Buffer[1].Level, snap.Buffer[1].Text)
	}
	if strings.ContainsAny(snap.Buffer[0].Text, "\n\r") || !strings.Contains(snap.Buffer[0].Text, "/api/clusters/forgedline/topology/servers 200") {
		t.Fatalf("control characters must be dropped: %q", snap.Buffer[0].Text)
	}

	repman.Conf.LogAPIAccess = false
	before := len(repman.APIAccessLogs.snapshot().Buffer)
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/clusters/dev3/settings/actions/set/a/b", nil))
	if after := len(repman.APIAccessLogs.snapshot().Buffer); after != before {
		t.Fatalf("log-api-access=false must record nothing")
	}
}

func TestSecurityEventRing(t *testing.T) {
	var nilRing *instanceLogRing
	nilRing.addSecurityEvent("api_auth_failure", "u", "1.2.3.4", "x") // no panic before init

	ring := newInstanceLogRing(0, 3)
	if ring.log.Len != 3 {
		t.Fatalf("size 0 must take the default, got %d", ring.log.Len)
	}
	ring.addSecurityEvent("api_login_success", "alice", "10.0.0.1", "API login successful")
	ring.addSecurityEvent("api_auth_failure", "bob", "10.0.0.2", "API authentication failure: invalid credentials")
	snap := ring.snapshot()
	if len(snap.Buffer) != 2 || snap.Buffer[0].Level != "WARN" || snap.Buffer[1].Level != "INFO" {
		t.Fatalf("unexpected security entries %+v", snap.Buffer)
	}
	if !strings.Contains(snap.Buffer[0].Text, `api_auth_failure`) || !strings.Contains(snap.Buffer[0].Text, `"bob"`) {
		t.Fatalf("security entry misses its fields: %s", snap.Buffer[0].Text)
	}
	ring.resize(1, 3)
	if got := len(ring.snapshot().Buffer); got != 1 {
		t.Fatalf("resize to 1 must keep 1 entry, got %d", got)
	}
}

func TestAPIAccessLogSkipsJobsProtocolAndCollapsesRepeats(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.Conf.LogAPIAccess = true
	repman.APIAccessLogs = newInstanceLogRing(10, DefaultAPIAccessLogLength)

	router := mux.NewRouter()
	router.Use(repman.apiAccessLogMiddleware)
	no := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("false"))
	}
	router.HandleFunc("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/needs/{taskname}", no)
	router.HandleFunc("/api/clusters/{clusterName}/jobs-log-level/{task}/{level}", no)
	router.HandleFunc("/api/clusters/{clusterName}/actions/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	poll := func() {
		for i := 0; i < 20; i++ {
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/clusters/dev3/servers/db1/3306/needs/mysqldump", nil))
		}
	}
	// at the default task level, the jobs protocol's "no" answers leave no trace
	repman.Conf.LogTask = true
	repman.Conf.LogTaskLevel = 1
	poll()
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/clusters/dev3/jobs-log-level/jobs-check/DEBUG", nil))
	if n := len(repman.APIAccessLogs.snapshot().Buffer); n != 0 {
		t.Fatalf("the jobs protocol polling must not be recorded below debug, got %d entries", n)
	}
	// at debug: one DEBUG entry naming the task, the repeats collapsed
	repman.Conf.LogTaskLevel = 4
	poll()
	snap0 := repman.APIAccessLogs.snapshot()
	if len(snap0.Buffer) != 1 || snap0.Buffer[0].Level != "DEBUG" ||
		!strings.Contains(snap0.Buffer[0].Text, "/needs/mysqldump 500") || !strings.Contains(snap0.Buffer[0].Text, "(repeated 20 times)") {
		t.Fatalf("want one collapsed DEBUG entry naming the task, got %+v", snap0.Buffer)
	}
	repman.APIAccessLogs = newInstanceLogRing(10, DefaultAPIAccessLogLength)

	// identical refusals in a row from one host (any source port) collapse into one entry
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("POST", "/api/clusters/dev3/actions/stop", nil)
		req.RemoteAddr = fmt.Sprintf("10.0.0.7:%d", 40000+i)
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	snap := repman.APIAccessLogs.snapshot()
	if len(snap.Buffer) != 1 || !strings.Contains(snap.Buffer[0].Text, "(repeated 5 times)") {
		t.Fatalf("want one collapsed entry, got %+v", snap.Buffer)
	}
	other := httptest.NewRequest("POST", "/api/clusters/dev3/actions/stop", nil)
	other.RemoteAddr = "10.0.0.8:1"
	router.ServeHTTP(httptest.NewRecorder(), other)
	if n := len(repman.APIAccessLogs.snapshot().Buffer); n != 2 {
		t.Fatalf("another host is a new entry, got %d", n)
	}
}
