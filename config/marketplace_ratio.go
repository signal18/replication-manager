// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "strings"

// Database Unit ratio, authoritative under cloud18-marketplace-pricing-mode ==
// global-unit-pricing: 1 core / 4GB RAM / 40GB disk / 1000 IOPS.
// See doc/implementation/config/CLOUD18_CREDIT_MODEL.md §2 and
// doc/implementation/config/CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md §3.1.
const (
	DBUnitCpuCores = 1    // 1 core per Database Unit
	DBUnitMemMB    = 4096 // 4 GB per Database Unit (in MB)
	DBUnitDiskGB   = 40   // 40 GB per Database Unit
	DBUnitIops     = 1000 // 1000 IOPS per Database Unit
)

// servicePlanDBUBySuffix is the plan-suffix -> per-node Database Unit mapping from
// CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md §3.3. It is the authoritative DB shape
// for a service plan when global-unit-pricing is active, replacing the (potentially
// off-ratio) raw CSV DB resource columns.
var servicePlanDBUBySuffix = map[string]int{
	"tiny":           1,
	"small":          4,
	"small.compute":  4,
	"small.perf":     8,
	"middle":         8,
	"middle.compute": 8,
	"middle.perf":    16,
	"large":          16,
	"large.compute":  16,
	"large.perf":     32,
	"huge":           50,
	"huge.compute":   50,
	"huge.perf":      64,
}

// PlanSuffix returns the workload-size portion of a service plan name, stripping the
// leading topology prefix (e.g. "x2." in "x2.middle.perf" -> "middle.perf"). Returns ""
// if planName has no "." separator.
func PlanSuffix(planName string) string {
	idx := strings.Index(planName, ".")
	if idx == -1 {
		return ""
	}
	return planName[idx+1:]
}

// ServicePlanDatabaseUnits looks up the per-node Database Unit count for a service plan
// name under global-unit-pricing. ok is false when the plan's suffix has no appendix
// mapping, in which case callers must fail explicitly rather than fall back to legacy
// CSV DB resource columns.
func ServicePlanDatabaseUnits(planName string) (dbu int, ok bool) {
	dbu, ok = servicePlanDBUBySuffix[PlanSuffix(planName)]
	return dbu, ok
}
