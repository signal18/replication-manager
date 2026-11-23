// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// ssoCheckManager is a scope test manager with the SSO owner check configured and the cache
// wired by hand (no background loop: the tests refresh it themselves).
func ssoCheckManager(t *testing.T, owners ...string) (*ReplicationManager, *cluster.Cluster, map[string]string) {
	t.Helper()
	repman, c1, _ := newScopeTestManager(t)
	repman.Conf.Cloud18 = true
	repman.Conf.APITokenSSOOwnerCheck = true
	repman.Conf.APITokenSSOOwnerCheckMaxOwners = 10
	repman.Conf.Cloud18SettlementKeyID = "sk_test"
	repman.Conf.Secrets.Set("cloud18-settlement-secret", config.Secret{Value: "settlement-secret"})
	repman.ssoOwners.answers = map[string]ssoOwnerAnswer{}
	repman.ssoOwners.kick = make(chan struct{}, 1)
	repman.apiTokenSSOOwnerActive = repman.ssoOwnerActiveFromCache
	tokens := map[string]string{}
	for _, o := range owners {
		u := cluster.APIUser{User: o, Roles: map[string]bool{}}
		c1.SetUserGrants(&u, "db cluster token")
		c1.APIUsers[o] = u
		tok, err := repman.createAPITokenAs(o, "SSO", APITokenForm{Label: "mcp", Clusters: []string{"c1"}}, "")
		if err != nil {
			t.Fatal(err)
		}
		tokens[o] = tok.Token
	}
	return repman, c1, tokens
}

func tokenWorks(repman *ReplicationManager, c1 *cluster.Cluster, token string) bool {
	ok, _ := repman.IsValidClusterACL(bearerRequest(token, "/api/clusters/c1"), c1)
	return ok
}

// A disabled Cloud18 account loses its tokens once the cache knows it; the others keep theirs.
func TestSSOOwnerCheckRefusesDisabledAccount(t *testing.T) {
	repman, c1, tokens := ssoCheckManager(t, "blocked@x.io", "ok@x.io", "gitlab-only@x.io")
	if !tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("never answered: the owner is active (fail-open)")
	}
	repman.refreshSSOOwners(func(email string) (ssoOwnerAnswer, error) {
		switch email {
		case "blocked@x.io":
			return ssoOwnerAnswer{Exists: true, Active: false}, nil
		case "ok@x.io":
			return ssoOwnerAnswer{Exists: true, Active: true}, nil
		}
		return ssoOwnerAnswer{Exists: false, Active: false}, nil
	})
	if tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("a disabled Cloud18 account must lose its tokens")
	}
	if !tokenWorks(repman, c1, tokens["ok@x.io"]) {
		t.Fatal("an enabled account keeps its tokens")
	}
	if !tokenWorks(repman, c1, tokens["gitlab-only@x.io"]) {
		t.Fatal("an email the realm does not know is not refused")
	}
	// the off-switch gives the tokens back
	repman.Conf.APITokenSSOOwnerCheck = false
	if !tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("api-token-sso-owner-check=false: no refusal")
	}
	// so does a missing settlement credential
	repman.Conf.APITokenSSOOwnerCheck = true
	repman.Conf.Cloud18SettlementKeyID = ""
	if !tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("no settlement credential: the check does not run")
	}
}

// A CRM failure keeps the last answer and opens GWARN026; an answer closes it.
func TestSSOOwnerCheckCRMFailureKeepsLastAnswer(t *testing.T) {
	repman, c1, tokens := ssoCheckManager(t, "blocked@x.io", "new@x.io")
	repman.InitAlertStateMachine()
	repman.refreshSSOOwners(func(email string) (ssoOwnerAnswer, error) {
		if email == "blocked@x.io" {
			return ssoOwnerAnswer{Exists: true, Active: false}, nil
		}
		return ssoOwnerAnswer{}, errors.New("CRM answered 503")
	})
	if tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("disabled account refused")
	}
	down := func(string) (ssoOwnerAnswer, error) { return ssoOwnerAnswer{}, errors.New("CRM unreachable") }
	repman.refreshSSOOwners(down)
	if tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("a CRM failure must keep the last answer, not reopen the tokens")
	}
	if !tokenWorks(repman, c1, tokens["new@x.io"]) {
		t.Fatal("an owner never answered stays active")
	}
	repman.ProduceAPITokenSSOOwnerCheckState()
	if !repman.StateMachine.CurState.Search(ssoOwnerCheckStateKey) {
		t.Fatal("GWARN026 must be open while the CRM does not answer")
	}
	repman.ssoOwners.mu.Lock()
	failure := repman.ssoOwners.failure
	repman.ssoOwners.mu.Unlock()
	if !strings.Contains(failure, "2 of 2") {
		t.Fatalf("failure reason: %q", failure)
	}
	repman.refreshSSOOwners(func(string) (ssoOwnerAnswer, error) { return ssoOwnerAnswer{Exists: true, Active: true}, nil })
	repman.ssoOwners.mu.Lock()
	failure = repman.ssoOwners.failure
	repman.ssoOwners.mu.Unlock()
	if failure != "" {
		t.Fatalf("all answered: no failure, got %q", failure)
	}
	if !tokenWorks(repman, c1, tokens["blocked@x.io"]) {
		t.Fatal("re-enabled account gets its tokens back")
	}
}

// The cache is bounded: at most max owners are asked, the others stay active and are reported.
func TestSSOOwnerCheckBounded(t *testing.T) {
	repman, _, _ := ssoCheckManager(t, "a@x.io", "b@x.io", "c@x.io")
	repman.Conf.APITokenSSOOwnerCheckMaxOwners = 2
	var mu sync.Mutex
	asked := 0
	repman.refreshSSOOwners(func(string) (ssoOwnerAnswer, error) {
		mu.Lock()
		asked++
		mu.Unlock()
		return ssoOwnerAnswer{Exists: true, Active: true}, nil
	})
	if asked != 2 || len(repman.ssoOwners.answers) != 2 {
		t.Fatalf("asked %d, kept %d: want 2", asked, len(repman.ssoOwners.answers))
	}
	if !strings.Contains(repman.ssoOwners.failure, "1 owners above") {
		t.Fatalf("the owner left out must be reported: %q", repman.ssoOwners.failure)
	}
	// an owner whose tokens are all revoked leaves the cache
	repman.Conf.APITokenSSOOwnerCheckMaxOwners = 10
	if _, err := repman.revokeUserAPITokens("a@x.io", "admin"); err != nil {
		t.Fatal(err)
	}
	repman.refreshSSOOwners(func(string) (ssoOwnerAnswer, error) { return ssoOwnerAnswer{Exists: true, Active: true}, nil })
	if _, ok := repman.ssoOwners.answers["a@x.io"]; ok || len(repman.ssoOwners.answers) != 2 {
		t.Fatalf("revoked owner must leave the cache: %v", repman.ssoOwners.answers)
	}
}

// An owner the cache does not know asks for a refresh without blocking the request.
func TestSSOOwnerCheckUnknownOwnerKicksRefresh(t *testing.T) {
	repman, _, _ := ssoCheckManager(t)
	if !repman.ssoOwnerActiveFromCache("new@x.io") {
		t.Fatal("unknown owner is active")
	}
	select {
	case <-repman.ssoOwners.kick:
	default:
		t.Fatal("an unknown owner must ask for a refresh")
	}
	// a second ask while one is pending does not block
	repman.ssoOwnerActiveFromCache("new@x.io")
	repman.ssoOwnerActiveFromCache("new@x.io")
}

// The CRM call sends the settlement credential in headers and reads exists/active.
func TestCRMIdentityActive(t *testing.T) {
	repman, _, _ := ssoCheckManager(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/identity/active" || r.Header.Get("X-Settlement-Key-Id") != "sk_test" ||
			r.Header.Get("Authorization") != "Bearer settlement-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Query().Get("email") {
		case "blocked@x.io":
			w.Write([]byte(`{"email":"blocked@x.io","exists":true,"active":false,"checked_at":"2026-11-23T00:00:00Z"}`))
		case "down@x.io":
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			w.Write([]byte(`{"email":"x","exists":true,"active":true}`))
		}
	}))
	defer srv.Close()
	repman.Conf.Cloud18CrmApiUrl = srv.URL
	a, err := repman.crmIdentityActive("blocked@x.io")
	if err != nil || !a.Exists || a.Active {
		t.Fatalf("blocked: %+v %v", a, err)
	}
	if _, err := repman.crmIdentityActive("down@x.io"); err == nil {
		t.Fatal("a 503 is an error (the last answer is kept)")
	}
	repman.Conf.Cloud18SettlementKeyID = "wrong"
	if _, err := repman.crmIdentityActive("ok@x.io"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("a refused credential is an error: %v", err)
	}
}
