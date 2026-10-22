// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"math"
	"sort"

	"github.com/signal18/replication-manager/cluster"
)

// infraCapacityInputs is the infrastructure's raw capacity as the global
// resources view and the self-service gate both see it: the unique physical
// agents of every cluster summed (cores, memory), a config override winning
// when set (resource-manager-infra-*; agents carry no disk/iops/network).
type infraCapacityInputs struct {
	Cores, MemMB, DiskGB, Iops, NetMbps float64
	Src                                 map[string]string  // axis -> "config" | "agents"
	AgentCores                          map[string]float64 // per-agent physical cores
}

func (repman *ReplicationManager) sortedClusters() []*cluster.Cluster {
	repman.Lock()
	clusters := make([]*cluster.Cluster, 0, len(repman.Clusters))
	for _, cl := range repman.Clusters {
		clusters = append(clusters, cl)
	}
	repman.Unlock()
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Name < clusters[j].Name })
	return clusters
}

func (repman *ReplicationManager) infraCapacityInputs(clusters []*cluster.Cluster) infraCapacityInputs {
	in := infraCapacityInputs{Src: map[string]string{}, AgentCores: map[string]float64{}}
	seen := map[string]bool{}
	var sumCores, sumMemMB float64
	for _, cl := range clusters {
		for _, a := range cl.Agents {
			key := a.HostName
			if key == "" {
				key = a.Id
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			// real cores: an SMT node counts cores x resource-manager-smt-gain (cluster/smt.go)
			ce := a.CoreEquivalents(repman.Conf.ResourceManagerSmtGain)
			in.AgentCores[key] = ce
			sumCores += ce
			// cluster.Agent.MemBytes is populated in MB (OpenSVC asset + on-prem
			// /proc/meminfo/1024 both store MB), despite the name.
			sumMemMB += float64(a.MemBytes)
		}
	}
	pick := func(axis string, override, agents float64) float64 {
		if override > 0 {
			in.Src[axis] = "config"
			return override
		}
		in.Src[axis] = "agents"
		return agents
	}
	in.Cores = pick("cpu", repman.Conf.ResourceManagerInfraCpuCores, sumCores)
	in.MemMB = pick("mem", repman.Conf.ResourceManagerInfraMemoryMB, sumMemMB)
	in.DiskGB = pick("disk", repman.Conf.ResourceManagerInfraDiskGB, 0)
	in.Iops = pick("io", repman.Conf.ResourceManagerInfraIops, 0)
	in.NetMbps = pick("net", repman.Conf.ResourceManagerInfraNetworkMbps, 0)
	return in
}

// The floor a cluster for sale keeps in the plan pot (units, not ratios): it is
// provisioned, so it never counts 0. Over-commit is measured on resources, not plans.
const (
	forSaleFloorDbuPerNode = 1.0
	forSaleFloorApu        = 1.0
)

// InfraUnitPool is the infrastructure's unit pool as the ResourceManager sees it:
// usable = capacity (binding axis) × quota, planned = Σ of every cluster's plan
// (the contracts already sold), free = usable − planned. DBU and APU are the
// SAME metal projected through the two profiles. Known is false when no
// capacity is observed or declared: the pool cannot gate anything then.
// ForSale is the part of planned held by clusters for sale (cloud18-shared) that a
// self-service client may take (#2144): an offer keeps its floor, 1 DBU per database
// node and 1 APU (its BKU plan stays counted whole by the ledger); only its plan above
// the floor is released.
type InfraUnitPool struct {
	Known      bool    `json:"known"`
	UsableDbu  float64 `json:"usableDbu"`
	PlannedDbu float64 `json:"plannedDbu"`
	FreeDbu    float64 `json:"freeDbu"`
	ForSaleDbu float64 `json:"forSaleDbu"`
	UsableApu  float64 `json:"usableApu"`
	PlannedApu float64 `json:"plannedApu"`
	FreeApu    float64 `json:"freeApu"`
	ForSaleApu float64 `json:"forSaleApu"`
}

func (repman *ReplicationManager) infraUnitPool() InfraUnitPool {
	rm := repman.resourceManager
	if rm == nil {
		return InfraUnitPool{}
	}
	clusters := repman.sortedClusters()
	in := repman.infraCapacityInputs(clusters)
	if in.Cores <= 0 && in.MemMB <= 0 {
		return InfraUnitPool{}
	}
	_, _, _, _, bindingDBU, _ := rm.CapacityDBUView(cluster.AgentCapacity{Cores: in.Cores, MemMB: in.MemMB, DiskGB: in.DiskGB, Iops: in.Iops})
	_, _, _, bindingAPU, _ := rm.CapacityAPUView(cluster.AgentCapacity{Cores: in.Cores, MemMB: in.MemMB, DiskGB: in.DiskGB})
	pool := InfraUnitPool{Known: true, UsableDbu: bindingDBU, UsableApu: bindingAPU}
	if quota := rm.QuotaPct(); quota > 0 {
		pool.UsableDbu = bindingDBU * quota / 100.0
		pool.UsableApu = bindingAPU * quota / 100.0
	}
	for _, cl := range clusters {
		if !cl.IsProvision {
			continue // an unprovisioned cluster reserves nothing on the infrastructure
		}
		dbu, apu := float64(cl.GetPlanDbu()), rm.AppPlanByCluster(cl.Name).Apu
		pool.PlannedDbu += dbu
		pool.PlannedApu += apu
		if cl.Conf.Cloud18Shared {
			pool.ForSaleDbu += math.Max(0, dbu-forSaleFloorDbuPerNode*float64(len(cl.Servers)))
			pool.ForSaleApu += math.Max(0, apu-forSaleFloorApu)
		}
	}
	pool.FreeDbu = pool.UsableDbu - pool.PlannedDbu
	pool.FreeApu = pool.UsableApu - pool.PlannedApu
	// The ledger knows what the OTHER units already reserved on the same metal: its plan pot
	// is the honest free figure (usable minus every plan, DBU + APU + BKU, physical).
	if l := rm.Ledger(); l.Known {
		pool.FreeDbu = l.PlanPotUnits.Dbu
		pool.FreeApu = l.PlanPotUnits.Apu
	}
	return pool
}
