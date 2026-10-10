package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The prices belong to the ResourceManager: a cluster whose copy of the server-scope
// settings is stale (or frozen in its own file) still bills the instance's price list.
func TestClusterPricesFromResourceManager(t *testing.T) {
	c := &Cluster{Name: "alcudia", Conf: &config.Config{Cloud18MarketplaceDBUPrice: 12, Cloud18MarketplaceAPUPrice: 6.67,
		Cloud18MarketplaceBKUPrice: 0, Cloud18MarketplaceBAUPrice: 0}}
	if p := c.unitPrices(); p.DBU != 12 || p.APU != 6.67 {
		t.Fatalf("without a manager the cluster copy is read: %+v", p)
	}
	rm := NewResourceManager()
	c.SetResourceManager(rm)
	if p := c.unitPrices(); p.DBU != 12 {
		t.Fatalf("a manager that never received its list must not price with zeros: %+v", p)
	}
	rm.SetPrices(BillingPrices{DBU: 14, APU: 11, BKU: 5, BAU: 2, OverPct: 150, UnderPct: 80})
	c.SetResourceManager(rm)
	if p := c.unitPrices(); p.DBU != 14 || p.APU != 11 || p.BKU != 5 || p.BAU != 2 || p.OverPct != 150 {
		t.Fatalf("the manager's price list must win over the cluster copy: %+v", p)
	}
	if got := c.bauUnitPrice(); got != 2 {
		t.Fatalf("BAU price = %v, want the manager's 2", got)
	}
	// a price change on the instance applies at once, no reload of the cluster
	rm.SetPrices(BillingPrices{DBU: 16, APU: 13})
	if p := c.unitPrices(); p.DBU != 16 || p.APU != 13 {
		t.Fatalf("live price change not seen: %+v", p)
	}
}

// The regression itself: the lines the ledger records carry the manager's prices, not the
// cluster copy frozen in its file (12 / 6.67 on RapidSpace, 2026-10-10).
func TestBillingUsageCarriesManagerPrices(t *testing.T) {
	c := &Cluster{Name: "alcudia", Conf: &config.Config{Cloud18MarketplaceDBUPrice: 12, Cloud18MarketplaceAPUPrice: 6.67,
		Cloud18MarketplaceBKUPrice: 30, Cloud18MarketplaceBAUPrice: 10, Cloud18MarketplaceGWUPrice: 9, ProvDbDbu: 2}}
	rm := NewResourceManager()
	rm.SetPrices(BillingPrices{DBU: 14, APU: 11, BKU: 5, BAU: 2, GWU: 1, OverPct: 150, UnderPct: 80})
	c.SetResourceManager(rm)
	want := map[string]float64{BillingFamilyDatabase: 14, BillingFamilyStateful: 14, BillingFamilyCompute: 11, BillingFamilyBackup: 5, BillingFamilyArchive: 2, BillingFamilyGateway: 1}
	seen := 0
	for _, u := range c.BillingUsage() {
		w, ok := want[u.Family]
		if !ok {
			continue
		}
		seen++
		if u.UnitPrice != w {
			t.Errorf("%s line priced %v, want the manager's %v", u.Family, u.UnitPrice, w)
		}
		if u.Family != BillingFamilyArchive && u.Family != BillingFamilyGateway && u.OverPct != 150 {
			t.Errorf("%s line over-commit %d, want the manager's 150", u.Family, u.OverPct)
		}
	}
	if seen != len(want) {
		t.Fatalf("saw %d priced families, want %d", seen, len(want))
	}
}
