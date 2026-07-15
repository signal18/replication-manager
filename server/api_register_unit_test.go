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

// TestApplyCRMSubscriptionSelfHeal: a valid 200 persists the plan; anything
// else (non-200, malformed body, empty plan) is a no-op.
func TestApplyCRMSubscriptionSelfHeal(t *testing.T) {
	t.Run("200 with valid plan persists it", func(t *testing.T) {
		repman := newSyncTestRepman("")
		repman.applyCRMSubscriptionSelfHeal(http.StatusOK, []byte(`{"plan":"partner","uri":"acme.dev.fr-1"}`), "acme.dev.fr-1")

		if repman.Conf.Cloud18SubscriptionPlan != "partner" {
			t.Fatalf("expected plan to be persisted, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("non-200 is a no-op", func(t *testing.T) {
		repman := newSyncTestRepman("")
		repman.applyCRMSubscriptionSelfHeal(http.StatusForbidden, []byte(`{"plan":"partner"}`), "acme.dev.fr-1")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged on non-200, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("malformed body is a no-op", func(t *testing.T) {
		repman := newSyncTestRepman("")
		repman.applyCRMSubscriptionSelfHeal(http.StatusOK, []byte(`not json`), "acme.dev.fr-1")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged on malformed body, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("empty plan is a no-op", func(t *testing.T) {
		repman := newSyncTestRepman("")
		repman.applyCRMSubscriptionSelfHeal(http.StatusOK, []byte(`{"plan":""}`), "acme.dev.fr-1")

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected plan to stay unchanged on empty plan, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})
}

// TestBootOrdering_SelfHealSurvivesConfigReassignment reproduces the exact
// shape of server.go's InitConfig boot sequence: a local conf value gets
// copied into repman.Conf, then copied in *again* later (InitConfig does
// this both before and after the init_git block, to reconcile the local
// working copy used for building per-cluster configs). Self-heal persists
// directly onto repman.Conf, not onto the local conf value — so if it runs
// while a later "*repman.Conf = conf" reassignment is still pending, that
// reassignment silently reverts it back to the stale pre-boot plan. This
// was the actual node-2-boot bug: the fetch succeeded, but a later,
// unrelated reassignment clobbered it before boot finished.
//
// The fix moved the self-heal call in server.go to run after the last such
// reassignment. This test pins that ordering requirement down so a future
// refactor of InitConfig can't silently reintroduce the clobber.
func TestBootOrdering_SelfHealSurvivesConfigReassignment(t *testing.T) {
	crm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"plan":"partner","uri":"acme.dev.fr-1"}`))
	}))
	defer crm.Close()

	t.Run("self-heal BEFORE the reassignment gets clobbered (the bug)", func(t *testing.T) {
		conf := config.Config{
			Cloud18CrmApiUrl:        crm.URL,
			Cloud18Domain:           "acme",
			Cloud18SubDomain:        "dev",
			Cloud18SubDomainZone:    "fr-1",
			Cloud18SubscriptionPlan: "free",
		}
		repman := &ReplicationManager{Conf: &config.Config{}}

		*repman.Conf = conf // InitConfig's early sync (server.go:1903-equivalent)

		repman.syncSubscriptionPlanFromCRMWithToken("token") // self-heal fetches "partner" onto repman.Conf
		if repman.Conf.Cloud18SubscriptionPlan != "partner" {
			t.Fatalf("expected self-heal to fetch partner, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}

		*repman.Conf = conf // InitConfig's final sync (server.go:1938/1950-equivalent) — clobbers it

		if repman.Conf.Cloud18SubscriptionPlan != "free" {
			t.Fatalf("expected this ordering to reproduce the clobber bug (plan reverted to free), got %q — if this now passes, the bug's precondition changed and this test needs revisiting", repman.Conf.Cloud18SubscriptionPlan)
		}
	})

	t.Run("self-heal AFTER the reassignment survives (the fix)", func(t *testing.T) {
		conf := config.Config{
			Cloud18CrmApiUrl:        crm.URL,
			Cloud18Domain:           "acme",
			Cloud18SubDomain:        "dev",
			Cloud18SubDomainZone:    "fr-1",
			Cloud18SubscriptionPlan: "free",
		}
		repman := &ReplicationManager{Conf: &config.Config{}}

		*repman.Conf = conf // early sync
		*repman.Conf = conf // final sync — nothing pending after this point

		repman.syncSubscriptionPlanFromCRMWithToken("token") // self-heal runs last, as server.go now does

		if repman.Conf.Cloud18SubscriptionPlan != "partner" {
			t.Fatalf("expected self-heal to survive when run after the final reassignment, got %q", repman.Conf.Cloud18SubscriptionPlan)
		}
	})
}
