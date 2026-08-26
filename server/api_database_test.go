package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// handlerMuxServersPortConfig's ACL gate: a Bearer JWT is checked first
// (IsValidClusterACL), and HTTP Basic Auth is the fallback for callers with
// no JWT to send -- the Kubernetes init-container config-bootstrap wget
// being the motivating case (cluster/prov_k8s_db.go). These tests exercise
// that fallback directly, without a real ServerMonitor: with no matching
// server/proxy registered, a request that clears the ACL gate falls through
// to "No server" (500), while one that doesn't gets "No valid ACL" (403) --
// a cheap way to distinguish "auth succeeded" from "auth failed" without
// wiring up real config generation.

func newTestClusterWithAdminUser(t *testing.T, password string) *cluster.Cluster {
	t.Helper()
	cl := newTestClusterForAPI(t)
	cl.Conf.APISecureConfig = true
	cl.APIUsers = map[string]cluster.APIUser{
		"admin": {
			User:     "admin",
			Password: password,
			Grants:   map[string]bool{config.GrantDBConfigFlag: true},
		},
	}
	return cl
}

func TestHandlerMuxServersPortConfig_ValidBasicAuthPassesACL(t *testing.T) {
	cl := newTestClusterWithAdminUser(t, "s3cr3t")
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	req := httptest.NewRequest("GET", "/api/clusters/"+cl.Name+"/servers/db1/3306/config", nil)
	req = setMuxVars(req, map[string]string{"clusterName": cl.Name, "serverName": "db1", "serverPort": "3306"})
	req.SetBasicAuth("admin", "s3cr3t")
	w := httptest.NewRecorder()

	repman.handlerMuxServersPortConfig(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("expected valid Basic Auth to pass the ACL gate, got 403: %s", w.Body.String())
	}
	if w.Code != http.StatusInternalServerError || w.Body.String() != "No server\n" {
		t.Fatalf("expected to reach the no-matching-server branch (500 \"No server\"), got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerMuxServersPortConfig_InvalidBasicAuthFailsACL(t *testing.T) {
	cl := newTestClusterWithAdminUser(t, "s3cr3t")
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	req := httptest.NewRequest("GET", "/api/clusters/"+cl.Name+"/servers/db1/3306/config", nil)
	req = setMuxVars(req, map[string]string{"clusterName": cl.Name, "serverName": "db1", "serverPort": "3306"})
	req.SetBasicAuth("admin", "wrong-password")
	w := httptest.NewRecorder()

	repman.handlerMuxServersPortConfig(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected invalid Basic Auth to be rejected with 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerMuxServersPortConfig_NoAuthAtAllFailsACL(t *testing.T) {
	cl := newTestClusterWithAdminUser(t, "s3cr3t")
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	req := httptest.NewRequest("GET", "/api/clusters/"+cl.Name+"/servers/db1/3306/config", nil)
	req = setMuxVars(req, map[string]string{"clusterName": cl.Name, "serverName": "db1", "serverPort": "3306"})
	w := httptest.NewRecorder()

	repman.handlerMuxServersPortConfig(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected no JWT and no Basic Auth to be rejected with 403, got %d: %s", w.Code, w.Body.String())
	}
}

// handlerMuxServerRestart gated Kubernetes out entirely (hardcoded to
// OpenSVC only), even though RestartDatabaseService (cluster/prov.go) has
// had a working Kubernetes branch since #1497's image-pull-policy work --
// confirmed live: that branch was never actually reachable through the API
// because this gate blocked the handler from ever setting the restart
// cookie CheckRestartContainerCookies (cluster_chk.go) consumes.
func TestRestartSupportedForOrchestrator(t *testing.T) {
	tests := []struct {
		orchestrator string
		want         bool
	}{
		{config.ConstOrchestratorOpenSVC, true},
		{config.ConstOrchestratorKubernetes, true},
		{config.ConstOrchestratorOnPremise, false},
		{config.ConstOrchestratorLocalhost, false},
		{"", false},
	}
	for _, tt := range tests {
		if got := restartSupportedForOrchestrator(tt.orchestrator); got != tt.want {
			t.Errorf("restartSupportedForOrchestrator(%q) = %v, want %v", tt.orchestrator, got, tt.want)
		}
	}
}

// handlerMuxGetDatabaseServiceConfig is one route shared by every
// orchestrator with a manifest/config concept to show (#1497 gap 6), keyed
// by a dynamic {orchestrator} path segment rather than a hardcoded route
// name per orchestrator (it used to be literally "service-opensvc", which
// would have been misleading to also serve Kubernetes content under):
// OpenSVC gets its existing raw service-config text unchanged; Kubernetes
// gets the live Deployment/Service/PVC/Pod manifests as JSON instead, since
// it has no equivalent single "service config" object; every other
// orchestrator keeps the route's pre-existing empty-body behavior. The
// path segment must match the cluster's actually configured orchestrator
// -- repman's own config is authoritative, never the caller's say-so.
// These exercise the handler's branching (cluster lookup, ACL, orchestrator
// match, server lookup) without a live Kubernetes API server;
// K8SGetDatabaseManifests's own live-fetch behavior is covered directly in
// cluster/prov_k8s_manifest_test.go.

func TestHandlerMuxGetDatabaseServiceConfig_NoClusterIs500(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "unused", newTestClusterForAPI(t))

	req := httptest.NewRequest("GET", "/api/clusters/does-not-exist/servers/db1/service/opensvc", nil)
	req = setMuxVars(req, map[string]string{"clusterName": "does-not-exist", "serverName": "db1", "orchestrator": "opensvc"})
	w := httptest.NewRecorder()

	repman.handlerMuxGetDatabaseServiceConfig(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for an unknown cluster, got %d: %s", w.Code, w.Body.String())
	}
}

// buildDatabaseServiceConfigResponse is the ACL-free business logic behind
// handlerMuxGetDatabaseServiceConfig, called directly here so these don't
// need a real JWT (IsValidClusterACL has no bypass) -- matching
// buildS3ProviderReferencesResponse's pattern (api_cluster_test.go).

func TestBuildDatabaseServiceConfigResponse_OrchestratorMismatchIs400(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC

	status, _, _ := buildDatabaseServiceConfigResponse(cl, "db1", config.ConstOrchestratorKubernetes)

	if status != http.StatusBadRequest {
		t.Fatalf("expected 400 when the requested orchestrator doesn't match the cluster's configured one, got %d", status)
	}
}

func TestBuildDatabaseServiceConfigResponse_UnhandledOrchestratorReturnsEmptyBody(t *testing.T) {
	cl := newTestClusterForAPI(t)

	status, contentType, body := buildDatabaseServiceConfigResponse(cl, "db1", "")

	if status != http.StatusOK || contentType != "" || string(body) != "" {
		t.Fatalf("expected 200 with an empty body for an orchestrator with no manifest view, got %d %q %q", status, contentType, body)
	}
}

func TestBuildDatabaseServiceConfigResponse_KubernetesReturnsJSONManifests(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	cl.Conf.KubeConfig = "/nonexistent/kubeconfig-path-for-test"
	cl.Servers = []*cluster.ServerMonitor{{Id: "db1", Name: "db1"}}

	status, contentType, body := buildDatabaseServiceConfigResponse(cl, "db1", config.ConstOrchestratorKubernetes)

	if status != http.StatusOK {
		t.Fatalf("expected 200 for a Kubernetes cluster, got %d: %s", status, body)
	}
	if contentType != "application/json" {
		t.Fatalf("expected application/json Content-Type for the Kubernetes response, got %q", contentType)
	}
	var m cluster.K8SDatabaseManifests
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("expected a valid K8SDatabaseManifests JSON body: %v (body: %s)", err, body)
	}
	// K8SConnectAPI fails against the bogus kubeconfig above, so every
	// section should carry that error rather than being silently empty --
	// confirms this actually reached K8SGetDatabaseManifests.
	if m.Deployment == "" {
		t.Fatalf("expected a non-empty Deployment section (even if it's an error), got: %+v", m)
	}
}

func TestBuildDatabaseServiceConfigResponse_KubernetesServerNotFoundIs500(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes

	status, _, _ := buildDatabaseServiceConfigResponse(cl, "does-not-exist", config.ConstOrchestratorKubernetes)

	if status != http.StatusInternalServerError {
		t.Fatalf("expected 500 for an unknown server, got %d", status)
	}
}

func TestHandlerMuxGetDatabaseServiceConfig_NoValidACLFailsWithForbidden(t *testing.T) {
	cl := newTestClusterWithAdminUser(t, "s3cr3t")
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorKubernetes
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	req := httptest.NewRequest("GET", "/api/clusters/"+cl.Name+"/servers/db1/service/kube", nil)
	req = setMuxVars(req, map[string]string{"clusterName": cl.Name, "serverName": "db1", "orchestrator": config.ConstOrchestratorKubernetes})
	w := httptest.NewRecorder()

	repman.handlerMuxGetDatabaseServiceConfig(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with no credentials against a secured cluster, got %d: %s", w.Code, w.Body.String())
	}
}

// api-credentials-secure-config off bypasses the ACL gate entirely,
// regardless of any credential -- matches the K8s init-container's own
// behavior of sending no Authorization header at all in that case
// (cluster/prov_k8s_db.go).
func TestHandlerMuxServersPortConfig_SecureConfigOffSkipsACL(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.APISecureConfig = false
	repman := newTestRepmanWithCluster(t, cl.Name, cl)

	req := httptest.NewRequest("GET", "/api/clusters/"+cl.Name+"/servers/db1/3306/config", nil)
	req = setMuxVars(req, map[string]string{"clusterName": cl.Name, "serverName": "db1", "serverPort": "3306"})
	w := httptest.NewRecorder()

	repman.handlerMuxServersPortConfig(w, req)

	if w.Code == http.StatusForbidden {
		t.Fatalf("expected api-credentials-secure-config=false to skip the ACL gate entirely, got 403: %s", w.Body.String())
	}
}
