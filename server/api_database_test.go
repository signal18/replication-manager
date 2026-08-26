package server

import (
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
