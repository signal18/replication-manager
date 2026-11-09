// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// countingListener accepts TCP connections on 127.0.0.1 and counts them.
func countingListener(t *testing.T) (port string, conns *int64, stop func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var n int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&n, 1)
			c.Close()
		}
	}()
	return strconv.Itoa(ln.Addr().(*net.TCPAddr).Port), &n, func() { ln.Close() }
}

// ageCheckResults moves every last check result back by d, as if d had elapsed.
func ageCheckResults(app *App, d time.Duration) {
	app.Lock()
	defer app.Unlock()
	for k, r := range app.CheckResults {
		r.at = r.at.Add(-d)
		app.CheckResults[k] = r
	}
}

// A passing check runs once per interval: the ticks in between reuse its result (#2362).
func TestAppCheckIntervalNoRouteProbeRunsOncePerInterval(t *testing.T) {
	port, conns, stop := countingListener(t)
	defer stop()
	app := newMonitoringTestApp(nil)
	app.AppConfig.AppPort = port

	for i := 0; i < 5; i++ {
		if st := app.GetMonitoringStatus(); st != stateAppRunning {
			t.Fatalf("tick %d: want %s, got %s", i, stateAppRunning, st)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(conns); got != 1 {
		t.Fatalf("5 ticks within the interval: want 1 probe, got %d", got)
	}

	ageCheckResults(app, appMonitorDefaultInterval)
	app.GetMonitoringStatus()
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(conns); got != 2 {
		t.Fatalf("after the interval: want 2 probes, got %d", got)
	}
}

// A failing check runs at every tick: the debounce reaches its threshold in 3 ticks, not
// 3 intervals, and the recovery is seen at the next tick.
func TestAppCheckIntervalFailingCheckRunsEveryTick(t *testing.T) {
	port, _, stop := countingListener(t)
	app := newMonitoringTestApp(nil)
	app.AppConfig.AppPort = port
	if st := app.GetMonitoringStatus(); st != stateAppRunning {
		t.Fatalf("want %s while the port answers, got %s", stateAppRunning, st)
	}
	stop()
	// Still within the interval: the last (passing) result stands.
	if st := app.GetMonitoringStatus(); st != stateAppRunning {
		t.Fatalf("within the interval: want the last result %s, got %s", stateAppRunning, st)
	}
	ageCheckResults(app, appMonitorDefaultInterval)
	var st string
	for i := 0; i < 3; i++ {
		st = app.GetMonitoringStatus()
	}
	if st != stateFailed {
		t.Fatalf("3 ticks after the failure: want %s, got %s", stateFailed, st)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; !ok {
		t.Fatalf("want %s raised after 3 ticks", ErrAppTCPConnectFailed)
	}

	// Recovery on the same port: seen at the next tick, no interval to wait.
	ln, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Skipf("port %s taken again by another process: %v", port, err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if st := app.GetMonitoringStatus(); st != stateAppRunning {
		t.Fatalf("next tick after recovery: want %s, got %s", stateAppRunning, st)
	}
}

// A TCP route: its local and external checks follow the route's monitor interval.
func TestAppCheckIntervalTCPRouteUsesMonitorInterval(t *testing.T) {
	port, conns, stop := countingListener(t)
	defer stop()
	route := config.Route{Protocol: "tcp", CName: "127.0.0.1", Port: port, Primary: true, Monitor: &config.RouteMonitor{Interval: 300}}
	app := newMonitoringTestApp([]config.Route{route})
	app.AppConfig.AppPort = port

	if st := app.GetMonitoringStatus(); st != stateAppRunning {
		t.Fatalf("want %s, got %s", stateAppRunning, st)
	}
	time.Sleep(50 * time.Millisecond)
	first := atomic.LoadInt64(conns)
	if first == 0 {
		t.Fatalf("first tick: want the route probed")
	}
	// Past the app default (60 s) but within the route's 300 s: nothing runs.
	ageCheckResults(app, 2*appMonitorDefaultInterval)
	for i := 0; i < 3; i++ {
		app.GetMonitoringStatus()
	}
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(conns); got != first {
		t.Fatalf("within the route's 300 s: want %d probes, got %d", first, got)
	}
	ageCheckResults(app, 300*time.Second)
	app.GetMonitoringStatus()
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt64(conns); got != 2*first {
		t.Fatalf("after the route's interval: want %d probes, got %d", 2*first, got)
	}
}

func TestAppCheckIntervalPrecedence(t *testing.T) {
	cnf := &config.AppConfig{}
	route := config.Route{}
	if got := routeCheckInterval(cnf, route); got != appMonitorDefaultInterval {
		t.Fatalf("nothing set: want %s, got %s", appMonitorDefaultInterval, got)
	}
	cnf.AppMonitorInterval = 30
	if got := routeCheckInterval(cnf, route); got != 30*time.Second {
		t.Fatalf("app-monitor-interval 30: want 30s, got %s", got)
	}
	route.Monitor = &config.RouteMonitor{Interval: 10}
	if got := routeCheckInterval(cnf, route); got != 10*time.Second {
		t.Fatalf("route monitor interval 10: want 10s, got %s", got)
	}
	if got := appCheckInterval(nil); got != appMonitorDefaultInterval {
		t.Fatalf("nil config: want %s, got %s", appMonitorDefaultInterval, got)
	}
}

// Results of checks that no longer exist are dropped: one entry per current check.
func TestAppCheckIntervalDropsRemovedChecks(t *testing.T) {
	port, _, stop := countingListener(t)
	defer stop()
	app := newMonitoringTestApp([]config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: port, Primary: true}})
	app.AppConfig.AppPort = port
	app.GetMonitoringStatus()
	if len(app.CheckResults) == 0 {
		t.Fatalf("want the route's check results kept")
	}
	app.AppConfig.Deployment.Routes = nil
	app.GetMonitoringStatus()
	if len(app.CheckResults) != 1 {
		t.Fatalf("route removed: want only the app-port check left, got %v", app.CheckResults)
	}
	if _, ok := app.CheckResults["app-port"]; !ok {
		t.Fatalf("route removed: want the app-port check, got %v", app.CheckResults)
	}
}

func TestRouteMonitorNormalizeNegativeInterval(t *testing.T) {
	m := config.RouteMonitor{Interval: -5}
	m.Normalize()
	if m.Interval != 0 {
		t.Fatalf("negative interval: want 0 (the app's), got %d", m.Interval)
	}
}
