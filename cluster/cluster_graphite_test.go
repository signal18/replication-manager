package cluster

import (
	"fmt"
	"net"
	"sync/atomic"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
)

func newTestClusterGraphite() *ClusterGraphite {
	return &ClusterGraphite{}
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

func TestClusterGraphite_AddMetrics_CapDropsOldest(t *testing.T) {
	// Bounded queue (T18): appending past the cap drops the oldest, keeps newest.
	cg := &ClusterGraphite{cl: &Cluster{Conf: &config.Config{GraphiteMetricsMaxQueueSize: 3}}}

	for i := 0; i < 5; i++ {
		cg.AddMetrics([]graphite.Metric{graphite.NewMetric(fmt.Sprintf("m%d", i), "1", int64(i))})
	}

	if len(cg.metrics) != 3 {
		t.Fatalf("expected queue capped at 3, got %d", len(cg.metrics))
	}
	want := []string{"m2", "m3", "m4"} // oldest (m0, m1) dropped
	for i, name := range want {
		if cg.metrics[i].Name != name {
			t.Fatalf("unexpected kept metric at %d: got %q want %q", i, cg.metrics[i].Name, name)
		}
	}
	if got := atomic.LoadUint64(&cg.DroppedMetrics); got != 2 {
		t.Fatalf("expected 2 dropped, got %d", got)
	}
}

func TestClusterGraphite_Requeue_RespectsCap(t *testing.T) {
	// requeue must also honor the cap, else a persistently-failing sink grows the
	// queue without bound via repeated requeue (the #1675 leak).
	cg := &ClusterGraphite{cl: &Cluster{Conf: &config.Config{GraphiteMetricsMaxQueueSize: 2}}}

	cg.AddMetrics([]graphite.Metric{graphite.NewMetric("new-1", "9", 9)})
	failed := []graphite.Metric{
		graphite.NewMetric("old-1", "1", 1),
		graphite.NewMetric("old-2", "2", 2),
		graphite.NewMetric("old-3", "3", 3),
	}
	cg.requeue(failed) // [old-1,old-2,old-3,new-1] -> cap 2 -> keep [old-3,new-1]

	if len(cg.metrics) != 2 {
		t.Fatalf("expected queue capped at 2 after requeue, got %d", len(cg.metrics))
	}
	want := []string{"old-3", "new-1"}
	for i, name := range want {
		if cg.metrics[i].Name != name {
			t.Fatalf("unexpected metric at %d: got %q want %q", i, cg.metrics[i].Name, name)
		}
	}
	if got := atomic.LoadUint64(&cg.DroppedMetrics); got != 2 {
		t.Fatalf("expected 2 dropped after requeue, got %d", got)
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
