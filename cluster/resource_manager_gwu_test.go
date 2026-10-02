package cluster

import (
	"math"
	"testing"
	"time"
)

// A cumulative family (GWU, #1872): the reading is the month-to-date volume. Two ticks
// must not integrate it as a rate: the month plan is the plan, over/under are measured
// against the reading, and the projection extrapolates the volume linearly.
func TestRecordUsageCumulativeFamily(t *testing.T) {
	m := NewResourceManager()
	m.SetPrices(BillingPrices{GWU: 2, OverPct: 50, UnderPct: 50})
	if err := m.SetBillingDir(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	// 10 days into a 30-day month, 5 GWU used on a plan of 10: projected 15 → 5 over.
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	usage := []UnitUsage{{Family: BillingFamilyGateway, Unit: "GWU", Plan: 10, Billable: 5, Priced: true, UnitPrice: 2, OverPct: 50, UnderPct: 50, Cumulative: true}}
	m.RecordUsage("c1", ClusterIdentity{}, usage, now)
	m.RecordUsage("c1", ClusterIdentity{}, usage, now.Add(time.Minute))
	st, err := m.Statement("2026-04", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	cs := st.Clusters["c1"]
	if cs == nil || len(cs.Units) != 1 {
		t.Fatalf("statement rows: %+v", st)
	}
	r := cs.Units[0]
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.05 }
	if !r.Cumulative || !near(r.MonthPlan, 10) || !near(r.MonthUnderCommit, 5) || !near(r.MonthOverCommit, 0) {
		t.Fatalf("month: plan=%v under=%v over=%v cumulative=%v", r.MonthPlan, r.MonthUnderCommit, r.MonthOverCommit, r.Cumulative)
	}
	if !near(r.ProjectedPlan, 10) || !near(r.ProjectedOverCommit, 5) || !near(r.ProjectedUnderCommit, 0) {
		t.Fatalf("projection: plan=%v over=%v under=%v", r.ProjectedPlan, r.ProjectedOverCommit, r.ProjectedUnderCommit)
	}
	// cost so far: plan 10 × 2 − 5 under × 2 × 50% = 15; projected: 20 + 5 over × 2 × 150% = 35
	if !near(r.MonthCost, 15) || !near(r.ProjectedCost, 35) || !near(cs.Projected, 35) || !near(st.Projected, 35) {
		t.Fatalf("cost: month=%v projected=%v cluster=%v statement=%v", r.MonthCost, r.ProjectedCost, cs.Projected, st.Projected)
	}
}
