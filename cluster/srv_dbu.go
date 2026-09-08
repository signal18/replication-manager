// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"math"
	"strconv"
	"time"

	"github.com/signal18/replication-manager/config"
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
	cores, _ := strconv.ParseFloat(cluster.Conf.ProvCores, 64)
	iops, _ := strconv.ParseFloat(cluster.Conf.ProvIops, 64)
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	diskGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	now := time.Now()
	r := cluster.resources.ComputeUsedDBU(now, now, int64(memMB)*1024*1024, cores, iops, int64(diskGB)*1024*1024*1024)
	return int(math.Ceil(r.Dbu))
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
func (cluster *Cluster) GetDBContainerMemoryCapMB() int {
	provMemMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	mode := cluster.Conf.ProvDBResourceAlign
	if mode == "" {
		mode = config.ConstResourceAlignPlan
	}
	if mode == config.ConstResourceAlignOff || cluster.resources == nil || len(cluster.Servers) == 0 {
		return int(provMemMB)
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
	// Overcommit DBU (prov-db-overcommit-dbu, default 1) added above the reservation tier --
	// a cap-only headroom kept in its OWN variable; the plan is never modified here.
	overcommit := float64(cluster.Conf.ProvDBOvercommitDbu)
	if overcommit < 0 {
		overcommit = 0
	}
	capMB := int(math.Ceil((tier + overcommit) * cluster.resources.DBMemMBPerUnit()))
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
