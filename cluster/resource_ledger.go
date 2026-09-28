// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"fmt"
	"math"
)

// The physical ledger (Stéphane 2026-09-28): DBU, APU and BKU are strongly correlated, they
// are the SAME metal projected through three ratios, so the free pot of one unit must be
// computed by subtracting what the OTHER units already hold. Two pots per axis, because a
// plan is a GUARANTEE and a borrowed resource is not:
//
//   plan pot        = capacity × quota − Σ plans (every cluster, every unit)
//                     what a PLAN INCREASE may still be admitted against; consumption plays
//                     no role, only other plans can bind a plan;
//   over-commit pot = capacity − Σ plans − Σ borrowed
//                     what an EXISTING plan may still grow into beyond itself: the space
//                     above the quota that no plan owns, lent, never sold.
//
// PRECEDENCE (documented rule): a plan increase always wins over borrowed resources. A plan
// admitted into the plan pot may drive the over-commit pot negative; the borrowed part must
// then give way (no further borrow is admitted, ProduceLedgerState raises the reclaim
// warning on the clusters that borrowed, and the dynamic driver's shrink path brings them
// back toward their plan). Neither pot ever reads consumption.
//
// Per unit, a pot is the smallest of its axes after the ratio (an axis with ratio 0 or an
// unknown capacity is excluded). Disk here is the NVMe class (DBU disk, backups, app disk);
// the archive class (BAU) has no capacity source yet, so no BAU pot is derived.

// LedgerAxes is a signed physical quantity per axis (a pot may be negative once a plan
// increase took precedence over borrowed resources).
type LedgerAxes struct {
	Cores     float64 `json:"cores"`
	MemBytes  float64 `json:"memBytes"`
	Iops      float64 `json:"iops"`
	DiskBytes float64 `json:"diskBytes"`
}

func (a LedgerAxes) sub(b LedgerAxes) LedgerAxes {
	return LedgerAxes{Cores: a.Cores - b.Cores, MemBytes: a.MemBytes - b.MemBytes, Iops: a.Iops - b.Iops, DiskBytes: a.DiskBytes - b.DiskBytes}
}

func (a LedgerAxes) scale(f float64) LedgerAxes {
	return LedgerAxes{Cores: a.Cores * f, MemBytes: a.MemBytes * f, Iops: a.Iops * f, DiskBytes: a.DiskBytes * f}
}

func axesOf(p PhysicalUsage) LedgerAxes {
	return LedgerAxes{Cores: p.CpuCores, MemBytes: float64(p.MemBytes), Iops: p.IoIops, DiskBytes: float64(p.DiskBytes)}
}

// UnitPots is a pot projected into the three units sharing the metal.
type UnitPots struct {
	Dbu float64 `json:"dbu"`
	Apu float64 `json:"apu"`
	Bku float64 `json:"bku"`
}

// ResourceLedger is the infrastructure-wide physical ledger and its two pots.
type ResourceLedger struct {
	Known         bool       `json:"known"`
	QuotaPct      float64    `json:"quotaPct"`
	Capacity      LedgerAxes `json:"capacity"`      // the metal (agents summed, config override winning)
	Sellable      LedgerAxes `json:"sellable"`      // capacity × quota: what plans may add up to
	Reserved      LedgerAxes `json:"reserved"`      // Σ plans of every cluster, DBU + APU + BKU, physical
	Borrowed      LedgerAxes `json:"borrowed"`      // Σ resources granted above the plans (DB config over plan, BKU usage over plan)
	PlanPot       LedgerAxes `json:"planPot"`       // sellable − reserved
	OverCommitPot LedgerAxes `json:"overCommitPot"` // capacity − reserved − borrowed
	PlanPotUnits  UnitPots   `json:"planPotUnits"`  // plan pot per unit (smallest axis)
	BorrowPot     UnitPots   `json:"borrowPot"`     // over-commit pot per unit (smallest axis)
	ReservedUnits UnitPots   `json:"reservedUnits"` // Σ plans per unit, as sold (DBU, APU, BKU)
	Overdrawn     bool       `json:"overdrawn"`     // an over-commit pot axis is negative: borrowed resources must give way
}

// SetInfraCapacity records the infrastructure's physical capacity (the server assembles it
// from the agents and the resource-manager-infra-* overrides). nil clears it.
func (m *ResourceManager) SetInfraCapacity(c *AgentCapacity) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.infraCapacity = c
}

// SetStoragePlan records a cluster's BKU plan (prov-db-bku), in units.
func (m *ResourceManager) SetStoragePlan(clusterName string, units int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if units <= 0 {
		delete(m.storagePlan, clusterName)
		return
	}
	m.storagePlan[clusterName] = units
}

// SetBorrowed records the physical resources one cluster holds ABOVE its plan on one track
// ("db": the configured DB resources over the DBU plan; "bku": backup usage over the BKU
// plan). A zero usage clears the entry.
func (m *ResourceManager) SetBorrowed(clusterName, track string, p PhysicalUsage) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := clusterName + "|" + track
	if p == (PhysicalUsage{}) {
		delete(m.borrowed, key)
		return
	}
	m.borrowed[key] = p
}

// BorrowedByCluster is what a cluster holds above its plans, every track summed.
func (m *ResourceManager) BorrowedByCluster(clusterName string) PhysicalUsage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var p PhysicalUsage
	for k, v := range m.borrowed {
		if len(k) > len(clusterName) && k[:len(clusterName)+1] == clusterName+"|" {
			p.add(v)
		}
	}
	return p
}

// unitAxes is one unit of a profile in physical terms.
func (m *ResourceManager) unitAxes(profile WorkloadProfile) LedgerAxes {
	r := m.ratios[profile]
	return LedgerAxes{Cores: r.CoresPerUnit, MemBytes: r.MemMBPerUnit * 1024 * 1024, Iops: r.IopsPerUnit, DiskBytes: r.DiskGBPerUnit * 1024 * 1024 * 1024}
}

// potUnits projects a physical pot into units of a profile: the smallest axis after the
// ratio, over the axes the profile uses and the capacity knows (capacity > 0).
func potUnits(pot, capacity, unit LedgerAxes) float64 {
	best := math.Inf(1)
	consider := func(p, c, u float64) {
		if u > 0 && c > 0 && p/u < best {
			best = p / u
		}
	}
	consider(pot.Cores, capacity.Cores, unit.Cores)
	consider(pot.MemBytes, capacity.MemBytes, unit.MemBytes)
	consider(pot.Iops, capacity.Iops, unit.Iops)
	consider(pot.DiskBytes, capacity.DiskBytes, unit.DiskBytes)
	if math.IsInf(best, 1) {
		return 0
	}
	return best
}

// Ledger computes the physical ledger from the manager's plans, borrows and capacity.
func (m *ResourceManager) Ledger() ResourceLedger {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var l ResourceLedger
	if m.infraCapacity == nil {
		return l
	}
	cap := axesOf(m.infraCapacity.Physical())
	if cap.Cores <= 0 && cap.MemBytes <= 0 && cap.DiskBytes <= 0 {
		return l
	}
	l.Known = true
	l.QuotaPct = m.quotaPct
	q := m.quotaPct / 100
	if q <= 0 || q > 1 {
		q = 1
	}
	l.Capacity = cap
	l.Sellable = cap.scale(q)

	var reserved, borrowed LedgerAxes
	for _, r := range m.plan {
		if r != nil {
			p := axesOf(r.Physical())
			reserved.Cores += p.Cores
			reserved.MemBytes += p.MemBytes
			reserved.Iops += p.Iops
			reserved.DiskBytes += p.DiskBytes
			l.ReservedUnits.Dbu += r.Dbu
		}
	}
	for _, r := range m.appPlan {
		if r != nil {
			p := axesOf(r.Physical())
			reserved.Cores += p.Cores
			reserved.MemBytes += p.MemBytes
			reserved.DiskBytes += p.DiskBytes
			l.ReservedUnits.Apu += r.Apu
		}
	}
	bkuUnit := m.unitAxes(ProfileStorage)
	for _, u := range m.storagePlan {
		reserved.DiskBytes += float64(u) * bkuUnit.DiskBytes
		l.ReservedUnits.Bku += float64(u)
	}
	for _, p := range m.borrowed {
		b := axesOf(p)
		borrowed.Cores += b.Cores
		borrowed.MemBytes += b.MemBytes
		borrowed.Iops += b.Iops
		borrowed.DiskBytes += b.DiskBytes
	}
	l.Reserved, l.Borrowed = reserved, borrowed
	l.PlanPot = l.Sellable.sub(reserved)
	l.OverCommitPot = cap.sub(reserved).sub(borrowed)

	dbu, apu := m.unitAxes(ProfileDatabase), m.unitAxes(ProfileCompute)
	l.PlanPotUnits = UnitPots{Dbu: potUnits(l.PlanPot, cap, dbu), Apu: potUnits(l.PlanPot, cap, apu), Bku: potUnits(l.PlanPot, cap, bkuUnit)}
	l.BorrowPot = UnitPots{Dbu: potUnits(l.OverCommitPot, cap, dbu), Apu: potUnits(l.OverCommitPot, cap, apu), Bku: potUnits(l.OverCommitPot, cap, bkuUnit)}
	l.Overdrawn = (cap.Cores > 0 && l.OverCommitPot.Cores < 0) || (cap.MemBytes > 0 && l.OverCommitPot.MemBytes < 0) ||
		(cap.Iops > 0 && l.OverCommitPot.Iops < 0) || (cap.DiskBytes > 0 && l.OverCommitPot.DiskBytes < 0)
	return l
}

// fits reports whether a physical request fits a pot on every axis the capacity knows.
func fits(req, pot, capacity LedgerAxes) (bool, string) {
	check := func(r, p, c float64, axis, unit string) string {
		if c > 0 && r > 0 && r > p+1e-9 {
			return fmt.Sprintf("%s: %.2f %s asked, %.2f free", axis, r, unit, p)
		}
		return ""
	}
	if s := check(req.Cores, pot.Cores, capacity.Cores, "cpu", "cores"); s != "" {
		return false, s
	}
	if s := check(req.MemBytes/1024/1024, pot.MemBytes/1024/1024, capacity.MemBytes, "mem", "MB"); s != "" {
		return false, s
	}
	if s := check(req.Iops, pot.Iops, capacity.Iops, "io", "iops"); s != "" {
		return false, s
	}
	if s := check(req.DiskBytes/1024/1024/1024, pot.DiskBytes/1024/1024/1024, capacity.DiskBytes, "disk", "GB"); s != "" {
		return false, s
	}
	return true, ""
}

// CanPlanIncrease is the PLAN gate: may `units` more units of a profile be sold? They must
// fit the plan pot, sellable minus every plan already sold, on every axis. An unknown
// capacity cannot gate (true). Consumption is never consulted: a plan is a guarantee and
// only other guarantees can bind it.
func (m *ResourceManager) CanPlanIncrease(profile WorkloadProfile, units float64) (bool, string) {
	l := m.Ledger()
	if !l.Known || units <= 0 {
		return true, ""
	}
	m.mu.RLock()
	req := m.unitAxes(profile).scale(units)
	m.mu.RUnlock()
	if ok, why := fits(req, l.PlanPot, l.Capacity); !ok {
		return false, fmt.Sprintf("no room in the plan pot for %.0f more %s: %s (sellable = capacity × %.0f%% quota, minus every plan sold)", units, profile, why, l.QuotaPct)
	}
	return true, ""
}

// CanBorrow is the OVER-COMMIT gate: may `units` more units of a profile be granted ABOVE a
// plan? They must fit the over-commit pot, capacity minus every plan minus everything
// already borrowed. An unknown capacity cannot gate (true). A plan increase admitted later
// takes precedence: the borrow is a loan, not a sale.
func (m *ResourceManager) CanBorrow(profile WorkloadProfile, units float64) (bool, string) {
	l := m.Ledger()
	if !l.Known || units <= 0 {
		return true, ""
	}
	m.mu.RLock()
	req := m.unitAxes(profile).scale(units)
	m.mu.RUnlock()
	if ok, why := fits(req, l.OverCommitPot, l.Capacity); !ok {
		return false, fmt.Sprintf("no unreserved capacity to borrow %.2f %s: %s (capacity minus every plan minus what is already borrowed; plans take precedence)", units, profile, why)
	}
	return true, ""
}
