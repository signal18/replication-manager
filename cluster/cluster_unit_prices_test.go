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
