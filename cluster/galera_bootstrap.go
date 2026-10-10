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
//   - else, no master ever seen: the first node rendered, as before.
//
// URLs are compared, never pointers: a config reload recreates the ServerMonitors.
func (cluster *Cluster) isGaleraBootstrapNode(server *ServerMonitor) bool {
	if server == nil || cluster.GetTopologyTarget() != config.TopoMultiMasterWsrep || !cluster.TopologyClusterDown() {
		return false
	}
	if m := cluster.GetMaster(); m != nil {
		return m.URL == server.URL
	}
	if u := cluster.lastMasterURL(); u != "" && cluster.GetServerFromURL(u) != nil {
		return u == server.URL
	}
	return true
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
