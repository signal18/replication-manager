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

// TestProxySetID_DistinctTypesSameNameAndPortDoNotCollide guards a real
// collision risk: SetID used to hash only cluster name + proxy name +
// write port, with no type -- two different proxy families sharing the
// same host and write port within one cluster (e.g. both configured as
// "127.0.0.1", the standby-mode convention this codebase's own clusterin
// test config uses) would get the identical Id, and GetProxyFromName
// would silently resolve API actions against whichever one happened to
// come first in cluster.Proxies instead of the one the caller meant.
func TestProxySetID_DistinctTypesSameNameAndPortDoNotCollide(t *testing.T) {
	cluster := &Cluster{Name: "clustera", Conf: &config.Config{}}
	cluster.crcTable = crc64.MakeTable(crc64.ECMA)

	haproxy := &Proxy{ClusterGroup: cluster, Type: config.ConstProxyHaproxy, Name: "127.0.0.1", WritePort: 3306}
	proxysql := &Proxy{ClusterGroup: cluster, Type: config.ConstProxySqlproxy, Name: "127.0.0.1", WritePort: 3306}

	haproxy.SetID()
	proxysql.SetID()

	if haproxy.Id == "" || proxysql.Id == "" {
		t.Fatalf("expected non-empty Ids, got haproxy=%q proxysql=%q", haproxy.Id, proxysql.Id)
	}
	if haproxy.Id == proxysql.Id {
		t.Fatalf("expected distinct Ids for different proxy types sharing name+write port, got the same Id %q for both", haproxy.Id)
	}
}

// TestProxySetID_DeterministicForSameInputs confirms SetID stays a pure,
// repeatable function of (cluster name, type, name, write port) -- callers
// rely on the same proxy resolving to the same Id across repeated calls
// (e.g. a config reload rebuilding cluster.Proxies from scratch).
func TestProxySetID_DeterministicForSameInputs(t *testing.T) {
	cluster := &Cluster{Name: "clustera", Conf: &config.Config{}}
	cluster.crcTable = crc64.MakeTable(crc64.ECMA)

	p1 := &Proxy{ClusterGroup: cluster, Type: config.ConstProxyHaproxy, Name: "haproxy1", WritePort: 3306}
	p2 := &Proxy{ClusterGroup: cluster, Type: config.ConstProxyHaproxy, Name: "haproxy1", WritePort: 3306}

	p1.SetID()
	p2.SetID()

	if p1.Id != p2.Id {
		t.Fatalf("expected identical Ids for identical (cluster, type, name, write port), got %q vs %q", p1.Id, p2.Id)
	}
}
