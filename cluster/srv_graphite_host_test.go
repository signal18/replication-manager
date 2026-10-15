// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2063: the dashboard selects a server's mysql.<host>.* series by the graphiteHost it is
// given, so it must be the very token the series are written with, whatever the hostname:
// an OpenSVC one carries the cluster name, an on-premise one does not.
func TestGraphiteHostIsTheSeriesToken(t *testing.T) {
	for hostname, want := range map[string]string{
		"DB1.BLAB.SVC.CLOUD18":        "DB1-BLAB-SVC-CLOUD18",
		"LXC-MARIADB-TEST-MATHIAS-01": "LXC-MARIADB-TEST-MATHIAS-01",
		"MY HOST (A)":                 "MY_HOST_-A-",
	} {
		srv := &ServerMonitor{Variables: config.NewStringsMap()}
		srv.Variables.Set("HOSTNAME", hostname)
		if got := srv.graphiteHostname(); got != want {
			t.Errorf("%q: %q, want %q", hostname, got, want)
		}
		if srv.graphiteHostToken() != srv.graphiteHostname() {
			t.Errorf("%q: the DBU token differs from the series token", hostname)
		}
		srv.GraphiteHost.Store(srv.graphiteHostname())
		out, _ := json.Marshal(&struct {
			GraphiteHost *published[string] `json:"graphiteHost"`
		}{&srv.GraphiteHost})
		if !strings.Contains(string(out), `"graphiteHost":"`+want+`"`) {
			t.Errorf("%q: json %s", hostname, out)
		}
	}
}
