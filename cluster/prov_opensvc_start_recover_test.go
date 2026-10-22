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

// The database start: a warn-state refusal is recovered once (abort then restart), a 409 or
// a 408 of a stop still running is retried, "already started" is a success, another error returns.
func TestStartOrRecoverWarn(t *testing.T) {
	warn := errors.New(`unexpected status code: 409, body: {"detail":"failover object is warn state","status":409,"title":"set instance monitor"}`)
	busy := errors.New(`unexpected status code: 409, body: {"detail":"orchestration x already in progress","status":409}`)
	started := errors.New(`unexpected status code: 409, body: already started`)

	starts, recovers := 0, 0
	if err := startOrRecoverWarn(func() error { starts++; return warn }, func(error) error { recovers++; return nil }, time.Minute, time.Millisecond); err != nil || starts != 1 || recovers != 1 {
		t.Fatalf("a warn-state refusal is recovered once: err=%v starts=%d recovers=%d", err, starts, recovers)
	}

	starts, recovers = 0, 0
	failed := errors.New("restart refused")
	if err := startOrRecoverWarn(func() error { starts++; return warn }, func(error) error { recovers++; return failed }, time.Minute, time.Millisecond); !errors.Is(err, failed) || !errors.Is(err, warn) || recovers != 1 {
		t.Fatalf("a failed recovery returns both errors: err=%v recovers=%d", err, recovers)
	}

	starts, recovers = 0, 0
	if err := startOrRecoverWarn(func() error {
		starts++
		if starts < 3 {
			return busy
		}
		return nil
	}, func(error) error { recovers++; return nil }, time.Minute, time.Millisecond); err != nil || starts != 3 || recovers != 0 {
		t.Fatalf("a stop still running is waited, no recovery: err=%v starts=%d recovers=%d", err, starts, recovers)
	}

	stopping := errors.New(`unexpected status code: 408, body: {"detail":"dev3/svc/db2 is stopping on s18-fr-5: the instance monitor reads the request when the action ends, and may refuse it","status":408}`)
	starts, recovers = 0, 0
	if err := startOrRecoverWarn(func() error {
		starts++
		if starts < 2 {
			return stopping
		}
		return nil
	}, func(error) error { recovers++; return nil }, time.Minute, time.Millisecond); err != nil || starts != 2 || recovers != 0 {
		t.Fatalf("a restart's start sent while the stop runs waits for it: err=%v starts=%d recovers=%d", err, starts, recovers)
	}

	if err := startOrRecoverWarn(func() error { return started }, func(error) error { t.Fatal("no recovery"); return nil }, time.Minute, time.Millisecond); err != nil {
		t.Fatalf("already started is a success: %v", err)
	}

	other := errors.New("object not found")
	starts = 0
	if err := startOrRecoverWarn(func() error { starts++; return other }, func(error) error { t.Fatal("no recovery"); return nil }, time.Minute, time.Millisecond); err != other || starts != 1 {
		t.Fatalf("another error returns at once: err=%v starts=%d", err, starts)
	}
}
