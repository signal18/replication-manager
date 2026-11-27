// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
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
//   - rejoin: resume from the State when the source still has the binary logs from the GTID
//     applied, else rebuild the denormalized tables (their init on a consistent snapshot) and
//     resume from the source's current GTID;
//   - it never writes to the source: it reads its binary log list and positions only (F1).
//
// The other operations (separate read or apply stop, reset of positions, removal, change of
// source, skip) are ErrReplicationNotSupported: the source follows replication-plugin-source
// (cluster_repl_source.go), the definition is the child's seed.

// cdcLinkPrefix names a feed among the links of a server: "cdc:<TOML name>".
const cdcLinkPrefix = "cdc:"

// cdcStopWait bounds the wait for a feed's process to exit before its files are rebuilt.
const cdcStopWait = replPluginStopGrace + 10*time.Second

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

func (l *cdcLink) Start(reason string) (string, error) {
	if err := l.setEnabled(true); err != nil {
		return "", err
	}
	l.server.clearLinkChange(l.Name())
	return "enabled = true in " + l.path, nil
}

func (l *cdcLink) Stop(reason string) (string, error) {
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

// errCDCRebuildNeedsSeed: the source lost the binary logs a feed's copied tables need, and only
// the child's seed (a restore of the parent's backup) loads them again.
var errCDCRebuildNeedsSeed = errors.New("the copied tables need the child's seed again")

// Rejoin resumes the feed, or rebuilds it when its source no longer has the binary logs from
// the GTID it applied (CINF0032). A feed it cannot rebuild stays stopped with WARN0352 saying why.
func (l *cdcLink) Rejoin() error {
	cluster := l.cluster
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
	retained := true
	why := ""
	if from != "" {
		retained, why, err = source.gtidRetained(from, !source.IsMariaDB(), "CDC", "Rejoin of feed "+l.Name())
		if err != nil {
			return err
		}
	}
	if retained {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: %s still has the binary logs from %s, the feed resumes from its State", l.Name(), l.server.URL, source.URL, from)
		_, err := l.Start(LinkByRejoin)
		return err
	}
	return l.rebuild(source, from, why, st)
}

// rebuild runs the init of the feed's denormalized tables again and resumes the capture from the
// source's current GTID: what was captured before is lost with the binary logs. The init reads
// the source on one consistent snapshot (CDC_CORE.md, "Init"), and a change captured from that
// GTID on is applied on top, which gives the same rows. A feed that copies tables
// (State.Tables) cannot be rebuilt here: their initial load is the child's seed.
func (l *cdcLink) rebuild(source *ServerMonitor, from, why string, st wire.State) error {
	cluster := l.cluster
	if len(st.Tables) > 0 {
		err := fmt.Errorf("feed %s: %s lost the binary logs from %s (%s): %w (reseed the child master %s from its parent)", l.Name(), source.URL, from, why, errCDCRebuildNeedsSeed, l.server.URL)
		l.server.recordLinkChange(l.Name(), linkOpStop, "rejoin: "+err.Error())
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoin of feed %s on %s: %s lost the binary logs from %s (%s), rebuilding its denormalized tables", l.Name(), l.server.URL, source.URL, from, why)
	if _, err := l.Stop(LinkByRejoin); err != nil {
		return err
	}
	if err := cluster.waitReplPluginStopped(l.path, cdcStopWait); err != nil {
		return err
	}
	// the GTID before the init: the capture from there is a superset of what follows its snapshot
	variable := "GTID_EXECUTED"
	if source.IsMariaDB() {
		variable = "GTID_BINLOG_POS"
	}
	gtid, q, err := dbhelper.GetVariableByName(source.Conn, variable, source.DBVersion)
	cluster.LogSQL(q, err, source.URL, "CDC", config.LvlErr, "Rejoin of feed %s: %s of %s: %s", l.Name(), variable, source.URL, err)
	if err != nil || gtid == "" {
		return fmt.Errorf("feed %s: the current position of %s is unknown (%v)", l.Name(), source.URL, err)
	}
	// the applied indexes, the checkpoints and the staging go: the init runs again at the start
	for _, p := range []string{wire.AppliedPath(l.path), wire.MergedPath(l.path)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := os.RemoveAll(wire.CheckpointDir(l.path)); err != nil {
		return err
	}
	if target := l.server; target.Conn != nil {
		if q, err := dbhelper.DropDatabaseIfExists(target.Conn, target.DBVersion, replStagingSchema); err != nil {
			cluster.LogSQL(q, err, target.URL, "CDC", config.LvlErr, "Rejoin of feed %s: staging not dropped on %s: %s", l.Name(), target.URL, err)
			return err
		}
	}
	if err := wire.WriteState(wire.StatePath(l.path), wire.State{GTID: gtid, RestoreDone: st.RestoreDone, Tables: st.Tables, Seq: st.Seq}); err != nil {
		return err
	}
	cluster.SetState("CINF0032", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0032"], l.Name(), cluster.Name, gtid, source.URL), ErrFrom: "REPL", ServerUrl: l.server.URL})
	_, err = l.Start(LinkByRejoin)
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

// waitReplPluginStopped waits for the feed's process to exit (its TOML disabled), bounded.
func (cluster *Cluster) waitReplPluginStopped(path string, max time.Duration) error {
	cluster.replPluginsMu.Lock()
	r := cluster.replPlugins[path]
	var done chan struct{}
	if r != nil {
		done = r.done
		cluster.stopReplPluginLocked(path, r)
	}
	cluster.replPluginsMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-time.After(max):
		return fmt.Errorf("replication plugin %s did not stop within %s", filepath.Base(path), max)
	}
}
