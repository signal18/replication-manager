// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// Each app check runs on its own interval (#2362). The apps are refreshed at every
// monitoring tick, but a check that passed is run again only once its interval has
// elapsed; in between, its last result stands. A check that failed, or any check of an
// app whose failures are still being debounced, runs at every tick, so a failure is
// confirmed in a few ticks and a recovery is seen at the next one.

// appMonitorDefaultInterval: app-monitor-interval when unset.
const appMonitorDefaultInterval = 60 * time.Second

// appCheckSlack: a check is due up to this much before its interval has elapsed, so the
// tick's jitter never pushes a run to the next tick (an interval equal to the monitoring
// ticker runs at every tick; a 60 s interval runs at 59-61 s, not at 62).
const appCheckSlack = time.Second

// appCheckResult is the outcome of one check run: err nil = passed. status is the HTTP
// status (0 for TCP, ping), errKey the APPERR key of a failure.
type appCheckResult struct {
	sig    string // the check's configuration when it ran: another one makes it due
	at     time.Time
	err    error
	status int
	errKey string
}

// appCheckInterval: the interval of the app's own check (app without a route), and the
// default of its routes.
func appCheckInterval(cnf *config.AppConfig) time.Duration {
	if cnf != nil && cnf.AppMonitorInterval > 0 {
		return time.Duration(cnf.AppMonitorInterval) * time.Second
	}
	return appMonitorDefaultInterval
}

// routeCheckInterval: a route's monitor interval, else the app's.
func routeCheckInterval(cnf *config.AppConfig, route config.Route) time.Duration {
	if route.Monitor != nil && route.Monitor.Interval > 0 {
		return time.Duration(route.Monitor.Interval) * time.Second
	}
	return appCheckInterval(cnf)
}

// runCheck returns the result of the check named key: its last result while it is not
// due, else a fresh run of probe. Due = never run, its configuration (sig) changed since,
// failed last time, a failure of the app still debouncing, or its interval elapsed.
func (app *App) runCheck(key, sig string, interval time.Duration, now time.Time, probe func() appCheckResult) appCheckResult {
	app.Lock()
	last, ok := app.CheckResults[key]
	due := !ok || last.sig != sig || last.err != nil || len(app.AppErrConsecutiveMap) > 0 || now.Sub(last.at)+appCheckSlack >= interval
	app.Unlock()
	if !due {
		return last
	}
	res := probe()
	res.sig = sig
	res.at = now
	app.Lock()
	if app.CheckResults == nil {
		app.CheckResults = make(map[string]appCheckResult)
	}
	app.CheckResults[key] = res
	app.Unlock()
	return res
}

// noRouteCheckSig: the configuration of the check of an app without a route.
func noRouteCheckSig(cnf *config.AppConfig, host string) string {
	if cnf == nil {
		return host
	}
	return strings.Join([]string{host, cnf.AppPort, cnf.AppMonitorMode, cnf.AppMonitorPath, cnf.AppMonitorExpectStatus, strconv.Itoa(cnf.AppMonitorCertExpireDays)}, "|")
}

// keepCheckResults drops the results of checks that no longer exist (a route removed or
// changed): the map holds one entry per current check, never more.
func (app *App) keepCheckResults(keys map[string]bool) {
	app.Lock()
	defer app.Unlock()
	for k := range app.CheckResults {
		if !keys[k] {
			delete(app.CheckResults, k)
		}
	}
}
