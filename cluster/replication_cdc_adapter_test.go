// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// Step 4 of REPLICATION_INTERFACE.md: every CDC plugin binary is a ReplicationPlugin through
// the one adapter, known by its name repl-<source>-<target>-cdc, its binaries and short names
// of before accepted as aliases.

func TestCDCPluginNamesAndAliases(t *testing.T) {
	for canonical, aliases := range map[string][]string{
		ReplicationPluginMariaDBDuckDBCDC:      {"duckdb", "replplugin-duckdb"},
		ReplicationPluginMariaDBColumnStoreCDC: {"columnstore", "replplugin-columnstore"},
		ReplicationPluginMariaDBMariaDBCDC:     {"innodb", "replplugin-innodb"},
		ReplicationPluginPostgreSQLMariaDBCDC:  {"postgresql", "replplugin-postgresql"},
	} {
		for _, n := range append(aliases, canonical) {
			if got := CanonicalReplicationPluginName(n); got != canonical {
				t.Errorf("%s: canonical %q, want %q", n, got, canonical)
			}
			p := (*Cluster)(nil).ReplicationPlugin(n)
			m := p.Manifest()
			if m.Name != canonical || ReplicationKind(m.Type) != ReplicationKindCDC || len(m.Source) == 0 || len(m.Target) == 0 || len(m.Topologies) == 0 {
				t.Errorf("%s: manifest %+v", n, m)
			}
			if m.Can(ReplicationActionPromote) || m.Can(ReplicationActionChangeSource) || !m.Can(ReplicationActionStart) || !m.Can(ReplicationActionRejoin) {
				t.Errorf("%s: a feed is started, stopped and rejoined by the supervisor, never promoted: %v", n, m.Actions)
			}
		}
		short := aliases[0]
		if got := replPluginBinaryNames(short); len(got) != 2 || got[0] != canonical || got[1] != wire.BinaryPrefix+short {
			t.Errorf("%s: binaries %v, want the new name then the old one", short, got)
		}
	}
	// a plugin repman knows no name of: its binary as before, the generic manifest
	if CanonicalReplicationPluginName("kafka") != "kafka" || isCDCPluginName("kafka") {
		t.Fatal("an unknown name is not a known CDC plugin")
	}
	if got := replPluginBinaryNames("kafka"); len(got) != 1 || got[0] != "replplugin-kafka" {
		t.Fatalf("unknown plugin binary: %v", got)
	}
	if m := defaultCDCManifest("repl-mariadb-kafka-cdc"); m.Name != "repl-mariadb-kafka-cdc" || m.Type != string(ReplicationKindCDC) {
		t.Fatalf("a repl-* CDC name of a newer repman keeps its name: %+v", m)
	}
	// the registry holds the four under their names
	c := &Cluster{Name: "c", Conf: &config.Config{WorkingDir: t.TempDir()}}
	if err := c.newReplicationPluginList(); err != nil {
		t.Fatal(err)
	}
	for _, d := range cdcPlugins {
		if _, ok := c.ReplicationPlugins[d.name]; !ok {
			t.Errorf("%s not registered", d.name)
		}
	}
	if c.ReplicationPlugin("duckdb") != c.ReplicationPlugin(ReplicationPluginMariaDBDuckDBCDC) {
		t.Fatal("an alias gives the registered plugin")
	}
}

// writeFakeBinary writes an executable plugin binary (a shell script) in the cluster's plugin
// directory, with its manifest when given.
func writeFakeBinary(t *testing.T, c *Cluster, name, script, manifest string) string {
	t.Helper()
	dir := filepath.Join(c.Conf.WorkingDir, "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, name)
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(bin+".manifest.json", []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// the binary of either name runs: the one named after the plugin first
func TestCDCBinaryOfEitherName(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{WorkingDir: t.TempDir()}}
	old := writeFakeBinary(t, c, "replplugin-duckdb", "exit 0", `{"name":"duckdb","tier":"enterprise","tags":["duckdb"]}`)
	for _, n := range []string{"duckdb", "replplugin-duckdb", ReplicationPluginMariaDBDuckDBCDC} {
		if bin, err := c.replPluginBinary(n); err != nil || bin != old {
			t.Fatalf("%s: %s %v, want the binary of before", n, bin, err)
		}
		if m, err := c.replPluginManifest(n); err != nil || m.Tier != config.ConstArchiveTierEnterprise || len(m.Tags) != 1 {
			t.Fatalf("%s: its manifest %+v %v", n, m, err)
		}
	}
	if m := c.cdcManifestOf("duckdb"); m.Name != ReplicationPluginMariaDBDuckDBCDC || m.Tier != config.ConstArchiveTierEnterprise {
		t.Fatalf("a binary of before keeps the plugin's name, its tier the one enforced: %+v", m)
	}
	renamed := writeFakeBinary(t, c, ReplicationPluginMariaDBDuckDBCDC, "exit 0", "")
	if bin, err := c.replPluginBinary("duckdb"); err != nil || bin != renamed {
		t.Fatalf("the binary named after the plugin first: %s %v", bin, err)
	}
	if _, err := c.replPluginBinary("columnstore"); err == nil || !strings.Contains(err.Error(), "repl-mariadb-columnstore-cdc or replplugin-columnstore") {
		t.Fatalf("a missing binary names both: %v", err)
	}
}

// a DuckDB child: its feed is a Replication of repl-mariadb-duckdb-cdc in its master's
// server.Replications, read by AnalyticFeedStatus through the object
func duckdbChildFixture(t *testing.T, destination string) (*Cluster, *ServerMonitor) {
	t.Helper()
	c := &Cluster{Name: "shop-duck", Conf: &config.Config{WorkingDir: t.TempDir(), ReplicationPluginDestination: destination, ReplicationPluginTables: "shop.*",
		ReplicationPluginSource: "master", ReplicationPluginCheckpointInterval: 10, ReplicationPluginMergeInterval: 60}, Log: s18log.NewHttpLog(10)}
	c.StateMachine = new(state.StateMachine)
	c.StateMachine.Init()
	sv := &ServerMonitor{Host: "db1.duck", Port: "3306", URL: "db1.duck:3306", ClusterGroup: c}
	c.Servers = serverList{sv}
	cfg := wire.Config{Plugin: destination, Enabled: true, Destination: destination, SourceCluster: "shop",
		Source: wire.Endpoint{Host: "db1.shop", Port: 3306, User: "repman"},
		Target: wire.Endpoint{Host: "db1.duck", Port: 3306, User: "repman"}}
	if err := writeReplPluginConfig(c.analyticPluginPath(), cfg); err != nil {
		t.Fatal(err)
	}
	return c, sv
}

func TestDuckDBChildFeedIsAReplication(t *testing.T) {
	for _, dest := range []string{"duckdb", "replplugin-duckdb", ReplicationPluginMariaDBDuckDBCDC} {
		c, sv := duckdbChildFixture(t, dest)
		feeds := c.cdcFeedsOf(sv)
		if len(feeds) != 1 {
			t.Fatalf("%s: one feed into the child's master: %+v", dest, feeds)
		}
		r := feeds[0]
		if r.Plugin().Name != ReplicationPluginMariaDBDuckDBCDC || KindOf(r) != ReplicationKindCDC || CanFailover(r) || r.Name() != "cdc:"+dest {
			t.Fatalf("%s: %s of %s", dest, r.Name(), r.Plugin().Name)
		}
		if r.TargetServer() != sv || r.SourceServer().Host != "db1.shop" {
			t.Fatalf("%s: ends %+v", dest, r.SourceServer())
		}
		// as Refresh stores it, beside the native channel from the parent
		sv.Replications = append(sv.replicationsOfRows(channel("shop", "db1.shop", "Yes", "Yes")), feeds...)
		if len(sv.NativeReplications()) != 1 || len(sv.Replications.OfKind(ReplicationKindCDC)) != 1 {
			t.Fatalf("%s: one native channel, one feed", dest)
		}
		// the JSON names the plugin, and decodes back to an object of it
		b, err := json.Marshal(sv.Replications)
		if err != nil || !strings.Contains(string(b), `"plugin":"repl-mariadb-duckdb-cdc"`) || !strings.Contains(string(b), `"kind":"cdc"`) {
			t.Fatalf("%s: JSON %s %v", dest, b, err)
		}
		var back ReplicationList
		if err := json.Unmarshal(b, &back); err != nil || len(back) != 2 {
			t.Fatal(err)
		}
		if _, ok := back[1].(*cdcReplication); !ok || back[1].Plugin().Name != ReplicationPluginMariaDBDuckDBCDC || CanFailover(back[1]) {
			t.Fatalf("%s: decoded %T %+v", dest, back[1], back[1].Plugin())
		}
		if _, err := back[1].Start(LinkByTest); err == nil {
			t.Fatal("a decoded copy acts on nothing")
		}

		// the analytic feed's status, through the object
		f, ok := c.AnalyticFeedStatus()
		if !ok || f.ConfigNotFound || f.Replication != ReplicationPluginMariaDBDuckDBCDC || f.Name != "cdc:"+dest || f.Running || f.MergeInterval != 60 || f.CheckpointInterval != 10 || f.Tables != "shop.*" {
			t.Fatalf("%s: feed %+v", dest, f)
		}
		path := c.analyticPluginPath()
		run := &replPluginRunner{path: path, cfg: wire.Config{Plugin: dest, Source: wire.Endpoint{Host: "db2.shop"}}, done: make(chan struct{})}
		run.status.Store(&wire.Status{State: wire.StateRunning, GTID: "0-1-42", AppliedGTID: "0-1-40", IndexesReady: []string{"orders"}})
		c.replPlugins = map[string]*replPluginRunner{path: run}
		if f, _ = c.AnalyticFeedStatus(); !f.Running || f.SourceHost != "db2.shop" || f.Status == nil || f.Status.AppliedGTID != "0-1-40" {
			t.Fatalf("%s: running %+v", dest, f)
		}
		if got := c.ReplicationPluginIndexesReady(); len(got) != 1 || got[0] != "orders" {
			t.Fatalf("%s: indexes ready through the objects: %v", dest, got)
		}
		row := c.cdcFeedsOf(sv)[0].Row()
		if row.SlaveIORunning.String != "Yes" || row.GtidSlavePos.String != "0-1-40" {
			t.Fatalf("%s: the row from the status line: %+v", dest, row)
		}
	}
}

// a seeded child keeps its TOML when replication-plugin-destination is then set to another
// name of its plugin
func TestAnalyticPluginPathAcrossNames(t *testing.T) {
	c, _ := duckdbChildFixture(t, "duckdb")
	old := c.analyticPluginPath()
	if filepath.Base(old) != "duckdb.toml" {
		t.Fatalf("path %s", old)
	}
	if err := c.SetReplicationPluginDestination(ReplicationPluginMariaDBDuckDBCDC); err != nil {
		t.Fatal(err)
	}
	if got := c.analyticPluginPath(); got != old {
		t.Fatalf("the TOML of before: %s, want %s", got, old)
	}
	if f, _ := c.AnalyticFeedStatus(); f.ConfigNotFound || f.Replication != ReplicationPluginMariaDBDuckDBCDC {
		t.Fatalf("feed %+v", f)
	}
	fresh := &Cluster{Name: "fresh", Conf: &config.Config{WorkingDir: t.TempDir(), ReplicationPluginDestination: ReplicationPluginMariaDBDuckDBCDC}}
	if filepath.Base(fresh.analyticPluginPath()) != ReplicationPluginMariaDBDuckDBCDC+".toml" {
		t.Fatalf("a new child: its destination's name: %s", fresh.analyticPluginPath())
	}
}

// the actions a binary's manifest adds run the binary with the action; the others are refused
func TestCDCAdapterRunsTheBinaryActions(t *testing.T) {
	c, sv := duckdbChildFixture(t, "duckdb")
	c.Conf.SecretKey = []byte("0123456789abcdef")
	script := `case "$1" in
stop-copy) printf 'paused capture\t%s\t%s\t%s\t%s\n' "$REPMAN_REPL_NAME" "$REPMAN_REPL_REASON" "$REPMAN_REPL_SETTING_BATCH_ROWS" "$REPMAN_REPL_SECRET_TOKEN" ;;
start-copy) echo "no capture to resume" >&2; exit 2 ;;
*) exit 0 ;;
esac`
	manifest := `{"name":"repl-mariadb-duckdb-cdc","tier":"enterprise","type":"cdc","source":["MariaDB"],"target":["DuckDB"],
 "topologies":[{"name":"copy","transport":"logical","consistency":"async"}],
 "actions":["status","start","stop","rejoin","start-copy","stop-copy","promote","change-source"],
 "settings":[{"name":"batch-rows","type":"int","default":"500"},{"name":"token","type":"secret"},{"name":"Bad Name","type":"string"}]}`
	writeFakeBinary(t, c, ReplicationPluginMariaDBDuckDBCDC, script, manifest)
	r := c.cdcFeedsOf(sv)[0]
	m := r.Plugin()
	if !m.Can(ReplicationActionStopCopyEvents) || !m.Can(ReplicationActionStartCopyEvents) || m.Can(ReplicationActionPromote) || m.Can(ReplicationActionChangeSource) || m.Can(ReplicationActionStopApplyEvents) {
		t.Fatalf("actions: %v", m.Actions)
	}
	if len(m.Settings) != 2 || m.Tier != config.ConstArchiveTierEnterprise {
		t.Fatalf("settings (a bad name left out) and tier: %+v", m)
	}
	// the manifest's default, then the cluster's value under an alias, the secret decrypted
	q, err := r.StopCopyEvents(LinkByOperator)
	if err != nil || q != "paused capture\tcdc:duckdb\toperator request\t500" {
		t.Fatalf("stop-copy: %q %v", q, err)
	}
	if got := sv.linkChangeList(); len(got) != 1 || got[0].op != linkOpStopCopyEvents {
		t.Fatalf("recorded (WARN0352): %+v", got)
	}
	c.Conf.ReplPluginEntries = []config.ReplPluginEntry{{Plugin: "duckdb", Settings: map[string]interface{}{"batch-rows": int64(2000), "token": c.Conf.GetEncryptedString("t0ken")}}}
	if q, err = r.StopCopyEvents(LinkByOperator); err != nil || q != "paused capture\tcdc:duckdb\toperator request\t2000\tt0ken" {
		t.Fatalf("stop-copy with settings: %q %v", q, err)
	}
	// a secret held in clear never reaches the plugin (F9)
	c.Conf.ReplPluginEntries[0].Settings["token"] = "clear"
	if q, _ = r.StopCopyEvents(LinkByOperator); strings.Contains(q, "clear") {
		t.Fatalf("a clear secret passed: %q", q)
	}
	if _, err := r.StartCopyEvents(LinkByOperator); err == nil || !strings.Contains(err.Error(), "no capture to resume") {
		t.Fatalf("the last line of stderr: %v", err)
	}
	for name, op := range map[string]func() (string, error){
		"StopApplyEvents": func() (string, error) { return r.StopApplyEvents(LinkByTest) },
		"Promote":         func() (string, error) { return r.Promote(LinkByTest) },
		"ChangeSource":    func() (string, error) { return r.ChangeSource(Endpoint{}, ChangeSourceOpt{}, LinkByTest) },
		"Remove":          func() (string, error) { return r.Remove(LinkByTest) },
	} {
		if _, err := op(); !errors.Is(err, ErrReplicationNotSupported) {
			t.Errorf("%s: want ErrReplicationNotSupported, got %v", name, err)
		}
	}
	// the run's environment carries the settings too
	env := strings.Join(c.replPluginEnv(wire.Config{Plugin: "duckdb"}), "\n")
	if !strings.Contains(env, "REPMAN_REPL_SETTING_BATCH_ROWS=2000") || strings.Contains(env, "REPMAN_REPL_SECRET_TOKEN") {
		t.Fatalf("run environment: %s", env)
	}
}

// a binary of before, without the replication fields: the supervisor's actions only
func TestCDCAdapterBinaryWithoutActions(t *testing.T) {
	c, sv := duckdbChildFixture(t, "duckdb")
	writeFakeBinary(t, c, "replplugin-duckdb", "echo should-not-run; exit 0", `{"name":"duckdb","tier":"enterprise"}`)
	r := c.cdcFeedsOf(sv)[0]
	for name, op := range map[string]func() (string, error){
		"StartCopyEvents":  func() (string, error) { return r.StartCopyEvents(LinkByTest) },
		"StopCopyEvents":   func() (string, error) { return r.StopCopyEvents(LinkByTest) },
		"StartApplyEvents": func() (string, error) { return r.StartApplyEvents(LinkByTest) },
		"StopApplyEvents":  func() (string, error) { return r.StopApplyEvents(LinkByTest) },
	} {
		if q, err := op(); !errors.Is(err, ErrReplicationNotSupported) || q != "" {
			t.Errorf("%s: want ErrReplicationNotSupported and nothing run, got %q %v", name, q, err)
		}
	}
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("a refused operation records nothing")
	}
}
