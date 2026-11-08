// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
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
