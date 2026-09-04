// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
package cluster

import (
	"hash/crc64"
	"os"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestNewProxyListSeedsNoConfigFetchCookieWhenDisabled guards against a
// cluster started with prov-proxy-start-fetch-config=false never getting
// the no-fetch cookie seeded on its proxies until an operator later toggles
// the setting through the API (switchClusterSettings/setClusterSetting,
// server/api_cluster.go, the only other CheckNeedConfigFetch() callers for
// proxies). newServerMonitor already does the equivalent for servers via
// server.CheckNeedConfigFetch() (cluster/srv.go); proxies had no such
// startup seeding before this fix.
func TestNewProxyListSeedsNoConfigFetchCookieWhenDisabled(t *testing.T) {
	cluster := &Cluster{Name: "ahmadcluster", crcTable: crc64.MakeTable(crc64.ECMA)}
	cluster.Conf = &config.Config{
		WorkingDir:                t.TempDir(),
		HaproxyOn:                 true,
		HaproxyHosts:              "127.0.0.1",
		ProvProxyStartFetchConfig: false,
	}

	if err := cluster.newProxyList(); err != nil {
		t.Fatalf("newProxyList() error = %v", err)
	}
	if len(cluster.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(cluster.Proxies))
	}

	if !cluster.Proxies[0].HasNoConfigFetchCookie() {
		t.Fatalf("newProxyList() did not seed the no-config-fetch cookie for a proxy created with prov-proxy-start-fetch-config=false")
	}
}

// TestNewProxyListRegistersColocatedProxiesOfDifferentTypes is a
// caller-level regression test for AddProxy's Name-collision behavior: an
// operator configuring HAProxy and ProxySQL on the same host (a normal,
// valid setup, provided their write ports differ) must end up with both
// proxies in cluster.Proxies. AddProxy used to also block same-name
// different-type registrations outright, which would have silently dropped
// ProxySQL here even though newProxyList()'s own (void, per proxy) calls to
// AddProxy give it no way to notice.
func TestNewProxyListRegistersColocatedProxiesOfDifferentTypes(t *testing.T) {
	cluster := &Cluster{Name: "ahmadcluster", crcTable: crc64.MakeTable(crc64.ECMA)}
	cluster.Conf = &config.Config{
		WorkingDir:    t.TempDir(),
		HaproxyOn:     true,
		HaproxyHosts:  "127.0.0.1",
		ProxysqlOn:    true,
		ProxysqlHosts: "127.0.0.1",
		ProxysqlPort:  "6033",
	}

	if err := cluster.newProxyList(); err != nil {
		t.Fatalf("newProxyList() error = %v", err)
	}
	if len(cluster.Proxies) != 2 {
		t.Fatalf("expected both colocated proxies (same host, distinct write ports) to register, got %d", len(cluster.Proxies))
	}
}

// TestNewProxyListLeavesConfigFetchEnabledByDefault is
// TestNewProxyListSeedsNoConfigFetchCookieWhenDisabled's counterpart: the
// default (prov-proxy-start-fetch-config=true) must not leave a stray
// no-fetch cookie behind.
func TestNewProxyListLeavesConfigFetchEnabledByDefault(t *testing.T) {
	cluster := &Cluster{Name: "ahmadcluster", crcTable: crc64.MakeTable(crc64.ECMA)}
	cluster.Conf = &config.Config{
		WorkingDir:                t.TempDir(),
		HaproxyOn:                 true,
		HaproxyHosts:              "127.0.0.1",
		ProvProxyStartFetchConfig: true,
	}

	if err := cluster.newProxyList(); err != nil {
		t.Fatalf("newProxyList() error = %v", err)
	}
	if len(cluster.Proxies) != 1 {
		t.Fatalf("expected 1 proxy, got %d", len(cluster.Proxies))
	}

	if cluster.Proxies[0].HasNoConfigFetchCookie() {
		t.Fatalf("newProxyList() seeded a no-config-fetch cookie despite prov-proxy-start-fetch-config=true")
	}

	if _, err := os.Stat(cluster.Proxies[0].(*HaproxyProxy).Datadir); err != nil {
		t.Fatalf("expected proxy datadir to exist: %v", err)
	}
}
