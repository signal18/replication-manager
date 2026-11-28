// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The CDC plugin: a replication plugin's feed (cluster_replplugin.go, the
// CDC core cluster/replplugin/cdc) is a link of the server it writes into, its target. Its
// surface is the plugin TOML (<cluster>/replication-plugins/<name>.toml), its State and applied
// files, and the status line the supervisor keeps: nothing else, so that any destination plugin
// (InnoDB, ColumnStore, DuckDB, ...) is a CDC link without code here.
//
//   - status: capture = read (Slave_IO_Running), apply = apply (Slave_SQL_Running), the lag, the
//     GTID captured (Gtid_IO_Pos) and applied (Gtid_Slave_Pos), the last error of each; the fields
//     with no meaning for a feed stay empty;
//   - start, stop: the TOML's enabled flag, the supervisor starts or stops the process;
//   - rejoin: resume from the State when the source still has its log from the position
//     applied, else a new initial load through ReinitReplicationPlugin (Rejoin);
//   - it never writes to the source: it reads its binary log list and positions only (F1).
//
// One generic manifest for every CDC binary until step 4 of the design gives each its own. The
// other actions (a separate start or stop of the copy or the apply, reset of positions, removal,
// change of source, skip, bootstrap, creation) are not in its manifest: ErrReplicationNotSupported: the source follows replication-plugin-source
// (cluster_repl_source.go), the definition is the child's seed.

// cdcLinkPrefix names a feed among the links of a server: "cdc:<TOML name>".
const cdcLinkPrefix = "cdc:"

// cdcFeed is a feed's configuration: what its TOML says.
type cdcFeed struct {
	cluster *Cluster // the cluster whose supervisor runs the feed (the child)
	path    string   // its TOML
	cfg     wire.Config
}

type cdcReplicationPlugin struct {
	unsupportedReplicationPlugin
}

// cdcManifest: status, start, stop and rejoin; never promote nor change-source (the failover
// leaves a feed alone).
var cdcManifest = ReplicationManifest{
	Name:   ReplicationPluginCDC,
	Type:   string(ReplicationKindCDC),
	Source: []string{"MariaDB", "MySQL", "Percona", "PostgreSQL"},
	Target: []string{"MariaDB", "ColumnStore", "DuckDB"},
	Topologies: []ReplicationTopology{
		{Name: "feed", Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	},
	Actions: []string{ReplicationActionStatus, ReplicationActionStart, ReplicationActionStop, ReplicationActionRejoin},
}

func (p *cdcReplicationPlugin) Manifest() ReplicationManifest { return cdcManifest }

// Bootstrap: step 5 of the design moves the seed of the child here.
func (p *cdcReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, ReplicationPluginCDC)
}

// Create: step 5, with the bootstrap.
func (p *cdcReplicationPlugin) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionCreate, name, ReplicationPluginCDC)
}

// cdcReplication is one feed into its target server: the row of the supervisor's status line
// when it was built, its source resolved from the TOML's source host and port.
type cdcReplication struct {
	replicationBase
	feed *cdcFeed // nil for a copy decoded from JSON
}

// cdcFeedReplication is the replication of one feed into server.
func (cluster *Cluster) cdcFeedReplication(server *ServerMonitor, path string, cfg wire.Config) *cdcReplication {
	name := cdcLinkPrefix + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	f := &cdcFeed{cluster: cluster, path: path, cfg: cfg}
	r := &cdcReplication{feed: f}
	r.replicationBase = newReplicationBase(cdcManifest, server, name, cdcRow(name, f), cfg.Source.Host, fmt.Sprint(cfg.Source.Port))
	return r
}

// cdcFeedsOf are the feeds of the cluster whose target is server, by TOML path: what Refresh
// adds to server.Replications after the native channels.
func (cluster *Cluster) cdcFeedsOf(server *ServerMonitor) ReplicationList {
	if cluster == nil || server == nil {
		return nil
	}
	cfgs, _, _ := cluster.readReplPluginConfigs()
	paths := make([]string, 0, len(cfgs))
	for p := range cfgs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out ReplicationList
	for _, p := range paths {
		c := cfgs[p]
		if c.Target.Host == server.Host && c.Target.Port == atoiPort(server.Port) {
			out = append(out, cluster.cdcFeedReplication(server, p, c))
		}
	}
	return out
}

// feedOf is the feed, refused for a copy decoded from JSON.
func (r *cdcReplication) feedOf() (*cdcFeed, error) {
	if r.feed == nil || r.target == nil {
		return nil, r.noTarget()
	}
	return r.feed, nil
}

func cdcNull(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func yesNo(b bool) sql.NullString {
	if b {
		return cdcNull("Yes")
	}
	return cdcNull("No")
}

// cdcRow is the feed as a status row, from the supervisor's last status line.
func cdcRow(name string, f *cdcFeed) dbhelper.SlaveStatus {
	ss := dbhelper.SlaveStatus{
		Kind:           string(ReplicationKindCDC),
		ConnectionName: cdcNull(name),
		MasterHost:     cdcNull(f.cfg.Source.Host),
		MasterUser:     cdcNull(f.cfg.Source.User),
		MasterPort:     cdcNull(fmt.Sprint(f.cfg.Source.Port)),
	}
	st, stop, running := f.cluster.replPluginRuntime(f.path)
	switch {
	case !f.cfg.Enabled:
		ss.SlaveIORunning, ss.SlaveSQLRunning = cdcNull("No"), cdcNull("No")
		ss.SlaveSQLRunningState = cdcNull("disabled")
	case stop != nil:
		ss.SlaveIORunning, ss.SlaveSQLRunning = cdcNull("No"), cdcNull("No")
		ss.LastIOError = cdcNull(stop.Reason)
		ss.SlaveSQLRunningState = cdcNull("stopped, restart at " + stop.Restart.Format(time.RFC3339))
	case !running || st == nil:
		ss.SlaveIORunning, ss.SlaveSQLRunning = cdcNull("No"), cdcNull("No")
		ss.SlaveSQLRunningState = cdcNull("not started")
	default:
		capturing := st.State == wire.StateRunning || st.State == wire.StateStarting
		ss.SlaveIORunning = yesNo(capturing)
		ss.SlaveSQLRunning = yesNo(capturing && st.ApplyError == "")
		ss.LastIOError = cdcNull(st.Error)
		ss.LastSQLError = cdcNull(st.ApplyError)
		ss.SlaveSQLRunningState = cdcNull(st.Description)
	}
	if st != nil {
		ss.SecondsBehindMaster = sql.NullInt64{Int64: st.LagSeconds, Valid: running && stop == nil}
		ss.GtidIOPos = cdcNull(st.GTID)
		ss.GtidSlavePos = cdcNull(st.AppliedGTID)
	}
	return ss
}

// replPluginRuntime is the feed's last status line, its stop and whether a runner holds it.
func (cluster *Cluster) replPluginRuntime(path string) (*wire.Status, *replPluginStop, bool) {
	cluster.replPluginsMu.Lock()
	defer cluster.replPluginsMu.Unlock()
	r := cluster.replPlugins[path]
	if r == nil {
		return nil, nil, false
	}
	return r.status.Load(), r.stopped.Load(), true
}

// Status is the feed's row from the supervisor's last status line now: no statement.
func (r *cdcReplication) Status() (dbhelper.SlaveStatus, string, error) {
	f, err := r.feedOf()
	if err != nil {
		return dbhelper.SlaveStatus{}, "", err
	}
	return cdcRow(r.name, f), "", nil
}

// setEnabled writes the TOML's enabled flag; the supervisor follows it at once.
func (f *cdcFeed) setEnabled(on bool) error {
	if err := rewriteReplPluginConfigs([]string{f.path}, func(c *wire.Config) { c.Enabled = on }); err != nil {
		return err
	}
	f.cfg.Enabled = on
	f.cluster.CheckReplicationPlugins()
	return nil
}

// settingDriven: the feed of the replication-plugin-feed-* settings, whose TOML the tick writes
// from them (syncReplicationFeed): one source of truth, never written here.
func (f *cdcFeed) settingDriven() bool {
	return f.path == f.cluster.replFeedPath()
}

func (r *cdcReplication) Start(reason string) (string, error) {
	f, err := r.feedOf()
	if err != nil {
		return "", err
	}
	if f.settingDriven() {
		return "", r.notSupported("start of a feed driven by its settings (replication-plugin-feed-source-cluster)")
	}
	if err := f.setEnabled(true); err != nil {
		return "", err
	}
	r.target.clearLinkChange(r.name)
	return "enabled = true in " + f.path, nil
}

// Stop disables the feed, recorded once its TOML says so.
func (r *cdcReplication) Stop(reason string) (string, error) {
	f, err := r.feedOf()
	if err != nil {
		return "", err
	}
	if f.settingDriven() {
		return "", r.notSupported("stop of a feed driven by its settings (replication-plugin-feed-source-cluster)")
	}
	if err := f.setEnabled(false); err != nil {
		return "", err
	}
	r.target.recordLinkChange(r.name, linkOpStop, reason)
	return "enabled = false in " + f.path, nil
}

// Rejoin resumes the feed when it can, else asks for a new initial load through
// ReinitReplicationPlugin (cluster_replplugin_feed.go, the one implementation of the rebuild):
//   - a capture source of another engine (SourceKind, a PostgreSQL feed): its State says when it
//     needs a new initial load (NeedsInit); otherwise it resumes;
//   - a MariaDB/MySQL binlog: the GTID applied is checked against the source's binary logs
//     (gtidRetained); purged, ReinitReplicationPlugin refuses with the reason (the copies' new
//     start is the child's seed) and the feed stays recorded, WARN0352 saying why.
func (r *cdcReplication) Rejoin() error {
	f, err := r.feedOf()
	if err != nil {
		return err
	}
	cluster := f.cluster
	if f.cfg.SourceKind != "" {
		why, needs, err := replFeedNeedsInit(f.path)
		if err != nil {
			return fmt.Errorf("state of %s: %w", r.name, err)
		}
		if needs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: a new initial load (%s)", r.name, r.target.URL, why)
			if err := cluster.ReinitReplicationPlugin(f.cfg.Plugin); err != nil {
				r.target.recordLinkChange(r.name, linkOpStop, "rejoin: "+err.Error())
				return err
			}
		}
		return r.resume()
	}
	st, err := wire.ReadState(wire.StatePath(f.path))
	if err != nil {
		return fmt.Errorf("state of %s: %w", r.name, err)
	}
	from := st.GTID
	if a, err := readReplPluginApplied(wire.AppliedPath(f.path)); err == nil && a.GTID != "" {
		from = a.GTID
	}
	source := cluster.resolveReplPluginSource(f.cfg)
	if source == nil || source.Conn == nil {
		return fmt.Errorf("feed %s: no source to read (see its state)", r.name)
	}
	if from != "" {
		ok, why, err := source.gtidRetained(from, !source.IsMariaDB(), "CDC", "Rejoin of feed "+r.name)
		if err != nil {
			return err
		}
		if !ok {
			err := cluster.ReinitReplicationPlugin(f.cfg.Plugin)
			if err == nil {
				return r.resume()
			}
			err = fmt.Errorf("feed %s: %s lost the binary logs from %s (%s): %w", r.name, source.URL, from, why, err)
			r.target.recordLinkChange(r.name, linkOpStop, "rejoin: "+err.Error())
			return err
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: %s still has the binary logs from %s, the feed resumes from its State", r.name, r.target.URL, source.URL, from)
	return r.resume()
}

// resume starts the feed again: Start, or for a setting-driven feed the next tick, which
// writes its TOML from the settings.
func (r *cdcReplication) resume() error {
	f := r.feed
	if f.settingDriven() {
		r.target.clearLinkChange(r.name)
		f.cluster.CheckReplicationPlugins()
		return nil
	}
	_, err := r.Start(LinkByRejoin)
	return err
}

// readReplPluginApplied reads a feed's applied file.
func readReplPluginApplied(path string) (wire.Applied, error) {
	var a wire.Applied
	b, err := os.ReadFile(path)
	if err != nil {
		return a, err
	}
	return a, json.Unmarshal(b, &a)
}
