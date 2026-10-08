// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
)

// The shape of an engine server (PostgreSQL) follows the CLUSTER topology, not its
// template (#1925). The postgres templates describe the active-passive shape: one instance
// that moves with its data, a failover service on every agent over a DRBD volume. A
// member of a REPLICATED cluster (replication-master-slave-pg-stream, -pg-logical) has
// its own data and never moves: it belongs to one agent, on the local data pool of the
// cluster's database volumes (prov-db-volume-data), exactly like a MariaDB server. On
// 2026-10-08 the pg-stream and pg-logical members of preprod, deployed as 3-node failover
// services on DRBD, moved and broke at a node crash.

// isReplicatedEngineCluster: the cluster's engine servers replicate between themselves.
func (cluster *Cluster) isReplicatedEngineCluster() bool {
	return cluster.Conf != nil && (cluster.Conf.MasterSlavePgStream || cluster.Conf.MasterSlavePgLogical)
}

// isReplicatedEngineMember: the app is an engine server of a replicated cluster.
func (cluster *Cluster) isReplicatedEngineMember(appcnf *config.AppConfig) bool {
	return appcnf != nil && strings.TrimSpace(appcnf.ProvAppConfigurator) != "" && cluster.isReplicatedEngineCluster()
}

// engineMemberAgent picks ONE agent for a new replicated member: the least loaded of the
// cluster's database agents, counting the agents the OTHER engine servers already hold,
// ties broken by list order. The member being placed is excluded from the count (it is
// already in the app list when AddSeededApp places it). Deletions do not drift the choice:
// the count is what is held now, not how many were added. Empty when the cluster has no
// agent list.
func (cluster *Cluster) engineMemberAgent(placing *config.AppConfig) string {
	agents := splitAgents(cluster.Conf.ProvAgents)
	if len(agents) == 0 {
		agents = splitAgents(cluster.Conf.ProvAppAgents)
	}
	if len(agents) == 0 {
		return ""
	}
	load := map[string]int{}
	for _, a := range cluster.appsSnapshot() {
		if a == nil || a.AppConfig == nil || a.AppConfig == placing || strings.TrimSpace(a.AppConfig.ProvAppConfigurator) == "" {
			continue
		}
		held := splitAgents(a.AppConfig.ProvAppAgents)
		if len(held) == 1 {
			load[held[0]]++
		}
	}
	best := agents[0]
	for _, ag := range agents[1:] {
		if load[ag] < load[best] {
			best = ag
		}
	}
	return best
}

// appsSnapshot copies the app list under the cluster lock: cluster.Apps is reassigned
// wholesale under that lock (newAppList, cluster_del.go).
func (cluster *Cluster) appsSnapshot() []*App {
	cluster.Lock()
	defer cluster.Unlock()
	return append([]*App(nil), cluster.Apps...)
}

func splitAgents(list string) []string {
	out := []string{}
	for _, a := range strings.Split(list, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// placeReplicatedEngineMember gives a replicated member its one agent, when the template
// left it on several (or none).
func (cluster *Cluster) placeReplicatedEngineMember(appcnf *config.AppConfig) {
	if !cluster.isReplicatedEngineMember(appcnf) {
		return
	}
	if len(splitAgents(appcnf.ProvAppAgents)) == 1 {
		return // already placed on one agent
	}
	if agent := cluster.engineMemberAgent(appcnf); agent != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlInfo, "Engine server %s of a replicated cluster is placed on %s (own data, local pool, no failover move)", appcnf.AppHost, agent)
		appcnf.ProvAppAgents = agent
	}
}

// engineMemberVolumePool: the pool a replicated member's DRBD volumes use instead, the
// cluster's data pool: a DRBD volume exists to move with an instance, and a replicated
// member never moves. Empty keeps the template's pools. Volumes on any other pool are left
// as the template says.
func (cluster *Cluster) engineMemberVolumePool(appcnf *config.AppConfig) string {
	if !cluster.isReplicatedEngineMember(appcnf) {
		return ""
	}
	return strings.TrimSpace(cluster.Conf.ProvVolumeData)
}
