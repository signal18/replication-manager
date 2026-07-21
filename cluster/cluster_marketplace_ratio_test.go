// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/sirupsen/logrus"
)

func newTestClusterForRatioLock(t *testing.T, pricingMode string) *Cluster {
	t.Helper()
	workDir := t.TempDir()
	conf := &config.Config{
		Cloud18MarketplacePricingMode: pricingMode,
		WorkingDir:                    workDir,
	}
	logger := logrus.New()
	return &Cluster{
		Name:          "test-cluster",
		Conf:          conf,
		Logrus:        logger,
		ConfigManager: manager.NewConfigManager(config.NewLogrusWrapper(conf, logger)),
	}
}

// writeServicePlanFixture writes a minimal serviceplan.json matching the real,
// genuinely off-ratio share/serviceplan.csv rows for x1.small (dbcores=2,
// dbmemory=16384 -> mem alone is already 4 DBU while cores is 2 DBU) and
// x1.small.compute, so tests can prove global-unit-pricing overrides the CSV shape.
func writeServicePlanFixture(t *testing.T, cluster *Cluster) {
	t.Helper()
	plans := []config.ServicePlan{
		{Plan: "x1.small", DbCores: 2, DbMemory: 16384, DbDataSize: 700, DbIops: 300, PrxCores: 1, PrxDataSize: 80},
		{Plan: "x1.small.compute", DbCores: 4, DbMemory: 16384, DbDataSize: 700, DbIops: 300, PrxCores: 1, PrxDataSize: 80},
		{Plan: "x1.unmapped-size", DbCores: 2, DbMemory: 4096, DbDataSize: 40, DbIops: 1000, PrxCores: 1, PrxDataSize: 80},
	}
	data, err := json.Marshal(plans)
	if err != nil {
		t.Fatalf("marshal plan fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cluster.Conf.WorkingDir, "serviceplan.json"), data, 0644); err != nil {
		t.Fatalf("write plan fixture: %v", err)
	}
}

// --- IsGlobalUnitPricing ---

func TestIsGlobalUnitPricing(t *testing.T) {
	cases := []struct {
		mode string
		want bool
	}{
		{config.ConstMarketplacePricingModeGlobalUnitPricing, true},
		{config.ConstMarketplacePricingModeCsvServicePlan, false},
		{"", false},
	}
	for _, tc := range cases {
		cl := &Cluster{Conf: &config.Config{Cloud18MarketplacePricingMode: tc.mode}}
		if got := cl.IsGlobalUnitPricing(); got != tc.want {
			t.Errorf("IsGlobalUnitPricing() with mode %q = %v, want %v", tc.mode, got, tc.want)
		}
	}
}

// --- ResizeDatabaseUnits ---

func TestResizeDatabaseUnits_AppliesWholeRatioShape(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeGlobalUnitPricing)

	if err := cl.ResizeDatabaseUnits(3); err != nil {
		t.Fatalf("ResizeDatabaseUnits(3): unexpected error: %v", err)
	}

	if got, want := cl.Conf.ProvCores, "3"; got != want {
		t.Errorf("ProvCores = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvMem, "12288"; got != want {
		t.Errorf("ProvMem = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvDisk, "120"; got != want {
		t.Errorf("ProvDisk = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvIops, "3000"; got != want {
		t.Errorf("ProvIops = %q, want %q", got, want)
	}
	if got, want := cl.ComputeDBUPerNode(), 3.0; got != want {
		t.Errorf("ComputeDBUPerNode() after resize = %v, want %v", got, want)
	}
}

func TestResizeDatabaseUnits_WorksOutsideUnitPricingToo(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeCsvServicePlan)

	if err := cl.ResizeDatabaseUnits(1); err != nil {
		t.Fatalf("ResizeDatabaseUnits(1): unexpected error: %v", err)
	}
	if got, want := cl.Conf.ProvCores, "1"; got != want {
		t.Errorf("ProvCores = %q, want %q", got, want)
	}
}

func TestResizeDatabaseUnits_RejectsLessThanOne(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeGlobalUnitPricing)

	for _, units := range []int{0, -1} {
		if err := cl.ResizeDatabaseUnits(units); err == nil {
			t.Errorf("ResizeDatabaseUnits(%d): expected error, got nil", units)
		}
	}
}

// --- SetServicePlanInfos: DB sizing branches by pricing mode ---

func TestSetServicePlanInfos_GlobalUnitPricing_OverridesOffRatioCSVShape(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeGlobalUnitPricing)
	writeServicePlanFixture(t, cl)

	if err := cl.SetServicePlanInfos("x1.small"); err != nil {
		t.Fatalf("SetServicePlanInfos: unexpected error: %v", err)
	}

	// x1.small -> DBU 4 -> cores=4, mem=16384, disk=160, iops=4000 (the CSV row's raw
	// cores=2/disk=700/iops=300 would be off-ratio and must not be used here).
	if got, want := cl.Conf.ProvCores, "4"; got != want {
		t.Errorf("ProvCores = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvMem, "16384"; got != want {
		t.Errorf("ProvMem = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvDisk, "160"; got != want {
		t.Errorf("ProvDisk = %q, want %q", got, want)
	}
	if got, want := cl.Conf.ProvIops, "4000"; got != want {
		t.Errorf("ProvIops = %q, want %q", got, want)
	}
}

func TestSetServicePlanInfos_CsvMode_UsesRawPlanColumns(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeCsvServicePlan)
	writeServicePlanFixture(t, cl)

	if err := cl.SetServicePlanInfos("x1.small"); err != nil {
		t.Fatalf("SetServicePlanInfos: unexpected error: %v", err)
	}

	if got, want := cl.Conf.ProvCores, "2"; got != want {
		t.Errorf("ProvCores = %q, want %q (raw CSV value)", got, want)
	}
	if got, want := cl.Conf.ProvDisk, "700"; got != want {
		t.Errorf("ProvDisk = %q, want %q (raw CSV value)", got, want)
	}
}

func TestSetServicePlanInfos_GlobalUnitPricing_LegacyVariantsCollapseToSameShape(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeGlobalUnitPricing)
	writeServicePlanFixture(t, cl)

	if err := cl.SetServicePlanInfos("x1.small"); err != nil {
		t.Fatalf("SetServicePlanInfos(x1.small): unexpected error: %v", err)
	}
	smallCores := cl.Conf.ProvCores

	if err := cl.SetServicePlanInfos("x1.small.compute"); err != nil {
		t.Fatalf("SetServicePlanInfos(x1.small.compute): unexpected error: %v", err)
	}
	if cl.Conf.ProvCores != smallCores {
		t.Errorf("x1.small.compute cores = %q, want same as x1.small (%q)", cl.Conf.ProvCores, smallCores)
	}
}

func TestSetServicePlanInfos_GlobalUnitPricing_UnmappedSuffixFailsExplicitly(t *testing.T) {
	cl := newTestClusterForRatioLock(t, config.ConstMarketplacePricingModeGlobalUnitPricing)
	writeServicePlanFixture(t, cl)

	err := cl.SetServicePlanInfos("x1.unmapped-size")
	if err == nil {
		t.Fatal("expected an explicit error for an unmapped plan suffix, got nil")
	}
	// No partial mutation: ProvServicePlan/ProvCores must remain untouched.
	if cl.Conf.ProvServicePlan != "" {
		t.Errorf("ProvServicePlan = %q, want unset after a failed unmapped-plan resolution", cl.Conf.ProvServicePlan)
	}
	if cl.Conf.ProvCores != "" {
		t.Errorf("ProvCores = %q, want unset after a failed unmapped-plan resolution", cl.Conf.ProvCores)
	}
}
