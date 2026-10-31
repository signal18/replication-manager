// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// fakeOpenSVCKeyAgent is a v2 agent's /key endpoint: it answers fail while fail is set and
// keeps the last key written.
type fakeOpenSVCKeyAgent struct {
	mu      sync.Mutex
	fail    bool
	path    string
	key     string
	data    string
	written int
}

func (a *fakeOpenSVCKeyAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		http.Error(w, `{"status":1}`, http.StatusInternalServerError)
		return
	}
	var req struct{ Path, Key, Data string }
	json.NewDecoder(r.Body).Decode(&req)
	a.path, a.key, a.data = req.Path, req.Key, req.Data
	a.written++
	w.Write([]byte(`{"status":0}`))
}

func (a *fakeOpenSVCKeyAgent) last() (string, string, string, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.path, a.key, a.data, a.written
}

func rotateOpenSVCTestCluster(t *testing.T, agent *fakeOpenSVCKeyAgent) *Cluster {
	t.Helper()
	srv := httptest.NewUnstartedServer(agent)
	srv.EnableHTTP2 = true // GetHttpClient's transport speaks h2 only
	srv.StartTLS()
	t.Cleanup(srv.Close)
	cl := secretKvTestCluster(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cl.Conf.ProvHost = strings.TrimPrefix(srv.URL, "https://")
	cl.Conf.ProvOpensvcUseCollectorAPI = true // no p12 certificate, no v3 probe
	cl.Conf.SecretKvRetryInterval = 1
	return cl
}

// #2255: the rotation writes MYSQL_ROOT_PASSWORD in the cluster's env secret; a failure is
// WARN0274, written again by the tick with the current password, closed once written
func TestProvisionRotatePasswordsOpenSVCSecretBehind(t *testing.T) {
	agent := &fakeOpenSVCKeyAgent{fail: true}
	cl := rotateOpenSVCTestCluster(t, agent)

	if err := cl.ProvisionRotatePasswords("rotated"); err == nil {
		t.Fatal("a failed write of the secret is returned to the rotation")
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if !hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("WARN0274 not raised while the OpenSVC secret is behind")
	}
	if desc := cl.SecurityStateMachine.GetStates(); strings.Contains(strings.Join(desc, " "), "rotated") {
		t.Fatalf("the state carries the password: %v", desc)
	}

	agent.mu.Lock()
	agent.fail = false
	agent.mu.Unlock()
	time.Sleep(1100 * time.Millisecond) // secret-kv-retry-interval
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores() // the retry starts in the background
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if _, _, _, n := agent.last(); n > 0 && !cl.secretKv.orchestratorWriting.Load() {
			break
		}
	}
	path, key, data, n := agent.last()
	if n != 1 || path != "c1/sec/env" || key != "MYSQL_ROOT_PASSWORD" || data != cl.GetDbPass() {
		t.Fatalf("retry wrote %q %q (current password: %v), %d time(s)", path, key, data == cl.GetDbPass(), n)
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("WARN0274 still raised once the secret is written")
	}
}

// #2255: a write that succeeds at once raises nothing
func TestProvisionRotatePasswordsOpenSVCSecretWritten(t *testing.T) {
	agent := &fakeOpenSVCKeyAgent{}
	cl := rotateOpenSVCTestCluster(t, agent)
	if err := cl.ProvisionRotatePasswords("rotated"); err != nil {
		t.Fatalf("rotation: %v", err)
	}
	if _, key, data, n := agent.last(); n != 1 || key != "MYSQL_ROOT_PASSWORD" || data != "rotated" {
		t.Fatalf("secret not written: key %q, %d write(s)", key, n)
	}
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	if hasState(cl.SecurityStateMachine, "WARN0274") {
		t.Fatal("WARN0274 raised after a successful write")
	}
}
