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

// The CDC backend of ReplicationLink: a replication plugin's feed (cluster_replplugin.go, the
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
// The other operations (separate read or apply stop, reset of positions, removal, change of
// source, skip) are ErrReplicationNotSupported: the source follows replication-plugin-source
// (cluster_repl_source.go), the definition is the child's seed.

// cdcLinkPrefix names a feed among the links of a server: "cdc:<TOML name>".
const cdcLinkPrefix = "cdc:"

type cdcLink struct {
	cluster *Cluster       // the cluster whose supervisor runs the feed (the child)
	server  *ServerMonitor // its target
	path    string         // its TOML
	cfg     wire.Config
}

// cdcFeedsOf are the feeds of the cluster whose target is server, by TOML path.
func (cluster *Cluster) cdcFeedsOf(server *ServerMonitor) []*cdcLink {
	if cluster == nil || server == nil {
		return nil
	}
	cfgs, _, _ := cluster.readReplPluginConfigs()
	paths := make([]string, 0, len(cfgs))
	for p := range cfgs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var out []*cdcLink
	for _, p := range paths {
		c := cfgs[p]
		if c.Target.Host == server.Host && c.Target.Port == atoiPort(server.Port) {
			out = append(out, &cdcLink{cluster: cluster, server: server, path: p, cfg: c})
		}
	}
	return out
}

// cdcFeedStatus are the status rows of the feeds writing into server (server.ReplicationFeeds).
func (cluster *Cluster) cdcFeedStatus(server *ServerMonitor) []dbhelper.SlaveStatus {
	var out []dbhelper.SlaveStatus
	for _, l := range cluster.cdcFeedsOf(server) {
		out = append(out, l.row())
	}
	return out
}

func (l *cdcLink) Kind() ReplicationKind { return ReplicationKindCDC }

func (l *cdcLink) Name() string {
	return cdcLinkPrefix + strings.TrimSuffix(filepath.Base(l.path), filepath.Ext(l.path))
}

func cdcNull(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func yesNo(b bool) sql.NullString {
	if b {
		return cdcNull("Yes")
	}
	return cdcNull("No")
}

// row is the feed as a status row, from the supervisor's last status line.
func (l *cdcLink) row() dbhelper.SlaveStatus {
	ss := dbhelper.SlaveStatus{
		Kind:           string(ReplicationKindCDC),
		ConnectionName: cdcNull(l.Name()),
		MasterHost:     cdcNull(l.cfg.Source.Host),
		MasterUser:     cdcNull(l.cfg.Source.User),
		MasterPort:     cdcNull(fmt.Sprint(l.cfg.Source.Port)),
	}
	st, stop, running := l.cluster.replPluginRuntime(l.path)
	switch {
	case !l.cfg.Enabled:
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

func (l *cdcLink) Status() (dbhelper.SlaveStatus, bool) { return l.row(), true }

func (l *cdcLink) ReadStatus() (dbhelper.SlaveStatus, string, error) { return l.row(), "", nil }

// setEnabled writes the TOML's enabled flag; the supervisor follows it at once.
func (l *cdcLink) setEnabled(on bool) error {
	if err := rewriteReplPluginConfigs([]string{l.path}, func(c *wire.Config) { c.Enabled = on }); err != nil {
		return err
	}
	l.cfg.Enabled = on
	l.cluster.CheckReplicationPlugins()
	return nil
}

// settingDriven: the feed of the replication-plugin-feed-* settings, whose TOML the tick writes
// from them (syncReplicationFeed): one source of truth, never written here.
func (l *cdcLink) settingDriven() bool {
	return l.path == l.cluster.replFeedPath()
}

func (l *cdcLink) Start(reason string) (string, error) {
	if l.settingDriven() {
		return "", replicationNotSupported("start of a feed driven by its settings (replication-plugin-feed-source-cluster)", l)
	}
	if err := l.setEnabled(true); err != nil {
		return "", err
	}
	l.server.clearLinkChange(l.Name())
	return "enabled = true in " + l.path, nil
}

func (l *cdcLink) Stop(reason string) (string, error) {
	if l.settingDriven() {
		return "", replicationNotSupported("stop of a feed driven by its settings (replication-plugin-feed-source-cluster)", l)
	}
	l.server.recordLinkChange(l.Name(), linkOpStop, reason)
	if err := l.setEnabled(false); err != nil {
		return "", err
	}
	return "enabled = false in " + l.path, nil
}

func (l *cdcLink) StopRead(reason string) (string, error) {
	return "", replicationNotSupported("stop of the read alone", l)
}
func (l *cdcLink) StopApply(reason string) (string, error) {
	return "", replicationNotSupported("stop of the apply alone", l)
}
func (l *cdcLink) ResetPositions(reason string) (string, error) {
	return "", replicationNotSupported("reset of positions (Rejoin rebuilds)", l)
}
func (l *cdcLink) Remove(reason string) (string, error) {
	return "", replicationNotSupported("removal (seed the child again)", l)
}
func (l *cdcLink) ChangeSource(opt dbhelper.ChangeMasterOpt, reason string) (string, error) {
	return "", replicationNotSupported("change of source (replication-plugin-source)", l)
}
func (l *cdcLink) SkipEvent(reason string) (string, error) {
	return "", replicationNotSupported("skip of an event", l)
}

// Rejoin resumes the feed when it can, else asks for a new initial load through
// ReinitReplicationPlugin (cluster_replplugin_feed.go, the one implementation of the rebuild):
//   - a capture source of another engine (SourceKind, a PostgreSQL feed): its State says when it
//     needs a new initial load (NeedsInit); otherwise it resumes;
//   - a MariaDB/MySQL binlog: the GTID applied is checked against the source's binary logs
//     (gtidRetained); purged, ReinitReplicationPlugin refuses with the reason (the copies' new
//     start is the child's seed) and the feed stays recorded, WARN0352 saying why.
func (l *cdcLink) Rejoin() error {
	cluster := l.cluster
	if l.cfg.SourceKind != "" {
		why, needs, err := replFeedNeedsInit(l.path)
		if err != nil {
			return fmt.Errorf("state of %s: %w", l.Name(), err)
		}
		if needs {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: a new initial load (%s)", l.Name(), l.server.URL, why)
			if err := cluster.ReinitReplicationPlugin(l.cfg.Plugin); err != nil {
				l.server.recordLinkChange(l.Name(), linkOpStop, "rejoin: "+err.Error())
				return err
			}
		}
		return l.resume()
	}
	st, err := wire.ReadState(wire.StatePath(l.path))
	if err != nil {
		return fmt.Errorf("state of %s: %w", l.Name(), err)
	}
	from := st.GTID
	if a, err := readReplPluginApplied(wire.AppliedPath(l.path)); err == nil && a.GTID != "" {
		from = a.GTID
	}
	source := cluster.resolveReplPluginSource(l.cfg)
	if source == nil || source.Conn == nil {
		return fmt.Errorf("feed %s: no source to read (see its state)", l.Name())
	}
	if from != "" {
		ok, why, err := source.gtidRetained(from, !source.IsMariaDB(), "CDC", "Rejoin of feed "+l.Name())
		if err != nil {
			return err
		}
		if !ok {
			err := cluster.ReinitReplicationPlugin(l.cfg.Plugin)
			if err == nil {
				return l.resume()
			}
			err = fmt.Errorf("feed %s: %s lost the binary logs from %s (%s): %w", l.Name(), source.URL, from, why, err)
			l.server.recordLinkChange(l.Name(), linkOpStop, "rejoin: "+err.Error())
			return err
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: %s still has the binary logs from %s, the feed resumes from its State", l.Name(), l.server.URL, source.URL, from)
	return l.resume()
}

// resume starts the feed again: Start, or for a setting-driven feed the next tick, which
// writes its TOML from the settings.
func (l *cdcLink) resume() error {
	if l.settingDriven() {
		l.server.clearLinkChange(l.Name())
		l.cluster.CheckReplicationPlugins()
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
