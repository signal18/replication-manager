// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"math"
	"time"

	"github.com/signal18/replication-manager/config"
)

// SaveDiscoveredResources keeps the capacity the agents show (the unique physical agents of every
// cluster, as the Resource Manager page sums them: cores x SMT gain, memory in MB) in the config as
// resource-manager-discovered-cpu-cores and resource-manager-discovered-memory-mb. They are saved to
// default.toml and pushed with the config, so tools reading the config repository can see the
// capacity without it being typed in by hand. They are not overrides: the
// resource-manager-infra-* values still win when set, and replication-manager rewrites these.
// An axis the agents do not show (none monitored yet, e.g. at startup) keeps its last value, so a
// restart does not flap the config; the config is saved only when a value changes. Run on its own
// timer (discoveredResourcesLoop), never from the monitoring loop.
func (repman *ReplicationManager) SaveDiscoveredResources() {
	if repman == nil || repman.Conf == nil {
		return
	}
	in := repman.infraCapacityInputs(repman.sortedClusters())
	cores := math.Round(in.AgentsCores*100) / 100
	memMB := math.Round(in.AgentsMemMB)
	changed := false
	if cores > 0 && cores != repman.Conf.ResourceManagerDiscoveredCpuCores {
		repman.Conf.ResourceManagerDiscoveredCpuCores = cores
		changed = true
	}
	if memMB > 0 && memMB != repman.Conf.ResourceManagerDiscoveredMemoryMB {
		repman.Conf.ResourceManagerDiscoveredMemoryMB = memMB
		changed = true
	}
	if !changed {
		return
	}
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
		"Resource Manager discovered capacity: %v cores, %v MB of memory", repman.Conf.ResourceManagerDiscoveredCpuCores, repman.Conf.ResourceManagerDiscoveredMemoryMB)
	repman.Save()
}

// discoveredResourcesLoop runs SaveDiscoveredResources every resource-manager-discovered-interval
// seconds, on its own timer so the monitoring loop is not involved; 0 turns it off.
func (repman *ReplicationManager) discoveredResourcesLoop() {
	interval := repman.Conf.ResourceManagerDiscoveredInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(interval) * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		if repman.exit.Load() {
			return
		}
		repman.SaveDiscoveredResources()
	}
}
