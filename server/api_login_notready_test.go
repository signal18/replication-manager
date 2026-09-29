package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Before the clusters (and their ACL users) are loaded, a login is answered 503 with
// Retry-After and is never counted as an authentication failure: it must not feed the
// per-username lock.
func TestLoginNotReadyAnswers503AndCountsNothing(t *testing.T) {
	repman := &ReplicationManager{}
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
}
