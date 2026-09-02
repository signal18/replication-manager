// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestRefreshResolvedIPSkipsLiteralIP confirms refreshResolvedIP is a no-op
// (beyond the short-circuit dbhelper.CheckHostAddr already does) when Host
// is already a literal IP -- nothing to resolve, and IP should already hold
// the same value from SetCredential's initial resolution.
func TestRefreshResolvedIPSkipsLiteralIP(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{DNSTimeout: 1}

	server := cluster.Servers[0]
	server.Host = "127.0.0.1"
	server.IP = "127.0.0.1"
	server.ClusterGroup = cluster

	server.refreshResolvedIP()

	if server.IP != "127.0.0.1" {
		t.Errorf("server.IP = %q, want unchanged %q", server.IP, "127.0.0.1")
	}
}

// TestRefreshResolvedIPSkipsEmptyHost confirms refreshResolvedIP doesn't
// panic or attempt a lookup for a server with no Host set at all.
func TestRefreshResolvedIPSkipsEmptyHost(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{DNSTimeout: 1}

	server := cluster.Servers[0]
	server.Host = ""
	server.IP = ""
	server.ClusterGroup = cluster

	server.refreshResolvedIP()

	if server.IP != "" {
		t.Errorf("server.IP = %q, want it to stay empty for an empty Host", server.IP)
	}
}

// TestRefreshResolvedIPUpdatesFromHostname confirms a resolvable hostname
// updates IP to the freshly-resolved address -- the actual mechanism that
// keeps ServerMonitor.IP (and therefore RuntimeAPIAddr) current after a pod
// restart hands a Kubernetes/OpenSVC server a new address.
func TestRefreshResolvedIPUpdatesFromHostname(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{DNSTimeout: 2}

	server := cluster.Servers[0]
	// "localhost" reliably resolves to a loopback address in any test
	// environment without relying on external/flaky DNS.
	server.Host = "localhost"
	server.IP = "192.0.2.1" // deliberately stale/wrong, to prove it gets corrected
	server.ClusterGroup = cluster

	server.refreshResolvedIP()

	if server.IP == "192.0.2.1" {
		t.Errorf("server.IP = %q, want it updated away from the stale placeholder", server.IP)
	}
	if server.IP != "127.0.0.1" && server.IP != "::1" {
		t.Errorf("server.IP = %q, want a loopback address resolved from %q", server.IP, "localhost")
	}
}

// TestRefreshResolvedIPPreservesLastKnownOnResolutionFailure confirms a
// resolution failure is a no-op, never clearing a last-known-good IP --
// refreshResolvedIP must never turn a transient resolver hiccup into a new
// failure mode for the server's runtime address.
func TestRefreshResolvedIPPreservesLastKnownOnResolutionFailure(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{DNSTimeout: 1}

	server := cluster.Servers[0]
	server.Host = "this-hostname-should-never-resolve.invalid"
	server.IP = "10.0.0.5"
	server.ClusterGroup = cluster

	server.refreshResolvedIP()

	if server.IP != "10.0.0.5" {
		t.Errorf("server.IP = %q, want the last-known-good value preserved on resolution failure", server.IP)
	}
}
