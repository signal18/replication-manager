package cluster

import (
	"errors"
	"testing"
)

// The orchestrator's 409 "already started" is the start succeeded; a 409 for an action in
// progress is not (#1981).
func TestOpenSVCAlreadyStarted(t *testing.T) {
	ok := errors.New(`unexpected status code: 409, body: {"detail":"already started","status":409,"title":"set instance monitor"}`)
	busy := errors.New(`unexpected status code: 409, body: {"detail":"action in progress","status":409}`)
	if !openSVCAlreadyStarted(ok) {
		t.Fatal("already started is a success")
	}
	if openSVCAlreadyStarted(busy) || openSVCAlreadyStarted(nil) {
		t.Fatal("only already started")
	}
}
