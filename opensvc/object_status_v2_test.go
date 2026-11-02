package opensvc

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// #2286: the confirm delay is shortened for the package's tests; its default is kept to be checked.
var confirmDelayDefault = objectExistsV2ConfirmDelay

func init() { objectExistsV2ConfirmDelay = 10 * time.Millisecond }

// An object reported unknown on the first answer but present on the confirming one (an agent
// restarted an instant before) is not re-created: /create would wipe it.
func TestCreateV2_ConfirmRecheckSeesLateObject(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(snapshot requestSnapshot) (int, string) {
				if snapshot.StatusCalls == 1 {
					return http.StatusOK, `{"error": "unknown service", "status": 1}`
				}
				return http.StatusOK, `{"nodes": {"node-2": {"status": {"kind": "sec"}}}, "status": 0}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			err := tc.createFn(collector, "ns1", "svc1", "node-1")
			if !errors.Is(err, ErrObjectAlreadyExists) {
				t.Fatalf("expected ErrObjectAlreadyExists, got: %v", err)
			}
			snapshot := state.snapshot()
			if snapshot.StatusCalls != 2 {
				t.Fatalf("expected 2 object_status calls, got %d", snapshot.StatusCalls)
			}
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create call, got %d", snapshot.CreateCalls)
			}
		})
	}
}

// The existence check asks the whole cluster (o-node ANY) even when an agent is given; the
// create that follows still goes to the agent.
func TestCreateV2_ExistenceAskedClusterWide(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"error": "unknown service", "status": 1}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			if err := tc.createFn(collector, "ns1", "svc1", "node-1"); err != nil {
				t.Fatalf("create failed: %v", err)
			}
			snapshot := state.snapshot()
			if snapshot.LastStatusNode != "ANY" {
				t.Fatalf("object_status asked with o-node %q, want ANY", snapshot.LastStatusNode)
			}
			if snapshot.LastCreateNode != "node-1" {
				t.Fatalf("create sent with o-node %q, want node-1", snapshot.LastCreateNode)
			}
		})
	}
}

// Missing needs two "unknown service" answers: an unsure confirming answer creates nothing.
func TestCreateV2_ConfirmUnsureCreatesNothing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(snapshot requestSnapshot) (int, string) {
				if snapshot.StatusCalls == 1 {
					return http.StatusOK, `{"error": "unknown service", "status": 1}`
				}
				return http.StatusOK, `{"nodes": {}, "status": 0}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			err := tc.createFn(collector, "ns1", "svc1", "node-1")
			if !errors.Is(err, errObjectStatusV2Unsure) {
				t.Fatalf("expected errObjectStatusV2Unsure, got: %v", err)
			}
			snapshot := state.snapshot()
			if snapshot.StatusCalls != 2 || snapshot.CreateCalls != 0 {
				t.Fatalf("expected 2 object_status calls and no create, got %d and %d", snapshot.StatusCalls, snapshot.CreateCalls)
			}
		})
	}
}

// The confirm wait covers the "unknown service" window measured after an agent restart (#2286).
func TestObjectExistsV2ConfirmDelayCoversRestartWindow(t *testing.T) {
	if confirmDelayDefault < 10*time.Second {
		t.Fatalf("confirm delay %s is shorter than 10s", confirmDelayDefault)
	}
}
