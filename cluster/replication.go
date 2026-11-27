// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

// ReplicationLink is one replication link of a server: a channel of its native replication
// (MariaDB, MySQL, PostgreSQL), or a CDC feed of a replication plugin that writes into it
// (cluster/replplugin). The link belongs to the client's topology (law T27): repman reads it,
// starts and stops it, and moves it to another source only from the failover, switchover and
// rejoin code, under their guards. Design: doc/implementation/cluster/REPLICATION_INTERFACE.md.
//
// Every method that changes the link (Stop, StopRead, StopApply, ResetPositions, ChangeSource)
// takes the reason and records the change on the server: WARN0352 names the channel, the
// action and the reason until the link runs again (checkLinkChanges). A Start that succeeds
// drops the record. A method whose concept does not exist on a backend returns
// ErrReplicationNotSupported, never nil.
type ReplicationLink interface {
	// Kind is ReplicationKindNative or ReplicationKindCDC.
	Kind() ReplicationKind
	// Name is the channel name ("" the default channel) or the CDC feed name ("cdc:<name>").
	Name() string
	// Status is the link's row as the monitor last read it, its Kind set; ok false when the
	// server has no such link (yet).
	Status() (dbhelper.SlaveStatus, bool)
	// ReadStatus reads the link's status now (native: SHOW SLAVE '<channel>' STATUS), with the
	// statement it ran.
	ReadStatus() (dbhelper.SlaveStatus, string, error)
	// Start starts the read and the apply.
	Start(reason string) (string, error)
	// Stop stops the read and the apply; the link stays defined.
	Stop(reason string) (string, error)
	// StopRead stops the read only (native: the IO thread).
	StopRead(reason string) (string, error)
	// StopApply stops the apply only (native: the SQL thread).
	StopApply(reason string) (string, error)
	// ResetPositions clears the positions and keeps the link (RESET SLAVE without ALL, T27).
	ResetPositions(reason string) (string, error)
	// Remove deletes the link's definition (RESET SLAVE ALL): the failover and switchover's
	// promotion of a replica, and a request of the operator only (T27), never another path.
	Remove(reason string) (string, error)
	// ChangeSource points the link at opt's source (CHANGE MASTER): the failover, switchover
	// and rejoin code only, under their guards. opt.Channel names the native channel, as
	// dbhelper.ChangeMaster reads it.
	ChangeSource(opt dbhelper.ChangeMasterOpt, reason string) (string, error)
	// SkipEvent skips the next event of the apply (sql_slave_skip_counter); the caller
	// stops and starts the link around it.
	SkipEvent(reason string) (string, error)
	// Rejoin brings the server back as a replica of the cluster (native: the rejoin family,
	// srv_rejoin.go), or the feed back to the destination (cdc: resume, or rebuild).
	Rejoin() error
}

// ReplicationKind is the backend of a link, carried in each status row (SlaveStatus.Kind).
type ReplicationKind string

const (
	ReplicationKindNative ReplicationKind = "native"
	ReplicationKindCDC    ReplicationKind = "cdc"
)

// ErrReplicationNotSupported: the backend has no such operation (a skip of an event on a CDC
// feed, a repoint of a feed whose source follows replication-plugin-source). Wrapped with the
// operation and the link: errors.Is tests it.
var ErrReplicationNotSupported = errors.New("operation not supported by this replication link")

func replicationNotSupported(op string, link ReplicationLink) error {
	return fmt.Errorf("%w: %s on %s link %q", ErrReplicationNotSupported, op, link.Kind(), link.Name())
}

// The changes a link records (linkChange.op): the verb of WARN0352.
const (
	linkOpStop           = "stopped"
	linkOpStopRead       = "read stopped"
	linkOpStopApply      = "apply stopped"
	linkOpResetPositions = "positions reset"
	linkOpChangeSource   = "repointed"
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
func (server *ServerMonitor) ReplicationLink() ReplicationLink {
	return server.nativeLink(server.ClusterGroup.Conf.MasterConn)
}

// ReplicationChannelLink is the server's native link of that channel name.
func (server *ServerMonitor) ReplicationChannelLink(channel string) ReplicationLink {
	return server.nativeLink(channel)
}

// ReplicationLinks are every link of the server: its native channels as last read, then the
// CDC feeds that write into it.
func (server *ServerMonitor) ReplicationLinks() []ReplicationLink {
	var links []ReplicationLink
	for _, r := range server.Replications {
		links = append(links, server.nativeLink(r.ConnectionName.String))
	}
	for _, f := range server.ClusterGroup.cdcFeedsOf(server) {
		links = append(links, f)
	}
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
