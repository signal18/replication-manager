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
	app := newMonitoringTestApp([]config.Route{{Protocol: "bad", CName: "invalid", Port: "80", Primary: true}})

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppUnsupportedProto]; !ok {
		t.Fatalf("expected immediate %s", ErrAppUnsupportedProto)
	}
	expected := fmt.Sprintf(config.ClusterError[ErrAppUnsupportedProto], "bad", app.GetId())
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

	for i := 1; i <= 2; i++ {
		status := app.GetMonitoringStatus()
		if status != stateFailed {
			t.Fatalf("expected state %s on iteration %d, got %s", stateFailed, i, status)
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
	if status != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, status)
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
