// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The external replication plugins (REPLICATION_INTERFACE.md "External: one adapter for every
// plugin binary"): every CDC plugin binary, whatever its destination (DuckDB, ColumnStore,
// MariaDB, a PostgreSQL source into MariaDB, ...), is a ReplicationPlugin driven by the one
// adapter here, replicationPluginAdapter; its feeds are Replication objects (cdcReplication) of
// the server they write into, their target, in that server's server.Replications. Adding a CDC
// plugin is shipping a signed binary with its manifest, with no change here.
//
// Its surface is the plugin TOML (<cluster>/replication-plugins/<name>.toml), its State and
// applied files, the status line the supervisor keeps (superviseReplPlugin, the long-running
// `run`), and for the actions its manifest adds, the binary called with the action:
//
//   - status: the supervisor's last status line, capture = read (Slave_IO_Running), apply =
//     apply (Slave_SQL_Running), the lag, the GTID captured (Gtid_IO_Pos) and applied
//     (Gtid_Slave_Pos), the last error of each; the fields with no meaning for a feed stay empty;
//   - start, stop: the supervisor's, the TOML's enabled flag (the next tick starts or stops run);
//   - rejoin: resume from the State when the source still has its log from the position
//     applied, else a new initial load through ReinitReplicationPlugin (Rejoin);
//   - start-copy, stop-copy, start-apply, stop-apply: only when the binary's manifest lists them,
//     `<binary> <action> -config <TOML>`, a deadline (cdcActionTimeout), the action, the link and
//     the reason in REPMAN_REPL_* variables beside the plugin's environment, the records it ran on
//     stdout (returned as the statement, F11), the error as the last line of stderr;
//   - it never writes to the source: it reads its binary log list and positions only (F1).
//
// Every other verb (a reset of positions, a removal, a change of source, a skip, a promotion,
// a position, a wait, an option, a credential, and Bootstrap and Create until step 5) answers
// ErrReplicationNotSupported: the source follows replication-plugin-source
// (cluster_repl_source.go), the definition is the child's seed.

// The CDC plugins repman knows by name (repl-<source>-<target>-cdc). Their binaries are named
// after them; the binaries named before (replplugin-<short>) and the short names the settings
// and the TOMLs hold (replication-plugin-destination = "duckdb", plugin = "duckdb") stay valid,
// as aliases: no key rewritten (T16), no TOML renamed.
const (
	ReplicationPluginMariaDBDuckDBCDC      = "repl-mariadb-duckdb-cdc"
	ReplicationPluginMariaDBColumnStoreCDC = "repl-mariadb-columnstore-cdc"
	ReplicationPluginMariaDBMariaDBCDC     = "repl-mariadb-mariadb-cdc"
	ReplicationPluginPostgreSQLMariaDBCDC  = "repl-postgresql-mariadb-cdc"
)

// The topologies of a CDC plugin: a mode of the link (a copy of the tables, their
// denormalization, a migration from another engine), never in its name.
const (
	CDCTopologyCopy        = "copy"
	CDCTopologyDenormalize = "denormalize"
	CDCTopologyMigration   = "migration"
)

// cdcPluginDef is a known CDC plugin: its name, the short name of its first binary
// (replplugin-<short>) and its manifest when its binary ships none (or one without the
// replication fields).
type cdcPluginDef struct {
	name, short string
	manifest    ReplicationManifest
}

func cdcTopologies(names ...string) []ReplicationTopology {
	out := make([]ReplicationTopology, 0, len(names))
	for _, n := range names {
		out = append(out, ReplicationTopology{Name: n, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync})
	}
	return out
}

// cdcSupervisorActions: what every CDC plugin does through the supervisor, never promote nor
// change-source (the failover leaves a feed alone: a child never replaces its parent).
var cdcSupervisorActions = []string{ReplicationActionStatus, ReplicationActionStart, ReplicationActionStop, ReplicationActionRejoin}

// cdcBinaryActions: the actions a binary may add in its manifest, run as `<binary> <action>`.
var cdcBinaryActions = []string{ReplicationActionStartCopyEvents, ReplicationActionStopCopyEvents, ReplicationActionStartApplyEvents, ReplicationActionStopApplyEvents}

func cdcDef(name, short string, source, target []string, topologies ...string) cdcPluginDef {
	return cdcPluginDef{name: name, short: short, manifest: ReplicationManifest{
		Name: name, Type: string(ReplicationKindCDC), Source: source, Target: target,
		Topologies: cdcTopologies(topologies...), Actions: cdcSupervisorActions,
	}}
}

var cdcPlugins = []cdcPluginDef{
	cdcDef(ReplicationPluginMariaDBDuckDBCDC, "duckdb", []string{"MariaDB"}, []string{"DuckDB"}, CDCTopologyCopy, CDCTopologyDenormalize),
	cdcDef(ReplicationPluginMariaDBColumnStoreCDC, "columnstore", []string{"MariaDB"}, []string{"ColumnStore"}, CDCTopologyCopy, CDCTopologyDenormalize),
	cdcDef(ReplicationPluginMariaDBMariaDBCDC, "innodb", []string{"MariaDB"}, []string{"MariaDB"}, CDCTopologyCopy, CDCTopologyDenormalize),
	cdcDef(ReplicationPluginPostgreSQLMariaDBCDC, "postgresql", []string{"PostgreSQL"}, []string{"MariaDB"}, CDCTopologyMigration),
}

// cdcManifest is the manifest of a CDC plugin no name is known for (a binary of another name
// without a manifest of its own): every engine, the supervisor's actions.
var cdcManifest = ReplicationManifest{
	Name:       ReplicationPluginCDC,
	Type:       string(ReplicationKindCDC),
	Source:     []string{"MariaDB", "MySQL", "Percona", "PostgreSQL"},
	Target:     []string{"MariaDB", "ColumnStore", "DuckDB"},
	Topologies: cdcTopologies(CDCTopologyCopy),
	Actions:    cdcSupervisorActions,
}

// cdcPluginOf is the known plugin of a name: its name, its short name, or its first binary's
// name (replplugin-<short>).
func cdcPluginOf(name string) (cdcPluginDef, bool) {
	name = strings.TrimSpace(name)
	short := strings.TrimPrefix(name, wire.BinaryPrefix)
	for _, d := range cdcPlugins {
		if name == d.name || short == d.short {
			return d, true
		}
	}
	return cdcPluginDef{}, false
}

// CanonicalReplicationPluginName is the name repl-<source>-<target>-cdc of a CDC plugin given
// by any of its names; the name itself for a plugin repman knows no other name of.
func CanonicalReplicationPluginName(name string) string {
	if d, ok := cdcPluginOf(name); ok {
		return d.name
	}
	return strings.TrimSpace(name)
}

// isCDCPluginName: a known CDC plugin, by any name, or the generic one.
func isCDCPluginName(name string) bool {
	_, ok := cdcPluginOf(name)
	return ok || name == ReplicationPluginCDC
}

// replPluginNames are every name of a plugin, the canonical one first: the entries of its
// settings ([[repl-plugin]]) are found under any of them.
func replPluginNames(plugin string) []string {
	plugin = strings.TrimSpace(plugin)
	d, ok := cdcPluginOf(plugin)
	if !ok {
		return []string{plugin}
	}
	names := []string{d.name, d.short, wire.BinaryPrefix + d.short}
	if plugin != d.name && plugin != d.short && plugin != wire.BinaryPrefix+d.short {
		names = append(names, plugin)
	}
	return names
}

// replPluginBinaryNames are the file names a plugin's binary may have, in order: the binary
// named after the plugin (repl-<source>-<target>-cdc), then the binary named before
// (replplugin-<short>), so a delivery of either runs. A name repman does not know: replplugin-<name>,
// or the name itself when it is a repl-* name.
func replPluginBinaryNames(plugin string) []string {
	if d, ok := cdcPluginOf(plugin); ok {
		return []string{d.name, wire.BinaryPrefix + d.short}
	}
	if strings.HasPrefix(plugin, "repl-") {
		return []string{plugin}
	}
	return []string{wire.BinaryPrefix + strings.TrimPrefix(plugin, wire.BinaryPrefix)}
}

// defaultCDCManifest is the manifest repman knows for a plugin name: the known plugin's, else
// the generic one (under that name when it is a repl-* one, a plugin of a newer repman).
func defaultCDCManifest(name string) ReplicationManifest {
	if d, ok := cdcPluginOf(name); ok {
		return cloneManifest(d.manifest)
	}
	m := cloneManifest(cdcManifest)
	if strings.HasPrefix(name, "repl-") {
		m.Name = name
	}
	return m
}

func cloneManifest(m ReplicationManifest) ReplicationManifest {
	m.Source = append([]string(nil), m.Source...)
	m.Target = append([]string(nil), m.Target...)
	m.Topologies = append([]ReplicationTopology(nil), m.Topologies...)
	m.Actions = append([]string(nil), m.Actions...)
	m.Settings = append(config.PluginSettings(nil), m.Settings...)
	return m
}

// cdcSettingName: the names a binary's manifest may give its settings.
var cdcSettingName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// cdcManifestOf is the manifest of a CDC plugin: repman's (defaultCDCManifest), completed by
// what its binary's manifest declares (<binary>.manifest.json): its engines, its topologies,
// its settings, its tier, the actions it adds (cdcBinaryActions only: a CDC plugin is never
// promoted nor repointed, its start and stop are the supervisor's). A known plugin keeps its
// name whatever its binary's manifest says (the binaries named before say "duckdb"). A nil
// cluster, or a binary not installed: repman's manifest.
func (cluster *Cluster) cdcManifestOf(plugin string) ReplicationManifest {
	m := defaultCDCManifest(plugin)
	if cluster == nil {
		return m
	}
	wm, bm, err := cluster.replPluginManifests(plugin)
	if err != nil {
		return m
	}
	if _, known := cdcPluginOf(plugin); !known && strings.HasPrefix(bm.Name, "repl-") {
		m.Name = bm.Name
	}
	if len(bm.Source) > 0 {
		m.Source = bm.Source
	}
	if len(bm.Target) > 0 {
		m.Target = bm.Target
	}
	if len(bm.Topologies) > 0 {
		m.Topologies = bm.Topologies
	}
	for _, st := range bm.Settings {
		// a name the environment carries as REPMAN_REPL_SETTING_<NAME>, nothing else
		if cdcSettingName.MatchString(st.Name) {
			m.Settings = append(m.Settings, st)
		}
	}
	m.Tier = wm.Tier // the tier the supervisor enforces (replPluginAllowed)
	for _, a := range bm.Actions {
		for _, b := range cdcBinaryActions {
			if a == b && !m.Can(a) {
				m.Actions = append(m.Actions, a)
			}
		}
	}
	return m
}

// replicationPluginAdapter is every external CDC plugin as a ReplicationPlugin: its manifest
// (cdcManifestOf); its feeds are built by Refresh (cdcFeedsOf), its Create and Bootstrap are
// step 5 of the design (today the seed of the child).
type replicationPluginAdapter struct {
	unsupportedReplicationPlugin
	cluster *Cluster // nil: repman's manifest only
	name    string
}

func (p *replicationPluginAdapter) Manifest() ReplicationManifest {
	return p.cluster.cdcManifestOf(p.name)
}

// Bootstrap: step 5 of the design moves the seed of the child here.
func (p *replicationPluginAdapter) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, p.name)
}

// Create: step 5, with the bootstrap.
func (p *replicationPluginAdapter) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionCreate, name, p.name)
}

// cdcLinkPrefix names a feed among the links of a server: "cdc:<TOML name>".
const cdcLinkPrefix = "cdc:"

// cdcFeed is a feed's configuration: what its TOML says.
type cdcFeed struct {
	cluster *Cluster // the cluster whose supervisor runs the feed (the child)
	path    string   // its TOML
	cfg     wire.Config
}

// cdcReplication is one feed into its target server: the row of the supervisor's status line
// when it was built, its source resolved from the TOML's source host and port.
type cdcReplication struct {
	replicationBase
	feed *cdcFeed // nil for a copy decoded from JSON
}

// cdcFeedReplication is the replication of one feed into server (nil when the target is not a
// server of the cluster: the object reads the feed, it acts on nothing).
func (cluster *Cluster) cdcFeedReplication(server *ServerMonitor, path string, cfg wire.Config) *cdcReplication {
	name := cdcLinkPrefix + strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	f := &cdcFeed{cluster: cluster, path: path, cfg: cfg}
	r := &cdcReplication{feed: f}
	r.replicationBase = newReplicationBase(cluster.cdcManifestOf(cfg.Plugin), server, name, cdcRow(name, f), cfg.Source.Host, fmt.Sprint(cfg.Source.Port))
	return r
}

// cdcFeedConfigs are the cluster's feed TOMLs that parse, by path, sorted.
func (cluster *Cluster) cdcFeedConfigs() ([]string, map[string]wire.Config) {
	cfgs, _, _ := cluster.readReplPluginConfigs()
	paths := make([]string, 0, len(cfgs))
	for p := range cfgs {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths, cfgs
}

// cdcFeedsOf are the feeds of the cluster whose target is server, by TOML path: what Refresh
// adds to server.Replications after the native channels.
func (cluster *Cluster) cdcFeedsOf(server *ServerMonitor) ReplicationList {
	if cluster == nil || server == nil {
		return nil
	}
	paths, cfgs := cluster.cdcFeedConfigs()
	var out ReplicationList
	for _, p := range paths {
		c := cfgs[p]
		if c.Target.Host == server.Host && c.Target.Port == atoiPort(server.Port) {
			out = append(out, cluster.cdcFeedReplication(server, p, c))
		}
	}
	return out
}

// CDCReplications are every feed of the cluster as its Replication object, by TOML path, each
// with its target server (nil when the target is not one of the cluster's servers): what the
// analytic child's status, the API and MCP read, the objects Refresh puts in the targets'
// server.Replications.
func (cluster *Cluster) CDCReplications() ReplicationList {
	if cluster == nil {
		return nil
	}
	paths, cfgs := cluster.cdcFeedConfigs()
	out := make(ReplicationList, 0, len(paths))
	for _, p := range paths {
		c := cfgs[p]
		out = append(out, cluster.cdcFeedReplication(cluster.GetServerFromURL(fmt.Sprintf("%s:%d", c.Target.Host, c.Target.Port)), p, c))
	}
	return out
}

// cdcReplicationAt is the feed of the TOML at path as its object; nil when the TOML does not
// exist or does not parse.
func (cluster *Cluster) cdcReplicationAt(path string) *cdcReplication {
	var c wire.Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil
	}
	return cluster.cdcFeedReplication(cluster.GetServerFromURL(fmt.Sprintf("%s:%d", c.Target.Host, c.Target.Port)), path, c)
}

// CDCFeedState is a CDC replication as its supervisor holds it now, beyond its row: its
// TOML, whether its process runs, its last stop, its last status line (a copy).
type CDCFeedState struct {
	Path   string
	Config wire.Config
	// Held: a runner of the supervisor holds the feed; Running: its process runs
	Held, Running bool
	// SourceHost: the source the running process reads, "" when none runs
	SourceHost    string
	StoppedReason string
	RestartAt     *time.Time
	Status        *wire.Status
}

// CDCReplication is a Replication of a CDC plugin: its feed's state as the supervisor holds it.
type CDCReplication interface {
	Replication
	Feed() (CDCFeedState, error)
}

// Feed is the feed's state now: the supervisor's runner, never a database.
func (r *cdcReplication) Feed() (CDCFeedState, error) {
	if r.feed == nil {
		return CDCFeedState{}, r.noTarget()
	}
	f := r.feed
	s := CDCFeedState{Path: f.path, Config: f.cfg}
	run := f.cluster.replPluginRunnerOf(f.path)
	if run == nil {
		return s, nil
	}
	s.Held, s.SourceHost = true, run.cfg.Source.Host
	if stop := run.stopped.Load(); stop != nil {
		at := stop.Restart
		s.StoppedReason, s.RestartAt = stop.Reason, &at
	} else {
		s.Running = true
	}
	if st := run.status.Load(); st != nil {
		c := *st
		s.Status = &c
	}
	return s, nil
}

// ReplicationPluginIndexesReady are the indexes whose init is done, of every feed of the
// cluster, read through their objects (the API and MCP).
func (cluster *Cluster) ReplicationPluginIndexesReady() []string {
	ready := []string{}
	for _, r := range cluster.CDCReplications() {
		if c, ok := r.(CDCReplication); ok {
			if s, err := c.Feed(); err == nil && s.Status != nil {
				ready = append(ready, s.Status.IndexesReady...)
			}
		}
	}
	return ready
}

// cdcActionTimeout: the deadline of a binary's action (start-copy, ...): the action changes
// the feed's state, never copies data.
const cdcActionTimeout = 2 * time.Minute

// cdcActionOutputMax bounds what an action's stdout keeps (F4).
const cdcActionOutputMax = 64 * 1024

// runAction runs an action the binary's manifest lists: `<binary> <action> -config <TOML>`,
// its records on stdout returned (F11), the last line of stderr as the error.
func (r *cdcReplication) runAction(action, reason string) (string, error) {
	if !r.manifest.Can(action) {
		return "", r.notSupported(action)
	}
	f, err := r.feedOf()
	if err != nil {
		return "", err
	}
	bin, err := f.cluster.verifiedReplPlugin(f.cfg.Plugin)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), cdcActionTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, action, "-config", f.path)
	cmd.Env = append(f.cluster.replPluginEnv(f.cfg),
		wire.EnvReplAction+"="+action,
		wire.EnvReplName+"="+r.name,
		wire.EnvReplReason+"="+strings.NewReplacer("\n", " ", "\r", " ", "\x00", "").Replace(reason))
	stdout, stderr := capBuffer{max: cdcActionOutputMax}, capBuffer{}
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	out := strings.TrimSpace(stdout.String())
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return out, fmt.Errorf("%s %s on %s: no answer after %s", filepath.Base(bin), action, r.name, cdcActionTimeout)
	}
	if runErr != nil {
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return out, fmt.Errorf("%s %s on %s: %s", filepath.Base(bin), action, r.name, lastLine(s))
		}
		return out, fmt.Errorf("%s %s on %s: %w", filepath.Base(bin), action, r.name, runErr)
	}
	if out == "" {
		out = filepath.Base(bin) + " " + action + " -config " + f.path
	}
	return out, nil
}

// StartCopyEvents starts the capture only, when the binary lists start-copy.
func (r *cdcReplication) StartCopyEvents(reason string) (string, error) {
	return r.runAction(ReplicationActionStartCopyEvents, reason)
}

// StopCopyEvents stops the capture only, when the binary lists stop-copy; recorded (WARN0352).
func (r *cdcReplication) StopCopyEvents(reason string) (string, error) {
	q, err := r.runAction(ReplicationActionStopCopyEvents, reason)
	if err == nil {
		r.target.recordLinkChange(r.name, linkOpStopCopyEvents, reason)
	}
	return q, err
}

// StartApplyEvents starts the apply only, when the binary lists start-apply.
func (r *cdcReplication) StartApplyEvents(reason string) (string, error) {
	return r.runAction(ReplicationActionStartApplyEvents, reason)
}

// StopApplyEvents stops the apply only, when the binary lists stop-apply; recorded (WARN0352).
func (r *cdcReplication) StopApplyEvents(reason string) (string, error) {
	q, err := r.runAction(ReplicationActionStopApplyEvents, reason)
	if err == nil {
		r.target.recordLinkChange(r.name, linkOpStopApplyEvents, reason)
	}
	return q, err
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

// replPluginRunnerOf is the supervisor's runner of the feed at path, nil when none holds it.
func (cluster *Cluster) replPluginRunnerOf(path string) *replPluginRunner {
	cluster.replPluginsMu.Lock()
	defer cluster.replPluginsMu.Unlock()
	return cluster.replPlugins[path]
}

// replPluginRuntime is the feed's last status line, its stop and whether a runner holds it.
func (cluster *Cluster) replPluginRuntime(path string) (*wire.Status, *replPluginStop, bool) {
	r := cluster.replPluginRunnerOf(path)
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
