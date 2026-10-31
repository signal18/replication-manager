// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

// fakeOpenSVCKeyAgent is an agent's env secret: the v2 /key endpoint and the v3 data key of
// <ns>/sec/env. It answers 500 while fail is set and keeps the last value written.
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
	if strings.HasSuffix(r.URL.Path, "/data/key") { // v3
		switch r.Method {
		case http.MethodGet:
			if a.written == 0 {
				http.NotFound(w, r)
				return
			}
			w.Write([]byte(a.data))
		default:
			b, _ := io.ReadAll(r.Body)
			a.key, a.data = r.URL.Query().Get("name"), string(b)
			a.written++
			w.WriteHeader(http.StatusNoContent)
		}
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
	cl.Conf.ProvOpensvcUseCollectorAPI = true // no p12 certificate, no v3 probe: the v2 API
	cl.Conf.SecretKvRetryInterval = 60
	return cl
}

// openSVCTick is one tick of the secret check, waiting for what it started in the background.
func openSVCTick(t *testing.T, cl *Cluster) {
	t.Helper()
	cl.SecurityStateMachine.ClearState()
	cl.CheckSecretKvStores()
	for deadline := time.Now().Add(5 * time.Second); cl.secretKv.orchestratorWriting.Load(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the background write of the OpenSVC secret did not end")
		}
	}
}

// retryNow makes the next tick's retry due without waiting secret-kv-retry-interval.
func retryNow(cl *Cluster) {
	cl.secretKv.mu.Lock()
	cl.secretKv.orchestratorTried = time.Time{}
	cl.secretKv.mu.Unlock()
}

// #2255: the rotation writes MYSQL_ROOT_PASSWORD in the cluster's env secret; a failure is
// WARN0275, written again by the tick with the current password, closed once written
func TestProvisionRotatePasswordsOpenSVCSecretBehind(t *testing.T) {
	agent := &fakeOpenSVCKeyAgent{fail: true}
	cl := rotateOpenSVCTestCluster(t, agent)
	openSVCTick(t, cl) // the check after the start (a v2 agent: nothing read)

	if err := cl.ProvisionRotatePasswords("rotated"); err == nil {
		t.Fatal("a failed write of the secret is returned to the rotation")
	}
	openSVCTick(t, cl)
	if !hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("WARN0275 not raised while the OpenSVC secret is behind")
	}
	if desc := cl.SecurityStateMachine.GetStates(); strings.Contains(strings.Join(desc, " "), "rotated") {
		t.Fatalf("the state carries the password: %v", desc)
	}
	if _, _, _, n := agent.last(); n != 0 {
		t.Fatalf("written again before secret-kv-retry-interval: %d write(s)", n)
	}

	agent.mu.Lock()
	agent.fail = false
	agent.mu.Unlock()
	retryNow(cl)
	openSVCTick(t, cl) // the retry
	path, key, data, n := agent.last()
	if n != 1 || path != "c1/sec/env" || key != "MYSQL_ROOT_PASSWORD" || data != cl.GetDbPass() {
		t.Fatalf("retry wrote %q %q (current password: %v), %d time(s)", path, key, data == cl.GetDbPass(), n)
	}
	openSVCTick(t, cl)
	if hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("WARN0275 still raised once the secret is written")
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
	openSVCTick(t, cl)
	openSVCTick(t, cl)
	if hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("WARN0275 raised after a successful write")
	}
}

// #2255 review: a write overlapped by a later one (a rotation during a retry) does not close
// the state: the agent may have kept the older value, the retry writes the current one
func TestOpenSVCSecretOverlappedWriteKeepsStateOpen(t *testing.T) {
	agent := &fakeOpenSVCKeyAgent{}
	cl := rotateOpenSVCTestCluster(t, agent)
	openSVCTick(t, cl)
	// a write starts (number 1), a later one starts and ends while it runs
	gate := make(chan struct{})
	slow := &slowAgent{next: agent, gate: gate, arrived: make(chan struct{})}
	slowSrv := httptest.NewUnstartedServer(slow)
	slowSrv.EnableHTTP2 = true
	slowSrv.StartTLS()
	defer slowSrv.Close()
	older := cl.OpenSVCConnect()
	older.Host, older.Port, _ = net.SplitHostPort(strings.TrimPrefix(slowSrv.URL, "https://"))
	done := make(chan struct{})
	go func() {
		cl.writeOpenSVCRootPassword(older, "older")
		close(done)
	}()
	<-slow.arrived
	if err := cl.writeOpenSVCRootPassword(cl.OpenSVCConnect(), "newer"); err != nil {
		t.Fatalf("later write: %v", err)
	}
	close(gate)
	<-done
	openSVCTick(t, cl)
	if !hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("an overlapped write closed the state")
	}
	retryNow(cl)
	openSVCTick(t, cl)
	if _, _, data, _ := agent.last(); data != cl.GetDbPass() {
		t.Fatal("the retry did not write the current password")
	}
	openSVCTick(t, cl)
	if hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("WARN0275 still raised once the current password is written")
	}
}

// slowAgent holds each request until gate is closed, then hands it to next.
type slowAgent struct {
	next    http.Handler
	gate    chan struct{}
	once    sync.Once
	arrived chan struct{}
}

func (s *slowAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.once.Do(func() { close(s.arrived) })
	<-s.gate
	s.next.ServeHTTP(w, r)
}

// #2255 review: after a start (or a failover), a v3 agent's secret holding another password
// than the current one is found and written again; one holding it, or none, raises nothing
func TestOpenSVCSecretCheckedAfterStart(t *testing.T) {
	agent := &fakeOpenSVCKeyAgent{}
	cl := rotateOpenSVCTestCluster(t, agent)
	v3 := func() opensvc.Collector {
		svc := cl.OpenSVCConnect()
		svc.SetV3()
		return svc
	}
	cl.verifyOpenSVCRootPassword(v3()) // no secret: a cluster monitored, not provisioned
	if cl.secretKv.orchestratorBehind != "" {
		t.Fatalf("a missing secret marked behind: %q", cl.secretKv.orchestratorBehind)
	}
	seed := v3()
	if err := seed.CreateSecretKeyValue("c1", "env", "MYSQL_ROOT_PASSWORD", cl.GetDbPass()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cl.verifyOpenSVCRootPassword(v3())
	if cl.secretKv.orchestratorBehind != "" {
		t.Fatalf("a current secret marked behind: %q", cl.secretKv.orchestratorBehind)
	}
	if err := seed.CreateSecretKeyValue("c1", "env", "MYSQL_ROOT_PASSWORD", "old"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	cl.verifyOpenSVCRootPassword(v3())
	cl.secretKv.orchestratorChecked = true
	openSVCTick(t, cl) // WARN0275, the retry due at once
	if !hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("an old secret found after the start raised no WARN0275")
	}
	if _, _, data, _ := agent.last(); data != cl.GetDbPass() {
		t.Fatal("the old secret was not written again")
	}
	openSVCTick(t, cl)
	if hasState(cl.SecurityStateMachine, "WARN0275") {
		t.Fatal("WARN0275 still raised once the secret is written")
	}
}
