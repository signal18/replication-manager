// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/graphite"
)

// Billing is a ResourceManager duty (Stéphane 2026-10-01): the manager already holds every
// cluster's plans, consumption and borrows, it gets the unit prices, and it is the one
// place that prices usage. The rule: the price of a cluster is the INTEGRAL over every
// monitoring period since the first of the month of each unit family's rate
//
//	rate(t) = unit price × [ plan(t) + over(t) × (100 + overcommit%)/100 − under(t) × undercommit%/100 ]
//
// over = max(0, billable − plan), under = max(0, plan − billable) at that tick. The
// clusters push their plan and billable units per family every tick (RecordUsage); the
// manager accrues the unit-seconds and the EUR into the MONTH STATEMENT, the record the
// back office invoices from: one JSON file per month in the billing directory, written
// every minute, closed as final at the month change. Each cluster row carries the partner
// (this infrastructure's Cloud18 identity) and the sponsor identities captured as the
// month runs, so a cluster dropped on the 12th keeps its 12 days. Graphite gets the same
// figures per tick (billing.<CLUSTER>.<family>.{plan,over,under,rate}) for the graphs and
// for the recovery: after a restart the month to date of every live cluster is
// re-integrated from the series, so the file never drifts from what was measured.

// Billing families, in the order the rows are answered.
const (
	BillingFamilyDatabase = "dbu"          // the database servers' plan, in DBU
	BillingFamilyStateful = "stateful_dbu" // stateful applications, in DBU
	BillingFamilyCompute  = "apu"          // applications and proxies, in APU
	BillingFamilyBackup   = "bku"          // local backups, in BKU
	BillingFamilyArchive  = "bau"          // archives, in BAU
	BillingFamilyGateway  = "gwu"          // gateway egress, in GWU, cumulative over the month (#1872)
)

// BillingPrices are the infrastructure's unit prices (EUR per unit per month) and the
// over/under-commit percentages, from cloud18-marketplace-*-price and -*-price-pct.
type BillingPrices struct {
	DBU, APU, BKU, BAU, GWU float64
	OverPct, UnderPct       int
}

func (p BillingPrices) of(family string) float64 {
	switch family {
	case BillingFamilyDatabase, BillingFamilyStateful:
		return p.DBU
	case BillingFamilyCompute:
		return p.APU
	case BillingFamilyBackup:
		return p.BKU
	case BillingFamilyArchive:
		return p.BAU
	case BillingFamilyGateway:
		return p.GWU
	}
	return 0
}

// UnitUsage is what a cluster pushes per family at one tick: its plan and what is billed.
// Priced=false (archives on the client's own storage) keeps the units but no price.
type UnitUsage struct {
	Family   string
	Unit     string
	Plan     float64
	Billable float64
	Priced   bool
	// NoPlan: pure usage (archives), billed at the unit price with no over/under-commit.
	NoPlan bool
	// FreePlan: Plan is a free allowance (the first GWU of traffic): it costs nothing, the
	// unused part is not credited, only the part on top is priced at the plain unit price.
	FreePlan bool
	// The price the cluster applies (its own configuration, the same the unit readings
	// use); 0 = the manager's default price list.
	UnitPrice         float64
	OverPct, UnderPct int
}

// ClusterIdentity is who pays whom, captured with every tick.
type ClusterIdentity struct {
	Partner  string   `json:"partner"`  // the infrastructure's Cloud18 identity
	Sponsors []string `json:"sponsors"` // identities holding the sponsor role (emails for SSO)
}

// UnitBillingRow is one family of a cluster in the statement: the last tick's figures
// and the month integrals (unit-months, 1 = one unit for the whole month; EUR accrued).
type UnitBillingRow struct {
	Family           string  `json:"family"`
	Unit             string  `json:"unit"`
	Plan             float64 `json:"plan"`
	Billable         float64 `json:"billable"`
	OverCommit       float64 `json:"overCommit"`
	UnderCommit      float64 `json:"underCommit"`
	UnitPrice        float64 `json:"unitPrice"`
	OverCommitPct    int     `json:"overCommitPct"`
	UnderCommitPct   int     `json:"underCommitPct"`
	Priced           bool    `json:"priced"`
	Cumulative       bool    `json:"cumulative"`  // month-to-date volume, not integrated (#1872)
	FreePlan         bool    `json:"freePlan"`    // the plan is a free allowance: no plan cost, no unused credit
	Rate             float64 `json:"rate"`        // EUR per month at the last tick = planCost + overCost − underCredit
	PlanCost         float64 `json:"planCost"`    // plan × price, per month, at the last tick
	OverCost         float64 `json:"overCost"`    // + over-commit × price × (100+over%)/100
	UnderCredit      float64 `json:"underCredit"` // − under-commit × price × under%/100
	MonthPlan        float64 `json:"monthPlan"`
	MonthOverCommit  float64 `json:"monthOverCommit"`
	MonthUnderCommit float64 `json:"monthUnderCommit"`
	MonthCost        float64 `json:"monthCost"` // = monthPlanCost + monthOverCost − monthUnderCredit
	MonthPlanCost    float64 `json:"monthPlanCost"`
	MonthOverCost    float64 `json:"monthOverCost"`
	MonthUnderCredit float64 `json:"monthUnderCredit"`
	// Projection to the end of the month: each component as it stands at the last tick
	// carried over the time left (plan count of every tick, over-commit and under-commit
	// projected per unit), then priced: projectedCost = monthCost + rate × time left.
	ProjectedPlan        float64 `json:"projectedPlan"`
	ProjectedOverCommit  float64 `json:"projectedOverCommit"`
	ProjectedUnderCommit float64 `json:"projectedUnderCommit"`
	ProjectedCost        float64 `json:"projectedCost"`
	ProjectedPlanCost    float64 `json:"projectedPlanCost"`
	ProjectedOverCost    float64 `json:"projectedOverCost"`
	ProjectedUnderCredit float64 `json:"projectedUnderCredit"`
	// accrued unit-seconds and EUR·seconds-per-month, the integrals before division
	planSec, overSec, underSec, rateSec      float64
	planCostSec, overCostSec, underCreditSec float64
}

// ClusterStatement is one cluster's rows for the month.
type ClusterStatement struct {
	Cluster   string           `json:"cluster"`
	Partner   string           `json:"partner"`
	Sponsors  []string         `json:"sponsors"`
	Units     []UnitBillingRow `json:"units"`
	Rate      float64          `json:"rate"`      // EUR per month at the last tick
	MonthCost float64          `json:"monthCost"` // EUR accrued this month
	Projected float64          `json:"projected"` // MonthCost + Rate × the time left
	FirstSeen time.Time        `json:"firstSeen"`
	LastSeen  time.Time        `json:"lastSeen"`
	// Accrued unit-seconds per family, kept on disk so a reload continues the month:
	// plan, over, under, rate, planCost, overCost, underCredit.
	Accrued map[string][7]float64 `json:"accrued"`
}

// MonthStatement is the file the back office invoices from.
type MonthStatement struct {
	Month       string                       `json:"month"`
	Currency    string                       `json:"currency"`
	Final       bool                         `json:"final"`
	ElapsedPct  float64                      `json:"elapsedPct"`
	Prices      BillingPrices                `json:"defaultPrices"` // the instance's price list; each row carries the price its cluster applied
	GeneratedAt time.Time                    `json:"generatedAt"`
	Clusters    map[string]*ClusterStatement `json:"clusters"`
	MonthCost   float64                      `json:"monthCost"`
	Rate        float64                      `json:"rate"`
	Projected   float64                      `json:"projected"`
}

type billingState struct {
	prices    BillingPrices
	priced    bool // SetPrices ran: prices is the instance's list, zeros included
	dir       string
	pushFinal func(ctx context.Context, path, month string) error // pushes a closed month's snapshot (Units.<month>.log) to the git sync repository; never called under the lock
	pushing   atomic.Bool                                         // one push at a time
	lastPush  time.Time                                           // last push attempt, retried every pushRetry while a snapshot is pending
	month     string
	stmt      *MonthStatement
	lastTick  map[string]time.Time
	lastSave  time.Time
	loaded    bool
	backfill  bool
	logf      func(format string, args ...interface{})
	maxTick   time.Duration
	saveEvery time.Duration
}

// renderFn reads a graphite series; a variable so tests never reach the network.
var billingRender = func(target string, from, until int32) ([]float64, []bool, int32, error) {
	md, err := graphite.Zipper.Render(target, from, until)
	if err != nil {
		return nil, nil, 0, err
	}
	return md.Values, md.IsAbsent, md.GetStepTime(), nil
}

func (m *ResourceManager) billing() *billingState {
	if m.bill == nil {
		m.bill = &billingState{lastTick: map[string]time.Time{}, maxTick: 5 * time.Minute, saveEvery: time.Minute,
			logf: func(string, ...interface{}) {}}
	}
	return m.bill
}

// SetPrices gives the manager the infrastructure's prices; called at every config refresh,
// so a price change applies from the next tick.
func (m *ResourceManager) SetPrices(p BillingPrices) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	b.prices, b.priced = p, true
}

// Prices is the infrastructure's price list in force: the one every cluster prices with.
func (m *ResourceManager) Prices() (BillingPrices, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	return b.prices, b.priced
}

// SetBillingDir names where the month statements live and loads the current month.
func (m *ResourceManager) SetBillingDir(dir string, logf func(string, ...interface{})) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	b.dir = dir
	if logf != nil {
		b.logf = logf
	}
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return m.loadMonthLocked(time.Now())
}

func monthKey(t time.Time) string { return t.UTC().Format("2006-01") }

func monthBounds(t time.Time) (start time.Time, seconds float64) {
	t = t.UTC()
	start = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0).Sub(start).Seconds()
}

// UnitsLogName is the statement file: the running month's unit usage, in the working
// directory, rewritten every minute, never staged by the periodic git sync. At the
// rollover (or at startup when the file holds a past month) the closed month is written
// to its snapshot Units.<month>.log, pushed to the git sync repository once it can be
// (outside the lock, bounded, retried every pushRetry until it lands), and the snapshot
// is removed; the file then starts the new month. Past months are the Units.<month>.log
// files of the git sync repository.
const UnitsLogName = "Units.log"

// pushRetry bounds how often a pending snapshot is pushed again after a failure.
const pushRetry = 10 * time.Minute

func (m *ResourceManager) statementPath() string {
	return filepath.Join(m.billing().dir, UnitsLogName)
}

// closedStatementPath is the snapshot of a closed month, kept until pushed.
func (m *ResourceManager) closedStatementPath(month string) string {
	return filepath.Join(m.billing().dir, "Units."+month+".log")
}

// SetFinalPush names what pushes a closed month's snapshot to git. The callback runs
// outside the manager's lock with a bounded context; an error keeps the snapshot and
// the push is retried later.
func (m *ResourceManager) SetFinalPush(f func(ctx context.Context, path, month string) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.billing().pushFinal = f
}

// closeMonthLocked writes a closed month's snapshot (final) next to Units.log.
func (m *ResourceManager) closeMonthLocked(st *MonthStatement, now time.Time) {
	b := m.billing()
	st.Final = true
	st.ElapsedPct = 100
	st.GeneratedAt = now
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		b.logf("statement %s not closed: %v", st.Month, err)
		return
	}
	path := m.closedStatementPath(st.Month)
	if err := os.WriteFile(path+".tmp", raw, 0o644); err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		b.logf("statement %s not closed: %v", st.Month, err)
		return
	}
	for name, cs := range st.Clusters {
		b.logf("Billing statement %s closed for cluster %s (partner %s, sponsors %s): %.2f EUR", st.Month, name, cs.Partner, strings.Join(cs.Sponsors, ","), cs.MonthCost)
	}
}

// pendingClosedMonths lists the snapshots not yet pushed, oldest first.
func (m *ResourceManager) pendingClosedMonths(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "Units.") && strings.HasSuffix(n, ".log") && n != UnitsLogName && !strings.HasSuffix(n, ".tmp") {
			out = append(out, strings.TrimSuffix(strings.TrimPrefix(n, "Units."), ".log"))
		}
	}
	sort.Strings(out)
	return out
}

// PushPending pushes the closed months whose snapshot is still on disk, one push at a
// time, never under the manager's lock; a snapshot is removed once its push succeeded.
// Called by Tick in a goroutine, and directly by tests.
func (m *ResourceManager) PushPending(ctx context.Context) {
	m.mu.Lock()
	b := m.billing()
	dir, push := b.dir, b.pushFinal
	m.mu.Unlock()
	if dir == "" || push == nil || !b.pushing.CompareAndSwap(false, true) {
		return
	}
	defer b.pushing.Store(false)
	for _, month := range m.pendingClosedMonths(dir) {
		path := filepath.Join(dir, "Units."+month+".log")
		pctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		err := push(pctx, path, month)
		cancel()
		if err != nil {
			b.logf("Units.%s.log not pushed to git, kept and retried in %s: %v", month, pushRetry, err)
			return
		}
		if rerr := os.Remove(path); rerr != nil && !os.IsNotExist(rerr) {
			b.logf("Units.%s.log pushed to git but not removed: %v", month, rerr)
		} else {
			b.logf("Units.%s.log pushed to git once, snapshot removed", month)
		}
	}
}

func (m *ResourceManager) loadMonthLocked(now time.Time) error {
	b := m.billing()
	b.month = monthKey(now)
	b.stmt = &MonthStatement{Month: b.month, Currency: "EUR", Clusters: map[string]*ClusterStatement{}}
	b.loaded = true
	if b.dir == "" {
		return nil
	}
	raw, err := os.ReadFile(m.statementPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var st MonthStatement
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("billing statement %s unreadable, starting the month from the series: %w", b.month, err)
	}
	if st.Month != b.month {
		// Units.log holds a past month (a restart across the rollover): close it now,
		// its snapshot gets pushed like any other, and this month starts fresh.
		if st.Month != "" {
			if _, err := os.Stat(m.closedStatementPath(st.Month)); os.IsNotExist(err) {
				m.closeMonthLocked(&st, now)
			}
		}
		return nil
	}
	if st.Clusters == nil {
		st.Clusters = map[string]*ClusterStatement{}
	}
	st.Final = false
	for _, cs := range st.Clusters {
		for i := range cs.Units {
			r := &cs.Units[i]
			if a, ok := cs.Accrued[r.Family]; ok {
				r.planSec, r.overSec, r.underSec, r.rateSec = a[0], a[1], a[2], a[3]
				r.planCostSec, r.overCostSec, r.underCreditSec = a[4], a[5], a[6]
			}
		}
	}
	b.stmt = &st
	return nil
}

// unitRate is the asymmetric price per month of a family at one instant.
func unitRate(plan, billable, price float64, overPct, underPct int) float64 {
	if plan < 0 {
		plan = 0
	}
	if billable < 0 {
		billable = 0
	}
	if price <= 0 {
		return 0
	}
	if billable > plan {
		return plan*price + (billable-plan)*price*float64(100+overPct)/100
	}
	reduced := float64(100 - underPct)
	if reduced < 0 {
		reduced = 0
	}
	return billable*price + (plan-billable)*price*reduced/100
}

func billingToken(name string) string {
	return strings.ToUpper(computeTokenReplacer.Replace(name))
}

// RecordUsage is the per-tick push of one cluster: prices the families, accrues the
// month, and returns the graphite metrics to emit with the cluster's batch.
func (m *ResourceManager) RecordUsage(cluster string, id ClusterIdentity, usage []UnitUsage, now time.Time) []graphite.Metric {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	if !b.loaded || b.month != monthKey(now) {
		m.rolloverLocked(now)
	}
	cs, ok := b.stmt.Clusters[cluster]
	if !ok {
		cs = &ClusterStatement{Cluster: cluster, FirstSeen: now, Accrued: map[string][7]float64{}}
		b.stmt.Clusters[cluster] = cs
	}
	cs.Partner = id.Partner
	if id.Sponsors != nil {
		cs.Sponsors = append([]string(nil), id.Sponsors...)
	}
	if cs.Sponsors == nil {
		cs.Sponsors = []string{}
	}
	// Δt since this cluster's last push, bounded: a pause (restart, maintenance) is not
	// billed as if the last reading had held, the series fill the gap on recovery.
	dt := 0.0
	if last, ok := b.lastTick[cluster]; ok {
		dt = now.Sub(last).Seconds()
		if dt < 0 || dt > b.maxTick.Seconds() {
			dt = 0
		}
	}
	b.lastTick[cluster] = now
	_, monthSeconds := monthBounds(now)
	tok := billingToken(cluster)
	var metrics []graphite.Metric
	f := func(v float64) string { return strconv.FormatFloat(v, 'f', 4, 64) }
	ts := now.Unix()
	rows := make([]UnitBillingRow, 0, len(usage))
	cs.Rate = 0
	for _, u := range usage {
		row := UnitBillingRow{Family: u.Family, Unit: u.Unit, Plan: u.Plan, Billable: u.Billable,
			UnitPrice: b.prices.of(u.Family), OverCommitPct: b.prices.OverPct, UnderCommitPct: b.prices.UnderPct}
		if u.UnitPrice > 0 {
			row.UnitPrice, row.OverCommitPct, row.UnderCommitPct = u.UnitPrice, u.OverPct, u.UnderPct
		}
		row.Priced = u.Priced && row.UnitPrice > 0
		if u.NoPlan {
			// Pure usage: no plan to be over or under, the unit price and nothing else.
			row.Plan, row.OverCommitPct, row.UnderCommitPct = 0, 0, 0
			if row.Priced {
				row.PlanCost = u.Billable * row.UnitPrice
			}
		} else {
			row.OverCommit = math.Max(0, u.Billable-u.Plan)
			row.UnderCommit = math.Max(0, u.Plan-u.Billable)
			if row.Priced {
				row.PlanCost = u.Plan * row.UnitPrice
				row.OverCost = row.OverCommit * row.UnitPrice * float64(100+row.OverCommitPct) / 100
				row.UnderCredit = row.UnderCommit * row.UnitPrice * float64(row.UnderCommitPct) / 100
			}
		}
		if u.FreePlan {
			// A free allowance: nothing to pay for the plan, nothing credited below it,
			// the part on top at the plain unit price (no over-commit surcharge).
			row.FreePlan = true
			row.UnderCommit, row.PlanCost, row.UnderCredit = 0, 0, 0
			if row.Priced {
				row.OverCost = row.OverCommit * row.UnitPrice
			}
		}
		row.Rate = row.PlanCost + row.OverCost - row.UnderCredit
		// Continue the family's integrals from the statement.
		for _, old := range cs.Units {
			if old.Family == u.Family {
				row.planSec, row.overSec, row.underSec, row.rateSec = old.planSec, old.overSec, old.underSec, old.rateSec
				row.planCostSec, row.overCostSec, row.underCreditSec = old.planCostSec, old.overCostSec, old.underCreditSec
			}
		}
		row.planSec += u.Plan * dt
		row.overSec += row.OverCommit * dt
		row.underSec += row.UnderCommit * dt
		row.rateSec += row.Rate * dt
		row.planCostSec += row.PlanCost * dt
		row.overCostSec += row.OverCost * dt
		row.underCreditSec += row.UnderCredit * dt
		row.MonthPlan = row.planSec / monthSeconds
		row.MonthOverCommit = row.overSec / monthSeconds
		row.MonthUnderCommit = row.underSec / monthSeconds
		row.MonthCost = row.rateSec / monthSeconds
		row.MonthPlanCost = row.planCostSec / monthSeconds
		row.MonthOverCost = row.overCostSec / monthSeconds
		row.MonthUnderCredit = row.underCreditSec / monthSeconds
		cs.Accrued[u.Family] = [7]float64{row.planSec, row.overSec, row.underSec, row.rateSec, row.planCostSec, row.overCostSec, row.underCreditSec}
		cs.Rate += row.Rate
		rows = append(rows, row)
		base := fmt.Sprintf("billing.%s.%s.", tok, u.Family)
		metrics = append(metrics,
			graphite.NewMetric(base+"plan", f(u.Plan), ts),
			graphite.NewMetric(base+"over", f(row.OverCommit), ts),
			graphite.NewMetric(base+"under", f(row.UnderCommit), ts),
			graphite.NewMetric(base+"rate", f(row.Rate), ts))
	}
	cs.Units = rows
	cs.LastSeen = now
	m.recomputeTotalsLocked(now)
	if b.dir != "" && now.Sub(b.lastSave) >= b.saveEvery {
		if err := m.saveLocked(now); err != nil {
			b.logf("billing statement %s not saved: %v", b.month, err)
		}
	}
	return metrics
}

func (m *ResourceManager) recomputeTotalsLocked(now time.Time) {
	b := m.billing()
	start, monthSeconds := monthBounds(now)
	elapsed := math.Max(0, now.UTC().Sub(start).Seconds())
	b.stmt.ElapsedPct = math.Round(elapsed/monthSeconds*1000) / 10
	b.stmt.Prices = b.prices
	b.stmt.MonthCost, b.stmt.Rate, b.stmt.Projected = 0, 0, 0
	left := (monthSeconds - elapsed) / monthSeconds
	for _, cs := range b.stmt.Clusters {
		cs.MonthCost, cs.Projected = 0, 0
		for i := range cs.Units {
			r := &cs.Units[i]
			r.ProjectedPlan = r.MonthPlan + r.Plan*left
			r.ProjectedOverCommit = r.MonthOverCommit + r.OverCommit*left
			r.ProjectedUnderCommit = r.MonthUnderCommit + r.UnderCommit*left
			r.ProjectedCost = r.MonthCost + r.Rate*left
			r.ProjectedPlanCost = r.MonthPlanCost + r.PlanCost*left
			r.ProjectedOverCost = r.MonthOverCost + r.OverCost*left
			r.ProjectedUnderCredit = r.MonthUnderCredit + r.UnderCredit*left
			if r.FreePlan { // a free allowance: no plan cost, no credit, the on-top part at the plain price
				r.ProjectedUnderCommit, r.ProjectedPlanCost, r.ProjectedUnderCredit = 0, 0, 0
				r.ProjectedCost = r.ProjectedOverCost
			}
			cs.MonthCost += r.MonthCost
			cs.Projected += r.ProjectedCost
		}
		b.stmt.MonthCost += cs.MonthCost
		b.stmt.Rate += cs.Rate
		b.stmt.Projected += cs.Projected
	}
	b.stmt.GeneratedAt = now
}

func (m *ResourceManager) saveLocked(now time.Time) error {
	b := m.billing()
	if b.dir == "" || b.stmt == nil {
		return nil
	}
	b.lastSave = now
	raw, err := json.MarshalIndent(b.stmt, "", "  ")
	if err != nil {
		return err
	}
	path := m.statementPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// rolloverLocked closes the running month as final and starts the month of now.
func (m *ResourceManager) rolloverLocked(now time.Time) {
	b := m.billing()
	if b.loaded && b.stmt != nil && b.month != "" && b.month != monthKey(now) {
		m.recomputeTotalsLocked(now)
		m.closeMonthLocked(b.stmt, now) // the snapshot; the push happens outside the lock (PushPending)
	}
	b.lastTick = map[string]time.Time{}
	if err := m.loadMonthLocked(now); err != nil {
		b.logf("%v", err)
	}
}

// Tick is the manager's own clock: saves the running statement and rolls the month over
// even when no cluster pushes.
func (m *ResourceManager) Tick(now time.Time) {
	m.mu.Lock()
	b := m.billing()
	if !b.loaded || b.month != monthKey(now) {
		m.rolloverLocked(now)
	} else if b.dir != "" && now.Sub(b.lastSave) >= b.saveEvery {
		m.recomputeTotalsLocked(now)
		if err := m.saveLocked(now); err != nil {
			b.logf("billing statement %s not saved: %v", b.month, err)
		}
	}
	// A closed month still on disk is pushed outside the lock, bounded, at most every
	// pushRetry: the monitor path never waits on a git remote (F2).
	due := b.dir != "" && b.pushFinal != nil && now.Sub(b.lastPush) >= pushRetry && len(m.pendingClosedMonths(b.dir)) > 0
	if due {
		b.lastPush = now
	}
	m.mu.Unlock()
	if due {
		go m.PushPending(context.Background())
	}
}

// Statement is the running month (or a past one by key, read from its file).
func (m *ResourceManager) Statement(month string, now time.Time) (*MonthStatement, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	if !b.loaded {
		m.rolloverLocked(now)
	}
	if month == "" || month == b.month {
		m.recomputeTotalsLocked(now)
		cp := *b.stmt
		return &cp, nil
	}
	if b.dir == "" {
		return nil, fmt.Errorf("no working directory: past statements are not kept")
	}
	raw, err := os.ReadFile(m.closedStatementPath(month))
	if err != nil {
		return nil, fmt.Errorf("no statement for %s on disk: a closed month is Units.%s.log in the git sync repository once pushed", month, month)
	}
	var st MonthStatement
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

// ClusterStatementOf is one cluster's rows of the running month.
func (m *ResourceManager) ClusterStatementOf(cluster string, now time.Time) (*ClusterStatement, bool) {
	st, err := m.Statement("", now)
	if err != nil {
		return nil, false
	}
	cs, ok := st.Clusters[cluster]
	if !ok {
		return nil, false
	}
	cp := *cs
	return &cp, true
}

// integrateSeries sums value × step over the non-absent points (value-seconds).
func integrateSeries(values []float64, absent []bool, step int32) float64 {
	if step <= 0 {
		step = 10
	}
	sum := 0.0
	for i, v := range values {
		if (i < len(absent) && absent[i]) || math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		sum += v * float64(step)
	}
	return sum
}

// BackfillFromGraphite re-integrates the month to date of the given clusters from the
// billing series, once after a start: memory was lost, the file is up to a minute old, the
// series hold every tick. Clusters absent from graphite keep the file's figures.
func (m *ResourceManager) BackfillFromGraphite(clusters []string, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	if b.backfill {
		return
	}
	if !b.loaded || b.month != monthKey(now) {
		m.rolloverLocked(now)
	}
	start, monthSeconds := monthBounds(now)
	from, until := int32(start.Unix()), int32(now.Unix())
	done := 0
	for _, name := range clusters {
		cs, ok := b.stmt.Clusters[name]
		if !ok {
			continue
		}
		tok := billingToken(name)
		for i := range cs.Units {
			r := &cs.Units[i]
			base := fmt.Sprintf("billing.%s.%s.", tok, r.Family)
			vals, abs, step, err := billingRender(base+"plan", from, until)
			if err != nil || len(vals) == 0 {
				continue
			}
			r.planSec = integrateSeries(vals, abs, step)
			if v, a, s, err := billingRender(base+"over", from, until); err == nil {
				r.overSec = integrateSeries(v, a, s)
			}
			if v, a, s, err := billingRender(base+"under", from, until); err == nil {
				r.underSec = integrateSeries(v, a, s)
			}
			if v, a, s, err := billingRender(base+"rate", from, until); err == nil {
				r.rateSec = integrateSeries(v, a, s)
			}
			// The cost components are recomputed from the re-integrated units at the row's
			// current price: an exact split when the price held over the month.
			r.planCostSec = r.planSec * r.UnitPrice
			r.overCostSec = r.overSec * r.UnitPrice * float64(100+r.OverCommitPct) / 100
			r.underCreditSec = r.underSec * r.UnitPrice * float64(r.UnderCommitPct) / 100
			cs.Accrued[r.Family] = [7]float64{r.planSec, r.overSec, r.underSec, r.rateSec, r.planCostSec, r.overCostSec, r.underCreditSec}
			r.MonthPlan, r.MonthOverCommit, r.MonthUnderCommit, r.MonthCost = r.planSec/monthSeconds, r.overSec/monthSeconds, r.underSec/monthSeconds, r.rateSec/monthSeconds
			r.MonthPlanCost, r.MonthOverCost, r.MonthUnderCredit = r.planCostSec/monthSeconds, r.overCostSec/monthSeconds, r.underCreditSec/monthSeconds
			done++
		}
	}
	if done > 0 {
		b.backfill = true
		b.logf("Billing statement %s re-integrated from graphite for %d family rows", b.month, done)
	}
}

// StatementMonths lists the months with a statement on disk, newest first.
func (m *ResourceManager) StatementMonths() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.billing()
	out := []string{}
	if b.month != "" {
		out = append(out, b.month)
	}
	if b.dir != "" {
		for _, p := range m.pendingClosedMonths(b.dir) {
			if p != b.month {
				out = append(out, p) // closed, pushed later; the rest is the git sync repository
			}
		}
	}
	return out
}

// unitPrices is the price list a cluster bills with: the ResourceManager's, the
// instance's one, applied live. The cluster's copy of these server-scope settings is
// read only without a manager (unit tests) or before the manager received its list,
// never over it.
func (cluster *Cluster) unitPrices() BillingPrices {
	if cluster.resources != nil {
		// before the instance gave its list (SetPrices), the cluster copy, never zeros
		if p, ok := cluster.resources.Prices(); ok {
			return p
		}
	}
	c := cluster.Conf
	return BillingPrices{DBU: c.Cloud18MarketplaceDBUPrice, APU: c.Cloud18MarketplaceAPUPrice, BKU: c.Cloud18MarketplaceBKUPrice,
		BAU: c.Cloud18MarketplaceBAUPrice, GWU: c.Cloud18MarketplaceGWUPrice,
		OverPct: c.Cloud18MarketplaceOvercommitPricePct, UnderPct: c.Cloud18MarketplaceUndercommitPricePct}
}
