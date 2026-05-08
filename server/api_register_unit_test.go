package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCRMCallConfirm_SendsBearerToken(t *testing.T) {
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected method POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/register/confirm" {
			t.Fatalf("expected path /api/register/confirm, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer user-token" {
			t.Fatalf("expected Authorization Bearer header, got %q", got)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer crm.Close()

	status, body, err := crmCallConfirm(crm.URL, "user-token", crmConfirmPayload{
		Email:     "admin@example.com",
		Password:  "secret",
		Domain:    "example",
		Subdomain: "dev",
		Zone:      "zone",
	})
	if err != nil {
		t.Fatalf("crmCallConfirm returned error: %v", err)
	}
	if status != http.StatusCreated {
		t.Fatalf("expected status 201, got %d body=%q", status, string(body))
	}
}

func TestCRMGetPlans_UsesCanonicalInstancePlansEndpoint(t *testing.T) {
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("expected method GET, got %s", r.Method)
		}
		if r.URL.Path != "/api/subscriptions/plans/instance" {
			t.Fatalf("expected path /api/subscriptions/plans/instance, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no Authorization header, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plans":[{"code":"free"}]}`))
	}))
	defer crm.Close()

	status, body, err := crmGetPlans(crm.URL)
	if err != nil {
		t.Fatalf("crmGetPlans returned error: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", status, string(body))
	}
}
