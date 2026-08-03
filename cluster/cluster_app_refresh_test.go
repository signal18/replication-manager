package cluster

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// newAppRefreshTestApp builds an App wired to cluster, following the shape
// of newMonitoringTestApp (app_error_test.go) but sharing the caller's
// Cluster instance so cluster.Apps can hold several apps pointing back at
// the same cluster -- the same shape production uses (needed to exercise
// maybeRefreshAppsAsync's batch iteration and aggregation).
func newAppRefreshTestApp(cluster *Cluster, name string, routes []config.Route) *App {
	return &App{
		Id:                   name,
		Name:                 name,
		Host:                 "127.0.0.1",
		Mutex:                &sync.Mutex{},
		ErrState:             make(map[string]state.State),
		AppErrConsecutiveMap: make(map[string]int),
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{Routes: routes},
		},
		ClusterGroup: cluster,
	}
}

// blockingHTTPServer returns a test server whose handler signals on entered
// (once per request) and then blocks until block is closed. Closing block
// releases the current and all future requests (reads from a closed channel
// never block), so a single close is enough to let a whole Refresh() call
// (which may hit the handler more than once -- local then external check)
// run to completion.
func blockingHTTPServer(entered chan<- struct{}, block <-chan struct{}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-block
		w.WriteHeader(http.StatusOK)
	}))
}

// httpRouteFor builds a route that makes both the local and external check
// hit srv: Host+DestinationPort is what GetAppLocalHTTPStatus dials, CName
// is what the external GetAppHTTPStatus call dials directly.
func httpRouteFor(srv *httptest.Server) config.Route {
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		panic(err)
	}
	return config.Route{Protocol: "http", CName: "127.0.0.1:" + port, DestinationPort: port, Primary: true}
}

func TestMaybeRefreshAppsAsync_SingleFlight(t *testing.T) {
	entered := make(chan struct{}, 8)
	block := make(chan struct{})
	var hits int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		entered <- struct{}{}
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cluster := &Cluster{
		Name: "single-flight",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 1},
	}
	app := newAppRefreshTestApp(cluster, "app1", []config.Route{httpRouteFor(srv)})
	cluster.Apps = []*App{app}

	done := make(chan struct{})
	go func() {
		cluster.maybeRefreshAppsAsync()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the batch to start checking the app")
	}

	// A second call while the first batch is still stuck mid-check must be a
	// no-op: it should return immediately and must not touch the app itself
	// (which would show up as extra hits on the blocked handler).
	start := time.Now()
	cluster.maybeRefreshAppsAsync()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("expected an overlapping call to return immediately (single-flight guard), took %s", elapsed)
	}
	if got := atomic.LoadInt64(&hits); got != 1 {
		t.Fatalf("expected exactly 1 check hit while the first batch is in flight, got %d", got)
	}

	close(block)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the batch to finish")
	}

	// One full Refresh() on one http route hits the handler twice: once for
	// the local check, once for the external check.
	if got := atomic.LoadInt64(&hits); got != 2 {
		t.Fatalf("expected exactly 2 total check hits from a single batch, got %d", got)
	}
	if cluster.AppRefreshLastAppCount != 1 {
		t.Fatalf("expected AppRefreshLastAppCount=1, got %d", cluster.AppRefreshLastAppCount)
	}
	if !cluster.AppRefreshLastSuccess {
		t.Fatalf("expected AppRefreshLastSuccess=true")
	}
	if atomic.LoadUint64(&cluster.appRefreshEpoch) != 1 {
		t.Fatalf("expected appRefreshEpoch=1 after one completed batch, got %d", cluster.appRefreshEpoch)
	}
}

// TestMaybeRefreshAppsAsync_ConcurrentAppListRebuildNoRace guards against
// cluster.Apps being reassigned wholesale (as newAppList/cluster_del.go do,
// under cluster.Lock()) while maybeRefreshAppsAsync is iterating it. Before
// the fix, the fan-out loop read cluster.Apps directly with no lock -- the
// same class of bug already found and fixed in GetAppsSubstitutionJSon
// (cluster/app_get.go). Run with -race.
func TestMaybeRefreshAppsAsync_ConcurrentAppListRebuildNoRace(t *testing.T) {
	cluster := &Cluster{
		Name: "list-rebuild",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 2},
	}
	app1 := newAppRefreshTestApp(cluster, "app1", nil)
	app2 := newAppRefreshTestApp(cluster, "app2", nil)
	cluster.Apps = []*App{app1, app2}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Mutator: repeatedly rebuilds cluster.Apps under cluster.Lock(),
	// mirroring newAppList's atomic swap.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				cluster.Lock()
				cluster.Apps = []*App{app1, app2}
				cluster.Unlock()
			}
		}
	}()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		cluster.maybeRefreshAppsAsync()
	}

	close(stop)
	wg.Wait()
}

// TestMaybeRefreshAppsAsync_SkippedCount asserts AppRefreshSkippedCount
// increments once per rejected overlapping launch, stays visible after the
// batch that saw the rejections finishes (not reset at completion -- that
// would race against the deferred appRefreshInProgress clear, see
// maybeRefreshAppsAsync), and resets to 0 only once the next batch starts.
func TestMaybeRefreshAppsAsync_SkippedCount(t *testing.T) {
	entered := make(chan struct{}, 8)
	block := make(chan struct{})

	srv := blockingHTTPServer(entered, block)
	defer srv.Close()

	cluster := &Cluster{
		Name: "skipped-count",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 1},
	}
	app := newAppRefreshTestApp(cluster, "app1", []config.Route{httpRouteFor(srv)})
	cluster.Apps = []*App{app}

	done := make(chan struct{})
	go func() {
		cluster.maybeRefreshAppsAsync()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the batch to start checking the app")
	}

	const rejectedAttempts = 3
	for i := 0; i < rejectedAttempts; i++ {
		cluster.maybeRefreshAppsAsync()
	}
	if got := cluster.AppRefreshSkippedCount; got != rejectedAttempts {
		t.Fatalf("expected AppRefreshSkippedCount=%d after %d overlapping calls, got %d", rejectedAttempts, rejectedAttempts, got)
	}

	close(block)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the batch to finish")
	}

	// Finishing the batch must NOT reset the count -- it stays visible for
	// inspection until the next batch starts.
	if got := cluster.AppRefreshSkippedCount; got != rejectedAttempts {
		t.Fatalf("expected AppRefreshSkippedCount to remain %d after the batch finishes, got %d", rejectedAttempts, got)
	}

	// Starting the next batch resets it.
	cluster.maybeRefreshAppsAsync()
	if got := cluster.AppRefreshSkippedCount; got != 0 {
		t.Fatalf("expected AppRefreshSkippedCount reset to 0 once the next batch starts, got %d", got)
	}
}

func TestMaybeRefreshAppsAsync_AtomicPublish(t *testing.T) {
	entered := make(chan struct{}, 8)
	block := make(chan struct{})
	slowSrv := blockingHTTPServer(entered, block)
	defer slowSrv.Close()

	cluster := &Cluster{
		Name: "atomic-publish",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 2, AppErrorDebounceThreshold: 1},
	}
	slowApp := newAppRefreshTestApp(cluster, "slow", []config.Route{httpRouteFor(slowSrv)})
	failApp := newAppRefreshTestApp(cluster, "fail", []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
	})
	cluster.Apps = []*App{slowApp, failApp}

	done := make(chan struct{})
	go func() {
		cluster.maybeRefreshAppsAsync()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the slow app's check to start")
	}

	// The fast-failing app should finish (and record its own live error)
	// well before the slow app is unblocked.
	deadline := time.Now().Add(2 * time.Second)
	recorded := false
	for time.Now().Before(deadline) {
		failApp.Lock()
		_, recorded = failApp.ErrState[ErrAppTCPConnectFailed]
		failApp.Unlock()
		if recorded {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !recorded {
		t.Fatal("expected the fast-failing app to have already recorded its own error while the slow app is still blocked")
	}

	// Even though one app in the batch is fully done, the cluster-wide
	// published snapshot must not reflect a partial batch.
	if snap := cluster.publishedAppErrStates.Load(); snap != nil {
		t.Fatalf("expected no published snapshot while the batch is still in flight, got %v", *snap)
	}

	close(block)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the batch to finish")
	}

	snap := cluster.publishedAppErrStates.Load()
	if snap == nil {
		t.Fatal("expected a published snapshot once the batch completes")
	}
	wantKey := state.BuildStateKey(ErrAppTCPConnectFailed, failApp.Host)
	if _, ok := (*snap)[wantKey]; !ok {
		t.Fatalf("expected the published snapshot to contain %s from the completed batch", wantKey)
	}
}

// TestMaybeRefreshAppsAsync_PreservesPerAppErrorsWithSameCode is a
// regression test for the async-refresh aggregation collapsing per-app
// state: two different apps raising the exact same error code must both
// survive into the published snapshot, keyed by app (ServerUrl), not
// overwrite each other under the bare error code.
func TestMaybeRefreshAppsAsync_PreservesPerAppErrorsWithSameCode(t *testing.T) {
	cluster := &Cluster{
		Name: "same-code-per-app",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 2, AppErrorDebounceThreshold: 1},
	}
	failAppA := newAppRefreshTestApp(cluster, "fail-a", []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
	})
	failAppA.Host = "app-a.example.com"
	failAppB := newAppRefreshTestApp(cluster, "fail-b", []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
	})
	failAppB.Host = "app-b.example.com"
	cluster.Apps = []*App{failAppA, failAppB}

	cluster.maybeRefreshAppsAsync()

	snap := cluster.publishedAppErrStates.Load()
	if snap == nil {
		t.Fatal("expected a published snapshot")
	}

	keyA := state.BuildStateKey(ErrAppTCPConnectFailed, failAppA.Host)
	keyB := state.BuildStateKey(ErrAppTCPConnectFailed, failAppB.Host)
	if keyA == keyB {
		t.Fatalf("test setup error: expected distinct keys, got %q for both apps", keyA)
	}
	stA, okA := (*snap)[keyA]
	stB, okB := (*snap)[keyB]
	if !okA || !okB {
		t.Fatalf("expected both apps' %s errors to survive aggregation, got snapshot %v", ErrAppTCPConnectFailed, *snap)
	}
	if stA.ServerUrl != failAppA.Host || stB.ServerUrl != failAppB.Host {
		t.Fatalf("expected each entry to retain its own app's ServerUrl, got %q and %q", stA.ServerUrl, stB.ServerUrl)
	}
}

func TestMaybeRefreshAppsAsync_FreshnessFields(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	cluster := &Cluster{
		Name: "freshness",
		Conf: &config.Config{Timeout: 5, AppRefreshConcurrency: 2},
	}
	app := newAppRefreshTestApp(cluster, "app1", []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: strconv.Itoa(port), DestinationPort: strconv.Itoa(port), Primary: true},
	})
	cluster.Apps = []*App{app}

	before := time.Now()
	cluster.maybeRefreshAppsAsync()
	after := time.Now()

	if cluster.AppRefreshLastStart.Before(before) || cluster.AppRefreshLastStart.After(after) {
		t.Fatalf("AppRefreshLastStart %s not within [%s, %s]", cluster.AppRefreshLastStart, before, after)
	}
	if cluster.AppRefreshLastEnd.Before(cluster.AppRefreshLastStart) {
		t.Fatalf("AppRefreshLastEnd %s before AppRefreshLastStart %s", cluster.AppRefreshLastEnd, cluster.AppRefreshLastStart)
	}
	if cluster.AppRefreshLastDurationMs < 0 {
		t.Fatalf("expected non-negative AppRefreshLastDurationMs, got %d", cluster.AppRefreshLastDurationMs)
	}
	if !cluster.AppRefreshLastSuccess {
		t.Fatalf("expected AppRefreshLastSuccess=true")
	}
	if cluster.AppRefreshLastAppCount != 1 {
		t.Fatalf("expected AppRefreshLastAppCount=1, got %d", cluster.AppRefreshLastAppCount)
	}
	if cluster.appRefreshInProgress.Load() {
		t.Fatalf("expected appRefreshInProgress=false after batch completion")
	}
}

func TestEmitAppErrors_ReadsPublishedSnapshotNotLiveApps(t *testing.T) {
	cluster := &Cluster{
		Name:         "emit-errors",
		Conf:         &config.Config{},
		StateMachine: &state.StateMachine{},
	}
	cluster.StateMachine.Init()

	// A live app with an error that has NOT been published yet -- e.g. a
	// batch that's still in flight. EmitAppErrors must not see this.
	liveOnlyApp := newAppRefreshTestApp(cluster, "live-only", nil)
	liveOnlyApp.ErrState[ErrAppTCPConnectFailed] = state.State{ErrKey: ErrAppTCPConnectFailed, ErrDesc: "should not be emitted"}
	cluster.Apps = []*App{liveOnlyApp}

	published := map[string]state.State{
		ErrAppConnectFailed: {ErrKey: ErrAppConnectFailed, ErrDesc: "published batch error"},
	}
	cluster.publishedAppErrStates.Store(&published)

	cluster.EmitAppErrors()

	if !cluster.StateMachine.IsInState(ErrAppConnectFailed) {
		t.Fatalf("expected %s from the published snapshot to be emitted", ErrAppConnectFailed)
	}
	if cluster.StateMachine.IsInState(ErrAppTCPConnectFailed) {
		t.Fatalf("expected %s (live-only, unpublished) to NOT be emitted", ErrAppTCPConnectFailed)
	}
}

func TestEmitAppErrors_NoPublishedSnapshotYetIsNoop(t *testing.T) {
	cluster := &Cluster{
		Name:         "emit-errors-cold",
		Conf:         &config.Config{},
		StateMachine: &state.StateMachine{},
	}
	cluster.StateMachine.Init()

	// Should not panic and should not emit anything before any batch has
	// ever completed.
	cluster.EmitAppErrors()

	if cluster.StateMachine.IsInState(ErrAppConnectFailed) {
		t.Fatalf("did not expect any state before the first published batch")
	}
}

// TestEmitAppErrors_EmitsPerAppStateForSameErrorCode is a regression test
// covering the full publish -> emit path: a published snapshot holding the
// same error code for two different apps (composite-keyed, as
// maybeRefreshAppsAsync now produces) must result in both apps' states
// landing in the cluster-wide state machine, not just one.
func TestEmitAppErrors_EmitsPerAppStateForSameErrorCode(t *testing.T) {
	cluster := &Cluster{
		Name:         "emit-errors-per-app",
		Conf:         &config.Config{},
		StateMachine: &state.StateMachine{},
	}
	cluster.StateMachine.Init()

	hostA := "app-a.example.com"
	hostB := "app-b.example.com"
	published := map[string]state.State{
		state.BuildStateKey(ErrAppUnexpectedStatus, hostA): {ErrKey: ErrAppUnexpectedStatus, ServerUrl: hostA, ErrDesc: "app A unexpected status"},
		state.BuildStateKey(ErrAppUnexpectedStatus, hostB): {ErrKey: ErrAppUnexpectedStatus, ServerUrl: hostB, ErrDesc: "app B unexpected status"},
	}
	cluster.publishedAppErrStates.Store(&published)

	cluster.EmitAppErrors()

	keyA := state.BuildStateKey(ErrAppUnexpectedStatus, hostA)
	keyB := state.BuildStateKey(ErrAppUnexpectedStatus, hostB)
	if !cluster.StateMachine.IsInState(keyA) {
		t.Fatalf("expected app A's %s to be emitted under %s", ErrAppUnexpectedStatus, keyA)
	}
	if !cluster.StateMachine.IsInState(keyB) {
		t.Fatalf("expected app B's %s to be emitted under %s", ErrAppUnexpectedStatus, keyB)
	}
}

func TestAppRefreshStaleness(t *testing.T) {
	now := time.Now()
	threshold := 20 * time.Second

	cases := []struct {
		name       string
		inProgress bool
		lastStart  time.Time
		lastEnd    time.Time
		wantStale  bool
		wantStuck  bool
	}{
		{
			name:      "never run yet",
			wantStale: false,
			wantStuck: false,
		},
		{
			name:      "recently completed, not stale",
			lastEnd:   now.Add(-1 * time.Second),
			wantStale: false,
			wantStuck: false,
		},
		{
			name:      "completed long ago, stale",
			lastEnd:   now.Add(-1 * time.Hour),
			wantStale: true,
			wantStuck: false,
		},
		{
			name:       "in progress, started recently, not stuck",
			inProgress: true,
			lastStart:  now.Add(-1 * time.Second),
			wantStale:  false,
			wantStuck:  false,
		},
		{
			name:       "in progress, started long ago, stuck",
			inProgress: true,
			lastStart:  now.Add(-1 * time.Hour),
			wantStale:  false,
			wantStuck:  true,
		},
		{
			name:       "in progress overrides a stale prior completion",
			inProgress: true,
			lastStart:  now.Add(-1 * time.Second),
			lastEnd:    now.Add(-1 * time.Hour),
			wantStale:  false,
			wantStuck:  false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stale, stuck := appRefreshStaleness(tc.inProgress, tc.lastStart, tc.lastEnd, now, threshold)
			if stale != tc.wantStale || stuck != tc.wantStuck {
				t.Fatalf("appRefreshStaleness() = (stale=%v, stuck=%v), want (stale=%v, stuck=%v)",
					stale, stuck, tc.wantStale, tc.wantStuck)
			}
		})
	}
}

// TestAppRefreshStaleThreshold guards two fixes for a flat threshold not
// scaling with app count:
//
//  1. Worst-case batch duration is roughly
//     ceil(apps/AppRefreshConcurrency) x routesPerApp x Conf.Timeout, which
//     at even 5 apps and default settings can exceed a flat
//     10xMonitoringTicker floor under a plausible worst case, producing
//     false "stuck" warnings on a batch that's still working normally.
//  2. Bound (1)'s fix -- scaling off the *last observed* batch duration --
//     still lags by one batch when the fleet just grew (e.g. 1 app -> 11
//     apps): the last duration reflects the old, small fleet, so the first
//     larger batch would still false-positive before any history catches
//     up. The current-fleet-size bound must react immediately, using the
//     current snapshot's app count rather than history.
func TestAppRefreshStaleThreshold(t *testing.T) {
	cases := []struct {
		name             string
		monitoringTicker int64
		appCount         int
		concurrency      int
		timeoutSeconds   int
		lastDurationMs   int64
		want             time.Duration
	}{
		{
			name:             "no batch completed yet: flat floor",
			monitoringTicker: 2,
			appCount:         1,
			concurrency:      2,
			timeoutSeconds:   5,
			lastDurationMs:   0,
			want:             20 * time.Second,
		},
		{
			name:             "fast batch, small fleet: flat floor still wins",
			monitoringTicker: 2,
			appCount:         1,
			concurrency:      2,
			timeoutSeconds:   5,
			lastDurationMs:   1000, // 4x = 4s, less than the 20s floor
			want:             20 * time.Second,
		},
		{
			name:             "slow observed batch: adaptive-from-history wins",
			monitoringTicker: 2,
			appCount:         1,
			concurrency:      2,
			timeoutSeconds:   5,
			lastDurationMs:   25000, // 4x = 100s, more than the 20s floor
			want:             100 * time.Second,
		},
		{
			name:             "fleet just grew, no history yet: current-size bound wins",
			monitoringTicker: 2,
			appCount:         11,
			concurrency:      2,
			timeoutSeconds:   5,
			lastDurationMs:   200, // still reflects the old 1-app fleet
			// ceil(11/2)=6 rounds x 5s x4 = 120s
			want: 120 * time.Second,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := appRefreshStaleThreshold(tc.monitoringTicker, tc.appCount, tc.concurrency, tc.timeoutSeconds, tc.lastDurationMs)
			if got != tc.want {
				t.Fatalf("appRefreshStaleThreshold(%d, %d, %d, %d, %d) = %s, want %s",
					tc.monitoringTicker, tc.appCount, tc.concurrency, tc.timeoutSeconds, tc.lastDurationMs, got, tc.want)
			}
		})
	}
}

// TestAppRefresh_TracksInProgressDuringSlowCheck guards the App-level
// (not batch-level) RefreshInProgress/LastRefreshDurationMs fields:
// RefreshInProgress must be observably true for the whole duration of a
// slow Refresh() call, and LastRefreshDurationMs must reflect that it
// actually blocked, not just that Refresh() was called.
func TestAppRefresh_TracksInProgressDuringSlowCheck(t *testing.T) {
	entered := make(chan struct{}, 4)
	block := make(chan struct{})
	srv := blockingHTTPServer(entered, block)
	defer srv.Close()

	cluster := &Cluster{
		Name: "app-refresh-tracking",
		Conf: &config.Config{Timeout: 5},
	}
	app := newAppRefreshTestApp(cluster, "app1", []config.Route{httpRouteFor(srv)})

	done := make(chan struct{})
	go func() {
		app.Refresh()
		close(done)
	}()

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the refresh to start checking the app")
	}

	app.Lock()
	inProgress := app.RefreshInProgress
	app.Unlock()
	if !inProgress {
		t.Fatalf("expected RefreshInProgress=true while Refresh() is still running")
	}

	close(block)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Refresh() to finish")
	}

	app.Lock()
	inProgress = app.RefreshInProgress
	lastDurationMs := app.LastRefreshDurationMs
	app.Unlock()
	if inProgress {
		t.Fatalf("expected RefreshInProgress=false after Refresh() finishes")
	}
	if lastDurationMs <= 0 {
		t.Fatalf("expected positive LastRefreshDurationMs for a check that blocked, got %d", lastDurationMs)
	}
}
