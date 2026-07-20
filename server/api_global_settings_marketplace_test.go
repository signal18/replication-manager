// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// --- setRepmanSetting: cloud18-marketplace-pricing-mode / prices (Section A1) ---

func TestSetRepmanSetting_MarketplacePricingMode(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    string
	}{
		{"csv-service-plan accepted", config.ConstMarketplacePricingModeCsvServicePlan, false, config.ConstMarketplacePricingModeCsvServicePlan},
		{"global-unit-pricing accepted", config.ConstMarketplacePricingModeGlobalUnitPricing, false, config.ConstMarketplacePricingModeGlobalUnitPricing},
		{"invalid value rejected", "bogus-mode", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
				ConfigManager: newConfigManagerForTest(),
			}
			err := repman.setRepmanSetting("cloud18-marketplace-pricing-mode", tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q, got nil", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repman.Conf.Cloud18MarketplacePricingMode != tc.want {
				t.Errorf("Cloud18MarketplacePricingMode = %q, want %q", repman.Conf.Cloud18MarketplacePricingMode, tc.want)
			}
		})
	}
}

func TestSetRepmanSetting_MarketplaceDBUPrice(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    float64
	}{
		{"positive decimal accepted", "12.5", false, 12.5},
		{"zero accepted", "0", false, 0},
		{"negative rejected", "-1", true, 0},
		{"non-numeric rejected", "abc", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
				ConfigManager: newConfigManagerForTest(),
			}
			err := repman.setRepmanSetting("cloud18-marketplace-dbu-price", tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q, got nil", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repman.Conf.Cloud18MarketplaceDBUPrice != tc.want {
				t.Errorf("Cloud18MarketplaceDBUPrice = %v, want %v", repman.Conf.Cloud18MarketplaceDBUPrice, tc.want)
			}
		})
	}
}

func TestSetRepmanSetting_MarketplaceAppUnitPrice(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    float64
	}{
		{"positive decimal accepted", "3.25", false, 3.25},
		{"negative rejected", "-0.01", true, 0},
		{"non-numeric rejected", "free", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
				ConfigManager: newConfigManagerForTest(),
			}
			err := repman.setRepmanSetting("cloud18-marketplace-app-unit-price", tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q, got nil", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repman.Conf.Cloud18MarketplaceAppUnitPrice != tc.want {
				t.Errorf("Cloud18MarketplaceAppUnitPrice = %v, want %v", repman.Conf.Cloud18MarketplaceAppUnitPrice, tc.want)
			}
		})
	}
}

// --- Per-cluster plans are NOT suppressed by global-unit-pricing mode.
//
// Earlier iterations of this feature blocked prov-service-plan and the reload-plan
// endpoints whenever cloud18-marketplace-pricing-mode was global-unit-pricing, on
// the theory that "no plan per cluster" meant plans must be disallowed outright.
// That was wrong: SetServicePlan bundles two unrelated things — provisioning sizing
// (cores/mem/disk/iops) and legacy CSV cost fields (Cloud18MonthlyInfraCost etc.).
// Blocking it also blocked the sizing convenience, which nobody asked for. The
// price the BO reads in unit-pricing mode already comes from ComputeDatabaseUnits/
// ComputeApplicationUnits (see cluster/cluster_marketplace_units_test.go), which
// read cluster.Conf.ProvCores/ProvMem/ProvDisk/ProvIops and
// Cloud18ApplicationCreditsUsed directly — never the plan's cost fields — so plans
// stay fully usable for sizing in both pricing modes. These tests guard against
// that guard ever coming back.

func TestSetClusterSetting_ProvServicePlan_SameBehaviorRegardlessOfPricingMode(t *testing.T) {
	for _, mode := range []string{
		config.ConstMarketplacePricingModeCsvServicePlan,
		config.ConstMarketplacePricingModeGlobalUnitPricing,
	} {
		t.Run(mode, func(t *testing.T) {
			cl := newTestClusterForAPI(t)
			cl.Conf.Secrets = make(map[string]config.Secret)
			cl.ConfigManager = newConfigManagerForTest()
			repman := newTestRepmanWithCluster(t, cl.Name, cl)
			repman.Conf = &config.Config{Cloud18MarketplacePricingMode: mode}

			// setClusterSetting's "prov-service-plan" case discards SetServicePlan's
			// error return (pre-existing, unrelated to pricing mode), so with no plan
			// repository loaded in this fixture the call returns nil in both modes.
			err := repman.setClusterSetting(cl, "prov-service-plan", "some-plan")
			if err != nil {
				t.Fatalf("prov-service-plan errored in mode %q: %v", mode, err)
			}
		})
	}
}

func TestHandlerMuxReloadPlans_SameBehaviorRegardlessOfPricingMode(t *testing.T) {
	for _, mode := range []string{
		config.ConstMarketplacePricingModeCsvServicePlan,
		config.ConstMarketplacePricingModeGlobalUnitPricing,
	} {
		t.Run(mode, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:     &config.Config{Cloud18MarketplacePricingMode: mode},
				Clusters: map[string]*cluster.Cluster{},
			}
			req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/reload-clusters-plans", nil)
			w := httptest.NewRecorder()
			repman.handlerMuxReloadPlans(w, req)
			// No clusters registered: must fail with "No cluster" (500) in both
			// modes, never a mode-driven 409.
			if w.Code != http.StatusInternalServerError {
				t.Errorf("mode %q: expected 500 (no cluster), got %d", mode, w.Code)
			}
		})
	}
}

func TestHandlerMuxReloadPlansInfo_SameBehaviorRegardlessOfPricingMode(t *testing.T) {
	for _, mode := range []string{
		config.ConstMarketplacePricingModeCsvServicePlan,
		config.ConstMarketplacePricingModeGlobalUnitPricing,
	} {
		t.Run(mode, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:     &config.Config{Cloud18MarketplacePricingMode: mode},
				Clusters: map[string]*cluster.Cluster{},
			}
			req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/reload-clusters-plan-info", nil)
			w := httptest.NewRecorder()
			repman.handlerMuxReloadPlansInfo(w, req)
			if w.Code != http.StatusInternalServerError {
				t.Errorf("mode %q: expected 500 (no cluster), got %d", mode, w.Code)
			}
		})
	}
}

func TestHandlerMuxReloadPlanInfo_SameBehaviorRegardlessOfPricingMode(t *testing.T) {
	for _, mode := range []string{
		config.ConstMarketplacePricingModeCsvServicePlan,
		config.ConstMarketplacePricingModeGlobalUnitPricing,
	} {
		t.Run(mode, func(t *testing.T) {
			cl := newTestClusterForAPI(t)
			repman := newTestRepmanWithCluster(t, cl.Name, cl)
			repman.Conf = &config.Config{Cloud18MarketplacePricingMode: mode}

			req := httptest.NewRequest(http.MethodPost, "/api/clusters/"+cl.Name+"/settings/actions/reload-plan-info", nil)
			req = setMuxVars(req, map[string]string{"clusterName": cl.Name})
			w := httptest.NewRecorder()
			repman.handlerMuxReloadPlanInfo(w, req)
			// No valid ACL/auth in this fixture, so it must fail with 403 in both
			// modes, never a mode-driven 409.
			if w.Code != http.StatusForbidden {
				t.Errorf("mode %q: expected 403 (no valid ACL), got %d", mode, w.Code)
			}
		})
	}
}

// --- handlerMuxRecalculateMarketplaceUnits: on-demand clusterstate.json refresh ---

func TestHandlerMuxRecalculateMarketplaceUnits_NoCluster(t *testing.T) {
	repman := &ReplicationManager{
		Conf:     &config.Config{},
		Clusters: map[string]*cluster.Cluster{},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/recalculate-marketplace-units", nil)
	w := httptest.NewRecorder()
	repman.handlerMuxRecalculateMarketplaceUnits(w, req)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 (no cluster), got %d", w.Code)
	}
}

func TestHandlerMuxRecalculateMarketplaceUnits_NoValidACL(t *testing.T) {
	cl := newTestClusterForAPI(t)
	repman := newTestRepmanWithCluster(t, cl.Name, cl)
	repman.Conf = &config.Config{}

	req := httptest.NewRequest(http.MethodPost, "/api/clusters/settings/actions/recalculate-marketplace-units", nil)
	w := httptest.NewRecorder()
	repman.handlerMuxRecalculateMarketplaceUnits(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403 (no valid ACL), got %d", w.Code)
	}
}

// --- setRepmanSetting: global-unit-pricing infrastructure/service info fields ---
//
// These fields (monthly costs, currency, infra description, SLA times,
// promotion %, certifications) are global-only (scope:"server") and mirror the
// legacy per-cluster CSV plan fields (Cloud18MonthlyInfraCost etc.) so an
// operator can declare them once for the whole marketplace instance instead of
// per cluster, per the design in doc — no fan-out into cluster.Conf.

func TestSetRepmanSetting_MarketplaceMonetaryFields(t *testing.T) {
	fields := []struct {
		setting string
		get     func(c *config.Config) float64
	}{
		{"cloud18-marketplace-monthly-infra-cost", func(c *config.Config) float64 { return c.Cloud18MarketplaceMonthlyInfraCost }},
		{"cloud18-marketplace-monthly-license-cost", func(c *config.Config) float64 { return c.Cloud18MarketplaceMonthlyLicenseCost }},
		{"cloud18-marketplace-monthly-sysops-cost", func(c *config.Config) float64 { return c.Cloud18MarketplaceMonthlySysopsCost }},
		{"cloud18-marketplace-monthly-dbops-cost", func(c *config.Config) float64 { return c.Cloud18MarketplaceMonthlyDbopsCost }},
	}
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    float64
	}{
		{"positive decimal accepted", "199.90", false, 199.90},
		{"zero accepted", "0", false, 0},
		{"negative rejected", "-1", true, 0},
		{"non-numeric rejected", "abc", true, 0},
	}
	for _, f := range fields {
		t.Run(f.setting, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					repman := &ReplicationManager{
						Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
						ConfigManager: newConfigManagerForTest(),
					}
					err := repman.setRepmanSetting(f.setting, tc.value)
					if tc.wantErr {
						if err == nil {
							t.Fatalf("expected error for value %q, got nil", tc.value)
						}
						return
					}
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if got := f.get(repman.Conf); got != tc.want {
						t.Errorf("%s = %v, want %v", f.setting, got, tc.want)
					}
				})
			}
		})
	}
}

func TestSetRepmanSetting_MarketplaceStringFields(t *testing.T) {
	fields := []struct {
		setting string
		get     func(c *config.Config) string
	}{
		{"cloud18-marketplace-cost-currency", func(c *config.Config) string { return c.Cloud18MarketplaceCostCurrency }},
		{"cloud18-marketplace-infra-cpu-model", func(c *config.Config) string { return c.Cloud18MarketplaceInfraCPUModel }},
		{"cloud18-marketplace-infra-cpu-freq", func(c *config.Config) string { return c.Cloud18MarketplaceInfraCPUFreq }},
		{"cloud18-marketplace-infra-data-centers", func(c *config.Config) string { return c.Cloud18MarketplaceInfraDataCenters }},
		{"cloud18-marketplace-infra-geo-localizations", func(c *config.Config) string { return c.Cloud18MarketplaceInfraGeoLocalizations }},
		{"cloud18-marketplace-infra-certifications", func(c *config.Config) string { return c.Cloud18MarketplaceInfraCertifications }},
	}
	for _, f := range fields {
		t.Run(f.setting, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
				ConfigManager: newConfigManagerForTest(),
			}
			want := "some value for " + f.setting
			if err := repman.setRepmanSetting(f.setting, want); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := f.get(repman.Conf); got != want {
				t.Errorf("%s = %q, want %q", f.setting, got, want)
			}
		})
	}
}

func TestSetRepmanSetting_MarketplaceSlaAndBandwidthFields(t *testing.T) {
	fields := []struct {
		setting string
		get     func(c *config.Config) float64
	}{
		{"cloud18-marketplace-infra-public-bandwidth", func(c *config.Config) float64 { return c.Cloud18MarketplaceInfraPublicBandwidth }},
		{"cloud18-marketplace-sla-response-time", func(c *config.Config) float64 { return c.Cloud18MarketplaceSlaResponseTime }},
		{"cloud18-marketplace-sla-repair-time", func(c *config.Config) float64 { return c.Cloud18MarketplaceSlaRepairTime }},
		{"cloud18-marketplace-sla-provision-time", func(c *config.Config) float64 { return c.Cloud18MarketplaceSlaProvisionTime }},
	}
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    float64
	}{
		{"positive decimal accepted", "4.5", false, 4.5},
		{"zero accepted", "0", false, 0},
		{"negative rejected", "-1", true, 0},
		{"non-numeric rejected", "abc", true, 0},
	}
	for _, f := range fields {
		t.Run(f.setting, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					repman := &ReplicationManager{
						Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
						ConfigManager: newConfigManagerForTest(),
					}
					err := repman.setRepmanSetting(f.setting, tc.value)
					if tc.wantErr {
						if err == nil {
							t.Fatalf("expected error for value %q, got nil", tc.value)
						}
						return
					}
					if err != nil {
						t.Fatalf("unexpected error: %v", err)
					}
					if got := f.get(repman.Conf); got != tc.want {
						t.Errorf("%s = %v, want %v", f.setting, got, tc.want)
					}
				})
			}
		})
	}
}

func TestSetRepmanSetting_MarketplacePromotionPct(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr bool
		want    float64
	}{
		{"mid-range accepted", "15", false, 15},
		{"zero accepted", "0", false, 0},
		{"hundred accepted", "100", false, 100},
		{"negative rejected", "-1", true, 0},
		{"over 100 rejected", "101", true, 0},
		{"non-numeric rejected", "abc", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
				ConfigManager: newConfigManagerForTest(),
			}
			err := repman.setRepmanSetting("cloud18-marketplace-promotion-pct", tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for value %q, got nil", tc.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repman.Conf.Cloud18MarketplacePromotionPct != tc.want {
				t.Errorf("Cloud18MarketplacePromotionPct = %v, want %v", repman.Conf.Cloud18MarketplacePromotionPct, tc.want)
			}
		})
	}
}

// TestSetRepmanSetting_MarketplaceInfraFields_NoClusterFanOut guards the design
// decision that these global fields are NOT pushed into per-cluster cluster.Conf:
// setting a global marketplace infra field must leave every registered cluster's
// own config (still holding its legacy per-cluster value) untouched.
func TestSetRepmanSetting_MarketplaceInfraFields_NoClusterFanOut(t *testing.T) {
	cl := newTestClusterForAPI(t)
	cl.Conf.Cloud18MonthlyInfraCost = 42
	cl.Conf.Cloud18InfraCPUModel = "per-cluster-cpu"
	repman := newTestRepmanWithCluster(t, cl.Name, cl)
	repman.Conf = &config.Config{Secrets: make(map[string]config.Secret)}
	repman.ConfigManager = newConfigManagerForTest()

	if err := repman.setRepmanSetting("cloud18-marketplace-monthly-infra-cost", "999"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := repman.setRepmanSetting("cloud18-marketplace-infra-cpu-model", "global-cpu"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repman.Conf.Cloud18MarketplaceMonthlyInfraCost != 999 {
		t.Errorf("global Cloud18MarketplaceMonthlyInfraCost = %v, want 999", repman.Conf.Cloud18MarketplaceMonthlyInfraCost)
	}
	if repman.Conf.Cloud18MarketplaceInfraCPUModel != "global-cpu" {
		t.Errorf("global Cloud18MarketplaceInfraCPUModel = %q, want %q", repman.Conf.Cloud18MarketplaceInfraCPUModel, "global-cpu")
	}
	if cl.Conf.Cloud18MonthlyInfraCost != 42 {
		t.Errorf("per-cluster Cloud18MonthlyInfraCost changed to %v, want unchanged 42 (no fan-out)", cl.Conf.Cloud18MonthlyInfraCost)
	}
	if cl.Conf.Cloud18InfraCPUModel != "per-cluster-cpu" {
		t.Errorf("per-cluster Cloud18InfraCPUModel changed to %q, want unchanged %q (no fan-out)", cl.Conf.Cloud18InfraCPUModel, "per-cluster-cpu")
	}
}
