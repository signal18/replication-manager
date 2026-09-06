// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "time"

// DBUReading is one period's consumed-DBU picture for a server. DBU is one unit
// projection over native resources; the conversion RATIOS (1 DBU = 1 core / 4 GB /
// 40 GB / 1000 IOPS by default) live on the ResourceManager -- the point where
// resources converge -- so ComputeDBU is a method there, not a package function. The raw per-axis
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
	r := cluster.resources.ComputeDBU(start, end, memMaxBytes, cpuMaxCores, ioMaxIops, diskMaxBytes)
	server.SetDBUConsumed(r)
	return r
}

// RestoreDBUConsumed reloads this server's last reading from the repman-side manager
// into the (freshly recreated) ServerMonitor, so a config reload does not blank the
// DBU metric. No entry (never pushed) leaves DBUConsumed nil.
func (server *ServerMonitor) RestoreDBUConsumed() {
	if cluster := server.ClusterGroup; cluster != nil && cluster.resources != nil {
		server.DBUConsumed = cluster.resources.GetConsumed(ResourceKey{Cluster: cluster.Name, Server: server.URL})
	}
}
