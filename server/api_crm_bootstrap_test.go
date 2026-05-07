package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

func TestEnsureCRMSessionBootstrapped_Get200_NoBootstrap(t *testing.T) {
	var getCount int
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-123" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/credits/personal":
			getCount++
			w.WriteHeader(http.StatusOK)
		case "/api/session/bootstrap":
			postCount++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: ts.URL}}
	repman.ensureCRMSessionBootstrapped("tok-123")

	if getCount != 1 {
		t.Fatalf("expected 1 credits GET call, got %d", getCount)
	}
	if postCount != 0 {
		t.Fatalf("expected 0 bootstrap POST calls, got %d", postCount)
	}
}

func TestEnsureCRMSessionBootstrapped_Get428_BootstrapThenRetry(t *testing.T) {
	var getCount int
	var postCount int
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok-abc" {
			t.Fatalf("unexpected authorization header: %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/api/credits/personal":
			getCount++
			if getCount == 1 {
				w.WriteHeader(http.StatusPreconditionRequired)
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/api/session/bootstrap":
			postCount++
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: ts.URL}}
	repman.ensureCRMSessionBootstrapped("tok-abc")

	if getCount != 2 {
		t.Fatalf("expected 2 credits GET calls, got %d", getCount)
	}
	if postCount != 1 {
		t.Fatalf("expected 1 bootstrap POST call, got %d", postCount)
	}
}

func TestEnsureCRMSessionBootstrapped_ErrorsAreBestEffort(t *testing.T) {
	t.Run("get network error", func(t *testing.T) {
		repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: "http://127.0.0.1:1"}}
		repman.ensureCRMSessionBootstrapped("tok-net")
	})

	t.Run("bootstrap failure", func(t *testing.T) {
		var getCount int
		var postCount int
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/credits/personal":
				getCount++
				w.WriteHeader(http.StatusPreconditionRequired)
			case "/api/session/bootstrap":
				postCount++
				w.WriteHeader(http.StatusInternalServerError)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer ts.Close()

		repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: ts.URL}}
		repman.ensureCRMSessionBootstrapped("tok-post")

		if getCount != 1 {
			t.Fatalf("expected 1 credits GET call when bootstrap fails, got %d", getCount)
		}
		if postCount != 1 {
			t.Fatalf("expected 1 bootstrap POST call when credits returns 428, got %d", postCount)
		}
	})
}

func TestEnsureCRMSessionBootstrappedAsync_DoesNotBlockLoginPath(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/credits/personal":
			select {
			case started <- struct{}{}:
			default:
			}
			<-release
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: ts.URL}}

	begin := time.Now()
	repman.ensureCRMSessionBootstrappedAsync("tok-async")
	if elapsed := time.Since(begin); elapsed > 100*time.Millisecond {
		t.Fatalf("async bootstrap call took too long: %s", elapsed)
	}

	select {
	case <-started:
		// background flow started
	case <-time.After(500 * time.Millisecond):
		t.Fatal("expected async bootstrap flow to start background CRM request")
	}

	close(release)
}
