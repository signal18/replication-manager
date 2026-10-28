// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/gtid"
)

// TestIsActiveStagingServer_TopologyStagingDisabled ensures that when
// topology-staging is off, no server is ever treated as staging, regardless
// of any other state - i.e. no behavior change outside staging topology.
func TestIsActiveStagingServer_TopologyStagingDisabled(t *testing.T) {
	staging := &ServerMonitor{Id: "db1", HostCnf: "staging-host"}
	cluster := &Cluster{
		Conf: &config.Config{
			TopologyStaging:   false,
			StagingServerHost: "staging-host",
		},
		StagingServer: staging,
		Servers:       serverList{staging},
	}

	if cluster.IsActiveStagingServer(staging) {
		t.Fatal("expected IsActiveStagingServer to be false when topology-staging is disabled")
	}
}

// TestIsActiveStagingServer_RecognizesConfiguredStagingServer covers the
// common, steady-state case: StagingServerHost (kept in sync with the live
// StagingServer pointer by SetStagingServer()) names the staging server.
func TestIsActiveStagingServer_RecognizesConfiguredStagingServer(t *testing.T) {
	staging := &ServerMonitor{Id: "db1", HostCnf: "staging-host"}
	other := &ServerMonitor{Id: "db2", HostCnf: "other-host"}
	cluster := &Cluster{
		Conf: &config.Config{
			TopologyStaging:   true,
			StagingServerHost: "staging-host",
		},
		StagingServer: staging,
		Servers:       serverList{staging, other},
	}

	if !cluster.IsActiveStagingServer(staging) {
		t.Fatal("expected the staging server to be recognized as the staging server")
	}
	if cluster.IsActiveStagingServer(other) {
		t.Fatal("expected a non-staging server not to be recognized as the staging server")
	}
}

// TestIsActiveStagingServer_WorksBeforeLivePointerIsSet reproduces the
// reported bug: right after a staging standalone recovers, Ping() runs
// before cluster_topo.go's TopologyDiscover has had a chance to populate
// cluster.StagingServer (see cluster_topo.go, "Set staging server from
// config if not yet set"). Since the check goes straight to
// Conf.StagingServerHost, it doesn't depend on that pointer at all - before
// this fix, isStagingServer stayed false in that window and the recovering
// staging standalone was forced read-only.
func TestIsActiveStagingServer_WorksBeforeLivePointerIsSet(t *testing.T) {
	staging := &ServerMonitor{Id: "db1", HostCnf: "staging-host"}
	other := &ServerMonitor{Id: "db2", HostCnf: "other-host"}
	cluster := &Cluster{
		Conf: &config.Config{
			TopologyStaging:   true,
			StagingServerHost: "staging-host",
		},
		StagingServer: nil, // not yet assigned by topology discovery
		Servers:       serverList{staging, other},
	}

	if !cluster.IsActiveStagingServer(staging) {
		t.Fatal("expected config-based lookup to recognize the staging server before cluster.StagingServer is set")
	}
	if cluster.IsActiveStagingServer(other) {
		t.Fatal("expected a non-staging server not to be recognized as the staging server via config fallback")
	}
}

// TestIsActiveStagingServer_NoStagingConfigured ensures that when
// topology-staging is enabled but no server matches the config, nothing is
// treated as staging.
func TestIsActiveStagingServer_NoStagingConfigured(t *testing.T) {
	srv := &ServerMonitor{Id: "db1", HostCnf: "some-host"}
	cluster := &Cluster{
		Conf: &config.Config{
			TopologyStaging:   true,
			StagingServerHost: "",
		},
		StagingServer: nil,
		Servers:       serverList{srv},
	}

	if cluster.IsActiveStagingServer(srv) {
		t.Fatal("expected IsActiveStagingServer to be false when no staging server is configured")
	}
}

// TestIsActiveStagingServer_FollowsConfigOverStalePointer covers a live
// config reload of staging-server-host: StagingServerHost is not
// scope:"server", so it can change without a repman restart, and
// cluster.StagingServer only gets reassigned once topology discovery runs
// (cluster_topo.go), which can lag behind. IsActiveStagingServer must follow
// Conf.StagingServerHost - the single source of truth SetStagingServer()
// always keeps in sync - rather than trusting a stale pointer: the newly
// configured server must be protected immediately, and the old one must
// immediately stop being treated as staging once config has moved on.
func TestIsActiveStagingServer_FollowsConfigOverStalePointer(t *testing.T) {
	oldStaging := &ServerMonitor{Id: "db1", HostCnf: "old-staging-host"}
	newStaging := &ServerMonitor{Id: "db2", HostCnf: "new-staging-host"}
	cluster := &Cluster{
		Conf: &config.Config{
			TopologyStaging:   true,
			StagingServerHost: "new-staging-host", // reassigned by a live config reload
		},
		StagingServer: oldStaging, // cluster_topo.go has not caught up yet
		Servers:       serverList{oldStaging, newStaging},
	}

	if !cluster.IsActiveStagingServer(newStaging) {
		t.Fatal("expected the newly configured staging server to be recognized immediately, even before the live pointer catches up")
	}
	if cluster.IsActiveStagingServer(oldStaging) {
		t.Fatal("expected the old staging server to stop being recognized once config points elsewhere, regardless of the stale live pointer")
	}
}

// TestMariadbDumpWritesAccounts: only --system=all or a list holding users writes the accounts
// as statements; --system=timezones dumps the account tables as data (seen in the lab).
func TestMariadbDumpWritesAccounts(t *testing.T) {
	for _, c := range []struct {
		opts string
		want bool
	}{
		{"--hex-blob --single-transaction --all-databases --system=all", true},
		{"--all-databases --system=users", true},
		{"--all-databases --system=timezones,users", true},
		{"--all-databases --system=timezones", false},
		{"--all-databases --system=plugins,udfs", false},
		{"--all-databases", false},
		{"", false},
	} {
		if got := mariadbDumpWritesAccounts(c.opts); got != c.want {
			t.Errorf("%q: got %v, want %v", c.opts, got, c.want)
		}
	}
}

// TestReseedGTIDSlavePos: the backup's GTID for its domains, the current one for the others.
func TestReseedGTIDSlavePos(t *testing.T) {
	for _, c := range []struct {
		name, pos, current, want string
	}{
		{"backup domains replace the current ones", "97-90125166-6,0-90125166-3836", "0-90125166-2144,97-90125166-5", "97-90125166-6,0-90125166-3836"},
		{"another domain is kept", "0-90125166-3836", "0-90125166-2144,5-200-10", "0-90125166-3836,5-200-10"},
		{"no current position", "0-90125166-3836", "", "0-90125166-3836"},
	} {
		var current *gtid.List
		if c.current != "" {
			current = gtid.NewList(c.current)
		}
		if got := reseedGTIDSlavePos(c.pos, current); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

// A master failed over is standalone for a moment: outside a staging topology it must not
// become the staging server nor be saved as staging-server-host; in one, the first
// standalone server still is.
func TestSetStandaloneAsStaging(t *testing.T) {
	for _, staging := range []bool{false, true} {
		standalone := &ServerMonitor{Id: "db1", HostCnf: "db1", State: stateUnconn}
		cluster := &Cluster{
			Conf:    &config.Config{TopologyStaging: staging},
			Servers: serverList{standalone},
		}
		got := cluster.SetStandaloneAsStaging()
		if !staging && (got != nil || cluster.StagingServer != nil || cluster.Conf.StagingServerHost != "") {
			t.Fatalf("topology-staging off: chose %v, staging-server-host %q", got, cluster.Conf.StagingServerHost)
		}
		if staging && (got != standalone || cluster.Conf.StagingServerHost != "db1") {
			t.Fatalf("topology-staging on: chose %v, staging-server-host %q", got, cluster.Conf.StagingServerHost)
		}
	}
}
