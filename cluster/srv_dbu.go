// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
)

// DBUReading is one period's consumed-DBU picture for a server. DBU is one unit
// projection over native resources; the conversion RATIOS (1 DBU = 1 core / 4 GB /
// 40 GB / 1000 IOPS by default) live on the ResourceManager -- the point where
// resources converge -- so ComputeUsedDBU is a method there, not a package function. The raw per-axis
// maxima are measured at the SYSTEM level (cgroup + statfs) by a thin sensor in
// the DB container and pushed here; repman does the DBU semantics (normalisation,
// pivot, binding) so the client's DB CPU is never spent on it. All the "max"
// aggregation is over the [WindowStart, WindowEnd] period, so Dbu is the *peak*
// DBU the workload reached — the size it actually needed, not an average.
type DBUReading struct {
	WindowStart time.Time `json:"windowStart"`
	WindowEnd   time.Time `json:"windowEnd"`

	// Raw per-axis maxima over the period, in native units, as read by the sensor.
	MemMaxBytes  int64   `json:"memMaxBytes"`  // cgroup memory.current peak
	CpuMaxCores  float64 `json:"cpuMaxCores"`  // cpu.stat usage_usec rate peak, in cores
	IoMaxIops    float64 `json:"ioMaxIops"`    // io.stat (rios+wios) rate peak, in iops
	DiskMaxBytes int64   `json:"diskMaxBytes"` // Σ statfs(mounts under datadir).used peak

	// Normalised per-axis DBU (raw / ratio).
	DbuMem  float64 `json:"dbuMem"`
	DbuCpu  float64 `json:"dbuCpu"`
	DbuIo   float64 `json:"dbuIo"`
	DbuDisk float64 `json:"dbuDisk"`

	// Dbu is the pivot: the peak DBU over the period = max of the four axes.
	// Binding is the axis that set it (the biggest contributor / bottleneck):
	// one of "cpu", "mem", "io", "disk".
	Dbu     float64 `json:"dbu"`
	Binding string  `json:"binding"`
}

// SetResourceManager injects the repman-side ResourceManager into this cluster (set once at
// cluster start). nil is tolerated (tests, or before wiring): the reading then only
// lives on the ServerMonitor, as before. See resource_manager.go for why the manager --
// not the ServerMonitor or the Cluster, both recreated on reload -- is the reading's
// durable home (it is what stops the DBU graph flapping).
func (cluster *Cluster) SetResourceManager(m *ResourceManager) { cluster.resources = m }

// SetDBUConsumed records the latest computed reading. Written by the DBU-push API
// handler (sensor callback), read by the Graphite emission on the monitor loop.
// It updates the ServerMonitor field (immediate emission + GUI JSON) AND the
// repman-side manager keyed by cluster/server, so the reading survives the
// ServerMonitor recreation on a config reload (RestoreDBUConsumed reloads it).
func (server *ServerMonitor) SetDBUConsumed(r DBUReading) {
	server.DBUConsumed = &r
	if cluster := server.ClusterGroup; cluster != nil && cluster.resources != nil {
		cluster.resources.SetConsumed(ResourceKey{Cluster: cluster.Name, Server: server.URL}, &r)
	}
}

// IngestDBUMaxes is the single entry point for a sensor push: it converts the raw
// per-axis maxima into a DBUReading using THIS cluster's ResourceManager ratios
// (conversion is owned by the manager, so the API handler stays dumb and just
// forwards raw numbers), stores it, and returns it for logging. No manager wired
// (tests, early startup) -> zero reading, nothing stored.
func (server *ServerMonitor) IngestDBUMaxes(start, end time.Time, memMaxBytes int64, cpuMaxCores, ioMaxIops float64, diskMaxBytes int64) DBUReading {
	cluster := server.ClusterGroup
	if cluster == nil || cluster.resources == nil {
		return DBUReading{}
	}
	r := cluster.resources.ComputeUsedDBU(start, end, memMaxBytes, cpuMaxCores, ioMaxIops, diskMaxBytes)
	server.SetDBUConsumed(r)
	return r
}

// ConsumedDBUForEmit returns the five DBU series values to emit, applying the DBU
// business rule -- this is DBU SEMANTICS only; the raw resource metrics are never
// touched. Emitted every tick, so the series is continuous (no gaps -> no flapping).
//
// The DBU never drops below 1 per axis -- even for a STOPPED service. The DBU drives
// plan-decrease proposals, and we can NEVER free a service's resources below what it
// needs to RESTART: a stopped service must always keep >= 1 DBU reserved, or it could
// fail to come back for lack of resource. (The raw resource series, by contrast, DO
// go to 0 when down -- that is real consumption; see RawResourceForEmit.)
//   - DOWN, or not measured yet -> 1 on every axis (the reserved restart minimum).
//   - UP                         -> max(measured, 1) on every axis.
func (server *ServerMonitor) ConsumedDBUForEmit() (dbu, cpu, mem, io, disk float64) {
	atLeastOne := func(v float64) float64 {
		if v < 1 {
			return 1
		}
		return v
	}
	if server.IsDown() || server.DBUConsumed == nil {
		return 1, 1, 1, 1, 1
	}
	r := server.DBUConsumed
	return atLeastOne(r.Dbu), atLeastOne(r.DbuCpu), atLeastOne(r.DbuMem), atLeastOne(r.DbuIo), atLeastOne(r.DbuDisk)
}

// RawResourceForEmit returns the RAW measured per-axis values to emit (native units:
// cores, mem bytes, iops, disk bytes) -- the real measurement, NOT floored (the DBU
// duplicate carries the min-1 rule).
//
// When the service is DOWN: cpu/mem/io go to 0 (the process is gone, they do not
// persist), but DISK keeps its LAST-KNOWN value -- the image and data volumes are
// still on disk, so the last measurement stays valid until the volume is actually
// deleted (the in-container sensor cannot re-measure a stopped service). Before the
// first measurement everything is 0.
func (server *ServerMonitor) RawResourceForEmit() (cores, memBytes, iops, diskBytes float64) {
	r := server.DBUConsumed
	if server.IsDown() {
		if r != nil {
			return 0, 0, 0, float64(r.DiskMaxBytes)
		}
		return 0, 0, 0, 0
	}
	if r == nil {
		return 0, 0, 0, 0
	}
	return r.CpuMaxCores, float64(r.MemMaxBytes), r.IoMaxIops, float64(r.DiskMaxBytes)
}

// GetProvDbuFromConfigPerNode returns the per-node DBU the cluster's current prov-db-* provisioning
// maps to = max over axes of (prov-db-* / ratio), computed by the ResourceManager -- the
// SINGLE ratio authority (MUST NOT be re-done in the frontend). It is the per-node term of
// the plan materialization (GetProvDbuFromConfigPerNode × node count). Parses the prov-db-* config and
// projects via ComputeUsedDBU (Database ratios); returns ceil(pivot). 0 when no manager is wired.
func (cluster *Cluster) GetProvDbuFromConfigPerNode() int {
	if cluster.resources == nil {
		return 0
	}
	return int(math.Ceil(cluster.GetConfigDBUPerNode().Dbu))
}

// GetConfigDBUPerNode projects the cluster's current prov-db-* provisioning into a per-node
// DBUReading via the ResourceManager ratios -- the PER-AXIS config allocation (DbuCpu/DbuMem/
// DbuIo/DbuDisk) plus the pivot (Dbu = max axis) and its Binding. It is the per-node config
// "capacity" the saturation check reads against (consumed_axis / config_axis). Zero reading
// when no manager is wired.
func (cluster *Cluster) GetConfigDBUPerNode() DBUReading {
	if cluster.resources == nil {
		return DBUReading{}
	}
	cores, _ := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	iops, _ := strconv.ParseFloat(cluster.Conf.ProvIops, 64)
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	diskGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	now := time.Now()
	return cluster.resources.ComputeUsedDBU(now, now, int64(memMB)*1024*1024, cores, iops, int64(diskGB)*1024*1024*1024)
}

// GetPlanDBUPerNode projects the cluster PLAN (the cap -- "le cap est déjà réglé au plan") to a
// per-node per-axis reference reading. The plan is a bundled DBU reservation, so each axis's
// per-node cap is the same GetPlanDbu()/node-count DBU. Used by CheckResourceConsumedOverPlan as
// the reference the consumed DBU is measured against (consumed approaching the plan -> cap up).
// Zero reading when there are no servers.
func (cluster *Cluster) GetPlanDBUPerNode() DBUReading {
	if len(cluster.Servers) == 0 {
		return DBUReading{}
	}
	p := float64(cluster.GetPlanDbu()) / float64(len(cluster.Servers))
	return DBUReading{DbuCpu: p, DbuMem: p, DbuIo: p, DbuDisk: p, Dbu: p}
}

// CheckResourceConsumed is THIS server's checkState: from its own DBUConsumed it sets the four
// consumed-vs-reference axis states (over/under x config/plan). checkState ONLY -- it sets state,
// takes no action (the resize/cap decision is composed downstream). Over = consumed_axis >=
// ref_axis x (1 - prov-db-cap-safety-pct/100); under = consumed_axis <= ref_axis x
// (prov-db-cap-shrink-pct/100); the dead-band between the two is status quo (anti-flap). Config
// ref = this server's own resource allocation (GetConfigDBUPerNode) -> raise/shrink THIS server;
// plan ref = the cap (GetPlanDBUPerNode) -> feeds the cluster cap-up/down composition. All four
// are cleared when the server is down / unmeasured / resource-align is off.
func (server *ServerMonitor) CheckResourceConsumed() {
	server.ResourceConsumedOverConfigAxes = nil
	server.ResourceConsumedUnderConfigAxes = nil
	server.ResourceConsumedOverPlanAxes = nil
	server.ResourceConsumedUnderPlanAxes = nil
	cluster := server.ClusterGroup
	if cluster == nil || cluster.Conf.ProvDBResourceAlign == config.ConstResourceAlignOff {
		return
	}
	if cluster.resources == nil || server.IsDown() || server.DBUConsumed == nil {
		return
	}
	clamp := func(p int) float64 {
		if p < 0 {
			return 0
		}
		if p > 100 {
			return 100
		}
		return float64(p)
	}
	hi := 1 - clamp(cluster.Conf.ProvDBCapSafetyPct)/100.0
	lo := clamp(cluster.Conf.ProvDBCapShrinkPct) / 100.0
	cfg := cluster.GetConfigDBUPerNode()
	plan := cluster.GetPlanDBUPerNode()
	c := server.DBUConsumed
	server.ResourceConsumedOverConfigAxes = dropMem(consumedAxes(c, cfg, hi, true))
	server.ResourceConsumedUnderConfigAxes = dropMem(consumedAxes(c, cfg, lo, false))
	server.ResourceConsumedOverPlanAxes = dropMem(consumedAxes(c, plan, hi, true))
	server.ResourceConsumedUnderPlanAxes = dropMem(consumedAxes(c, plan, lo, false))
}

// dropMem removes the memory axis from a scaling state. dbu_mem is cgroup memory OCCUPANCY,
// and a healthy InnoDB buffer pool is ALWAYS ~full (clean + dirty pages, adaptive hash index,
// change buffer, ...), so occupancy is not a workload-demand signal in EITHER direction: it
// never means "grow" (it is pinned near the cap regardless of load) and never means "shrink"
// (memory is sticky -- the buffer pool does not release on idle). So the autonomous scaling
// states are driven by the DEMAND axes -- cpu (usage), io (saturation), disk (usage) -- not by
// memory occupancy. Real memory NEED surfaces as IO: a too-small buffer pool causes misses
// (Innodb_buffer_pool_reads -> disk reads), which the io axis already sees. Memory SHRINK is
// the deliberate reclaim path (SET GLOBAL buffer pool down), never an occupancy trigger.
// Follow-up: a pressure-based mem grow signal (Innodb_buffer_pool_reads / wait_free / hit-ratio)
// to disambiguate io saturation into "grow iops" vs "grow memory".
func dropMem(axes []string) []string {
	out := make([]string, 0, len(axes))
	for _, a := range axes {
		if a != "mem" {
			out = append(out, a)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// consumedAxes returns the axes (cpu/mem/io/disk, stable order) where consumed_axis / ref_axis
// compares to frac: >= frac when over is true, <= frac when over is false. An axis whose
// reference is 0 is skipped (not provisioned / unknown). nil when none match.
func consumedAxes(c *DBUReading, ref DBUReading, frac float64, over bool) []string {
	set := map[string]bool{}
	for _, a := range []struct {
		name       string
		cons, capa float64
	}{
		{"cpu", c.DbuCpu, ref.DbuCpu}, {"mem", c.DbuMem, ref.DbuMem},
		{"io", c.DbuIo, ref.DbuIo}, {"disk", c.DbuDisk, ref.DbuDisk},
	} {
		if a.capa <= 0 {
			continue
		}
		r := a.cons / a.capa
		if (over && r >= frac) || (!over && r <= frac) {
			set[a.name] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for _, a := range []string{"cpu", "mem", "io", "disk"} {
		if set[a] {
			out = append(out, a)
		}
	}
	return out
}

// GetPlanDbu returns the cluster's EFFECTIVE plan DBU: the explicit
// prov-service-plan-dbu reservation contract when set (> 0), else AUTO-computed =
// per-node derived DBU (GetProvDbuFromConfigPerNode) × the number of DB nodes. "Auto only when
// zero" -- a stored 0 means "let repman compute it". Single source used by the API and
// the graphite emission.
func (cluster *Cluster) GetPlanDbu() int {
	if cluster.Conf.ProvServicePlanDbu > 0 {
		return cluster.Conf.ProvServicePlanDbu
	}
	return cluster.GetProvDbuFromConfigPerNode() * len(cluster.Servers)
}

// GetDBContainerMemoryCapMB returns the cgroup --memory cap (MB) for the DB container.
//
// The cap is aligned to the DBU tier PLUS one overcommit DBU -- the same overcommit slack the
// MariaDB dynamic-resize model uses -- so it sits ABOVE the MySQL config memory. prov-db-memory
// (immutable) keeps driving my.cnf (buffer pool etc.) and is NEVER changed here; only the
// container cap moves. A cap flush against the config memory OOM-kills mariadbd the instant its
// real footprint (connections, temp tables, performance_schema, allocator overhead) exceeds the
// buffer pool -- the db3 crash. The +1 DBU headroom prevents that.
//
// Modes (prov-db-resource-align): "plan" (default) tier = prov-service-plan-dbu / node count;
// "up" tier = max-axis config DBU (coherence/debug); "off" cap = prov-db-memory (legacy).
// The cap never drops below prov-db-memory.
// GetDBTierDbuPerNode is the per-node DBU tier the container cap aligns to, per
// prov-db-resource-align: "plan" (prov-service-plan-dbu / nodes), "up" (max-axis config DBU).
// Returns 0 when alignment is off or there is no RM/servers yet (caller falls back to legacy).
func (cluster *Cluster) GetDBTierDbuPerNode() float64 {
	mode := cluster.Conf.ProvDBResourceAlign
	if mode == "" {
		mode = config.ConstResourceAlignPlan
	}
	if mode == config.ConstResourceAlignOff || cluster.resources == nil || len(cluster.Servers) == 0 {
		return 0
	}
	var tier float64
	if mode == config.ConstResourceAlignUp {
		tier = float64(cluster.GetProvDbuFromConfigPerNode())
	} else {
		tier = float64(cluster.GetPlanDbu()) / float64(len(cluster.Servers))
	}
	if tier < 1 {
		tier = 1
	}
	return tier
}

// GetDBContainerMemoryCapMB returns the cgroup --memory cap (MB) for the DB container:
// (tier + cap-burst) × mem-ratio, deliberately ABOVE prov-db-memory (the my.cnf sizing, never
// changed) so mariadbd has headroom and is not OOM-killed. Falls back to prov-db-memory when
// alignment is off. Never below prov-db-memory.
func (cluster *Cluster) GetDBContainerMemoryCapMB() int {
	provMemMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	tier := cluster.GetDBTierDbuPerNode()
	if tier <= 0 {
		return int(provMemMB)
	}
	capMB := int(math.Ceil(tier * cluster.resources.DBMemMBPerUnit()))
	if capMB < int(provMemMB) {
		capMB = int(provMemMB)
	}
	return capMB
}

// RestoreDBUConsumed reloads this server's last reading from the repman-side manager
// into the (freshly recreated) ServerMonitor, so a config reload does not blank the
// DBU metric. No entry (never pushed) leaves DBUConsumed nil.
func (server *ServerMonitor) RestoreDBUConsumed() {
	if cluster := server.ClusterGroup; cluster != nil && cluster.resources != nil {
		server.DBUConsumed = cluster.resources.GetConsumed(ResourceKey{Cluster: cluster.Name, Server: server.URL})
	}
}

func clampPct(p int) float64 {
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return float64(p)
}

// axisConfigDBU returns the per-node config DBU of one axis from a reading.
func axisConfigDBU(r DBUReading, axis string) float64 {
	switch axis {
	case "cpu":
		return r.DbuCpu
	case "mem":
		return r.DbuMem
	case "io":
		return r.DbuIo
	case "disk":
		return r.DbuDisk
	}
	return 0
}

// lastRenderValue returns the last non-absent point of a graphite render result.
func lastRenderValue(vals []float64, absent []bool) (float64, bool) {
	for i := len(vals) - 1; i >= 0; i-- {
		if i < len(absent) && absent[i] {
			continue
		}
		return vals[i], true
	}
	return 0, false
}

// graphiteHostToken is this server's token in the mysql.<host>.* graphite series (same
// replacer as srv_snd.go's emission).
func (server *ServerMonitor) graphiteHostToken() string {
	replacer := strings.NewReplacer("`", "", "?", "", " ", "_", ".", "-", "(", "-", ")", "-", "/", "_", "<", "-", "'", "-", "\"", "-")
	return replacer.Replace(server.Variables.Get("HOSTNAME"))
}

// canScaleSustained is the shared SPEED gate for the scale-due decisions: given the instant
// over/under axes, the client-set speed, and the per-node reference (config or plan), it returns
// the axes for which the saturation has PERSISTED long enough to act. At the fastest speed (<= one
// sensor tick, the 1m default) the instant state is the decision -- no history. For a slower speed
// it asks Graphite whether the consumed axis stayed over/under its threshold for the WHOLE window
// (summarize min for grow / max for shrink); a Graphite hiccup falls back to the instant state.
// DECISION only -- never resizes (the resize is composed downstream, gated by CanConfigResize).
func (server *ServerMonitor) canScaleSustained(up bool, instant []string, speedStr string, ref DBUReading) []string {
	cluster := server.ClusterGroup
	if cluster == nil || len(instant) == 0 {
		return nil // no cluster, or not even instantaneously over/under -> nothing to sustain
	}
	d, err := time.ParseDuration(speedStr)
	if err != nil || d <= time.Minute {
		return instant // fast path: 1 tick / <= 1m -> the instant state IS the decision
	}
	overThrFactor := 1 - clampPct(cluster.Conf.ProvDBCapSafetyPct)/100.0
	underThrFactor := clampPct(cluster.Conf.ProvDBCapShrinkPct) / 100.0
	host := server.graphiteHostToken()
	until := int32(time.Now().Unix())
	from := until - int32(d.Seconds()) - 60
	agg := "max" // shrink: even the busiest sample of the window must be under
	if up {
		agg = "min" // grow: even the quietest sample of the window must be over
	}
	var due []string
	for _, axis := range instant {
		capa := axisConfigDBU(ref, axis)
		if capa <= 0 {
			continue
		}
		target := fmt.Sprintf("summarize(mysql.%s.dbu_%s,'%ds','%s')", host, axis, int(d.Seconds()), agg)
		md, rerr := graphite.Zipper.Render(target, from, until)
		if rerr != nil {
			due = append(due, axis) // Graphite unavailable -> trust the instant state
			continue
		}
		v, ok := lastRenderValue(md.Values, md.IsAbsent)
		if !ok {
			due = append(due, axis)
			continue
		}
		if (up && v >= capa*overThrFactor) || (!up && v <= capa*underThrFactor) {
			due = append(due, axis)
		}
	}
	return due
}

// CanScaleConfigInPlan reports the axes for which a config resource scale is DUE (up = grow on
// saturation, down = shrink on under-use), reference = the per-server CONFIG, speed =
// ScaleUp/DownConfigInPlanSpeed. In-plan, so cheap/reactive (fast default).
func (server *ServerMonitor) CanScaleConfigInPlan(up bool) []string {
	cluster := server.ClusterGroup
	if cluster == nil {
		return nil
	}
	if up {
		return server.canScaleSustained(true, server.ResourceConsumedOverConfigAxes, cluster.Conf.ScaleUpConfigInPlanSpeed, cluster.GetConfigDBUPerNode())
	}
	return server.canScaleSustained(false, server.ResourceConsumedUnderConfigAxes, cluster.Conf.ScaleDownConfigInPlanSpeed, cluster.GetConfigDBUPerNode())
}

// CanScalePlan reports the axes for which a PLAN scale is DUE (up = cap up, down = cap down),
// reference = the PLAN (the cap), speed = ScaleUp/DownPlanSpeed. Commercial, so slower/more
// conservative than in-plan. Per server; the cluster composes cap up/down from these (one server
// suffices to force cap-up; every server must agree for cap-down).
func (server *ServerMonitor) CanScalePlan(up bool) []string {
	cluster := server.ClusterGroup
	if cluster == nil {
		return nil
	}
	if up {
		return server.canScaleSustained(true, server.ResourceConsumedOverPlanAxes, cluster.Conf.ScaleUpPlanSpeed, cluster.GetPlanDBUPerNode())
	}
	return server.canScaleSustained(false, server.ResourceConsumedUnderPlanAxes, cluster.Conf.ScaleDownPlanSpeed, cluster.GetPlanDBUPerNode())
}
