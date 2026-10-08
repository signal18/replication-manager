package cluster

import (
	"errors"
	"testing"
	"time"
)

// Only the orchestrator's warn-state refusal triggers the abort + restart recovery.
func TestIsWarnObjectRefusal(t *testing.T) {
	if !isWarnObjectRefusal(errors.New(`unexpected status code: 409, body: {"detail":"failover object is warn state","status":409,"title":"set instance monitor"}`)) {
		t.Fatal("the 409 warn-state refusal must be recognised")
	}
	for _, e := range []error{nil, errors.New("connection refused"), errors.New("unexpected status code: 404, body: object not found"), errors.New(`unexpected status code: 409, body: {"detail":"orchestration x already in progress","status":409,"title":"set instance monitor"}`)} {
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

// retryWhenIdle aborts once on "already in progress" and retries until the action passes;
// any other error comes back untouched without an abort.
func TestRetryWhenIdle(t *testing.T) {
	busy := errors.New(`unexpected status code: 409, body: {"detail":"orchestration x (>restarted) already in progress","status":409}`)
	aborts, calls := 0, 0
	err := retryWhenIdle(func() error { aborts++; return nil }, func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	}, 30*time.Second)
	if err != nil || aborts != 1 || calls != 3 {
		t.Fatalf("want one abort and the third call accepted: err=%v aborts=%d calls=%d", err, aborts, calls)
	}
	other := errors.New("object not found")
	aborts, calls = 0, 0
	if err := retryWhenIdle(func() error { aborts++; return nil }, func() error { calls++; return other }, time.Second); err != other || aborts != 0 || calls != 1 {
		t.Fatalf("another error returns at once without abort: err=%v aborts=%d calls=%d", err, aborts, calls)
	}
	aborts, calls = 0, 0
	if err := retryWhenIdle(func() error { aborts++; return nil }, func() error { calls++; return busy }, 3*time.Second); !isOrchestrationInProgress(err) || aborts != 1 || calls < 2 {
		t.Fatalf("a never-settling orchestration gives up at the deadline: err=%v aborts=%d calls=%d", err, aborts, calls)
	}
}
