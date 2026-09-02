// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestGetConfigProxyModuleAppendsDNSForNonRuntimeAPIModes guards the fix for
// haproxy-mode=externalcheck (and standby) never getting a "resolvers dns"/
// "init-addr" clause on their server lines: GetConfigProxyModule's
// runtimeapi branch already appended this (see prx_get.go), but the
// else-branch used by externalcheck/standby did not, so HAProxy resolved
// each server's hostname exactly once at config-parse time and never
// re-resolved it -- on Kubernetes, where a pod restart hands out a brand
// new address, this left HAProxy pointed at a dead IP until the HAProxy
// pod itself was restarted, even though repman's own check scripts (see
// refreshResolvedIP in srv.go) had already picked up the new address.
func TestGetConfigProxyModuleAppendsDNSForNonRuntimeAPIModes(t *testing.T) {
	for _, mode := range []string{"externalcheck", "standby"} {
		t.Run(mode, func(t *testing.T) {
			cluster := setupTestCluster(t, 1)
			defer cleanupTestCluster(t, cluster)
			cluster.Conf = &config.Config{HaproxyMode: mode}

			server := cluster.Servers[0]
			server.Id = "server1"
			server.ClusterGroup = cluster

			proxy := &Proxy{
				ClusterGroup: cluster,
				// A non-literal-IP Host makes HasDNS() true without needing
				// to set up Configurator tags or an orchestrator.
				Host: "haproxy.example.com",
			}
			if !proxy.HasDNS() {
				t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
			}

			read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
			write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

			const wantClause = "init-addr last,libc,none resolvers dns"
			if !strings.Contains(read, wantClause) {
				t.Errorf("read backend = %q, want it to contain %q", read, wantClause)
			}
			if !strings.Contains(write, wantClause) {
				t.Errorf("write backend = %q, want it to contain %q", write, wantClause)
			}
		})
	}
}

// TestGetConfigProxyModuleOmitsDNSWhenProxyHasNoDNS confirms the DNS clause
// is only added when proxy.HasDNS() is true, so a plain non-DNS/non-cloud
// setup (literal IP proxy host, no "dns" tag, no OpenSVC/Kubernetes
// orchestrator) keeps producing the original, resolvers-free server lines.
func TestGetConfigProxyModuleOmitsDNSWhenProxyHasNoDNS(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "externalcheck"}

	server := cluster.Servers[0]
	server.Id = "server1"
	server.ClusterGroup = cluster

	proxy := &Proxy{
		ClusterGroup: cluster,
		Host:         "127.0.0.1",
	}
	if proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be false")
	}

	read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	if strings.Contains(read, "resolvers") {
		t.Errorf("read backend = %q, want no resolvers clause when proxy.HasDNS() is false", read)
	}
	if strings.Contains(write, "resolvers") {
		t.Errorf("write backend = %q, want no resolvers clause when proxy.HasDNS() is false", write)
	}
}
