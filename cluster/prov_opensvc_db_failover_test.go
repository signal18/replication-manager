// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// #2004: an active-passive database service is placed for failover on the server's
// agents over the failover pool; a replicated topology keeps it pinned to one node.
func TestDBFailoverPlacement(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{ProvType: "docker", ProvDiskType: "volume", ProvVolumeData: "zpool", ProvVolumeDataFailover: "drbd",
		ProvAgents: "s18-fr-4,s18-fr-5", TopologyTarget: config.TopoMasterSlave}
	srv := cluster.Servers[0]
	srv.ClusterGroup = cluster
	srv.Agent = "s18-fr-5"

	if cluster.dbFailoverPlacement() {
		t.Fatal("master-slave: replication is the HA, the server stays pinned")
	}
	if got := srv.OpenSVCGetDBDefaultSection()["nodes"]; got != "s18-fr-5" {
		t.Fatalf("pinned service on its agent only, got %q", got)
	}

	cluster.Conf.TopologyTarget = config.TopoActivePassive
	if !cluster.dbFailoverPlacement() {
		t.Fatal("active-passive on volumes with a failover pool is placed for failover")
	}
	sections := dbFailoverTestSections(srv)
	if err := srv.applyDBFailoverPlacement(sections, nil); err != nil {
		t.Fatal(err)
	}
	if got := sections["DEFAULT"]["nodes"]; got != "s18-fr-5 s18-fr-4" {
		t.Fatalf("nodes: the server's agent first, then the others, got %q", got)
	}
	if sections["DEFAULT"]["orchestrate"] != "ha" || sections["DEFAULT"]["cluster_type"] != "" {
		t.Fatalf("failover service orchestrated ha, got %v", sections["DEFAULT"])
	}
	if v := sections["volume#01"]; v["pool"] != "drbd" || v["shared"] != "true" {
		t.Fatalf("data volume on the failover pool, shared: %v", v)
	}
	if sections["container#db"]["shared"] != "true" || sections["container#jobs"]["shared"] != "true" {
		t.Fatal("containers run on one node at a time: shared")
	}
	if sections["container#01"]["shared"] != "" {
		t.Fatal("the pause container holds the standby's network namespace: not shared, like a failover app's")
	}

	cluster.Conf.ProvAgents = "s18-fr-5"
	if err := srv.applyDBFailoverPlacement(dbFailoverTestSections(srv), nil); err == nil {
		t.Fatal("one agent: refused, an active-passive server on one node is not active-passive")
	}

	cluster.Conf.ProvVolumeDataFailover = ""
	if cluster.dbFailoverPlacement() {
		t.Fatal("no failover pool: pinned as before")
	}
}

// A template refresh keeps the placement the live service was provisioned with: its exact
// node list, whatever prov-db-agents says now.
func TestDBFailoverRefreshKeepsLiveNodes(t *testing.T) {
	nodes, err := liveConfigNodes([]byte("[DEFAULT]\nnodes = s18-fr-4 s18-fr-5\n"))
	if err != nil || len(nodes) != 2 {
		t.Fatalf("live nodes: %v (%v)", nodes, err)
	}
	if one, _ := liveConfigNodes([]byte("[DEFAULT]\nnodes = s18-fr-4\n")); len(one) != 1 {
		t.Fatalf("a pinned service has one node, got %v", one)
	}
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{ProvType: "docker", ProvDiskType: "volume", ProvVolumeDataFailover: "drbd",
		ProvAgents: "s18-fr-4,s18-fr-5,s18-fr-6", TopologyTarget: config.TopoActivePassive}
	srv := cluster.Servers[0]
	srv.ClusterGroup = cluster
	srv.Agent = "s18-fr-4"
	sections := dbFailoverTestSections(srv)
	if err := srv.applyDBFailoverPlacement(sections, nodes); err != nil {
		t.Fatal(err)
	}
	if got := sections["DEFAULT"]["nodes"]; got != "s18-fr-4 s18-fr-5" {
		t.Fatalf("a refresh keeps the live nodes, not prov-db-agents: got %q", got)
	}
}

// dbFailoverTestSections: the real DEFAULT and data volume sections, the containers by name.
func dbFailoverTestSections(srv *ServerMonitor) map[string]map[string]string {
	return map[string]map[string]string{
		"DEFAULT":        srv.OpenSVCGetDBDefaultSection(),
		"volume#01":      srv.ClusterGroup.openSVCGetVolumeDataSection(""),
		"container#01":   {},
		"container#db":   {},
		"container#jobs": {},
	}
}
