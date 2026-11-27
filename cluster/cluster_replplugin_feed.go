// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// A CDC feed into this cluster from a source cluster of another engine
// (doc/implementation/cluster/CDC_SOURCES.md): replication-plugin-feed-source-cluster names it,
// empty is the off switch. The feed is a replication plugin like the analytic child's (the same
// supervisor, its TOML in the cluster's replication-plugins directory), whose binary brings the
// capture of that engine (wire.Config.SourceKind) and applies to this cluster's master. Nothing
// is created on the source unless replication-plugin-feed-create-source-objects allows it (F1);
// what the feed created there is in its State (Owned), removed only on the operator's request
// (DropReplicationFeedSourceObjects).

// feedPluginOfKind: the plugin binary of a source engine
var feedPluginOfKind = map[string]string{"postgresql": "postgresql"}

const feedTOMLName = "feed.toml"

func (cluster *Cluster) replFeedPath() string {
	return filepath.Join(cluster.replPluginDir(), feedTOMLName)
}

// FeedObjectName is the publication and the slot of the feed on the source, from the cluster's
// name (lower case letters, digits and _: never an identifier that needs quoting)
func FeedObjectName(cluster string) string {
	var b strings.Builder
	b.WriteString("repman_")
	for _, r := range strings.ToLower(cluster) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if len(s) > 63 {
		s = s[:63]
	}
	return s
}

// replFeedConfig is the feed's TOML from the settings; ok false when there is no feed, or
// none can run now (no source, no master: the state says why).
func (cluster *Cluster) replFeedConfig() (wire.Config, bool) {
	name := strings.TrimSpace(cluster.Conf.ReplicationPluginFeedSourceCluster)
	if name == "" {
		return wire.Config{}, false
	}
	src, err := cluster.GetClusterFromName(name)
	if err != nil || src == nil {
		cluster.SetState("ERR00131", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00131"], "feed", cluster.Name, name, "is not a cluster of this replication-manager", name), ErrFrom: "REPL"})
		return wire.Config{}, false
	}
	if !src.isPostgresCluster() {
		cluster.SetState("ERR00132", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00132"], "feed", cluster.Name, "the source cluster "+name+" is not a PostgreSQL cluster: a MariaDB source is an analytic child"), ErrFrom: "REPL"})
		return wire.Config{}, false
	}
	sm := src.GetMaster()
	tm := cluster.GetMaster()
	if sm == nil || tm == nil {
		return wire.Config{}, false // the source follows the master: none known yet
	}
	sourceKind := "postgresql"
	obj := FeedObjectName(cluster.Name)
	retained := cluster.Conf.ReplicationPluginFeedRetainedMax
	if retained <= 0 {
		retained = 10240
	}
	return wire.Config{
		Plugin:         feedPluginOfKind[sourceKind],
		Enabled:        true,
		SourceKind:     sourceKind,
		SourceCluster:  name,
		Source:         wire.Endpoint{Host: sm.Host, Port: atoiPort(sm.Port), User: src.GetDbUser(), Database: strings.TrimSpace(cluster.Conf.ReplicationPluginFeedDatabase)},
		Target:         wire.Endpoint{Host: tm.Host, Port: atoiPort(tm.Port), User: cluster.GetDbUser()},
		Rules:          analyticRules(cluster.Conf.ReplicationPluginFeedTables),
		BatchTimeoutMs: cluster.replPluginCheckpointMs(),
		SourceOptions: map[string]string{
			"slot":                  obj,
			"publication":           obj,
			"create_source_objects": strconv.FormatBool(cluster.Conf.ReplicationPluginFeedCreateSourceObjects),
			"retained_max_mb":       strconv.Itoa(retained),
		},
	}, true
}

// syncReplicationFeed keeps the feed's TOML on the settings, every tick before the supervisor:
// rewritten when a setting or a master changed (the supervisor restarts the plugin from its
// State), disabled when the feed is stopped (replication-plugin-feed-enabled off) or has no
// source (its State and what it created on the source stay: a feed started again resumes;
// DropReplicationFeedSourceObjects removes them). It is the only writer of the feed TOML's
// enabled: a stop or a start (the GUI, the replication interface) changes the setting, never
// the file.
func (cluster *Cluster) syncReplicationFeed() {
	path := cluster.replFeedPath()
	var cur wire.Config
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if exists {
		if _, err := toml.DecodeFile(path, &cur); err != nil {
			return // the supervisor says it (WARN0242)
		}
	}
	want, ok := cluster.replFeedConfig()
	off := strings.TrimSpace(cluster.Conf.ReplicationPluginFeedSourceCluster) == "" || !cluster.Conf.ReplicationPluginFeedEnabled
	if !ok || off {
		if exists && cur.Enabled && off {
			cur.Enabled = false
			if ok {
				cur = want // the settings changed while stopped: written, not run
				cur.Enabled = false
			}
			if err := writeReplFeedConfig(path, cur); err == nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "CDC feed of %s stopped: the plugin stops, its position and what it created on the source are kept", cluster.Name)
			}
		}
		return
	}
	if exists && sameReplPluginConfig(cur, want) {
		return
	}
	if err := writeReplFeedConfig(path, want); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "CDC feed of %s: %s not written: %s", cluster.Name, path, err)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "CDC feed of %s from %s (%s:%d, database %s) written to %s", cluster.Name, want.SourceCluster, want.Source.Host, want.Source.Port, want.Source.Database, path)
}

// sameReplPluginConfig compares two plugin TOMLs as they are written: a slice or map empty on
// one side and nil on the other (a TOML read back) is the same file, never a rewrite (which
// would restart the plugin every tick).
func sameReplPluginConfig(a, b wire.Config) bool {
	var ea, eb bytes.Buffer
	if toml.NewEncoder(&ea).Encode(a) != nil || toml.NewEncoder(&eb).Encode(b) != nil {
		return false
	}
	return bytes.Equal(ea.Bytes(), eb.Bytes())
}

func writeReplFeedConfig(path string, cfg wire.Config) error {
	return writeReplPluginTOML(path, cfg, "# replication-manager: the CDC feed of the replication-plugin-feed-* settings (CDC_SOURCES.md), rewritten from them\n")
}

// replFeedNeedsInit reads the State of the plugin TOML at path: the reason when the feed
// waits for a new initial load.
func replFeedNeedsInit(path string) (string, bool) {
	st, err := wire.ReadState(wire.StatePath(path))
	if err != nil || !st.NeedsInit {
		return "", false
	}
	return st.Reason, true
}

// ErrNoReplicationPlugin: no plugin TOML of that name in the cluster.
var ErrNoReplicationPlugin = errors.New("no such replication plugin in the cluster")

// replPluginPathOf is the TOML of the cluster's plugin named plugin.
func (cluster *Cluster) replPluginPathOf(plugin string) (string, wire.Config, error) {
	cfgs, _, _ := cluster.readReplPluginConfigs()
	for path, c := range cfgs {
		if c.Plugin == plugin {
			return path, c, nil
		}
	}
	return "", wire.Config{}, fmt.Errorf("%w: %s", ErrNoReplicationPlugin, plugin)
}

// stopReplPluginAt stops the runner of the TOML at path and waits for its process to end (a
// SIGTERM, then the grace period). The stop-wait of the rebuild and of the removal; it never
// touches the source, nor the TOML (the next tick starts it again when the TOML is enabled).
func (cluster *Cluster) stopReplPluginAt(path string) {
	cluster.replPluginsMu.Lock()
	r := cluster.replPlugins[path]
	if r != nil {
		cluster.stopReplPluginLocked(path, r)
	}
	cluster.replPluginsMu.Unlock()
	if r != nil {
		<-r.done
	}
}

// ReinitReplicationPlugin is the rebuild of a feed with a capture source, the one implementation
// (the API, and the replication interface's CDC link rejoin when the source lost the position):
// a new initial load. It never touches the source: the plugin stopped, its
// position, applied file, checkpoints and merges removed; what it created on the source
// (State.Owned) is kept, so the new initial load replaces its own slot and never another. The
// next tick starts it.
func (cluster *Cluster) ReinitReplicationPlugin(plugin string) error {
	path, cfg, err := cluster.replPluginPathOf(plugin)
	if err != nil {
		return err
	}
	if cfg.SourceKind == "" {
		return fmt.Errorf("replication plugin %s reads a MariaDB binlog: its new start is a new seed of the child", plugin)
	}
	cluster.stopReplPluginAt(path)
	st, err := wire.ReadState(wire.StatePath(path))
	if err != nil {
		return err
	}
	// a checkpoint left behind would be applied after the new initial load
	if err := os.RemoveAll(wire.CheckpointDir(path)); err != nil {
		return err
	}
	for _, f := range []string{wire.AppliedPath(path), wire.MergedPath(path)} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := wire.WriteState(wire.StatePath(path), wire.State{Owned: st.Owned}); err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s: a new initial load requested (position %s dropped; kept on the source: %s)", plugin, cluster.Name, st.GTID, strings.Join(st.Owned, ", "))
	return nil
}

// replPluginDropTimeout bounds a plugin run with -drop, replPluginDropStdoutMax what it prints
const (
	replPluginDropTimeout   = 2 * time.Minute
	replPluginDropStdoutMax = 64 * 1024
)

// DropReplicationFeedSourceObjects removes what the cluster's feed plugin created on its source
// (State.Owned: its replication slot, its publication; never anything else), on the operator's
// request, with the feed switched off first: the plugin runs with -drop, each statement it ran
// logged and returned (F11).
func (cluster *Cluster) DropReplicationFeedSourceObjects(plugin string) ([]string, error) {
	path, cfg, err := cluster.replPluginPathOf(plugin)
	if err != nil {
		return nil, err
	}
	if cfg.SourceKind == "" {
		return nil, fmt.Errorf("replication plugin %s created nothing on its source", plugin)
	}
	if cfg.Enabled {
		return nil, fmt.Errorf("the feed %s runs: stop it first (replication-plugin-feed-enabled off)", plugin)
	}
	cluster.stopReplPluginAt(path)
	bin, err := cluster.verifiedReplPlugin(plugin)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), replPluginDropTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-config", path, "-drop")
	cmd.Env = cluster.replPluginEnv(cfg)
	var stderr capBuffer
	stdout := capBuffer{max: replPluginDropStdoutMax}
	cmd.Stderr, cmd.Stdout = &stderr, &stdout
	err = cmd.Run()
	var ran []string
	for _, l := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			ran = append(ran, l)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s ran on its source %s:%d: %s", plugin, cluster.Name, cfg.Source.Host, cfg.Source.Port, l)
		}
	}
	if err != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return ran, fmt.Errorf("%v: %s", err, lastLine(s))
		}
		return ran, err
	}
	return ran, nil
}

// checkReplPluginSourceStates raises the states of a capture source from its status line: what
// the source keeps for the feed (CINF0040), a warning on its settings or its bound (WARN0360),
// and logs each statement it ran on the source once (F11).
func (cluster *Cluster) checkReplPluginSourceStates(r *replPluginRunner, st *wire.Status) {
	if st.SourceKind == "" {
		return
	}
	name := r.cfg.Plugin
	src := fmt.Sprintf("%s:%d", r.cfg.Source.Host, r.cfg.Source.Port)
	detail := ""
	if st.InitialLoad {
		detail = fmt.Sprintf("; initial load running, %d tables copied", st.InitialLoadTables)
	}
	cluster.SetState("CINF0040", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0040"], name, cluster.Name, src, st.SourceRetainedMB, st.SourceRetainedMaxMB, detail), ErrFrom: "REPL"})
	if st.SourceWarning != "" {
		cluster.SetState("WARN0360", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0360"], name, cluster.Name, src, st.SourceWarning), ErrFrom: "REPL"})
	}
	r.logSourceStatements(st.SourceStatements, st.SourceStatementsTotal, func(q string) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication plugin %s of %s ran on its source %s: %s", name, cluster.Name, src, q)
	})
}

// logSourceStatements logs the statements of stmts not logged yet. total counts every statement
// the process ran (the status keeps the last wire.MaxSourceStatements of them): the new ones are
// the last total-logged of stmts. A new process of the plugin (its generation, or a total lower
// than the one logged) starts the count again.
func (r *replPluginRunner) logSourceStatements(stmts []string, total int64, log func(string)) {
	r.inMu.Lock()
	gen := r.gen
	r.inMu.Unlock()
	r.stmtMu.Lock()
	defer r.stmtMu.Unlock()
	if gen != r.loggedGen || total < r.loggedStmts {
		r.loggedGen, r.loggedStmts = gen, 0
	}
	n := total - r.loggedStmts
	if n > int64(len(stmts)) {
		n = int64(len(stmts)) // more than the status keeps: the older ones are lost, said by the count
	}
	for _, q := range stmts[int64(len(stmts))-n:] {
		log(q)
	}
	r.loggedStmts = total
}
