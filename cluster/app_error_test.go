package cluster

import (
	"fmt"
	"net"
	"strconv"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func newTestApp() *App {
	return &App{Mutex: &sync.Mutex{}}
}

func newMonitoringTestApp(routes []config.Route) *App {
	return &App{
		Id:                   "app-test",
		Name:                 "app-test",
		Host:                 "127.0.0.1",
		Mutex:                &sync.Mutex{},
		ErrState:             make(map[string]state.State),
		AppErrConsecutiveMap: make(map[string]int),
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{Routes: routes},
		},
		ClusterGroup: &Cluster{
			Name: "test-cluster",
			Conf: &config.Config{Timeout: 1, AppErrorDebounceThreshold: 3},
		},
	}
}

func TestRecordAppErrorThreadSafe(t *testing.T) {
	app := newTestApp()

	const total = 100
	var wg sync.WaitGroup
	wg.Add(total)

	for i := 0; i < total; i++ {
		i := i
		go func() {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			app.RecordAppError(key, state.State{ErrKey: key})
		}()
	}

	wg.Wait()

	if len(app.ErrState) != total {
		t.Fatalf("expected %d error entries, got %d", total, len(app.ErrState))
	}
}

func TestResetAppErrorMultipleKeys(t *testing.T) {
	app := newTestApp()
	app.ErrState = map[string]state.State{
		"a": {ErrKey: "a"},
		"b": {ErrKey: "b"},
		"c": {ErrKey: "c"},
	}

	app.ResetAppError("a", "c", "missing")

	if len(app.ErrState) != 1 {
		t.Fatalf("expected 1 error entry remaining, got %d", len(app.ErrState))
	}
	if _, ok := app.ErrState["b"]; !ok {
		t.Fatalf("expected key 'b' to remain in error state")
	}
	if _, ok := app.ErrState["a"]; ok {
		t.Fatalf("expected key 'a' to be removed from error state")
	}
	if _, ok := app.ErrState["c"]; ok {
		t.Fatalf("expected key 'c' to be removed from error state")
	}
}

func TestClearAppErrorResetsMap(t *testing.T) {
	app := newTestApp()
	app.ErrState = map[string]state.State{
		"a": {ErrKey: "a"},
	}

	oldMap := app.ErrState
	app.ClearAppError()

	if len(app.ErrState) != 0 {
		t.Fatalf("expected cleared error state map, got %d entries", len(app.ErrState))
	}

	oldMap["old"] = state.State{ErrKey: "old"}
	if _, ok := app.ErrState["old"]; ok {
		t.Fatalf("expected new error state map after ClearAppError")
	}
}

func TestSetRouteStatusesThreadSafe(t *testing.T) {
	app := newTestApp()

	const total = 50
	var wg sync.WaitGroup
	wg.Add(total)

	for i := 0; i < total; i++ {
		i := i
		go func() {
			defer wg.Done()
			app.SetRouteStatuses([]config.RouteStatus{
				{
					Route:  config.Route{CName: fmt.Sprintf("app-%d", i), Port: "80", Protocol: "https"},
					Status: stateAppRunning,
				},
			})
		}()
	}

	wg.Wait()

	finalStatuses := []config.RouteStatus{{
		Route:  config.Route{CName: "final", Port: "443", Protocol: "https"},
		Status: stateAppRunning,
	}}
	app.SetRouteStatuses(finalStatuses)

	if len(app.RouteStatus) != 1 {
		t.Fatalf("expected 1 route status after final set, got %d", len(app.RouteStatus))
	}
	if app.RouteStatus[0].Route.CName != "final" {
		t.Fatalf("expected final route status to be stored")
	}
}

func TestGetMonitoringStatusDebouncesOncePerRouteInvocation(t *testing.T) {
	app := newMonitoringTestApp([]config.Route{{Protocol: "https", CName: "127.0.0.1:1", Port: "443", Primary: true}})

	for i := 1; i <= 2; i++ {
		status := app.GetMonitoringStatus()
		if status != stateFailed {
			t.Fatalf("expected state %s on iteration %d, got %s", stateFailed, i, status)
		}
		if _, ok := app.ErrState[ErrAppConnectFailed]; ok {
			t.Fatalf("did not expect %s before threshold, iteration %d", ErrAppConnectFailed, i)
		}
	}

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; !ok {
		t.Fatalf("expected %s at threshold", ErrAppConnectFailed)
	}
}

func TestGetMonitoringStatusNoRoutesEmitsImmediately(t *testing.T) {
	app := newMonitoringTestApp(nil)

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; !ok {
		t.Fatalf("expected immediate %s for no routes", ErrAppConnectFailed)
	}
}

func TestGetMonitoringStatusUnsupportedProtocolEmitsImmediately(t *testing.T) {
	route := config.Route{Protocol: "bad", CName: "invalid", Port: "80", Primary: true}
	app := newMonitoringTestApp([]config.Route{route})

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppUnsupportedProto]; !ok {
		t.Fatalf("expected immediate %s", ErrAppUnsupportedProto)
	}
	routeNorm := route
	routeNorm.Normalize()
	expected := fmt.Sprintf(config.ClusterError[ErrAppUnsupportedProto], "bad", app.GetId()) + " on route " + routeNorm.Label()
	if got := app.ErrState[ErrAppUnsupportedProto].ErrDesc; got != expected {
		t.Fatalf("unexpected %s description: got %q want %q", ErrAppUnsupportedProto, got, expected)
	}
}

func TestGetMonitoringStatusSuccessOnOneRouteDoesNotResetOtherRouteDebounce(t *testing.T) {
	app := newMonitoringTestApp(nil)
	app.ClusterGroup.Conf.AppErrorDebounceThreshold = 3

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test tcp listener: %v", err)
	}
	defer ln.Close()

	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	failRoute := config.Route{Protocol: "tcp", CName: "127.0.0.1", Port: "1", Primary: true}
	okRoute := config.Route{Protocol: "tcp", CName: "127.0.0.1", Port: port, Primary: false}
	app.AppConfig.Deployment.Routes = []config.Route{failRoute, okRoute}

	// Compute debounce keys the same way GetMonitoringStatus does.
	normFail := failRoute
	normFail.Normalize()
	failKey := config.BuildRouteStateKey(normFail)

	normOK := okRoute
	normOK.Normalize()
	okKey := config.BuildRouteStateKey(normOK)

	// With one local endpoint up and one down the aggregate is AppWarning, not Failed.
	for i := 1; i <= 2; i++ {
		status := app.GetMonitoringStatus()
		if status != stateAppWarning {
			t.Fatalf("expected state %s on iteration %d, got %s", stateAppWarning, i, status)
		}
		if _, ok := app.ErrState[ErrAppTCPConnectFailed]; ok {
			t.Fatalf("did not expect %s before threshold, iteration %d", ErrAppTCPConnectFailed, i)
		}
		if got := app.AppErrConsecutiveMap[failKey]; got != i {
			t.Fatalf("expected failing route counter=%d, got %d", i, got)
		}
		if _, ok := app.AppErrConsecutiveMap[okKey]; ok {
			t.Fatalf("did not expect successful route counter to be tracked")
		}
	}

	status := app.GetMonitoringStatus()
	if status != stateAppWarning {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateAppWarning, status)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; !ok {
		t.Fatalf("expected %s at threshold", ErrAppTCPConnectFailed)
	}
}

func TestGetMonitoringStatusNoRoutesClearsAllRouteDebounceCounters(t *testing.T) {
	app := newMonitoringTestApp([]config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: "1", Primary: true}})
	app.GetMonitoringStatus()
	if len(app.AppErrConsecutiveMap) == 0 {
		t.Fatalf("expected route debounce counters to be populated before reset")
	}

	app.AppConfig.Deployment.Routes = nil
	app.GetMonitoringStatus()
	if len(app.AppErrConsecutiveMap) != 0 {
		t.Fatalf("expected all route debounce counters to be cleared, got %d", len(app.AppErrConsecutiveMap))
	}
}

func TestGetMonitoringStatusTCPFailureDualEmissionWithConfigurableThreshold(t *testing.T) {
	app := newMonitoringTestApp([]config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: "1", Primary: true}})
	app.ClusterGroup.Conf.AppErrorDebounceThreshold = 2

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s on first iteration, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; ok {
		t.Fatalf("did not expect %s before threshold", ErrAppTCPConnectFailed)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; ok {
		t.Fatalf("did not expect %s before threshold", ErrAppConnectFailed)
	}

	status = app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; !ok {
		t.Fatalf("expected canonical %s at threshold", ErrAppTCPConnectFailed)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; !ok {
		t.Fatalf("expected compatibility %s at threshold", ErrAppConnectFailed)
	}
}

func TestGetMonitoringStatusDefaultDebounceThresholdIsThree(t *testing.T) {
	app := newMonitoringTestApp([]config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: "1", Primary: true}})
	app.ClusterGroup.Conf.AppErrorDebounceThreshold = 0

	for i := 1; i <= 2; i++ {
		status := app.GetMonitoringStatus()
		if status != stateFailed {
			t.Fatalf("expected state %s on iteration %d, got %s", stateFailed, i, status)
		}
		if _, ok := app.ErrState[ErrAppTCPConnectFailed]; ok {
			t.Fatalf("did not expect %s before default threshold, iteration %d", ErrAppTCPConnectFailed, i)
		}
	}

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; !ok {
		t.Fatalf("expected %s at default threshold", ErrAppTCPConnectFailed)
	}
}

// --- Aggregation behaviour tests ---

func TestGetMonitoringStatusTwoUniqueLocalsBothFail_IsFailed(t *testing.T) {
	// Both unique local endpoints are unreachable → aggregate must be Failed.
	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
		{Protocol: "tcp", CName: "127.0.0.2", Port: "2", DestinationPort: "2", Primary: false},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected %s when all local endpoints fail, got %s", stateFailed, status)
	}
}

func TestGetMonitoringStatusTwoUniquesOneLocalFails_IsAppWarning(t *testing.T) {
	// One local endpoint is up, one is down → aggregate must be AppWarning.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	openPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
		{Protocol: "tcp", CName: "127.0.0.1", Port: openPort, DestinationPort: openPort, Primary: false},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()
	if status != stateAppWarning {
		t.Fatalf("expected %s for partial local outage, got %s", stateAppWarning, status)
	}
}

func TestGetMonitoringStatusReverseOrderStillAppWarning(t *testing.T) {
	// Same as above but with routes in the reverse order to verify the result is
	// not sensitive to route ordering.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	openPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: openPort, DestinationPort: openPort, Primary: false},
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()
	if status != stateAppWarning {
		t.Fatalf("expected %s for partial local outage (reversed order), got %s", stateAppWarning, status)
	}
}

func TestGetMonitoringStatusLocalSucceedsExternalFails_IsAppWarning(t *testing.T) {
	// Local reachable, external unreachable → AppWarning.
	// Use separate Port (external) and DestinationPort (local) on the same route.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	localPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	// Port="1" is the external target; DestinationPort=localPort is the local target.
	route := config.Route{
		Protocol:        "tcp",
		CName:           "127.0.0.1",
		Port:            "1",
		DestinationPort: localPort,
		Primary:         true,
	}
	app := newMonitoringTestApp([]config.Route{route})

	status := app.GetMonitoringStatus()
	if status != stateAppWarning {
		t.Fatalf("expected %s when local ok but external fails, got %s", stateAppWarning, status)
	}
}

func TestGetMonitoringStatusAllChecksPass_IsRunning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	// Both local and external connect to the same open port.
	route := config.Route{Protocol: "tcp", CName: "127.0.0.1", Port: port, DestinationPort: port, Primary: true}
	app := newMonitoringTestApp([]config.Route{route})

	status := app.GetMonitoringStatus()
	if status != stateAppRunning {
		t.Fatalf("expected %s when all checks pass, got %s", stateAppRunning, status)
	}
}

func TestGetMonitoringStatusSharedLocalEndpointBothGetRouteStatus(t *testing.T) {
	// Two routes that share the same local endpoint (same DestinationPort) must
	// each appear in the RouteStatus slice even though the local check is run once.
	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
		{Protocol: "tcp", CName: "127.0.0.2", Port: "2", DestinationPort: "1", Primary: false},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected %s when shared local endpoint is down, got %s", stateFailed, status)
	}

	statuses := app.RouteStatus
	if len(statuses) != 2 {
		t.Fatalf("expected 2 route statuses, got %d", len(statuses))
	}
	for i, rs := range statuses {
		if rs.Status != stateFailed {
			t.Fatalf("route[%d]: expected status %s, got %s", i, stateFailed, rs.Status)
		}
	}
}

func TestGetMonitoringStatusRefreshPartialOutageIsAppWarning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	openPort := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
		{Protocol: "tcp", CName: "127.0.0.1", Port: openPort, DestinationPort: openPort, Primary: false},
	}
	app := newMonitoringTestApp(routes)

	if err := app.Refresh(); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if app.State != stateAppWarning {
		t.Fatalf("expected app state %s after partial local outage, got %s", stateAppWarning, app.State)
	}
}

// --- Local dedupe key policy tests ---
// Policy: http and https collapse to the same "http" local backend class
// (HAProxy terminates TLS; backend always speaks plain HTTP).
// Two routes only get separate local probes when the backend contract differs.

// TestBuildLocalCheckKeyHTTPAndHTTPSSameContractShareKey verifies that an http
// and an https route pointing at the same backend destination with the same
// monitor contract share a single local check key.
func TestBuildLocalCheckKeyHTTPAndHTTPSSameContractShareKey(t *testing.T) {
	httpsRoute := config.Route{Protocol: "https", Port: "443", DestinationPort: "8080"}
	httpRoute := config.Route{Protocol: "http", Port: "80", DestinationPort: "8080"}

	httpsKey := buildLocalCheckKey("127.0.0.1", httpsRoute)
	httpKey := buildLocalCheckKey("127.0.0.1", httpRoute)

	if httpsKey != httpKey {
		t.Fatalf("expected http and https routes with identical backend contract to share a local check key; got http=%q https=%q", httpKey, httpsKey)
	}
}

// TestBuildLocalCheckKeyDifferentMonitorPathNoDedupe verifies that routes with
// the same backend port but different monitor paths get distinct local check keys.
func TestBuildLocalCheckKeyDifferentMonitorPathNoDedupe(t *testing.T) {
	route1 := config.Route{Protocol: "https", DestinationPort: "8080", Monitor: &config.RouteMonitor{Path: "/health"}}
	route2 := config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{Path: "/status"}}

	key1 := buildLocalCheckKey("127.0.0.1", route1)
	key2 := buildLocalCheckKey("127.0.0.1", route2)

	if key1 == key2 {
		t.Fatalf("expected different monitor paths to produce distinct local check keys; both got %q", key1)
	}
}

// TestBuildLocalCheckKeyNilMonitorAndDefaultMonitorDedupe verifies that a nil
// monitor and a zero-value monitor struct produce the same local check key,
// preventing phantom duplicate probes from config variations.
func TestBuildLocalCheckKeyNilMonitorAndDefaultMonitorDedupe(t *testing.T) {
	routeNilMon := config.Route{Protocol: "https", DestinationPort: "8080"}
	routeEmptyMon := config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{}}

	keyNil := buildLocalCheckKey("127.0.0.1", routeNilMon)
	keyEmpty := buildLocalCheckKey("127.0.0.1", routeEmptyMon)

	if keyNil != keyEmpty {
		t.Fatalf("expected nil and zero-value monitor to produce the same local check key; got nil=%q empty=%q", keyNil, keyEmpty)
	}
}

// TestBuildLocalCheckKeyNormalizesMonitorContract verifies that monitor fields
// are normalized before keying so that semantically equivalent configs share
// a key: explicit default ExpectStatus, path without leading slash, and
// AuthType case variations must all produce the same key as their canonical forms.
func TestBuildLocalCheckKeyNormalizesMonitorContract(t *testing.T) {
	cases := []struct {
		name string
		r1   config.Route
		r2   config.Route
	}{
		{
			name: "explicit 200 ExpectStatus matches nil monitor default",
			r1:   config.Route{Protocol: "https", DestinationPort: "8080"},
			r2:   config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{ExpectStatus: "200"}},
		},
		{
			name: "monitor path without leading slash matches path with leading slash",
			r1:   config.Route{Protocol: "https", DestinationPort: "8080", Monitor: &config.RouteMonitor{Path: "/health"}},
			r2:   config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{Path: "health"}},
		},
		{
			name: "AuthType 'BASIC' matches 'basic'",
			r1:   config.Route{Protocol: "https", DestinationPort: "8080", Monitor: &config.RouteMonitor{AuthType: "BASIC", AuthUser: "u", AuthSecretVar: "s"}},
			r2:   config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{AuthType: "basic", AuthUser: "u", AuthSecretVar: "s"}},
		},
		{
			name: "expect-status list order does not affect key",
			r1:   config.Route{Protocol: "https", DestinationPort: "8080", Monitor: &config.RouteMonitor{ExpectStatus: "200,204"}},
			r2:   config.Route{Protocol: "http", DestinationPort: "8080", Monitor: &config.RouteMonitor{ExpectStatus: "204,200"}},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			k1 := buildLocalCheckKey("127.0.0.1", tc.r1)
			k2 := buildLocalCheckKey("127.0.0.1", tc.r2)
			if k1 != k2 {
				t.Fatalf("expected same key for semantically equivalent monitor configs; got %q and %q", k1, k2)
			}
		})
	}
}

// TestGetMonitoringStatusHTTPAndHTTPSSameBackendDeduped verifies the end-to-end
// deduplication: an http and an https route pointing at the same unreachable
// backend share a single local check, both receive Failed status, and the
// aggregate is Failed (all deduped local backends are down).
func TestGetMonitoringStatusHTTPAndHTTPSSameBackendDeduped(t *testing.T) {
	// Port 1 is unreachable; both routes point at it as the local backend.
	routes := []config.Route{
		{Protocol: "https", CName: "127.0.0.1", Port: "443", DestinationPort: "1", Primary: true},
		{Protocol: "http", CName: "127.0.0.1", Port: "80", DestinationPort: "1", Primary: false},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()

	// Single deduped local backend is down → all local backends failed → Failed.
	if status != stateFailed {
		t.Fatalf("expected %s when all deduped local backends are down, got %s", stateFailed, status)
	}
	if len(app.RouteStatus) != 2 {
		t.Fatalf("expected 2 route statuses, got %d", len(app.RouteStatus))
	}
	for i, rs := range app.RouteStatus {
		if rs.Status != stateFailed {
			t.Errorf("route[%d]: expected %s, got %s", i, stateFailed, rs.Status)
		}
	}
}

// TestGetMonitoringStatusUnsupportedPlusHealthyRouteIsAppWarning verifies the
// softer aggregate policy: an unsupported-protocol route combined with a
// healthy route yields AppWarning, not Failed.
func TestGetMonitoringStatusUnsupportedPlusHealthyRouteIsAppWarning(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test listener: %v", err)
	}
	defer ln.Close()
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)

	routes := []config.Route{
		{Protocol: "bad", CName: "127.0.0.1", Port: port, Primary: false},
		{Protocol: "tcp", CName: "127.0.0.1", Port: port, DestinationPort: port, Primary: true},
	}
	app := newMonitoringTestApp(routes)

	status := app.GetMonitoringStatus()
	if status != stateAppWarning {
		t.Fatalf("expected %s for unsupported+healthy route pair, got %s", stateAppWarning, status)
	}
}

func TestGetMonitoringStatusRefreshTotalOutageBecomesFailedAfterMaxFail(t *testing.T) {
	routes := []config.Route{
		{Protocol: "tcp", CName: "127.0.0.1", Port: "1", DestinationPort: "1", Primary: true},
		{Protocol: "tcp", CName: "127.0.0.1", Port: "2", DestinationPort: "2", Primary: false},
	}
	app := newMonitoringTestApp(routes)
	app.ClusterGroup.Conf.MaxFail = 1

	// First Refresh: FailCount(0) < MaxFail(1) → Suspect.
	if err := app.Refresh(); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if app.State != stateSuspect {
		t.Fatalf("expected %s on first total-outage refresh, got %s", stateSuspect, app.State)
	}

	// Second Refresh: FailCount(1) >= MaxFail(1) → Failed.
	if err := app.Refresh(); err != nil {
		t.Fatalf("Refresh returned error: %v", err)
	}
	if app.State != stateFailed {
		t.Fatalf("expected %s after max-fail threshold, got %s", stateFailed, app.State)
	}
}
