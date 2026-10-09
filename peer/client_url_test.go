package peer

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// One slash between the base URL and the endpoint, whatever each side carries (#1953).
func TestPeerClientJoinsOneSlash(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.URL.Path }))
	defer ts.Close()
	for _, base := range []string{ts.URL, ts.URL + "/"} {
		for _, ep := range []string{"/api/health", "api/health"} {
			pc := &PeerClient{baseURL: base, client: ts.Client(), headers: map[string]string{}}
			if _, _, err := pc.Get(ep); err != nil {
				t.Fatal(err)
			}
			if got != "/api/health" {
				t.Errorf("base %q endpoint %q: path %q, want /api/health", base, ep, got)
			}
		}
	}
}
