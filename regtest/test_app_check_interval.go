// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestAppCheckInterval is the real-loop regtest of the per-check interval (#2362,
// doc/implementation/cluster/APP_MONITOR_MODE.md "Check interval"): through the ticking
// monitor (maybeRefreshAppsAsync), a passing route check runs once per monitor.interval,
// not at every tick; a backend that goes down is seen at the next run and confirmed;
// while it fails it is checked at every tick, so its recovery is seen within a few ticks.
//
// The app has one TCP route on a local listener that counts the connections it accepts:
// each run of the route opens two (the local and the external check).
func (regtest *RegTest) TestAppCheckInterval(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
	}
	const interval = 20 // seconds, the route's monitor.interval

	var conns int64
	var mu sync.Mutex
	var ln net.Listener
	serve := func(l net.Listener) {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			atomic.AddInt64(&conns, 1)
			c.Close()
		}
	}
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logf(config.LvlErr, "TEST app-check-interval: FAIL could not start the listener: %s", err)
		return false
	}
	ln = first
	go serve(ln)
	port := strconv.Itoa(first.Addr().(*net.TCPAddr).Port)
	closeLn := func() {
		mu.Lock()
		defer mu.Unlock()
		if ln != nil {
			ln.Close()
			ln = nil
		}
	}
	reopen := func() error {
		var lastErr error
		for attempt := 0; attempt < 10; attempt++ {
			l, err := net.Listen("tcp", "127.0.0.1:"+port)
			if err == nil {
				mu.Lock()
				ln = l
				mu.Unlock()
				go serve(l)
				return nil
			}
			lastErr = err
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("could not rebind 127.0.0.1:%s: %w", port, lastErr)
	}

	host := "127.0.0.1"
	appcnf := cl.NewAppConfig(host, port)
	appcnf.Deployment.Routes = []config.Route{
		{Protocol: "tcp", CName: host, Port: port, DestinationPort: port, Primary: true, Monitor: &config.RouteMonitor{Interval: interval}},
	}
	app := &cluster.App{Name: "regtest-app-check-interval", Host: host, Port: port, AppConfig: appcnf, Mutex: &sync.Mutex{}}

	tick := time.Duration(cl.Conf.MonitoringTicker) * time.Second
	if tick <= 0 {
		tick = 2 * time.Second
	}
	appAdded := false
	defer func() {
		if appAdded {
			waitForNextCompletedRefreshInProgressClear(app, 10*time.Second)
			if err := cl.RemoveAppMonitor(host, port); err != nil {
				logf(config.LvlWarn, "TEST app-check-interval: teardown: RemoveAppMonitor failed: %s", err)
			}
			time.Sleep(tick + 2*time.Second)
			if app.Datadir != "" {
				os.RemoveAll(app.Datadir)
			}
		}
		closeLn()
	}()
	if err := cl.AddApp(app); err != nil {
		logf(config.LvlErr, "TEST app-check-interval: FAIL AddApp: %s", err)
		return false
	}
	appAdded = true

	threshold := cl.Conf.AppErrorDebounceThreshold
	if threshold <= 0 {
		threshold = 3
	}
	waitState := func(timeout time.Duration, ok func(string) bool) (time.Duration, bool) {
		start := time.Now()
		res := proxyReadBackendWaitFor(timeout, func() bool { return ok(app.GetState()) })
		return time.Since(start), res
	}
	running := func(s string) bool { return s == appStateRunningForTest }

	if _, ok := waitState(tick*10+30*time.Second, running); !ok {
		logf(config.LvlErr, "TEST app-check-interval: FAIL the app did not settle on %s, got %s", appStateRunningForTest, app.GetState())
		return false
	}
	logf("TEST", "app-check-interval: app settled on %s -- OK", appStateRunningForTest)

	// 1) Passing: over 50 s, at most ceil(50/20)+1 = 4 runs (8 connections); at every tick
	// it would be 25 runs (50 connections).
	window := 50 * time.Second
	atomic.StoreInt64(&conns, 0)
	time.Sleep(window)
	got := atomic.LoadInt64(&conns)
	runsAtEveryTick := int64(window/tick) * 2
	if got < 2 || got > 10 {
		logf(config.LvlErr, "TEST app-check-interval: FAIL %d connections in %s with a %d s interval (want 2-10; every tick would be about %d)", got, window, interval, runsAtEveryTick)
		return false
	}
	logf("TEST", "app-check-interval: %d connections in %s on a %d s interval (every tick: about %d) -- OK", got, window, interval, runsAtEveryTick)

	// 2) Down: seen at the next run (at most one interval), then confirmed.
	closeLn()
	limit := time.Duration(interval)*time.Second + time.Duration(threshold+cl.Conf.MaxFail+3)*tick + 15*time.Second
	took, ok := waitState(limit, func(s string) bool { return !running(s) })
	if !ok {
		logf(config.LvlErr, "TEST app-check-interval: FAIL the backend is down and the app is still %s after %s", app.GetState(), limit)
		return false
	}
	logf("TEST", "app-check-interval: backend down -> %s after %s (limit %s) -- OK", app.GetState(), took.Round(time.Second), limit)

	// 3) Back up: a failing check runs at every tick, so the recovery is seen within a
	// few ticks, not after an interval.
	if err := reopen(); err != nil {
		logf(config.LvlErr, "TEST app-check-interval: FAIL %s", err)
		return false
	}
	recoverLimit := 3*tick + 5*time.Second
	took, ok = waitState(recoverLimit, running)
	if !ok {
		logf(config.LvlErr, "TEST app-check-interval: FAIL the backend is back and the app is still %s after %s", app.GetState(), recoverLimit)
		return false
	}
	logf("TEST", "app-check-interval: backend back -> %s after %s (limit %s, interval %d s) -- OK", appStateRunningForTest, took.Round(time.Second), recoverLimit, interval)
	return true
}
