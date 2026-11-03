// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

// A single proxy placed for failover (#2289), the proxy side of #2004. With one proxy, the
// application's only way to the database is that proxy: pinned to one node, it is lost with
// its node. Its service is placed on its agent then the other agents (prov-proxy-agents, then
// the database agents), one instance at a time, and OpenSVC restarts it on the next node.
// A proxy is stateless: the init container fetches its configuration from repman at every
// start (prov-proxy-start-fetch-config), so a local volume on each node is enough; on a
// failover pool (DRBD) the volume moves with it, shared, as on the database path. The
// service name does not change, so <proxy>.<cluster>.svc.<orchestrator cluster> follows it.
// Two proxies or more keep one pinned per node: they are each other's HA. The legacy
// zpool + prov-proxy-agents-failover placement keeps precedence. Applied to the v3 template
// only, as the database placement.

// proxyFailoverPlacement tells whether the cluster's proxy is placed for failover: its only
// proxy, prov-proxy-single-failover on, the legacy failover placement not set.
func (cluster *Cluster) proxyFailoverPlacement() bool {
	return cluster.Conf.ProvProxySingleFailover && len(cluster.Proxies) == 1 &&
		!(cluster.Conf.ProvProxDiskPool == "zpool" && cluster.Conf.ProvProxAgentsFailover != "")
}

// proxyFailoverNodes is the node list of a single proxy's failover service: its agent first,
// then the proxy agents, then the database agents.
func (cluster *Cluster) proxyFailoverNodes(agent string) []string {
	return failoverNodes(agent, strings.Split(cluster.Conf.ProvProxAgents, ","), cluster.GetDatabaseAgentNames(nil))
}

// applyProxyFailoverPlacement places a rendered single-proxy service for failover, pools
// being the OpenSVC pool list (poolsErr its lookup error). Fewer than two nodes, or a pool
// list repman could not read, keeps the proxy pinned to its agent as before: a cluster on
// one node still gets its proxy.
func (cluster *Cluster) applyProxyFailoverPlacement(svcsection map[string]map[string]string, pri DatabaseProxy, pools []opensvc.PoolInfo, poolsErr error) {
	nodes := cluster.proxyFailoverNodes(pri.GetAgent())
	if len(nodes) < 2 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"Proxy service %s stays on %s: a single proxy needs a second agent to fail over", pri.GetName(), pri.GetAgent())
		return
	}
	movingVolume := false
	if vol, ok := svcsection["volume#01"]; ok {
		if poolsErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
				"Proxy service %s stays on %s: OpenSVC pools unknown (%s)", pri.GetName(), pri.GetAgent(), poolsErr)
			return
		}
		poolSet := make(map[string]opensvc.PoolInfo, len(pools))
		for _, p := range pools {
			poolSet[p.Name] = p
		}
		movingVolume = isFailoverPool(poolSet, vol["pool"])
	}
	delete(svcsection["DEFAULT"], "flex_primary")
	placeServiceForFailover(svcsection, nodes, movingVolume)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Proxy service %s placed for failover on %s (volume moving with it: %t)", pri.GetName(), svcsection["DEFAULT"]["nodes"], movingVolume)
}
