// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// Replication is one replication of a server: an edge of the cluster's topology, from a source
// (a server of a cluster, or an external address) into the server whose server.Replications
// holds it, given by a plugin (its manifest). Failover, switchover, rejoin, reseed, the monitor,
// the GUI, the API and MCP drive replications through this interface only (law T7). A
// replication belongs to the client's topology (law T27): repman reads it, starts and stops it,
// and moves it to another source only from the failover, switchover and rejoin code, under their
// guards. Design: doc/implementation/cluster/REPLICATION_INTERFACE.md.
//
// The objects are built where the monitor reads the links, ServerMonitor.Refresh
// (refreshNativeLinks for the native channels, cdcFeedsOf for the CDC feeds), once per tick: the
// row is the one read then. Status reads it again now; Row is the row of that read, no SQL, what
// the code that only reads a field uses.
//
// Every verb that changes a link (Stop, StopCopyEvents, StopApplyEvents, ResetPositions,
// ChangeSource) takes the reason and records the change on the server once its statement
// succeeded: WARN0352 names the channel, the action and the reason until the link runs again
// (checkLinkChanges). A Start that succeeds drops the record. An action the plugin's manifest
// does not list answers ErrReplicationNotSupported, never nil.
//
// Every verb returns the statements it ran, so the caller logs them and the operator can
// replay them (law F11): Status included, which returns them beside the row.
type Replication interface {
	Plugin() ReplicationManifest
	SourceCluster() *Cluster      // nil for an external source repman does not manage
	SourceServer() Endpoint       // a server of a cluster, or an external address
	TargetCluster() *Cluster      // the cluster of TargetServer
	TargetServer() *ServerMonitor // the server whose server.Replications holds it
	Name() string                 // its identity on the target: the channel, the subscription, the feed
	// Row is the replication's row as the monitor last read it (no statement).
	Row() dbhelper.SlaveStatus

	// Status reads the replication's row now (native: SHOW SLAVE '<channel>' STATUS) and the
	// statement it ran.
	Status() (dbhelper.SlaveStatus, string, error)
	// WaitCaughtUp waits until the target applied until (a GTID, else a binary log file and
	// position), timeout at most: ErrReplicationNotCaughtUp when the timeout came first.
	WaitCaughtUp(until Position, timeout time.Duration) (string, error)

	// Start starts the copy and the apply of the events.
	Start(reason string) (string, error)
	// Stop stops the copy and the apply; the link stays defined.
	Stop(reason string) (string, error)
	// StartCopyEvents starts the copy of the source's events only (native: the IO thread).
	StartCopyEvents(reason string) (string, error)
	// StopCopyEvents stops the copy of the source's events only (native: the IO thread).
	StopCopyEvents(reason string) (string, error)
	// StartApplyEvents starts the apply of the events copied only (native: the SQL thread).
	StartApplyEvents(reason string) (string, error)
	// StopApplyEvents stops the apply of the events copied only (native: the SQL thread).
	StopApplyEvents(reason string) (string, error)

	// ResetPositions clears the positions and keeps the link (RESET SLAVE without ALL, T27).
	ResetPositions(reason string) (string, error)
	// SetPosition sets where the target's apply resumes (native: gtid_slave_pos on MariaDB, the
	// link's; GTID_PURGED on MySQL, the server's GTID state, only right after a restore).
	SetPosition(at Position, reason string) (string, error)
	// SkipEvent skips the next event of the apply (sql_slave_skip_counter); the caller stops
	// and starts the link around it.
	SkipEvent(reason string) (string, error)
	// ChangeSource points the replication at to (CHANGE MASTER): the failover, switchover and
	// rejoin code only, under their guards. opt carries the rest of the definition; opt.Channel
	// names the native channel, as dbhelper.ChangeMaster reads it.
	ChangeSource(to Endpoint, opt ChangeSourceOpt, reason string) (string, error)
	// Promote ends this edge so its target can become a source (native: RESET SLAVE ALL on the
	// channel; PostgreSQL pg_promote()). read_only is the server's: the caller sets it.
	Promote(reason string) (string, error)
	// Remove deletes the replication's definition (native: RESET SLAVE ALL): the promotion of a
	// replica, and a request of the operator only (T27).
	Remove(reason string) (string, error)
	// Rejoin brings the server back as a replica of the cluster (native: the rejoin family,
	// srv_rejoin.go), or the feed back to its target (cdc: resume, or rebuild).
	Rejoin() error

	// Configure applies the options its manifest declares (native: the force-slave-* settings).
	Configure(opts LinkOptions, reason string) (string, error)
	// RotateCredential sets the cluster's current replication credential on the link.
	RotateCredential(reason string) (string, error)
}

// ReplicationPlugin is a type of replication: the native replication of an engine, or a CDC
// feed of a replication plugin (cluster/replplugin). It keeps what an object can not do: describe
// itself, create an edge, set up a topology; every other verb is the Replication's.
type ReplicationPlugin interface {
	Manifest() ReplicationManifest
	// Create adds one replication (the bootstrap, an operator), its checks first (step 5 of the
	// design: today the bootstrap in prov.go).
	Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error)
	// Bootstrap sets up the cluster's replication in one topology of the manifest (step 5 of
	// the design: today BootstrapReplication in prov.go).
	Bootstrap(c *Cluster, topology string, master *ServerMonitor) error
}

// Endpoint is one end of a replication: a server repman monitors, else an external address
// (the account then comes from the credential chain).
type Endpoint struct {
	Server *ServerMonitor // set when repman manages the server
	Host   string         // otherwise its address
	Port   string
}

// URL is the end's host:port, "" when unknown.
func (e Endpoint) URL() string {
	if e.Server != nil {
		return e.Server.URL
	}
	if e.Host == "" {
		return ""
	}
	return e.Host + ":" + e.Port
}

// Position is the engine's position a replication reaches or resumes from: a GTID set, else a
// binary log file and position (ReplicationTarget, the type the waits already used).
type Position = ReplicationTarget

// ChangeSourceOpt is the rest of a replication's definition when its source changes: the
// account, the mode, TLS, the delay (dbhelper's CHANGE MASTER options).
type ChangeSourceOpt = dbhelper.ChangeMasterOpt

// SourceOf is the source opt names, for the callers that build the options first.
func SourceOf(opt ChangeSourceOpt) Endpoint {
	return Endpoint{Host: opt.Host, Port: opt.Port}
}

// LinkOptions are the properties Configure sets on a replication, each when non-zero, in this
// order. Native: the statements the force-slave-* settings send (CheckSlaveSettings,
// checkReplicationHeartbeatPeriod), unchanged.
type LinkOptions struct {
	GTIDMode        string // master_use_gtid of the channel (slave_pos)
	GTIDStrict      bool   // gtid_strict_mode on (MariaDB)
	ExecMode        string // slave_exec_mode (IDEMPOTENT, STRICT)
	ParallelMode    string // slave_parallel_mode (OPTIMISTIC, CONSERVATIVE, ...); stops and starts the apply
	HeartbeatPeriod string // master_heartbeat_period of the channel, in seconds
}

func (o LinkOptions) empty() bool {
	return o.GTIDMode == "" && !o.GTIDStrict && o.ExecMode == "" && o.ParallelMode == "" && o.HeartbeatPeriod == ""
}

// ReplicationManifest declares a plugin: its name (repl-<source>-<target>-<type>), the engines it
// reads and writes, the topologies it sets up and the actions it implements.
type ReplicationManifest struct {
	Name       string                `json:"name"`           // repl-<source>-<target>-<type>
	Type       string                `json:"type"`           // ReplicationKind*: native | cdc | trigger
	Tier       string                `json:"tier,omitempty"` // config.ConstArchiveTierEnterprise, "" for every plan
	Source     []string              `json:"source"`         // the engines it reads (utils/version flavors)
	Target     []string              `json:"target"`         // the engines it writes
	Topologies []ReplicationTopology `json:"topologies"`
	Actions    []string              `json:"actions"` // the ReplicationAction* it implements
	// Settings: its settings and secrets, as the snapshot and archive plugins (step 4)
	Settings config.PluginSettings `json:"settings,omitempty"`
}

// ReplicationTopology is one topology a plugin sets up.
type ReplicationTopology struct {
	Name        string `json:"name"`        // config.Topo*: master-slave, multi-master-wsrep, ...
	Transport   string `json:"transport"`   // physical | logical
	Consistency string `json:"consistency"` // async | semi-sync | group
}

// Can tells whether the plugin implements the action, as SnapshotPluginManifest.Can.
func (m ReplicationManifest) Can(action string) bool {
	for _, a := range m.Actions {
		if a == action {
			return true
		}
	}
	return false
}

// The actions of a manifest: the verbs of Replication and ReplicationPlugin, under the names the
// external plugins' contract gives them (the first argument of the binary, design "External").
const (
	ReplicationActionBootstrap        = "bootstrap"
	ReplicationActionCreate           = "create"
	ReplicationActionRemove           = "remove"
	ReplicationActionStatus           = "status"
	ReplicationActionWaitCaughtUp     = "wait-caught-up"
	ReplicationActionStart            = "start"
	ReplicationActionStop             = "stop"
	ReplicationActionStartCopyEvents  = "start-copy"
	ReplicationActionStopCopyEvents   = "stop-copy"
	ReplicationActionStartApplyEvents = "start-apply"
	ReplicationActionStopApplyEvents  = "stop-apply"
	ReplicationActionResetPositions   = "reset-positions"
	ReplicationActionSetPosition      = "set-position"
	ReplicationActionChangeSource     = "change-source"
	ReplicationActionPromote          = "promote"
	ReplicationActionSkipEvent        = "skip-event"
	ReplicationActionRejoin           = "rejoin"
	ReplicationActionConfigure        = "configure"
	ReplicationActionRotateCredential = "rotate-credential"
)

// The transports and consistencies of a topology.
const (
	ReplicationTransportPhysical = "physical"
	ReplicationTransportLogical  = "logical"

	ReplicationConsistencyAsync    = "async"
	ReplicationConsistencySemiSync = "semi-sync"
	ReplicationConsistencyGroup    = "group"
)

// ReplicationKind is the type of a plugin (ReplicationManifest.Type), carried in each status row
// (SlaveStatus.Kind).
type ReplicationKind string

const (
	ReplicationKindNative  ReplicationKind = "native"
	ReplicationKindCDC     ReplicationKind = "cdc"
	ReplicationKindTrigger ReplicationKind = "trigger"
)

// KindOf is the replication's plugin type.
func KindOf(r Replication) ReplicationKind { return ReplicationKind(r.Plugin().Type) }

// CanFailover: the failover and the switchover only act on the replications whose plugin can
// promote and change the source (never a CDC feed: a child never replaces its parent).
func CanFailover(r Replication) bool {
	m := r.Plugin()
	return m.Can(ReplicationActionPromote) && m.Can(ReplicationActionChangeSource)
}

// ErrReplicationNotSupported: the plugin has no such action (a skip of an event on a CDC feed,
// a repoint of a feed whose source follows replication-plugin-source, a bootstrap not moved
// into the plugins yet). Wrapped with the action and the replication: errors.Is tests it.
var ErrReplicationNotSupported = errors.New("operation not supported by this replication link")

// ErrReplicationPositionNotEmpty: SetPosition on MySQL refused, the server already has a GTID
// state (gtid_executed not empty): GTID_PURGED is set only right after a restore.
var ErrReplicationPositionNotEmpty = dbhelper.ErrGTIDStateNotEmpty

// ErrReplicationNotCaughtUp: WaitCaughtUp's timeout came before the position.
var ErrReplicationNotCaughtUp = errors.New("replication did not reach the position before the timeout")

// ReplicationList is a server's replications (server.Replications): the native channels first,
// then the CDC feeds writing into it. It marshals each entry as its row plus its plugin and its
// ends (replicationJSON) and decodes them back (the API clients, the double marshal of the gRPC
// API) as objects detached from any server: their verbs answer an error.
type ReplicationList []Replication

// OfKind are the replications of one plugin type, in order.
func (l ReplicationList) OfKind(kind ReplicationKind) ReplicationList {
	var out ReplicationList
	for _, r := range l {
		if KindOf(r) == kind {
			out = append(out, r)
		}
	}
	return out
}

// Rows are the replications' rows (Row), in order.
func (l ReplicationList) Rows() []dbhelper.SlaveStatus {
	if l == nil {
		return nil
	}
	out := make([]dbhelper.SlaveStatus, 0, len(l))
	for _, r := range l {
		out = append(out, r.Row())
	}
	return out
}

// Named is the replication of that name, nil when there is none.
func (l ReplicationList) Named(name string) Replication {
	for _, r := range l {
		if r.Name() == name {
			return r
		}
	}
	return nil
}

// UnmarshalJSON decodes the entries as detached objects of their plugin (by its name).
func (l *ReplicationList) UnmarshalJSON(b []byte) error {
	var raw []replicationJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if raw == nil {
		*l = nil
		return nil
	}
	out := make(ReplicationList, 0, len(raw))
	for _, j := range raw {
		base := replicationBase{
			name: j.ConnectionName.String,
			row:  j.SlaveStatus,
			ends: replicationEnds{SourceCluster: j.SourceCluster, SourceServer: j.SourceServer, TargetCluster: j.TargetCluster, TargetServer: j.TargetServer},
		}
		switch {
		case isNativePlugin(j.Plugin):
			base.manifest = nativePlugins[j.Plugin].Manifest()
			out = append(out, newNativeObject(base))
		case j.Plugin == ReplicationPluginCDC:
			base.manifest = cdcManifest
			out = append(out, &cdcReplication{replicationBase: base})
		default:
			base.manifest = ReplicationManifest{Name: j.Plugin, Type: j.Kind}
			out = append(out, &base)
		}
	}
	*l = out
	return nil
}

// replicationJSON is an entry of server.Replications in the JSON: every field of the row, with
// today's keys, then the plugin and the four ends (their cluster's name, their server's URL).
type replicationJSON struct {
	dbhelper.SlaveStatus
	Plugin        string `json:"plugin"`
	SourceCluster string `json:"sourceCluster"`
	SourceServer  string `json:"sourceServer"`
	TargetCluster string `json:"targetCluster"`
	TargetServer  string `json:"targetServer"`
}

// replicationEnds are the ends' names of an object decoded from JSON, which has no live server
// or cluster: kept so that a decode then an encode loses nothing.
type replicationEnds struct {
	SourceCluster, SourceServer, TargetCluster, TargetServer string
}

// replicationBase is what every Replication holds, and answers ErrReplicationNotSupported to
// every verb: each plugin's type embeds it and declares the verbs it implements, as
// unsupportedOrchestrator. A bare replicationBase is the object of an unknown plugin.
type replicationBase struct {
	manifest      ReplicationManifest
	target        *ServerMonitor
	source        Endpoint
	sourceCluster *Cluster
	name          string
	row           dbhelper.SlaveStatus
	ends          replicationEnds
}

// newReplicationBase builds the common part, the source resolved among the monitored servers
// (resolveEndpoint).
func newReplicationBase(m ReplicationManifest, target *ServerMonitor, name string, row dbhelper.SlaveStatus, sourceHost, sourcePort string) replicationBase {
	b := replicationBase{manifest: m, target: target, name: name, row: row}
	if target != nil {
		b.sourceCluster, b.source = target.ClusterGroup.resolveEndpoint(sourceHost, sourcePort)
	}
	return b
}

func (r *replicationBase) Plugin() ReplicationManifest   { return r.manifest }
func (r *replicationBase) SourceCluster() *Cluster       { return r.sourceCluster }
func (r *replicationBase) SourceServer() Endpoint        { return r.source }
func (r *replicationBase) TargetServer() *ServerMonitor  { return r.target }
func (r *replicationBase) Name() string                  { return r.name }
func (r *replicationBase) Row() dbhelper.SlaveStatus     { return r.row }
func (r *replicationBase) rowRef() *dbhelper.SlaveStatus { return &r.row }

func (r *replicationBase) TargetCluster() *Cluster {
	if r.target == nil {
		return nil
	}
	return r.target.ClusterGroup
}

// MarshalJSON: the row with today's keys, plus the plugin and the ends.
func (r *replicationBase) MarshalJSON() ([]byte, error) {
	j := replicationJSON{SlaveStatus: r.row, Plugin: r.manifest.Name}
	j.SourceCluster, j.SourceServer, j.TargetCluster, j.TargetServer = r.ends.SourceCluster, r.ends.SourceServer, r.ends.TargetCluster, r.ends.TargetServer
	if r.sourceCluster != nil {
		j.SourceCluster = r.sourceCluster.Name
	}
	if u := r.source.URL(); u != "" {
		j.SourceServer = u
	}
	if r.target != nil {
		j.TargetServer = r.target.URL
		if r.target.ClusterGroup != nil {
			j.TargetCluster = r.target.ClusterGroup.Name
		}
	}
	return json.Marshal(j)
}

// mutableRow is the stored row of a replication built here, for the monitor's own adjustments
// of the row it just read (the PostgreSQL server id); nil for another implementation.
func mutableRow(r Replication) *dbhelper.SlaveStatus {
	if m, ok := r.(interface{ rowRef() *dbhelper.SlaveStatus }); ok {
		return m.rowRef()
	}
	return nil
}

func (r *replicationBase) notSupported(action string) error {
	return fmt.Errorf("%w: %s on %s link %q", ErrReplicationNotSupported, action, r.manifest.Name, r.name)
}

// noTarget: an object decoded from JSON has no server to act on.
func (r *replicationBase) noTarget() error {
	return fmt.Errorf("replication %q of %s has no server to act on (a copy decoded from JSON)", r.name, r.manifest.Name)
}

func (r *replicationBase) Status() (dbhelper.SlaveStatus, string, error) {
	return dbhelper.SlaveStatus{}, "", r.notSupported(ReplicationActionStatus)
}
func (r *replicationBase) WaitCaughtUp(until Position, timeout time.Duration) (string, error) {
	return "", r.notSupported(ReplicationActionWaitCaughtUp)
}
func (r *replicationBase) Start(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStart)
}
func (r *replicationBase) Stop(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStop)
}
func (r *replicationBase) StartCopyEvents(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStartCopyEvents)
}
func (r *replicationBase) StopCopyEvents(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStopCopyEvents)
}
func (r *replicationBase) StartApplyEvents(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStartApplyEvents)
}
func (r *replicationBase) StopApplyEvents(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionStopApplyEvents)
}
func (r *replicationBase) ResetPositions(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionResetPositions)
}
func (r *replicationBase) SetPosition(at Position, reason string) (string, error) {
	return "", r.notSupported(ReplicationActionSetPosition)
}
func (r *replicationBase) SkipEvent(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionSkipEvent)
}
func (r *replicationBase) ChangeSource(to Endpoint, opt ChangeSourceOpt, reason string) (string, error) {
	return "", r.notSupported(ReplicationActionChangeSource)
}
func (r *replicationBase) Promote(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionPromote)
}
func (r *replicationBase) Remove(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionRemove)
}
func (r *replicationBase) Rejoin() error {
	return r.notSupported(ReplicationActionRejoin)
}
func (r *replicationBase) Configure(opts LinkOptions, reason string) (string, error) {
	return "", r.notSupported(ReplicationActionConfigure)
}
func (r *replicationBase) RotateCredential(reason string) (string, error) {
	return "", r.notSupported(ReplicationActionRotateCredential)
}

// resolveEndpoint finds the server host:port names among the monitored ones: this cluster's
// first, then the other clusters' (a parent, a multi-source head), as replPluginSourcePass;
// otherwise an external Endpoint. The cluster is nil for an external source.
func (cluster *Cluster) resolveEndpoint(host, port string) (*Cluster, Endpoint) {
	if host == "" {
		return nil, Endpoint{}
	}
	if c, s := cluster.serverOfAnyCluster(host + ":" + port); s != nil {
		return c, Endpoint{Server: s, Host: s.Host, Port: s.Port}
	}
	return nil, Endpoint{Host: host, Port: port}
}

// serverOfAnyCluster is the monitored server of that URL and its cluster: this cluster's first,
// then the other clusters' (clusterList, read without the cluster lock as every caller of the
// monitor tick does).
func (cluster *Cluster) serverOfAnyCluster(url string) (*Cluster, *ServerMonitor) {
	if cluster == nil {
		return nil, nil
	}
	if s := cluster.GetServerFromURL(url); s != nil {
		return cluster, s
	}
	for _, cl := range cluster.clusterList {
		if cl != nil && cl != cluster {
			if s := cl.GetServerFromURL(url); s != nil {
				return cl, s
			}
		}
	}
	return nil, nil
}

// The changes a link records (linkChange.op): the verb of WARN0352.
const (
	linkOpStop            = "stopped"
	linkOpStopCopyEvents  = "copy of events stopped"
	linkOpStopApplyEvents = "apply of events stopped"
	linkOpResetPositions  = "positions reset"
	linkOpChangeSource    = "repointed"
)

// The reasons callers give (the action that changed the link), as WARN0352 shows them.
const (
	LinkByFailover           = "failover"
	LinkBySwitchover         = "switchover"
	LinkByRejoin             = "rejoin"
	LinkByReseed             = "reseed"
	LinkByBackupVerification = "backup verification"
	LinkByOperator           = "operator request"
	LinkByBootstrap          = "replication bootstrap"
	LinkByServiceStop        = "database service stop"
	LinkBySQLErrorRestart    = "replication-restart-on-sqlerror-match"
	LinkByStaging            = "staging refresh"
	LinkByFreeze             = "write freeze"
	LinkByArbitration        = "arbitration"
	LinkByColumnStore        = "ColumnStore replica rebootstrap"
	LinkByMonitor            = "monitor enforcement of the force-slave-* settings"
	LinkByTest               = "regtest"
)

// failoverOrSwitchover is the reason a MasterFailover(fail) gives.
func failoverOrSwitchover(fail bool) string {
	if fail {
		return LinkByFailover
	}
	return LinkBySwitchover
}

// ReplicationLink is the server's managed native replication, the channel
// replication-source-name names (cluster.Conf.MasterConn): the one the failover, switchover,
// rejoin and reseed drive.
func (server *ServerMonitor) ReplicationLink() Replication {
	return server.ReplicationChannelLink(server.ClusterGroup.Conf.MasterConn)
}

// ReplicationChannelLink is the server's native replication of that channel: the object of the
// monitor's last read, else (a channel not defined yet, about to be by a ChangeSource) a new
// one with an empty row and no source.
func (server *ServerMonitor) ReplicationChannelLink(channel string) Replication {
	for _, r := range server.Replications {
		if r.Name() == channel && KindOf(r) == ReplicationKindNative {
			return r
		}
	}
	return server.newNativeReplication(channel, dbhelper.SlaveStatus{})
}

// NativeReplications are the server's native channels as last read (no CDC feed): what the
// code that acts on the engine's channels (multi-source, reseed, the stop of every channel)
// iterates.
func (server *ServerMonitor) NativeReplications() ReplicationList {
	return server.Replications.OfKind(ReplicationKindNative)
}

// ReplicationLinkStatus are the rows of every replication of the server, each with its kind:
// the native channels, then the CDC feeds. What the MCP reads.
func (server *ServerMonitor) ReplicationLinkStatus() []dbhelper.SlaveStatus {
	return server.Replications.Rows()
}
