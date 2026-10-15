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
// ColumnStore is the exception (#2021): each server holds a dbroot of the shared data,
// none is a copy of another, so each is its own failover service, every dbroot alive.
// Stop, start and restart already follow the node the service runs on (placementNode).

// dbFailoverPlacement tells whether the cluster's database services are placed for
// failover: active-passive or columnstore, volume disks, a failover pool set.
func (cluster *Cluster) dbFailoverPlacement() bool {
	return (cluster.HasConfigTopoActivePassive() || cluster.HasConfigTopoColumnstore()) && cluster.Conf.ProvDiskType == "volume" &&
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
// on the server's agents (or on nodes, the live service's: a refresh never moves a service
// onto a node its volume is not on), the data volume on the failover pool, the volume and
// the service's containers shared (one instance at a time; without it the pool provisions the
// volume's filesystem on every node and the peer's mkfs on the read-only DRBD device fails,
// then its rollback wipes the peer's DRBD resource: dev3, 2026-10-12). Fewer than two
// nodes is refused: a failover server on one node only does not fail over.
func (server *ServerMonitor) applyDBFailoverPlacement(svcsection map[string]map[string]string, nodes []string) error {
	if len(nodes) == 0 {
		nodes = server.dbFailoverNodes()
	}
	if len(nodes) < 2 {
		return fmt.Errorf("failover database service %s needs at least two agents (prov-db-agents), found %q", server.URL, strings.Join(nodes, ","))
	}
	def := svcsection["DEFAULT"]
	def["nodes"] = strings.Join(nodes, " ")
	delete(def, "cluster_type")
	def["rollback"] = "false"
	def["orchestrate"] = "ha"
	if env, ok := svcsection["env"]; ok {
		env["nodes"] = def["nodes"] // informational, the placement is DEFAULT.nodes
	}
	if vol, ok := svcsection["volume#01"]; ok {
		vol["pool"] = server.ClusterGroup.Conf.ProvVolumeDataFailover
		vol["shared"] = "true"
	}
	for name, section := range svcsection {
		// container#01 is the pause holding the network namespace: like a failover app's,
		// it is not shared, the standby provisions ip#01 (not shared) into it; shared, the
		// standby's provision failed entering a namespace not running there (dev3 cstest)
		if strings.HasPrefix(name, "container#") && name != "container#01" {
			section["shared"] = "true"
		}
	}
	server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Database service %s placed for failover on %s, data on pool %s", server.URL, def["nodes"], server.ClusterGroup.Conf.ProvVolumeDataFailover)
	return nil
}

// openSVCServiceNodes reads the live service's DEFAULT.nodes: a template refresh keeps the
// placement the service was provisioned with (one node: pinned, its data is there only;
// several: failover on exactly those nodes, where its volume is).
func (cluster *Cluster) openSVCServiceNodes(svc opensvc.Collector, serviceName string) ([]string, error) {
	parts := strings.SplitN(serviceName, "/", 3)
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid service name format %q, expected namespace/kind/name", serviceName)
	}
	b, err := svc.GetObjectConfigFileV3(parts[0], parts[1], parts[2])
	if err != nil {
		return nil, fmt.Errorf("placement of %s unknown, template not refreshed: %w", serviceName, err)
	}
	return liveConfigNodes(b)
}

// liveConfigNodes parses a service definition's DEFAULT.nodes.
func liveConfigNodes(b []byte) ([]string, error) {
	cfg, err := ini.Load(b)
	if err != nil {
		return nil, err
	}
	return strings.Fields(cfg.Section("DEFAULT").Key("nodes").String()), nil
}
