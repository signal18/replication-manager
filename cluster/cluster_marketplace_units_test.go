// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func newTestClusterForMarketplaceUnits(t *testing.T) *Cluster {
	t.Helper()
	return &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{},
	}
}

// --- ComputeDatabaseUnits / ComputeDBUPerNode ---

func TestComputeDBUPerNode_MaxOfFourRatios(t *testing.T) {
	cases := []struct {
		name  string
		cores string
		mem   string
		disk  string
		iops  string
		want  float64
	}{
		{"cores dominate", "4", "4096", "40", "1000", 4},  // cores=4, mem=1, disk=1, iops=1
		{"mem dominates", "1", "16384", "40", "1000", 4},  // cores=1, mem=4, disk=1, iops=1
		{"disk dominates", "1", "4096", "160", "1000", 4}, // cores=1, mem=1, disk=4, iops=1
		{"iops dominates", "1", "4096", "40", "4000", 4},  // cores=1, mem=1, disk=1, iops=4
		{"all equal, 1 DBU", "1", "4096", "40", "1000", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cl := newTestClusterForMarketplaceUnits(t)
			cl.Conf.ProvCores = tc.cores
			cl.Conf.ProvMem = tc.mem
			cl.Conf.ProvDisk = tc.disk
			cl.Conf.ProvIops = tc.iops
			if got := cl.ComputeDBUPerNode(); got != tc.want {
				t.Errorf("ComputeDBUPerNode() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestComputeDatabaseUnits_ScalesWithNodeCount(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "2"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	// DBU per node = max(2, 1, 1, 1) = 2
	cl.Servers = make(serverList, 3)
	for i := range cl.Servers {
		cl.Servers[i] = &ServerMonitor{}
	}

	if got, want := cl.ComputeDatabaseUnits(), 6.0; got != want {
		t.Errorf("ComputeDatabaseUnits() = %v, want %v", got, want)
	}
}

func TestComputeDatabaseUnits_NoServers_IsZero(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "4"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"

	if got := cl.ComputeDatabaseUnits(); got != 0 {
		t.Errorf("ComputeDatabaseUnits() with no servers = %v, want 0", got)
	}
}

func TestComputeDatabaseUnits_NoServers_BackupEnabled_StillZero(t *testing.T) {
	// No db nodes to back up means no cluster to price, regardless of the
	// backup flag.
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "4"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	cl.Conf.SchedulerBackupLogical = true

	if got := cl.ComputeDatabaseUnits(); got != 0 {
		t.Errorf("ComputeDatabaseUnits() with no servers = %v, want 0", got)
	}
}

func TestComputeDatabaseUnits_LogicalBackupEnabled_AddsFlatOneUnit(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "2"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	cl.Conf.SchedulerBackupLogical = true
	// DBU per node = max(2, 1, 1, 1) = 2
	cl.Servers = make(serverList, 3)
	for i := range cl.Servers {
		cl.Servers[i] = &ServerMonitor{}
	}

	// 2*3 node units + flat 1 for backup = 7, not 8 — retained snapshots are
	// bundled into the single flat unit, not counted individually.
	if got, want := cl.ComputeDatabaseUnits(), 7.0; got != want {
		t.Errorf("ComputeDatabaseUnits() with logical backup enabled = %v, want %v", got, want)
	}
}

func TestComputeDatabaseUnits_PhysicalBackupEnabled_AddsFlatOneUnit(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "2"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	cl.Conf.SchedulerBackupPhysical = true
	cl.Servers = make(serverList, 3)
	for i := range cl.Servers {
		cl.Servers[i] = &ServerMonitor{}
	}

	if got, want := cl.ComputeDatabaseUnits(), 7.0; got != want {
		t.Errorf("ComputeDatabaseUnits() with physical backup enabled = %v, want %v", got, want)
	}
}

func TestComputeDatabaseUnits_BothBackupTypesEnabled_StillFlatOneUnit(t *testing.T) {
	// Flat, not per-line: logical + physical both on must not double-count.
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "2"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	cl.Conf.SchedulerBackupLogical = true
	cl.Conf.SchedulerBackupPhysical = true
	cl.Servers = make(serverList, 3)
	for i := range cl.Servers {
		cl.Servers[i] = &ServerMonitor{}
	}

	if got, want := cl.ComputeDatabaseUnits(), 7.0; got != want {
		t.Errorf("ComputeDatabaseUnits() with both backup types enabled = %v, want %v", got, want)
	}
}

func TestComputeDatabaseUnits_BackupDisabled_NoExtraUnit(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvCores = "2"
	cl.Conf.ProvMem = "4096"
	cl.Conf.ProvDisk = "40"
	cl.Conf.ProvIops = "1000"
	cl.Conf.SchedulerBackupLogical = false
	cl.Conf.SchedulerBackupPhysical = false
	cl.Servers = make(serverList, 3)
	for i := range cl.Servers {
		cl.Servers[i] = &ServerMonitor{}
	}

	if got, want := cl.ComputeDatabaseUnits(), 6.0; got != want {
		t.Errorf("ComputeDatabaseUnits() with backup disabled = %v, want %v", got, want)
	}
}

// --- ComputeApplicationUnits ---
//
// The formula is resource-derived, not credit-based: each provisioned app contributes
// its App Unit (1 core / 4GB / 10GB ratio, ceil'd) times its agent count, and each live
// proxy contributes one App Unit computed from cluster.Conf.ProvProxCores/Mem/Disk —
// see doc/implementation/config/CLOUD18_CREDIT_MODEL_IMPLEMENTATION_PLAN.md §3.5.

func newProvisionedTestApp(t *testing.T, cores, memMB, diskGB, agents string) *App {
	t.Helper()
	app := &App{
		Datadir: t.TempDir(),
		AppConfig: &config.AppConfig{
			ProvAppCpuCores: cores,
			ProvAppMem:      memMB,
			ProvAppDisk:     diskGB,
			ProvAppAgents:   agents,
		},
	}
	if err := app.SetProvisionCookie(); err != nil {
		t.Fatalf("SetProvisionCookie: %v", err)
	}
	return app
}

func TestComputeApplicationUnits_ProvisionedApp_ScalesWithAgentCount(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	// cores=2 -> unit 2 dominates mem/disk (both at ratio 1); 3 agents.
	app := newProvisionedTestApp(t, "2", "4096", "10", "a1,a2,a3")
	cl.Apps = appList{app}

	if got, want := cl.ComputeApplicationUnits(), 6.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_UnprovisionedApp_NotCounted(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	app := &App{
		Datadir: t.TempDir(),
		AppConfig: &config.AppConfig{
			ProvAppCpuCores: "8",
			ProvAppMem:      "4096",
			ProvAppDisk:     "10",
			ProvAppAgents:   "a1",
		},
	}
	// No SetProvisionCookie(): app is configured but never provisioned.
	cl.Apps = appList{app}

	if got, want := cl.ComputeApplicationUnits(), 0.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_LiveProxiesUseAppUnitRatio(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	// cores=1, mem=8192 (2 App Units), disk=10 -> proxy unit = 2.
	cl.Conf.ProvProxCores = "1"
	cl.Conf.ProvProxMem = "8192"
	cl.Conf.ProvProxDisk = "10"
	cl.Proxies = proxyList{
		&Proxy{State: stateSuspect}, // down, must not count
		&Proxy{State: stateFailed},  // down, must not count
		&Proxy{State: "Running"},    // live
		&Proxy{State: "Running"},    // live
	}

	// 2 live proxies * 2 App Units = 4
	if got, want := cl.ComputeApplicationUnits(), 4.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_AllProxiesDown_NoContribution(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.ProvProxCores = "4"
	cl.Conf.ProvProxMem = "4096"
	cl.Conf.ProvProxDisk = "10"
	cl.Proxies = proxyList{
		&Proxy{State: stateFailed},
		&Proxy{State: stateErrorAuth},
	}

	if got, want := cl.ComputeApplicationUnits(), 0.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_AppsAndProxiesCombined(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	app := newProvisionedTestApp(t, "1", "4096", "10", "a1,a2") // unit 1 * 2 agents = 2
	cl.Apps = appList{app}
	cl.Conf.ProvProxCores = "1"
	cl.Conf.ProvProxMem = "4096"
	cl.Conf.ProvProxDisk = "10" // proxy unit = 1
	cl.Proxies = proxyList{
		&Proxy{State: "Running"}, // live, +1
	}

	if got, want := cl.ComputeApplicationUnits(), 3.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

// --- ClusterState JSON persistence (Section D) ---

func TestClusterState_MarshalsMarketplaceUnitFields(t *testing.T) {
	clsave := ClusterState{
		DatabaseUnits:    12.5,
		ApplicationUnits: 7,
	}
	out, err := json.Marshal(clsave)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	var roundtrip map[string]interface{}
	if err := json.Unmarshal(out, &roundtrip); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if got, ok := roundtrip["databaseUnits"]; !ok || got != 12.5 {
		t.Errorf(`expected "databaseUnits":12.5 in output, got %v (present=%v)`, got, ok)
	}
	if got, ok := roundtrip["applicationUnits"]; !ok || got != 7.0 {
		t.Errorf(`expected "applicationUnits":7 in output, got %v (present=%v)`, got, ok)
	}
}

// TestClusterState_UnmarshalsOldFileWithoutUnitFields confirms the additive-field
// compatibility note in the plan: a clusterstate.json written before this change
// (missing databaseUnits/applicationUnits) must still unmarshal cleanly, with the
// new fields defaulting to zero rather than erroring.
func TestClusterState_UnmarshalsOldFileWithoutUnitFields(t *testing.T) {
	oldJSON := `{
		"servers": "127.0.0.1:3306",
		"crashes": [],
		"provisioned": true,
		"repmgrVersion": "1.0",
		"repmgrArch": "amd64",
		"repmgrOS": "linux",
		"isDown": false,
		"isMasterDown": false,
		"isFailable": true,
		"isProvisioned": true
	}`

	var clsave ClusterState
	if err := json.Unmarshal([]byte(oldJSON), &clsave); err != nil {
		t.Fatalf("unmarshal of pre-existing clusterstate.json format failed: %v", err)
	}
	if clsave.DatabaseUnits != 0 {
		t.Errorf("expected DatabaseUnits to default to 0 on old file, got %v", clsave.DatabaseUnits)
	}
	if clsave.ApplicationUnits != 0 {
		t.Errorf("expected ApplicationUnits to default to 0 on old file, got %v", clsave.ApplicationUnits)
	}
	if clsave.Servers != "127.0.0.1:3306" {
		t.Errorf("expected existing fields to still unmarshal, got Servers=%q", clsave.Servers)
	}
}
