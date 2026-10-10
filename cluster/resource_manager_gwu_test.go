package cluster

import (
	"math"
	"testing"
	"time"
)

// The GWU row (#1872): the gateway bandwidth in GWU (a rate, integrated like DBU) against a
// free allowance (Stéphane 2026-10-04): the free units cost nothing, nothing below them is
// credited, the bandwidth held above them is priced at the plain unit price.
func TestRecordUsageGWUFreeAllowance(t *testing.T) {
	m := NewResourceManager()
	m.SetPrices(BillingPrices{GWU: 2, OverPct: 50, UnderPct: 50})
	if err := m.SetBillingDir(t.TempDir(), nil); err != nil {
		t.Fatal(err)
	}
	near := func(a, b float64) bool { return math.Abs(a-b) < 0.05 }
	now := time.Date(2026, 4, 11, 0, 0, 0, 0, time.UTC)
	// 12 GWU held on 10 free: 2 on top, priced 2 × 2 = 4 EUR/month, no credit, no plan cost
	usage := []UnitUsage{{Family: BillingFamilyGateway, Unit: "GWU", Plan: 10, Billable: 12, Priced: true, UnitPrice: 2, OverPct: 50, UnderPct: 50, FreePlan: true}}
	m.RecordUsage("c1", ClusterIdentity{}, usage, now)
	m.RecordUsage("c1", ClusterIdentity{}, usage, now.Add(time.Minute))
	st, err := m.Statement("2026-04", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	r := st.Clusters["c1"].Units[0]
	if !r.FreePlan || !near(r.OverCommit, 2) || !near(r.UnderCommit, 0) || !near(r.PlanCost, 0) || !near(r.UnderCredit, 0) || !near(r.Rate, 4) {
		t.Fatalf("rate row: over=%v under=%v planCost=%v credit=%v rate=%v", r.OverCommit, r.UnderCommit, r.PlanCost, r.UnderCredit, r.Rate)
	}
	if r.ProjectedPlanCost != 0 || r.ProjectedUnderCredit != 0 || r.ProjectedUnderCommit != 0 || !near(r.ProjectedCost, r.ProjectedOverCost) {
		t.Fatalf("projection must carry only the on-top part: %+v", r)
	}
	// below the allowance: nothing on top, nothing credited, nothing to pay, ever
	m2 := NewResourceManager()
	m2.SetPrices(BillingPrices{GWU: 2, OverPct: 50, UnderPct: 50})
	_ = m2.SetBillingDir(t.TempDir(), nil)
	u2 := []UnitUsage{{Family: BillingFamilyGateway, Unit: "GWU", Plan: 10, Billable: 0.5, Priced: true, UnitPrice: 2, OverPct: 50, UnderPct: 50, FreePlan: true}}
	m2.RecordUsage("c2", ClusterIdentity{}, u2, now)
	m2.RecordUsage("c2", ClusterIdentity{}, u2, now.Add(time.Minute))
	st2, _ := m2.Statement("2026-04", now.Add(time.Minute))
	r2 := st2.Clusters["c2"].Units[0]
	if r2.OverCommit != 0 || r2.UnderCommit != 0 || r2.MonthCost != 0 || r2.ProjectedCost != 0 || r2.ProjectedUnderCommit != 0 {
		t.Fatalf("below allowance: over=%v under=%v cost=%v projected=%v projUnder=%v", r2.OverCommit, r2.UnderCommit, r2.MonthCost, r2.ProjectedCost, r2.ProjectedUnderCommit)
	}
}
