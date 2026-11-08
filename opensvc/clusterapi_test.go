package opensvc

import (
	"context"
	"crypto/md5"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type requestState struct {
	mu             sync.Mutex
	statusCalls    int
	createCalls    int
	lastStatusPath string
	lastStatusNode string
	lastCreateNode string
	lastCreateBody []byte
}

type requestSnapshot struct {
	StatusCalls    int
	CreateCalls    int
	LastStatusPath string
	LastStatusNode string
	LastCreateNode string
	LastCreateBody []byte
}

func (s *requestState) recordStatus(path string, node string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusCalls++
	s.lastStatusPath = path
	s.lastStatusNode = node
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
		StatusCalls:    s.statusCalls,
		CreateCalls:    s.createCalls,
		LastStatusPath: s.lastStatusPath,
		LastStatusNode: s.lastStatusNode,
		LastCreateNode: s.lastCreateNode,
		LastCreateBody: append([]byte(nil), s.lastCreateBody...),
	}
}

func newOpenSVCTestServer(t *testing.T, state *requestState, statusResponder func(requestSnapshot) (int, string)) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/object_status", func(w http.ResponseWriter, r *http.Request) {
		state.recordStatus(r.URL.Query().Get("path"), r.Header.Get("o-node"))
		snapshot := state.snapshot()
		code, body := statusResponder(snapshot)
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
				return http.StatusOK, `{"nodes": {"node-1": {"status": {"kind": "sec"}}}, "status": 0}`
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
			if snapshot.StatusCalls != 1 {
				t.Fatalf("expected %d object_status calls, got %d", 1, snapshot.StatusCalls)
			}
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create calls, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastStatusPath)
			}
			if snapshot.LastStatusNode != "ANY" {
				t.Fatalf("unexpected object_status node %q", snapshot.LastStatusNode)
			}
		})
	}
}

func TestCreateV2_CreatesWhenMissing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"error": "unknown service", "status": 1}`
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
			if snapshot.StatusCalls != 2 {
				t.Fatalf("expected %d object_status calls, got %d", 2, snapshot.StatusCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastStatusPath)
			}
			if snapshot.LastCreateNode != agent {
				t.Fatalf("unexpected create node %q", snapshot.LastCreateNode)
			}
			assertCreatePayload(t, snapshot.LastCreateBody, expectedPath)
		})
	}
}

// An empty nodes map is not "missing" (#2286): it is answered for an existing object while
// the API node's view lags, and /create would wipe it. Nothing is created, an error is returned.
func TestCreateV2_EmptyNodesCreatesNothing(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(_ requestSnapshot) (int, string) {
				return http.StatusOK, `{"nodes": {}, "status": 0}`
			})
			defer server.Close()

			collector := newTestCollector(t, server)
			err := tc.createFn(collector, "ns1", "svc1", "node-1")
			if !errors.Is(err, errObjectStatusV2Unsure) {
				t.Fatalf("expected errObjectStatusV2Unsure, got: %v", err)
			}
			snapshot := state.snapshot()
			if snapshot.CreateCalls != 0 {
				t.Fatalf("expected no create call, got %d", snapshot.CreateCalls)
			}
		})
	}
}

func TestCreateV2_DoesNotRecreateOnSecondProvision(t *testing.T) {
	for _, tc := range createCases() {
		t.Run(tc.name, func(t *testing.T) {
			state := &requestState{}
			server := newOpenSVCTestServer(t, state, func(snapshot requestSnapshot) (int, string) {
				if snapshot.CreateCalls == 0 {
					return http.StatusOK, `{"error": "unknown service", "status": 1}`
				}
				return http.StatusOK, `{"nodes": {"node-1": {"status": {"kind": "sec"}}}, "status": 0}`
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
			if snapshot.StatusCalls != 3 {
				t.Fatalf("expected %d object_status calls, got %d", 3, snapshot.StatusCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastStatusPath)
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
				t.Fatalf("expected error from object_status failure")
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
				return http.StatusOK, `{"error": "unknown service", "status": 1}`
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
			if snapshot.StatusCalls != 2 {
				t.Fatalf("expected %d object_status calls, got %d", 2, snapshot.StatusCalls)
			}
			if snapshot.CreateCalls != 1 {
				t.Fatalf("expected 1 create call, got %d", snapshot.CreateCalls)
			}
			if snapshot.LastStatusPath != expectedPath {
				t.Fatalf("unexpected object_status path %q", snapshot.LastStatusPath)
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

func TestGetPoolInfoListV2_ParsesSharedCapabilities(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/get_pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"poolA":{"capabilities":["blk"]},"poolB":{"capabilities":["shared","blk"]},"nodes":{"n1":{"poolC":{"name":"poolC","capabilities":["shared"]}}}}`))
	})

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()

	collector := newTestCollector(t, server)
	collector.ContextTimeoutSecond = 2

	infos, err := collector.GetPoolInfoListV2()
	if err != nil {
		t.Fatalf("GetPoolInfoListV2 failed: %v", err)
	}

	byName := make(map[string]PoolInfo)
	for _, info := range infos {
		byName[info.Name] = info
	}

	if len(byName) != 3 {
		t.Fatalf("unexpected pool count: got %d want 3", len(byName))
	}
	if byName["poolA"].Shared {
		t.Fatalf("poolA should not be shared")
	}
	if !byName["poolB"].Shared {
		t.Fatalf("poolB should be shared")
	}
	if !byName["poolC"].Shared {
		t.Fatalf("poolC should be shared")
	}
}

func TestGetPoolInfoListV2_Non2xxReturnsStatusError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/get_pools", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
	})

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()

	collector := newTestCollector(t, server)
	collector.ContextTimeoutSecond = 2

	_, err := collector.GetPoolInfoListV2()
	if err == nil {
		t.Fatal("expected error")
	}

	var statusErr *StatusError
	if !errors.As(err, &statusErr) {
		t.Fatalf("expected StatusError, got %T (%v)", err, err)
	}
	if statusErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("unexpected status code: got %d want %d", statusErr.StatusCode, http.StatusBadGateway)
	}
}

func TestGetPoolInfoListV2_IgnoresTopLevelMetadataKeys(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/get_pools", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"poolA":{},"metadata":"ignore-me","count":2,"status":0}`))
	})

	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()

	collector := newTestCollector(t, server)
	collector.ContextTimeoutSecond = 2

	infos, err := collector.GetPoolInfoListV2()
	if err != nil {
		t.Fatalf("GetPoolInfoListV2 failed: %v", err)
	}

	if len(infos) != 1 {
		t.Fatalf("unexpected pool count: got %d want 1", len(infos))
	}
	if infos[0].Name != "poolA" {
		t.Fatalf("unexpected pool name: got %q want %q", infos[0].Name, "poolA")
	}
}

func TestGetPoolInfoListV3_ParsesShared(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"name":"pool-a","shared":true},{"name":"pool-b","capabilities":["shared"]},{"name":"pool-c"}]}`))
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

	infos, err := collector.GetPoolInfoListV3()
	if err != nil {
		t.Fatalf("GetPoolInfoListV3 failed: %v", err)
	}

	byName := make(map[string]PoolInfo)
	for _, info := range infos {
		byName[info.Name] = info
	}

	if !byName["pool-a"].Shared {
		t.Fatalf("pool-a should be shared")
	}
	if !byName["pool-b"].Shared {
		t.Fatalf("pool-b should be shared")
	}
	if byName["pool-c"].Shared {
		t.Fatalf("pool-c should not be shared")
	}
}

// ---------------------------------------------------------------------------
// checkV2ResponseBody unit tests
// ---------------------------------------------------------------------------

func TestCheckV2ResponseBody(t *testing.T) {
	tests := []struct {
		name    string
		body    []byte
		wantErr bool
	}{
		{"empty body", []byte{}, false},
		{"status zero", []byte(`{"status":0}`), false},
		{"status nonzero", []byte(`{"status":1}`), true},
		{"error field set", []byte(`{"status":0,"error":"permission denied"}`), true},
		{"error field empty string", []byte(`{"status":0,"error":""}`), false},
		{"error field whitespace only", []byte(`{"status":0,"error":"   "}`), false},
		{"error only no status field", []byte(`{"error":"bad key"}`), true},
		{"both nonzero status and error", []byte(`{"status":2,"error":"disk full"}`), true},
		{"non-json body", []byte(`not json at all`), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkV2ResponseBody(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("checkV2ResponseBody(%q) error = %v, wantErr %v", tt.body, err, tt.wantErr)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Integration tests: /key and /create body-error detection
// ---------------------------------------------------------------------------

// newV2TestCollector returns a Collector wired to the given httptest TLS server URL.
// UseCollectorAPI=true skips the P12 cert load path in GetHttpClient.
func newV2TestCollector(t *testing.T, serverURL string) *Collector {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return &Collector{
		Host:                 parsed.Hostname(),
		Port:                 parsed.Port(),
		RplMgrUser:           "user",
		RplMgrPassword:       "pass",
		UseCollectorAPI:      true,
		ContextTimeoutSecond: 2,
	}
}

// newV2TLSServer starts an httptest TLS server with HTTP/2 enabled so that
// GetHttpClient's http2.Transport can complete the ALPN handshake.
func newV2TLSServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestCreateKeyValueV2_BodyErrors(t *testing.T) {
	tests := []struct {
		name        string
		respBody    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "success status zero",
			respBody: `{"status":0}`,
			wantErr:  false,
		},
		{
			name:        "body status nonzero",
			respBody:    `{"status":1}`,
			wantErr:     true,
			errContains: "non-zero status 1",
		},
		{
			name:        "body error field set",
			respBody:    `{"status":0,"error":"key already exists"}`,
			wantErr:     true,
			errContains: "key already exists",
		},
		{
			name:     "empty error field ignored",
			respBody: `{"status":0,"error":""}`,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run("CreateSecretKeyValueV2/"+tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, tt.respBody)
			})
			srv := newV2TLSServer(t, mux)

			c := newV2TestCollector(t, srv.URL)
			err := c.CreateSecretKeyValueV2("ns", "svc", "mykey", "myval")
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
		})

		t.Run("CreateConfigKeyValueV2/"+tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, tt.respBody)
			})
			srv := newV2TLSServer(t, mux)

			c := newV2TestCollector(t, srv.URL)
			err := c.CreateConfigKeyValueV2("ns", "svc", "mykey", "myval")
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// objectStatusV2Exists unit tests (body-level existence logic)
// ---------------------------------------------------------------------------

func TestObjectStatusV2Exists(t *testing.T) {
	tests := []struct {
		name       string
		body       []byte
		wantExists bool
		wantErr    bool
	}{
		// user-specified cases
		{
			name:       "empty nodes map → unsure, error",
			body:       []byte(`{"nodes":{}}`),
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		{
			name:       "object fields plus empty nodes → exists",
			body:       []byte(`{"updated":123,"kind":"sec","nodes":{}}`),
			wantExists: true,
		},
		{
			name:       "nodes with node entry → exists",
			body:       []byte(`{"nodes":{"n1":{"status":{"kind":"sec"}}}}`),
			wantExists: true,
		},
		{
			name:    "error field set → error",
			body:    []byte(`{"error":"permission denied"}`),
			wantErr: true,
		},
		{
			name:       "unknown service (o-node ANY, missing object) → not exists",
			body:       []byte(`{"error": "unknown service", "status": 1}`),
			wantExists: false,
		},
		// additional guard cases
		{
			name:       "empty body → unsure, error",
			body:       []byte{},
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		{
			name:       "empty object → unsure, error",
			body:       []byte(`{}`),
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		{
			name:       "null nodes → unsure, error",
			body:       []byte(`{"nodes":null}`),
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		{
			name:       "error empty string and empty nodes → unsure, error",
			body:       []byte(`{"error":"","nodes":{}}`),
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		// OpenSVC 2.1 answers with a "status" meta-field on every object_status
		{
			name:       "status meta-field and empty nodes → unsure, error",
			body:       []byte(`{"nodes": {}, "status": 0}`),
			wantErr:    true, // unsure: no node listed, not reported unknown
			wantExists: false,
		},
		{
			name:       "status meta-field and a node → exists",
			body:       []byte(`{"nodes": {"node-1": {"status": {"kind": "sec"}}}, "status": 0}`),
			wantExists: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exists, err := objectStatusV2Exists(tt.body)
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if exists != tt.wantExists {
				t.Errorf("exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ObjectExistsV2 integration tests (HTTP layer)
// ---------------------------------------------------------------------------

func TestObjectExistsV2(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		respBody   string
		wantExists bool
		wantErr    bool
	}{
		{
			name:       "404 → error (only unknown service is missing)",
			statusCode: http.StatusNotFound,
			respBody:   "",
			wantExists: false,
			wantErr:    true,
		},
		{
			name:       "200 empty nodes → unsure, error",
			statusCode: http.StatusOK,
			respBody:   `{"nodes":{}}`,
			wantExists: false,
			wantErr:    true,
		},
		{
			name:       "200 object fields plus empty nodes → exists",
			statusCode: http.StatusOK,
			respBody:   `{"updated":123,"kind":"sec","nodes":{}}`,
			wantExists: true,
		},
		{
			name:       "200 non-empty nodes → exists",
			statusCode: http.StatusOK,
			respBody:   `{"nodes":{"n1":{"status":{"kind":"sec"}}}}`,
			wantExists: true,
		},
		{
			name:       "200 error field → error",
			statusCode: http.StatusOK,
			respBody:   `{"error":"permission denied"}`,
			wantExists: false,
			wantErr:    true,
		},
		{
			name:       "200 unknown service (o-node ANY) → not exists",
			statusCode: http.StatusOK,
			respBody:   `{"error": "unknown service", "status": 1}`,
			wantExists: false,
		},
		{
			name:       "200 status meta-field and empty nodes → unsure, error",
			statusCode: http.StatusOK,
			respBody:   `{"nodes": {}, "status": 0}`,
			wantExists: false,
			wantErr:    true,
		},
		{
			name:       "500 → error",
			statusCode: http.StatusInternalServerError,
			respBody:   `server error`,
			wantExists: false,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/object_status", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				fmt.Fprint(w, tt.respBody)
			})
			srv := newV2TLSServer(t, mux)
			c := newV2TestCollector(t, srv.URL)

			exists, err := c.ObjectExistsV2("ns/sec/svc", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if exists != tt.wantExists {
				t.Errorf("exists = %v, want %v", exists, tt.wantExists)
			}
		})
	}
}

func TestCreateObjectV2_BodyErrors(t *testing.T) {
	tests := []struct {
		name        string
		respBody    string
		wantErr     bool
		errContains string
	}{
		{
			name:     "success status zero",
			respBody: `{"status":0}`,
			wantErr:  false,
		},
		{
			name:        "body status nonzero",
			respBody:    `{"status":1}`,
			wantErr:     true,
			errContains: "non-zero status 1",
		},
		{
			name:        "body error field set",
			respBody:    `{"status":0,"error":"no space left"}`,
			wantErr:     true,
			errContains: "no space left",
		},
	}

	for _, tt := range tests {
		tt := tt
		// object_status returns the real V2 missing-object answer to o-node ANY
		// ("unknown service") so ObjectExistsV2 reports "not exists" and /create is called.
		setupMux := func(createBody string) *http.ServeMux {
			mux := http.NewServeMux()
			mux.HandleFunc("/object_status", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, `{"error": "unknown service", "status": 1}`)
			})
			mux.HandleFunc("/create", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, createBody)
			})
			return mux
		}

		t.Run("CreateSecretV2/"+tt.name, func(t *testing.T) {
			srv := newV2TLSServer(t, setupMux(tt.respBody))

			c := newV2TestCollector(t, srv.URL)
			err := c.CreateSecretV2("ns", "svc", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
		})

		t.Run("CreateConfigV2/"+tt.name, func(t *testing.T) {
			srv := newV2TLSServer(t, setupMux(tt.respBody))

			c := newV2TestCollector(t, srv.URL)
			err := c.CreateConfigV2("ns", "svc", "")
			if (err != nil) != tt.wantErr {
				t.Errorf("error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" && err != nil {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

// TestWaitObjectConfigSettledV3_UsesTheAPINodeCopyNotTheProxiedRead reproduces #1795: after
// a PUT the proxied object read still serves the OLD file (the instance node has not fetched
// the new one yet) while the API node's own copy is the NEW file. The wait must anchor on the
// latter and return only once the instance node's loaded csum reaches the new md5.
func TestWaitObjectConfigSettledV3_UsesTheAPINodeCopyNotTheProxiedRead(t *testing.T) {
	oldFile := []byte("[DEFAULT]\nnodes = n1\n")
	newFile := []byte("[DEFAULT]\nnodes = n1\npg_cpu_quota = 200%@all\n")
	oldSum := fmt.Sprintf("%x", md5.Sum(oldFile))
	newSum := fmt.Sprintf("%x", md5.Sum(newFile))
	var polls int32

	mux := http.NewServeMux()
	mux.HandleFunc("/api/node/name/_/instance/path/ns/svc/db1/config/file", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newFile) // the API node holds the freshly written file
	})
	mux.HandleFunc("/api/object/path/ns/svc/db1/config/file", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(oldFile) // proxied to the instance node: still the old file
	})
	mux.HandleFunc("/api/instance", func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&polls, 1)
		csum := oldSum
		if n >= 3 {
			csum = newSum // the instance node loads the new config on the third poll
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"meta":{"node":"n1"},"data":{"config":{"csum":"` + csum + `"}}}]}`))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()

	c := newTestCollector(t, server)
	c.ContextTimeoutSecond = 5
	if err := c.WaitObjectConfigSettledV3("n1", "ns", "svc", "db1", 5*time.Second); err != nil {
		t.Fatalf("wait must succeed once the node loaded the new config: %v", err)
	}
	if got := atomic.LoadInt32(&polls); got < 3 {
		t.Fatalf("the wait returned after %d poll(s): it anchored on the stale proxied file (md5(old) == old csum) instead of the API node copy", got)
	}
}

// TestWaitObjectConfigSettledV3_FallsBackToTheProxiedRead: once the API node has dropped
// its foreign copy (404), the proxied read is the reference -- by then it is the new file.
func TestWaitObjectConfigSettledV3_FallsBackToTheProxiedRead(t *testing.T) {
	file := []byte("[DEFAULT]\nnodes = n1\npg_cpu_quota = 200%@all\n")
	sum := fmt.Sprintf("%x", md5.Sum(file))
	mux := http.NewServeMux()
	mux.HandleFunc("/api/node/name/_/instance/path/ns/svc/db1/config/file", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/api/object/path/ns/svc/db1/config/file", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(file)
	})
	mux.HandleFunc("/api/instance", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"items":[{"meta":{"node":"n1"},"data":{"config":{"csum":"` + sum + `"}}}]}`))
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()

	c := newTestCollector(t, server)
	c.ContextTimeoutSecond = 5
	if err := c.WaitObjectConfigSettledV3("n1", "ns", "svc", "db1", 3*time.Second); err != nil {
		t.Fatalf("fallback to the proxied read must succeed: %v", err)
	}
}

// TestCreateObjectV3_WaitsLocal: om3 rc46 made POST config/file return before the object is
// initialised unless wait_local=true is asked; the create must carry it so the provisioning
// that follows does not race the daemon.
func TestCreateObjectV3_WaitsLocal(t *testing.T) {
	var got string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/object/path/ns/svc/db1/config/file", func(w http.ResponseWriter, r *http.Request) {
		got = r.Method + " wait_local=" + r.URL.Query().Get("wait_local")
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()
	collector := newTestCollector(t, server)
	collector.ContextTimeoutSecond = 5
	if _, err := collector.CreateObjectV3("ns", "svc", "db1", []byte("[DEFAULT]\nnodes = n1\n")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got != "POST wait_local=true" {
		t.Fatalf("the create must ask the daemon to wait for the local object: got %q", got)
	}
}

// TestCreateSecretKeyValueV2Ctx_CallerDeadline: an agent that never answers is cut by the
// caller's context (here 300 ms), not left to the collector's own 10 s, so the secret store
// writer's budget bounds the HTTP request itself.
func TestCreateSecretKeyValueV2Ctx_CallerDeadline(t *testing.T) {
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/key", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	server := httptest.NewUnstartedServer(mux)
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{NextProtos: []string{"h2"}}
	server.StartTLS()
	defer server.Close()
	defer close(release)

	collector := newTestCollector(t, server)
	collector.ContextTimeoutSecond = 10

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := collector.CreateSecretKeyValueV2Ctx(ctx, "ns", "env", "K", "secret-value")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("expected a deadline error from a hung agent")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("call took %s: the caller's 300ms deadline did not bound the request", elapsed)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("error leaks the value (F10): %v", err)
	}
}

// TestRequestContext_UnsetTimeoutIsBounded: an unset ContextTimeoutSecond never gives an
// unbounded request nor one expired at once.
func TestRequestContext_UnsetTimeoutIsBounded(t *testing.T) {
	c := &Collector{}
	ctx, cancel := c.requestContext(context.Background())
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatalf("no deadline with ContextTimeoutSecond unset")
	}
	if left := time.Until(dl); left < 9*time.Second || left > 11*time.Second {
		t.Fatalf("deadline %s away, want about 10s", left)
	}
}

// TestV3CallsReuseConnection: two v3 calls to the same agent go over one connection. They used
// to set req.Close, which closed it after each request and made every call pay a new TCP and
// TLS handshake although om3 caches the client.
func TestV3CallsReuseConnection(t *testing.T) {
	var newConns atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("value"))
	}))
	server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			newConns.Add(1)
		}
	}
	server.StartTLS()
	defer server.Close()

	collector := newTestCollector(t, server)
	collector.SetV3()
	for i := 0; i < 3; i++ {
		if _, err := collector.GetSecretKeyValueV3("ns", "env", "K"); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if n := newConns.Load(); n != 1 {
		t.Fatalf("%d connections for 3 calls, want 1 (reused)", n)
	}
}
