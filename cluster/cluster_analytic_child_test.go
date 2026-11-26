// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/logplugin"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// CDC_CORE.md: the tables of replication-plugin-tables become the plugin's rules.
func TestAnalyticRules(t *testing.T) {
	cfg := wire.Config{Rules: analyticRules("shop.orders, shop.lines,crm.*, bad, .x")}
	for _, c := range []struct {
		schema, table string
		want          bool
	}{{"shop", "orders", true}, {"shop", "lines", true}, {"shop", "carts", false}, {"crm", "anything", true}, {"bad", "x", false}} {
		if got := cfg.Matches(c.schema, c.table); got != c.want {
			t.Errorf("%s.%s: %v, want %v", c.schema, c.table, got, c.want)
		}
	}
	if len(analyticRules("")) != 0 {
		t.Fatal("no table, no rule")
	}
}

// The CDC source: refused when its binlog cannot carry a full copy, each reason said.
func TestReplSourceRefusal(t *testing.T) {
	srv := &ServerMonitor{Variables: config.NewStringsMap(), DBVersion: &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}, ClusterGroup: &Cluster{Conf: &config.Config{}}}
	set := func(kv ...string) {
		for i := 0; i+1 < len(kv); i += 2 {
			srv.Variables.Set(kv[i], kv[i+1])
		}
	}
	set("LOG_BIN", "ON", "BINLOG_FORMAT", "ROW", "BINLOG_ROW_IMAGE", "FULL", "LOG_SLAVE_UPDATES", "ON")
	if why := replSourceRefusal(srv, true); why != "" {
		t.Fatalf("a good replica refused: %s", why)
	}
	set("LOG_SLAVE_UPDATES", "OFF")
	if why := replSourceRefusal(srv, false); why != "" {
		t.Fatalf("a master needs no log_slave_updates: %s", why)
	}
	if why := replSourceRefusal(srv, true); !strings.Contains(why, "log_slave_updates") {
		t.Fatalf("replica without log_slave_updates: %q", why)
	}
	set("LOG_BIN", "OFF", "BINLOG_FORMAT", "MIXED", "BINLOG_ROW_IMAGE", "MINIMAL")
	why := replSourceRefusal(srv, false)
	for _, want := range []string{"log_bin is OFF", "binlog_format is MIXED", "binlog_row_image is MINIMAL"} {
		if !strings.Contains(why, want) {
			t.Errorf("missing %q in %q", want, why)
		}
	}
}

// An analytic child renders its channel's ignore list from its plugin's TOML and State.
func TestAnalyticChildReplicateIgnore(t *testing.T) {
	cl := newColumnstoreTestCluster()
	cl.Name = "shop-olap"
	cl.Conf.WorkingDir = t.TempDir()
	srv := &ServerMonitor{ClusterGroup: cl}
	if got := srv.replPluginReplicateIgnore(); got != "" {
		t.Fatalf("not an analytic child: %q", got)
	}
	cl.Conf.ReplicationPluginDestination = "colstore"
	path := cl.analyticPluginPath()
	if err := writeReplPluginConfig(path, wire.Config{Plugin: "colstore", Enabled: true, SourceChannel: "shop", Destination: "colstore"}); err != nil {
		t.Fatal(err)
	}
	if err := wire.WriteState(wire.StatePath(path), wire.State{GTID: "0-1-5", RestoreDone: true, Tables: []string{"shop.lines", "shop.orders"}}); err != nil {
		t.Fatal(err)
	}
	if got := srv.replPluginReplicateIgnore(); got != "shop.replicate_ignore_table=shop.lines,shop.orders" {
		t.Fatalf("ignore line %q", got)
	}
	if err := cl.SetReplicationPluginDestination("../x"); err == nil {
		t.Fatal("a path as plugin name must be refused")
	}
}

// A plugin is allowed when installed and, for an enterprise tier, when the plan grants it;
// its manifest gives its tags and image.
func TestReplPluginAllowed(t *testing.T) {
	cl := newColumnstoreTestCluster()
	cl.Conf.WorkingDir = t.TempDir()
	dir := filepath.Join(cl.Conf.WorkingDir, logplugin.PluginDirName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := cl.replPluginAllowed("colstore"); err == nil {
		t.Fatal("a missing plugin must be refused")
	}
	bin := filepath.Join(dir, wire.BinaryPrefix+"colstore")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := cl.replPluginAllowed("colstore"); err != nil {
		t.Fatalf("a plugin without manifest is of every plan: %v", err)
	}
	if err := os.WriteFile(bin+".manifest.json", []byte(`{"name":"colstore","tier":"enterprise","tags":["eng","localinfile"],"image":"example/db:1","singleServer":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cl.replPluginAllowed("colstore"); err == nil || !strings.Contains(err.Error(), "enterprise") {
		t.Fatalf("an enterprise plugin without the plan: %v", err)
	}
	cl.Conf.Cloud18GitUser, cl.Conf.Cloud18SubscriptionPlan = "someone", "developer"
	if err := cl.replPluginAllowed("colstore"); err != nil {
		t.Fatalf("an enterprise plugin on a granting plan: %v", err)
	}
	m, err := cl.replPluginManifest("colstore")
	if err != nil || m.Image != "example/db:1" || len(m.Tags) != 2 || !m.SingleServer {
		t.Fatalf("manifest %+v, %v", m, err)
	}
}
