package cluster

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The http/https check is taken only in its two modes; a full app-host name is called as
// is, a short one on the cluster address; path and status default like a route monitor (#2209).
func TestAppMonitorHTTPRoute(t *testing.T) {
	for _, mode := range []string{"", "port", "ping"} {
		if _, ok := appMonitorHTTPRoute(&config.AppConfig{AppMonitorMode: mode}, "x"); ok {
			t.Fatalf("%q must not be an http check", mode)
		}
	}
	if _, ok := appMonitorHTTPRoute(nil, "x"); ok {
		t.Fatal("nil config: no http check")
	}
	r, ok := appMonitorHTTPRoute(&config.AppConfig{AppMonitorMode: "HTTPS", AppHost: "web.example.com", AppPort: "443"}, "web.example.com.c1.svc.cloud18")
	if !ok || r.Protocol != "https" || r.CName != "web.example.com:443" {
		t.Fatalf("full name: want https web.example.com:443, got %v %+v", ok, r)
	}
	if r.Monitor == nil || r.Monitor.Path != "/" || r.Monitor.ExpectStatus != "200" {
		t.Fatalf("defaults: want / and 200, got %+v", r.Monitor)
	}
	r, _ = appMonitorHTTPRoute(&config.AppConfig{AppMonitorMode: "http", AppHost: "web", AppPort: "8080", AppMonitorPath: "/health", AppMonitorExpectStatus: "200,204"}, "web.c1.svc.cloud18")
	if r.Protocol != "http" || r.CName != "web.c1.svc.cloud18:8080" || r.Monitor.Path != "/health" || r.Monitor.ExpectStatus != "200,204" {
		t.Fatalf("short name: want the cluster address and the set path/status, got %+v %+v", r, r.Monitor)
	}
}

// A service that answers is up; a wrong status opens APPERR002 with the code; a service
// that accepts the connection and never answers opens APPERR001 within the timeout: the
// case port and ping cannot see (#2209).
func TestProbeNoRouteHTTP(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	appFor := func(url, path, status string) *App {
		hostPort := strings.TrimPrefix(url, "http://")
		host, port, _ := net.SplitHostPort(hostPort)
		cl := &Cluster{Conf: &config.Config{Timeout: 1}}
		return &App{Id: "ap1", Name: "web", Host: host, Port: port, ClusterGroup: cl,
			AppConfig: &config.AppConfig{AppHost: host, AppPort: port, AppMonitorMode: "http", AppMonitorPath: path, AppMonitorExpectStatus: status}}
	}

	if key, err := appFor(ok.URL, "/health", "").probeNoRoute(time.Second); err != nil {
		t.Fatalf("answering service: want up, got %s %v", key, err)
	}
	app := appFor(ok.URL, "/", "200")
	key, err := app.probeNoRoute(time.Second)
	if key != ErrAppUnexpectedStatus || err == nil {
		t.Fatalf("404: want APPERR002, got %s %v", key, err)
	}
	if desc := app.noRouteProbeErrDesc(key, err); !strings.Contains(desc, "404") {
		t.Fatalf("APPERR002 text must carry the code: %q", desc)
	}

	// accepts the connection, never answers
	ln, lerr := net.Listen("tcp", "127.0.0.1:0")
	if lerr != nil {
		t.Fatal(lerr)
	}
	defer ln.Close()
	go func() {
		for {
			c, aerr := ln.Accept()
			if aerr != nil {
				return
			}
			defer c.Close()
		}
	}()
	app = appFor("http://"+ln.Addr().String(), "/", "")
	start := time.Now()
	key, err = app.probeNoRoute(time.Second)
	if key != ErrAppConnectFailed || err == nil {
		t.Fatalf("hanging service: want APPERR001, got %s %v", key, err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the check must end with the cluster timeout, took %s", elapsed)
	}
	if desc := app.noRouteProbeErrDesc(key, err); !strings.Contains(desc, "ap1") {
		t.Fatalf("APPERR001 text must name the app: %q", desc)
	}
}

// The mode accepts http and https; the path must start with /; the expected status uses
// the route syntax; a refused value is never stored (#2209).
func TestSetAppMonitorHTTPSettings(t *testing.T) {
	app := &App{AppConfig: &config.AppConfig{}, ClusterGroup: &Cluster{}}
	for _, v := range []string{"http", "HTTPS"} {
		if err := app.SetSetting("app-monitor-mode", v); err != nil {
			t.Fatalf("%q must be accepted: %v", v, err)
		}
	}
	if err := app.SetSetting("app-monitor-path", "/users/sign_in"); err != nil || app.AppConfig.AppMonitorPath != "/users/sign_in" {
		t.Fatalf("path: %v %q", err, app.AppConfig.AppMonitorPath)
	}
	if err := app.SetSetting("app-monitor-path", "nolead"); err == nil || app.AppConfig.AppMonitorPath != "/users/sign_in" {
		t.Fatalf("a path without / must be refused and not stored: %v %q", err, app.AppConfig.AppMonitorPath)
	}
	if err := app.SetSetting("app-monitor-expect-status", "200, 302"); err != nil || app.AppConfig.AppMonitorExpectStatus != "200, 302" {
		t.Fatalf("status: %v %q", err, app.AppConfig.AppMonitorExpectStatus)
	}
	if err := app.SetSetting("app-monitor-expect-status", "2xx"); err == nil || app.AppConfig.AppMonitorExpectStatus != "200, 302" {
		t.Fatalf("a bad status must be refused and not stored: %v %q", err, app.AppConfig.AppMonitorExpectStatus)
	}
	for _, k := range []string{"app-monitor-path", "app-monitor-expect-status"} {
		if err := app.SetSetting(k, ""); err != nil {
			t.Fatalf("clear %s: %v", k, err)
		}
	}
}

// A monitor-only app (app-monitor-only, set when added without image nor template): the
// service actions refuse it with a clear error; any other app is not refused (#2209).
func TestRefuseMonitorOnlyApp(t *testing.T) {
	cl := &Cluster{Conf: &config.Config{}}
	mon := &App{Id: "ap1", AppConfig: &config.AppConfig{AppMonitorOnly: true}}
	if !mon.AppConfig.IsMonitorOnly() {
		t.Fatal("app-monitor-only: monitor-only")
	}
	if err := cl.refuseMonitorOnlyApp(mon); err == nil || !strings.Contains(err.Error(), "monitor-only") {
		t.Fatalf("monitor-only app must be refused: %v", err)
	}
	if err := cl.InitAppService(mon); err == nil || !strings.Contains(err.Error(), "monitor-only") {
		t.Fatalf("provision of a monitor-only app must be refused before the orchestrator: %v", err)
	}
	// an app config without image nor template is NOT monitor-only unless flagged: an
	// existing app keeps its plan units and its service actions
	for _, cnf := range []*config.AppConfig{{}, {ProvAppDockerImg: "nginx:1"}, {ProvAppTemplate: "shared/nginx/nginx"}} {
		if cnf.IsMonitorOnly() {
			t.Fatalf("%+v is deployable", cnf)
		}
		if err := cl.refuseMonitorOnlyApp(&App{AppConfig: cnf}); err != nil {
			t.Fatalf("%+v must not be refused: %v", cnf, err)
		}
	}
	var nilCnf *config.AppConfig
	if nilCnf.IsMonitorOnly() {
		t.Fatal("nil config is not monitor-only")
	}
}

// A check reads at most appHTTPCheckMaxBody of the body: a service answering a huge page
// costs no more memory per check (F4, #2209).
func TestProbeNoRouteHTTPBodyBounded(t *testing.T) {
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		for i := 0; i < 64; i++ { // 4 MiB
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	defer big.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(big.URL, "http://"))
	app := &App{Id: "ap1", Host: host, Port: port, ClusterGroup: &Cluster{Conf: &config.Config{Timeout: 5}},
		AppConfig: &config.AppConfig{AppHost: host, AppPort: port, AppMonitorMode: "http"}}
	route, _ := appMonitorHTTPRoute(app.AppConfig, host)
	status, body, err := app.GetAppHTTPStatus(route, true)
	if err != nil || status != http.StatusOK {
		t.Fatalf("want 200, got %d %v", status, err)
	}
	if len(body) != appHTTPCheckMaxBody {
		t.Fatalf("body must be capped at %d bytes, got %d", appHTTPCheckMaxBody, len(body))
	}
}

// A monitor-only app gets no default route at registration: a route would be published on
// the cluster's gateway and its check would call the cluster address (#2209, found on
// preprod). An app declared from an image keeps it; a template decides its own routes.
func TestAppGetsDefaultRoute(t *testing.T) {
	if appGetsDefaultRoute(&config.AppConfig{AppMonitorOnly: true}) {
		t.Fatal("monitor-only app: no default route")
	}
	if !appGetsDefaultRoute(&config.AppConfig{ProvAppDockerImg: "nginx:1"}) {
		t.Fatal("app from an image: default route")
	}
	if appGetsDefaultRoute(&config.AppConfig{ProvAppTemplate: "shared/nginx/nginx"}) {
		t.Fatal("app from a template: the template decides")
	}
}

// The certificate warns when it expires within the days set (14 by default), and says
// when it already expired (#2209).
func TestCertExpiryError(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	if err := certExpiryError(now.Add(60*24*time.Hour), now, 0); err != nil {
		t.Fatalf("60 days left, default 14: valid, got %v", err)
	}
	if err := certExpiryError(now.Add(10*24*time.Hour), now, 0); err == nil || !strings.Contains(err.Error(), "expires in 10 days") {
		t.Fatalf("10 days left, default 14: want a warning, got %v", err)
	}
	if err := certExpiryError(now.Add(10*24*time.Hour), now, 7); err != nil {
		t.Fatalf("10 days left, warning below 7: valid, got %v", err)
	}
	if err := certExpiryError(now.Add(-time.Hour), now, 14); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("past: want expired, got %v", err)
	}
}

// https-cert: the GET passes (no verification, like the route checks), then the verified
// look at the certificate refuses a self-signed one with APPERR010; plain https does not
// look at the certificate (#2209).
func TestProbeNoRouteHTTPSCert(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer srv.Close()
	host, port, _ := net.SplitHostPort(strings.TrimPrefix(srv.URL, "https://"))
	appFor := func(mode string) *App {
		return &App{Id: "ap1", Host: host, Port: port, ClusterGroup: &Cluster{Conf: &config.Config{Timeout: 2}},
			AppConfig: &config.AppConfig{AppHost: host, AppPort: port, AppMonitorMode: mode}}
	}
	if key, err := appFor("https").probeNoRoute(2 * time.Second); err != nil {
		t.Fatalf("https on a self-signed service: the GET passes, got %s %v", key, err)
	}
	app := appFor("https-cert")
	key, err := app.probeNoRoute(2 * time.Second)
	if key != ErrAppCertificate || err == nil {
		t.Fatalf("https-cert on a self-signed certificate: want APPERR010, got %s %v", key, err)
	}
	if desc := app.noRouteProbeErrDesc(key, err); !strings.Contains(desc, "ap1") || !strings.Contains(desc, "not valid") {
		t.Fatalf("APPERR010 text: %q", desc)
	}
	if err := app.SetSetting("app-monitor-mode", "https-cert"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetSetting("app-monitor-cert-expire-days", "30"); err != nil || app.AppConfig.AppMonitorCertExpireDays != 30 {
		t.Fatalf("days: %v %d", err, app.AppConfig.AppMonitorCertExpireDays)
	}
	if err := app.SetSetting("app-monitor-cert-expire-days", "-1"); err == nil || app.AppConfig.AppMonitorCertExpireDays != 30 {
		t.Fatalf("negative days must be refused and not stored: %v %d", err, app.AppConfig.AppMonitorCertExpireDays)
	}
}
