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
func computeAPUBilling(units []apuBillingUnit, plan int, unitPrice float64, overPct, underPct int, now time.Time) *APUBilling {
	b := &APUBilling{Plan: plan, Units: len(units), UnitPrice: unitPrice, OverPricePct: overPct, UnderPricePct: underPct, UpdatedAt: now}
	for _, u := range units {
		if u.Down {
			continue
		}
		b.RunningUnits++
		floor := u.Floor
		if floor < 1 {
			floor = 1
		}
		b.FloorUnits += floor
		b.MeasuredApu += math.Max(0, u.Measured)
		billable := int(math.Ceil(math.Max(0, u.Measured)))
		if billable < floor {
			billable = floor
		}
		b.BillableUnits += billable
	}
	if plan < 0 {
		plan = 0
	}
	if b.BillableUnits > plan {
		b.OverPlanUnits = b.BillableUnits - plan
	} else {
		b.UnderPlanUnits = plan - b.BillableUnits
	}
	b.MonthlyCost = planUnitCost(plan, b.BillableUnits, unitPrice, overPct, underPct)
	return b
}

// RefreshComputeBilling rebuilds ComputeUnits from the cluster's apps and proxies, the
// ResourceManager's last measured readings and the APU plan. Called each tick right after
// RefreshComputePlanAPU (same inputs, same cadence). No-op without a manager.
func (cluster *Cluster) RefreshComputeBilling() {
	if cluster == nil || cluster.resources == nil {
		return
	}
	units := make([]apuBillingUnit, 0, len(cluster.Apps)+len(cluster.Proxies))
	for _, app := range cluster.Apps {
		if app == nil {
			continue
		}
		u := apuBillingUnit{Name: app.Name, Floor: cluster.appInstanceCount(app), Down: app.IsDown()}
		if r := cluster.resources.GetAppConsumed(AppKey{Cluster: cluster.Name, App: app.Name, Kind: KindApp}); r != nil {
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
}
