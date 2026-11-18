// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	log "github.com/sirupsen/logrus"
)

// needs jobs-upgrade asked without a CLI version (an older script over socat, or an older CLI)
// answers "false" and defers the upgrade; asked by the current CLI it answers as before and
// records the CLI version.
func TestNeedsJobsUpgradeDeferredWithoutCli(t *testing.T) {
	cl := &cluster.Cluster{Name: "c1", Conf: &config.Config{WorkingDir: t.TempDir()}, StateMachine: &state.StateMachine{},
		ConfigStateMachine: &state.StateMachine{}, Logrus: log.New()}
	cl.StateMachine.Init()
	cl.ConfigStateMachine.Init()
	node := &cluster.ServerMonitor{Host: "127.0.0.1", Port: "3306", URL: "127.0.0.1:3306", ClusterGroup: cl, Datadir: t.TempDir()}
	node.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	cl.Servers = []*cluster.ServerMonitor{node}
	repman := &ReplicationManager{Conf: &config.Config{}, Logrus: log.New(), Clusters: map[string]*cluster.Cluster{"c1": cl}}
	ask := func(ua string) string {
		req := httptest.NewRequest(http.MethodPost, "/api/clusters/c1/servers/127.0.0.1/3306/needs/jobs-upgrade", nil)
		req = mux.SetURLVars(req, map[string]string{"clusterName": "c1", "serverName": "127.0.0.1", "serverPort": "3306", "taskname": "jobs-upgrade"})
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		w := httptest.NewRecorder()
		repman.handlerMuxServerNeeds(w, req)
		return strings.TrimSpace(w.Body.String())
	}

	node.SetWaitJobsUpgradeCookie()
	if got := ask(""); got != "false" || node.HasWaitJobsUpgradeCookie() {
		t.Fatalf("an older script is not upgraded: %q, cookie %v", got, node.HasWaitJobsUpgradeCookie())
	}
	cl.CheckJobsUpgradeDeferred()
	if open := cl.ConfigStateMachine.GetOpenStates(); len(open) != 1 || open[0].ErrKey != "WARN0325" {
		t.Fatalf("WARN0325: %+v", open)
	}

	node.SetWaitJobsUpgradeCookie()
	if got := ask(jobapi.UserAgentPrefix + "v3.1.52"); got != "true" || node.JobsCliVersion != "v3.1.52" {
		t.Fatalf("the current CLI gets its upgrade: %q, version %q", got, node.JobsCliVersion)
	}
	cl.ConfigStateMachine.ClearState()
	cl.CheckJobsUpgradeDeferred()
	if open := cl.ConfigStateMachine.GetOpenStates(); len(open) != 0 {
		t.Fatalf("no longer deferred: %+v", open)
	}
}
