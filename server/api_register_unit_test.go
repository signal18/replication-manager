package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/config"
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

func TestParseSubscriptionPlan(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr bool
	}{
		{name: "valid plan", body: `{"plan":"support-services"}`, want: "support-services"},
		{name: "plan needs normalization", body: `{"plan":"  Support  "}`, want: "support"},
		{name: "missing plan field", body: `{"uri":"acme.dev.fr-1"}`, wantErr: true},
		{name: "empty plan field", body: `{"plan":""}`, wantErr: true},
		{name: "malformed json", body: `not json`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSubscriptionPlan([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got plan %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected plan %q, got %q", tt.want, got)
			}
		})
	}
}

func newSyncTestRepman(crmURL string) *ReplicationManager {
	return &ReplicationManager{
		Conf: &config.Config{
			Cloud18CrmApiUrl:        crmURL,
			Cloud18Domain:           "acme",
			Cloud18SubDomain:        "dev",
			Cloud18SubDomainZone:    "fr-1",
			Cloud18SubscriptionPlan: "free",
		},
	}
}

func TestSyncSubscriptionPlanFromCRMWithToken_PersistsPlanOnSuccess(t *testing.T) {
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/subscription" {
			t.Fatalf("expected path /api/subscription, got %s", r.URL.Path)
		}
		if got := r.URL.Query().Get("uri"); got != "acme.dev.fr-1" {
			t.Fatalf("expected uri=acme.dev.fr-1, got %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer peer-token" {
			t.Fatalf("expected Authorization Bearer peer-token, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plan":"support-services","uri":"acme.dev.fr-1"}`))
	}))
	defer crm.Close()

	repman := newSyncTestRepman(crm.URL)
	repman.syncSubscriptionPlanFromCRMWithToken("peer-token")

	if repman.Conf.Cloud18SubscriptionPlan != "support-services" {
		t.Fatalf("expected plan to be synced from CRM, got %q", repman.Conf.Cloud18SubscriptionPlan)
	}
}

// TestSyncSubscriptionPlanFromCRMWithToken_LeavesPlanUntouchedWhenDisconnected
// pins down the "don't auto-change on failure" requirement: when CRM cannot
// be reached, returns a non-200, or returns an unparseable/empty body, the
// local plan must be left exactly as it was — never reset or overwritten
// with an error value.
func TestSyncSubscriptionPlanFromCRMWithToken_LeavesPlanUntouchedWhenDisconnected(t *testing.T) {
	t.Run("CRM unreachable", func(t *testing.T) {
		repman := newSyncTestRepman("http://127.0.0.1:0") // nothing listening here
		repman.syncSubscriptionPlanFromCRMWithToken("peer-token")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged when CRM unreachable, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("CRM returns non-200", func(t *testing.T) {
		crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"not entitled"}`))
		}))
		defer crm.Close()

		repman := newSyncTestRepman(crm.URL)
		repman.syncSubscriptionPlanFromCRMWithToken("peer-token")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged on non-200, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("CRM returns malformed body", func(t *testing.T) {
		crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`not json`))
		}))
		defer crm.Close()

		repman := newSyncTestRepman(crm.URL)
		repman.syncSubscriptionPlanFromCRMWithToken("peer-token")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged on malformed body, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("instance URI not configured", func(t *testing.T) {
		repman := &ReplicationManager{
			Conf: &config.Config{Cloud18SubscriptionPlan: "free"},
		}
		repman.syncSubscriptionPlanFromCRMWithToken("peer-token")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged when URI incomplete, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})
}

// TestSyncSubscriptionPlanFromCRM_NoCredentials verifies the production
// entry point (token derivation + sync) never panics and never touches the
// local plan when Cloud18 credentials aren't configured — this is the path
// applyCloudConnect takes if InitGitConfig's own credential setup left the
// instance without usable Cloud18GitUser/password, and it must not block
// or corrupt registration success.
func TestSyncSubscriptionPlanFromCRM_NoCredentials(t *testing.T) {
	repman := &ReplicationManager{
		Conf: &config.Config{
			Cloud18Domain:           "acme",
			Cloud18SubDomain:        "dev",
			Cloud18SubDomainZone:    "fr-1",
			Cloud18SubscriptionPlan: "free",
		},
	}

	repman.syncSubscriptionPlanFromCRM()

	if repman.Conf.Cloud18SubscriptionPlan != "free" {
		t.Fatalf("expected plan to stay unchanged with no credentials, got %q", repman.Conf.Cloud18SubscriptionPlan)
	}
}
