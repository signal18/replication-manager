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
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// These mirror the unexported cluster.stateAppRunning/stateAppWarning string
// constants (cluster/srv.go) -- the regtest package can't reference them
// directly, and App.State's JSON value is the same literal a dashboard or API
// consumer would compare against anyway.
const (
	appStateRunningForTest = "AppRunning"
	appStateWarningForTest = "AppWarning"
)

// waitForNextCompletedRefresh polls app's race-free API view
// (App.GetAppAPIView, taken entirely under app.Lock()) until it observes a
// refresh cycle that both started strictly after `after` and has finished
// (RefreshInProgress == false), or the timeout elapses.
//
// This is what lets the test assert exactly what state the Nth completed
// refresh cycle landed on, independent of the real ticker/batch cadence,
// ambient cluster load, or single-flight skips in maybeRefreshAppsAsync
// (cluster/cluster_app.go): rather than guessing how much wall-clock time N
// ticks should take and asserting against that guess, this waits for the
// actual completion *event*. Keying on LastRefreshStart rather than
// LastRefreshEnd matters: SetRefreshInProgress(true) is called before the
// checks run and SetRefreshResult (which updates LastRefreshStart/End
// together, see cluster/app.go's Refresh()) only after they finish, so a
// cycle whose LastRefreshStart is after `after` is guaranteed to have run
// GetMonitoringStatus() no earlier than `after` -- unlike LastRefreshEnd
// alone, which a cycle that straddled `after` (started slightly before,
// finished slightly after) could also satisfy.
func waitForNextCompletedRefresh(app *cluster.App, after time.Time, timeout time.Duration) (*cluster.AppAPIView, bool) {
	deadline := time.Now().Add(timeout)
	var last *cluster.AppAPIView
	for {
		view := app.GetAppAPIView()
		last = view
		if !view.RefreshInProgress && view.LastRefreshStart.After(after) {
			return view, true
		}
		if time.Now().After(deadline) {
			return last, false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TestAppWarningDebounceAndRecovery is the real-cluster regtest for the App
// health-check debounce/recovery fix
// (doc/implementation/cluster/APP_WARNING_DEBOUNCE.md): a single transient
// check failure must not commit App.State to AppWarning, `threshold`
// consecutive warning-worthy cycles must, and recovery back to AppRunning
// must be immediate (committed on the very next completed cycle, not
// debounced).
//
// This only exercises the AppRunning <-> AppWarning path. It deliberately
// never lets the aggregate reach Failed/Suspect: one of the two routes below
// is backed by a listener that stays up for the entire test, so at least one
// unique local endpoint is always reachable and GetMonitoringStatus can only
// return AppRunning or AppWarning (cluster/app_chk.go: Failed requires every
// unique local endpoint down). The Failed/Suspect/MaxFail debounce is
// pre-existing behavior, unchanged by and out of scope for this fix.
//
// It cannot see the ALERT/ALERTOK log line or level: cluster.tlog/htlog are
// unexported and there is no public "recent log" accessor, and adding one
// only for this test would be scope creep. That part is covered at the unit
// level instead, where it can be asserted precisely without log-capture
// plumbing: cluster/app_error_test.go's TestAppTransitionAlertLevel and
// config/log_level_eligibility_test.go's ALERTOK eligibility tests. What
// this regtest validates that no unit test can is that the debounce and
// recovery actually happen correctly through the REAL, ticking monitoring
// loop (cluster.maybeRefreshAppsAsync), not through direct, synchronous
// Refresh() calls.
//
// Rather than mutating App.AppConfig.Deployment.Routes on the live,
// registered App (GetMonitoringStatus reads that slice unlocked -- see
// app_chk.go -- so mutating it from this goroutine while
// maybeRefreshAppsAsync's background worker concurrently reads it on the
// same App would be a genuine data race), this drives every transition by
// opening/closing real TCP listeners on fixed local ports the Route config
// points at for the whole test. That is also more realistic: it simulates an
// actual backend going down and back up, exactly what AppWarning/AppRunning
// are meant to detect.
func (regtest *RegTest) TestAppWarningDebounceAndRecovery(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, level, format, args...)
	}

	// routeA's backend stays up for the entire test -- see the func comment.
	lnA, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL could not start the always-up listener: %s", err)
		return false
	}
	portA := strconv.Itoa(lnA.Addr().(*net.TCPAddr).Port)

	// routeB's backend is the one this test takes down and brings back up.
	lnB, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		lnA.Close()
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL could not start the flapping listener: %s", err)
		return false
	}
	portB := strconv.Itoa(lnB.Addr().(*net.TCPAddr).Port)
	lnBOpen := true
	closeB := func() {
		if lnBOpen {
			lnB.Close()
			lnBOpen = false
		}
	}
	// reopenB rebinds the exact same port: Go's listening sockets set
	// SO_REUSEADDR, so this is expected to succeed immediately, but a real
	// backend restarting can occasionally lose the race with a lingering
	// TIME_WAIT socket -- retry briefly rather than flake the whole test on
	// that.
	reopenB := func() error {
		var reopenErr error
		for attempt := 0; attempt < 10; attempt++ {
			ln, err := net.Listen("tcp", "127.0.0.1:"+portB)
			if err == nil {
				lnB = ln
				lnBOpen = true
				return nil
			}
			reopenErr = err
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("could not rebind 127.0.0.1:%s after retrying: %w", portB, reopenErr)
	}

	appHost := "127.0.0.1"
	appcnf := cl.NewAppConfig(appHost, portA)
	appcnf.Deployment.Routes = []config.Route{
		{Protocol: "tcp", CName: appHost, Port: portA, DestinationPort: portA, Primary: true},
		{Protocol: "tcp", CName: appHost, Port: portB, DestinationPort: portB, Primary: false},
	}

	app := &cluster.App{
		Name:      "regtest-app-warning-debounce",
		Host:      appHost,
		Port:      portA,
		AppConfig: appcnf,
		Mutex:     &sync.Mutex{},
	}
	appAdded := false

	// Single ordered teardown, deferred once so it runs on every return path
	// (including early failures) in the same fixed order regardless of where
	// the function returns from:
	//  1. wait for any in-flight refresh of this app to finish, best-effort,
	//     so RemoveAppMonitor doesn't race a worker still using this App;
	//  2. remove the app from the cluster's monitored list;
	//  3. a short grace sleep for a batch that had already snapshotted
	//     cluster.Apps (cluster/cluster_app.go's maybeRefreshAppsAsync takes
	//     that snapshot under cluster.Lock() at batch start, before this
	//     goroutine's RemoveAppMonitor call could be observed) to finish --
	//     this narrows, but with no drain API exposed for apps, cannot
	//     perfectly eliminate, the removal race;
	//  4. remove the app's generated data directory (AddApp/SetDataDir
	//     creates app.Datadir with log/var/init/bck subdirectories) so
	//     repeated runs don't accumulate residue;
	//  5. close whichever listener(s) are still open.
	tick := time.Duration(cl.Conf.MonitoringTicker) * time.Second
	if tick <= 0 {
		tick = 2 * time.Second
	}
	defer func() {
		if appAdded {
			if !waitForNextCompletedRefreshInProgressClear(app, 10*time.Second) {
				logf(config.LvlWarn, "TEST app-warning-debounce: teardown: a refresh was still in progress after waiting -- removing anyway")
			}
			if err := cl.RemoveAppMonitor(appHost, portA); err != nil {
				logf(config.LvlWarn, "TEST app-warning-debounce: teardown: RemoveAppMonitor failed: %s -- removing %s manually", err, app.Datadir)
			}
			time.Sleep(tick + 2*time.Second)
			if app.Datadir != "" {
				if err := os.RemoveAll(app.Datadir); err != nil {
					logf(config.LvlWarn, "TEST app-warning-debounce: teardown: could not remove %s: %s", app.Datadir, err)
				}
			}
		}
		closeB()
		lnA.Close()
	}()

	if err := cl.AddApp(app); err != nil {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL AddApp: %s", err)
		return false
	}
	appAdded = true

	threshold := cl.Conf.AppErrorDebounceThreshold
	if threshold <= 0 {
		threshold = 3
	}
	// Generous per-step ceiling: correctness below never depends on this
	// value (each step waits for an actual completion event, not a computed
	// deadline), it only bounds how long the test is willing to wait before
	// concluding the loop genuinely hung.
	cycleTimeout := tick*10 + 30*time.Second

	waitForAppState := func(want string, timeout time.Duration) bool {
		return proxyReadBackendWaitFor(timeout, func() bool { return app.GetState() == want })
	}

	// 0) Both routes healthy: must settle on AppRunning. This step alone is
	// exempt from the "wait for a specific completed cycle" discipline used
	// below -- there is no prior perturbation to anchor a reference
	// timestamp to, so it just waits for eventual convergence.
	if !waitForAppState(appStateRunningForTest, cycleTimeout) {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL app did not settle on %s within %s, got %s",
			appStateRunningForTest, cycleTimeout, app.GetState())
		return false
	}
	logf("TEST", "app-warning-debounce: app settled on %s -- OK", appStateRunningForTest)

	// 1) Transient blip: one warning-worthy cycle that recovers before the
	// debounce threshold must never commit AppWarning. Meaningless when
	// threshold <= 1 (there is no sub-threshold window to prove anything
	// about), so it's skipped in that configuration, matching the analogous
	// guard in cluster/app_error_test.go's interruption-reset unit test.
	if threshold > 1 {
		closeB()
		blipStart := time.Now()
		view, ok := waitForNextCompletedRefresh(app, blipStart, cycleTimeout)
		if !ok {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL did not observe a completed refresh after the transient blip within %s", cycleTimeout)
			return false
		}
		if view.State != appStateRunningForTest {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL a single transient warning cycle committed %s (expected to remain %s)", view.State, appStateRunningForTest)
			return false
		}

		if err := reopenB(); err != nil {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL could not bring the backend back up after the transient blip: %s", err)
			return false
		}
		recoverStart := time.Now()
		view, ok = waitForNextCompletedRefresh(app, recoverStart, cycleTimeout)
		if !ok {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL did not observe a completed refresh after recovering from the transient blip within %s", cycleTimeout)
			return false
		}
		if view.State != appStateRunningForTest {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL state was %s after a transient blip that recovered before the debounce threshold", view.State)
			return false
		}
		logf("TEST", "app-warning-debounce: a transient (sub-threshold) warning cycle did not commit %s -- OK", appStateWarningForTest)
	} else {
		logf("TEST", "app-warning-debounce: threshold=1, skipping the transient-blip phase (no sub-threshold window exists)")
	}

	// 2) Sustained outage: step through completed cycles one at a time.
	// State must remain AppRunning for exactly the first threshold-1
	// completed warning cycles, then commit AppWarning on the threshold-th.
	closeB()
	after := time.Now()
	for i := 1; i < threshold; i++ {
		view, ok := waitForNextCompletedRefresh(app, after, cycleTimeout)
		if !ok {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL did not observe completed warning cycle %d/%d within %s", i, threshold, cycleTimeout)
			return false
		}
		if view.State != appStateRunningForTest {
			logf(config.LvlErr, "TEST app-warning-debounce: FAIL state committed to %s too early, on cycle %d/%d (expected to remain %s until cycle %d)",
				view.State, i, threshold, appStateRunningForTest, threshold)
			return false
		}
		after = view.LastRefreshStart
	}
	finalView, ok := waitForNextCompletedRefresh(app, after, cycleTimeout)
	if !ok {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL did not observe the threshold-committing cycle %d/%d within %s", threshold, threshold, cycleTimeout)
		return false
	}
	if finalView.State != appStateWarningForTest {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL expected %s committed on cycle %d/%d, got %s",
			appStateWarningForTest, threshold, threshold, finalView.State)
		return false
	}
	logf("TEST", "app-warning-debounce: %s stayed %s for cycles 1-%d and committed %s on cycle %d/%d -- debounce is cycle-accurate -- OK",
		app.Name, appStateRunningForTest, threshold-1, appStateWarningForTest, threshold, threshold)

	// 3) Recovery: bringing the backend back must commit AppRunning on the
	// very next completed refresh -- not debounced, regardless of threshold.
	if err := reopenB(); err != nil {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL could not bring the backend back up: %s", err)
		return false
	}
	recoverStart := time.Now()
	view, ok := waitForNextCompletedRefresh(app, recoverStart, cycleTimeout)
	if !ok {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL did not observe a completed refresh after recovery within %s", cycleTimeout)
		return false
	}
	if view.State != appStateRunningForTest {
		logf(config.LvlErr, "TEST app-warning-debounce: FAIL expected immediate recovery to %s on the very next completed refresh, got %s",
			appStateRunningForTest, view.State)
		return false
	}
	logf("TEST", "app-warning-debounce: recovered to %s on the very next completed refresh -- immediate, not debounced -- OK", appStateRunningForTest)

	return true
}

// waitForNextCompletedRefreshInProgressClear is the teardown-time cousin of
// waitForNextCompletedRefresh: it only needs "no refresh of this app is
// currently running", not "a specific cycle happened", so it polls
// RefreshInProgress directly without a reference timestamp.
func waitForNextCompletedRefreshInProgressClear(app *cluster.App, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if !app.GetAppAPIView().RefreshInProgress {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
