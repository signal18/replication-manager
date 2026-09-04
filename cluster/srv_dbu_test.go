// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"
)

func TestComputeDBUFromMaxes_BindingPerAxis(t *testing.T) {
	now := time.Now()
	const GB = 1024 * 1024 * 1024
	const MB = 1024 * 1024

	cases := []struct {
		name         string
		memBytes     int64
		cpuCores     float64
		ioIops       float64
		diskBytes    int64
		wantDbu      float64
		wantBinding  string
	}{
		// Each dimension pinned to exactly 4 DBU in turn -> that axis binds.
		{"cpu binds", 1 * MB, 4, 10, 1 * GB, 4, "cpu"},
		{"mem binds", 16384 * MB, 1, 10, 1 * GB, 4, "mem"},
		{"io binds", 1 * MB, 1, 4000, 1 * GB, 4, "io"},
		{"disk binds", 1 * MB, 1, 10, 160 * GB, 4, "disk"},
		// dev3's real shape: 4 cores, tiny mem/io/disk -> cpu-bound at 4 DBU.
		{"dev3 cpu-bound", 768 * MB, 4, 800, 2 * GB, 4, "cpu"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := ComputeDBUFromMaxes(now, now.Add(time.Minute), c.memBytes, c.cpuCores, c.ioIops, c.diskBytes)
			if r.Binding != c.wantBinding {
				t.Fatalf("binding = %q, want %q (dbu cpu=%.2f mem=%.2f io=%.2f disk=%.2f)",
					r.Binding, c.wantBinding, r.DbuCpu, r.DbuMem, r.DbuIo, r.DbuDisk)
			}
			if r.Dbu < c.wantDbu-0.01 || r.Dbu > c.wantDbu+0.01 {
				t.Fatalf("dbu = %.4f, want %.4f", r.Dbu, c.wantDbu)
			}
		})
	}
}
