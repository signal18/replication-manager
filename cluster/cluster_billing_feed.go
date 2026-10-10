// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"sort"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
)

// The cluster's side of billing: it knows its plans and what it really holds per unit
// family, and who sponsors it; it pushes that to the ResourceManager every tick, which
// prices it and keeps the month statement (resource_manager_billing.go).

// BillingUsage is the plan and the billable units of the five families at this tick: the
// database plan against what the servers hold (config DBU when provisioned, plan +
// borrowed never less), and the four unit objects the cluster already maintains.
func (cluster *Cluster) BillingUsage() []UnitUsage {
	// Billable = what the servers really hold: the config DBU per node (the pivot of
	// the allocated axes), which the dynamic resize moves above the plan (borrow) or
	// below it (shrink). An unprovisioned cluster holds nothing: it is billed its plan.
	plan := float64(cluster.GetPlanDbu())
	billable := plan
	if cluster.IsProvision && len(cluster.Servers) > 0 {
		if cfg := cluster.GetConfigDBUPerNode().Dbu; cfg > 0 {
			billable = cfg * float64(len(cluster.Servers))
		}
	}
	// Prices: the ResourceManager's price list, the instance's one, the same its unit
	// readings price with (never the cluster's copy of a server-scope setting).
	c := cluster.Conf
	pr := cluster.unitPrices()
	over, under := pr.OverPct, pr.UnderPct
	out := []UnitUsage{{Family: BillingFamilyDatabase, Unit: "DBU", Plan: plan, Billable: billable, Priced: true,
		UnitPrice: pr.DBU, OverPct: over, UnderPct: under}}
	st := UnitUsage{Family: BillingFamilyStateful, Unit: "DBU", Priced: true, UnitPrice: pr.DBU, OverPct: over, UnderPct: under}
	if b := cluster.StatefulUnits; b != nil {
		st.Plan, st.Billable = float64(b.Plan), float64(b.BillableUnits)
		if b.UnitPrice > 0 {
			st.UnitPrice, st.OverPct, st.UnderPct = b.UnitPrice, b.OverPricePct, b.UnderPricePct
		}
	}
	co := UnitUsage{Family: BillingFamilyCompute, Unit: "APU", Priced: true, UnitPrice: pr.APU, OverPct: over, UnderPct: under}
	if b := cluster.ComputeUnits; b != nil {
		co.Plan, co.Billable = float64(b.Plan), float64(b.BillableUnits)
		if b.UnitPrice > 0 {
			co.UnitPrice, co.OverPct, co.UnderPct = b.UnitPrice, b.OverPricePct, b.UnderPricePct
		}
	}
	bk := UnitUsage{Family: BillingFamilyBackup, Unit: "BKU", Priced: true, UnitPrice: pr.BKU, OverPct: over, UnderPct: under}
	if b := cluster.BackupUnits; b != nil {
		// The units really consumed, not BilledUnits (floored at the plan): the ledger
		// derives the over-commit AND the under-commit from plan vs consumed, so the
		// unused BKU is credited like the other families (Stéphane 2026-10-02).
		bk.Plan, bk.Billable = float64(b.Plan), float64(b.ConsumedUnits)
		if b.UnitPrice > 0 {
			bk.UnitPrice, bk.OverPct, bk.UnderPct = b.UnitPrice, b.OverPricePct, b.UnderPricePct
		}
	}
	// Archives: priced unless the client brought its own storage; before the first
	// reading of the day there are no units yet, the price still shows.
	ar := UnitUsage{Family: BillingFamilyArchive, Unit: "BAU", NoPlan: true, Priced: !c.Cloud18MarketplaceBAUClientStorage, UnitPrice: pr.BAU}
	if b := cluster.BackupArchiveUnits; b != nil {
		ar.Billable, ar.Priced = float64(b.BilledUnits), b.Priced
		if b.UnitPrice > 0 {
			ar.UnitPrice = b.UnitPrice
		}
	}
	out = append(out, st, co, bk, ar)
	if gw, ok := cluster.gatewayUsage(pr); ok {
		out = append(out, gw)
	}
	return out
}

// BillingIdentity is who pays whom: the infrastructure's Cloud18 identity and the
// identities holding the sponsor role on the cluster (emails for SSO).
func (cluster *Cluster) BillingIdentity() ClusterIdentity {
	id := ClusterIdentity{Sponsors: []string{}}
	if c := cluster.Conf; c.Cloud18 && c.Cloud18Domain != "" {
		id.Partner = c.Cloud18Domain + "/" + c.Cloud18SubDomain + "-" + c.Cloud18SubDomainZone
	}
	for name, u := range cluster.APIUsers {
		if u.Roles[config.RoleSponsor] {
			id.Sponsors = append(id.Sponsors, name)
		}
	}
	sort.Strings(id.Sponsors)
	return id
}

// pushBillingUsage is the per-tick feed; returns the metrics to send with the batch.
func (cluster *Cluster) pushBillingUsage(now time.Time) []graphite.Metric {
	if cluster.resources == nil {
		return nil
	}
	return cluster.resources.RecordUsage(cluster.Name, cluster.BillingIdentity(), cluster.BillingUsage(), now)
}
