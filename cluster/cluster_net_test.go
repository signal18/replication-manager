package cluster

import (
	"math"
	"testing"
	"time"
)

func TestNetMbps(t *testing.T) {
	// 12.5 MB in 1 s = 100 Mb/s (bits, decimal mega -- never bytes)
	if got := netMbps(12_500_000, time.Second); math.Abs(got-100) > 1e-9 {
		t.Fatalf("netMbps = %v, want 100", got)
	}
	if got := netMbps(1, 0); got != 0 {
		t.Fatalf("zero window must yield 0, got %v", got)
	}
}

func TestNetCounterDeltaResetSafe(t *testing.T) {
	if netCounterDelta(10, 4) != 0 {
		t.Fatal("a counter going backwards (reset) must count 0, never underflow")
	}
	if netCounterDelta(4, 10) != 6 {
		t.Fatal("plain delta")
	}
}

func TestIngestNetCountersRatesAgainstPreviousSample(t *testing.T) {
	c := &Cluster{Name: "c"}
	t0 := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	first := c.IngestNetCounters(NetUnitDatabase, "db1", t0, 1_000, 2_000)
	if first.Rated {
		t.Fatal("the first sample only seeds the counters")
	}
	// +75 MB rx, +0 tx over 60 s -> 10 Mb/s rx
	r := c.IngestNetCounters(NetUnitDatabase, "db1", t0.Add(60*time.Second), 75_001_000, 2_000)
	if !r.Rated || math.Abs(r.RxMbps-10) > 1e-6 || r.TxMbps != 0 {
		t.Fatalf("rated reading wrong: %+v", r)
	}
	if !r.WindowStart.Equal(t0) {
		t.Fatalf("window must start at the previous sample end, got %v", r.WindowStart)
	}
	// counter reset: rate 0, never negative
	r = c.IngestNetCounters(NetUnitDatabase, "db1", t0.Add(120*time.Second), 500, 100)
	if r.RxMbps != 0 || r.TxMbps != 0 {
		t.Fatalf("reset must rate 0, got %+v", r)
	}
	// out-of-order sample: not rated, no panic
	r = c.IngestNetCounters(NetUnitDatabase, "db1", t0, 1, 1)
	if r.Rated {
		t.Fatal("a sample older than the previous one is not rated")
	}
}

func TestNetStatusFallbackYieldsToFreshPodReading(t *testing.T) {
	c := &Cluster{Name: "c"}
	now := time.Now()
	c.IngestNetCounters(NetUnitProxy, "prx1", now, 10, 10)
	if _, used := c.IngestNetStatusCounters(NetUnitProxy, "prx1", now, 5, 5); used {
		t.Fatal("a fresh pod reading must win over the status fallback")
	}
	// no pod reading at all for prx2 -> the fallback is recorded
	if r, used := c.IngestNetStatusCounters(NetUnitProxy, "prx2", now, 5, 5); !used || r.Source != NetSourceStatus {
		t.Fatalf("fallback must be recorded when nothing else reports: used=%v %+v", used, r)
	}
	// a source change restarts the counters: the first pod sample after status samples is not rated
	c.IngestNetStatusCounters(NetUnitProxy, "prx2", now.Add(time.Minute), 5_000_000, 5)
	if r := c.IngestNetCounters(NetUnitProxy, "prx2", now.Add(2*time.Minute), 9_000_000, 5); r.Rated {
		t.Fatal("status->pod switch must reseed, not rate across sources")
	}
}

func TestNetClusterMbpsSkipsStale(t *testing.T) {
	c := &Cluster{Name: "c"}
	st := c.netStoreRef()
	st.readings[netKey(NetUnitDatabase, "db1")] = &NetReading{Rated: true, RxMbps: 3, TxMbps: 1, ReceivedAt: time.Now()}
	st.readings[netKey(NetUnitApp, "old")] = &NetReading{Rated: true, RxMbps: 100, TxMbps: 100, ReceivedAt: time.Now().Add(-resourceSensorFreshnessWindow - time.Second)}
	st.readings[netKey(NetUnitProxy, "seed")] = &NetReading{Rated: false, ReceivedAt: time.Now()}
	if rx, tx := c.NetClusterMbps(time.Now()); math.Abs(rx-3) > 1e-9 || math.Abs(tx-1) > 1e-9 {
		t.Fatalf("cluster totals = %v/%v, want 3/1 (stale and unrated left out, in and out apart)", rx, tx)
	}
}
