// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane@signal18.io>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"net"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
)

// TestGraphiteMetricsQueueBound reproduces issue #1675 (unbounded memory
// growth in the Graphite metrics queue when the carbon sink is unreachable)
// against a real, live cluster object — not a mock. It runs inside the
// regtest framework so it exercises the real ClusterGraphite instance,
// including the concurrent per-tick metric collection goroutine already
// running against the provisioned cluster, rather than a bare struct as in
// the cluster/cluster_graphite_test.go unit tests.
//
// Scenario:
//  1. Point the cluster's Graphite connection at a guaranteed-refused TCP
//     address (a reserved-then-closed ephemeral port) — deterministic
//     "unreachable sink" without depending on network/DNS specifics.
//  2. Set a small queue-limit and feed metrics far beyond it directly via
//     AddMetrics(), interleaved with failed SendGraphiteMetrics() flush
//     attempts (each of which requeues into the same bounded queue).
//  3. Assert the queue length never exceeds the configured cap — the core
//     #1675 acceptance criterion, and the one unit tests can't fully prove
//     since the queue can't be observed racing against a real concurrent
//     monitor loop.
//  4. Assert WARN0192 is raised once drops persist across sustained flush
//     cycles.
//  5. Restore connectivity and confirm a flush succeeds cleanly again.
//
// Because a real background monitor goroutine is also feeding/flushing this
// same cluster concurrently, assertions here are written to tolerate that
// interleaving (bounded retries, inequality checks) rather than assuming
// exclusive access — unlike the deterministic bare-struct unit tests.
func (regtest *RegTest) TestGraphiteMetricsQueueBound(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	logf := func(level, format string, args ...interface{}) {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGraphite, level, format, args...)
	}

	// Save everything this test touches so it can restore cluster state
	// regardless of pass/fail — this cluster keeps running other scenarios
	// after this one.
	originalHost := cl.Conf.GraphiteCarbonHost
	originalPort := cl.Conf.GraphiteCarbonPort
	originalLimit := cl.Conf.GraphiteMetricsQueueLimit
	originalMetrics := cl.Conf.GraphiteMetrics
	defer func() {
		cl.Conf.GraphiteCarbonHost = originalHost
		cl.Conf.GraphiteCarbonPort = originalPort
		cl.Conf.GraphiteMetricsQueueLimit = originalLimit
		cl.Conf.GraphiteMetrics = originalMetrics
		cl.SetGraphiteConnection(nil) // force a fresh connection using restored config
	}()

	cl.Conf.GraphiteMetrics = true
	const limit = 50
	cl.Conf.GraphiteMetricsQueueLimit = limit

	// Reserve then immediately free a port: dialing it is guaranteed to be
	// refused (deterministic, unlike assuming a fixed port has nothing
	// listening).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logf(config.LvlErr, "TEST graphite-queue-bound: failed to reserve a port: %s", err)
		return false
	}
	unreachablePort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cl.Conf.GraphiteCarbonHost = "127.0.0.1"
	cl.Conf.GraphiteCarbonPort = unreachablePort

	// Force this exact broken connection regardless of whatever the cluster
	// already had cached — Connect() dials lazily on first use, so this
	// struct alone doesn't fail until SendGraphiteMetrics() calls Connect().
	cl.SetGraphiteConnection(&graphite.Graphite{
		Host:     "127.0.0.1",
		Port:     unreachablePort,
		Protocol: "tcp",
		Timeout:  time.Second,
	})

	burst := make([]graphite.Metric, limit*2)
	for i := range burst {
		burst[i] = graphite.NewMetric("regtest.queue_bound.metric", "1", time.Now().Unix())
	}

	// Step 1: a single burst well over the cap must never leave the queue
	// over the cap — checked immediately after AddMetrics returns, since
	// boundMetrics() enforces the cap synchronously under lock on every
	// mutation (including the background monitor's concurrent AddMetrics
	// calls), so this holds regardless of what else is running.
	cl.AddMetrics(burst)
	if qlen := cl.QueueLength(); qlen > limit {
		logf(config.LvlErr, "TEST graphite-queue-bound: FAIL queue length %d exceeds cap %d after a single over-cap burst", qlen, limit)
		return false
	}
	logf("TEST", "graphite-queue-bound: queue length %d after burst, cap %d — OK", cl.QueueLength(), limit)

	// Step 2: repeatedly add over-cap bursts and attempt a flush (which will
	// fail against the unreachable sink and requeue) until WARN0192 is
	// raised, or we give up. Interleaving Add+Send guarantees fresh drops
	// land between consecutive checkSustainedDrops() evaluations even if
	// the real monitor loop's own tick occasionally wins the single-flight
	// guard instead of this goroutine's call.
	raised := false
	for i := 0; i < 20; i++ {
		cl.AddMetrics(burst)
		if qlen := cl.QueueLength(); qlen > limit {
			logf(config.LvlErr, "TEST graphite-queue-bound: FAIL queue length %d exceeds cap %d on iteration %d", qlen, limit, i)
			return false
		}
		cl.SendGraphiteMetrics()

		// Checked two ways because each has an opposite blind spot against
		// the real concurrent monitor loop's own ClearState() rotation:
		// CurState can be wiped by a ClearState() racing right after this
		// call raised it (before this check runs), while IsInState (which
		// reads OldState — see its own doc comment) won't see a freshly
		// raised state until the *next* rotation completes, which depends
		// on the ambient monitoring-ticker cadence, not this loop's 50ms
		// retries. Either one seeing it is sufficient proof the state is
		// genuinely open.
		if cl.StateMachine.CurState.Search("WARN0192") || cl.StateMachine.IsInState("WARN0192") {
			raised = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !raised {
		logf(config.LvlErr, "TEST graphite-queue-bound: FAIL WARN0192 was not raised after sustained drops against an unreachable sink")
		return false
	}
	logf("TEST", "graphite-queue-bound: WARN0192 raised after sustained drops, DroppedMetricsTotal=%d — OK", cl.DroppedMetricsTotal.Load())

	// Step 3: point at a known-good local listener — not the ambient/original
	// Graphite destination, whose reachability inside any given regtest
	// environment isn't actually guaranteed (embedded carbon may be
	// disabled, or this scenario's config.toml may not configure a real
	// sink at all) — and confirm a flush succeeds. This also exercises the
	// real reconnect-after-failure path: the broken conn was nilled on
	// failure, so this Connect() call must redial rather than reuse a dead
	// connection. Full WARN0192-lapses-after-recovery behavior is proven at
	// the state-machine level by
	// TestWARN0192_NotPreservedForeverWhenGraphiteDisabled in
	// cluster/cluster_graphite_test.go — this only checks the connection
	// itself recovers, deterministically.
	goodLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logf(config.LvlErr, "TEST graphite-queue-bound: FAIL cannot start a local listener for the recovery check: %s", err)
		return false
	}
	defer goodLn.Close()
	go func() {
		for {
			conn, err := goodLn.Accept()
			if err != nil {
				return
			}
			go func() {
				buf := make([]byte, 4096)
				for {
					if _, err := conn.Read(buf); err != nil {
						conn.Close()
						return
					}
				}
			}()
		}
	}()
	goodAddr := goodLn.Addr().(*net.TCPAddr)
	cl.Conf.GraphiteCarbonHost = "127.0.0.1"
	cl.Conf.GraphiteCarbonPort = goodAddr.Port
	cl.SetGraphiteConnection(nil)
	cl.AddMetrics([]graphite.Metric{graphite.NewMetric("regtest.queue_bound.recovery", "1", time.Now().Unix())})
	if err := cl.SendGraphiteMetrics(); err != nil {
		logf(config.LvlErr, "TEST graphite-queue-bound: FAIL flush against a known-reachable listener still errored: %s", err)
		return false
	}

	logf("TEST", "PASS: graphite metrics queue stayed bounded, dropped metrics tracked, WARN0192 raised on sustained drops, recovered after sink restored")
	return true
}
