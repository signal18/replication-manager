package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestBackupStallWatchdog_CancelsOnStall: once progress stops advancing, the
// watchdog must cancel within roughly the stall timeout. This is the software
// stand-in for "the backup volume was disconnected" — the write side stops
// accepting bytes, so the byte counter freezes.
func TestBackupStallWatchdog_CancelsOnStall(t *testing.T) {
	var progress atomic.Int64
	var canceled atomic.Bool
	var stallReported atomic.Bool
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	defer close(done)

	stallTimeout := 60 * time.Millisecond
	interval := 10 * time.Millisecond

	go backupStallWatchdog(done, func() { canceled.Store(true); cancel() }, &progress,
		stallTimeout, interval, func() { stallReported.Store(true) })

	// Simulate a healthy backup for a bit — bytes keep flowing, no cancel.
	for i := 0; i < 5; i++ {
		progress.Add(4096)
		time.Sleep(interval)
	}
	if canceled.Load() {
		t.Fatal("watchdog cancelled while progress was still advancing")
	}

	// Now the "volume dies": stop advancing progress and wait past the timeout.
	select {
	case <-ctx.Done():
		// good — cancelled after the stall
	case <-time.After(stallTimeout + 20*interval):
		t.Fatal("watchdog did not cancel after progress stalled")
	}
	if !stallReported.Load() {
		t.Fatal("stall callback was not invoked")
	}
	if !canceled.Load() {
		t.Fatal("cancel was not called on stall")
	}
}

// TestBackupStallWatchdog_NoCancelWhenProgressing: continuous progress must
// never trip the watchdog.
func TestBackupStallWatchdog_NoCancelWhenProgressing(t *testing.T) {
	var progress atomic.Int64
	var canceled atomic.Bool
	done := make(chan struct{})

	stallTimeout := 40 * time.Millisecond
	interval := 10 * time.Millisecond

	go backupStallWatchdog(done, func() { canceled.Store(true) }, &progress,
		stallTimeout, interval, nil)

	deadline := time.Now().Add(stallTimeout * 5)
	for time.Now().Before(deadline) {
		progress.Add(1)
		time.Sleep(interval / 2)
	}
	close(done)
	if canceled.Load() {
		t.Fatal("watchdog cancelled a backup that was making continuous progress")
	}
}

// TestBackupStallWatchdog_DisabledWhenZero: a zero timeout disables the
// watchdog entirely (opt-out), so it must return immediately and never cancel.
func TestBackupStallWatchdog_DisabledWhenZero(t *testing.T) {
	var progress atomic.Int64
	var canceled atomic.Bool
	done := make(chan struct{})
	defer close(done)

	returned := make(chan struct{})
	go func() {
		backupStallWatchdog(done, func() { canceled.Store(true) }, &progress, 0, 10*time.Millisecond, nil)
		close(returned)
	}()

	select {
	case <-returned:
		// good — disabled watchdog returns immediately without waiting on done
	case <-time.After(time.Second):
		t.Fatal("disabled watchdog (timeout=0) did not return immediately")
	}
	if canceled.Load() {
		t.Fatal("disabled watchdog cancelled the backup")
	}
}

// TestBoundedWait covers the D-state / hard-hung-mount mitigation: a wait that
// never completes must be abandoned (leaked=true) once the stall was fired, so
// the backup returns instead of hanging forever; a completing wait returns false.
func TestBoundedWait_LeaksStuckGoroutineAfterStall(t *testing.T) {
	// completes normally, no stall → false
	var wgOK sync.WaitGroup
	wgOK.Add(1)
	go func() { time.Sleep(10 * time.Millisecond); wgOK.Done() }()
	if boundedWait(&wgOK, make(chan struct{}), 50*time.Millisecond) {
		t.Fatal("boundedWait reported leak for a wg that completed")
	}

	// never completes + stall fired → leaked=true after grace
	var wgStuck sync.WaitGroup
	wgStuck.Add(1) // never Done() — simulates a D-state-wedged goroutine
	fired := make(chan struct{})
	close(fired) // watchdog already fired
	start := time.Now()
	if !boundedWait(&wgStuck, fired, 40*time.Millisecond) {
		t.Fatal("boundedWait did not report leak for a stuck wg after stall fired")
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("boundedWait returned before the grace period elapsed")
	}
}
