// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"
)

// TestRecentReseedRate_NotReadyUntilTwoSamples verifies the windowed "recent" rate
// stays unready (0, false) until at least two ticks have been sampled -- a single
// sample has no time delta to compute a rate from.
func TestRecentReseedRate_NotReadyUntilTwoSamples(t *testing.T) {
	server := &ServerMonitor{}

	if rate, ready := server.recentReseedRate(); ready || rate != 0 {
		t.Fatalf("expected (0, false) with no samples, got (%d, %v)", rate, ready)
	}

	server.reseedBytes.Store(1000)
	server.sampleReseedRate()
	if rate, ready := server.recentReseedRate(); ready || rate != 0 {
		t.Fatalf("expected (0, false) after a single sample, got (%d, %v)", rate, ready)
	}

	server.reseedBytes.Store(2000)
	time.Sleep(5 * time.Millisecond)
	server.sampleReseedRate()
	rate, ready := server.recentReseedRate()
	if !ready {
		t.Fatal("expected ready after two samples")
	}
	if rate <= 0 {
		t.Fatalf("expected a positive rate once bytes advanced between two samples, got %d", rate)
	}
}

// TestSampleReseedRate_WindowCapEvictsOldSamples verifies sampleReseedRate caps the
// window at reseedRateWindowSize, evicting the oldest sample once it's full -- the
// windowed rate must reflect CURRENT throughput, not grow unbounded.
func TestSampleReseedRate_WindowCapEvictsOldSamples(t *testing.T) {
	server := &ServerMonitor{}

	for i, bytes := range []int64{0, 100, 200, 300, 400} {
		server.reseedBytes.Store(bytes)
		server.sampleReseedRate()
		window, _ := server.reseedRateWindow.Load().([]reseedRateSample)
		if want := min(i+1, reseedRateWindowSize); len(window) != want {
			t.Fatalf("after %d sample(s): expected window length %d, got %d", i+1, want, len(window))
		}
	}

	// 5 samples taken (bytes 0,100,200,300,400), window capped at reseedRateWindowSize
	// (3) -- only the 3 most recent (200,300,400) should remain.
	window, _ := server.reseedRateWindow.Load().([]reseedRateSample)
	if window[0].bytes != 200 {
		t.Fatalf("expected the two oldest samples (bytes 0,100) evicted, oldest remaining has bytes=%d", window[0].bytes)
	}
}

// TestRecentReseedRate_ReflectsWindowNotWholeHistory verifies the rate math only looks
// at whatever is currently in the window -- i.e. once an early burst has been evicted
// (simulated here directly, deterministically, rather than via real elapsed time),
// recentReseedRate must reflect the recent stall, not the burst that's no longer in
// the window. This is the property that makes it useful where RateBytesSec (the
// lifetime average) is known to lag: a stalled reseed's average stays inflated for a
// long time after real throughput has collapsed, but the windowed rate catches up
// within a few ticks.
func TestRecentReseedRate_ReflectsWindowNotWholeHistory(t *testing.T) {
	server := &ServerMonitor{}
	base := time.Now()

	// Window as it would look right after the burst sample (bytes:0) has already been
	// evicted: 1MB arrived in the first second, then it crawls at ~1 byte/sec.
	server.reseedRateWindow.Store([]reseedRateSample{
		{bytes: 1_000_000, at: base},
		{bytes: 1_000_001, at: base.Add(1 * time.Second)},
		{bytes: 1_000_002, at: base.Add(2 * time.Second)},
	})

	rate, ready := server.recentReseedRate()
	if !ready {
		t.Fatal("expected ready with a full window")
	}
	// ~1 byte/sec over the window, nowhere near the ~1MB/sec the burst would suggest
	// if the window still held it.
	if rate > 100 {
		t.Fatalf("expected the windowed rate to reflect the recent stall (~1B/s), not the early burst, got %d B/s", rate)
	}
}

// TestSampleReseedRate_ClearedByLifecycle verifies begin/start/stopReseedProgress all
// reset the sample window, so a new reseed doesn't inherit stale samples from a
// previous one on the same server (which would produce a bogus recent rate for a few
// ticks -- e.g. a huge negative-clamped-to-zero delta against ancient bytes).
func TestSampleReseedRate_ClearedByLifecycle(t *testing.T) {
	server := &ServerMonitor{}
	server.reseedBytes.Store(500)
	server.sampleReseedRate()
	time.Sleep(5 * time.Millisecond) // guarantee a positive delta, see TestRecentReseedRate_NotReadyUntilTwoSamples
	server.reseedBytes.Store(1000)
	server.sampleReseedRate()
	if _, ready := server.recentReseedRate(); !ready {
		t.Fatal("expected ready before starting a new reseed (test setup sanity check)")
	}

	server.beginReseedProgress(&ReseedProgress{Backup: "next-reseed"}, 0)
	if rate, ready := server.recentReseedRate(); ready || rate != 0 {
		t.Fatalf("expected beginReseedProgress to clear the sample window, got (%d, %v)", rate, ready)
	}

	server.reseedBytes.Store(10)
	server.sampleReseedRate()
	time.Sleep(5 * time.Millisecond)
	server.reseedBytes.Store(20)
	server.sampleReseedRate()
	if _, ready := server.recentReseedRate(); !ready {
		t.Fatal("expected ready again after sampling the new reseed (test setup sanity check)")
	}

	server.stopReseedProgress()
	if rate, ready := server.recentReseedRate(); ready || rate != 0 {
		t.Fatalf("expected stopReseedProgress to clear the sample window, got (%d, %v)", rate, ready)
	}
}
