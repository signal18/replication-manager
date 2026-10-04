// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// Internal network monitoring (Stéphane, 2026-10-04): what every unit of the cluster --
// each database, proxy and app -- moves on its own network interface, in Mb/s, both
// directions. It is MONITORING ONLY: no unit, no price, no plan, no alert. The gateway
// bandwidth (GWU, cluster_gwu.go) is the cluster seen from the uplink; this is the same
// cluster seen from inside, unit by unit, so the operator can tell which unit holds the
// bandwidth before any throttling decision (#1880).
//
// Transparent across orchestrators, the same way the DBU sensor is: the counters are the
// cumulative octets of /proc/<pid>/net/dev read by the SAME jobs script that already
// pushes the cgroup maxima (dbjobs_new.sh for a database, app_job.sh for a proxy or an
// app). On OpenSVC the jobs container shares the pod network namespace, so the counters
// are the pod's eth0; on premise the job runs on the host, so they are the host NICs, i.e.
// what that database host moves, replication and backups included. The script ships the
// RAW cumulative counters in the existing /dbu and /apu pushes (two optional fields), and
// repman computes the rate here: a restart of the script, a reset of the counters or a
// missed push can never produce a negative or inflated rate.
//
// When no script reports (an on-premise proxy has no jobs script), the proxy's own
// counters stand in -- HAProxy bin/bout, ProxySQL BYTES_RECEIVED/BYTES_SENT -- flagged
// Source=status so the GUI never passes a SQL-only figure for the pod's traffic. A
// fresh pod reading always wins over the status fallback.

// NetUnitKind names the kind of unit a network reading belongs to.
type NetUnitKind string

const (
	NetUnitDatabase NetUnitKind = "database"
	NetUnitProxy    NetUnitKind = "proxy"
	NetUnitApp      NetUnitKind = "app"

	// NetSourcePod: counters read in the unit's own network namespace by the jobs
	// script (everything the pod moves). NetSourceStatus: the proxy's own byte counters
	// (SQL traffic only), used when no script reports.
	NetSourcePod    = "pod"
	NetSourceStatus = "status"
)

// NetReading is the latest network reading of one unit.
type NetReading struct {
	Kind        NetUnitKind `json:"kind"`
	Name        string      `json:"name"`
	WindowStart time.Time   `json:"windowStart"`
	WindowEnd   time.Time   `json:"windowEnd"`
	RxBytes     uint64      `json:"rxBytes"` // cumulative octets received, as reported
	TxBytes     uint64      `json:"txBytes"` // cumulative octets sent, as reported
	RxMbps      float64     `json:"rxMbps"`  // Mb/s received over the last window
	TxMbps      float64     `json:"txMbps"`  // Mb/s sent over the last window
	Mbps        float64     `json:"mbps"`    // RxMbps + TxMbps: both directions, like GWU
	Source      string      `json:"source"`  // pod | status
	ReceivedAt  time.Time   `json:"receivedAt"`
	// Rated reports whether this reading carries a rate: the first push of a unit only
	// seeds the counters (no previous sample to derive a rate from).
	Rated bool `json:"rated"`
}

// netStore holds the latest reading per unit for one cluster. It lives on the cluster, not
// on the ServerMonitor/Proxy/App, so a config reload (which recreates those) does not wipe
// the counters and restart every rate from zero (the DBUConsumed reload-wipe lesson).
type netStore struct {
	mu       sync.RWMutex
	readings map[string]*NetReading
}

func netKey(kind NetUnitKind, name string) string { return string(kind) + "/" + name }

// netCounterDelta returns cur-prev, or 0 when the counter went backwards (a reset of the
// interface, of the pod or of the host). Same contract as the gateway delta.
func netCounterDelta(prev, cur uint64) uint64 {
	if cur < prev {
		return 0
	}
	return cur - prev
}

// netMbps converts a byte delta over a window into Mb/s (bits, decimal mega: the unit every
// network operator quotes, never bytes).
func netMbps(deltaBytes uint64, window time.Duration) float64 {
	if window <= 0 {
		return 0
	}
	return float64(deltaBytes) * 8 / window.Seconds() / 1e6
}

// netRate derives the rate of a new cumulative sample against the previous reading. The
// window is the time between the two samples' ends: the two scripts push on their own
// cadence (~60 s), and a missed push just widens the window, it does not inflate the rate.
func netRate(prev *NetReading, cur *NetReading) {
	if prev == nil || prev.WindowEnd.IsZero() || !cur.WindowEnd.After(prev.WindowEnd) {
		return
	}
	window := cur.WindowEnd.Sub(prev.WindowEnd)
	cur.RxMbps = netMbps(netCounterDelta(prev.RxBytes, cur.RxBytes), window)
	cur.TxMbps = netMbps(netCounterDelta(prev.TxBytes, cur.TxBytes), window)
	cur.Mbps = cur.RxMbps + cur.TxMbps
	cur.WindowStart = prev.WindowEnd
	cur.Rated = true
}

func (cluster *Cluster) netStoreRef() *netStore {
	cluster.netOnce.Do(func() {
		cluster.net = &netStore{readings: make(map[string]*NetReading)}
	})
	return cluster.net
}

// IngestNetCounters records a cumulative rx/tx sample pushed by a unit's jobs script
// (Source=pod) and returns the reading, rated against the previous sample when there is one.
func (cluster *Cluster) IngestNetCounters(kind NetUnitKind, name string, end time.Time, rxBytes, txBytes uint64) NetReading {
	return cluster.ingestNet(kind, name, end, rxBytes, txBytes, NetSourcePod)
}

// IngestNetStatusCounters records the fallback sample taken from the unit's own counters
// (Source=status). It is ignored while a fresh pod reading exists for the unit, so the
// script always wins when both report.
func (cluster *Cluster) IngestNetStatusCounters(kind NetUnitKind, name string, end time.Time, rxBytes, txBytes uint64) (NetReading, bool) {
	if cur := cluster.GetNetReading(kind, name); cur != nil && cur.Source == NetSourcePod && end.Sub(cur.ReceivedAt) < resourceSensorFreshnessWindow {
		return *cur, false
	}
	return cluster.ingestNet(kind, name, end, rxBytes, txBytes, NetSourceStatus), true
}

func (cluster *Cluster) ingestNet(kind NetUnitKind, name string, end time.Time, rxBytes, txBytes uint64, source string) NetReading {
	if end.IsZero() {
		end = time.Now()
	}
	cur := &NetReading{Kind: kind, Name: name, WindowEnd: end, RxBytes: rxBytes, TxBytes: txBytes, Source: source, ReceivedAt: time.Now()}
	st := cluster.netStoreRef()
	st.mu.Lock()
	prev := st.readings[netKey(kind, name)]
	// A source change (status -> pod or back) restarts the counters: the two count
	// different things, their delta means nothing.
	if prev != nil && prev.Source == source {
		netRate(prev, cur)
	}
	st.readings[netKey(kind, name)] = cur
	st.mu.Unlock()
	if cur.Rated {
		cluster.emitNetMetrics(cur)
	}
	return *cur
}

// GetNetReading returns the latest reading of a unit, or nil.
func (cluster *Cluster) GetNetReading(kind NetUnitKind, name string) *NetReading {
	st := cluster.netStoreRef()
	st.mu.RLock()
	defer st.mu.RUnlock()
	if r, ok := st.readings[netKey(kind, name)]; ok {
		c := *r
		return &c
	}
	return nil
}

// NetReadings returns a copy of every unit's latest reading (GUI/API view).
func (cluster *Cluster) NetReadings() []NetReading {
	st := cluster.netStoreRef()
	st.mu.RLock()
	defer st.mu.RUnlock()
	out := make([]NetReading, 0, len(st.readings))
	for _, r := range st.readings {
		out = append(out, *r)
	}
	return out
}

// DropNetReading forgets a unit (dropped proxy/app, removed server).
func (cluster *Cluster) DropNetReading(kind NetUnitKind, name string) {
	st := cluster.netStoreRef()
	st.mu.Lock()
	delete(st.readings, netKey(kind, name))
	st.mu.Unlock()
}

// NetClusterMbps sums the fresh rated readings of every unit: the cluster's internal
// traffic, both directions, in Mb/s. Stale units (no push within the sensor freshness
// window) are left out rather than frozen at their last value.
func (cluster *Cluster) NetClusterMbps(now time.Time) float64 {
	st := cluster.netStoreRef()
	st.mu.RLock()
	defer st.mu.RUnlock()
	total := 0.0
	for _, r := range st.readings {
		if r.Rated && now.Sub(r.ReceivedAt) < resourceSensorFreshnessWindow {
			total += r.Mbps
		}
	}
	return total
}

// emitNetMetrics writes the unit series and the cluster total. Path shape follows the APU
// convention: net.<cluster>.<kind>.<unit>.* with the RAW cluster name (the GUI scope()
// splices the same string) and a sanitised unit segment.
func (cluster *Cluster) emitNetMetrics(r *NetReading) {
	if cluster.ClusterGraphite == nil {
		return // no sink yet (startup, tests)
	}
	ts := r.WindowEnd.Unix()
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	unit := fmt.Sprintf("net.%s.%s.%s", cluster.Name, r.Kind, computeTokenReplacer.Replace(r.Name))
	cluster.AddMetrics([]graphite.Metric{
		graphite.NewMetric(unit+".rx_mbps", f(r.RxMbps), ts),
		graphite.NewMetric(unit+".tx_mbps", f(r.TxMbps), ts),
		graphite.NewMetric(unit+".mbps", f(r.Mbps), ts),
		graphite.NewMetric(fmt.Sprintf("net.%s.mbps", cluster.Name), f(cluster.NetClusterMbps(r.ReceivedAt)), ts),
	})
}

// ingestNetFromBackends feeds the status fallback from the proxy's backend rows: the
// cumulative bytes in/out of every backend server the proxy reports (HAProxy stat bin/bout,
// ProxySQL BYTES_RECEIVED/BYTES_SENT). Counters are strings on the Backend view; anything
// unparsable counts as 0.
func (proxy *Proxy) ingestNetFromBackends() {
	cluster := proxy.ClusterGroup
	if cluster == nil || proxy == nil {
		return
	}
	var rx, tx uint64
	for _, b := range append(append([]Backend{}, proxy.BackendsWrite...), proxy.BackendsRead...) {
		in, _ := strconv.ParseUint(strings.TrimSpace(b.PrxByteIn), 10, 64)
		out, _ := strconv.ParseUint(strings.TrimSpace(b.PrxByteOut), 10, 64)
		rx += in
		tx += out
	}
	if rx == 0 && tx == 0 {
		return
	}
	cluster.IngestNetStatusCounters(NetUnitProxy, proxy.GetName(), time.Now(), rx, tx)
}
