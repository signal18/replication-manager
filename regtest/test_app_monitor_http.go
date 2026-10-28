// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// TestAppMonitorHTTP (#2209): a monitor-only app (added without image nor template) in
// app-monitor-mode http, through the real monitoring loop:
//  1. on a web service that answers, the app settles on AppRunning;
//  2. when the service keeps accepting connections but stops answering, the app goes
//     Failed with APPERR001 after the debounce (port and ping would stay green);
//  3. when it answers again, the app comes back to AppRunning;
//  4. provision of the monitor-only app is refused before the orchestrator.
//
// The web service is a listener of this process on 127.0.0.1 (a full name, called as is).
// Nothing is deployed and nothing is published on the gateway.
func (regtest *RegTest) TestAppMonitorHTTP(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
	}

	var hang atomic.Bool
	release := make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL could not start the web service: %s", err)
		return false
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hang.Load() {
			// accepted, never answered while hanging
			select {
			case <-release:
			case <-r.Context().Done():
			}
			return
		}
		if r.URL.Path != "/health" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	})}
	go srv.Serve(ln)
	host := "127.0.0.1"
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	added := false
	defer func() {
		close(release)
		if added {
			if err := cl.RemoveAppMonitor(host, port); err != nil {
				logf(config.LvlWarn, "TEST app-monitor-http: teardown: RemoveAppMonitor failed: %s", err)
			}
		}
		srv.Close()
	}()

	if err := cl.AddSeededApp(host, port, "", ""); err != nil {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL adding a monitor-only app: %s", err)
		return false
	}
	added = true
	app := cl.GetAppByConfig(&config.AppConfig{AppHost: host, AppPort: port})
	if app == nil {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL the added app is not in the cluster")
		return false
	}
	if !app.AppConfig.IsMonitorOnly() {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL an app added without image nor template is not monitor-only")
		return false
	}
	for k, v := range map[string]string{"app-monitor-mode": "http", "app-monitor-path": "/health"} {
		if err := app.SetSetting(k, v); err != nil {
			logf(config.LvlErr, "TEST app-monitor-http: FAIL setting %s: %s", k, err)
			return false
		}
	}

	tick := time.Duration(cl.Conf.MonitoringTicker) * time.Second
	if tick <= 0 {
		tick = 2 * time.Second
	}
	threshold := cl.Conf.AppErrorDebounceThreshold
	if threshold <= 0 {
		threshold = 3
	}
	// every cycle of a hanging service lasts the cluster timeout
	wait := time.Duration(threshold+3)*(tick+time.Duration(cl.Conf.Timeout)*time.Second) + 30*time.Second
	waitFor := func(want string) bool {
		return proxyReadBackendWaitFor(wait, func() bool { return app.GetState() == want })
	}
	hasErr := func(key string) bool {
		app.Lock()
		defer app.Unlock()
		_, ok := app.ErrState[key]
		return ok
	}

	if !waitFor(appStateRunningForTest) {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL the app did not settle on %s within %s, got %s", appStateRunningForTest, wait, app.GetState())
		return false
	}
	logf("TEST", "app-monitor-http: answering service -> %s -- OK", appStateRunningForTest)

	hang.Store(true)
	if !waitFor("Failed") || !hasErr(cluster.ErrAppConnectFailed) {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL a service that never answers did not open Failed with %s within %s, got %s", cluster.ErrAppConnectFailed, wait, app.GetState())
		return false
	}
	logf("TEST", "app-monitor-http: hanging service (port open) -> Failed with %s -- OK", cluster.ErrAppConnectFailed)

	hang.Store(false)
	if !waitFor(appStateRunningForTest) || hasErr(cluster.ErrAppConnectFailed) {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL the app did not recover to %s within %s, got %s", appStateRunningForTest, wait, app.GetState())
		return false
	}
	logf("TEST", "app-monitor-http: service answering again -> %s -- OK", appStateRunningForTest)

	if err := cl.InitAppService(app); err == nil || !strings.Contains(err.Error(), "monitor-only") {
		logf(config.LvlErr, "TEST app-monitor-http: FAIL provision of a monitor-only app was not refused: %v", err)
		return false
	}
	logf("TEST", "app-monitor-http: provision of a monitor-only app refused -- OK")
	return true
}
