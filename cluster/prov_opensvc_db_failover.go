// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	ini "gopkg.in/ini.v1"
)

// Active-passive on a failover volume (#2004). The active-passive topology has no replica:
// the HA of its single server is the volume's. Its database service is placed on the
// server's agents (failover, orchestrate ha) over a failover-capable pool
// (prov-db-volume-data-failover, DRBD), so OpenSVC moves it with its data when its node is
// lost. Replicated topologies keep one server pinned per node: replication is their HA.
// Stop, start and restart already follow the node the service runs on (placementNode).

// dbFailoverPlacement tells whether the cluster's database services are placed for
// failover: active-passive, volume disks, a failover pool set.
func (cluster *Cluster) dbFailoverPlacement() bool {
	return cluster.HasConfigTopoActivePassive() && cluster.Conf.ProvDiskType == "volume" &&
		strings.TrimSpace(cluster.Conf.ProvVolumeDataFailover) != ""
}

// dbFailoverNodes is the node list of a failover database service: the server's agent
// first, then the other agents of the server.
func (server *ServerMonitor) dbFailoverNodes() []string {
	nodes := []string{}
	seen := map[string]bool{}
	for _, n := range append([]string{server.Agent}, server.ClusterGroup.GetDatabaseAgentNames(server)...) {
		if n = strings.TrimSpace(n); n != "" && !seen[n] {
			seen[n] = true
			nodes = append(nodes, n)
		}
	}
	return nodes
}

// applyDBFailoverPlacement turns a rendered database service into a failover one: placed
// on the server's agents, the data volume on the failover pool, the volume and the
// containers shared (one instance at a time; without it the pool provisions the volume's
// filesystem on every node and the peer's mkfs on the read-only DRBD device fails, then
// its rollback wipes the peer's DRBD resource: dev3, 2026-10-12). Fewer than two agents
// is refused: an active-passive server on one node only is not active-passive.
func (server *ServerMonitor) applyDBFailoverPlacement(svcsection map[string]map[string]string) error {
	nodes := server.dbFailoverNodes()
	if len(nodes) < 2 {
		return fmt.Errorf("active-passive %s needs at least two agents to fail over (prov-db-agents), found %q", server.URL, strings.Join(nodes, ","))
	}
	def := svcsection["DEFAULT"]
	def["nodes"] = strings.Join(nodes, " ")
	delete(def, "cluster_type")
	def["rollback"] = "false"
	def["orchestrate"] = "ha"
	if vol, ok := svcsection["volume#01"]; ok {
		vol["pool"] = server.ClusterGroup.Conf.ProvVolumeDataFailover
		vol["shared"] = "true"
	}
	for name, section := range svcsection {
		if strings.HasPrefix(name, "container#") {
			section["shared"] = "true"
		}
	}
	server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Active-passive %s placed for failover on %s, data on pool %s", server.URL, def["nodes"], server.ClusterGroup.Conf.ProvVolumeDataFailover)
	return nil
}

// openSVCServicePlacedOnSeveralNodes reads the live service's DEFAULT.nodes: a template
// refresh keeps the placement the service was provisioned with.
func (cluster *Cluster) openSVCServicePlacedOnSeveralNodes(svc opensvc.Collector, serviceName string) (bool, error) {
	parts := strings.SplitN(serviceName, "/", 3)
	if len(parts) != 3 {
		return false, fmt.Errorf("invalid service name format %q, expected namespace/kind/name", serviceName)
	}
	b, err := svc.GetObjectConfigFileV3(parts[0], parts[1], parts[2])
	if err != nil {
		return false, fmt.Errorf("placement of %s unknown, template not refreshed: %w", serviceName, err)
	}
	return liveConfigPlacedOnSeveralNodes(b)
}

// liveConfigPlacedOnSeveralNodes parses a service definition's DEFAULT.nodes.
func liveConfigPlacedOnSeveralNodes(b []byte) (bool, error) {
	cfg, err := ini.Load(b)
	if err != nil {
		return false, err
	}
	return len(strings.Fields(cfg.Section("DEFAULT").Key("nodes").String())) > 1, nil
}
