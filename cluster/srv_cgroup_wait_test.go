package cluster

import (
	"testing"
	"time"
)

// Two samples 60 s apart: 6 s of throttled time = 0.1 core refused, 30 of 600 periods
// throttled = 5 %, 12 s of io some = 0.2; a reset counter rates as 0; the first sample is
// not rated; the store survives a new ServerMonitor for the same URL.
func TestIngestWaitCountersRates(t *testing.T) {
	cl := &Cluster{Name: "t"}
	srv := &ServerMonitor{URL: "db1:3306", ClusterGroup: cl}
	t0 := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	first := srv.IngestWaitCounters(t0, WaitCounters{CpuNrPeriods: 1000, CpuNrThrottled: 10, CpuThrottledUsec: 1e6, IoPressureSomeUs: 5e6, MemPressureFullUs: 9e6})
	if first.Rated || srv.Wait == nil {
		t.Fatalf("first sample is kept but not rated: %+v", first)
	}
	srv2 := &ServerMonitor{URL: "db1:3306", ClusterGroup: cl} // recreated on a config reload
	r := srv2.IngestWaitCounters(t0.Add(60*time.Second), WaitCounters{CpuNrPeriods: 1600, CpuNrThrottled: 40, CpuThrottledUsec: 7e6, IoPressureSomeUs: 17e6, MemPressureFullUs: 1e6})
	if !r.Rated || r.WindowStart != t0 {
		t.Fatalf("second sample must be rated from the stored first one: %+v", r)
	}
	near := func(a, b float64) bool { return a > b-1e-9 && a < b+1e-9 }
	if !near(r.CpuThrottled, 0.1) || !near(r.CpuThrottledPeriods, 0.05) || !near(r.IoPsiSome, 0.2) || r.MemPsiFull != 0 || r.CpuPsiSome != 0 {
		t.Fatalf("rates: throttled %.3f periods %.3f io %.3f mem(reset) %.3f cpu psi %.3f", r.CpuThrottled, r.CpuThrottledPeriods, r.IoPsiSome, r.MemPsiFull, r.CpuPsiSome)
	}
	// a sample that does not advance the window is kept but not rated
	if r3 := srv2.IngestWaitCounters(t0.Add(60*time.Second), WaitCounters{}); r3.Rated {
		t.Fatalf("same window end: not rated")
	}
}
