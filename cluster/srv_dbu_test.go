// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"
)

func TestComputeUsedDBUFromMaxes_BindingPerAxis(t *testing.T) {
	now := time.Now()
	const GB = 1024 * 1024 * 1024
	const MB = 1024 * 1024

	cases := []struct {
		name        string
		memBytes    int64
		cpuCores    float64
		ioIops      float64
		diskBytes   int64
		wantDbu     float64
		wantBinding string
	}{
		// Each dimension pinned to exactly 4 DBU in turn -> that axis binds.
		{"cpu binds", 1 * MB, 4, 10, 1 * GB, 4, "cpu"},
		{"mem binds", 16384 * MB, 1, 10, 1 * GB, 4, "mem"},
		{"io binds", 1 * MB, 1, 4000, 1 * GB, 4, "io"},
		{"disk binds", 1 * MB, 1, 10, 80 * GB, 4, "disk"}, // 20 GB per DBU on the disk axis (2026-09-22)
		// dev3's real shape: 4 cores, tiny mem/io/disk -> cpu-bound at 4 DBU.
		{"dev3 cpu-bound", 768 * MB, 4, 800, 2 * GB, 4, "cpu"},
	}

	rm := NewResourceManager()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := rm.ComputeUsedDBU(now, now.Add(time.Minute), c.memBytes, c.cpuCores, c.ioIops, c.diskBytes)
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

// TestWindowExtremumRequiresCoverage pins the sustain check: the window's max/min over the
// present samples, and no decision when the samples cover less than 80% of the window.
func TestWindowExtremumRequiresCoverage(t *testing.T) {
	window := 5 * time.Minute
	step := int32(60)
	full := []float64{0.2, 0.3, 0.9, 0.25, 0.2} // 5 x 60s = the whole window
	if v, ok := windowExtremum(full, nil, step, window, false); !ok || v != 0.9 {
		t.Fatalf("shrink check must take the busiest sample: got %v ok=%v", v, ok)
	}
	if v, ok := windowExtremum(full, nil, step, window, true); !ok || v != 0.2 {
		t.Fatalf("grow check must take the quietest sample: got %v ok=%v", v, ok)
	}
	// only the last two points present (the partial-bucket case): not sustained
	absent := []bool{true, true, true, false, false}
	if _, ok := windowExtremum(full, absent, step, window, false); ok {
		t.Fatalf("2 of 5 minutes covered must not count as sustained")
	}
	// 4 of 5 minutes present = 80%: enough, and the absent point is skipped in the extremum
	absent = []bool{false, false, true, false, false}
	if v, ok := windowExtremum(full, absent, step, window, false); !ok || v != 0.3 {
		t.Fatalf("80%% coverage must decide on the present samples only: got %v ok=%v", v, ok)
	}
	if _, ok := windowExtremum(nil, nil, step, window, false); ok {
		t.Fatalf("no sample must mean no decision")
	}
	if _, ok := windowExtremum(full, nil, 0, window, false); ok {
		t.Fatalf("no step must mean no decision")
	}
}
