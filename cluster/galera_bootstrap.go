// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
	ini "gopkg.in/ini.v1"
)

// galeraNameWaitSeconds is how long a Galera node's start waits for its own name.
const galeraNameWaitSeconds = 60

// galeraNameWaitCommand is the blocking_pre_start of a Galera node's container#db: a name is
// published asynchronously after the orchestrator gives the node its address, and a joiner that
// starts before that gets no state transfer (the donor cannot reach it by name) and aborts. The
// final dot asks for that exact name: without it the host's search domains can answer for a name
// not published yet, which is how wait_dns of OpenSVC v3 ends at once. The wait ends after
// galeraNameWaitSeconds and the start goes on: it never blocks a start. "" for an address, which
// has no name to wait for.
func galeraNameWaitCommand(host string) string {
	host = misc.Unbracket(host)
	if host == "" || net.ParseIP(host) != nil {
		return ""
	}
	return fmt.Sprintf("sh -c 'i=0; while [ $i -lt %d ]; do getent hosts %s. >/dev/null && exit 0; i=$((i+1)); sleep 1; done; echo %s not resolvable after %ds, starting anyway >&2'",
		galeraNameWaitSeconds, host, host, galeraNameWaitSeconds)
}

// galeraNameWaitPatch sets the wait on a service provisioned before it existed. A
// blocking_pre_start other than this one is the operator's and is left alone.
func galeraNameWaitPatch(section *ini.Section, host string) (changed bool) {
	want := galeraNameWaitCommand(host)
	if want == "" || section.HasKey("blocking_pre_start") {
		return false
	}
	section.Key("blocking_pre_start").SetValue(want)
	return true
}

// isGaleraBootstrapNode tells whether server starts the Galera cluster
// (--wsrep_new_cluster) when the whole cluster is down:
//   - the master still known (GetMaster): a remembered vmaster used to make GetMaster
//     non-nil, so NO node bootstrapped and the cluster could not restart;
//   - lastmaster, the last node seen when the cluster went down (TopologyClusterDown),
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
//
// lastmaster comes first: GetMaster can still answer with a failed virtual master that left
// before the others, the one node Galera will not let bootstrap (safe_to_bootstrap: 0).
func (cluster *Cluster) galeraBootstrapURL() string {
	if u := cluster.lastMasterURL(); u != "" && cluster.GetServerFromURL(u) != nil {
		return u
	}
	if m := cluster.GetMaster(); m != nil {
		return m.URL
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

// trackGaleraLastNodeSeen keeps lastmaster on the node seen last while a Galera cluster goes down.
// The master moves to a surviving node given the time, but nodes lost within its failure retries
// leave the first one stopped as master, and the last node to leave is the one Galera lets
// bootstrap. alive is the replicas still up while the master has failed, nil with a healthy
// master. One survivor is that node; with several, or a healthy master, there is none yet: an
// earlier partial outage must not name the node of a later one.
func (cluster *Cluster) trackGaleraLastNodeSeen(alive []*ServerMonitor) {
	if cluster.GetTopologyTarget() != config.TopoMultiMasterWsrep {
		return
	}
	cluster.galeraLastNodeUnknown.Store(len(alive) > 1)
	if len(alive) == 1 {
		cluster.lastmaster = alive[0]
	} else if cluster.lastmaster != nil {
		cluster.lastmaster = nil
	}
}

// keepsGaleraLastNodeSeen: the cluster is entirely down and lastmaster already holds the last
// Galera node seen (trackGaleraLastNodeSeen), which the failed master must not overwrite.
func (cluster *Cluster) keepsGaleraLastNodeSeen() bool {
	return cluster.GetTopologyTarget() == config.TopoMultiMasterWsrep && cluster.lastmaster != nil
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
