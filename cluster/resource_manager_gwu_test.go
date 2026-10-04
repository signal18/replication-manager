package cluster

import (
	"math"
	"testing"
	"time"
)

// The GWU volume row (#1872): a cumulative family with a free allowance. Rules (Stéphane
// 2026-10-04): the free units cost nothing, nothing below them is credited, the part on top
// is priced at the plain unit price, the month-to-date volume is recorded as is and
// projected linearly.
func TestRecordUsageGWUFreeAllowance(t *testing.T) {
	m := NewResourceManager()
	m.SetPrices(BillingPrices{GWU: 2, OverPct: 50, UnderPct: 50})
	if err := m.SetBillingDir(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.05 }
	// 10 days into a 30-day month, 15 GWU of traffic on 10 free: 5 on top now, 35 projected.
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	usage := []UnitUsage{{Family: BillingFamilyGateway, Unit: "GWU", Plan: 10, Billable: 15, Priced: true, UnitPrice: 2, OverPct: 50, UnderPct: 50, Cumulative: true, FreePlan: true}}
	m.RecordUsage("c1", ClusterIdentity{}, usage, now)
	m.RecordUsage("c1", ClusterIdentity{}, usage, now.Add(time.Minute))
	st, err := m.Statement("2026-04", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r := st.Clusters["c1"].Units[0]
	if !r.Cumulative || !r.FreePlan || !near(r.MonthPlan, 10) || !near(r.MonthOverCommit, 5) || !near(r.MonthUnderCommit, 0) {
		t.Fatalf("month: plan=%v over=%v under=%v flags=%v/%v", r.MonthPlan, r.MonthOverCommit, r.MonthUnderCommit, r.Cumulative, r.FreePlan)
	}
	// cost: free plan 0, 5 on top × 2 = 10, no surcharge; projected 45 → 35 on top × 2 = 70
	if !near(r.MonthPlanCost, 0) || !near(r.MonthUnderCredit, 0) || !near(r.MonthCost, 10) || !near(r.ProjectedOverCommit, 35) || !near(r.ProjectedCost, 70) || !near(st.Projected, 70) {
		t.Fatalf("cost: planCost=%v underCredit=%v month=%v projOver=%v projCost=%v stmt=%v", r.MonthPlanCost, r.MonthUnderCredit, r.MonthCost, r.ProjectedOverCommit, r.ProjectedCost, st.Projected)
	}
	// below the allowance: nothing on top, nothing credited, nothing to pay
	m2 := NewResourceManager()
	m2.SetPrices(BillingPrices{GWU: 2, OverPct: 50, UnderPct: 50})
	_ = m2.SetBillingDir(t.TempDir(), nil)
	m2.RecordUsage("c2", ClusterIdentity{}, []UnitUsage{{Family: BillingFamilyGateway, Unit: "GWU", Plan: 10, Billable: 4, Priced: true, UnitPrice: 2, OverPct: 50, UnderPct: 50, Cumulative: true, FreePlan: true}}, now)
	st2, _ := m2.Statement("2026-04", now)
	r2 := st2.Clusters["c2"].Units[0]
	if !near(r2.MonthOverCommit, 0) || !near(r2.MonthUnderCommit, 0) || !near(r2.MonthCost, 0) || !near(r2.ProjectedCost, 2*2) {
		t.Fatalf("below allowance: over=%v under=%v cost=%v projected=%v (4 GWU in 10 days → 12 projected → 2 on top × 2)", r2.MonthOverCommit, r2.MonthUnderCommit, r2.MonthCost, r2.ProjectedCost)
	}
}
