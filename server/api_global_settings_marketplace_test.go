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
