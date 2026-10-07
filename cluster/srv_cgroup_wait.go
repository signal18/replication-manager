// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// WaitCounters are the cgroup v2 wait counters of a database service as the sensor reads
// them, raw and cumulative: cpu.stat (quota throttling) and the three pressure files
// (PSI some/full totals, microseconds). They say what the CPU usage alone never says:
// whether the service WAITED -- for a quota that refused it cycles, for a CPU, for the
// disk, for memory. Engine-agnostic: the same files under MariaDB and PostgreSQL, read by
// the jobs sidecar next to the DBU maxima (collect_dbu / report_usage). Raw only: repman
// derives the rates with the previous sample and absorbs resets, like the network
// counters (cluster_net.go). Collected to be GRAPHED first; which of them drives the
// dynamic CPU grow is the decision they are meant to inform (#1904).
type WaitCounters struct {
	CpuNrPeriods      uint64 `json:"cpuNrPeriods"`
	CpuNrThrottled    uint64 `json:"cpuNrThrottled"`
	CpuThrottledUsec  uint64 `json:"cpuThrottledUsec"`
	CpuPressureSomeUs uint64 `json:"cpuPressureSomeUsec"`
	CpuPressureFullUs uint64 `json:"cpuPressureFullUsec"`
	IoPressureSomeUs  uint64 `json:"ioPressureSomeUsec"`
	IoPressureFullUs  uint64 `json:"ioPressureFullUsec"`
	MemPressureSomeUs uint64 `json:"memPressureSomeUsec"`
	MemPressureFullUs uint64 `json:"memPressureFullUsec"`
}

// WaitReading is one sensor window of waits as fractions of wall time (0..1, a value
// over 1 on the throttled time means more than one core's worth of refused cycles).
type WaitReading struct {
	WindowStart time.Time    `json:"windowStart"`
	WindowEnd   time.Time    `json:"windowEnd"`
	ReceivedAt  time.Time    `json:"receivedAt"`
	Counters    WaitCounters `json:"counters"`
	// CpuThrottled is the throttled time per second: the cores the quota refused.
	CpuThrottled float64 `json:"cpuThrottled"`
	// CpuThrottledPeriods is the share of scheduler periods that hit the quota.
	CpuThrottledPeriods float64 `json:"cpuThrottledPeriods"`
	CpuPsiSome          float64 `json:"cpuPsiSome"` // time some task was runnable and not running
	CpuPsiFull          float64 `json:"cpuPsiFull"` // time every task was
	IoPsiSome           float64 `json:"ioPsiSome"`  // time some task was stalled on IO
	IoPsiFull           float64 `json:"ioPsiFull"`
	MemPsiSome          float64 `json:"memPsiSome"`
	MemPsiFull          float64 `json:"memPsiFull"`
	Rated               bool    `json:"rated"` // false on the first sample (no previous counters)
}

type waitStore struct {
	mu       sync.RWMutex
	readings map[string]*WaitReading // keyed by server URL
}

func (cluster *Cluster) waitStoreRef() *waitStore {
	cluster.waitOnce.Do(func() {
		cluster.wait = &waitStore{readings: make(map[string]*WaitReading)}
	})
	return cluster.wait
}

func waitDelta(prev, cur uint64) float64 {
	if cur < prev {
		return 0 // counter reset (container recreated): nothing to rate
	}
	return float64(cur - prev)
}

// waitRate fills cur's fractions from the previous sample's counters over the window
// between the two sample ends. Not rated when the window is not strictly positive.
func waitRate(prev *WaitReading, cur *WaitReading) {
	if prev == nil || prev.WindowEnd.IsZero() || !cur.WindowEnd.After(prev.WindowEnd) {
		return
	}
	us := cur.WindowEnd.Sub(prev.WindowEnd).Seconds() * 1e6
	p, c := prev.Counters, cur.Counters
	cur.CpuThrottled = waitDelta(p.CpuThrottledUsec, c.CpuThrottledUsec) / us
	if periods := waitDelta(p.CpuNrPeriods, c.CpuNrPeriods); periods > 0 {
		cur.CpuThrottledPeriods = waitDelta(p.CpuNrThrottled, c.CpuNrThrottled) / periods
	}
	cur.CpuPsiSome = waitDelta(p.CpuPressureSomeUs, c.CpuPressureSomeUs) / us
	cur.CpuPsiFull = waitDelta(p.CpuPressureFullUs, c.CpuPressureFullUs) / us
	cur.IoPsiSome = waitDelta(p.IoPressureSomeUs, c.IoPressureSomeUs) / us
	cur.IoPsiFull = waitDelta(p.IoPressureFullUs, c.IoPressureFullUs) / us
	cur.MemPsiSome = waitDelta(p.MemPressureSomeUs, c.MemPressureSomeUs) / us
	cur.MemPsiFull = waitDelta(p.MemPressureFullUs, c.MemPressureFullUs) / us
	cur.WindowStart = prev.WindowEnd
	cur.Rated = true
}

// IngestWaitCounters records a sensor push of the wait counters for this server, rates
// it against the previous sample and emits the rated reading. The previous sample lives
// in the cluster's store so a ServerMonitor recreation (config reload) does not lose it.
func (server *ServerMonitor) IngestWaitCounters(end time.Time, c WaitCounters) WaitReading {
	cluster := server.ClusterGroup
	if cluster == nil {
		return WaitReading{}
	}
	if end.IsZero() {
		end = time.Now()
	}
	cur := &WaitReading{WindowEnd: end, Counters: c, ReceivedAt: time.Now()}
	st := cluster.waitStoreRef()
	st.mu.Lock()
	waitRate(st.readings[server.URL], cur)
	st.readings[server.URL] = cur
	st.mu.Unlock()
	server.Wait = cur
	if cur.Rated {
		cluster.emitWaitMetrics(server, cur)
	}
	return *cur
}

// GetWaitReading returns this server's latest wait reading, or nil (never pushed).
func (server *ServerMonitor) GetWaitReading() *WaitReading {
	cluster := server.ClusterGroup
	if cluster == nil {
		return nil
	}
	st := cluster.waitStoreRef()
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.readings[server.URL]
}

// emitWaitMetrics writes the rated fractions as dbu.<cluster>.<host>.wait_* next to the
// DBU series of the same server, one point per sensor window, per server (the primary
// and its replica read apart on the graph).
func (cluster *Cluster) emitWaitMetrics(server *ServerMonitor, r *WaitReading) {
	if cluster.ClusterGraphite == nil {
		return
	}
	ts := r.WindowEnd.Unix()
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	base := fmt.Sprintf("dbu.%s.%s.", cluster.Name, server.graphiteHostToken())
	cluster.AddMetrics([]graphite.Metric{
		graphite.NewMetric(base+"wait_cpu_throttled", f(r.CpuThrottled), ts),
		graphite.NewMetric(base+"wait_cpu_throttled_periods", f(r.CpuThrottledPeriods), ts),
		graphite.NewMetric(base+"wait_cpu_psi_some", f(r.CpuPsiSome), ts),
		graphite.NewMetric(base+"wait_cpu_psi_full", f(r.CpuPsiFull), ts),
		graphite.NewMetric(base+"wait_io_psi_some", f(r.IoPsiSome), ts),
		graphite.NewMetric(base+"wait_io_psi_full", f(r.IoPsiFull), ts),
		graphite.NewMetric(base+"wait_mem_psi_some", f(r.MemPsiSome), ts),
		graphite.NewMetric(base+"wait_mem_psi_full", f(r.MemPsiFull), ts),
	})
}
