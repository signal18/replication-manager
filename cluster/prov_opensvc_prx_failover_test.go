// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
)

func proxyFailoverTestSections(prx DatabaseProxy, pool string) map[string]map[string]string {
	sections := map[string]map[string]string{
		"DEFAULT":       prx.OpenSVCGetProxyDefaultSection(),
		"container#01":  {},
		"container#prx": {},
		"env":           {},
	}
	if pool != "" {
		sections["volume#01"] = map[string]string{"name": "{name}", "pool": pool}
	}
	return sections
}

// #2289: a cluster's only proxy is placed for failover on its agent then the other agents;
// two proxies stay pinned, one per node; one agent only keeps the proxy where it is.
func TestProxyFailoverPlacement(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{ProvType: "docker", ProvProxDiskType: "volume", ProvProxySingleFailover: true,
		ProvProxAgents: "node1,node2", ProvAgents: "node1,node2,node3"}
	prx := &HaproxyProxy{Proxy: Proxy{Id: "prx1", Name: "prx1", Agent: "node2", ClusterGroup: cluster}}
	cluster.Proxies = []DatabaseProxy{prx}

	if !cluster.proxyFailoverPlacement() {
		t.Fatal("the only proxy is placed for failover")
	}
	// a local pool: a volume on each node, nothing shared
	pools := []opensvc.PoolInfo{{Name: "tank", Capabilities: []string{"snap"}}, {Name: "drbd", Shared: true, Capabilities: []string{"drbd", "shared", "move"}}}
	sections := proxyFailoverTestSections(prx, "tank")
	cluster.applyProxyFailoverPlacement(sections, prx, pools, nil)
	def := sections["DEFAULT"]
	if def["nodes"] != "node2 node1 node3" {
		t.Fatalf("nodes: its agent, the proxy agents, then the database agents, got %q", def["nodes"])
	}
	if def["orchestrate"] != "ha" || def["rollback"] != "false" || def["flex_primary"] != "" || def["cluster_type"] != "" {
		t.Fatalf("failover service orchestrated ha, no flex primary: %v", def)
	}
	if sections["env"]["nodes"] != def["nodes"] {
		t.Fatal("env.nodes follows DEFAULT.nodes")
	}
	if sections["volume#01"]["shared"] != "" || sections["container#prx"]["shared"] != "" {
		t.Fatal("a local volume is provisioned on each node: not shared")
	}

	// a failover pool: the volume moves with the proxy, shared, as on the database path
	sections = proxyFailoverTestSections(prx, "drbd")
	cluster.applyProxyFailoverPlacement(sections, prx, pools, nil)
	if sections["volume#01"]["shared"] != "true" || sections["container#prx"]["shared"] != "true" || sections["container#01"]["shared"] != "" {
		t.Fatalf("drbd volume and its containers shared, the pause container not: %v", sections)
	}

	// the pool list could not be read: the volume's pool type is unknown, the proxy stays pinned
	sections = proxyFailoverTestSections(prx, "tank")
	cluster.applyProxyFailoverPlacement(sections, prx, nil, errors.New("orchestrator unreachable"))
	if sections["DEFAULT"]["nodes"] != "node2" || sections["DEFAULT"]["flex_primary"] != "node2" || sections["env"]["nodes"] != "" {
		t.Fatalf("pools unknown: pinned as before, got %v", sections["DEFAULT"])
	}

	// one agent only: the proxy stays where it is
	cluster.Conf.ProvProxAgents = "node2"
	cluster.Conf.ProvAgents = "node2"
	sections = proxyFailoverTestSections(prx, "")
	cluster.applyProxyFailoverPlacement(sections, prx, nil, nil)
	if sections["DEFAULT"]["nodes"] != "node2" || sections["DEFAULT"]["flex_primary"] != "node2" {
		t.Fatalf("one agent: pinned as before, got %v", sections["DEFAULT"])
	}

	// two proxies are each other's HA; the switch off and the legacy zpool placement win too
	cluster.Proxies = []DatabaseProxy{prx, &HaproxyProxy{Proxy: Proxy{Id: "prx2", Name: "prx2", ClusterGroup: cluster}}}
	if cluster.proxyFailoverPlacement() {
		t.Fatal("two proxies stay pinned")
	}
	cluster.Proxies = []DatabaseProxy{prx}
	cluster.Conf.ProvProxySingleFailover = false
	if cluster.proxyFailoverPlacement() {
		t.Fatal("prov-proxy-single-failover off")
	}
	cluster.Conf.ProvProxySingleFailover = true
	cluster.Conf.ProvProxDiskPool = "zpool"
	cluster.Conf.ProvProxAgentsFailover = "node3"
	if cluster.proxyFailoverPlacement() {
		t.Fatal("the legacy zpool failover placement keeps precedence")
	}
}

// failoverNodes: the agent first, then the others, each once, blanks dropped (an empty
// prov-proxy-agents splits into one empty entry).
func TestFailoverNodes(t *testing.T) {
	got := failoverNodes(" node2 ", strings.Split("", ","), []string{"node1", "node2", " ", "node3"}, []string{"node1"})
	if strings.Join(got, " ") != "node2 node1 node3" {
		t.Fatalf("got %q", got)
	}
	if got := failoverNodes("", []string{"node1", "node2"}); strings.Join(got, " ") != "node1 node2" {
		t.Fatalf("no agent: the lists in order, got %q", got)
	}
	if got := failoverNodes("", nil); len(got) != 0 {
		t.Fatalf("no agent, no list: no node, got %q", got)
	}
}
