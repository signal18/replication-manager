package cluster

import (
	"testing"
	"time"
)

// A stateful app's plan is reserved in the DBU pool with all four physical axes, and
// never counted as APU.
func TestLedgerStatefulAppReservesDBU(t *testing.T) {
	m := ledgerFixture(t)
	before := m.Ledger()
	now := time.Now()
	dbu := m.Ratios(ProfileDatabase)
	r := m.ComputeUsedDBU(now, now, int64(2*dbu.MemMBPerUnit)*1024*1024, 2*dbu.CoresPerUnit, 0, int64(2*dbu.DiskGBPerUnit)*1024*1024*1024)
	m.SetStatefulPlan(AppKey{Cluster: "crm", App: "minio", Kind: KindApp}, &r)
	l := m.Ledger()
	if l.ReservedUnits.Dbu != before.ReservedUnits.Dbu+2 || l.ReservedUnits.Apu != before.ReservedUnits.Apu {
		t.Fatalf("reserved units = %+v (before %+v), want +2 DBU, APU unchanged", l.ReservedUnits, before.ReservedUnits)
	}
	if l.Reserved.Cores != before.Reserved.Cores+2 {
		t.Fatalf("reserved cores = %v, want %v", l.Reserved.Cores, before.Reserved.Cores+2)
	}
	if got := l.Reserved.DiskBytes - before.Reserved.DiskBytes; got != 40*1024*1024*1024 {
		t.Fatalf("reserved disk delta = %v, want 40 GB", got)
	}
	m.SetStatefulPlan(AppKey{Cluster: "crm", App: "minio", Kind: KindApp}, nil)
	if l2 := m.Ledger(); l2.ReservedUnits.Dbu != before.ReservedUnits.Dbu {
		t.Fatalf("clearing the stateful plan must release it: %+v", l2.ReservedUnits)
	}
}
