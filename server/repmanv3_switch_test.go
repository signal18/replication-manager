package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/improbable-eng/grpc-web/go/grpcweb"
	"github.com/signal18/replication-manager/config"
	"google.golang.org/grpc"
)

// TestV3APISwitch: with api-v3-server off (the default) a v3 request gets 404 and every other
// request goes to the REST API; with it on, /v3/ goes to the v3 gateway again (#2273).
func TestV3APISwitch(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}}
	repman.grpcServer = grpc.NewServer()
	repman.grpcWrapped = grpcweb.WrapServer(repman.grpcServer)

	var served string
	v3Handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = "v3" })
	restHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { served = "rest" })
	h := grpcHandlerFunc(repman, v3Handler, restHandler)

	call := func(req *http.Request) (int, string) {
		served = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, served
	}

	// off
	if code, got := call(httptest.NewRequest("GET", "/v3/clusters/c1/topology/SERVERS", nil)); code != http.StatusNotFound || got != "" {
		t.Fatalf("off, /v3/ path: code %d served %q, want 404 and nothing served", code, got)
	}
	grpcReq := httptest.NewRequest("POST", "/signal18.replication_manager.v3.ClusterService/GetCluster", nil)
	grpcReq.ProtoMajor = 2
	grpcReq.Header.Set("Content-Type", "application/grpc")
	if code, got := call(grpcReq); code != http.StatusNotFound || got != "" {
		t.Fatalf("off, gRPC request: code %d served %q, want 404 and nothing served", code, got)
	}
	webReq := httptest.NewRequest("POST", "/signal18.replication_manager.v3.ClusterService/GetCluster", nil)
	webReq.Header.Set("Content-Type", "application/grpc-web")
	if code, got := call(webReq); code != http.StatusNotFound || got != "" {
		t.Fatalf("off, gRPC-web request: code %d served %q, want 404 and nothing served", code, got)
	}
	// a REST path that only contains "v3" (a cluster named dev3) is REST
	if _, got := call(httptest.NewRequest("GET", "/api/clusters/dev3", nil)); got != "rest" {
		t.Fatalf("off, /api/clusters/dev3: served %q, want rest", got)
	}

	// on
	repman.Conf.ApiV3Serv = true
	if _, got := call(httptest.NewRequest("GET", "/v3/clusters/c1/topology/SERVERS", nil)); got != "v3" {
		t.Fatalf("on, /v3/ path: served %q, want v3", got)
	}
	if _, got := call(httptest.NewRequest("GET", "/api/clusters", nil)); got != "rest" {
		t.Fatalf("on, /api/clusters: served %q, want rest", got)
	}
}
