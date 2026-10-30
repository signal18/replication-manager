// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/config"
	log "github.com/sirupsen/logrus"
)

// The credential-less endpoints answer only callers whose TCP peer is in
// api-trusted-networks: a gateway relaying the public, or a forged X-Forwarded-For,
// never gets a session.
func TestCredentialLessEndpointsRefuseUntrustedCallers(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{
		APIDashboardUser:   "viewer",
		APIAutologin:       true,
		APIAutologinUser:   "admin",
		APITrustedNetworks: "127.0.0.1/32, ::1/128, 10.0.5.0/24",
	}, Logrus: log.New()}

	cases := []struct {
		remote  string
		forward string
		trusted bool
	}{
		{"127.0.0.1:40000", "", true},
		{"[::1]:40000", "", true},
		{"10.0.5.17:40000", "", true},
		{"192.168.10.43:40000", "", false},           // a gateway relaying the internet
		{"67.213.127.157:40000", "", false},          // the internet
		{"67.213.127.157:40000", "127.0.0.1", false}, // a forged forwarded header
		{"not-an-address", "", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/api/dashboard-token", nil)
		r.RemoteAddr = c.remote
		if c.forward != "" {
			r.Header.Set("X-Forwarded-For", c.forward)
		}
		if got := repman.fromTrustedNetwork(r); got != c.trusted {
			t.Errorf("%s (forwarded %q): trusted=%v, want %v", c.remote, c.forward, got, c.trusted)
		}
		if c.trusted {
			continue
		}
		for _, h := range []http.HandlerFunc{repman.dashboardTokenHandler, repman.autologinHandler} {
			w := httptest.NewRecorder()
			h(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("%s: answered %d, want 403", c.remote, w.Code)
			}
		}
	}
}
