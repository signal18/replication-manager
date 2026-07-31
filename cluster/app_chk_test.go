package cluster

import (
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// assertNoGoroutineLeak runs check() a warm-up time plus `iterations` times
// and fails if the goroutine count grows beyond a small constant slack
// afterwards. A regression that leaks a persistConn per call would grow the
// count roughly linearly with iterations.
func assertNoGoroutineLeak(t *testing.T, iterations int, check func() error) {
	t.Helper()

	if err := check(); err != nil {
		t.Fatalf("warm-up request failed: %v", err)
	}
	runtime.GC()
	time.Sleep(50 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for i := 0; i < iterations; i++ {
		if err := check(); err != nil {
			t.Fatalf("request %d failed: %v", i, err)
		}
	}

	runtime.GC()
	time.Sleep(200 * time.Millisecond)
	after := runtime.NumGoroutine()

	if delta := after - baseline; delta > 10 {
		t.Fatalf("goroutine count grew by %d after %d requests (baseline=%d, after=%d); suspect leaked HTTP connections",
			delta, iterations, baseline, after)
	}
}

func newTestAppForHTTPChecks(host string) *App {
	return &App{
		Name:         "app1",
		Host:         host,
		ClusterGroup: &Cluster{Conf: &config.Config{Timeout: 2}},
		Mutex:        &sync.Mutex{},
	}
}

// TestGetAppHTTPStatus_DoesNotLeakConnections guards against the regression
// where GetAppHTTPStatus built a brand-new http.Transport per call without
// ever closing its idle connections. Each successful check left a
// persistConn readLoop/writeLoop goroutine (and its socket) running forever,
// since nothing else referenced the ad-hoc Transport once the function
// returned. Under sustained monitoring ticks this accumulated tens of
// thousands of leaked goroutines until the only fix was restarting the
// process.
func TestGetAppHTTPStatus_DoesNotLeakConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := newTestAppForHTTPChecks("")
	route := config.Route{
		CName:    srv.Listener.Addr().String(),
		Protocol: "http",
	}

	assertNoGoroutineLeak(t, 200, func() error {
		_, _, err := app.GetAppHTTPStatus(route, false)
		return err
	})
}

// TestGetAppHTTPStatus_HTTPS_DoesNotLeakConnections exercises the default
// (HTTPS) branch of GetAppHTTPStatus, which builds a TLS-enabled Transport.
// TLS connections spawn the same persistConn readLoop/writeLoop goroutines
// as plain HTTP, so this path needs its own leak coverage.
func TestGetAppHTTPStatus_HTTPS_DoesNotLeakConnections(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	app := newTestAppForHTTPChecks("")
	route := config.Route{
		CName:    srv.Listener.Addr().String(),
		Protocol: "https",
	}

	assertNoGoroutineLeak(t, 200, func() error {
		_, _, err := app.GetAppHTTPStatus(route, false)
		return err
	})
}

// TestGetAppLocalHTTPStatus_FallbackDoesNotLeakConnections covers
// GetAppLocalHTTPStatus's HTTPS-to-HTTP fallback, which invokes
// GetAppHTTPStatus twice per logical check: the initial attempt fails the
// TLS handshake against a plain-HTTP backend (no persistConn is created on a
// failed handshake), then the HTTP fallback succeeds and establishes a
// keep-alive connection. Two GetAppHTTPStatus calls per iteration made this
// leak twice as fast as the plain success path, so it's worth its own case.
func TestGetAppLocalHTTPStatus_FallbackDoesNotLeakConnections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to split test server address: %v", err)
	}

	app := newTestAppForHTTPChecks(host)
	route := config.Route{
		Protocol: "https",
		Port:     port,
	}

	assertNoGoroutineLeak(t, 200, func() error {
		_, _, err := app.GetAppLocalHTTPStatus(route, false)
		return err
	})
}
