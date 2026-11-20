package server

import (
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
		{"/api/x/{n:[0-9]{1,3}}", map[string]string{"n": "42"}, "/api/x/42"},
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
	router.HandleFunc("/api/global/access-logs", func(w http.ResponseWriter, r *http.Request) {})
	router.HandleFunc("/static/x", func(w http.ResponseWriter, r *http.Request) {})

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest("GET", "/api/clusters/dev3/settings/actions/set/db-servers-credential/root:s3cret?token=qwerty", nil)
		req.Header.Set("Authorization", "Bearer eyJhbGciOiJSUzI1NiJ9.bearer-secret.sig")
		router.ServeHTTP(httptest.NewRecorder(), req)
	}
	// not recorded: the access log's own reads, and paths outside /api/
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/global/access-logs", nil))
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/static/x", nil))

	snap := repman.APIAccessLogs.snapshot()
	if len(snap.Buffer) != 3 { // a ring of 2 keeps Len+1 entries (HttpLog.Shift)
		t.Fatalf("want 3 entries kept, got %d", len(snap.Buffer))
	}
	if snap.Dropped != 2 {
		t.Fatalf("want 2 dropped, got %d", snap.Dropped)
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
