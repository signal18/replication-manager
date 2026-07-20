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
		{"cores dominate", "4", "4096", "40", "1000", 4},   // cores=4, mem=1, disk=1, iops=1
		{"mem dominates", "1", "16384", "40", "1000", 4},   // cores=1, mem=4, disk=1, iops=1
		{"disk dominates", "1", "4096", "160", "1000", 4},  // cores=1, mem=1, disk=4, iops=1
		{"iops dominates", "1", "4096", "40", "4000", 4},   // cores=1, mem=1, disk=1, iops=4
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

// --- ComputeApplicationUnits ---

func TestComputeApplicationUnits_UsedCreditsOnly_NoProxies(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.Cloud18ApplicationCreditsUsed = 5
	cl.Conf.ProvProxCores = "2"

	if got, want := cl.ComputeApplicationUnits(), 5.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_AddsLiveProxyCoresOnly(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.Cloud18ApplicationCreditsUsed = 5
	cl.Conf.ProvProxCores = "2"
	cl.Proxies = proxyList{
		&Proxy{State: stateSuspect}, // down, must not count
		&Proxy{State: stateFailed},  // down, must not count
		&Proxy{State: "Running"},    // live
		&Proxy{State: "Running"},    // live
	}

	// 5 used credits + 2 live proxies * 2 cores = 9
	if got, want := cl.ComputeApplicationUnits(), 9.0; got != want {
		t.Errorf("ComputeApplicationUnits() = %v, want %v", got, want)
	}
}

func TestComputeApplicationUnits_AllProxiesDown_NoContribution(t *testing.T) {
	cl := newTestClusterForMarketplaceUnits(t)
	cl.Conf.Cloud18ApplicationCreditsUsed = 3
	cl.Conf.ProvProxCores = "4"
	cl.Proxies = proxyList{
		&Proxy{State: stateFailed},
		&Proxy{State: stateErrorAuth},
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
