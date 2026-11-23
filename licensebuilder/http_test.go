package licensebuilder

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestHandlerLogsRefusals: a request without a bearer and one with a malformed token each leave one
// log line naming the method, path and reason, and the token never appears in the log.
func TestHandlerLogsRefusals(t *testing.T) {
	var mu sync.Mutex
	var lines []string
	b := New(Config{IssuerURL: "http://127.0.0.1:1/realms/cloud18", AllowedClients: []string{"cloud18-crm"}}, func(f string, a ...interface{}) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(f, a...))
		mu.Unlock()
	})
	h := b.Handler()
	for _, auth := range []string{"", "Bearer not-a-jwt-SECRETTOKEN"} {
		req := httptest.NewRequest(http.MethodPost, "/queue", strings.NewReader("{}"))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q: %d, want 401", auth, rec.Code)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("%d log lines, want 2: %v", len(lines), lines)
	}
	if !strings.Contains(lines[0], "POST /queue by unknown caller refused (401): Authorization") ||
		!strings.Contains(lines[1], "refused (401): token refused") {
		t.Errorf("log lines %v", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "SECRETTOKEN") {
			t.Errorf("token in the log: %s", l)
		}
	}
}
