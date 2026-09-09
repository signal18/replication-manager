// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
)

// newMaintenanceTestCluster builds a minimal Cluster with numServers servers
// named db0..dbN-1, wired enough to exercise maintenance membership
// (Conf, ConfigManager, Servers[i].URL/Name/SourceClusterName/ClusterGroup).
// The ConfigManager is a bare zero-value: SaveConfig's non-wait path only
// touches isStopping.Load() (safe zero-value) and cluster.Save() (no I/O), so
// this avoids NewConfigManager's background git-push goroutine entirely.
func newMaintenanceTestCluster(numServers int) *Cluster {
	cl := &Cluster{
		Name:          "cluster1",
		Conf:          &config.Config{},
		ConfigManager: &manager.ConfigManager{},
		Servers:       make([]*ServerMonitor, numServers),
	}
	for i := 0; i < numServers; i++ {
		name := "db" + string(rune('0'+i))
		cl.Servers[i] = &ServerMonitor{
			Name:              name,
			URL:               name + ":3306",
			SourceClusterName: cl.Name,
			ClusterGroup:      cl,
		}
	}
	return cl
}

func TestIsInMaintenanceHostsExactMatchNoSubstring(t *testing.T) {
	cl := newMaintenanceTestCluster(2)
	// "db1" must not match "db10" or a longer host sharing the same prefix.
	cl.Conf.MaintenanceSrv = "db1:3306"

	db10 := &ServerMonitor{Name: "db10", URL: "db10:3306", SourceClusterName: cl.Name}
	if cl.IsInMaintenanceHosts(db10) {
		t.Fatalf("IsInMaintenanceHosts matched db10:3306 against configured db1:3306 (substring collision)")
	}

	db1 := &ServerMonitor{Name: "db1", URL: "db1:3306", SourceClusterName: cl.Name}
	if !cl.IsInMaintenanceHosts(db1) {
		t.Fatalf("IsInMaintenanceHosts did not match the exact configured host db1:3306")
	}
}

func TestIsInMaintenanceHostsChildClusterNeverMatches(t *testing.T) {
	cl := newMaintenanceTestCluster(0)
	cl.Conf.MaintenanceSrv = "db0:3306"

	child := &ServerMonitor{Name: "db0", URL: "db0:3306", SourceClusterName: "other-cluster"}
	if cl.IsInMaintenanceHosts(child) {
		t.Fatalf("IsInMaintenanceHosts must not match a server sourced from a different (child) cluster")
	}
}

// TestMaintenanceDomainQualifiedServerNormalization exercises AddMaintenanceSrv's
// entry := strings.ReplaceAll(node.URL, node.Domain+":3306", "") normalization
// (copied from SetIgnoreSrv's existing idiom) against a domain-qualified
// server (Domain != "", the GetDomain()/GetDomainHeadCluster() case), which
// the other tests in this file don't cover since they all use an empty
// Domain. Two sub-cases:
//
//  1. default port (3306): Domain+":3306" is a literal suffix of URL, so it
//     gets stripped and the stored entry is the bare Name.
//  2. non-default port: the ":3306" suffix isn't present in URL, so
//     ReplaceAll is a no-op and the stored entry is the full domain-qualified
//     URL.
//
// Both must still round-trip through IsInMaintenanceHosts for a freshly
// rebuilt ServerMonitor (simulating restart/reload) with the same
// deterministically-derived Name/Domain/Port/URL, since maintenanceListHasHost
// checks both the URL and the Name form.
func TestMaintenanceDomainQualifiedServerNormalization(t *testing.T) {
	cl := newMaintenanceTestCluster(0)

	// --- Sub-case 1: domain-qualified, default port -> stored as bare Name. ---
	domSrv := &ServerMonitor{
		Name:              "db1",
		Domain:            ".svc.cluster.local",
		Port:              "3306",
		URL:               "db1.svc.cluster.local:3306",
		SourceClusterName: cl.Name,
	}
	cl.Servers = []*ServerMonitor{domSrv}

	cl.AddMaintenanceSrv(domSrv)
	if got := cl.Conf.MaintenanceSrv; got != "db1" {
		t.Fatalf("expected domain+port stripped to bare name %q, got %q", "db1", got)
	}

	rebuilt := &ServerMonitor{
		Name:              "db1",
		Domain:            ".svc.cluster.local",
		Port:              "3306",
		URL:               "db1.svc.cluster.local:3306",
		SourceClusterName: cl.Name,
	}
	if !cl.IsInMaintenanceHosts(rebuilt) {
		t.Fatalf("domain-qualified server on the default port did not restore from its stripped bare-name entry")
	}

	// --- Sub-case 2: domain-qualified, non-default port -> stored as the full URL. ---
	if err := cl.RemoveMaintenanceSrv(domSrv); err != nil {
		t.Fatalf("RemoveMaintenanceSrv: %v", err)
	}
	domSrvAltPort := &ServerMonitor{
		Name:              "db2",
		Domain:            ".svc.cluster.local",
		Port:              "3307",
		URL:               "db2.svc.cluster.local:3307",
		SourceClusterName: cl.Name,
	}
	cl.Servers = []*ServerMonitor{domSrvAltPort}

	cl.AddMaintenanceSrv(domSrvAltPort)
	if got := cl.Conf.MaintenanceSrv; got != "db2.svc.cluster.local:3307" {
		t.Fatalf("expected unstripped full URL %q for a non-default port, got %q", "db2.svc.cluster.local:3307", got)
	}

	rebuiltAltPort := &ServerMonitor{
		Name:              "db2",
		Domain:            ".svc.cluster.local",
		Port:              "3307",
		URL:               "db2.svc.cluster.local:3307",
		SourceClusterName: cl.Name,
	}
	if !cl.IsInMaintenanceHosts(rebuiltAltPort) {
		t.Fatalf("domain-qualified server on a non-default port did not restore from its unstripped URL entry")
	}
}

// TestSetMaintenanceSrvExactMatchNoSubstring guards against a regression where
// SetMaintenanceSrv matched membership with strings.Contains instead of an
// exact token comparison: "1.2.3.4:3306" is a literal substring of
// "11.2.3.4:3306" (starting at index 1), so a naive Contains(list, srv.URL)
// check would have wrongly pulled the unrelated "1.2.3.4:3306" server into
// maintenance when only "11.2.3.4:3306" was ever configured.
func TestSetMaintenanceSrvExactMatchNoSubstring(t *testing.T) {
	cl := newMaintenanceTestCluster(0)
	cl.Servers = []*ServerMonitor{
		{Name: "a", URL: "11.2.3.4:3306", SourceClusterName: cl.Name},
		{Name: "b", URL: "1.2.3.4:3306", SourceClusterName: cl.Name},
	}
	for _, s := range cl.Servers {
		s.ClusterGroup = cl
	}

	cl.SetMaintenanceSrv("11.2.3.4:3306")

	if !cl.Servers[0].IsMaintenance {
		t.Fatalf("SetMaintenanceSrv did not mark the exactly-configured host 11.2.3.4:3306 as in maintenance")
	}
	if cl.Servers[1].IsMaintenance {
		t.Fatalf("SetMaintenanceSrv substring-matched 1.2.3.4:3306 against configured 11.2.3.4:3306")
	}
}

// TestMaintenanceMutationPreservesAbsentServerEntries guards against a
// regression where Add/RemoveMaintenanceSrv rebuilt the persisted list only
// from currently-instantiated cluster.Servers: mutating maintenance for one
// server used to silently drop a persisted entry belonging to a host that
// isn't presently represented in cluster.Servers (e.g. topology churn, a
// db-servers-hosts edit). The persisted token for that absent host must
// survive an unrelated add or remove.
func TestMaintenanceMutationPreservesAbsentServerEntries(t *testing.T) {
	cl := newMaintenanceTestCluster(1)
	present := cl.Servers[0]
	cl.Conf.MaintenanceSrv = "db-absent"

	cl.AddMaintenanceSrv(present)
	if !maintenanceListHasHost(cl.Conf.MaintenanceSrv, "db-absent", "db-absent") {
		t.Fatalf("AddMaintenanceSrv dropped the persisted entry for a server absent from cluster.Servers: got %q", cl.Conf.MaintenanceSrv)
	}
	if !cl.IsInMaintenanceHosts(present) {
		t.Fatalf("AddMaintenanceSrv did not add the requested server")
	}

	if err := cl.RemoveMaintenanceSrv(present); err != nil {
		t.Fatalf("RemoveMaintenanceSrv returned unexpected error: %v", err)
	}
	if !maintenanceListHasHost(cl.Conf.MaintenanceSrv, "db-absent", "db-absent") {
		t.Fatalf("RemoveMaintenanceSrv dropped the persisted entry for a server absent from cluster.Servers: got %q", cl.Conf.MaintenanceSrv)
	}
	if cl.IsInMaintenanceHosts(present) {
		t.Fatalf("RemoveMaintenanceSrv did not remove the requested server")
	}
}

func TestAddRemoveMaintenanceSrvUpdatesMembershipAndRuntimeFlag(t *testing.T) {
	cl := newMaintenanceTestCluster(2)
	target := cl.Servers[0]
	other := cl.Servers[1]

	cl.AddMaintenanceSrv(target)

	if !target.IsMaintenance {
		t.Fatalf("AddMaintenanceSrv did not set the runtime IsMaintenance flag")
	}
	if other.IsMaintenance {
		t.Fatalf("AddMaintenanceSrv incorrectly marked an unrelated server as in maintenance")
	}
	if !cl.IsInMaintenanceHosts(target) {
		t.Fatalf("AddMaintenanceSrv did not persist target into cluster.Conf.MaintenanceSrv")
	}
	if !cl.IsNeedConfigSave {
		t.Fatalf("AddMaintenanceSrv did not request config persistence (IsNeedConfigSave)")
	}

	// Adding an already-tracked server is a no-op (must not duplicate the entry).
	// Note: SetMaintenanceSrv strips ":3306" when Domain is unset, mirroring
	// SetIgnoreSrv's existing host-normalization behavior.
	cl.AddMaintenanceSrv(target)
	if got := cl.Conf.MaintenanceSrv; got != "db0" {
		t.Fatalf("AddMaintenanceSrv duplicated membership on repeat call: got %q", got)
	}

	cl.IsNeedConfigSave = false
	if err := cl.RemoveMaintenanceSrv(target); err != nil {
		t.Fatalf("RemoveMaintenanceSrv returned unexpected error: %v", err)
	}
	if target.IsMaintenance {
		t.Fatalf("RemoveMaintenanceSrv did not clear the runtime IsMaintenance flag")
	}
	if cl.IsInMaintenanceHosts(target) {
		t.Fatalf("RemoveMaintenanceSrv did not clear target from cluster.Conf.MaintenanceSrv")
	}
	if !cl.IsNeedConfigSave {
		t.Fatalf("RemoveMaintenanceSrv did not request config persistence (IsNeedConfigSave)")
	}

	// Removing a server that isn't tracked is an error and a no-op.
	if err := cl.RemoveMaintenanceSrv(target); err == nil {
		t.Fatalf("RemoveMaintenanceSrv should error when the server is not in the maintenance list")
	}
}

func TestServerSetDelMaintenancePersistMembership(t *testing.T) {
	cl := newMaintenanceTestCluster(1)
	server := cl.Servers[0]

	server.SetMaintenance()
	if !server.IsMaintenance || !cl.IsInMaintenanceHosts(server) {
		t.Fatalf("SetMaintenance did not set runtime flag and durable membership together")
	}

	server.DelMaintenance()
	if server.IsMaintenance || cl.IsInMaintenanceHosts(server) {
		t.Fatalf("DelMaintenance did not clear runtime flag and durable membership together")
	}
}

func TestSwitchMaintenancePersistsBothDirections(t *testing.T) {
	cl := newMaintenanceTestCluster(1)
	server := cl.Servers[0]

	if err := server.SwitchMaintenance(); err != nil {
		t.Fatalf("SwitchMaintenance returned unexpected error: %v", err)
	}
	if !server.IsMaintenance || !cl.IsInMaintenanceHosts(server) {
		t.Fatalf("SwitchMaintenance (off->on) did not persist membership")
	}

	if err := server.SwitchMaintenance(); err != nil {
		t.Fatalf("SwitchMaintenance returned unexpected error: %v", err)
	}
	if server.IsMaintenance || cl.IsInMaintenanceHosts(server) {
		t.Fatalf("SwitchMaintenance (on->off) did not clear persisted membership")
	}
}

// TestMaintenanceRestoredOnServerMonitorRebuild simulates what newServerMonitor
// does on process restart and config reload (cluster/srv.go): a brand new
// ServerMonitor for a host that was previously in maintenance must come back
// up with IsMaintenance derived from the durable membership, without any
// call to SetMaintenance (which would replay the state-change script).
func TestMaintenanceRestoredOnServerMonitorRebuild(t *testing.T) {
	cl := newMaintenanceTestCluster(1)
	server := cl.Servers[0]
	server.SetMaintenance()

	// Simulate a restart/reload: the old ServerMonitor is discarded and a
	// fresh one is constructed for the same host.
	rebuilt := &ServerMonitor{
		Name:              server.Name,
		URL:               server.URL,
		SourceClusterName: cl.Name,
		ClusterGroup:      cl,
	}
	// This is the exact restoration line added to newServerMonitor.
	rebuilt.IsMaintenance = cl.IsInMaintenanceHosts(rebuilt)

	if !rebuilt.IsMaintenance {
		t.Fatalf("rebuilt ServerMonitor did not restore IsMaintenance from durable membership")
	}

	// And once cleared, a rebuild must NOT resurrect maintenance.
	server.DelMaintenance()
	rebuilt2 := &ServerMonitor{
		Name:              server.Name,
		URL:               server.URL,
		SourceClusterName: cl.Name,
		ClusterGroup:      cl,
	}
	rebuilt2.IsMaintenance = cl.IsInMaintenanceHosts(rebuilt2)
	if rebuilt2.IsMaintenance {
		t.Fatalf("rebuilt ServerMonitor resurrected maintenance after it was cleared")
	}
}
