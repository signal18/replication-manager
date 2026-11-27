// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// ReplicationPlugin is a type of replication link: the native replication of an engine, or a
// CDC feed of a replication plugin (cluster/replplugin). Every link of a cluster is a Link, an
// instance of one plugin; failover, switchover, rejoin, reseed, the monitor, the GUI, the API and
// MCP drive links through this interface only (law T7). A link belongs to the client's topology
// (law T27): repman reads it, starts and stops it, and moves it to another source only from the
// failover, switchover and rejoin code, under their guards.
// Design: doc/implementation/cluster/REPLICATION_INTERFACE.md.
//
// Every method that changes a link (Stop, StopCopyEvents, StopApplyEvents, ResetPositions,
// ChangeSource) takes the reason and records the change on the server: WARN0352 names the
// channel, the action and the reason until the link runs again (checkLinkChanges). A Start that
// succeeds drops the record. An action the plugin's manifest does not list answers
// ErrReplicationNotSupported, never nil (Link checks the manifest before the call).
//
// Every verb returns the statements it ran, so the caller logs them and the operator can
// replay them (law F11): Status included, which returns them beside the row.
type ReplicationPlugin interface {
	Manifest() ReplicationManifest

	// Bootstrap sets up the cluster's replication in one topology of the manifest (step 5 of
	// the design: today BootstrapReplication in prov.go).
	Bootstrap(c *Cluster, topology string, master *ServerMonitor) error
	// Create adds one link (the bootstrap, an operator).
	Create(l Link, reason string) (string, error)
	// Remove deletes the link's definition (native: RESET SLAVE ALL): the failover and
	// switchover's promotion of a replica, and a request of the operator only (T27).
	Remove(l Link, reason string) (string, error)

	// Status reads the link's row now (native: SHOW SLAVE '<channel>' STATUS), its Kind the
	// plugin's type, and the statement it ran.
	Status(l Link) (dbhelper.SlaveStatus, string, error)

	// Start starts the copy and the apply of the events.
	Start(l Link, reason string) (string, error)
	// Stop stops the copy and the apply; the link stays defined.
	Stop(l Link, reason string) (string, error)
	// StartCopyEvents starts the copy of the source's events only (native: the IO thread).
	StartCopyEvents(l Link, reason string) (string, error)
	// StopCopyEvents stops the copy of the source's events only (native: the IO thread).
	StopCopyEvents(l Link, reason string) (string, error)
	// StartApplyEvents starts the apply of the events copied only (native: the SQL thread).
	StartApplyEvents(l Link, reason string) (string, error)
	// StopApplyEvents stops the apply of the events copied only (native: the SQL thread).
	StopApplyEvents(l Link, reason string) (string, error)

	// ResetPositions clears the positions and keeps the link (RESET SLAVE without ALL, T27).
	ResetPositions(l Link, reason string) (string, error)
	// ChangeSource points the link at opt's source (CHANGE MASTER): the failover, switchover
	// and rejoin code only, under their guards. opt.Channel names the native channel, as
	// dbhelper.ChangeMaster reads it.
	ChangeSource(l Link, opt dbhelper.ChangeMasterOpt, reason string) (string, error)
	// SkipEvent skips the next event of the apply (sql_slave_skip_counter); the caller stops
	// and starts the link around it.
	SkipEvent(l Link, reason string) (string, error)
	// Rejoin brings the server back as a replica of the cluster (native: the rejoin family,
	// srv_rejoin.go), or the feed back to its target (cdc: resume, or rebuild).
	Rejoin(l Link) error
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

// The actions of a manifest: the verbs of ReplicationPlugin, under the names the external
// plugins' contract gives them (the first argument of the binary, design "External").
const (
	ReplicationActionBootstrap        = "bootstrap"
	ReplicationActionCreate           = "create"
	ReplicationActionRemove           = "remove"
	ReplicationActionStatus           = "status"
	ReplicationActionStart            = "start"
	ReplicationActionStop             = "stop"
	ReplicationActionStartCopyEvents  = "start-copy"
	ReplicationActionStopCopyEvents   = "stop-copy"
	ReplicationActionStartApplyEvents = "start-apply"
	ReplicationActionStopApplyEvents  = "stop-apply"
	ReplicationActionResetPositions   = "reset-positions"
	ReplicationActionChangeSource     = "change-source"
	ReplicationActionSkipEvent        = "skip-event"
	ReplicationActionRejoin           = "rejoin"
)

// The transports and consistencies of a topology.
const (
	ReplicationTransportPhysical = "physical"
	ReplicationTransportLogical  = "logical"

	ReplicationConsistencyAsync    = "async"
	ReplicationConsistencySemiSync = "semi-sync"
	ReplicationConsistencyGroup    = "group"
)

// Link is one replication link of a server: an instance of a plugin, the server it writes into,
// its name (the native channel, "" the default one; "cdc:<name>" a feed) and the plugin's own
// configuration of it (nil for a native channel, *cdcFeed for a feed). A value: the methods
// below call the plugin with it, after checking that its manifest lists the action.
type Link struct {
	Plugin ReplicationPlugin
	Server *ServerMonitor
	Name   string
	Config any
}

// Kind is the plugin's type (ReplicationKindNative, ReplicationKindCDC), what each status row
// carries (SlaveStatus.Kind).
func (l Link) Kind() ReplicationKind { return ReplicationKind(l.Plugin.Manifest().Type) }

// can answers ErrReplicationNotSupported for an action the manifest does not list.
func (l Link) can(action string) error {
	if l.Plugin.Manifest().Can(action) {
		return nil
	}
	return replicationNotSupported(action, l)
}

func (l Link) Create(reason string) (string, error) {
	if err := l.can(ReplicationActionCreate); err != nil {
		return "", err
	}
	return l.Plugin.Create(l, reason)
}

func (l Link) Remove(reason string) (string, error) {
	if err := l.can(ReplicationActionRemove); err != nil {
		return "", err
	}
	return l.Plugin.Remove(l, reason)
}

func (l Link) Status() (dbhelper.SlaveStatus, string, error) {
	if err := l.can(ReplicationActionStatus); err != nil {
		return dbhelper.SlaveStatus{}, "", err
	}
	return l.Plugin.Status(l)
}

func (l Link) Start(reason string) (string, error) {
	if err := l.can(ReplicationActionStart); err != nil {
		return "", err
	}
	return l.Plugin.Start(l, reason)
}

func (l Link) Stop(reason string) (string, error) {
	if err := l.can(ReplicationActionStop); err != nil {
		return "", err
	}
	return l.Plugin.Stop(l, reason)
}

func (l Link) StartCopyEvents(reason string) (string, error) {
	if err := l.can(ReplicationActionStartCopyEvents); err != nil {
		return "", err
	}
	return l.Plugin.StartCopyEvents(l, reason)
}

func (l Link) StopCopyEvents(reason string) (string, error) {
	if err := l.can(ReplicationActionStopCopyEvents); err != nil {
		return "", err
	}
	return l.Plugin.StopCopyEvents(l, reason)
}

func (l Link) StartApplyEvents(reason string) (string, error) {
	if err := l.can(ReplicationActionStartApplyEvents); err != nil {
		return "", err
	}
	return l.Plugin.StartApplyEvents(l, reason)
}

func (l Link) StopApplyEvents(reason string) (string, error) {
	if err := l.can(ReplicationActionStopApplyEvents); err != nil {
		return "", err
	}
	return l.Plugin.StopApplyEvents(l, reason)
}

func (l Link) ResetPositions(reason string) (string, error) {
	if err := l.can(ReplicationActionResetPositions); err != nil {
		return "", err
	}
	return l.Plugin.ResetPositions(l, reason)
}

func (l Link) ChangeSource(opt dbhelper.ChangeMasterOpt, reason string) (string, error) {
	if err := l.can(ReplicationActionChangeSource); err != nil {
		return "", err
	}
	return l.Plugin.ChangeSource(l, opt, reason)
}

func (l Link) SkipEvent(reason string) (string, error) {
	if err := l.can(ReplicationActionSkipEvent); err != nil {
		return "", err
	}
	return l.Plugin.SkipEvent(l, reason)
}

func (l Link) Rejoin() error {
	if err := l.can(ReplicationActionRejoin); err != nil {
		return err
	}
	return l.Plugin.Rejoin(l)
}

// ReplicationKind is the type of a plugin (ReplicationManifest.Type), carried in each status row
// (SlaveStatus.Kind).
type ReplicationKind string

const (
	ReplicationKindNative  ReplicationKind = "native"
	ReplicationKindCDC     ReplicationKind = "cdc"
	ReplicationKindTrigger ReplicationKind = "trigger"
)

// ErrReplicationNotSupported: the plugin has no such action (a skip of an event on a CDC feed,
// a repoint of a feed whose source follows replication-plugin-source, a bootstrap not moved
// into the plugins yet). Wrapped with the action and the link: errors.Is tests it.
var ErrReplicationNotSupported = errors.New("operation not supported by this replication link")

func replicationNotSupported(op string, l Link) error {
	return fmt.Errorf("%w: %s on %s link %q", ErrReplicationNotSupported, op, l.Plugin.Manifest().Name, l.Name)
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
	LinkByTest               = "regtest"
)

// failoverOrSwitchover is the reason a MasterFailover(fail) gives.
func failoverOrSwitchover(fail bool) string {
	if fail {
		return LinkByFailover
	}
	return LinkBySwitchover
}

// ReplicationLink is the server's managed native link, the channel replication-source-name
// names (cluster.Conf.MasterConn): the one the failover, switchover, rejoin and reseed drive.
func (server *ServerMonitor) ReplicationLink() Link {
	return server.nativeLink(server.ClusterGroup.Conf.MasterConn)
}

// ReplicationChannelLink is the server's native link of that channel name.
func (server *ServerMonitor) ReplicationChannelLink(channel string) Link {
	return server.nativeLink(channel)
}

// ReplicationLinks are every link of the server: its native channels as last read, then the
// CDC feeds that write into it.
func (server *ServerMonitor) ReplicationLinks() []Link {
	var links []Link
	for _, r := range server.Replications {
		links = append(links, server.nativeLink(r.ConnectionName.String))
	}
	links = append(links, server.ClusterGroup.cdcFeedsOf(server)...)
	return links
}

// ReplicationLinkStatus are the status rows of every link of the server, each with its kind:
// the native channels, then the CDC feeds. What the GUI, the API and the MCP read.
func (server *ServerMonitor) ReplicationLinkStatus() []dbhelper.SlaveStatus {
	out := make([]dbhelper.SlaveStatus, 0, len(server.Replications)+len(server.ReplicationFeeds))
	out = append(out, server.Replications...)
	out = append(out, server.ReplicationFeeds...)
	return out
}
