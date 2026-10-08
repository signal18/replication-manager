package cluster

import (
	"errors"
	"testing"
)

// Only the orchestrator's warn-state refusal triggers the abort + restart recovery.
func TestIsWarnObjectRefusal(t *testing.T) {
	if !isWarnObjectRefusal(errors.New(`unexpected status code: 409, body: {"detail":"failover object is warn state","status":409,"title":"set instance monitor"}`)) {
		t.Fatal("the 409 warn-state refusal must be recognised")
	}
	for _, e := range []error{nil, errors.New("connection refused"), errors.New("unexpected status code: 404, body: object not found")} {
		if isWarnObjectRefusal(e) {
			t.Fatalf("%v is not a warn-state refusal", e)
		}
	}
}

// The restart after an abort waits only on the orchestrator's "already in progress".
func TestIsOrchestrationInProgress(t *testing.T) {
	if !isOrchestrationInProgress(errors.New(`unexpected status code: 409, body: {"detail":"orchestration f965d4dc (>aborted) already in progress","status":409}`)) {
		t.Fatal("the in-progress refusal must be recognised")
	}
	if isOrchestrationInProgress(nil) || isOrchestrationInProgress(errors.New("failover object is warn state")) {
		t.Fatal("other errors are not an orchestration in progress")
	}
}
