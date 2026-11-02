// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"

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
		ProvProxAgents: "s18-fr-4,s18-fr-5", ProvAgents: "s18-fr-4,s18-fr-5,s18-fr-6"}
	prx := &HaproxyProxy{Proxy: Proxy{Id: "prx1", Name: "prx1", Agent: "s18-fr-5", ClusterGroup: cluster}}
	cluster.Proxies = []DatabaseProxy{prx}

	if !cluster.proxyFailoverPlacement() {
		t.Fatal("the only proxy is placed for failover")
	}
	// a local pool: a volume on each node, nothing shared
	cluster.opensvcPoolInfoCache = []opensvc.PoolInfo{{Name: "tank", Capabilities: []string{"snap"}}, {Name: "drbd", Shared: true, Capabilities: []string{"drbd", "shared", "move"}}}
	cluster.opensvcPoolInfoCacheAt = time.Now()
	sections := proxyFailoverTestSections(prx, "tank")
	cluster.applyProxyFailoverPlacement(sections, prx)
	def := sections["DEFAULT"]
	if def["nodes"] != "s18-fr-5 s18-fr-4 s18-fr-6" {
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
	cluster.applyProxyFailoverPlacement(sections, prx)
	if sections["volume#01"]["shared"] != "true" || sections["container#prx"]["shared"] != "true" || sections["container#01"]["shared"] != "" {
		t.Fatalf("drbd volume and its containers shared, the pause container not: %v", sections)
	}

	// one agent only: the proxy stays where it is
	cluster.Conf.ProvProxAgents = "s18-fr-5"
	cluster.Conf.ProvAgents = "s18-fr-5"
	sections = proxyFailoverTestSections(prx, "")
	cluster.applyProxyFailoverPlacement(sections, prx)
	if sections["DEFAULT"]["nodes"] != "s18-fr-5" || sections["DEFAULT"]["flex_primary"] != "s18-fr-5" {
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
	cluster.Conf.ProvProxAgentsFailover = "s18-fr-6"
	if cluster.proxyFailoverPlacement() {
		t.Fatal("the legacy zpool failover placement keeps precedence")
	}
}
