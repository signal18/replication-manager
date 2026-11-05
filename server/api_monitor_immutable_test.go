// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	log "github.com/sirupsen/logrus"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// #2268: /api/monitor lists the immutable global settings (names only, never their values) to a
// logged-in caller, so the GUI shows them locked; a caller without a token does not get the list.
func TestMonitorListsImmutableSettings(t *testing.T) {
	repman := &ReplicationManager{Clusters: map[string]*cluster.Cluster{}, Conf: &config.Config{TokenTimeout: 1,
		ImmuableFlagMap: map[string]interface{}{"mail-to": "ops@acme.example", "api-credentials": "admin:secret"}}, Logrus: log.New()}
	repman.initKeys()
	for name, c := range map[string]struct {
		token string
		want  []string
	}{
		"logged in": {issueGlobalSettingsTestJWT(t, repman, "viewer"), []string{"api-credentials", "mail-to"}},
		"no login":  {"", nil},
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/monitor", nil)
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}
		rec := httptest.NewRecorder()
		repman.handlerMuxReplicationManager(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d %s", name, rec.Code, rec.Body.String())
		}
		var body struct {
			ImmutableSettings []string `json:"immutableSettings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Join(body.ImmutableSettings, ",") != strings.Join(c.want, ",") {
			t.Fatalf("%s: immutableSettings %v, want %v", name, body.ImmutableSettings, c.want)
		}
		if strings.Contains(rec.Body.String(), "admin:secret") || (c.token != "" && strings.Contains(rec.Body.String(), "ops@acme.example\"]")) {
			t.Fatalf("%s: an immutable value leaked into the response", name)
		}
	}
}
