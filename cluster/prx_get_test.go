// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"os"
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

// TestGetConfigProxyModuleRuntimeAPIUsesPlaceholderNotResolvers guards the
// fix for Bug 5b/N4 (haproxy-mode=runtimeapi's dynamic read-backend
// reconciliation, reconcileReadBackendServers in cluster/prx_haproxy.go,
// being structurally inert on Kubernetes/OpenSVC): runtimeapi drives every
// backend member's address itself over the Runtime API via
// ServerMonitor.RuntimeAPIAddr, so it doesn't need HAProxy's own background
// DNS re-resolution the way externalcheck/standby do (see
// TestGetConfigProxyModuleAppendsDNSForNonRuntimeAPIModes) — and
// "resolvers dns" is exactly what makes a server line resolver-attached,
// which blocks "add server" and gets "del server" refused by HAProxy.
//
// Rendering db.Host (the FQDN) with "init-addr none" instead was tried and
// live-reproduced as broken: HAProxy leaves such a server with no address
// family assigned at all, and its Runtime API then refuses the very first
// "set server addr" against it ("Update for the current server address
// family is only supported through configuration file.") — exactly the
// command reconcileReadBackendServers/the write-path SetMaster calls need
// to correct it. 192.0.2.1 (RFC 5737 TEST-NET-1) is used as the config-time
// address instead: a real IPv4 literal gives the line a real address family
// from the start, so a later "set server addr" to the real resolved IP is
// an update within the same family, which HAProxy does allow at runtime —
// no "init-addr"/"resolvers" clause is needed at all once the address is
// already literal.
func TestGetConfigProxyModuleRuntimeAPIUsesPlaceholderNotResolvers(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi", HaproxyAPIBootstrapServers: true}

	server := cluster.Servers[0]
	server.Id = "server1"
	server.ClusterGroup = cluster

	proxy := &Proxy{
		ClusterGroup: cluster,
		// A non-literal-IP Host makes HasDNS() true without needing to set
		// up Configurator tags or an orchestrator.
		Host: "haproxy.example.com",
	}
	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	if strings.Contains(read, "resolvers") || strings.Contains(read, "init-addr") {
		t.Errorf("read backend = %q, want no resolvers/init-addr clause for runtimeapi", read)
	}
	if strings.Contains(write, "resolvers") || strings.Contains(write, "init-addr") {
		t.Errorf("write backend = %q, want no resolvers/init-addr clause for runtimeapi", write)
	}
	if !strings.Contains(read, "192.0.2.1:") {
		t.Errorf("read backend = %q, want the RFC 5737 placeholder %q instead of the server's FQDN", read, "192.0.2.1")
	}
	if !strings.Contains(write, "192.0.2.1:") {
		t.Errorf("write backend = %q, want the RFC 5737 placeholder %q instead of the server's FQDN", write, "192.0.2.1")
	}
}

// TestGetConfigProxyModuleRuntimeAPIKeepsResolversWithoutBootstrapFlag guards
// a regression found live against a real Kubernetes cluster: the placeholder
// design above is scoped to HaproxyAPIBootstrapServers, not to
// haproxy-mode=="runtimeapi" alone. With the flag left at its default
// (false, unset here), runtimeapi must render exactly like the
// externalcheck/standby else-branch — db.Host with the full
// "resolvers dns"/"init-addr" clause — because reconcileReadBackendServers
// itself no-ops without the flag (see its own gate), so nothing would ever
// correct a non-resolver placeholder address back to the real one.
// Live-reproduced: every read-backend member got stuck at 192.0.2.1/DOWN
// forever on a real cluster where this flag was left unset, the normal case
// for anyone not deliberately opting into the newer dynamic add/del
// lifecycle (Phase 1 of issue #1724).
func TestGetConfigProxyModuleRuntimeAPIKeepsResolversWithoutBootstrapFlag(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi"} // HaproxyAPIBootstrapServers left false

	server := cluster.Servers[0]
	server.Id = "server1"
	server.Port = "3306"
	server.ClusterGroup = cluster
	// Marks server1 as master so the write backend is rendered through the
	// same per-server loop as the read backend, not the separate "no leader
	// resolved yet" fallback (which always uses the placeholder,
	// deliberately unconditional on the bootstrap flag — see
	// TestGetConfigProxyModuleRuntimeAPINoLeaderFallbackUsesTestNetPlaceholder).
	cluster.master = server

	proxy := &Proxy{
		ClusterGroup: cluster,
		Host:         "haproxy.example.com",
	}
	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	const wantClause = "init-addr last,libc,none resolvers dns"
	if !strings.Contains(read, wantClause) {
		t.Errorf("read backend = %q, want it to contain %q (unchanged legacy behavior without the bootstrap flag)", read, wantClause)
	}
	if !strings.Contains(write, wantClause) {
		t.Errorf("write backend = %q, want it to contain %q (unchanged legacy behavior without the bootstrap flag)", write, wantClause)
	}
	if strings.Contains(read, "192.0.2.1") {
		t.Errorf("read backend = %q, want no placeholder address without the bootstrap flag", read)
	}
}

// TestGetConfigProxyModuleRuntimeAPIUsesResolvedIPDirectlyWhenKnown confirms
// a server that already has a resolved IP (db.IP, kept current by
// refreshResolvedIP or SetCredential's initial resolution) is rendered with
// that real address directly, not the placeholder -- a known-good address
// needs no placeholder-then-correct dance at all.
func TestGetConfigProxyModuleRuntimeAPIUsesResolvedIPDirectlyWhenKnown(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi", HaproxyAPIBootstrapServers: true}

	server := cluster.Servers[0]
	server.Id = "server1"
	server.Host = "clustera-1.db.clustera.svc.cluster.local"
	server.IP = "10.244.3.3"
	server.Port = "3306"
	server.ClusterGroup = cluster
	cluster.master = server

	proxy := &Proxy{ClusterGroup: cluster, Host: "haproxy.example.com"}
	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	if !strings.Contains(read, "10.244.3.3:3306") {
		t.Errorf("read backend = %q, want the already-resolved IP %q, not a placeholder", read, "10.244.3.3:3306")
	}
	if !strings.Contains(write, "10.244.3.3:3306") {
		t.Errorf("write backend = %q, want the already-resolved IP %q, not a placeholder", write, "10.244.3.3:3306")
	}
	if strings.Contains(read, "192.0.2.1") || strings.Contains(write, "192.0.2.1") {
		t.Errorf("read=%q write=%q, want no placeholder when db.IP is already known", read, write)
	}
}

// TestGetConfigProxyModuleRuntimeAPIUsesIPv6PlaceholderForIPv6Cluster guards
// the fix for an untested gap: a bootstrap-enabled runtimeapi server with no
// resolved IP yet must get a placeholder in the SAME address family it will
// eventually resolve to (ProvUseIpv6 is this cluster's existing signal for
// that, see GetBindAddress) -- correcting an IPv4 placeholder to a real IPv6
// address would hit the same "address family" Runtime API refusal
// live-reproduced for the family-less "init-addr none" design (HAProxy only
// allows "set server addr" within the same family at runtime).
func TestGetConfigProxyModuleRuntimeAPIUsesIPv6PlaceholderForIPv6Cluster(t *testing.T) {
	cluster := setupTestCluster(t, 1)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi", HaproxyAPIBootstrapServers: true, ProvUseIpv6: true}

	server := cluster.Servers[0]
	server.Id = "server1"
	server.Host = "clustera-1.db.clustera.svc.cluster.local"
	server.IP = "" // not resolved yet
	server.Port = "3306"
	server.ClusterGroup = cluster
	cluster.master = server

	proxy := &Proxy{ClusterGroup: cluster, Host: "haproxy.example.com"}
	if !proxy.HasDNS() {
		t.Fatalf("test setup error: expected proxy.HasDNS() to be true")
	}

	read := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_READ%%")
	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	const wantAddr = "[2001:db8::1]:3306"
	if !strings.Contains(read, wantAddr) {
		t.Errorf("read backend = %q, want the IPv6 placeholder %q", read, wantAddr)
	}
	if !strings.Contains(write, wantAddr) {
		t.Errorf("write backend = %q, want the IPv6 placeholder %q", write, wantAddr)
	}
	if strings.Contains(read, "192.0.2.1") || strings.Contains(write, "192.0.2.1") {
		t.Errorf("read=%q write=%q, want no IPv4 placeholder for an IPv6 cluster", read, write)
	}
}

// TestGetConfigProxyModuleRuntimeAPINoLeaderFallbackUsesIPv6PlaceholderForIPv6Cluster
// is the empty-cluster ("no leader resolved yet") fallback's IPv6
// counterpart to TestGetConfigProxyModuleRuntimeAPIUsesIPv6PlaceholderForIPv6Cluster.
func TestGetConfigProxyModuleRuntimeAPINoLeaderFallbackUsesIPv6PlaceholderForIPv6Cluster(t *testing.T) {
	cluster := setupTestCluster(t, 0)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi", ProvUseIpv6: true}

	proxy := &Proxy{ClusterGroup: cluster, Host: "haproxy.example.com"}

	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	const wantAddr = "[2001:db8::1]:3306"
	if !strings.Contains(write, wantAddr) {
		t.Errorf("write backend = %q, want the IPv6 placeholder %q", write, wantAddr)
	}
	if strings.Contains(write, "192.0.2.1") {
		t.Errorf("write backend = %q, want no IPv4 placeholder for an IPv6 cluster", write)
	}
}

// TestGetConfigProxyModuleRuntimeAPINoLeaderFallbackUsesTestNetPlaceholder
// guards the "no leader resolved yet" fallback (reachable when
// cluster.Servers is empty, e.g. a fresh cluster mid-provisioning): it must
// not render the literal hostname "none" as the fallback server's address —
// HAProxy refuses to start trying to resolve that as a real hostname
// (live-reproduced against HaproxyProxy.Init()'s equivalent fallback in
// cluster/prx_haproxy.go, which this mirrors).
func TestGetConfigProxyModuleRuntimeAPINoLeaderFallbackUsesTestNetPlaceholder(t *testing.T) {
	cluster := setupTestCluster(t, 0)
	defer cleanupTestCluster(t, cluster)
	cluster.Conf = &config.Config{HaproxyMode: "runtimeapi"}

	proxy := &Proxy{ClusterGroup: cluster, Host: "haproxy.example.com"}

	write := proxy.GetConfigProxyModule("%%ENV:SERVERS_HAPROXY_WRITE%%")

	if strings.Contains(write, "none:3306") {
		t.Errorf("write backend = %q, want no literal %q hostname", write, "none:3306")
	}
	if !strings.Contains(write, "192.0.2.1:3306") {
		t.Errorf("write backend = %q, want the RFC 5737 placeholder %q", write, "192.0.2.1:3306")
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

// TestModulesetHaproxyExternalcheckDefinesResolversDNS pins the other half
// of TestGetConfigProxyModuleAppendsDNSForNonRuntimeAPIModes' invariant:
// GetConfigProxyModule appends "resolvers dns" to every server line for
// externalcheck/standby whenever proxy.HasDNS() is true (always true for
// OpenSVC, prx_has.go) -- but that reference is only valid HAProxy config
// if a "resolvers dns { ... }" section is actually DEFINED somewhere in the
// same file. externalcheck on OpenSVC runs haproxy_check.cfg, rendered
// from the moduleset's "proxy_cnf_haproxy" entry (prov_opensvc_haproxy.go),
// not the image-default "proxy_cnf_haproxy_runtime_api" (haproxy.cfg) --
// the two are separate templates, and only the section definition
// (%%ENV:SVC_CONF_HAPROXY_DNS%%, substituted by GetConfigProxyDNS) lets the
// server-line references above resolve to something real. Without it in
// "proxy_cnf_haproxy" too, HAProxy refuses to start at all ("unknown
// resolvers 'dns'") on every OpenSVC externalcheck deployment.
func TestModulesetHaproxyExternalcheckDefinesResolversDNS(t *testing.T) {
	fmtStr := moduleValueFmt(t, "proxy_cnf_haproxy")
	if !strings.Contains(fmtStr, "%%ENV:SVC_CONF_HAPROXY_DNS%%") {
		t.Errorf(`moduleset entry "proxy_cnf_haproxy" (haproxy_check.cfg, externalcheck) does not define %%%%ENV:SVC_CONF_HAPROXY_DNS%%%% -- server lines referencing "resolvers dns" (see GetConfigProxyModule) would have nothing to resolve against, and HAProxy refuses to start`)
	}

	// Regression guard for the sibling entry this was modeled after.
	runtimeAPIFmt := moduleValueFmt(t, "proxy_cnf_haproxy_runtime_api")
	if !strings.Contains(runtimeAPIFmt, "%%ENV:SVC_CONF_HAPROXY_DNS%%") {
		t.Errorf(`moduleset entry "proxy_cnf_haproxy_runtime_api" (haproxy.cfg, standby/image-default) no longer defines %%%%ENV:SVC_CONF_HAPROXY_DNS%%%%`)
	}
}

// moduleValueFmt loads share/opensvc/moduleset_mariadb.svc.mrm.proxy.json
// and returns the "fmt" field of the named variable's (JSON-encoded string)
// var_value -- fatal if the file, the variable, or that field can't be
// found/parsed.
func moduleValueFmt(t *testing.T, varName string) string {
	t.Helper()
	data, err := os.ReadFile("../share/opensvc/moduleset_mariadb.svc.mrm.proxy.json")
	if err != nil {
		t.Fatalf("failed to read moduleset_mariadb.svc.mrm.proxy.json: %v", err)
	}

	var doc struct {
		Rulesets []struct {
			Variables []struct {
				VarName  string `json:"var_name"`
				VarValue string `json:"var_value"`
			} `json:"variables"`
		} `json:"rulesets"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("failed to parse moduleset_mariadb.svc.mrm.proxy.json: %v", err)
	}

	for _, rs := range doc.Rulesets {
		for _, v := range rs.Variables {
			if v.VarName != varName {
				continue
			}
			var value struct {
				Fmt string `json:"fmt"`
			}
			if err := json.Unmarshal([]byte(v.VarValue), &value); err != nil {
				t.Fatalf("failed to parse var_value for %q: %v", varName, err)
			}
			return value.Fmt
		}
	}
	t.Fatalf("moduleset variable %q not found", varName)
	return ""
}
