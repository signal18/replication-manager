package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

// newResticRepman builds a ReplicationManager with JWT keys initialised and the
// named cluster registered. The cluster has restic enabled, a ResticManager with
// a paused worker, and a test user with GrantClusterProcess.
func newResticRepman(t *testing.T, clusterName string, resticEnabled bool) (*ReplicationManager, string) {
	t.Helper()

	rm := backupmgr.NewResticRepo("", nil, config.ConstLogModRestic)
	rm.PauseWorker()
	t.Cleanup(rm.ShutdownWorker)

	const (
		testUser = "restic-test-user"
		testPass = "restic-test-pass"
	)

	cl := &cluster.Cluster{
		Name: clusterName,
		Conf: &config.Config{
			BackupRestic: resticEnabled,
		},
		ResticManager: rm,
	}
	cl.APIUsers = map[string]cluster.APIUser{
		testUser: {
			User:     testUser,
			Password: testPass,
			Grants:   map[string]bool{config.GrantClusterProcess: true},
		},
	}

	repman := &ReplicationManager{
		Clusters: map[string]*cluster.Cluster{clusterName: cl},
		Conf:     &config.Config{TokenTimeout: 1},
	}
	repman.initKeys()

	tok, err := repman.issueJWT(map[string]interface{}{
		"Name":     testUser,
		"Password": testPass,
	}, "")
	if err != nil {
		t.Fatalf("issueJWT: %v", err)
	}
	return repman, tok
}

// TestHandlerResticCopy_NoCluster verifies that a request for a non-existent
// cluster returns 500 before any ACL or business-logic checks run.
func TestHandlerResticCopy_NoCluster(t *testing.T) {
	repman := newTestRepmanWithCluster(t, "other-cluster", newTestClusterForAPI(t))
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/missing/restic/copy",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "missing"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 for missing cluster, got %d", w.Code)
	}
}

// TestHandlerResticCopy_NoACL verifies that a request for an existing cluster
// without a valid JWT is rejected with 403.
func TestHandlerResticCopy_NoACL(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.BackupRestic = true
	repman := newTestRepmanWithCluster(t, "test-cluster", cl)
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/test-cluster/restic/copy",
		strings.NewReader(`{}`))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without JWT, got %d", w.Code)
	}
}

// TestHandlerResticCopy_BadJSON verifies that a malformed JSON body returns 400.
func TestHandlerResticCopy_BadJSON(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(`{not valid json`))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad JSON, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCopy_InvalidMode verifies that a valid-JSON body with an
// unrecognised source mode returns 400.
func TestHandlerResticCopy_InvalidMode(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	body := `{"source":{"mode":"unsupported","repository":"/x","password":"p"}}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(body))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mode, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerResticCopy_ValidRequest verifies that a well-formed, authenticated
// request returns 200 and queues the task.
func TestHandlerResticCopy_ValidRequest(t *testing.T) {
	repman, tok := newResticRepman(t, "test-cluster", true)
	body := `{"source":{"mode":"restic-local","repository":"/src","password":"srcpass"}}`
	req := httptest.NewRequest(http.MethodPost,
		"/api/clusters/test-cluster/restic/copy",
		strings.NewReader(body))
	req = setMuxVars(req, map[string]string{"clusterName": "test-cluster"})
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", tok))
	w := httptest.NewRecorder()

	repman.handlerMuxResticCopy(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for valid request, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "queued") {
		t.Errorf("expected response body to mention 'queued', got: %s", w.Body.String())
	}
}
