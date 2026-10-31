package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// #2042: the API refuses before starting the rotation: 409 with the reason; #2269: a
// credential set in a config file with no store keeping versions (versioning off here)
func TestRespondPasswordRotationRefusedImmutable(t *testing.T) {
	cl := &cluster.Cluster{Conf: &config.Config{
		SecretKey:       []byte("0123456789abcdef"),
		ConfRewrite:     true,
		ImmuableFlagMap: map[string]interface{}{"db-servers-credential": "root:x"},
	}}
	cl.Servers = append(cl.Servers, &cluster.ServerMonitor{State: "Master"})
	w := httptest.NewRecorder()
	respondPasswordRotation(w, cl)
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409", w.Code)
	}
	if body := w.Body.String(); !strings.HasPrefix(body, "Password rotation refused: credential change refused: db-servers-credential is set in a config file (immutable)") {
		t.Fatalf("body %q", body)
	}
}
