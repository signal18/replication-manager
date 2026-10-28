// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"math"
	"sync"
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
// timer (discoveredResourcesLoop), never from the monitoring loop. Returns whether the agents showed
// any capacity.
func (repman *ReplicationManager) SaveDiscoveredResources() bool {
	if repman == nil || repman.Conf == nil {
		return false
	}
	in := repman.infraCapacityInputs(repman.sortedClusters())
	// Rounded the same way every time (cores to 0.01, MB to 1), so the != below compares values that
	// went through the same rounding and a stable hardware never rewrites the config.
	cores := math.Round(in.AgentsCores*100) / 100
	memMB := math.Round(in.AgentsMemMB)
	changed := false
	if cores > 0 && cores != repman.Conf.ResourceManagerDiscoveredCpuCores && repman.discoveredKeyWritable("resource-manager-discovered-cpu-cores") {
		repman.Conf.ResourceManagerDiscoveredCpuCores = cores
		changed = true
	}
	if memMB > 0 && memMB != repman.Conf.ResourceManagerDiscoveredMemoryMB && repman.discoveredKeyWritable("resource-manager-discovered-memory-mb") {
		repman.Conf.ResourceManagerDiscoveredMemoryMB = memMB
		changed = true
	}
	if changed {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"Resource Manager discovered capacity: %v cores, %v MB of memory", repman.Conf.ResourceManagerDiscoveredCpuCores, repman.Conf.ResourceManagerDiscoveredMemoryMB)
		repman.Save()
	}
	return cores > 0 || memMB > 0
}

// discoveredPinnedLogged remembers the discovered keys already reported as pinned, so the log says it once.
var discoveredPinnedLogged sync.Map

// discoveredKeyWritable reports whether replication-manager may write a discovered key: not when it is
// set in a config file (immutable, ImmuableFlagMap). The operator's value is kept, never overwritten
// by what the agents show; SaveDynamic would drop the key from default.toml anyway, so the file value
// would come back at each restart and be replaced again at the next reading.
func (repman *ReplicationManager) discoveredKeyWritable(key string) bool {
	if !repman.Conf.IsVariableImmutable(key) {
		return true
	}
	if _, logged := discoveredPinnedLogged.LoadOrStore(key, true); !logged {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo,
			"Resource Manager discovered capacity: %s is set in a config file, kept as is (not rewritten from the agents)", key)
	}
	return false
}

// discoveredTimeUnit is the unit of resource-manager-discovered-interval and of the first-reading
// retry (a test shortens it).
var discoveredTimeUnit = time.Second

// discoveredResourcesLoop runs SaveDiscoveredResources on its own timer, so the monitoring loop is not
// involved. Until the agents have been read once (after a start) it retries every monitoring-ticker,
// then every resource-manager-discovered-interval. The interval is read again before each wait, so a
// change applies without a restart; 0 turns the reading off (checked again every minute). It stops
// within a second of replication-manager stopping.
func (repman *ReplicationManager) discoveredResourcesLoop() {
	seen := false
	for !repman.exit.Load() {
		interval := repman.Conf.ResourceManagerDiscoveredInterval
		wait := time.Duration(interval) * discoveredTimeUnit
		if interval <= 0 {
			wait = 60 * discoveredTimeUnit
		} else if !seen {
			wait = time.Duration(max(repman.Conf.MonitoringTicker, 1)) * discoveredTimeUnit
		}
		// Wait in steps of one unit, so replication-manager stopping is seen within a second.
		for deadline := time.Now().Add(wait); time.Now().Before(deadline); {
			if repman.exit.Load() {
				return
			}
			time.Sleep(min(time.Until(deadline), discoveredTimeUnit))
		}
		if repman.exit.Load() {
			return
		}
		if repman.Conf.ResourceManagerDiscoveredInterval > 0 && repman.SaveDiscoveredResources() {
			seen = true
		}
	}
}
