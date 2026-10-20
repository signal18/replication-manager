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

// newSchedulerOffTestCluster builds a cluster as started with monitoring-scheduler off:
// initScheduler never ran, so it has no scheduler (#2120).
func newSchedulerOffTestCluster(status string) *Cluster {
	return &Cluster{
		Name:     "cluster1",
		Conf:     &config.Config{SchedulerSLARotateCron: "0 0 0 1 * *"},
		Schedule: make(map[string]cron.Entry),
		Status:   status,
	}
}

// TestSetMonitoringSchedulerOnCreatesScheduler is the regression test for #2120: setting
// monitoring-scheduler to true through the API on a cluster started with it off called
// Start() on a nil scheduler and panicked. It now creates the scheduler with its jobs and
// starts it when the monitor is active.
func TestSetMonitoringSchedulerOnCreatesScheduler(t *testing.T) {
	cluster := newSchedulerOffTestCluster(ConstMonitorActif)
	before := runtime.NumGoroutine()

	cluster.SetMonitoringScheduler(true)
	if cluster.scheduler == nil {
		t.Fatal("expected a scheduler once monitoring-scheduler is set to true")
	}
	t.Cleanup(func() { cluster.scheduler.Stop() })
	if !cluster.Conf.MonitorScheduler {
		t.Fatal("expected monitoring-scheduler to be true")
	}
	if cluster.GetSchedule("slarotate") == nil {
		t.Fatal("expected the scheduler's jobs to be registered")
	}
	if after := waitForGoroutineAtLeast(t, before+1, time.Second); after < before+1 {
		t.Fatalf("expected the scheduler to start on an active monitor: before=%d after=%d", before, after)
	}
}

// TestSetMonitoringSchedulerOnStandbyDoesNotStart: on a standby monitor the scheduler is
// created, not started; SetActiveStatus starts it on activation.
func TestSetMonitoringSchedulerOnStandbyDoesNotStart(t *testing.T) {
	cluster := newSchedulerOffTestCluster(ConstMonitorStandby)
	before := runtime.NumGoroutine()

	cluster.SetMonitoringScheduler(true)
	if cluster.scheduler == nil {
		t.Fatal("expected a scheduler once monitoring-scheduler is set to true")
	}
	t.Cleanup(func() { cluster.scheduler.Stop() })
	time.Sleep(20 * time.Millisecond)
	if after := runtime.NumGoroutine(); after > before+goroutineSlack {
		t.Fatalf("expected no scheduler goroutine on a standby monitor: before=%d after=%d", before, after)
	}

	cluster.SetActiveStatus(ConstMonitorActif)
	if after := waitForGoroutineAtLeast(t, before+1, time.Second); after < before+1 {
		t.Fatalf("expected the scheduler to start on activation: before=%d after=%d", before, after)
	}
}

// TestSetMonitoringSchedulerOnKeepsExistingJobs: an existing scheduler is started again as
// it is, not created anew. Created anew, its jobs are registered on a new cron whose IDs
// start again at 1, while Schedule still holds the old IDs: removing an old ID can delete
// another job's new entry (here backupphysical, enabled after start).
func TestSetMonitoringSchedulerOnKeepsExistingJobs(t *testing.T) {
	cluster := newSchedulerOffTestCluster(ConstMonitorStandby)
	cluster.Conf.MonitorScheduler = true
	cluster.Conf.SchedulerBackupLogical, cluster.Conf.BackupLogicalCron = true, "0 0 1 * * *"
	cluster.initScheduler()
	cluster.Conf.SchedulerBackupPhysical, cluster.Conf.BackupPhysicalCron = true, "0 0 2 * * *"
	cluster.SetSchedulerBackupPhysical()
	t.Cleanup(func() { cluster.scheduler.Stop() })

	cluster.SetMonitoringScheduler(false)
	cluster.SetMonitoringScheduler(true)

	ids := map[cron.EntryID]bool{}
	for _, e := range cluster.scheduler.Entries() {
		ids[e.ID] = true
	}
	for _, title := range []string{"backuplogical", "backupphysical", "slarotate"} {
		entry := cluster.GetSchedule(title)
		if entry == nil || !ids[entry.ID] {
			t.Fatalf("job %s lost by setting monitoring-scheduler off then on", title)
		}
	}
	if len(ids) != 3 {
		t.Fatalf("expected 3 scheduled jobs, got %d", len(ids))
	}
}
