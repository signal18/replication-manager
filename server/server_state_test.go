package server

import (
	"testing"

	"github.com/signal18/replication-manager/utils/state"
)

func TestInitAlertStateMachine(t *testing.T) {
	repman := &ReplicationManager{}

	// Before init, StateMachine should be nil
	if repman.StateMachine != nil {
		t.Fatalf("expected nil StateMachine before InitAlertStateMachine, got %v", repman.StateMachine)
	}

	// Init
	repman.InitAlertStateMachine()

	// After init, StateMachine should be non-nil
	if repman.StateMachine == nil {
		t.Fatal("expected non-nil StateMachine after InitAlertStateMachine")
	}
}

func TestInitAlertStateMachineIdempotent(t *testing.T) {
	repman := &ReplicationManager{}

	// Init twice should not panic
	repman.InitAlertStateMachine()
	repman.InitAlertStateMachine()

	if repman.StateMachine == nil {
		t.Fatal("expected non-nil StateMachine after double InitAlertStateMachine")
	}
}

func TestGetStateMachine(t *testing.T) {
	repman := &ReplicationManager{}

	// Before init, GetStateMachine should return nil
	if repman.GetStateMachine() != nil {
		t.Fatal("expected nil GetStateMachine before init")
	}

	// Init
	repman.InitAlertStateMachine()

	// After init, GetStateMachine should return the state machine
	sm := repman.GetStateMachine()
	if sm == nil {
		t.Fatal("expected non-nil GetStateMachine after init")
	}
	if sm != repman.StateMachine {
		t.Fatal("GetStateMachine should return the same StateMachine instance")
	}
}

func TestSetState(t *testing.T) {
	repman := &ReplicationManager{}
	repman.InitAlertStateMachine()

	// Set a state
	repman.SetState("TEST_KEY", state.State{
		ErrKey:  "TEST_KEY",
		ErrType: "WARNING",
		ErrDesc: "Test description",
		ErrFrom: "TEST",
	})

	// Verify state is in the state machine
	states := repman.StateMachine.GetOpenStates()
	if len(states) != 1 {
		t.Fatalf("expected 1 open state, got %d", len(states))
	}
	if states[0].ErrKey != "TEST_KEY" {
		t.Fatalf("expected ErrKey TEST_KEY, got %s", states[0].ErrKey)
	}
}

func TestSetStateNilSafe(t *testing.T) {
	repman := &ReplicationManager{}

	// Should not panic when StateMachine is nil
	repman.SetState("TEST_KEY", state.State{
		ErrKey:  "TEST_KEY",
		ErrType: "WARNING",
		ErrDesc: "Test description",
		ErrFrom: "TEST",
	})
}

func TestPreserveState(t *testing.T) {
	repman := &ReplicationManager{}
	repman.InitAlertStateMachine()

	// Set a state in cycle 1
	repman.SetState("PRESERVE_KEY", state.State{
		ErrKey:  "PRESERVE_KEY",
		ErrType: "WARNING",
		ErrDesc: "Preserve me",
		ErrFrom: "TEST",
	})

	// ClearState moves CurState to OldState
	repman.StateMachine.ClearState()

	// At this point, PRESERVE_KEY is in OldState, CurState is empty

	// Now before setting new states, preserve the key
	repman.PreserveState("PRESERVE_KEY")

	// After PreserveState, PRESERVE_KEY should be in CurState
	states := repman.StateMachine.GetOpenStates()
	found := false
	for _, s := range states {
		if s.ErrKey == "PRESERVE_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected PRESERVE_KEY to be in CurState after PreserveState")
	}

	// Now add another state for the new cycle
	repman.SetState("NEW_KEY", state.State{
		ErrKey:  "NEW_KEY",
		ErrType: "WARNING",
		ErrDesc: "New state",
		ErrFrom: "TEST",
	})

	// ClearState moves CurState to OldState (both PRESERVE_KEY and NEW_KEY)
	repman.StateMachine.ClearState()

	// After ClearState, CurState is empty, OldState has both
	// Preserve again - PRESERVE_KEY should come back
	repman.PreserveState("PRESERVE_KEY")

	// Check that PRESERVE_KEY is back in CurState
	states = repman.StateMachine.GetOpenStates()
	found = false
	for _, s := range states {
		if s.ErrKey == "PRESERVE_KEY" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected PRESERVE_KEY to be preserved after second ClearState + PreserveState")
	}
}

func TestPreserveStateNilSafe(t *testing.T) {
	repman := &ReplicationManager{}

	// Should not panic when StateMachine is nil
	repman.PreserveState("ANY_KEY")
}
