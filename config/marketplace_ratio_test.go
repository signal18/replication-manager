// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestPlanSuffix(t *testing.T) {
	cases := []struct {
		plan string
		want string
	}{
		{"x1.small", "small"},
		{"x1.small.compute", "small.compute"},
		{"x2.middle.perf", "middle.perf"},
		{"x3.huge", "huge"},
		{"x2.tiny", "tiny"},
		{"no-dot-plan", ""},
	}
	for _, tc := range cases {
		t.Run(tc.plan, func(t *testing.T) {
			if got := PlanSuffix(tc.plan); got != tc.want {
				t.Errorf("PlanSuffix(%q) = %q, want %q", tc.plan, got, tc.want)
			}
		})
	}
}

func TestServicePlanDatabaseUnits(t *testing.T) {
	cases := []struct {
		plan    string
		wantDBU int
		wantOK  bool
	}{
		{"x1.tiny", 1, true},
		{"x1.small", 4, true},
		{"x1.small.compute", 4, true},
		{"x1.small.perf", 8, true},
		{"x1.middle", 8, true},
		{"x1.middle.compute", 8, true},
		{"x1.middle.perf", 16, true},
		{"x1.large", 16, true},
		{"x1.large.compute", 16, true},
		{"x1.large.perf", 32, true},
		{"x1.huge", 50, true},
		{"x1.huge.compute", 50, true},
		{"x1.huge.perf", 64, true},
		{"x2.middle.perf", 16, true}, // topology prefix doesn't change the per-node DBU
		{"x1.unknown-size", 0, false},
		{"bogus", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.plan, func(t *testing.T) {
			gotDBU, gotOK := ServicePlanDatabaseUnits(tc.plan)
			if gotOK != tc.wantOK || gotDBU != tc.wantDBU {
				t.Errorf("ServicePlanDatabaseUnits(%q) = (%d, %v), want (%d, %v)", tc.plan, gotDBU, gotOK, tc.wantDBU, tc.wantOK)
			}
		})
	}
}

// x1.small and x1.small.compute intentionally collapse to the same DBU (and therefore
// the same Database shape) in global-unit-pricing — see
// doc/implementation/config/CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md §3.3.
func TestServicePlanDatabaseUnits_LegacyVariantsCollapseToSameDBU(t *testing.T) {
	small, ok1 := ServicePlanDatabaseUnits("x1.small")
	smallCompute, ok2 := ServicePlanDatabaseUnits("x1.small.compute")
	if !ok1 || !ok2 {
		t.Fatalf("expected both plans to resolve, got ok1=%v ok2=%v", ok1, ok2)
	}
	if small != smallCompute {
		t.Errorf("x1.small DBU=%d, x1.small.compute DBU=%d, want equal", small, smallCompute)
	}
}
