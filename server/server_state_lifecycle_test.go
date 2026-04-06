package server

import (
	"testing"

	"github.com/signal18/replication-manager/utils/state"
)

func testLifecycleState(key, desc string) state.State {
	return state.State{
		ErrKey:  key,
		ErrType: "WARNING",
		ErrDesc: desc,
		ErrFrom: "TEST",
	}
}

func assertLifecycleTransitions(t *testing.T, repman *ReplicationManager, opened, resolved int) {
	t.Helper()

	gotOpened := len(repman.StateMachine.GetLastOpenedStates())
	if gotOpened != opened {
		t.Fatalf("unexpected opened transition count: got %d, want %d", gotOpened, opened)
	}

	gotResolved := len(repman.StateMachine.GetLastResolvedStates())
	if gotResolved != resolved {
		t.Fatalf("unexpected resolved transition count: got %d, want %d", gotResolved, resolved)
	}
}

func TestProcessAlertStateLifecycleNilSafe(t *testing.T) {
	repman := &ReplicationManager{}

	// Should not panic when StateMachine is nil.
	repman.ProcessAlertStateLifecycle()
}

func TestProcessAlertStateLifecycleOpenedOnceAndStaleResolution(t *testing.T) {
	repman := &ReplicationManager{}
	repman.InitAlertStateMachine()

	// Cycle 1: first open.
	repman.SetState("WARN0001", testLifecycleState("WARN0001", "first open"))
	assertLifecycleTransitions(t, repman, 1, 0)
	repman.ProcessAlertStateLifecycle()

	if !repman.StateMachine.IsInState("WARN0001") {
		t.Fatal("expected WARN0001 to be open after cycle 1")
	}

	// Cycle 2: unchanged input (re-added) should not re-open.
	repman.SetState("WARN0001", testLifecycleState("WARN0001", "first open"))
	assertLifecycleTransitions(t, repman, 0, 0)
	repman.ProcessAlertStateLifecycle()

	if !repman.StateMachine.IsInState("WARN0001") {
		t.Fatal("expected WARN0001 to remain open after unchanged cycle")
	}

	// Cycle 3: stale state (not re-added) resolves.
	assertLifecycleTransitions(t, repman, 0, 1)
	repman.ProcessAlertStateLifecycle()

	if repman.StateMachine.IsInState("WARN0001") {
		t.Fatal("expected WARN0001 to resolve when no longer re-added")
	}
}

func TestProcessAlertStateLifecyclePreservedStateSurvivesRollover(t *testing.T) {
	repman := &ReplicationManager{}
	repman.InitAlertStateMachine()

	// Cycle 1: open state.
	repman.SetState("WARN0002", testLifecycleState("WARN0002", "preserved warning"))
	repman.ProcessAlertStateLifecycle()

	// Cycle 2: preserve without re-adding should keep it open.
	repman.PreserveState("WARN0002")
	assertLifecycleTransitions(t, repman, 0, 0)
	repman.ProcessAlertStateLifecycle()

	if !repman.StateMachine.IsInState("WARN0002") {
		t.Fatal("expected WARN0002 to remain open after PreserveState")
	}

	// Cycle 3: without preserve/re-add, state resolves.
	assertLifecycleTransitions(t, repman, 0, 1)
	repman.ProcessAlertStateLifecycle()

	if repman.StateMachine.IsInState("WARN0002") {
		t.Fatal("expected WARN0002 to resolve after preserve stops")
	}
}

func TestProcessAlertStateLifecycleNoOpOnRepeatedUnchangedCycles(t *testing.T) {
	repman := &ReplicationManager{}
	repman.InitAlertStateMachine()

	// Initial opening cycle.
	repman.SetState("WARN0003", testLifecycleState("WARN0003", "stable warning"))
	repman.ProcessAlertStateLifecycle()

	for i := 0; i < 3; i++ {
		repman.SetState("WARN0003", testLifecycleState("WARN0003", "stable warning"))
		assertLifecycleTransitions(t, repman, 0, 0)
		repman.ProcessAlertStateLifecycle()

		if !repman.StateMachine.IsInState("WARN0003") {
			t.Fatalf("expected WARN0003 to stay open in unchanged cycle %d", i+1)
		}
	}
}
