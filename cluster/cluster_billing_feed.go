// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"math"
	"sort"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/graphite"
)

// The cluster's side of billing: it knows its plans and what it really holds per unit
// family, and who sponsors it; it pushes that to the ResourceManager every tick, which
// prices it and keeps the month statement (resource_manager_billing.go).

func unitPivot(p PhysicalUsage, r UnitRatios) float64 {
	v := 0.0
	if r.CoresPerUnit > 0 {
		v = math.Max(v, p.CpuCores/r.CoresPerUnit)
	}
	if r.MemMBPerUnit > 0 {
		v = math.Max(v, float64(p.MemBytes)/1024/1024/r.MemMBPerUnit)
	}
	if r.IopsPerUnit > 0 {
		v = math.Max(v, p.IoIops/r.IopsPerUnit)
	}
	if r.DiskGBPerUnit > 0 {
		v = math.Max(v, float64(p.DiskBytes)/1024/1024/1024/r.DiskGBPerUnit)
	}
	return v
}

// BillingUsage is the plan and the billable units of the five families at this tick: the
// database plan against what the servers hold (config DBU when provisioned, plan +
// borrowed never less), and the four unit objects the cluster already maintains.
func (cluster *Cluster) BillingUsage() []UnitUsage {
	plan := float64(cluster.GetPlanDbu())
	billable := plan
	if cluster.IsProvision && len(cluster.Servers) > 0 {
		billable = cluster.GetConfigDBUPerNode().Dbu * float64(len(cluster.Servers))
		if cluster.resources != nil {
			if b := cluster.resources.BorrowedByCluster(cluster.Name); b != (PhysicalUsage{}) {
				billable = math.Max(billable, plan+unitPivot(b, cluster.resources.Ratios(ProfileDatabase)))
			}
		}
	}
	out := []UnitUsage{{Family: BillingFamilyDatabase, Unit: "DBU", Plan: plan, Billable: billable, Priced: true}}
	st := UnitUsage{Family: BillingFamilyStateful, Unit: "DBU", Priced: true}
	if b := cluster.StatefulUnits; b != nil {
		st.Plan, st.Billable = float64(b.Plan), float64(b.BillableUnits)
	}
	co := UnitUsage{Family: BillingFamilyCompute, Unit: "APU", Priced: true}
	if b := cluster.ComputeUnits; b != nil {
		co.Plan, co.Billable = float64(b.Plan), float64(b.BillableUnits)
	}
	bk := UnitUsage{Family: BillingFamilyBackup, Unit: "BKU", Priced: true}
	if b := cluster.BackupUnits; b != nil {
		bk.Plan, bk.Billable = float64(b.Plan), float64(b.BilledUnits)
	}
	ar := UnitUsage{Family: BillingFamilyArchive, Unit: "BAU"}
	if b := cluster.BackupArchiveUnits; b != nil {
		ar.Billable, ar.Priced = float64(b.BilledUnits), b.Priced
	}
	return append(out, st, co, bk, ar)
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
