package cluster

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestUnitRate(t *testing.T) {
	if r := unitRate(4, 4, 10, 50, 50); !near(r, 40) {
		t.Fatalf("at the plan 4 × 10 = 40, got %v", r)
	}
	if r := unitRate(4, 6, 10, 50, 50); !near(r, 40+2*10*1.5) {
		t.Fatalf("2 over at 150%%: 70, got %v", r)
	}
	if r := unitRate(4, 3, 10, 50, 50); !near(r, 30+1*10*0.5) {
		t.Fatalf("1 under refunded at 50%%: 35, got %v", r)
	}
	if r := unitRate(4, 4, 0, 50, 50); r != 0 {
		t.Fatalf("no price no rate: %v", r)
	}
}

// The month statement is the integral of the per-tick rate: two ticks of 60 s at a rate of
// 40 EUR/month accrue 40 × 120 / monthSeconds; identity captured; totals and projection;
// the file is written and reloads with the accruals; the month change closes it as final.
func TestResourceManagerBillingAccrualAndStatement(t *testing.T) {
	dir := t.TempDir()
	m := NewResourceManager()
	m.SetPrices(BillingPrices{DBU: 10, APU: 5, BKU: 1, BAU: 2, OverPct: 50, UnderPct: 50})
	if err := m.SetBillingDir(dir, nil); err != nil {
		t.Fatal(err)
	}
	m.bill.saveEvery = 0 // save on every push in the test
	t0 := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	id := ClusterIdentity{Partner: "signal18/fr-2", Sponsors: []string{"u@x.io"}}
	usage := []UnitUsage{
		{Family: BillingFamilyDatabase, Unit: "DBU", Plan: 4, Billable: 4, Priced: true},
		{Family: BillingFamilyCompute, Unit: "APU", Plan: 4, Billable: 6, Priced: true},
		{Family: BillingFamilyArchive, Unit: "BAU", NoPlan: true, Plan: 0, Billable: 3, Priced: false},
		{Family: BillingFamilyBackup, Unit: "BKU", Plan: 2, Billable: 2, Priced: true, UnitPrice: 30, OverPct: 150, UnderPct: 80},
		{Family: "bau_priced", Unit: "BAU", NoPlan: true, Billable: 3, Priced: true, UnitPrice: 2},
	}
	metrics := m.RecordUsage("belair", id, usage, t0) // first tick: dt = 0
	if len(metrics) != 20 {
		t.Fatalf("4 series × 5 families: %d", len(metrics))
	}
	m.RecordUsage("belair", id, usage, t0.Add(60*time.Second))
	m.RecordUsage("belair", id, usage, t0.Add(120*time.Second))
	st, err := m.Statement("", t0.Add(120*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	cs := st.Clusters["belair"]
	if cs == nil || cs.Partner != "signal18/fr-2" || cs.Sponsors[0] != "u@x.io" {
		t.Fatalf("identity captured: %+v", cs)
	}
	_, monthSec := monthBounds(t0)
	db := cs.Units[0]
	if !near(db.Rate, 40) || !near(db.MonthCost, 40*120/monthSec) || !near(db.MonthPlan, 4*120/monthSec) {
		t.Fatalf("database: rate 40, accrued 120 s: %+v", db)
	}
	ap := cs.Units[1]
	if ap.OverCommit != 2 || !near(ap.Rate, 20+2*5*1.5) || !near(ap.MonthOverCommit, 2*120/monthSec) {
		t.Fatalf("compute: 2 over at 150%%: %+v", ap)
	}
	// The price splits: plan 20, + over 15, − under 0 = 35; accrued the same way.
	if !near(ap.PlanCost, 20) || !near(ap.OverCost, 15) || ap.UnderCredit != 0 || !near(ap.MonthPlanCost+ap.MonthOverCost-ap.MonthUnderCredit, ap.MonthCost) {
		t.Fatalf("cost split: %+v", ap)
	}
	if ar := cs.Units[2]; ar.Priced || ar.Rate != 0 || ar.Billable != 3 {
		t.Fatalf("unpriced archives keep the units: %+v", ar)
	}
	// A row carrying its cluster's price overrides the manager's default list.
	if bk := cs.Units[3]; bk.UnitPrice != 30 || bk.OverCommitPct != 150 || !near(bk.Rate, 60) {
		t.Fatalf("the row's own price must apply: %+v", bk)
	}
	// Pure usage: billed units × price, never over or under a plan.
	if pa := cs.Units[4]; pa.OverCommit != 0 || pa.UnderCommit != 0 || !near(pa.Rate, 6) {
		t.Fatalf("archives are pure usage: %+v", pa)
	}
	if !near(cs.Rate, 141) || !near(st.MonthCost, cs.MonthCost) || st.Projected <= st.MonthCost {
		t.Fatalf("totals and projection: %+v", st)
	}
	// Projection per component: the plan of the last tick carried over the time left.
	start, _ := monthBounds(t0)
	left := (monthSec - t0.Add(120*time.Second).Sub(start).Seconds()) / monthSec
	if !near(db.ProjectedPlan, db.MonthPlan+4*left) || !near(ap.ProjectedOverCommit, ap.MonthOverCommit+2*left) || !near(db.ProjectedCost, db.MonthCost+40*left) {
		t.Fatalf("projected components: %+v %+v", db, ap)
	}
	// A pause longer than maxTick is not billed as if the reading had held.
	m.RecordUsage("belair", id, usage, t0.Add(2*time.Hour))
	if cs2, _ := m.ClusterStatementOf("belair", t0.Add(2*time.Hour)); !near(cs2.Units[0].MonthCost, db.MonthCost) {
		t.Fatalf("a 2 h gap must accrue nothing: %v vs %v", cs2.Units[0].MonthCost, db.MonthCost)
	}
	// Reload from the file continues the accruals.
	if _, err := os.Stat(filepath.Join(dir, UnitsLogName)); err != nil {
		t.Fatalf("statement file must exist: %v", err)
	}
	m2 := NewResourceManager()
	m2.SetPrices(BillingPrices{DBU: 10, APU: 5, OverPct: 50, UnderPct: 50})
	if err := m2.SetBillingDir(dir, nil); err != nil {
		t.Fatal(err)
	}
	// loadMonthLocked keyed on time.Now(): point the test at the file's month explicitly.
	m2.mu.Lock()
	_ = m2.loadMonthLocked(t0)
	m2.mu.Unlock()
	cs3, ok := m2.ClusterStatementOf("belair", t0.Add(3*time.Hour))
	if !ok || !near(cs3.Units[0].MonthCost, db.MonthCost) {
		t.Fatalf("reload must continue the month: ok=%v %+v", ok, cs3)
	}
	// Graphite backfill replaces the accruals with the series' integral.
	billingRender = func(target string, from, until int32) ([]float64, []bool, int32, error) {
		return []float64{40, 40, 40}, []bool{false, false, false}, 10, nil
	}
	defer func() {
		billingRender = func(string, int32, int32) ([]float64, []bool, int32, error) { return nil, nil, 0, nil }
	}()
	m2.BackfillFromGraphite([]string{"belair"}, t0.Add(3*time.Hour))
	cs4, _ := m2.ClusterStatementOf("belair", t0.Add(3*time.Hour))
	if !near(cs4.Units[0].MonthCost, 40*30/monthSec) {
		t.Fatalf("backfill: 3 points × 10 s at 40: %v", cs4.Units[0].MonthCost)
	}
	// Month change: the running month is closed as final, the new one starts empty.
	nov := time.Date(2026, 11, 1, 0, 0, 10, 0, time.UTC)
	pushed := []string{}
	m2.SetFinalPush(func(path, month string) error {
		raw, _ := os.ReadFile(path)
		if !strings.Contains(string(raw), "\"final\": true") || !strings.HasSuffix(path, UnitsLogName) {
			t.Fatalf("the rollover pushes the FINAL %s: path=%s", UnitsLogName, path)
		}
		pushed = append(pushed, month)
		return nil
	})
	m2.Tick(nov)
	m2.Tick(nov) // a second tick of the new month pushes nothing more
	if len(pushed) != 1 || pushed[0] != "2026-10" {
		t.Fatalf("the closed month is pushed once: %v", pushed)
	}
	past, err := m2.Statement("2026-10", nov)
	if err != nil || !past.Final || past.Clusters["belair"] == nil {
		t.Fatalf("October must be closed as final: %v %+v", err, past)
	}
	cur, _ := m2.Statement("", nov)
	if cur.Month != "2026-11" || len(cur.Clusters) != 0 {
		t.Fatalf("November starts empty: %+v", cur)
	}
	if months := m2.StatementMonths(); len(months) < 1 || months[0] != "2026-11" && months[0] != "2026-10" {
		t.Fatalf("statement months: %v", months)
	}
}
