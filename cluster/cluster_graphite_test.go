package cluster

import (
	"fmt"
	"net"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
	"github.com/signal18/replication-manager/utils/state"
)

func newTestClusterGraphite() *ClusterGraphite {
	return &ClusterGraphite{}
}

// newTestClusterGraphiteWithStateMachine builds a ClusterGraphite backed by
// a real *Cluster + *state.StateMachine (as opposed to newTestClusterGraphite's
// bare struct with cg.cl == nil), so tests can assert on actual WARN0192
// emission through cluster.SetState, not just the internal drop counters.
func newTestClusterGraphiteWithStateMachine() *ClusterGraphite {
	sm := &state.StateMachine{}
	sm.Init()
	cl := &Cluster{
		Name:         "cluster1",
		Conf:         &config.Config{},
		StateMachine: sm,
	}
	cg := &ClusterGraphite{cl: cl}
	cl.ClusterGraphite = cg
	return cg
}

func TestClusterGraphite_AddMetricsAppends(t *testing.T) {
	cg := newTestClusterGraphite()

	cg.AddMetrics([]graphite.Metric{graphite.NewMetric("a", "1", 1)})
	cg.AddMetrics([]graphite.Metric{graphite.NewMetric("b", "2", 2)})

	if len(cg.metrics) != 2 {
		t.Fatalf("expected 2 queued metrics, got %d", len(cg.metrics))
	}
	if cg.metrics[0].Name != "a" || cg.metrics[1].Name != "b" {
		t.Fatalf("unexpected metric order: %+v", cg.metrics)
	}
}

func TestClusterGraphite_SendGraphiteMetrics_SuccessClearsQueue(t *testing.T) {
	cg := newTestClusterGraphite()
	// nop graphite connection: SendMetrics always succeeds without any network I/O
	cg.SetGraphiteConnection(graphite.NewGraphiteNop("localhost", 0))

	cg.AddMetrics([]graphite.Metric{graphite.NewMetric("a", "1", 1)})

	if err := cg.SendGraphiteMetrics(); err != nil {
		t.Fatalf("expected nil error on successful send, got %v", err)
	}
	if len(cg.metrics) != 0 {
		t.Fatalf("expected queue to be cleared after successful send, got %d entries", len(cg.metrics))
	}
}

func TestClusterGraphite_SendGraphiteMetrics_EmptyQueueIsNoop(t *testing.T) {
	cg := newTestClusterGraphite() // cg.gc and cg.cl are both nil

	// With an empty queue, SendGraphiteMetrics must return before touching
	// cg.GetGraphiteConnection() (which would nil-deref cg.cl.Conf).
	if err := cg.SendGraphiteMetrics(); err != nil {
		t.Fatalf("expected nil error on empty queue, got %v", err)
	}
	if cg.flushing.Load() {
		t.Fatal("flushing flag must be released after an empty-queue no-op")
	}
}

func TestClusterGraphite_SendGraphiteMetrics_FailureRequeuesBatch(t *testing.T) {
	cg := newTestClusterGraphite()
	// Unreachable connection: bind an ephemeral port and close it immediately,
	// so dialing it is guaranteed to be refused (deterministic, unlike
	// assuming a fixed low port has nothing listening).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to reserve a port: %v", err)
	}
	freedPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	cg.SetGraphiteConnection(&graphite.Graphite{
		Host:     "127.0.0.1",
		Port:     freedPort,
		Protocol: "tcp",
	})

	original := []graphite.Metric{graphite.NewMetric("a", "1", 1), graphite.NewMetric("b", "2", 2)}
	cg.AddMetrics(original)

	sendErr := cg.SendGraphiteMetrics()
	if sendErr == nil {
		t.Fatal("expected an error connecting to an unreachable graphite host")
	}
	if len(cg.metrics) != len(original) {
		t.Fatalf("expected failed batch to be requeued, got %d entries", len(cg.metrics))
	}
	for i, m := range original {
		if cg.metrics[i] != m {
			t.Fatalf("requeued metric order mismatch at %d: got %+v want %+v", i, cg.metrics[i], m)
		}
	}
	if cg.flushing.Load() {
		t.Fatal("flushing flag must be released after a failed send")
	}
}

func TestClusterGraphite_Requeue_PrependsAheadOfNewMetrics(t *testing.T) {
	cg := newTestClusterGraphite()

	failedBatch := []graphite.Metric{graphite.NewMetric("old-1", "1", 1), graphite.NewMetric("old-2", "2", 2)}
	// Simulate producers appending new metrics while the failed send was in flight.
	cg.AddMetrics([]graphite.Metric{graphite.NewMetric("new-1", "3", 3)})

	cg.requeue(failedBatch)

	want := []string{"old-1", "old-2", "new-1"}
	if len(cg.metrics) != len(want) {
		t.Fatalf("expected %d metrics, got %d", len(want), len(cg.metrics))
	}
	for i, name := range want {
		if cg.metrics[i].Name != name {
			t.Fatalf("unexpected order at %d: got %q want %q", i, cg.metrics[i].Name, name)
		}
	}
}

func TestClusterGraphite_SendGraphiteMetrics_SingleFlightSkipsConcurrentFlush(t *testing.T) {
	cg := newTestClusterGraphite()
	cg.SetGraphiteConnection(graphite.NewGraphiteNop("localhost", 0))

	queued := []graphite.Metric{graphite.NewMetric("a", "1", 1)}
	cg.AddMetrics(queued)

	// Simulate a flush already in progress.
	cg.flushing.Store(true)

	if err := cg.SendGraphiteMetrics(); err != nil {
		t.Fatalf("expected nil error when skipping a concurrent flush, got %v", err)
	}
	if len(cg.metrics) != len(queued) {
		t.Fatalf("skipped flush must not touch the queue, got %d entries", len(cg.metrics))
	}
}

func TestClusterGraphite_AddMetrics_CapEnforced(t *testing.T) {
	cg := newTestClusterGraphite() // cg.cl is nil -> falls back to defaultGraphiteMetricsQueueLimit

	over := defaultGraphiteMetricsQueueLimit + 10
	batch := make([]graphite.Metric, over)
	for i := range batch {
		batch[i] = graphite.NewMetric(fmt.Sprintf("m%d", i), "1", int64(i))
	}

	cg.AddMetrics(batch)

	if len(cg.metrics) != defaultGraphiteMetricsQueueLimit {
		t.Fatalf("expected queue capped at %d, got %d", defaultGraphiteMetricsQueueLimit, len(cg.metrics))
	}
	if got := cg.DroppedMetricsTotal.Load(); got != 10 {
		t.Fatalf("expected 10 dropped metrics, got %d", got)
	}
	// Oldest dropped, newest retained: the first surviving entry is m10.
	if cg.metrics[0].Name != "m10" {
		t.Fatalf("expected oldest metrics dropped, first entry is %q", cg.metrics[0].Name)
	}
	if last := cg.metrics[len(cg.metrics)-1]; last.Name != fmt.Sprintf("m%d", over-1) {
		t.Fatalf("expected newest metric retained, got %q", last.Name)
	}
}

func TestClusterGraphite_Requeue_CapEnforcedOldestDropped(t *testing.T) {
	cg := newTestClusterGraphite()

	over := defaultGraphiteMetricsQueueLimit + 5
	failedBatch := make([]graphite.Metric, over)
	for i := range failedBatch {
		failedBatch[i] = graphite.NewMetric(fmt.Sprintf("old%d", i), "1", int64(i))
	}

	cg.requeue(failedBatch)

	if len(cg.metrics) != defaultGraphiteMetricsQueueLimit {
		t.Fatalf("expected queue capped at %d, got %d", defaultGraphiteMetricsQueueLimit, len(cg.metrics))
	}
	if got := cg.DroppedMetricsTotal.Load(); got != 5 {
		t.Fatalf("expected 5 dropped metrics, got %d", got)
	}
	if cg.metrics[0].Name != "old5" {
		t.Fatalf("expected oldest metrics dropped first, first entry is %q", cg.metrics[0].Name)
	}
}

func TestClusterGraphite_BoundMetrics_NilClusterSafe(t *testing.T) {
	cg := &ClusterGraphite{} // cg.cl is nil, matches other bare-struct tests in this file

	bounded, dropped := cg.boundMetrics(
		[]graphite.Metric{graphite.NewMetric("a", "1", 1)},
		[]graphite.Metric{graphite.NewMetric("b", "2", 2)},
	)
	if dropped != 0 || len(bounded) != 2 {
		t.Fatalf("expected no drop under the limit with a nil cl, got dropped=%d len=%d", dropped, len(bounded))
	}
}

func TestClusterGraphite_CheckSustainedDrops_RequiresConsecutiveCycles(t *testing.T) {
	cg := newTestClusterGraphite()
	cg.SetGraphiteConnection(graphite.NewGraphiteNop("localhost", 0))

	// One cycle with a drop must not raise WARN0192 (cg.cl is nil here so
	// checkSustainedDrops can't call SetState anyway; this test only
	// exercises the counter, not the state machine).
	cg.DroppedMetricsTotal.Add(1)
	cg.checkSustainedDrops()
	if cg.consecutiveDropFlushes != 1 {
		t.Fatalf("expected consecutiveDropFlushes=1 after first drop-observing cycle, got %d", cg.consecutiveDropFlushes)
	}

	// A second consecutive cycle with more drops crosses the threshold.
	cg.DroppedMetricsTotal.Add(1)
	cg.checkSustainedDrops()
	if cg.consecutiveDropFlushes != graphiteSustainedDropThreshold {
		t.Fatalf("expected consecutiveDropFlushes=%d, got %d", graphiteSustainedDropThreshold, cg.consecutiveDropFlushes)
	}

	// A clean cycle (no new drops) resets the counter.
	cg.checkSustainedDrops()
	if cg.consecutiveDropFlushes != 0 {
		t.Fatalf("expected consecutiveDropFlushes reset to 0 after a clean cycle, got %d", cg.consecutiveDropFlushes)
	}
}

func TestClusterGraphite_CheckSustainedDrops_RaisesWARN0192OnRealStateMachine(t *testing.T) {
	cg := newTestClusterGraphiteWithStateMachine()

	if cg.cl.StateMachine.CurState.Search("WARN0192") {
		t.Fatal("WARN0192 must not be raised before any drop is observed")
	}

	// First drop-observing cycle: below the sustained threshold, must not raise yet.
	cg.DroppedMetricsTotal.Add(1)
	cg.checkSustainedDrops()
	if cg.cl.StateMachine.CurState.Search("WARN0192") {
		t.Fatal("WARN0192 must not be raised after a single drop-observing cycle")
	}

	// Second consecutive drop-observing cycle: crosses graphiteSustainedDropThreshold.
	cg.DroppedMetricsTotal.Add(1)
	cg.checkSustainedDrops()
	if !cg.cl.StateMachine.CurState.Search("WARN0192") {
		t.Fatal("expected WARN0192 to be raised on the real StateMachine once drops are sustained")
	}

	// A clean cycle resets the counter; nothing re-raises the state that
	// tick, so per the StateMachine's own OldState/CurState rotation it
	// won't reappear once ClearState() runs (that rotation is exercised by
	// PreserveState's own tests, not re-tested here — this only asserts the
	// checkSustainedDrops side: it stops calling AddState).
	cg.checkSustainedDrops()
	if cg.consecutiveDropFlushes != 0 {
		t.Fatalf("expected consecutiveDropFlushes reset to 0 after a clean cycle, got %d", cg.consecutiveDropFlushes)
	}
}

// TestWARN0192_NotPreservedForeverWhenGraphiteDisabled reproduces a bug
// caught in review: cluster.go originally bundled WARN0192 into the same
// PreserveState call as WARN0139/WARN0140, gated by
// `!(cluster.Conf.GraphiteMetrics && heartbeats%5 == 0)`. That condition
// becomes permanently true once GraphiteMetrics is turned off (the first
// operand is false), so PreserveState("WARN0192") would run every tick
// forever — and since PreserveState re-adds any key still present in
// OldState, a WARN0192 raised before Graphite was disabled would never
// lapse. The fix splits WARN0192 into its own conditional that also
// requires GraphiteMetrics to still be enabled. This test exercises a bare
// *state.StateMachine directly with both conditionals (mirroring
// TestRejoinCatalog_WARN0191_PreservedAcrossIntermediateTicks in
// cluster_bck_test.go, which tests the pstates30 pattern the same way),
// rather than the full monitor tick loop.
func TestWARN0192_NotPreservedForeverWhenGraphiteDisabled(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()

	// Flush tick: sustained drops raise WARN0192, GraphiteMetrics enabled.
	sm.AddState("WARN0192", state.State{ErrType: "WARNING", ErrDesc: "queue over limit", ErrFrom: "GRAPHITE"})
	sm.ClearState()
	if !sm.IsInState("WARN0192") {
		t.Fatal("setup: WARN0192 should be open after being raised on a flush tick")
	}

	preserveWARN0192 := func(graphiteMetrics bool, heartbeats int64) {
		if graphiteMetrics && heartbeats%5 != 0 {
			sm.PreserveState("WARN0192")
		}
	}

	// Intermediate (non-flush) ticks, GraphiteMetrics still enabled: must be
	// preserved so it doesn't flap.
	for _, heartbeats := range []int64{1, 2, 3, 4} {
		preserveWARN0192(true, heartbeats)
		sm.ClearState()
	}
	if !sm.IsInState("WARN0192") {
		t.Fatal("WARN0192 dropped across non-flush ticks while GraphiteMetrics stayed enabled")
	}

	// GraphiteMetrics gets disabled: SendGraphiteMetrics no longer runs, so
	// nothing naturally resolves WARN0192 — it must stop being preserved
	// instead, so it resolves rather than persisting forever.
	preserveWARN0192(false, 6)

	// GetLastResolvedStates() diffs OldState against CurState as it stands
	// *before* the next ClearState() rotates them — check it here, matching
	// TestRejoinCatalog_WARN0191_PreservedAcrossIntermediateTicks's ordering.
	resolvedHas := false
	for _, s := range sm.GetLastResolvedStates() {
		if s.ErrKey == "WARN0192" {
			resolvedHas = true
		}
	}
	if !resolvedHas {
		t.Fatal("expected WARN0192 to resolve once GraphiteMetrics was disabled and nothing preserved it")
	}

	sm.ClearState()
	if sm.IsInState("WARN0192") {
		t.Fatal("WARN0192 persisted after GraphiteMetrics was disabled — it must resolve, not persist forever")
	}
}
