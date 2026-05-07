package opensvc

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

type requestState struct {
	mu                 sync.Mutex
	objectKeysCalls    int
	createCalls        int
	lastObjectKeysPath string
	lastObjectKeysNode string
	lastCreateNode     string
	lastCreateBody     []byte
}

type requestSnapshot struct {
	ObjectKeysCalls    int
	CreateCalls        int
	LastObjectKeysPath string
	LastObjectKeysNode string
	LastCreateNode     string
	LastCreateBody     []byte
}

func (s *requestState) recordObjectKeys(path string, node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objectKeysCalls++
	s.lastObjectKeysPath = path
	s.lastObjectKeysNode = node
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
		ObjectKeysCalls:    s.objectKeysCalls,
		CreateCalls:        s.createCalls,
		LastObjectKeysPath: s.lastObjectKeysPath,
		LastObjectKeysNode: s.lastObjectKeysNode,
		LastCreateNode:     s.lastCreateNode,
		LastCreateBody:     append([]byte(nil), s.lastCreateBody...),
	}
}

func newOpenSVCTestServer(t *testing.T, state *requestState, objectKeysResponder func(requestSnapshot) (int, string)) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/object_keys", func(w http.ResponseWriter, r *http.Request) {
		state.recordObjectKeys(r.URL.Query().Get("path"), r.Header.Get("o-node"))
		snapshot := state.snapshot()
		code, body := objectKeysResponder(snapshot)
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
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
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
				return http.StatusOK, `{"data":["exists"]}`
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
			if snapshot.ObjectKeysCalls != 1 {
				t.Fatalf("expected 1 object_keys call, got %d", snapshot.ObjectKeysCalls)
			}
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create calls, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectKeysPath != expectedPath {
				t.Fatalf("unexpected object_keys path %q", snapshot.LastObjectKeysPath)
			}
			if snapshot.LastObjectKeysNode != agent {
				t.Fatalf("unexpected object_keys node %q", snapshot.LastObjectKeysNode)
			}
		})
	}
}

func TestCreateV2_CreatesWhenMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusNotFound, `{"error":"not found"}`
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
			if snapshot.ObjectKeysCalls != 1 {
				t.Fatalf("expected 1 object_keys call, got %d", snapshot.ObjectKeysCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectKeysPath != expectedPath {
				t.Fatalf("unexpected object_keys path %q", snapshot.LastObjectKeysPath)
			}
			if snapshot.LastCreateNode != agent {
				t.Fatalf("unexpected create node %q", snapshot.LastCreateNode)
			}
			assertCreatePayload(t, snapshot.LastCreateBody, expectedPath)
		})
	}
}

func TestCreateV2_TreatsEmptyDataAsMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"data":[]}`
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
					return http.StatusNotFound, `{"error":"not found"}`
				}
				return http.StatusOK, `{"data":["exists"]}`
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
			if snapshot.ObjectKeysCalls != 2 {
				t.Fatalf("expected 2 object_keys calls, got %d", snapshot.ObjectKeysCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectKeysPath != expectedPath {
				t.Fatalf("unexpected object_keys path %q", snapshot.LastObjectKeysPath)
			}
		})
	}
}

func TestCreateV2_ObjectKeysError(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusInternalServerError, `{"error":"boom"}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			if err := tc.createFn(collector, "ns1", "svc1", "node-1"); err == nil {
				t.Fatalf("expected error from object_keys failure")
			}

			snapshot := state.snapshot()
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create calls, got %d", snapshot.CreateCalls)
			}
		})
	}
}

func TestCreateV2_TreatsUnknownServiceAsMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"error":"unknown service"}`
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
			if snapshot.ObjectKeysCalls != 1 {
				t.Fatalf("expected 1 object_keys call, got %d", snapshot.ObjectKeysCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastObjectKeysPath != expectedPath {
				t.Fatalf("unexpected object_keys path %q", snapshot.LastObjectKeysPath)
			}
			assertCreatePayload(t, snapshot.LastCreateBody, expectedPath)
		})
	}
}

// --- RunTask tests ---

func newServiceActionTestServer(t *testing.T) (*httptest.Server, *serviceActionState) {
	t.Helper()
	state := &serviceActionState{}
	mux := http.NewServeMux()
	mux.HandleFunc("/service_action", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		state.mu.Lock()
		state.calls++
		state.lastBody = body
		state.lastNode = r.Header.Get("o-node")
		state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":0}`))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	return server, state
}

type serviceActionState struct {
	mu       sync.Mutex
	calls    int
	lastBody []byte
	lastNode string
}

func (s *serviceActionState) snapshot() (calls int, body []byte, node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, append([]byte(nil), s.lastBody...), s.lastNode
}

func TestRunTaskV3_MalformedServiceString(t *testing.T) {
	collector := &Collector{ClusterApiVersion: "v3"}

	cases := []string{"namespace/svc", "singlepart", ""}
	for _, svc := range cases {
		err := collector.RunTaskV3(svc, "node1", "task#mergecfg", "")
		if err == nil {
			t.Fatalf("expected error for service %q, got nil", svc)
		}
		if !strings.Contains(err.Error(), "invalid service format") {
			t.Fatalf("unexpected error for %q: %v", svc, err)
		}
	}
}

func TestRunTask_RoutesToV3OnMalformedInput(t *testing.T) {
	collector := &Collector{ClusterApiVersion: "v3"}
	err := collector.RunTask("cluster", "ns/svc", "node1", "task#mergecfg", "")
	if err == nil {
		t.Fatal("expected error for malformed service format")
	}
	if !strings.Contains(err.Error(), "invalid service format") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTaskV2_SendsCorrectRequest(t *testing.T) {
	server, state := newServiceActionTestServer(t)
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	collector := &Collector{
		Host:            parsed.Hostname(),
		Port:            parsed.Port(),
		UseCollectorAPI: true,
	}

	svc := "ns1/svc/haproxy"
	node := "s18-fr-6"
	task := "task#mergecfg"

	if err := collector.RunTaskV2("cluster1", svc, node, task, ""); err != nil {
		t.Fatalf("RunTaskV2 failed: %v", err)
	}

	calls, body, gotNode := state.snapshot()
	if calls != 1 {
		t.Fatalf("expected 1 service_action call, got %d", calls)
	}
	if gotNode != node {
		t.Fatalf("expected o-node header %q, got %q", node, gotNode)
	}

	var req ActionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if req.Path != svc {
		t.Fatalf("expected path %q, got %q", svc, req.Path)
	}
	if req.Action != "run" {
		t.Fatalf("expected action %q, got %q", "run", req.Action)
	}
	rid, ok := req.Options["rid"]
	if !ok {
		t.Fatal("expected rid in options")
	}
	if rid != task {
		t.Fatalf("expected rid %q, got %v", task, rid)
	}
}

func TestGetPoolListV2_CallsGetPoolsAndParses(t *testing.T) {
	t.Run("direct object keys", func(t *testing.T) {
		var gotPath string
		var gotNode string

		mux := http.NewServeMux()
		mux.HandleFunc("/get_pools", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			gotNode = r.Header.Get("o-node")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"poolB":{},"poolA":{},"status":0}`))
		})

		server := httptest.NewUnstartedServer(mux)
		server.EnableHTTP2 = true
		server.TLS = &tls.Config{NextProtos: []string{"h2"}}
		server.StartTLS()
		defer server.Close()

		collector := newTestCollector(t, server)
		collector.ContextTimeoutSecond = 2

		pools, err := collector.GetPoolListV2()
		if err != nil {
			t.Fatalf("GetPoolListV2 failed: %v", err)
		}

		if gotPath != "/get_pools" {
			t.Fatalf("unexpected endpoint: %s", gotPath)
		}
		if gotNode != "*" {
			t.Fatalf("unexpected o-node header: %s", gotNode)
		}

		want := []string{"poolA", "poolB"}
		if fmt.Sprint(pools) != fmt.Sprint(want) {
			t.Fatalf("unexpected pools: got %v want %v", pools, want)
		}
	})

	t.Run("nodes wrapped and deduped", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/get_pools", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"nodes":{"n1":{"poolZ":{},"poolA":{}},"n2":{"poolA":{},"poolB":{}}}}`))
		})

		server := httptest.NewUnstartedServer(mux)
		server.EnableHTTP2 = true
		server.TLS = &tls.Config{NextProtos: []string{"h2"}}
		server.StartTLS()
		defer server.Close()

		collector := newTestCollector(t, server)
		collector.ContextTimeoutSecond = 2

		pools, err := collector.GetPoolListV2()
		if err != nil {
			t.Fatalf("GetPoolListV2 failed: %v", err)
		}

		want := []string{"poolA", "poolB", "poolZ"}
		if fmt.Sprint(pools) != fmt.Sprint(want) {
			t.Fatalf("unexpected pools: got %v want %v", pools, want)
		}
	})
}

func TestGetPoolListV3_StatusAndParsing(t *testing.T) {
	t.Run("success parses and sorts", func(t *testing.T) {
		var gotPath string
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"name":"pool-c"},{"name":"pool-a"},{"name":"pool-a"}]}`))
		})

		server := httptest.NewTLSServer(mux)
		defer server.Close()

		parsed, err := url.Parse(server.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}

		collector := &Collector{
			Host:                 parsed.Hostname(),
			Port:                 parsed.Port(),
			RplMgrUser:           "user",
			RplMgrPassword:       "pass",
			ClusterApiVersion:    "v3",
			ContextTimeoutSecond: 2,
		}

		pools, err := collector.GetPoolListV3()
		if err != nil {
			t.Fatalf("GetPoolListV3 failed: %v", err)
		}
		if !strings.Contains(gotPath, "pool") {
			t.Fatalf("unexpected endpoint: %s", gotPath)
		}

		want := []string{"pool-a", "pool-c"}
		if fmt.Sprint(pools) != fmt.Sprint(want) {
			t.Fatalf("unexpected pools: got %v want %v", pools, want)
		}
	})

	t.Run("non 2xx returns StatusError", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"bad request"}`))
		})

		server := httptest.NewTLSServer(mux)
		defer server.Close()

		parsed, err := url.Parse(server.URL)
		if err != nil {
			t.Fatalf("parse server url: %v", err)
		}

		collector := &Collector{
			Host:                 parsed.Hostname(),
			Port:                 parsed.Port(),
			RplMgrUser:           "user",
			RplMgrPassword:       "pass",
			ClusterApiVersion:    "v3",
			ContextTimeoutSecond: 2,
		}

		_, err = collector.GetPoolListV3()
		if err == nil {
			t.Fatal("expected error")
		}
		var statusErr *StatusError
		if !errors.As(err, &statusErr) {
			t.Fatalf("expected StatusError, got %T (%v)", err, err)
		}
		if statusErr.StatusCode != http.StatusBadRequest {
			t.Fatalf("unexpected status code: %d", statusErr.StatusCode)
		}
	})
}
