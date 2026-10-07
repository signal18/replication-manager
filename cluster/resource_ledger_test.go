package cluster

import (
	"github.com/signal18/replication-manager/config"
	"testing"
	"time"
)

// Preprod-like ledger: 64 cores / 576 GB / 4050 GB / 60000 iops, quota 40 %; plans = 20 DBU
// (4 clusters), 41 APU, 24 BKU; borrowed = 3 cores of DB config over plan. The plan pot is
// sellable minus every plan; the over-commit pot is capacity minus plans minus borrows.
func ledgerFixture(t *testing.T) *ResourceManager {
	t.Helper()
	m := NewResourceManager()
	m.SetQuotaPct(40)
	m.SetInfraCapacity(&AgentCapacity{Cores: 64, MemMB: 576 * 1024, DiskGB: 4050, Iops: 60000})
	now := time.Now()
	dbu := m.Ratios(ProfileDatabase)
	for i, n := range []int{6, 6, 4, 4} { // 20 DBU as per-server plans (one server holding the cluster plan)
		r := m.ComputeUsedDBU(now, now, int64(float64(n)*dbu.MemMBPerUnit)*1024*1024, float64(n)*dbu.CoresPerUnit, float64(n)*dbu.IopsPerUnit, int64(float64(n)*dbu.DiskGBPerUnit)*1024*1024*1024)
		m.SetPlan(ResourceKey{Cluster: []string{"curepipe", "flacq", "belair", "crm"}[i], Server: "db1"}, &r)
	}
	apu := m.Ratios(ProfileCompute)
	for i, n := range []int{7, 4, 6, 24} { // 41 APU
		r := m.ComputeUsedAPU(now, now, int64(float64(n)*apu.MemMBPerUnit)*1024*1024, float64(n)*apu.CoresPerUnit, int64(float64(n)*apu.DiskGBPerUnit)*1024*1024*1024)
		m.SetAppPlan(AppKey{Cluster: []string{"curepipe", "flacq", "belair", "crm"}[i], App: "all", Kind: KindApp}, &r)
	}
	for _, c := range []string{"curepipe", "flacq", "belair", "crm"} {
		m.SetStoragePlan(c, 6) // 24 BKU
	}
	m.SetBorrowed("curepipe", "db", PhysicalUsage{CpuCores: 3, MemBytes: 12 * 1024 * 1024 * 1024})
	return m
}

func TestLedgerPots(t *testing.T) {
	m := ledgerFixture(t)
	l := m.Ledger()
	if !l.Known || l.QuotaPct != 40 {
		t.Fatalf("ledger unknown or quota %v", l.QuotaPct)
	}
	if l.ReservedUnits.Dbu != 20 || l.ReservedUnits.Apu != 41 || l.ReservedUnits.Bku != 24 {
		t.Fatalf("reserved units = %+v, want 20/41/24", l.ReservedUnits)
	}
	// cores: reserved 20 + 41 = 61; sellable 25.6 -> plan pot -35.4; over-commit pot 64-61-3 = 0.
	if l.Reserved.Cores != 61 || l.Sellable.Cores != 25.6 {
		t.Fatalf("cores reserved=%v sellable=%v", l.Reserved.Cores, l.Sellable.Cores)
	}
	if got := l.PlanPot.Cores; got < -35.41 || got > -35.39 {
		t.Fatalf("plan pot cores = %v, want -35.4", got)
	}
	if got := l.OverCommitPot.Cores; got < -1e-9 || got > 1e-9 {
		t.Fatalf("over-commit pot cores = %v, want 0", got)
	}
	// disk: reserved 20x20 + 41x10 + 24x20 = 400+410+480 = 1290 GB; sellable 1620 -> plan pot 330 GB.
	if got := l.PlanPot.DiskBytes / 1024 / 1024 / 1024; got < 329.9 || got > 330.1 {
		t.Fatalf("plan pot disk = %v GB, want 330", got)
	}
	// Unit pots are the smallest axis: cores bind everything with a cpu ratio.
	if l.PlanPotUnits.Dbu > -35.3 || l.PlanPotUnits.Apu > -35.3 {
		t.Fatalf("plan pot units = %+v, want cpu-bound negative", l.PlanPotUnits)
	}
	if got := l.PlanPotUnits.Bku; got < 16.4 || got > 16.6 {
		t.Fatalf("plan pot BKU = %v, want 16.5 (330 GB / 20)", got)
	}
	if l.Overdrawn {
		t.Fatalf("pot at exactly 0 is not overdrawn")
	}
}

// Gates: a plan increase is refused when it does not fit the plan pot even if capacity is
// idle; a borrow is refused when the over-commit pot is exhausted; a plan increase admitted
// later drives the over-commit pot negative (precedence) and the ledger says so.
func TestLedgerGates(t *testing.T) {
	m := ledgerFixture(t)
	if ok, why := m.CanPlanIncrease(ProfileDatabase, 1); ok {
		t.Fatalf("plan pot is negative on cpu, a DBU sale must be refused: %s", why)
	}
	if ok, _ := m.CanPlanIncrease(ProfileStorage, 16); !ok {
		t.Fatalf("16 BKU fit the 330 GB plan pot on disk")
	}
	if ok, _ := m.CanPlanIncrease(ProfileStorage, 17); ok {
		t.Fatalf("17 BKU (340 GB) do not fit the 330 GB plan pot")
	}
	if ok, _ := m.CanBorrow(ProfileDatabase, 0.5); ok {
		t.Fatalf("over-commit pot is 0 cores, a borrow must be refused")
	}
	m.SetBorrowed("curepipe", "db", PhysicalUsage{}) // give the 3 cores back
	if ok, why := m.CanBorrow(ProfileDatabase, 2); !ok {
		t.Fatalf("3 cores unreserved, 2 DBU borrow must pass: %s", why)
	}
	if ok, _ := m.CanBorrow(ProfileDatabase, 4); ok {
		t.Fatalf("4 DBU exceed the 3 unreserved cores")
	}
	// Precedence: a borrow of 3, then the metal is sold (quota 100): the over-commit pot goes negative.
	m.SetBorrowed("crm", "db", PhysicalUsage{CpuCores: 3})
	m.SetQuotaPct(100)
	now := time.Now()
	r := m.ComputeUsedDBU(now, now, 4*4096*1024*1024, 4, 4000, 4*20*1024*1024*1024)
	m.SetPlan(ResourceKey{Cluster: "new", Server: "db1"}, &r)
	l := m.Ledger()
	if !l.Overdrawn || l.OverCommitPot.Cores >= 0 {
		t.Fatalf("after the sale the borrowed part must give way: overdrawn=%v pot=%v", l.Overdrawn, l.OverCommitPot.Cores)
	}
	// Unknown capacity never gates.
	m.SetInfraCapacity(nil)
	if ok, _ := m.CanPlanIncrease(ProfileDatabase, 100); !ok {
		t.Fatalf("unknown capacity cannot gate")
	}
}

// An overdrawn ledger (plans sold beyond the capacity) refuses a PLAN increase but never a
// RESOURCE grow above the plan: the grow answers to the overcommit envelope and the node's
// free pool, not to the sum of plans sold (#1905).
func TestOverdrawnLedgerDoesNotGateResourceGrow(t *testing.T) {
	m := ledgerFixture(t)
	if ok, _ := m.CanBorrow(ProfileDatabase, 0.5); ok {
		t.Fatalf("fixture: the over-commit pot must be exhausted for the test to mean anything")
	}
	cl := &Cluster{Name: "t", resources: m, Conf: &config.Config{ProvDbDbu: 2, ProvDBOvercommitPct: 50}}
	cl.Servers = []*ServerMonitor{{URL: "db:3306", ClusterGroup: cl}} // no agent: the node pool check is skipped
	if ok, why := cl.resourceManagerGrowCheck(cl.Servers[0], 2.3); !ok {
		t.Fatalf("a 0.3 DBU grow over a 2 DBU plan must not be refused by the ledger: %s", why)
	}
	if ok, _ := cl.resourceManagerGrowCheck(cl.Servers[0], 3.5); ok {
		t.Fatalf("the overcommit envelope (2 x 1.5 = 3) still gates")
	}
}
