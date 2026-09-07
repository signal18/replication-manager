// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "time"

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

// RestoreDBUConsumed reloads this server's last reading from the repman-side manager
// into the (freshly recreated) ServerMonitor, so a config reload does not blank the
// DBU metric. No entry (never pushed) leaves DBUConsumed nil.
func (server *ServerMonitor) RestoreDBUConsumed() {
	if cluster := server.ClusterGroup; cluster != nil && cluster.resources != nil {
		server.DBUConsumed = cluster.resources.GetConsumed(ResourceKey{Cluster: cluster.Name, Server: server.URL})
	}
}
