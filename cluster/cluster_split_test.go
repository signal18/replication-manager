// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// newTestClusterForSplit builds the minimal Cluster needed by ReconcileLostArbitrationMaster.
func newTestClusterForSplit(t *testing.T, masterURL string) *Cluster {
	t.Helper()
	conf := &config.Config{
		Arbitration:           true,
		ArbitrationSasSecret:  "test-secret",
		MonitoringTicker:      2,
		ArbitrationReadTimout: 0,
	}
	cl := &Cluster{
		Name:         "test-cluster",
		Conf:         conf,
		StateMachine: new(state.StateMachine),
	}
	cl.StateMachine.Init()

	if masterURL != "" {
		srv := &ServerMonitor{
			Host: "127.0.0.1",
			Port: "3306",
			URL:  masterURL,
		}
		cl.master = srv
		cl.Servers = []*ServerMonitor{srv}
	}
	return cl
}

// TestReconcileLostArbitrationMaster_404 verifies that a 404 from the arbitrator
// (no winner elected yet) causes an early return without touching the cluster state.
func TestReconcileLostArbitrationMaster_404(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/winner-master" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "no elected winner for authority cluster"})
	}))
	defer ts.Close()

	cl := newTestClusterForSplit(t, "127.0.0.1:3306")
	cl.Conf.ArbitrationSasHosts = ts.Listener.Addr().String()

	cl.ReconcileLostArbitrationMaster("auth-cluster")

	if cl.StateMachine.CurState.Search("WARN0082") {
		t.Error("expected no WARN0082 on 404 path")
	}
}

// TestReconcileLostArbitrationMaster_MasterMatches verifies that when the arbitrator
// returns the same master URL as the local master, no state change occurs.
func TestReconcileLostArbitrationMaster_MasterMatches(t *testing.T) {
	const masterURL = "127.0.0.1:3306"

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"winner_uuid": "uuid-winner",
			"master":      masterURL,
		})
	}))
	defer ts.Close()

	cl := newTestClusterForSplit(t, masterURL)
	cl.Conf.ArbitrationSasHosts = ts.Listener.Addr().String()

	cl.ReconcileLostArbitrationMaster("auth-cluster")

	if cl.StateMachine.CurState.Search("WARN0082") {
		t.Error("expected no WARN0082 when masters match")
	}
}

// TestReconcileLostArbitrationMaster_UnknownWinnerMaster verifies that when the
// winner's master URL is not found in the local server list (host canonicalization
// mismatch), WARN0082 is emitted instead of calling LostArbitration blindly.
func TestReconcileLostArbitrationMaster_UnknownWinnerMaster(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secret := r.Header.Get("X-Arbitration-Secret")
		if secret != "test-secret" {
			t.Errorf("expected X-Arbitration-Secret=test-secret, got %q", secret)
		}
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"winner_uuid": "uuid-winner",
			// Winner reports a different hostname than what the loser knows.
			"master": "db-primary:3306",
		})
	}))
	defer ts.Close()

	// Local cluster knows the master as 127.0.0.1:3306, not db-primary:3306.
	cl := newTestClusterForSplit(t, "127.0.0.1:3306")
	cl.Conf.ArbitrationSasHosts = ts.Listener.Addr().String()

	cl.ReconcileLostArbitrationMaster("auth-cluster")

	if !cl.StateMachine.CurState.Search("WARN0082") {
		t.Error("expected WARN0082 when winner master URL not found in local server list")
	}
}

// TestReconcileLostArbitrationMaster_SecretInHeader verifies the secret is sent via
// X-Arbitration-Secret header, not as a query parameter.
func TestReconcileLostArbitrationMaster_SecretInHeader(t *testing.T) {
	secretInQuery := false
	secretInHeader := false

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("secret") != "" {
			secretInQuery = true
		}
		if r.Header.Get("X-Arbitration-Secret") == "test-secret" {
			secretInHeader = true
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	cl := newTestClusterForSplit(t, "127.0.0.1:3306")
	cl.Conf.ArbitrationSasHosts = ts.Listener.Addr().String()

	cl.ReconcileLostArbitrationMaster("auth-cluster")

	if secretInQuery {
		t.Error("secret must not appear in URL query string")
	}
	if !secretInHeader {
		t.Error("secret must be sent in X-Arbitration-Secret header")
	}
}

// TestReconcileLostArbitrationMaster_NoMaster verifies early return when the cluster
// has no current master (e.g. during cluster startup or after all servers failed).
func TestReconcileLostArbitrationMaster_NoMaster(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cl := newTestClusterForSplit(t, "") // no master
	cl.Conf.ArbitrationSasHosts = ts.Listener.Addr().String()

	cl.ReconcileLostArbitrationMaster("auth-cluster")

	if called {
		t.Error("expected no HTTP call when cluster has no master")
	}
}
