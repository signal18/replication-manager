//go:build arbitrator
// +build arbitrator

package arbitrator

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/server"
)

// resetArbitratorTestState clears the package-level connection state shared
// by getArbitratorDB and friends so tests don't leak state into each other.
func resetArbitratorTestState(t *testing.T) {
	t.Helper()
	arbitratorDBMu.Lock()
	if arbitratorDB != nil {
		arbitratorDB.Close()
		arbitratorDB = nil
	}
	lastReconnectAttempt = time.Time{}
	arbitratorDBMu.Unlock()

	lastGoodHostMu.Lock()
	lastGoodHost = ""
	lastGoodHostMu.Unlock()
}

func TestArbitratorHostTrialOrder(t *testing.T) {
	hosts := []string{"h1:3306", "h2:3306", "h3:3306"}

	t.Run("no last-good host keeps configured order", func(t *testing.T) {
		resetArbitratorTestState(t)
		got := arbitratorHostTrialOrder(hosts)
		want := []string{"h1:3306", "h2:3306", "h3:3306"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("last-good host moves to front", func(t *testing.T) {
		resetArbitratorTestState(t)
		setLastGoodHost("h2:3306")
		got := arbitratorHostTrialOrder(hosts)
		want := []string{"h2:3306", "h1:3306", "h3:3306"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("last-good host not in current list is prepended, list unchanged otherwise", func(t *testing.T) {
		resetArbitratorTestState(t)
		setLastGoodHost("stale:3306")
		got := arbitratorHostTrialOrder(hosts)
		want := []string{"stale:3306", "h1:3306", "h2:3306", "h3:3306"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	})
}

func TestArbitratorMySQLHosts(t *testing.T) {
	t.Run("parses, trims, and drops empty entries", func(t *testing.T) {
		RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
			"arbitrator": {Hosts: " h1:3306 ,h2:3306,,h3:3306 "},
		}}
		got, err := arbitratorMySQLHosts()
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		want := []string{"h1:3306", "h2:3306", "h3:3306"}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("got %v, want %v", got, want)
		}
	})

	t.Run("empty hosts config errors", func(t *testing.T) {
		RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
			"arbitrator": {Hosts: ""},
		}}
		if _, err := arbitratorMySQLHosts(); err == nil {
			t.Fatal("expected error for empty hosts, got nil")
		}
	})
}

func TestGetArbitratorDB_SQLiteReconnectAndCooldown(t *testing.T) {
	resetArbitratorTestState(t)
	defer resetArbitratorTestState(t)

	tmpDir := t.TempDir()
	conf.WorkingDir = tmpDir
	RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
		"arbitrator": {
			ArbitratorDriver:         "sqlite",
			ArbitratorConnectTimeout: 3,
		},
	}}

	db, err := getArbitratorDB()
	if err != nil {
		t.Fatalf("initial connect failed: %s", err)
	}
	if db == nil {
		t.Fatal("expected non-nil db")
	}

	t.Run("healthy handle is reused without reconnecting", func(t *testing.T) {
		again, err := getArbitratorDB()
		if err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if again != db {
			t.Error("expected the same *sqlx.DB handle to be returned when healthy")
		}
	})

	t.Run("dead handle triggers a transparent reconnect", func(t *testing.T) {
		arbitratorDBMu.Lock()
		arbitratorDB.Close() // simulate the connection going bad
		arbitratorDBMu.Unlock()

		reconnected, err := getArbitratorDB()
		if err != nil {
			t.Fatalf("expected reconnect to succeed against the same sqlite file, got: %s", err)
		}
		if reconnected == db {
			t.Error("expected a new *sqlx.DB handle after reconnect")
		}
	})

	t.Run("cooldown short-circuits repeated attempts after a failure", func(t *testing.T) {
		resetArbitratorTestState(t)
		// Point at a working directory sqlite cannot open a database file in,
		// so every connect attempt fails.
		conf.WorkingDir = t.TempDir() + "/does-not-exist"

		if _, err := getArbitratorDB(); err == nil {
			t.Fatal("expected first attempt to fail")
		}
		firstAttempt := lastReconnectAttempt

		start := time.Now()
		if _, err := getArbitratorDB(); err == nil {
			t.Fatal("expected cooldown-gated attempt to fail")
		}
		elapsed := time.Since(start)

		if lastReconnectAttempt != firstAttempt {
			t.Error("expected lastReconnectAttempt to be unchanged during cooldown (no real retry attempted)")
		}
		if elapsed >= reconnectCooldown {
			t.Errorf("expected cooldown-gated call to return quickly, took %s", elapsed)
		}

		time.Sleep(reconnectCooldown)
		if _, err := getArbitratorDB(); err == nil {
			t.Fatal("expected retry after cooldown to still fail (bad path)")
		}
		if !lastReconnectAttempt.After(firstAttempt) {
			t.Error("expected a fresh retry attempt after the cooldown window elapsed")
		}
	})

	t.Run("lastReconnectAttempt resets to zero after a successful reconnect", func(t *testing.T) {
		resetArbitratorTestState(t)
		conf.WorkingDir = tmpDir

		if _, err := getArbitratorDB(); err != nil {
			t.Fatalf("unexpected error: %s", err)
		}
		if !lastReconnectAttempt.IsZero() {
			t.Error("expected lastReconnectAttempt to be reset to zero after a successful reconnect")
		}
	})
}

func TestArbitratorConnectTimeoutSeconds(t *testing.T) {
	cases := []struct {
		name       string
		configured int
		want       int
	}{
		{"positive value passes through", 5, 5},
		{"zero clamps to default", 0, defaultArbitratorConnectTimeoutSeconds},
		{"negative clamps to default", -1, defaultArbitratorConnectTimeoutSeconds},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
				"arbitrator": {ArbitratorConnectTimeout: tc.configured},
			}}
			if got := arbitratorConnectTimeoutSeconds(); got != tc.want {
				t.Errorf("arbitratorConnectTimeoutSeconds() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestHandlerHealth(t *testing.T) {
	t.Run("healthy sqlite backend returns 200", func(t *testing.T) {
		resetArbitratorTestState(t)
		conf.WorkingDir = t.TempDir()
		RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
			"arbitrator": {ArbitratorDriver: "sqlite", ArbitratorConnectTimeout: 3},
		}}

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rr := httptest.NewRecorder()
		handlerHealth(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("status = %d, want %d, body=%s", rr.Code, http.StatusOK, rr.Body.String())
		}
	})

	t.Run("unreachable backend returns 503, not a hang", func(t *testing.T) {
		resetArbitratorTestState(t)
		// A directory sqlite cannot open a database file in, so every
		// connect attempt fails and getArbitratorDB returns an error.
		conf.WorkingDir = t.TempDir() + "/does-not-exist"
		RepMan = &server.ReplicationManager{Confs: map[string]config.Config{
			"arbitrator": {ArbitratorDriver: "sqlite", ArbitratorConnectTimeout: 3},
		}}

		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rr := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			handlerHealth(rr, req)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("handlerHealth did not return within 5s")
		}

		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("status = %d, want %d, body=%s", rr.Code, http.StatusServiceUnavailable, rr.Body.String())
		}
	})
}
