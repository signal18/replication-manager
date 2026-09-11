// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// Compute (APU) graphite emission -- the stateless-unit twin of the DB DBU block in
// srv_snd.go GetDatabaseMetrics. DBU rides graphite as mysql.<host>.dbu*; APU rides it
// the SAME way as apu.<name>.apu*, so the GUI renders the Compute graph through the
// identical graphite-render path (no bespoke endpoint, no new grouped component).
// Proxies and apps are the two Compute kinds; both read their APUReading from the
// ResourceManager (survives ServerMonitor/Cluster recreation) and emit every tick.

// computeTokenReplacer sanitises a unit name into a graphite path token, matching the
// DB hostname sanitiser in GetDatabaseMetrics so the two metric trees are consistent.
var computeTokenReplacer = strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-", ":", "-")

// consumedAPUForEmit mirrors ConsumedDBUForEmit on the Compute track: floor each axis at
// 1 (the reserved restart minimum) so a unit that is down or has not yet pushed still
// yields a continuous, gap-free series (no flapping). No io axis -- Compute has no IOPS
// lock. nil reading or a down unit -> all 1s.
func consumedAPUForEmit(r *APUReading, down bool) (apu, cpu, mem, disk float64) {
	// Compute (APU) emits the REAL projected value -- NOT floored to 1 like the DBU billing
	// duplicate. A proxy is a lightweight router (tens of MB, well under one core): flooring
	// hid its true usage and made the graph read a flat "1". A down/unmeasured unit reports 0
	// -- still a value, so the series stays gap-free (no flapping), just honest.
	if down || r == nil {
		return 0, 0, 0, 0
	}
	return r.Apu, r.ApuCpu, r.ApuMem, r.ApuDisk
}

// rawResourceAPUForEmit is the native measurement (NOT floored), 0 when down -- the same
// "service" unit the resource model reasons in, mirroring RawResourceForEmit for DBs.
func rawResourceAPUForEmit(r *APUReading, down bool) (cores, memBytes, diskBytes float64) {
	if down || r == nil {
		return 0, 0, 0
	}
	return r.CpuMaxCores, float64(r.MemMaxBytes), float64(r.DiskMaxBytes)
}

// computeUnitMetrics builds the graphite series for ONE Compute unit (proxy or app),
// under apu.<name>.*, mirroring the mysql.<host>.dbu* block. ts is shared across the
// batch so the axes line up.
func (cluster *Cluster) computeUnitMetrics(name string, r *APUReading, down bool, ts int64) []graphite.Metric {
	// Cluster-scoped path apu.<cluster>.<name>.* so a multi-cluster repman never MIXES clusters.
	// DELIBERATE DEPARTURE from the DBU convention (cluster embedded IN the service/host id,
	// mysql.<HOST>): here the cluster is its OWN leading segment and uses the RAW cluster.Name --
	// the GUI scope() rewrites apu.* -> apu.<cluster.Name>.* with the same raw name, so both sides
	// splice the identical string and always match (no Go/JS sanitiser, robust even to a dot in
	// the name). Only the unit segment is sanitised (it is matched by the scope wildcard). Applied
	// to APU only for now; migrating mysql.* to this shape is a separate change.
	token := cluster.Name + "." + computeTokenReplacer.Replace(name)
	apu, cpu, mem, disk := consumedAPUForEmit(r, down)
	cores, memBytes, diskBytes := rawResourceAPUForEmit(r, down)

	f := func(v float64, prec int) string { return strconv.FormatFloat(v, 'f', prec, 64) }
	return []graphite.Metric{
		// APU duplicate (>= 1 per axis, gap-free): apu is the pivot, apu_* the axes.
		graphite.NewMetric(fmt.Sprintf("apu.%s.apu", token), f(apu, 4), ts),
		graphite.NewMetric(fmt.Sprintf("apu.%s.apu_cpu", token), f(cpu, 4), ts),
		graphite.NewMetric(fmt.Sprintf("apu.%s.apu_mem", token), f(mem, 4), ts),
		graphite.NewMetric(fmt.Sprintf("apu.%s.apu_disk", token), f(disk, 4), ts),
		// Raw resource values (native units), the real measurement, 0 when down.
		graphite.NewMetric(fmt.Sprintf("apu.%s.service_cpu", token), f(cores, 4), ts),
		graphite.NewMetric(fmt.Sprintf("apu.%s.service_mem", token), f(memBytes, 0), ts),
		graphite.NewMetric(fmt.Sprintf("apu.%s.service_disk", token), f(diskBytes, 0), ts),
	}
}

// CollectComputeMetrics queues the APU series for every Compute unit (proxies + apps)
// into the graphite batch, once per graphite tick. The twin of the per-server
// FetchDatabaseStats path, but stateless units have no ServerMonitor, so the cluster
// drives them directly. No-op if the ResourceManager is not wired.
func (cluster *Cluster) CollectComputeMetrics() {
	if cluster.resources == nil {
		return
	}
	ts := time.Now().Unix()
	var metrics []graphite.Metric

	for _, prx := range cluster.Proxies {
		if prx == nil {
			continue
		}
		k := AppKey{Cluster: cluster.Name, App: prx.GetName(), Kind: KindProxy}
		metrics = append(metrics, cluster.computeUnitMetrics(prx.GetName(), cluster.resources.GetAppConsumed(k), prx.IsDown(), ts)...)
	}
	for _, app := range cluster.Apps {
		if app == nil {
			continue
		}
		k := AppKey{Cluster: cluster.Name, App: app.Name, Kind: KindApp}
		metrics = append(metrics, cluster.computeUnitMetrics(app.Name, cluster.resources.GetAppConsumed(k), app.IsDown(), ts)...)
	}

	// Cluster-level APU PLAN contract, the Compute mirror of resourcemanager.<C>.plan_dbu:
	// the contract is the ROLLUP of the per-instance reservations (AppPlanByCluster = Σ proxies
	// at prov-proxy-apu + Σ apps at their own config), emitted once per cluster so the fleet view
	// is sumSeries(resourcemanager.*.plan_apu) and overcommit is DERIVED at query time (plan vs
	// sumSeries(apu.*.apu)) -- no server-side aggregation. Uppercased token, scoped like the DBU
	// plan + apu.<name> series.
	ctoken := strings.ToUpper(computeTokenReplacer.Replace(cluster.Name))
	planAPU := 0.0
	if cluster.resources != nil {
		planAPU = cluster.resources.AppPlanByCluster(cluster.Name).Apu
	}
	metrics = append(metrics, graphite.NewMetric(
		fmt.Sprintf("resourcemanager.%s.plan_apu", ctoken),
		strconv.FormatFloat(planAPU, 'f', 4, 64), ts))

	if len(metrics) > 0 {
		cluster.AddMetrics(metrics)
	}
}
