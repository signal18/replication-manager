// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"hash/crc64"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestProxySetID_DeterministicForSameInputs confirms SetID stays a pure,
// repeatable function of (cluster name, name, write port) -- callers rely
// on the same proxy resolving to the same Id across repeated calls (e.g. a
// config reload rebuilding cluster.Proxies from scratch), and across
// upgrades: the hash never changes shape, so a proxy's Id never changes on
// restart/reprovision.
func TestProxySetID_DeterministicForSameInputs(t *testing.T) {
	cluster := &Cluster{Name: "clustera", Conf: &config.Config{}}
	cluster.crcTable = crc64.MakeTable(crc64.ECMA)

	p1 := &Proxy{ClusterGroup: cluster, Type: config.ConstProxyHaproxy, Name: "haproxy1", WritePort: 3306}
	p2 := &Proxy{ClusterGroup: cluster, Type: config.ConstProxyHaproxy, Name: "haproxy1", WritePort: 3306}

	p1.SetID()
	p2.SetID()

	if p1.Id != p2.Id {
		t.Fatalf("expected identical Ids for identical (cluster, name, write port), got %q vs %q", p1.Id, p2.Id)
	}
}

// TestAddProxy_BlocksIdCollision guards a real risk: SetID hashes only
// cluster name + proxy name + write port, with no type -- two different
// proxy families sharing the same host and write port within one cluster
// (e.g. haproxy-write-port and proxysql-port both default to 3306) would
// get the identical Id, and GetProxyFromName (prx_get.go) would then
// silently resolve API actions against whichever one happened to come
// first in cluster.Proxies instead of the one the caller meant. AddProxy
// blocks the second registration instead, so cluster.Proxies never holds
// two entries with the same Id.
func TestAddProxy_BlocksIdCollision(t *testing.T) {
	cluster := &Cluster{Name: "clustera", Conf: &config.Config{}}
	cluster.crcTable = crc64.MakeTable(crc64.ECMA)

	haproxy := &Proxy{Type: config.ConstProxyHaproxy, Name: "127.0.0.1", WritePort: 3306}
	proxysql := &Proxy{Type: config.ConstProxySqlproxy, Name: "127.0.0.1", WritePort: 3306}

	cluster.AddProxy(haproxy)
	cluster.AddProxy(proxysql)

	if len(cluster.Proxies) != 1 {
		t.Fatalf("expected the colliding proxy to be blocked, got %d proxies registered", len(cluster.Proxies))
	}
	if cluster.Proxies[0].GetType() != config.ConstProxyHaproxy {
		t.Fatalf("expected the first-registered proxy (haproxy) to win, got %q", cluster.Proxies[0].GetType())
	}
}

// TestAddProxy_AllowsSameNameDifferentTypeOnDistinctPorts guards against
// over-blocking: AddProxy must NOT reject two different-type proxies that
// happen to share a name as long as their write ports differ (a normal,
// valid setup -- e.g. HAProxy and ProxySQL colocated on the same host,
// which must run on different ports to coexist at all). An earlier version
// of this guard also compared Name alone and wrongly rejected this case;
// the actual Kubernetes/OpenSVC object-name collision that setup can cause
// is now caught at the orchestrator layer instead
// (k8sProxyNameOwnedByDifferentType, prov_k8s_prx.go), which has the
// context AddProxy doesn't to know whether it's actually going to collide.
func TestAddProxy_AllowsSameNameDifferentTypeOnDistinctPorts(t *testing.T) {
	cluster := &Cluster{Name: "clustera", Conf: &config.Config{}}
	cluster.crcTable = crc64.MakeTable(crc64.ECMA)

	haproxy := &Proxy{Type: config.ConstProxyHaproxy, Name: "127.0.0.1", WritePort: 3306}
	proxysql := &Proxy{Type: config.ConstProxySqlproxy, Name: "127.0.0.1", WritePort: 6033}

	cluster.AddProxy(haproxy)
	cluster.AddProxy(proxysql)

	if len(cluster.Proxies) != 2 {
		t.Fatalf("expected both proxies to register (same name, distinct write ports is valid), got %d proxies registered", len(cluster.Proxies))
	}
}
