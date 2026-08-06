package cluster

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

type fakeEvalPlugin struct {
	name  string
	calls int32
	res   logplugin.EvaluateResult
}

func (f *fakeEvalPlugin) Name() string { return f.name }
func (f *fakeEvalPlugin) Evaluate(src logplugin.LogSource) logplugin.EvaluateResult {
	atomic.AddInt32(&f.calls, 1)
	return f.res
}

// TestCachedPluginEval_OffTickAndNoFlap verifies the two guarantees of the
// off-tick plugin evaluation:
//  1. Evaluate() (the subprocess) runs in a background goroutine, not inline —
//     the first call returns an empty result and triggers an async refresh.
//  2. Once cached, repeated calls within the refresh interval return the SAME
//     cached result WITHOUT re-running Evaluate — so the per-tick apply keeps
//     asserting the findings and they never flap.
func TestCachedPluginEval_OffTickAndNoFlap(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()
	cluster := &Cluster{Conf: &config.Config{}, StateMachine: sm}
	server := &ServerMonitor{ClusterGroup: cluster, URL: "db1:3306"}

	p := &fakeEvalPlugin{name: "test", res: logplugin.EvaluateResult{MetricName: "x", CurrentCount: 7}}
	src := logplugin.LogSource{ServerURL: "db1:3306"}

	// (1) First call: nothing cached yet → empty result, and it must NOT have run
	// Evaluate inline (it's dispatched to a goroutine).
	if got := server.cachedPluginEval(p, src); got.MetricName != "" || got.CurrentCount != 0 {
		t.Fatalf("first call must return empty result (eval is async), got %+v", got)
	}

	// Wait for the background Evaluate to populate the cache.
	deadline := time.Now().Add(2 * time.Second)
	for {
		server.pluginEvalMu.Lock()
		e := server.pluginEval["test"]
		have := e != nil && e.have
		server.pluginEvalMu.Unlock()
		if have || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if c := atomic.LoadInt32(&p.calls); c != 1 {
		t.Fatalf("expected exactly 1 background Evaluate, got %d", c)
	}

	// (2) Repeated calls within the interval: cached result, no new Evaluate.
	for i := 0; i < 8; i++ {
		got := server.cachedPluginEval(p, src)
		if got.MetricName != "x" || got.CurrentCount != 7 {
			t.Fatalf("expected cached result {x,7}, got %+v", got)
		}
	}
	if c := atomic.LoadInt32(&p.calls); c != 1 {
		t.Fatalf("cached reads must not re-run Evaluate within the interval; Evaluate calls=%d (want 1)", c)
	}
}
