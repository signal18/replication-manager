package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/signal18/replication-manager/config"
)

func makeJWTWithGitLabToken(t *testing.T, gitlabToken string) string {
	t.Helper()

	repman := &ReplicationManager{}
	repman.initKeys()

	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	claims["iss"] = "https://api.replication-manager.signal18.io"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["jti"] = "1"
	claims["token"] = gitlabToken
	claims["CustomUserInfo"] = map[string]interface{}{
		"Name":     "sso-user@example.com",
		"Password": "encrypted",
		"Role":     "Member",
		"email":    "sso-user@example.com",
		"profile":  "https://gitlab.signal18.io/sso-user",
	}

	sk, err := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	if err != nil {
		t.Fatalf("makeJWTWithGitLabToken: parse private key: %v", err)
	}

	tokenStr, err := signer.SignedString(sk)
	if err != nil {
		t.Fatalf("makeJWTWithGitLabToken: sign token: %v", err)
	}

	return tokenStr
}

func TestHandlerBillingPersonal_BootstrapAndRetry(t *testing.T) {
	var creditsCalls int
	var bootstrapCalls int

	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}

		switch r.URL.Path {
		case "/api/credits/personal":
			creditsCalls++
			if creditsCalls == 1 {
				w.WriteHeader(http.StatusPreconditionRequired)
				_, _ = w.Write([]byte(`{"error":"session_bootstrap_required"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"balance":123}`))
		case "/api/session/bootstrap":
			bootstrapCalls++
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer crm.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: crm.URL}}
	tok := makeJWTWithGitLabToken(t, "user-token")

	req := httptest.NewRequest(http.MethodGet, "/api/billing/personal", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()

	repman.handlerBillingPersonal(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "balance") {
		t.Fatalf("expected balance payload, got %q", w.Body.String())
	}
	if creditsCalls != 2 {
		t.Fatalf("expected 2 credits calls, got %d", creditsCalls)
	}
	if bootstrapCalls != 1 {
		t.Fatalf("expected 1 bootstrap call, got %d", bootstrapCalls)
	}
}

func TestHandlerBillingSubscription_ProxiesCRM(t *testing.T) {
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/clients/dbaas/subscription" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plan":"starter","status":"active"}`))
	}))
	defer crm.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: crm.URL}}
	tok := makeJWTWithGitLabToken(t, "user-token")

	req := httptest.NewRequest(http.MethodGet, "/api/billing/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()

	repman.handlerBillingSubscription(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "starter") {
		t.Fatalf("expected subscription payload, got %q", w.Body.String())
	}
}

func TestHandlerBillingTransactions_ValidatesQueryParams(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: "http://127.0.0.1:1"}}
	tok := makeJWTWithGitLabToken(t, "user-token")

	req := httptest.NewRequest(http.MethodGet, "/api/billing/transactions?direction=sideways", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()

	repman.handlerBillingTransactions(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%q", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid direction") {
		t.Fatalf("expected invalid direction error, got %q", w.Body.String())
	}
}

func TestHandlerBillingTransactions_BootstrapAndRetry(t *testing.T) {
	var txCalls int
	var bootstrapCalls int

	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("unexpected authorization header: %q", got)
		}

		switch r.URL.Path {
		case "/api/users/transactions":
			txCalls++
			if got := r.URL.Query().Get("direction"); got != "desc" {
				t.Fatalf("expected direction=desc, got %q", got)
			}
			if got := r.URL.Query().Get("limit"); got != "20" {
				t.Fatalf("expected limit=20, got %q", got)
			}
			if txCalls == 1 {
				w.WriteHeader(http.StatusPreconditionRequired)
				_, _ = w.Write([]byte(`{"error":"session_bootstrap_required"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"transactions":[{"id":"tx-1","amount":10}]}`))
		case "/api/session/bootstrap":
			bootstrapCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer crm.Close()

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: crm.URL}}
	tok := makeJWTWithGitLabToken(t, "user-token")

	req := httptest.NewRequest(http.MethodGet, "/api/billing/transactions?limit=20&offset=0&direction=desc", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()

	repman.handlerBillingTransactions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", w.Code, w.Body.String())
	}
	if txCalls != 2 {
		t.Fatalf("expected 2 transaction calls, got %d", txCalls)
	}
	if bootstrapCalls != 1 {
		t.Fatalf("expected 1 bootstrap call, got %d", bootstrapCalls)
	}
}
