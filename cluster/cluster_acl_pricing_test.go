package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// The metering/pricing settings are the provider's: app-config (what a sponsor holds)
// does not grant them, sales-pricing does, and sizing stays on app-config.
func TestACL_PricingSettingsRequireSalesPricing(t *testing.T) {
	cl := setupACLTestCluster()
	cl.APIUsers["sponsor"] = APIUser{User: "sponsor", Grants: map[string]bool{config.GrantAppConfig: true, config.GrantClusterSettings: true}}
	cl.APIUsers["provider"] = APIUser{User: "provider", Grants: map[string]bool{config.GrantSalesPricing: true}}

	base := "/api/clusters/" + cl.Name + "/apps/ap1/settings/actions/set/"
	for _, u := range []string{base + "app-stateful/true", base + "app-s3-provider/true",
		"/api/clusters/" + cl.Name + "/settings/actions/switch/cloud18-marketplace-bau-client-storage"} {
		if cl.IsURLPassACL("sponsor", u, false) {
			t.Fatalf("app-config/cluster-settings must NOT grant the metering rule: %s", u)
		}
		if !cl.IsURLPassACL("provider", u, false) {
			t.Fatalf("sales-pricing must grant the metering rule: %s", u)
		}
	}
	// Sizing is the owner's right: unchanged.
	if !cl.IsURLPassACL("sponsor", base+"prov-app-units/2", false) {
		t.Fatalf("app-config must still grant sizing (prov-app-units)")
	}
	if cl.IsURLPassACL("provider", base+"prov-app-units/2", false) {
		t.Fatalf("sales-pricing alone must not grant sizing")
	}
	// Role defaults: the owner roles never get it, sysops does.
	for _, role := range []string{config.RoleSponsor, config.RoleExtSysOps, config.RoleDBOps, config.RoleExtDBOps} {
		for _, w := range strings.Fields(config.GetDefaultGrants(role)) {
			if w == config.GrantSalesPricing || w == "sales" || w == "*" {
				t.Fatalf("role %s defaults must not carry %s", role, w)
			}
		}
	}
	// GetDefaultGrants is the COMPACT form: a complete family collapses to its word.
	has := false
	for _, w := range strings.Fields(config.GetDefaultGrants(config.RoleSysOps)) {
		if w == config.GrantSalesPricing || w == "sales" || w == "*" {
			has = true
		}
	}
	if !has {
		t.Fatalf("sysops defaults must carry %s (or the whole sales family)", config.GrantSalesPricing)
	}
}
