package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newSignupTestRepman() *ReplicationManager {
	return &ReplicationManager{Conf: &config.Config{}}
}

func TestHandlerSignup_ThrottlesPerIP(t *testing.T) {
	signupLimiter.resetForTests()
	t.Cleanup(signupLimiter.resetForTests)

	repman := newSignupTestRepman()

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(`{}`))
		req.RemoteAddr = "203.0.113.10:12345"
		w := httptest.NewRecorder()
		repman.handlerSignup(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 before burst exhausted at request %d", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(`{}`))
	req.RemoteAddr = "203.0.113.10:12345"
	w := httptest.NewRecorder()
	repman.handlerSignup(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 4th request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerSignupPromo_ThrottlesPerIP(t *testing.T) {
	signupPromoLimiter.resetForTests()
	t.Cleanup(signupPromoLimiter.resetForTests)

	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: "http://[::1"}}

	for i := 0; i < 10; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/signup/promo", nil)
		req.RemoteAddr = "203.0.113.11:12345"
		w := httptest.NewRecorder()
		repman.handlerSignupPromo(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 before burst exhausted at request %d", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/signup/promo", nil)
	req.RemoteAddr = "203.0.113.11:12345"
	w := httptest.NewRecorder()
	repman.handlerSignupPromo(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 11th request, got %d: %s", w.Code, w.Body.String())
	}
}

func TestSignupHandlers_ErrorResponseJSONAndSanitized(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{Cloud18CrmApiUrl: "http://[::1"}}

	t.Run("signup", func(t *testing.T) {
		signupLimiter.resetForTests()
		t.Cleanup(signupLimiter.resetForTests)

		payload := `{"first_name":"a","last_name":"b","username":"c","email":"a@b.co","password":"p"}`
		req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(payload))
		req.RemoteAddr = "203.0.113.12:12345"
		w := httptest.NewRecorder()
		repman.handlerSignup(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
		}

		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not valid JSON: %v, body=%q", err, w.Body.String())
		}
		if body["error"] == "" {
			t.Fatalf("expected JSON error field, got: %v", body)
		}
		if strings.Contains(w.Body.String(), "missing ']' in host") {
			t.Fatalf("raw internal error leaked in response: %s", w.Body.String())
		}
	})

	t.Run("signup promo", func(t *testing.T) {
		signupPromoLimiter.resetForTests()
		t.Cleanup(signupPromoLimiter.resetForTests)

		req := httptest.NewRequest(http.MethodGet, "/api/signup/promo", nil)
		req.RemoteAddr = "203.0.113.13:12345"
		w := httptest.NewRecorder()
		repman.handlerSignupPromo(w, req)

		if w.Code != http.StatusBadGateway {
			t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
		}

		var body map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("response is not valid JSON: %v, body=%q", err, w.Body.String())
		}
		if body["error"] == "" {
			t.Fatalf("expected JSON error field, got: %v", body)
		}
		if strings.Contains(w.Body.String(), "missing ']' in host") {
			t.Fatalf("raw internal error leaked in response: %s", w.Body.String())
		}
	})
}

func TestSignupOptionsPreflight(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{
		APIPublicURL:         "https://public.example",
		Cloud18Domain:        "acme",
		Cloud18SubDomain:     "app",
		Cloud18SubDomainZone: "test",
	}}

	t.Run("signup options", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/signup", nil)
		req.Header.Set("Origin", "https://acme.app.test")
		w := httptest.NewRecorder()
		repman.handlerSignup(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://acme.app.test" {
			t.Fatalf("unexpected allow-origin header: %q", got)
		}
		if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "POST") {
			t.Fatalf("expected POST in allow-methods, got %q", got)
		}
	})

	t.Run("signup promo options", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/signup/promo", nil)
		req.Header.Set("Origin", "https://acme.app.test")
		w := httptest.NewRecorder()
		repman.handlerSignupPromo(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://acme.app.test" {
			t.Fatalf("unexpected allow-origin header: %q", got)
		}
		if got := w.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
			t.Fatalf("expected GET in allow-methods, got %q", got)
		}
	})

	t.Run("signup options disallowed origin", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodOptions, "/api/signup", nil)
		req.Header.Set("Origin", "https://evil.example")
		w := httptest.NewRecorder()
		repman.handlerSignup(w, req)

		if w.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", w.Code)
		}
		if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Fatalf("expected no allow-origin for disallowed origin, got %q", got)
		}
	})
}

func TestHandlerSignup_IgnoreSpoofedXFFFromUntrustedRemote(t *testing.T) {
	signupLimiter.resetForTests()
	t.Cleanup(signupLimiter.resetForTests)

	repman := newSignupTestRepman()
	xffs := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"}

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(`{}`))
		req.RemoteAddr = "198.51.100.77:34567"
		req.Header.Set("X-Forwarded-For", xffs[i])
		w := httptest.NewRecorder()
		repman.handlerSignup(w, req)
		if w.Code == http.StatusTooManyRequests {
			t.Fatalf("unexpected 429 before burst exhausted at request %d", i+1)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/signup", strings.NewReader(`{}`))
	req.RemoteAddr = "198.51.100.77:34567"
	req.Header.Set("X-Forwarded-For", "203.0.113.200")
	w := httptest.NewRecorder()
	repman.handlerSignup(w, req)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 4th request despite spoofed XFF, got %d: %s", w.Code, w.Body.String())
	}
}
