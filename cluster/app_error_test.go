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
		Id:       "app-test",
		Name:     "app-test",
		Host:     "127.0.0.1",
		Mutex:    &sync.Mutex{},
		ErrState: make(map[string]state.State),
		AppConfig: &config.AppConfig{
			Deployment: &config.Deployment{Routes: routes},
		},
		ClusterGroup: &Cluster{
			Name: "test-cluster",
			Conf: &config.Config{Timeout: 1},
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

func TestGetMonitoringStatusDebouncesAppErrorsAtThreeFailures(t *testing.T) {
	app := newMonitoringTestApp(nil)

	for i := 1; i <= 2; i++ {
		state := app.GetMonitoringStatus()
		if state != stateFailed {
			t.Fatalf("expected state %s on iteration %d, got %s", stateFailed, i, state)
		}
		if _, ok := app.ErrState[ErrAppConnectFailed]; ok {
			t.Fatalf("did not expect %s before threshold, iteration %d", ErrAppConnectFailed, i)
		}
	}

	state := app.GetMonitoringStatus()
	if state != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, state)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; !ok {
		t.Fatalf("expected %s at threshold", ErrAppConnectFailed)
	}
}

func TestGetMonitoringStatusResetsFailureCounterOnSuccessfulRouteCheck(t *testing.T) {
	app := newMonitoringTestApp([]config.Route{{Protocol: "bad", CName: "invalid", Port: "80", Primary: true}})

	app.GetMonitoringStatus()
	app.GetMonitoringStatus()
	if app.AppErrConsecutiveCnt != 2 {
		t.Fatalf("expected debounce counter to be 2, got %d", app.AppErrConsecutiveCnt)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start test tcp listener: %v", err)
	}
	defer ln.Close()

	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	app.AppConfig.Deployment.Routes = []config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: port, Primary: true}}

	state := app.GetMonitoringStatus()
	if state != stateAppRunning {
		t.Fatalf("expected state %s after successful check, got %s", stateAppRunning, state)
	}
	if app.AppErrConsecutiveCnt != 0 {
		t.Fatalf("expected debounce counter reset after success, got %d", app.AppErrConsecutiveCnt)
	}

	app.AppConfig.Deployment.Routes = []config.Route{{Protocol: "bad", CName: "invalid", Port: "80", Primary: true}}
	app.GetMonitoringStatus()
	if app.AppErrConsecutiveCnt != 1 {
		t.Fatalf("expected debounce counter restart from 1 after success reset, got %d", app.AppErrConsecutiveCnt)
	}
	if _, ok := app.ErrState[ErrAppUnsupportedProto]; ok {
		t.Fatalf("did not expect %s immediately after reset", ErrAppUnsupportedProto)
	}
}

func TestGetMonitoringStatusMapsTCPConnectFailureToAPPERR001(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate test tcp port: %v", err)
	}
	port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
	ln.Close()

	app := newMonitoringTestApp([]config.Route{{Protocol: "tcp", CName: "127.0.0.1", Port: port, Primary: true}})
	// Seed legacy key to ensure app checks clear it under the new mapping.
	app.RecordAppError(ErrAppTCPConnectFailed, state.State{ErrType: "WARN", ErrKey: ErrAppTCPConnectFailed})

	for i := 1; i <= 2; i++ {
		status := app.GetMonitoringStatus()
		if status != stateFailed {
			t.Fatalf("expected state %s on iteration %d, got %s", stateFailed, i, status)
		}
		if _, ok := app.ErrState[ErrAppConnectFailed]; ok {
			t.Fatalf("did not expect %s before threshold, iteration %d", ErrAppConnectFailed, i)
		}
		if _, ok := app.ErrState[ErrAppTCPConnectFailed]; ok {
			t.Fatalf("did not expect legacy key %s to persist, iteration %d", ErrAppTCPConnectFailed, i)
		}
	}

	status := app.GetMonitoringStatus()
	if status != stateFailed {
		t.Fatalf("expected state %s on threshold iteration, got %s", stateFailed, status)
	}
	if _, ok := app.ErrState[ErrAppConnectFailed]; !ok {
		t.Fatalf("expected %s at threshold", ErrAppConnectFailed)
	}
	if _, ok := app.ErrState[ErrAppTCPConnectFailed]; ok {
		t.Fatalf("did not expect %s to be emitted for tcp failures", ErrAppTCPConnectFailed)
	}
}
