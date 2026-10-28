// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"hash"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// The agents' capacity goes into the config (resource-manager-discovered-*), summed as the Resource
// Manager page sums it (an agent shared by two clusters once, cores x SMT gain), whatever the
// resource-manager-infra-* overrides say; the config is saved only when a value changes, an axis the
// agents stop showing keeps its value, and default.toml carries both keys for the config push.
func TestSaveDiscoveredResources(t *testing.T) {
	repman, c1 := newTokenTestManager(t)
	repman.Conf.Secrets = config.NewSecrets(map[string]config.Secret{})
	repman.Conf.ResourceManagerSmtGain = 1.25
	a := cluster.Agent{Id: "a", HostName: "node-a", CpuCores: 8, MemBytes: 32768}
	b := cluster.Agent{Id: "b", HostName: "node-b", CpuCores: 4, CpuThreads: 8, MemBytes: 16384} // SMT: 4 x 1.25
	c1.Agents = []cluster.Agent{a, b}
	c2 := &cluster.Cluster{Name: "c2", Conf: &config.Config{}, Agents: []cluster.Agent{a}} // node-a again
	repman.Clusters[c2.Name] = c2
	repman.Conf.ResourceManagerInfraCpuCores = 100 // an override does not change what the agents show

	repman.SaveDiscoveredResources()
	if got := repman.Conf.ResourceManagerDiscoveredCpuCores; got != 13 {
		t.Errorf("discovered cores = %v, want 13 (8 + 4 x 1.25, node-a once)", got)
	}
	if got := repman.Conf.ResourceManagerDiscoveredMemoryMB; got != 49152 {
		t.Errorf("discovered memory = %v MB, want 49152", got)
	}
	if !repman.IsNeedConfigSave {
		t.Fatal("a discovered change must ask for a config save")
	}

	// Unchanged: no save.
	repman.IsNeedConfigSave = false
	repman.SaveDiscoveredResources()
	if repman.IsNeedConfigSave {
		t.Error("unchanged discovered values must not ask for a config save")
	}

	// No agent shown (e.g. at startup): the last values stay, no save.
	c1.Agents, c2.Agents = nil, nil
	repman.SaveDiscoveredResources()
	if repman.IsNeedConfigSave || repman.Conf.ResourceManagerDiscoveredCpuCores != 13 || repman.Conf.ResourceManagerDiscoveredMemoryMB != 49152 {
		t.Errorf("without agents the values must stay: %v cores, %v MB, save %v",
			repman.Conf.ResourceManagerDiscoveredCpuCores, repman.Conf.ResourceManagerDiscoveredMemoryMB, repman.IsNeedConfigSave)
	}

	// A node added: saved again.
	c1.Agents = []cluster.Agent{a, b, {Id: "c", HostName: "node-c", CpuCores: 2, MemBytes: 8192}}
	repman.SaveDiscoveredResources()
	if !repman.IsNeedConfigSave || repman.Conf.ResourceManagerDiscoveredCpuCores != 15 || repman.Conf.ResourceManagerDiscoveredMemoryMB != 57344 {
		t.Errorf("a new node: %v cores, %v MB, save %v",
			repman.Conf.ResourceManagerDiscoveredCpuCores, repman.Conf.ResourceManagerDiscoveredMemoryMB, repman.IsNeedConfigSave)
	}

	// A key set in a config file (immutable) is the operator's: never rewritten from the agents.
	repman.Conf.SetImmuableFlag("resource-manager-discovered-cpu-cores", 6.0)
	repman.Conf.ResourceManagerDiscoveredCpuCores = 6
	repman.IsNeedConfigSave = false
	c1.Agents = append(c1.Agents, cluster.Agent{Id: "d", HostName: "node-d", CpuCores: 2, MemBytes: 8192})
	repman.SaveDiscoveredResources()
	if repman.Conf.ResourceManagerDiscoveredCpuCores != 6 {
		t.Errorf("a pinned key must keep the file value 6, got %v", repman.Conf.ResourceManagerDiscoveredCpuCores)
	}
	if !repman.IsNeedConfigSave || repman.Conf.ResourceManagerDiscoveredMemoryMB != 65536 {
		t.Errorf("the unpinned memory key still follows the agents: %v MB, save %v", repman.Conf.ResourceManagerDiscoveredMemoryMB, repman.IsNeedConfigSave)
	}
	repman.Conf.DeleteImmuableFlag("resource-manager-discovered-cpu-cores")
	c1.Agents = c1.Agents[:3]
	repman.SaveDiscoveredResources() // back to the agents: 15 cores, 57344 MB

	// default.toml carries them (their flag default is 0, so a discovered value is saved).
	repman.DefaultFlagMap = map[string]interface{}{
		"resource-manager-discovered-cpu-cores": 0.0,
		"resource-manager-discovered-memory-mb": 0.0,
	}
	repman.CheckSumConfig = map[string]hash.Hash{}
	if _, err := repman.SaveDynamic(); err != nil {
		t.Fatalf("SaveDynamic: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(repman.Conf.WorkingDir, "default.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"resource-manager-discovered-cpu-cores = 15", "resource-manager-discovered-memory-mb = 57344"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("default.toml has no %q:\n%s", want, raw)
		}
	}
	if !cluster.IsInstanceLocalConfigKey("resource-manager-discovered-cpu-cores") || !cluster.IsInstanceLocalConfigKey("resource-manager-discovered-memory-mb") {
		t.Error("each instance writes its own agents' capacity: the discovered keys never replicate between peers")
	}
}

// The reading runs on its own timer (resource-manager-discovered-interval), not the monitoring loop. The
// time unit is shortened to a millisecond so the test does not hang on a real ticker: 0 reads nothing, and
// after a start the agents' capacity is read at the first monitoring tick (not one full interval later);
// the timer stops with replication-manager. Values are read only after the loop has returned.
func TestDiscoveredResourcesLoop(t *testing.T) {
	defer func(u time.Duration) { discoveredTimeUnit = u }(discoveredTimeUnit)
	discoveredTimeUnit = time.Millisecond
	repman, c1 := newTokenTestManager(t)
	repman.Conf.Secrets = config.NewSecrets(map[string]config.Secret{})
	repman.Conf.MonitoringTicker = 2
	c1.Agents = []cluster.Agent{{Id: "a", HostName: "node-a", CpuCores: 8, MemBytes: 32768}}

	run := func(d time.Duration) {
		t.Helper()
		repman.exit.Store(false)
		done := make(chan struct{})
		go func() { repman.discoveredResourcesLoop(); close(done) }()
		time.Sleep(d)
		repman.exit.Store(true)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the timer must stop with replication-manager")
		}
	}

	// 0: off, nothing read.
	run(200 * time.Millisecond)
	if repman.Conf.ResourceManagerDiscoveredCpuCores != 0 {
		t.Errorf("interval 0: nothing written, got %v cores", repman.Conf.ResourceManagerDiscoveredCpuCores)
	}

	// A long interval (100 000 units): still read at the first monitoring tick after the start.
	repman.Conf.ResourceManagerDiscoveredInterval = 100000
	run(200 * time.Millisecond)
	if repman.Conf.ResourceManagerDiscoveredCpuCores != 8 || repman.Conf.ResourceManagerDiscoveredMemoryMB != 32768 {
		t.Fatalf("first reading after a start: %v cores, %v MB", repman.Conf.ResourceManagerDiscoveredCpuCores, repman.Conf.ResourceManagerDiscoveredMemoryMB)
	}
}
