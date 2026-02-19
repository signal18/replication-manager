package cluster

import (
	"fmt"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func newTestApp() *App {
	return &App{Mutex: &sync.Mutex{}}
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
