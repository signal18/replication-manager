// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2268: hiding the clusters for sale is for paying customers: a Support or a Support and
// Services plan, or a licence; the free, developer and partner plans see what the marketplace sells.
func TestCloud18ForSaleOptOutAllowed(t *testing.T) {
	for plan, want := range map[string]bool{"support": true, "support-services": true, "free": false, "developer": false, "partner": false, "": false} {
		if got := (&Config{Cloud18SubscriptionPlan: plan}).Cloud18ForSaleOptOutAllowed(); got != want {
			t.Errorf("plan %q: %v, want %v", plan, got, want)
		}
	}
	if !(&Config{Cloud18SubscriptionPlan: "free", Cloud18LicenseFile: "licence.tar.gz"}).Cloud18ForSaleOptOutAllowed() {
		t.Error("a licence allows it whatever the plan")
	}
}
