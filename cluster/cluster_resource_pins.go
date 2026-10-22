// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"sort"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// A resource axis set in /etc (cluster.d) is immutable: each start reads it back, whatever the
// dynamic config moved it to (that value goes to overwrite.toml, which a start does not read).
// Under the dynamic config it is dangerous, the disk most: the follow grew dev3's volume from a
// pinned 2G to 9G, and each start brought the plan back to 2G, the binlog budget and the plan's
// max_binlog_total_size with it, against a 9G volume (2026-10-22, #2142). So the dynamic config
// never moves a pinned axis (grow, shrink, follow, plan alignment), and ERR00119 (disk) /
// WARN0266 (cores, memory, iops) say which key to remove from cluster.d. A pinned resource is
// still written in the saved config (SaveConfigFile): the start ignores it while the pin is there
// (the /etc value wins), and takes it once the pin is removed, never the setting's default
// (unpinned, dev3 restarted on 4G of memory for 768M containers, 20G of disk, 300 IOPS).

var dynamicResourceAxisKeys = map[string]string{
	"cpu":  "prov-db-cpu-cores",
	"mem":  "prov-db-memory",
	"disk": "prov-db-disk-size",
	"io":   "prov-db-disk-iops",
}

// isDynamicResourceKey: key is one of the four resource axes the dynamic config owns.
func isDynamicResourceKey(key string) bool {
	for _, k := range dynamicResourceAxisKeys {
		if k == key {
			return true
		}
	}
	return false
}

// resourceAxisPinned: the axis's key is set in /etc.
func (cluster *Cluster) resourceAxisPinned(axis string) bool {
	key, ok := dynamicResourceAxisKeys[axis]
	return ok && cluster.IsVariableImmutable(key)
}

func (cluster *Cluster) dynamicResourcesOn() bool {
	return cluster.Conf.ProvDBApplyDynamicConfig || cluster.Conf.ProvDBDynamicResource
}

// checkPinnedResourceStates, every tick: ERR00119 while the disk is pinned under the dynamic
// config, WARN0266 for the other pinned axes.
func (cluster *Cluster) checkPinnedResourceStates() {
	if !cluster.dynamicResourcesOn() {
		return
	}
	var others []string
	for axis, key := range dynamicResourceAxisKeys {
		if !cluster.resourceAxisPinned(axis) {
			continue
		}
		v, _ := cluster.Conf.GetImmuableFlag(key)
		pin := fmt.Sprintf("%s = %v", key, v)
		if axis == "disk" {
			cluster.SetState("ERR00119", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00119"], pin), ErrFrom: "PROV"})
			continue
		}
		others = append(others, pin)
	}
	if len(others) > 0 {
		sort.Strings(others)
		cluster.SetState("WARN0266", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0266"], strings.Join(others, ", ")), ErrFrom: "PROV"})
	}
}
