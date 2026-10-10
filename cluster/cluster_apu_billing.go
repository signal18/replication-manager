// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"math"
	"time"
)

// APUBilling is the per-cluster Compute (apps + proxies) billing picture against the APU
// plan (prov-service-plan-apu, the sum of the per-unit reservations). The MEASURED side
// (compute sensor) stays honest in the apu.<cluster>.<unit>.* series, 0 when nothing pushed;
// the BILLABLE side applies the floor a running unit can never go under (Stéphane
// 2026-09-28: "can not invoice less than 1 APU per application if we have no metrics", and
// "the APU on app depend on load balancing or disk replication"):
//   - an app running on N agents counts at least N APU (one instance per agent when flex,
//     one instance plus the drbd-replicated volume on every agent when failover);
//   - a proxy counts at least 1 APU;
//   - a unit that is down counts 0 (its plan unit then falls under the under-commit rate).
//
// Measured consumption only matters above that floor: billable = max(floor, ceil(measured)).
// Cost = planUnitCost with cloud18-marketplace-apu-price and the instance-wide asymmetric
// ratios, exactly like the BKU.
type APUBilling struct {
	Plan           int       `json:"plan"`           // prov-service-plan-apu
	Units          int       `json:"units"`          // compute units (apps + proxies) in the cluster
	RunningUnits   int       `json:"runningUnits"`   // of which not down
	FloorUnits     int       `json:"floorUnits"`     // Σ floors of the running units (instances)
	MeasuredApu    float64   `json:"measuredApu"`    // Σ measured APU (sensor), 0 when unmeasured
	BillableUnits  int       `json:"billableUnits"`  // Σ max(floor, ceil(measured)) per running unit
	OverPlanUnits  int       `json:"overPlanUnits"`  // max(0, BillableUnits - Plan)
	UnderPlanUnits int       `json:"underPlanUnits"` // max(0, Plan - BillableUnits)
	UnitPrice      float64   `json:"unitPrice"`      // cloud18-marketplace-apu-price, Eur per APU per month (0 = not priced)
	OverPricePct   int       `json:"overPricePct"`   // cloud18-marketplace-overcommit-price-pct
	UnderPricePct  int       `json:"underPricePct"`  // cloud18-marketplace-undercommit-price-pct
	MonthlyCost    float64   `json:"monthlyCost"`    // planUnitCost(Plan, BillableUnits, ...)
	UpdatedAt      time.Time `json:"updatedAt"`
}

// apuBillingUnit is one compute unit as the billing sees it.
type apuBillingUnit struct {
	Name     string
	Floor    int     // agents for an app, 1 for a proxy
	Measured float64 // last sensor reading in APU, 0 when never pushed
	Down     bool
}

// computeAPUBilling folds the units into the cluster picture.
// StatefulBilling is the DBU twin of APUBilling for the STATEFUL apps of a cluster
// (app-stateful, e.g. minio): same floor rule (1 unit per running instance), same
// over/under ratios around the plan, but the unit is the DBU and the price
// cloud18-marketplace-dbu-price. Its plan is the sum of the stateful apps' planned DBU,
// never the databases' prov-service-plan-dbu: the two stay separate lines.
type StatefulBilling struct {
	Plan           int       `json:"plan"`           // Σ planned DBU of the stateful apps (StatefulPlanByCluster, rounded)
	Units          int       `json:"units"`          // stateful apps in the cluster
	RunningUnits   int       `json:"runningUnits"`   // of which not down
	FloorUnits     int       `json:"floorUnits"`     // Σ floors of the running units (instances)
	MeasuredDbu    float64   `json:"measuredDbu"`    // Σ measured DBU (sensor, Database ratio), 0 when unmeasured
	BillableUnits  int       `json:"billableUnits"`  // Σ max(floor, ceil(measured)) per running unit
	OverPlanUnits  int       `json:"overPlanUnits"`  // max(0, BillableUnits - Plan)
	UnderPlanUnits int       `json:"underPlanUnits"` // max(0, Plan - BillableUnits)
	UnitPrice      float64   `json:"unitPrice"`      // cloud18-marketplace-dbu-price, Eur per DBU per month (0 = not priced)
	OverPricePct   int       `json:"overPricePct"`
	UnderPricePct  int       `json:"underPricePct"`
	MonthlyCost    float64   `json:"monthlyCost"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// unitBilling is the unit-agnostic result of the floor/measured/plan arithmetic shared
// by the APU (compute) and DBU (stateful) tracks -- one rule, two prices.
type unitBilling struct {
	Units, Running, Floor int
	Measured              float64
	Billable, Over, Under int
	Cost                  float64
}

func computeUnitBilling(units []apuBillingUnit, plan int, unitPrice float64, overPct, underPct int) unitBilling {
	b := unitBilling{Units: len(units)}
	for _, u := range units {
		if u.Down {
			continue
		}
		b.Running++
		floor := u.Floor
		if floor < 1 {
			floor = 1
		}
		b.Floor += floor
		b.Measured += math.Max(0, u.Measured)
		billable := int(math.Ceil(math.Max(0, u.Measured)))
		if billable < floor {
			billable = floor
		}
		b.Billable += billable
	}
	if plan < 0 {
		plan = 0
	}
	if b.Billable > plan {
		b.Over = b.Billable - plan
	} else {
		b.Under = plan - b.Billable
	}
	b.Cost = planUnitCost(plan, b.Billable, unitPrice, overPct, underPct)
	return b
}

func computeAPUBilling(units []apuBillingUnit, plan int, unitPrice float64, overPct, underPct int, now time.Time) *APUBilling {
	if plan < 0 {
		plan = 0
	}
	u := computeUnitBilling(units, plan, unitPrice, overPct, underPct)
	return &APUBilling{Plan: plan, Units: u.Units, RunningUnits: u.Running, FloorUnits: u.Floor, MeasuredApu: u.Measured,
		BillableUnits: u.Billable, OverPlanUnits: u.Over, UnderPlanUnits: u.Under,
		UnitPrice: unitPrice, OverPricePct: overPct, UnderPricePct: underPct, MonthlyCost: u.Cost, UpdatedAt: now}
}

func computeStatefulBilling(units []apuBillingUnit, plan int, unitPrice float64, overPct, underPct int, now time.Time) *StatefulBilling {
	if plan < 0 {
		plan = 0
	}
	u := computeUnitBilling(units, plan, unitPrice, overPct, underPct)
	return &StatefulBilling{Plan: plan, Units: u.Units, RunningUnits: u.Running, FloorUnits: u.Floor, MeasuredDbu: u.Measured,
		BillableUnits: u.Billable, OverPlanUnits: u.Over, UnderPlanUnits: u.Under,
		UnitPrice: unitPrice, OverPricePct: overPct, UnderPricePct: underPct, MonthlyCost: u.Cost, UpdatedAt: now}
}

// RefreshComputeBilling rebuilds ComputeUnits from the cluster's apps and proxies, the
// ResourceManager's last measured readings and the APU plan. Called each tick right after
// RefreshComputePlanAPU (same inputs, same cadence). No-op without a manager.
func (cluster *Cluster) RefreshComputeBilling() {
	if cluster == nil || cluster.resources == nil {
		return
	}
	units := make([]apuBillingUnit, 0, len(cluster.Apps)+len(cluster.Proxies))
	stateful := make([]apuBillingUnit, 0)
	for _, app := range cluster.Apps {
		if app == nil || cluster.engineServerOfApp(app) != nil {
			// an engine that is a monitored server is the server's DBU, never an app unit
			continue
		}
		k := AppKey{Cluster: cluster.Name, App: app.Name, Kind: KindApp}
		u := apuBillingUnit{Name: app.Name, Floor: cluster.appInstanceCount(app), Down: app.IsDown()}
		if app.AppConfig != nil && app.AppConfig.AppStateful {
			if r := cluster.resources.GetStatefulConsumed(k); r != nil {
				u.Measured = r.Dbu
			}
			stateful = append(stateful, u)
			continue
		}
		if r := cluster.resources.GetAppConsumed(k); r != nil {
			u.Measured = r.Apu
		}
		units = append(units, u)
	}
	for _, prx := range cluster.Proxies {
		if prx == nil {
			continue
		}
		u := apuBillingUnit{Name: prx.GetName(), Floor: 1, Down: prx.IsDown()}
		if r := cluster.resources.GetAppConsumed(AppKey{Cluster: cluster.Name, App: prx.GetName(), Kind: KindProxy}); r != nil {
			u.Measured = r.Apu
		}
		units = append(units, u)
	}
	cluster.ComputeUnits = computeAPUBilling(units, cluster.Conf.ProvServicePlanApu, cluster.Conf.Cloud18MarketplaceAPUPrice,
		cluster.Conf.Cloud18MarketplaceOvercommitPricePct, cluster.Conf.Cloud18MarketplaceUndercommitPricePct, time.Now())
	// Stateful apps: their own DBU line, plan = Σ their planned DBU (never the DB plan).
	statefulPlan := int(cluster.resources.StatefulPlanByCluster(cluster.Name).Dbu + 0.5)
	cluster.StatefulUnits = computeStatefulBilling(stateful, statefulPlan, cluster.Conf.Cloud18MarketplaceDBUPrice,
		cluster.Conf.Cloud18MarketplaceOvercommitPricePct, cluster.Conf.Cloud18MarketplaceUndercommitPricePct, time.Now())
}
