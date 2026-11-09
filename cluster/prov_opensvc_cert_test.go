// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/credstore"
	"github.com/signal18/replication-manager/opensvc/opensvctest"
	"github.com/signal18/replication-manager/utils/state"
)

// Many OpenSVCConnect callers failing at once: exactly one of them sees the first failure (one
// ERR log), and a success followed by a failure is a first failure again.
func TestOpenSVCCertStateConcurrentFirstFailure(t *testing.T) {
	var c openSVCCertState
	loadErr := errors.New("no such file")
	var firsts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if first, _ := c.observe(nil, "", loadErr); first {
				firsts.Add(1)
			}
		}()
	}
	wg.Wait()
	if n := firsts.Load(); n != 1 {
		t.Fatalf("first failures = %d, want 1", n)
	}
	if first, _ := c.observe(nil, "", loadErr); first {
		t.Fatalf("a repeated failure counted as first")
	}
	c.observe([]byte("p12"), "pw", nil)
	if first, _ := c.observe(nil, "", loadErr); !first {
		t.Fatalf("a failure after a success is not first")
	}
}

// The first load sets the fingerprint, the same p12 again is no rotation, a new p12 or a new
// password is.
func TestOpenSVCCertStateRotation(t *testing.T) {
	var c openSVCCertState
	if _, rotated := c.observe([]byte("p12-a"), "pw", nil); rotated {
		t.Fatalf("first load counted as rotation")
	}
	if _, rotated := c.observe([]byte("p12-a"), "pw", nil); rotated {
		t.Fatalf("same p12 counted as rotation")
	}
	if _, rotated := c.observe([]byte("p12-b"), "pw", nil); !rotated {
		t.Fatalf("new p12 not seen as rotation")
	}
	if _, rotated := c.observe([]byte("p12-b"), "pw2", nil); !rotated {
		t.Fatalf("new password not seen as rotation")
	}
	// a failure in between keeps the last fingerprint
	c.observe(nil, "", errors.New("gone"))
	if _, rotated := c.observe([]byte("p12-b"), "pw2", nil); rotated {
		t.Fatalf("the same p12 back after a failure counted as rotation")
	}
}

func openSVCStoreTestCluster(t *testing.T, p12Path, host string) *Cluster {
	t.Helper()
	cl := secretKvTestCluster(t)
	cl.Conf.ProvOrchestrator = config.ConstOrchestratorOpenSVC
	cl.Conf.ProvOpensvcP12Certificate = p12Path
	cl.Conf.ProvHost = host
	cl.StateMachine = new(state.StateMachine)
	cl.StateMachine.Init()
	cl.SetOrchestratorVersion("v3")
	return cl
}

func testStoreItems(n int) []credstore.StoreItem {
	items := make([]credstore.StoreItem, n)
	for i := range items {
		items[i] = credstore.StoreItem{Entry: orchestratorStoreEntry, Key: "KEY_" + string(rune('A'+i)), Version: credstore.Version{Value: "v"}}
	}
	return items
}

// No p12: WriteAll requeues everything at once with the certificate cause, without calling the
// agent, and WARN0099 is set.
func TestOpenSVCStoreCertNotLoadedFailsFast(t *testing.T) {
	cl := openSVCStoreTestCluster(t, filepath.Join(t.TempDir(), "missing.p12"), "127.0.0.1:1")
	items := testStoreItems(3)
	start := time.Now()
	failed, err := cl.openSVCStore().WriteAll(context.Background(), items, credstore.Timeout(5*time.Second))
	if !errors.Is(err, errOpenSVCCertNotLoaded) {
		t.Fatalf("err = %v, want errOpenSVCCertNotLoaded", err)
	}
	if len(failed) != len(items) {
		t.Fatalf("requeued %d items, want %d", len(failed), len(items))
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s: the agent was called", d)
	}
	if !cl.openSVCCertFailed() || !cl.StateMachine.CurState.Search("WARN0099") {
		t.Fatalf("certificate failure not tracked (failed=%v)", cl.openSVCCertFailed())
	}
	if _, _, rerr := cl.openSVCStore().ReadCurrent("KEY_A"); !errors.Is(rerr, errOpenSVCCertNotLoaded) {
		t.Fatalf("ReadCurrent err = %v, want errOpenSVCCertNotLoaded", rerr)
	}
}

// A v3 agent that never answers: 5 items end within about one budget, all requeued, and the
// agent sees one request, not one per item.
func TestOpenSVCStoreWriteAllDeadAgentV3(t *testing.T) {
	var requests atomic.Int32
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	p12Path := filepath.Join(dir, "cluster.p12")
	// secretKvTestCluster has no opensvc-p12-secret: the p12 is encrypted with an empty password
	if err := os.WriteFile(p12Path, opensvctest.P12(t, "repman", ""), 0600); err != nil {
		t.Fatal(err)
	}
	cl := openSVCStoreTestCluster(t, p12Path, u.Host)

	items := testStoreItems(5)
	budget := 300 * time.Millisecond
	start := time.Now()
	failed, werr := cl.openSVCStore().WriteAll(context.Background(), items, credstore.Timeout(budget))
	elapsed := time.Since(start)
	if werr == nil {
		t.Fatalf("expected an error from a dead agent")
	}
	if errors.Is(werr, errOpenSVCCertNotLoaded) {
		t.Fatalf("certificate not loaded: the test p12 is wrong (%v)", werr)
	}
	if len(failed) != len(items) {
		t.Fatalf("requeued %d items, want %d", len(failed), len(items))
	}
	if elapsed > 3*budget {
		t.Fatalf("took %s for %d items: not one budget (%s)", elapsed, len(items), budget)
	}
	if n := requests.Load(); n != 1 {
		t.Fatalf("agent saw %d requests, want 1 (the first read, then the budget is spent)", n)
	}
}

// kvAgent is a v3 agent that keeps one value per key of the env secret: GET 404 on an absent
// key, POST 409 on an existing one (the client then PUTs), PUT stores.
type kvAgent struct {
	mu   sync.Mutex
	keys map[string]string
	fail bool // every request answers 500 (an agent failing)
}

func (a *kvAgent) setFail(b bool) {
	a.mu.Lock()
	a.fail = b
	a.mu.Unlock()
}

func (a *kvAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail {
		http.Error(w, `{"status":500}`, http.StatusInternalServerError)
		return
	}
	name := r.URL.Query().Get("name")
	cur, ok := a.keys[name]
	switch r.Method {
	case http.MethodGet:
		if !ok {
			http.Error(w, `{"status":404}`, http.StatusNotFound)
			return
		}
		w.Write([]byte(cur))
	case http.MethodPost:
		if ok {
			http.Error(w, `{"status":409}`, http.StatusConflict)
			return
		}
		fallthrough
	case http.MethodPut:
		b, _ := io.ReadAll(r.Body)
		a.keys[name] = string(b)
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *kvAgent) get(k string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.keys[k]
	return v, ok
}

// Seen on dev4: the rotation wrote MYSQL_ROOT_PASSWORD directly, then queued it; the queue found
// the new value already there and wrote no _OLD, the old password was lost. The rotation now
// writes through the store writer: the superseded password lands in MYSQL_ROOT_PASSWORD_OLD.
func TestOpenSVCRotationKeepsOldPasswordV3(t *testing.T) {
	agent := &kvAgent{keys: map[string]string{}}
	server := httptest.NewTLSServer(agent)
	defer server.Close()
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p12Path := filepath.Join(t.TempDir(), "cluster.p12")
	if err := os.WriteFile(p12Path, opensvctest.P12(t, "repman", ""), 0600); err != nil {
		t.Fatal(err)
	}
	cl := openSVCStoreTestCluster(t, p12Path, u.Host)

	if err := cl.ProvisionRotatePasswords("first"); err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	if _, ok := agent.get("MYSQL_ROOT_PASSWORD_OLD"); ok {
		t.Fatalf("an _OLD written on the first value")
	}
	if err := cl.ProvisionRotatePasswords("second"); err != nil {
		t.Fatalf("second rotation: %v", err)
	}
	if v, _ := agent.get("MYSQL_ROOT_PASSWORD"); v != "second" {
		t.Fatalf("MYSQL_ROOT_PASSWORD not the rotated password")
	}
	if v, ok := agent.get("MYSQL_ROOT_PASSWORD_OLD"); !ok || v != "first" {
		t.Fatalf("MYSQL_ROOT_PASSWORD_OLD does not hold the superseded password (present=%v)", ok)
	}
}

// kvAgentCluster is a v3 cluster whose agent is a kvAgent.
func kvAgentCluster(t *testing.T, agent *kvAgent) *Cluster {
	t.Helper()
	server := httptest.NewTLSServer(agent)
	t.Cleanup(server.Close)
	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p12Path := filepath.Join(t.TempDir(), "cluster.p12")
	if err := os.WriteFile(p12Path, opensvctest.P12(t, "repman", ""), 0600); err != nil {
		t.Fatal(err)
	}
	return openSVCStoreTestCluster(t, p12Path, u.Host)
}

// #2357 review point 1: the provisioning seeds went through a direct write, so a reprovision over
// an existing secret lost <KEY>_OLD, as the rotation did. Through the store writer, the
// superseded root password lands in MYSQL_ROOT_PASSWORD_OLD.
func TestOpenSVCProvisionSeedKeepsOldPassword(t *testing.T) {
	agent := &kvAgent{keys: map[string]string{"MYSQL_ROOT_PASSWORD": "before"}}
	cl := kvAgentCluster(t, agent)
	if cl.Conf.Secrets == nil {
		cl.Conf.Secrets = config.NewSecrets(nil)
	}
	cl.Conf.Secrets.Set("db-servers-credential", config.Secret{Value: "root:after"})
	if errs := cl.seedOrchestratorPasswords(cl.OpenSVCConnect()); len(errs) != 0 {
		t.Fatalf("seed errors: %v", errs)
	}
	if v, _ := agent.get("MYSQL_ROOT_PASSWORD"); v != "after" {
		t.Fatal("MYSQL_ROOT_PASSWORD not the provisioned password")
	}
	if v, ok := agent.get("MYSQL_ROOT_PASSWORD_OLD"); !ok || v != "before" {
		t.Fatalf("MYSQL_ROOT_PASSWORD_OLD does not hold the superseded password (present=%v)", ok)
	}
	if _, ok := agent.get("SHARDPROXY_ROOT_PASSWORD"); !ok {
		t.Fatal("SHARDPROXY_ROOT_PASSWORD not written (an empty value is written, as before)")
	}
}

// #2357 review point 7: a rotation against a failing agent keeps WARN0275's cause open; once the
// agent answers, the retry writes the password and _OLD holds the one before the failed rotation.
func TestOpenSVCRotationFailingAgentThenRecovers(t *testing.T) {
	agent := &kvAgent{keys: map[string]string{}}
	cl := kvAgentCluster(t, agent)
	if err := cl.ProvisionRotatePasswords("first"); err != nil {
		t.Fatalf("first rotation: %v", err)
	}
	agent.setFail(true)
	if err := cl.ProvisionRotatePasswords("second"); err == nil {
		t.Fatal("a failing agent accepted the write")
	}
	cl.secretKv.mu.Lock()
	behind := cl.secretKv.orchestratorBehind
	cl.secretKv.mu.Unlock()
	if behind == "" {
		t.Fatal("no WARN0275 cause after the failed write")
	}
	agent.setFail(false)
	if err := cl.writeOpenSVCRootPassword(cl.OpenSVCConnect(), "second"); err != nil {
		t.Fatalf("retry: %v", err)
	}
	cl.secretKv.mu.Lock()
	behind = cl.secretKv.orchestratorBehind
	cl.secretKv.mu.Unlock()
	if behind != "" {
		t.Fatalf("WARN0275 cause kept after the retry: %s", behind)
	}
	if v, _ := agent.get("MYSQL_ROOT_PASSWORD"); v != "second" {
		t.Fatal("the retry did not write the rotated password")
	}
	if v, _ := agent.get("MYSQL_ROOT_PASSWORD_OLD"); v != "first" {
		t.Fatal("MYSQL_ROOT_PASSWORD_OLD does not hold the password before the failed rotation")
	}
}

// #2357 review point 1: the seed against a failing agent leaves each password key in the version
// queue, which retries it (the direct write it replaced was followed by an unconditional queue
// push), and returns each key's error.
func TestOpenSVCProvisionSeedFailingAgentQueues(t *testing.T) {
	agent := &kvAgent{keys: map[string]string{}, fail: true}
	cl := kvAgentCluster(t, agent)
	if cl.Conf.Secrets == nil {
		cl.Conf.Secrets = config.NewSecrets(nil)
	}
	cl.Conf.Secrets.Set("db-servers-credential", config.Secret{Value: "root:pw"})
	cl.Conf.Secrets.Set("shardproxy-credential", config.Secret{Value: "root:spw"})
	cl.APIUsers = map[string]APIUser{"admin": {User: "admin", Password: "apw"}}
	errs := cl.seedOrchestratorPasswords(cl.OpenSVCConnect())
	for _, k := range []string{"REPLICATION_MANAGER_PASSWORD", "MYSQL_ROOT_PASSWORD", "SHARDPROXY_ROOT_PASSWORD"} {
		if errs[k] == nil {
			t.Fatalf("%s: no error from a failing agent", k)
		}
	}
	queued := map[string]bool{}
	for _, it := range cl.storeQueue().Take() {
		if it.Entry == orchestratorStoreEntry {
			queued[it.Key] = true
		}
	}
	for _, k := range []string{"REPLICATION_MANAGER_PASSWORD", "MYSQL_ROOT_PASSWORD", "SHARDPROXY_ROOT_PASSWORD"} {
		if !queued[k] {
			t.Fatalf("%s not queued for a retry (queued %v)", k, queued)
		}
	}
}
