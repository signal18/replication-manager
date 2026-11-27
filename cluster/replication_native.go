// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The native backend of ReplicationLink: a channel of the server's own replication, MariaDB
// and MySQL/Percona channels, PostgreSQL subscriptions and the standby's WAL receiver (the
// engine branches are dbhelper's, T1/T23). It is the code that was spread over srv.go
// (StopSlave, StartSlave, ResetSlave, ...), srv_set.go (ChangeMaster) and the monitor's status
// read, moved here without a change of what it sends: this file is the only one of the
// cluster, server, mcp and regtest packages that calls dbhelper's replication link helpers
// (TestNoReplicationLinkSQLOutsideBackend).

// nativeLink is one channel of the server; "" the default (unnamed) channel.
type nativeLink struct {
	server  *ServerMonitor
	channel string
}

func (server *ServerMonitor) nativeLink(channel string) *nativeLink {
	return &nativeLink{server: server, channel: channel}
}

func (l *nativeLink) Kind() ReplicationKind { return ReplicationKindNative }
func (l *nativeLink) Name() string          { return l.channel }

func (l *nativeLink) Status() (dbhelper.SlaveStatus, bool) {
	ss, err := l.server.GetSlaveStatus(l.channel)
	if err != nil || ss == nil {
		return dbhelper.SlaveStatus{}, false
	}
	return *ss, true
}

// ReadStatus reads the channel's status from the server now (SHOW SLAVE '<channel>' STATUS),
// not the monitor's last read.
func (l *nativeLink) ReadStatus() (dbhelper.SlaveStatus, string, error) {
	ss, logs, err := dbhelper.GetSlaveStatus(l.server.Conn, l.channel, l.server.DBVersion)
	ss.Kind = string(ReplicationKindNative)
	return ss, logs, err
}

func (l *nativeLink) Stop(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	// recorded once the statement ran: a failed stop leaves no WARN0352 on a link still running
	logs, err := dbhelper.StopSlave(l.server.Conn, l.channel, l.server.DBVersion)
	if err == nil {
		l.server.recordLinkChange(l.channel, linkOpStop, reason)
	}
	return logs, err
}

func (l *nativeLink) Start(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection")
	}
	logs, err := dbhelper.StartSlave(l.server.Conn, l.channel, l.server.DBVersion)
	if err == nil {
		l.server.clearLinkChange(l.channel)
	}
	return logs, err
}

func (l *nativeLink) StopRead(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	l.server.recordLinkChange(l.channel, linkOpStopRead, reason)
	return dbhelper.StopSlaveIOThread(l.server.Conn, l.channel, l.server.DBVersion)
}

func (l *nativeLink) StopApply(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	l.server.recordLinkChange(l.channel, linkOpStopApply, reason)
	return dbhelper.StopSlaveSQLThread(l.server.Conn, l.channel, l.server.DBVersion)
}

// ResetPositions clears the channel's positions and relay logs and keeps the link (RESET
// SLAVE without ALL): what a reseed may do, never delete the link (law T27, #2093).
func (l *nativeLink) ResetPositions(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	logs, err := dbhelper.ResetSlave(l.server.Conn, false, l.channel, l.server.DBVersion)
	if err == nil {
		l.server.recordLinkChange(l.channel, linkOpResetPositions, reason)
	}
	return logs, err
}

// Remove is RESET SLAVE ALL on the channel. Not recorded, and its earlier record (the stop
// before a promotion) is dropped: the link no longer exists, the promotion that removed it
// (failover, switchover) has its own states, so reason is not used.
func (l *nativeLink) Remove(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	logs, err := dbhelper.ResetSlave(l.server.Conn, true, l.channel, l.server.DBVersion)
	if err == nil {
		l.server.clearLinkChange(l.channel)
	}
	return logs, err
}

// ChangeSource points this server's replication at opt's primary. Every CHANGE MASTER of
// a cluster goes through it, so the replication TLS material is decided in one place,
// from the primary's live certificate (ReplicationTLSFor, #1933).
func (l *nativeLink) ChangeSource(opt dbhelper.ChangeMasterOpt, reason string) (string, error) {
	server := l.server
	if opt.SSLCa == "" && !opt.SSLCaClear && !opt.SSLOff {
		tls := server.ReplicationTLSFor(opt.Host, opt.Port, opt.SSL)
		opt.SSLCa, opt.SSLCaClear, opt.SSLOff = tls.CA, tls.ClearCA, tls.SSLOff
		// The decision is traceable: a replica that then fails its handshake opens
		// WARN0236 (CheckReplicationTLS).
		if cluster := server.ClusterGroup; cluster != nil && cluster.Conf != nil && (tls.CA != "" || tls.ClearCA || tls.SSLOff) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Replication TLS of %s towards %s:%s: CA %q, CA emptied %t, TLS off %t", server.URL, opt.Host, opt.Port, tls.CA, tls.ClearCA, tls.SSLOff)
		}
	}
	server.recordLinkChange(opt.Channel, linkOpChangeSource, reason)
	return dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
}

// SkipEvent skips one event; reason is not used: the skip runs between a stop and a start,
// which record and clear the link's state.
func (l *nativeLink) SkipEvent(reason string) (string, error) {
	if l.server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	return dbhelper.SkipBinlogEvent(l.server.Conn, l.channel, l.server.DBVersion)
}

// Rejoin is the rejoin family of srv_rejoin.go (flashback, reseed, dump, SST, script, ...):
// the server rejoins the cluster's master, whatever the channel the link names.
func (l *nativeLink) Rejoin() error {
	return l.server.rejoinMaster()
}

// SkipReplicationEvent stops the managed link, skips the next event of its apply and starts it
// again.
func (server *ServerMonitor) SkipReplicationEvent(reason string) {
	cluster := server.ClusterGroup
	link := server.ReplicationLink()
	// the same three statements as before, each error now logged
	for _, step := range []struct {
		what string
		run  func(string) (string, error)
	}{{"stop", link.Stop}, {"skip of the event", link.SkipEvent}, {"start", link.Start}} {
		if _, err := step.run(reason); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTopology, config.LvlErr, "Skip of a replication event on %s: the %s failed: %s", server.URL, step.what, err)
		}
	}
}

// StopAllSlaves stops every channel of the monitor's last status, one by one.
func (server *ServerMonitor) StopAllSlaves(reason string) (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	sql := ""
	var lasterror error
	for _, rep := range server.Replications {
		res, errslave := server.nativeLink(rep.ConnectionName.String).Stop(reason)
		sql += res
		if errslave != nil {
			lasterror = errslave
		}
	}

	return sql, lasterror
}

// StopAllExtraSourceSlaves stops every channel of the monitor's last status but the managed one.
func (server *ServerMonitor) StopAllExtraSourceSlaves(reason string) (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	sql := ""
	var lasterror error
	for _, rep := range server.Replications {
		if rep.ConnectionName.String != cluster.Conf.MasterConn {
			res, errslave := server.nativeLink(rep.ConnectionName.String).Stop(reason)
			sql += res
			if errslave != nil {
				lasterror = errslave
			}
		}
	}

	return sql, lasterror
}

// stopEveryChannel is STOP ALL SLAVES (MariaDB): every channel the server has, whatever the
// monitor's last status. Recorded on the channels of that status.
func (server *ServerMonitor) stopEveryChannel(reason string) (string, error) {
	logs, err := dbhelper.StopAllSlaves(server.Conn, server.DBVersion)
	if err == nil {
		for _, rep := range server.Replications {
			server.recordLinkChange(rep.ConnectionName.String, linkOpStop, reason)
		}
	}
	return logs, err
}

// removeEveryChannel is the replication bootstrap's wipe of every channel (RESET SLAVE ALL
// on each): an explicit operator request, which sets up the whole topology again; reason is not
// used, the bootstrap logs itself.
func (server *ServerMonitor) removeEveryChannel(reason string) (string, error) {
	return dbhelper.ResetAllSlaves(server.Conn, server.DBVersion)
}

// readNativeLinks reads every channel of the server from the server itself: SHOW ALL SLAVES
// STATUS (allSlaves, MariaDB and the PostgreSQL subscriptions), else the channels one by one
// (MySQL). Each row's Kind is native.
func (server *ServerMonitor) readNativeLinks(allSlaves bool) ([]dbhelper.SlaveStatus, string, error) {
	var ss []dbhelper.SlaveStatus
	var logs string
	var err error
	if allSlaves {
		ss, logs, err = dbhelper.GetAllSlavesStatus(server.Conn, server.DBVersion)
	} else {
		ss, logs, err = dbhelper.GetChannelSlaveStatus(server.Conn, server.DBVersion)
	}
	for i := range ss {
		ss[i].Kind = string(ReplicationKindNative)
	}
	return ss, logs, err
}

// refreshNativeLinks is the monitor's read of the server's channels into server.Replications.
func (server *ServerMonitor) refreshNativeLinks() (string, error) {
	cluster := server.ClusterGroup
	var logs string
	var err error
	server.Replications, logs, err = server.readNativeLinks((!cluster.Conf.MxsBinlogOn || !server.IsMaxscale) && server.DBVersion.IsMariaDB() || server.DBVersion.IsPostgreSQL())
	if len(server.Replications) > 0 && err == nil && server.DBVersion.IsPostgreSQL() && server.ReplicationSourceName == "" {
		//setting first subscription if we don't have one
		server.ReplicationSourceName = server.Replications[0].ConnectionName.String
	}
	return logs, err
}
