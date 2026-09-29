package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

// Before the clusters (and their ACL users) are loaded, a login is answered 503 with
// Retry-After and is never counted as an authentication failure: it must not feed the
// per-username lock.
func TestLoginNotReadyAnswers503AndCountsNothing(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{}, Logrus: log.New()}
	body := strings.NewReader(`{"username":"admin","password":"x"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/login", body)
	w := httptest.NewRecorder()
	repman.loginHandler(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Fatalf("Retry-After header missing")
	}
	if _, counted := repman.UserAuthTry.Load("admin"); counted {
		t.Fatalf("a not-ready login must not be counted as an attempt")
	}
	// Once ready the gate steps aside: with no cluster at all the same login reaches the
	// normal path, is refused 401 and IS counted (guards against an inverted flag).
	repman.clustersReady.Store(true)
	req = httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"admin","password":"x"}`))
	w = httptest.NewRecorder()
	repman.loginHandler(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("ready status = %d, want 401", w.Code)
	}
	if _, counted := repman.UserAuthTry.Load("admin"); !counted {
		t.Fatalf("a ready login failure must be counted")
	}
}
