// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/cron"
)

// goroutineSlack tolerates unrelated runtime/test goroutines appearing or
// disappearing around the measurement (GC workers, timers, -race
// bookkeeping). A real leak from N redundant SetActiveStatus calls grows the
// count by N, which dwarfs this slack.
const goroutineSlack = 3

// newSchedulerTestCluster builds a minimal Cluster sufficient to exercise
// SetActiveStatus's scheduler start/stop side effects, without the rest of
// cluster init (no servers, no state machine).
func newSchedulerTestCluster() *Cluster {
	return &Cluster{
		Name:      "cluster1",
		Conf:      &config.Config{MonitorScheduler: true},
		scheduler: cron.New(),
		Status:    ConstMonitorStandby,
	}
}

// waitForGoroutineAtLeast polls runtime.NumGoroutine() until it reaches at
// least min or the timeout elapses, returning the last observed count.
// cron.Cron.Start() spawns its run loop in a separate goroutine, so the
// count only settles after that goroutine has actually been scheduled.
func waitForGoroutineAtLeast(t *testing.T, min int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	got := runtime.NumGoroutine()
	for got < min && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		got = runtime.NumGoroutine()
	}
	return got
}

// waitForGoroutineAtMost polls until runtime.NumGoroutine() settles at or
// below max, or the timeout elapses, returning the last observed count.
// Callers must assert on the returned value: a timeout without convergence
// means teardown didn't happen, not that the assertion passed.
func waitForGoroutineAtMost(t *testing.T, max int, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	got := runtime.NumGoroutine()
	for got > max && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		got = runtime.NumGoroutine()
	}
	return got
}

// TestSetActiveStatusRedundantCallsDoNotLeakGoroutines is the regression test
// for GH-1674: repeated arbitration winner/loser ticks used to call
// SetActiveStatus(sameStatus) every tick, and since cron.Cron.Start() always
// does `go c.run()` regardless of whether it is already running, each
// redundant call leaked another scheduler goroutine forever. SetActiveStatus
// must now no-op when the status does not actually change.
func TestSetActiveStatusRedundantCallsDoNotLeakGoroutines(t *testing.T) {
	cluster := newSchedulerTestCluster()
	// Safety net: Cron.Stop() is a no-op if never started or already stopped,
	// so this fires harmlessly on the success path and only matters if a
	// t.Fatalf below skips the explicit Stop() further down.
	t.Cleanup(func() { cluster.scheduler.Stop() })

	before := runtime.NumGoroutine()

	cluster.SetActiveStatus(ConstMonitorActif)
	afterFirst := waitForGoroutineAtLeast(t, before+1, time.Second)
	if afterFirst < before+1 {
		t.Fatalf("expected scheduler goroutine to start on activation: before=%d after=%d", before, afterFirst)
	}

	// Simulate repeated arbitration winner ticks re-affirming the same status.
	for i := 0; i < 20; i++ {
		cluster.SetActiveStatus(ConstMonitorActif)
	}
	// Give any (buggy) extra goroutines time to actually spawn before sampling.
	time.Sleep(50 * time.Millisecond)

	afterRedundant := runtime.NumGoroutine()
	if delta := afterRedundant - afterFirst; delta > goroutineSlack {
		pprof.Lookup("goroutine").WriteTo(os.Stdout, 1)
		t.Fatalf("redundant SetActiveStatus calls leaked goroutines: afterFirst=%d afterRedundant=%d (delta=%d exceeds slack=%d)",
			afterFirst, afterRedundant, delta, goroutineSlack)
	}

	cluster.scheduler.Stop()
	afterStop := waitForGoroutineAtMost(t, before+goroutineSlack, time.Second)
	if delta := afterStop - before; delta > goroutineSlack {
		t.Fatalf("teardown left scheduler goroutine(s) running: before=%d afterStop=%d (delta=%d exceeds slack=%d)",
			before, afterStop, delta, goroutineSlack)
	}
}

// TestSetActiveStatusRealTransitionStartsAndStopsScheduler ensures the
// early-return guard only skips no-op transitions: an actual actif<->standby
// flip must still start/stop the scheduler as before.
func TestSetActiveStatusRealTransitionStartsAndStopsScheduler(t *testing.T) {
	cluster := newSchedulerTestCluster()
	// Safety net: if a t.Fatalf below fires after activation but before the
	// standby transition stops the scheduler, this still tears it down.
	t.Cleanup(func() { cluster.scheduler.Stop() })

	before := runtime.NumGoroutine()

	cluster.SetActiveStatus(ConstMonitorActif)
	afterStart := waitForGoroutineAtLeast(t, before+1, time.Second)
	if afterStart < before+1 {
		t.Fatalf("expected scheduler goroutine to start: before=%d after=%d", before, afterStart)
	}

	cluster.SetActiveStatus(ConstMonitorStandby)
	afterStop := waitForGoroutineAtMost(t, before+goroutineSlack, time.Second)
	if delta := afterStop - before; delta > goroutineSlack {
		t.Fatalf("expected scheduler goroutine to stop: before=%d afterStop=%d (delta=%d exceeds slack=%d)",
			before, afterStop, delta, goroutineSlack)
	}
}
