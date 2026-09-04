// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import "time"

// DBU (Database Unit) locking ratio: one DBU = 1 core / 4 GB RAM / 40 GB disk /
// 1000 IOPS. A workload consumes an integer-ish number of DBU determined by
// whichever axis binds (the max). See CLOUD18_CREDIT_MODEL.md.
const (
	dbuCoresPerUnit  = 1.0
	dbuMemMBPerUnit  = 4096.0
	dbuDiskGBPerUnit = 40.0
	dbuIopsPerUnit   = 1000.0
)

// DBUReading is one period's consumed-DBU picture for a server. The raw per-axis
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

// ComputeDBUFromMaxes turns the four raw per-axis period maxima (as pushed by the
// sensor) into a DBUReading: normalises each axis to DBU, then the pivot is the
// max and Binding is the argmax. Pure function — no I/O, no client cost.
func ComputeDBUFromMaxes(start, end time.Time, memMaxBytes int64, cpuMaxCores, ioMaxIops float64, diskMaxBytes int64) DBUReading {
	r := DBUReading{
		WindowStart:  start,
		WindowEnd:    end,
		MemMaxBytes:  memMaxBytes,
		CpuMaxCores:  cpuMaxCores,
		IoMaxIops:    ioMaxIops,
		DiskMaxBytes: diskMaxBytes,
		DbuMem:       (float64(memMaxBytes) / (1024 * 1024)) / dbuMemMBPerUnit,
		DbuCpu:       cpuMaxCores / dbuCoresPerUnit,
		DbuIo:        ioMaxIops / dbuIopsPerUnit,
		DbuDisk:      (float64(diskMaxBytes) / (1024 * 1024 * 1024)) / dbuDiskGBPerUnit,
	}

	// Pivot = max of the four axes; Binding = the axis that set it. Order is
	// deterministic on ties (cpu, mem, io, disk) so the same input always maps
	// to the same binding.
	r.Dbu, r.Binding = r.DbuCpu, "cpu"
	if r.DbuMem > r.Dbu {
		r.Dbu, r.Binding = r.DbuMem, "mem"
	}
	if r.DbuIo > r.Dbu {
		r.Dbu, r.Binding = r.DbuIo, "io"
	}
	if r.DbuDisk > r.Dbu {
		r.Dbu, r.Binding = r.DbuDisk, "disk"
	}
	return r
}

// SetDBUConsumed stores the latest computed reading on the server. Written by the
// DBU-push API handler (sensor callback), read by the Graphite emission on the
// monitor loop. A single pointer swap keeps the last full period picture.
func (server *ServerMonitor) SetDBUConsumed(r DBUReading) {
	server.DBUConsumed = &r
}
