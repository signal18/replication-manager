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

// cdcFeed is a feed link's configuration (Link.Config).
type cdcFeed struct {
	cluster *Cluster // the cluster whose supervisor runs the feed (the child)
	path    string   // its TOML
	cfg     wire.Config
}

type cdcReplicationPlugin struct {
	unsupportedReplicationPlugin
}

func (p *cdcReplicationPlugin) Manifest() ReplicationManifest {
	return ReplicationManifest{
		Name:   ReplicationPluginCDC,
		Type:   string(ReplicationKindCDC),
		Source: []string{"MariaDB", "MySQL", "Percona", "PostgreSQL"},
		Target: []string{"MariaDB", "ColumnStore", "DuckDB"},
		Topologies: []ReplicationTopology{
			{Name: "feed", Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
		},
		Actions: []string{ReplicationActionStatus, ReplicationActionStart, ReplicationActionStop, ReplicationActionRejoin},
	}
}

// Bootstrap: step 5 of the design moves the seed of the child here.
func (p *cdcReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, ReplicationPluginCDC)
}

// Create: step 5, with the bootstrap.
func (p *cdcReplicationPlugin) Create(l Link, reason string) (string, error) {
	return "", replicationNotSupported(ReplicationActionCreate+" (step 5)", l)
}

// cdcFeedOf is the link's configuration; links are built by cdcFeedLink.
func cdcFeedOf(l Link) *cdcFeed {
	f, _ := l.Config.(*cdcFeed)
	if f == nil {
		f = &cdcFeed{}
	}
	return f
}

// cdcFeedLink is the link of one feed into server.
func (cluster *Cluster) cdcFeedLink(server *ServerMonitor, path string, cfg wire.Config) Link {
	return Link{
		Plugin: cluster.ReplicationPlugin(ReplicationPluginCDC),
		Server: server,
		Name:   cdcLinkPrefix + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
		Config: &cdcFeed{cluster: cluster, path: path, cfg: cfg},
	}
}

// cdcFeedsOf are the feeds of the cluster whose target is server, by TOML path.
func (cluster *Cluster) cdcFeedsOf(server *ServerMonitor) []Link {
	if cluster == nil || server == nil {
		return nil
	}
	cfgs, _, _ := cluster.readReplPluginConfigs()
	paths := make([]string, 0, len(cfgs))
	for p := range cfgs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []Link
	for _, p := range paths {
		c := cfgs[p]
		if c.Target.Host == server.Host && c.Target.Port == atoiPort(server.Port) {
			out = append(out, cluster.cdcFeedLink(server, p, c))
		}
	}
	return out
}

// cdcFeedStatus are the status rows of the feeds writing into server (server.ReplicationFeeds).
func (cluster *Cluster) cdcFeedStatus(server *ServerMonitor) []dbhelper.SlaveStatus {
	var out []dbhelper.SlaveStatus
	for _, l := range cluster.cdcFeedsOf(server) {
		out = append(out, cdcRow(l))
	}
	return out
}

func cdcNull(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func yesNo(b bool) sql.NullString {
	if b {
		return cdcNull("Yes")
	}
	return cdcNull("No")
}

// cdcRow is the feed as a status row, from the supervisor's last status line.
func cdcRow(l Link) dbhelper.SlaveStatus {
	f := cdcFeedOf(l)
	ss := dbhelper.SlaveStatus{
		Kind:           string(ReplicationKindCDC),
		ConnectionName: cdcNull(l.Name),
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

// Status is the feed's row from the supervisor's last status line: no statement.
func (p *cdcReplicationPlugin) Status(l Link) (dbhelper.SlaveStatus, string, error) {
	return cdcRow(l), "", nil
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

func (p *cdcReplicationPlugin) Start(l Link, reason string) (string, error) {
	f := cdcFeedOf(l)
	if f.settingDriven() {
		return "", replicationNotSupported("start of a feed driven by its settings (replication-plugin-feed-source-cluster)", l)
	}
	if err := f.setEnabled(true); err != nil {
		return "", err
	}
	l.Server.clearLinkChange(l.Name)
	return "enabled = true in " + f.path, nil
}

func (p *cdcReplicationPlugin) Stop(l Link, reason string) (string, error) {
	f := cdcFeedOf(l)
	if f.settingDriven() {
		return "", replicationNotSupported("stop of a feed driven by its settings (replication-plugin-feed-source-cluster)", l)
	}
	l.Server.recordLinkChange(l.Name, linkOpStop, reason)
	if err := f.setEnabled(false); err != nil {
		return "", err
	}
	return "enabled = false in " + f.path, nil
}

// Rejoin resumes the feed when it can, else asks for a new initial load through
// ReinitReplicationPlugin (cluster_replplugin_feed.go, the one implementation of the rebuild):
//   - a capture source of another engine (SourceKind, a PostgreSQL feed): its State says when it
//     needs a new initial load (NeedsInit); otherwise it resumes;
//   - a MariaDB/MySQL binlog: the GTID applied is checked against the source's binary logs
//     (gtidRetained); purged, ReinitReplicationPlugin refuses with the reason (the copies' new
//     start is the child's seed) and the feed stays recorded, WARN0352 saying why.
func (p *cdcReplicationPlugin) Rejoin(l Link) error {
	f := cdcFeedOf(l)
	cluster := f.cluster
	if f.cfg.SourceKind != "" {
		why, needs, err := replFeedNeedsInit(f.path)
		if err != nil {
			return fmt.Errorf("state of %s: %w", l.Name, err)
		}
		if needs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: a new initial load (%s)", l.Name, l.Server.URL, why)
			if err := cluster.ReinitReplicationPlugin(f.cfg.Plugin); err != nil {
				l.Server.recordLinkChange(l.Name, linkOpStop, "rejoin: "+err.Error())
				return err
			}
		}
		return p.resume(l)
	}
	st, err := wire.ReadState(wire.StatePath(f.path))
	if err != nil {
		return fmt.Errorf("state of %s: %w", l.Name, err)
	}
	from := st.GTID
	if a, err := readReplPluginApplied(wire.AppliedPath(f.path)); err == nil && a.GTID != "" {
		from = a.GTID
	}
	source := cluster.resolveReplPluginSource(f.cfg)
	if source == nil || source.Conn == nil {
		return fmt.Errorf("feed %s: no source to read (see its state)", l.Name)
	}
	if from != "" {
		ok, why, err := source.gtidRetained(from, !source.IsMariaDB(), "CDC", "Rejoin of feed "+l.Name)
		if err != nil {
			return err
		}
		if !ok {
			err := cluster.ReinitReplicationPlugin(f.cfg.Plugin)
			if err == nil {
				return p.resume(l)
			}
			err = fmt.Errorf("feed %s: %s lost the binary logs from %s (%s): %w", l.Name, source.URL, from, why, err)
			l.Server.recordLinkChange(l.Name, linkOpStop, "rejoin: "+err.Error())
			return err
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: %s still has the binary logs from %s, the feed resumes from its State", l.Name, l.Server.URL, source.URL, from)
	return p.resume(l)
}

// resume starts the feed again: Start, or for a setting-driven feed the next tick, which
// writes its TOML from the settings.
func (p *cdcReplicationPlugin) resume(l Link) error {
	f := cdcFeedOf(l)
	if f.settingDriven() {
		l.Server.clearLinkChange(l.Name)
		f.cluster.CheckReplicationPlugins()
		return nil
	}
	_, err := l.Start(LinkByRejoin)
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
