package opensvc

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"golang.org/x/net/http2"
)

type requestState struct {
	mu                   sync.Mutex
	objectStatusCalls    int
	createCalls          int
	lastObjectStatusPath string
	lastObjectStatusNode string
	lastCreateNode       string
	lastCreateBody       []byte
}

type requestSnapshot struct {
	ObjectStatusCalls    int
	CreateCalls          int
	LastObjectStatusPath string
	LastObjectStatusNode string
	LastCreateNode       string
	LastCreateBody       []byte
}

func (s *requestState) recordObjectStatus(path string, node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objectStatusCalls++
	s.lastObjectStatusPath = path
	s.lastObjectStatusNode = node
}

func (s *requestState) recordCreate(body []byte, node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.createCalls++
	s.lastCreateBody = body
	s.lastCreateNode = node
}

func (s *requestState) snapshot() requestSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return requestSnapshot{
		ObjectStatusCalls:    s.objectStatusCalls,
		CreateCalls:          s.createCalls,
		LastObjectStatusPath: s.lastObjectStatusPath,
		LastObjectStatusNode: s.lastObjectStatusNode,
		LastCreateNode:       s.lastCreateNode,
		LastCreateBody:       append([]byte(nil), s.lastCreateBody...),
	}
}

func newOpenSVCTestServer(t *testing.T, state *requestState, objectStatusResponder func(requestSnapshot) (int, string)) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/object_status", func(w http.ResponseWriter, r *http.Request) {
		state.recordObjectStatus(r.URL.Query().Get("path"), r.Header.Get("o-node"))
		snapshot := state.snapshot()
		code, body := objectStatusResponder(snapshot)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	})
	mux.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		state.recordCreate(body, r.Header.Get("o-node"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":0}`))
	})

	server := httptest.NewUnstartedServer(mux)
	http2.ConfigureServer(server.Config, &http2.Server{})
	server.StartTLS()
	return server
}

func newTestCollector(t *testing.T, server *httptest.Server) *Collector {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	return &Collector{
		Host:            parsed.Hostname(),
		Port:            parsed.Port(),
		UseCollectorAPI: true,
	}
}

func createCases() []struct {
	name     string
	kind     string
	createFn func(*Collector, string, string, string) error
} {
	return []struct {
		name     string
		kind     string
		createFn func(*Collector, string, string, string) error
	}{
		{
			name: "secret",
			kind: "sec",
			createFn: func(c *Collector, namespace string, service string, agent string) error {
				return c.CreateSecretV2(namespace, service, agent)
			},
		},
		{
			name: "config",
			kind: "cfg",
			createFn: func(c *Collector, namespace string, service string, agent string) error {
				return c.CreateConfigV2(namespace, service, agent)
			},
		},
	}
}

func assertCreatePayload(t *testing.T, body []byte, expectedPath string) {
	t.Helper()

	var req struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal create request: %v", err)
	}
	if req.Data == nil {
		t.Fatalf("missing data field in create request")
	}
	if _, ok := req.Data[expectedPath]; !ok {
		t.Fatalf("missing data key %q in create request", expectedPath)
	}
}

func TestCreateV2_SkipsCreateWhenExists(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"status":0}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			namespace := "ns1"
			service := "svc1"
			agent := "node-1"
			expectedPath := namespace + "/" + tc.kind + "/" + service

			err := tc.createFn(collector, namespace, service, agent)
			if !errors.Is(err, ErrObjectAlreadyExists) {
				t.Fatalf("expected ErrObjectAlreadyExists, got: %v", err)
			}

			snapshot := state.snapshot()
			if snapshot.ObjectStatusCalls != 1 {
				t.Fatalf("expected 1 object_status call, got %d", snapshot.ObjectStatusCalls)
			}
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create calls, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastObjectStatusPath)
			}
			if snapshot.LastObjectStatusNode != agent {
				t.Fatalf("unexpected object_status node %q", snapshot.LastObjectStatusNode)
			}
		})
	}
}

func TestCreateV2_CreatesWhenMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusNotFound, `{"status":1,"error":"not found"}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			namespace := "ns1"
			service := "svc1"
			agent := "node-1"
			expectedPath := namespace + "/" + tc.kind + "/" + service

			if err := tc.createFn(collector, namespace, service, agent); err != nil {
				t.Fatalf("create failed: %v", err)
			}

			snapshot := state.snapshot()
			if snapshot.ObjectStatusCalls != 1 {
				t.Fatalf("expected 1 object_status call, got %d", snapshot.ObjectStatusCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastObjectStatusPath)
			}
			if snapshot.LastCreateNode != agent {
				t.Fatalf("unexpected create node %q", snapshot.LastCreateNode)
			}
			assertCreatePayload(t, snapshot.LastCreateBody, expectedPath)
		})
	}
}

func TestCreateV2_TreatsNotFoundStatusAsMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"status":1,"error":"object not found"}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			namespace := "ns1"
			service := "svc1"
			agent := "node-1"
			expectedPath := namespace + "/" + tc.kind + "/" + service

			if err := tc.createFn(collector, namespace, service, agent); err != nil {
				t.Fatalf("create failed: %v", err)
			}

			snapshot := state.snapshot()
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			assertCreatePayload(t, snapshot.LastCreateBody, expectedPath)
		})
	}
}

func TestCreateV2_DoesNotRecreateOnSecondProvision(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(snapshot requestSnapshot) (int, string) {
				if snapshot.CreateCalls == 0 {
					return http.StatusNotFound, `{"status":1,"error":"not found"}`
				}
				return http.StatusOK, `{"status":0}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			namespace := "ns1"
			service := "svc1"
			agent := "node-1"
			expectedPath := namespace + "/" + tc.kind + "/" + service

			if err := tc.createFn(collector, namespace, service, agent); err != nil {
				t.Fatalf("first create failed: %v", err)
			}
			if err := tc.createFn(collector, namespace, service, agent); !errors.Is(err, ErrObjectAlreadyExists) {
				t.Fatalf("expected ErrObjectAlreadyExists on second create, got: %v", err)
			}

			snapshot := state.snapshot()
			if snapshot.ObjectStatusCalls != 2 {
				t.Fatalf("expected 2 object_status calls, got %d", snapshot.ObjectStatusCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastObjectStatusPath)
			}
		})
	}
}

func TestCreateV2_ObjectStatusError(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusInternalServerError, `{"status":1,"error":"boom"}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			if err := tc.createFn(collector, "ns1", "svc1", "node-1"); err == nil {
				t.Fatalf("expected error from object_status failure")
			}

			snapshot := state.snapshot()
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create calls, got %d", snapshot.CreateCalls)
			}
		})
	}
}
