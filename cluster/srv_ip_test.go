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

// TestRefreshResolvedIPSkipsLiteralIP guards against a wasted (or, worse,
// self-overwriting-with-itself) DNS lookup when Host is already a literal
// IP -- dbhelper.CheckHostAddr already short-circuits this, so this just
// locks in that IP stays untouched either way.
func TestRefreshResolvedIPSkipsLiteralIP(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{}

	server := cluster.Servers[0]
	server.ClusterGroup = cluster
	server.Host = "203.0.113.5"
	server.IP = "old-should-not-change"

	server.refreshResolvedIP()
	if server.IP != "203.0.113.5" {
		t.Fatalf("IP = %q, want %q (Host is already a literal IP)", server.IP, "203.0.113.5")
	}
}

// TestRefreshResolvedIPSkipsEmptyHost guards the other no-op case: no Host
// configured at all, nothing to resolve.
func TestRefreshResolvedIPSkipsEmptyHost(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{}

	server := cluster.Servers[0]
	server.ClusterGroup = cluster
	server.Host = ""
	server.IP = "unchanged"

	server.refreshResolvedIP()
	if server.IP != "unchanged" {
		t.Fatalf("IP = %q, want unchanged (no Host configured)", server.IP)
	}
}

// TestRefreshResolvedIPUpdatesFromHostname is the actual bug guard: this is
// the exact mechanism that was missing entirely before this fix --
// SetCredential() (cluster/srv_set.go) resolves IP via this same
// dbhelper.CheckHostAddr() call, but only once, at server setup, so it goes
// stale the moment a server's real address changes -- which is exactly what
// let haproxy-mode=externalcheck's checkmaster/checkslave lookups
// (GetServerFromURL, cluster/cluster_get.go, matching on IP) fail
// indefinitely, live-reproduced against a Kubernetes Deployment where every
// pod recreation gets a brand-new overlay IP. "localhost" is used here
// instead of a real network hostname so this test has no external
// dependency: it resolves via /etc/hosts (or the platform equivalent) on
// every machine, no DNS or network access needed.
func TestRefreshResolvedIPUpdatesFromHostname(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{}

	server := cluster.Servers[0]
	server.ClusterGroup = cluster
	server.Host = "localhost"
	server.IP = "stale-should-be-replaced"

	server.refreshResolvedIP()
	if server.IP == "stale-should-be-replaced" {
		t.Fatalf("IP was not refreshed from a resolvable hostname")
	}
	if server.IP != "127.0.0.1" && server.IP != "::1" {
		t.Fatalf("IP = %q, want a loopback address resolved from \"localhost\"", server.IP)
	}
}

// TestRefreshResolvedIPPreservesLastKnownOnResolutionFailure guards the
// "best-effort" half of the contract: a resolution failure (e.g. a
// transiently-unreachable resolver, or a hostname that stops existing for a
// moment) must never clear IP to "" or otherwise corrupt it -- the last
// known-good address should stay in place until a resolution actually
// succeeds, since a healthy server temporarily losing its cached IP would
// break GetServerFromURL's match just as badly as never having refreshed it
// at all.
func TestRefreshResolvedIPPreservesLastKnownOnResolutionFailure(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{}

	server := cluster.Servers[0]
	server.ClusterGroup = cluster
	server.Host = "this-hostname-should-never-resolve.invalid"
	server.IP = "10.0.0.1"

	server.refreshResolvedIP()
	if server.IP != "10.0.0.1" {
		t.Fatalf("IP = %q, want the last known-good address preserved on resolution failure", server.IP)
	}
}
