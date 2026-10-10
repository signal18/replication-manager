package cluster

import (
	"testing"
	"time"
)

// The om3 pg exposition: one cgroup slice per service path; only the cpu counter and the
// current memory are read, everything else (stats, weights, +Inf limits) is ignored.
func TestParsePgMetrics(t *testing.T) {
	body := []byte(`# HELP opensvc_pg_cgroup_cpu_usage_usec Total CPU usage in microseconds for the cgroup
opensvc_pg_cgroup_cpu_usage_usec{namespace="crm",path="crm/svc/phpmyadmin"} 2.3373401646e+10
opensvc_pg_cgroup_cpu_quota{namespace="crm",path="crm/svc/phpmyadmin"} +Inf
opensvc_pg_cgroup_memory_current_bytes{namespace="crm",path="crm/svc/phpmyadmin"} 8.62830592e+08
opensvc_pg_cgroup_memory_max_bytes{namespace="crm",path="crm/svc/phpmyadmin"} +Inf
opensvc_pg_cgroup_memory_stat_bytes{namespace="crm",path="crm/svc/phpmyadmin",stat="percpu"} 446800
opensvc_pg_cgroup_memory_current_bytes{namespace="crm",path="crm/vol/api-drbd"} 204800
opensvc_pg_cgroup_cpu_usage_usec{namespace="crm",path="crm/svc/api"} 1000000
`)
	now := time.Now()
	pts := parsePgMetrics(body, now)
	p, ok := pts["crm/svc/phpmyadmin"]
	if !ok || p.cpuUsec != 2.3373401646e+10 || p.memBytes != 862830592 || !p.at.Equal(now) {
		t.Fatalf("phpmyadmin = %+v, want cpu 2.3373401646e+10 mem 862830592", p)
	}
	if v := pts["crm/vol/api-drbd"]; v.memBytes != 204800 || v.cpuUsec != 0 {
		t.Fatalf("a volume slice is parsed like any path: %+v", v)
	}
	if a := pts["crm/svc/api"]; a.cpuUsec != 1000000 || a.memBytes != 0 {
		t.Fatalf("api = %+v", a)
	}
	if len(pts) != 3 {
		t.Fatalf("paths = %d, want 3", len(pts))
	}
}

// cores = Δusage_usec / 1e6 / Δseconds; a reset counter or no elapsed time reads 0.
func TestCoresBetween(t *testing.T) {
	t0 := time.Now()
	prev := pgPoint{cpuUsec: 10e6, at: t0}
	cur := pgPoint{cpuUsec: 40e6, at: t0.Add(20 * time.Second)} // 30 cpu-seconds over 20 s
	if c := coresBetween(prev, cur); c < 1.4999 || c > 1.5001 {
		t.Fatalf("cores = %v, want 1.5", c)
	}
	if c := coresBetween(cur, prev); c != 0 {
		t.Fatalf("backwards must read 0, got %v", c)
	}
	if c := coresBetween(prev, pgPoint{cpuUsec: 5e6, at: t0.Add(time.Second)}); c != 0 {
		t.Fatalf("a reset counter must read 0, got %v", c)
	}
	if c := coresBetween(prev, pgPoint{cpuUsec: 50e6, at: t0}); c != 0 {
		t.Fatalf("no elapsed time must read 0, got %v", c)
	}
}

// Two reads of the same daemon snapshot (refreshed every ~15 s) are one sample, not a zero.
func TestSamePgSnapshot(t *testing.T) {
	a := pgPoint{cpuUsec: 10, memBytes: 20, at: time.Now()}
	b := pgPoint{cpuUsec: 10, memBytes: 20, at: a.at.Add(5 * time.Second)}
	if !samePgSnapshot(a, b) {
		t.Fatalf("identical counters at different times are the same snapshot")
	}
	if samePgSnapshot(a, pgPoint{cpuUsec: 11, memBytes: 20, at: b.at}) || samePgSnapshot(a, pgPoint{cpuUsec: 10, memBytes: 21, at: b.at}) {
		t.Fatalf("a moved counter is a new snapshot")
	}
}

// A short agent name takes the domain of the configured opensvc-host; a name with a domain
// or an opensvc-host without one is left as is.
func TestAgentAddress(t *testing.T) {
	if a := agentAddress("s18-fr-4", "s18-fr-6.signal18.io:1215"); a != "s18-fr-4.signal18.io" {
		t.Fatalf("got %q", a)
	}
	if a := agentAddress("s18-fr-4.other.net", "s18-fr-6.signal18.io:1215"); a != "s18-fr-4.other.net" {
		t.Fatalf("got %q", a)
	}
	if a := agentAddress("node1", "10.0.0.6:1215"); a != "node1" {
		t.Fatalf("an IP opensvc-host lends no domain, got %q", a)
	}
	if a := agentAddress("node1", "localhost:1215"); a != "node1" {
		t.Fatalf("got %q", a)
	}
}
