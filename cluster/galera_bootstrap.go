// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"

	"github.com/signal18/replication-manager/config"
)

// isGaleraBootstrapNode tells whether server starts the Galera cluster
// (--wsrep_new_cluster) when the whole cluster is down:
//   - the master still known (GetMaster): a remembered vmaster used to make GetMaster
//     non-nil, so NO node bootstrapped and the cluster could not restart;
//   - else lastmaster, the master seen when the cluster went down (TopologyClusterDown),
//     from memory or from clusterstate.json after a restart of replication-manager;
//   - else, no master ever seen: the first declared server, never "any node": the empty
//     member list (GetGComm) and the start option are rendered separately, each node
//     answering true bootstrapped its own cluster (#2191).
//
// During a full provision the node chosen when it started is kept (galeraProvisionBootstrap):
// the discovery running meanwhile can set another master, and the node started first and
// the node rendered with the empty list would differ again.
//
// URLs are compared, never pointers: a config reload recreates the ServerMonitors.
func (cluster *Cluster) isGaleraBootstrapNode(server *ServerMonitor) bool {
	if server == nil || cluster.GetTopologyTarget() != config.TopoMultiMasterWsrep || !cluster.TopologyClusterDown() {
		return false
	}
	if u := cluster.galeraProvisionBootstrap.Load(); u != nil {
		return *u == server.URL
	}
	return cluster.galeraBootstrapURL() == server.URL
}

// galeraBootstrapURL is the URL of the node a full Galera start bootstraps (see
// isGaleraBootstrapNode), "" with no server.
func (cluster *Cluster) galeraBootstrapURL() string {
	if m := cluster.GetMaster(); m != nil {
		return m.URL
	}
	if u := cluster.lastMasterURL(); u != "" && cluster.GetServerFromURL(u) != nil {
		return u
	}
	if len(cluster.Servers) > 0 {
		return cluster.Servers[0].URL
	}
	return ""
}

// pinGaleraBootstrap keeps the bootstrap node for the length of a full provision, when the
// cluster is down at its start; the returned function releases it.
func (cluster *Cluster) pinGaleraBootstrap() func() {
	if cluster.GetTopologyTarget() != config.TopoMultiMasterWsrep || !cluster.TopologyClusterDown() {
		return func() {}
	}
	u := cluster.galeraBootstrapURL()
	if u == "" {
		return func() {}
	}
	cluster.galeraProvisionBootstrap.Store(&u)
	return func() { cluster.galeraProvisionBootstrap.Store(nil) }
}

// galeraProvisionOrder is the order a full provision starts the servers in: the Galera
// bootstrap node first, the others in declared order. The provision is serialized and
// each joiner waits pc.wait_prim_timeout for a Primary component: a joiner started
// before the bootstrap node aborts ("failed to reach primary view", #2191). Other
// topologies, or no bootstrap node (the cluster is up): the declared order.
func (cluster *Cluster) galeraProvisionOrder() []*ServerMonitor {
	for i, s := range cluster.Servers {
		if cluster.isGaleraBootstrapNode(s) {
			order := append([]*ServerMonitor{s}, cluster.Servers[:i]...)
			return append(order, cluster.Servers[i+1:]...)
		}
	}
	return cluster.Servers
}

// lastMasterURL is lastmaster's URL, from memory, else as saved in clusterstate.json
// (read only here, on a full cluster start).
func (cluster *Cluster) lastMasterURL() string {
	if cluster.lastmaster != nil {
		return cluster.lastmaster.URL
	}
	b, err := os.ReadFile(cluster.WorkingDir + "/clusterstate.json")
	if err != nil {
		return ""
	}
	var st ClusterState
	if json.Unmarshal(b, &st) != nil {
		return ""
	}
	return st.LastMaster
}
