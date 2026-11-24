// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	log "github.com/sirupsen/logrus"
)

// need-config-refresh: 200 once when the cookie is set, 204 with no body for "nothing to do"
// (not a 500: the access log records no error for it), 500 for an unknown server or cluster.
func TestNeedConfigRefreshStatus(t *testing.T) {
	cl := &cluster.Cluster{Name: "c1", Conf: &config.Config{WorkingDir: t.TempDir()}, StateMachine: &state.StateMachine{}, Logrus: log.New()}
	cl.StateMachine.Init()
	node := &cluster.ServerMonitor{Host: "127.0.0.1", Port: "3306", URL: "127.0.0.1:3306", ClusterGroup: cl, Datadir: t.TempDir()}
	node.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	cl.Servers = []*cluster.ServerMonitor{node}
	repman := &ReplicationManager{Conf: &config.Config{}, Logrus: log.New(), Clusters: map[string]*cluster.Cluster{"c1": cl}}
	ask := func(clusterName, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/api/clusters/"+clusterName+"/servers/"+host+"/3306/need-config-refresh", nil)
		req = mux.SetURLVars(req, map[string]string{"clusterName": clusterName, "serverName": host, "serverPort": "3306"})
		w := httptest.NewRecorder()
		repman.handlerMuxServerNeedConfigRefresh(w, req)
		return w
	}

	if w := ask("c1", "127.0.0.1"); w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Fatalf("nothing to do: %d %q, want 204 and no body", w.Code, w.Body.String())
	}
	node.SetConfigRefreshCookie()
	if w := ask("c1", "127.0.0.1"); w.Code != http.StatusOK {
		t.Fatalf("refresh asked: %d %q", w.Code, w.Body.String())
	}
	if w := ask("c1", "127.0.0.1"); w.Code != http.StatusNoContent {
		t.Fatalf("the cookie is consumed by the first 200: %d", w.Code)
	}
	if w := ask("c1", "10.0.0.9"); w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown server: %d, want 500", w.Code)
	}
	if w := ask("nope", "127.0.0.1"); w.Code != http.StatusInternalServerError {
		t.Fatalf("unknown cluster: %d, want 500", w.Code)
	}
}
