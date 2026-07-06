package cluster

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// newExcessTestApp builds a minimal Cluster+App pair wired well enough to
// exercise the manual-mode excess delta helper without going through the
// full monitoring/orchestration stack.
func newExcessTestApp(t *testing.T, sizingMode string, credits int, cpuCores, memMB, diskGB string) *App {
	t.Helper()
	root := t.TempDir()

	sm := &state.StateMachine{}
	sm.Init()
	cluster := &Cluster{
		Name: "cl",
		Conf: &config.Config{
			WorkingDir: root,
		},
		StateMachine: sm,
	}

	datadir := filepath.Join(root, "app1")
	if err := os.MkdirAll(datadir, os.ModePerm); err != nil {
		t.Fatalf("failed to create app datadir: %v", err)
	}

	app := &App{
		Name:         "app1",
		Host:         "app1",
		Port:         "8080",
		Mutex:        &sync.Mutex{},
		ClusterGroup: cluster,
		Datadir:      datadir,
		AppConfig: &config.AppConfig{
			ProvAppSizingMode:    sizingMode,
			ProvAppCreditPlanned: credits,
			ProvAppCpuCores:      cpuCores,
			ProvAppMem:           memMB,
			ProvAppDisk:          diskGB,
		},
	}
	cluster.Apps = []*App{app}
	return app
}

// 1. 2 credits + 2 cores / 8G / 50G disk must report 30GB disk excess and must
// not collapse into 5 whole credits.
func TestManualExcess_DoesNotCollapseIntoWholeCredits(t *testing.T) {
	app := newExcessTestApp(t, config.AppSizingModeManual, 2, "2", "8192", "50")

	cpuExcess, memExcess, diskExcess := app.ManualCreditExcess()

	if app.AppConfig.ProvAppCreditPlanned != 2 {
		t.Fatalf("expected planned credits to stay at 2, got %d", app.AppConfig.ProvAppCreditPlanned)
	}
	if cpuExcess != 0 {
		t.Fatalf("expected 0 cpu excess, got %d", cpuExcess)
	}
	if memExcess != 0 {
		t.Fatalf("expected 0 memory excess, got %d", memExcess)
	}
	if diskExcess != 30 {
		t.Fatalf("expected 30 GB disk excess, got %d", diskExcess)
	}
}

// 2. Mixed excess across CPU, memory, and disk simultaneously.
func TestManualExcess_MixedAcrossResources(t *testing.T) {
	app := newExcessTestApp(t, config.AppSizingModeManual, 1, "3", "10240", "25")

	cpuExcess, memExcess, diskExcess := app.ManualCreditExcess()

	if cpuExcess != 2 {
		t.Fatalf("expected 2 cpu core excess, got %d", cpuExcess)
	}
	if memExcess != 6144 {
		t.Fatalf("expected 6144 MB memory excess, got %d", memExcess)
	}
	if diskExcess != 15 {
		t.Fatalf("expected 15 GB disk excess, got %d", diskExcess)
	}
}

// 3. Unit-mode apps must report zero excess even with large configured
// resources, and must be excluded from cluster aggregate excess totals.
func TestManualExcess_UnitModeIgnoredInClusterAggregates(t *testing.T) {
	app := newExcessTestApp(t, config.AppSizingModeUnit, 2, "2", "8192", "50")

	cpuExcess, memExcess, diskExcess := app.ManualCreditExcess()
	if cpuExcess != 0 || memExcess != 0 || diskExcess != 0 {
		t.Fatalf("expected 0 excess in unit mode, got cpu=%d mem=%d disk=%d", cpuExcess, memExcess, diskExcess)
	}

	app.ClusterGroup.recomputeAppCredits()

	conf := app.ClusterGroup.Conf
	if conf.Cloud18ApplicationExcessCpuCores != 0 || conf.Cloud18ApplicationExcessDiskGB != 0 {
		t.Fatalf("expected unit-mode app excluded from aggregates, got cpu=%d disk=%d",
			conf.Cloud18ApplicationExcessCpuCores, conf.Cloud18ApplicationExcessDiskGB)
	}
}

// 4. manualCreditExcess must scale the per-agent-node resource spec by the
// number of agents before comparing it to the whole-app credit entitlement,
// or a multi-agent app's real excess is undercounted. 2 credits with 2
// agents include 2*10=20 GB disk total; each agent provisions 15 GB (30 GB
// total), so total excess must be 10 GB, not 0.
func TestManualExcess_ScalesPerAgentResourcesByAgentCount(t *testing.T) {
	app := newExcessTestApp(t, config.AppSizingModeManual, 2, "1", "2048", "15")
	app.AppConfig.ProvAppAgents = "node1,node2"

	cpuExcess, _, diskExcess := app.ManualCreditExcess()

	if cpuExcess != 0 {
		t.Fatalf("expected 0 cpu excess (2 total cores == 2 included), got %d", cpuExcess)
	}
	if diskExcess != 10 {
		t.Fatalf("expected 10 GB disk excess across 2 agents (30 total - 20 included), got %d", diskExcess)
	}
}

// 5. An app with no explicit CPU/memory/disk/agents of its own inherits
// those defaults from the cluster at provisioning time (see
// OpenSVCGetAppContainerSection/OpenSVCGetAppEnvSection, which read through
// cluster.GetAppCores/GetAppMemory/GetAppDisk/GetAppAgents). ManualCreditExcess
// must resolve the same inherited defaults rather than treating empty
// app-level fields as zero resources, or excess is silently undercounted to 0.
func TestManualExcess_UsesInheritedClusterResourceDefaults(t *testing.T) {
	app := newExcessTestApp(t, config.AppSizingModeManual, 2, "", "", "")
	app.ClusterGroup.Conf.ProvAppCpuCores = "1"
	app.ClusterGroup.Conf.ProvAppMem = "2048"
	app.ClusterGroup.Conf.ProvAppDisk = "15"
	app.ClusterGroup.Conf.ProvAppAgents = "node1,node2"

	cpuExcess, _, diskExcess := app.ManualCreditExcess()

	if cpuExcess != 0 {
		t.Fatalf("expected 0 cpu excess (2 total cores == 2 included), got %d", cpuExcess)
	}
	if diskExcess != 10 {
		t.Fatalf("expected 10 GB disk excess from inherited cluster defaults across 2 agents, got %d", diskExcess)
	}
}

// 6. Cluster-level aggregate excess must sum per-app deltas across every
// manual-mode app in the cluster.
func TestRecomputeAppCredits_AggregatesManualExcessAcrossApps(t *testing.T) {
	app1 := newExcessTestApp(t, config.AppSizingModeManual, 2, "2", "8192", "50") // 30GB disk excess
	cluster := app1.ClusterGroup
	app2 := newExcessTestApp(t, config.AppSizingModeManual, 1, "3", "10240", "25")
	app2.ClusterGroup = cluster
	cluster.Apps = []*App{app1, app2}

	cluster.recomputeAppCredits()

	conf := cluster.Conf
	if conf.Cloud18ApplicationExcessCpuCores != 2 {
		t.Fatalf("expected aggregate cpu excess 2, got %d", conf.Cloud18ApplicationExcessCpuCores)
	}
	if conf.Cloud18ApplicationExcessMemoryMB != 6144 {
		t.Fatalf("expected aggregate memory excess 6144, got %d", conf.Cloud18ApplicationExcessMemoryMB)
	}
	if conf.Cloud18ApplicationExcessDiskGB != 45 {
		t.Fatalf("expected aggregate disk excess 45, got %d", conf.Cloud18ApplicationExcessDiskGB)
	}
}
