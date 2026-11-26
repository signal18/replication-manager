// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
)

// The analytic child (CDC_CORE.md): a child cluster whose chosen tables
// (replication-plugin-tables) are copies of the parent's made and fed by a destination plugin
// (replication-plugin-destination, the binary replplugin-<name>, built on the CDC core), for
// an engine native replication cannot feed. At the seed, once the parent's backup is restored
// and before the child's channel from the parent starts, the plugin is run with -create: it
// makes each chosen table its destination's on the child (never on the parent: F1), or leaves
// it InnoDB, replicated natively, with the reason (WARN0340, a schema state per table). The
// channel ignores the tables made; the plugin then streams the parent's binlog from the GTID
// the channel resumes from. A plugin missing, or of a tier the seat's plan does not grant,
// is refused with ERR00132 and the seed stops with the channel stopped.

// analyticSkippedFile: the tables the seed left as they were, with their reason, raised every tick
const analyticSkippedFile = "analytic.skipped.json"

// replStagingSchema is the CDC core's staging schema on the target (cdc.StagingSchema)
const replStagingSchema = "replication_manager_staging"

// analyticCreateTimeout: how long the plugin's -create may take (a conversion copies its table)
const analyticCreateTimeout = 24 * time.Hour

// HasAnalyticChild reports whether the cluster is an analytic child: a destination plugin set.
func (cluster *Cluster) HasAnalyticChild() bool {
	return strings.TrimSpace(cluster.Conf.ReplicationPluginDestination) != ""
}

func (cluster *Cluster) analyticPluginPath() string {
	return filepath.Join(cluster.replPluginDir(), strings.TrimSpace(cluster.Conf.ReplicationPluginDestination)+".toml")
}

func (cluster *Cluster) analyticSkippedPath() string {
	return filepath.Join(cluster.replPluginDir(), analyticSkippedFile)
}

// analyticRules turns replication-plugin-tables (schema.table or schema.*, comma separated)
// into the plugin's rules.
func analyticRules(patterns string) []wire.Rule {
	var tables []string
	for _, p := range strings.Split(patterns, ",") {
		if s, t, ok := strings.Cut(strings.TrimSpace(p), "."); ok && s != "" && t != "" {
			tables = append(tables, s+"."+t)
		}
	}
	return wire.RulesOf(tables)
}

// addAnalyticTags: an analytic child's servers carry the tags its plugin's manifest names
// (its engine, localinfile for LOAD DATA LOCAL).
func (cluster *Cluster) addAnalyticTags() {
	if !cluster.HasAnalyticChild() {
		return
	}
	m, err := cluster.replPluginManifest(strings.TrimSpace(cluster.Conf.ReplicationPluginDestination))
	if err != nil {
		return // the plugin is missing: ERR00132 at the seed
	}
	for _, tag := range m.Tags {
		if !cluster.Configurator.HaveDBTag(tag) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Adding database tag %s for the destination %s", tag, m.Name)
			cluster.AddDBTagConfig(tag)
		}
	}
}

// SetReplicationPluginDestination sets the analytic child's destination plugin ("" none) and
// adds the tags of its manifest; the tables are made at the next seed from the parent.
func (cluster *Cluster) SetReplicationPluginDestination(name string) error {
	name = strings.TrimSpace(name)
	if strings.ContainsAny(name, `/\. `) {
		return fmt.Errorf("invalid destination plugin name %q", name)
	}
	cluster.Conf.ReplicationPluginDestination = name
	cluster.addAnalyticTags()
	return nil
}

// prepareAnalyticChild runs on the child master after the parent's backup is restored, its
// channel from the parent still stopped.
func (cluster *Cluster) prepareAnalyticChild(parent *Cluster, target *ServerMonitor, channel string) error {
	plugin := strings.TrimSpace(cluster.Conf.ReplicationPluginDestination)
	if err := cluster.replPluginAllowed(plugin); err != nil {
		cluster.SetState("ERR00132", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00132"], plugin, cluster.Name, err), ErrFrom: "REPL"})
		return err
	}
	path := cluster.analyticPluginPath()
	// a fresh seed: the plugin of an older one stopped (its TOML removed, the reconcile stops
	// it), its position, checkpoints and staging dropped
	os.Remove(path)
	cluster.CheckReplicationPlugins()
	for _, p := range []string{wire.StatePath(path), wire.AppliedPath(path), wire.MergedPath(path), cluster.analyticSkippedPath()} {
		os.Remove(p)
	}
	os.RemoveAll(wire.CheckpointDir(path))
	if q, err := dbhelper.DropDatabaseIfExists(target.Conn, target.DBVersion, replStagingSchema); err != nil {
		cluster.LogSQL(q, err, target.URL, "AnalyticChild", config.LvlErr, "Analytic child %s: staging not dropped on %s: %s", cluster.Name, target.URL, err)
		return err
	}
	source := cluster.resolveReplPluginSource(wire.Config{Plugin: plugin, SourceCluster: parent.Name})
	if source == nil {
		return fmt.Errorf("no source for the replication plugin on %s (replication-plugin-source %q): see its state", parent.Name, cluster.Conf.ReplicationPluginSource)
	}
	gtid, q, err := dbhelper.GetVariableByName(target.Conn, "GTID_SLAVE_POS", target.DBVersion)
	if err != nil || gtid == "" {
		cluster.LogSQL(q, err, target.URL, "AnalyticChild", config.LvlErr, "Analytic child %s: gtid_slave_pos of %s: %v", cluster.Name, target.URL, err)
		return fmt.Errorf("the position of %s after the restore is unknown (%v): the plugin cannot know where to start", target.URL, err)
	}
	rules := analyticRules(cluster.Conf.ReplicationPluginTables)
	if len(rules) == 0 {
		return fmt.Errorf("replication-plugin-tables %q names no table", cluster.Conf.ReplicationPluginTables)
	}
	cfg := wire.Config{
		Plugin:          plugin,
		Enabled:         false, // not started before its tables are made
		Mode:            "csv",
		ServerID:        columnstorePluginServerID(cluster.Name + "/" + plugin),
		Source:          wire.Endpoint{Host: source.Host, Port: atoiPort(source.Port), User: cluster.GetDbUser()},
		Target:          wire.Endpoint{Host: target.Host, Port: atoiPort(target.Port), User: cluster.GetDbUser()},
		Rules:           rules,
		BatchTimeoutMs:  cluster.replPluginCheckpointMs(),
		StartGTID:       gtid,
		MaxPendingMB:    1024,
		BatchMB:         64,
		SourceChannel:   channel,
		Destination:     plugin,
		SourceCluster:   parent.Name,
		MergeIntervalMs: cluster.Conf.ReplicationPluginMergeInterval * 1000,
	}
	if err := writeReplPluginConfig(path, cfg); err != nil {
		return err
	}
	results, err := cluster.runReplPluginCreate(plugin, path, cfg)
	if err != nil {
		os.Remove(path)
		return err
	}
	var carried []string
	var skipped []wire.CreateResult
	for _, r := range results {
		if r.Created {
			carried = append(carried, r.Table)
			continue
		}
		skipped = append(skipped, r)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Analytic child %s: %s stays %s, not made a %s table: %s", cluster.Name, r.Table, r.Engine, plugin, r.Reason)
	}
	sort.Strings(carried)
	if err := writeAnalyticSkipped(cluster.analyticSkippedPath(), skipped); err != nil {
		return err
	}
	if len(carried) == 0 {
		os.Remove(path)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Analytic child %s: no table made by %s (replication-plugin-tables %q): nothing for the plugin to carry", cluster.Name, plugin, cluster.Conf.ReplicationPluginTables)
		return nil
	}
	q, err = dbhelper.SetChannelReplicateIgnoreTable(context.Background(), target.Conn, target.DBVersion, channel, carried)
	cluster.LogSQL(q, err, target.URL, "AnalyticChild", config.LvlErr, "Ignore list of channel %s on %s: %s", channel, target.URL, err)
	if err != nil {
		return err
	}
	// no restore to feed: the backup is restored, the plugin streams from the channel's GTID,
	// the tables it made only
	if err := wire.WriteState(wire.StatePath(path), wire.State{GTID: gtid, RestoreDone: true, Tables: carried}); err != nil {
		return err
	}
	cfg.Enabled, cfg.Rules = true, wire.RulesOf(carried)
	if err := writeReplPluginConfig(path, cfg); err != nil {
		return err
	}
	cluster.CheckReplicationPlugins()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Analytic child %s: %d tables made by %s, carried from %s at GTID %s, ignored by channel %s; %d left as they were", cluster.Name, len(carried), plugin, source.URL, gtid, channel, len(skipped))
	return nil
}

// runReplPluginCreate runs the plugin with -create on its TOML at path and reads its results.
func (cluster *Cluster) runReplPluginCreate(plugin, path string, cfg wire.Config) ([]wire.CreateResult, error) {
	bin, err := cluster.verifiedReplPlugin(plugin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), analyticCreateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-config", path, "-create")
	cmd.Env = cluster.replPluginEnv(cfg)
	var stdout bytes.Buffer
	var stderr capBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var out []wire.CreateResult
	sc := bufio.NewScanner(&stdout)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var r wire.CreateResult
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Table != "" {
			out = append(out, r)
		}
	}
	if runErr != nil {
		return out, fmt.Errorf("%s -create: %v: %s", filepath.Base(bin), runErr, lastLine(strings.TrimSpace(stderr.String())))
	}
	return out, nil
}

func writeAnalyticSkipped(path string, skipped []wire.CreateResult) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.Marshal(skipped)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0640)
}

func readAnalyticSkipped(path string) []wire.CreateResult {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var s []wire.CreateResult
	json.Unmarshal(b, &s)
	return s
}

// checkAnalyticChildStates raises, every tick, a schema-domain WARN0340 per table the seed left
// as it was: the operator asked for its copy and it has none.
func (cluster *Cluster) checkAnalyticChildStates() {
	if !cluster.HasAnalyticChild() || cluster.SchemaStateMachine == nil {
		return
	}
	plugin := strings.TrimSpace(cluster.Conf.ReplicationPluginDestination)
	for _, s := range readAnalyticSkipped(cluster.analyticSkippedPath()) {
		cluster.SchemaStateMachine.AddState("WARN0340@"+s.Table, state.State{ErrType: "WARNING", ErrKey: "WARN0340", ErrDesc: fmt.Sprintf(clusterError["WARN0340"], cluster.Name, s.Table, s.Engine, plugin, s.Reason), ErrFrom: "SCHEMA"})
	}
}

// replPluginReplicateIgnore is "<channel>.replicate_ignore_table=<tables>" for the server's
// configuration bundle, from the plugin TOML of a ColumnStore or analytic child, so the
// channel keeps ignoring the carried tables after a restart; "" when there is none.
func (server *ServerMonitor) replPluginReplicateIgnore() string {
	cl := server.ClusterGroup
	if cl == nil {
		return ""
	}
	switch {
	case cl.HasConfigTopoColumnstore():
		return replPluginReplicateIgnoreLine(cl.columnstorePluginPath())
	case cl.HasAnalyticChild():
		return replPluginReplicateIgnoreLine(cl.analyticPluginPath())
	}
	return ""
}

// SetReplicationPluginIntervals sets replication-plugin-checkpoint-interval and
// replication-plugin-merge-interval (seconds, negative refused) and writes them into the
// plugin TOMLs of the child: each plugin restarts with them. A merge interval set back to 0
// merges what the staging holds first (the CDC core).
func (cluster *Cluster) SetReplicationPluginIntervals(checkpoint, merge int) error {
	if checkpoint < 0 || merge < 0 {
		return fmt.Errorf("an interval is a number of seconds, 0 or more")
	}
	cluster.Conf.ReplicationPluginCheckpointInterval, cluster.Conf.ReplicationPluginMergeInterval = checkpoint, merge
	paths := []string{cluster.columnstorePluginPath()}
	if cluster.HasAnalyticChild() {
		paths = append(paths, cluster.analyticPluginPath())
	}
	for _, path := range paths {
		var cfg wire.Config
		if _, err := toml.DecodeFile(path, &cfg); err != nil {
			continue // no such plugin
		}
		cfg.BatchTimeoutMs = cluster.replPluginCheckpointMs()
		cfg.MergeIntervalMs = merge * 1000
		if err := writeReplPluginConfig(path, cfg); err != nil {
			return err
		}
	}
	return nil
}
