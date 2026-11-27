// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The native replications: a channel of the server's own replication (MariaDB and MySQL/Percona
// channels, PostgreSQL subscriptions and the standby's WAL receiver), given by one of the native
// plugins built into repman (F2: a failover never waits on an external process):
// replication_binlog.go (repl-mariadb-mariadb-native, repl-mysql-mysql-native,
// repl-mysql-mariadb-native, repl-columnstore-columnstore-native), replication_postgres.go
// (repl-postgresql-postgresql-native), replication_group.go (the group membership of the MySQL
// plugin's group replication). This file holds what every one of them does the same way, the
// engine branches being dbhelper's (T1/T23), and the monitor's read that builds the objects.
// These files are the only ones of the cluster, server, mcp and regtest packages that call
// dbhelper's replication link helpers (TestNoReplicationLinkSQLOutsideBackend).

// nativePlugin is a native plugin: it builds the object of each replication the monitor reads.
type nativePlugin interface {
	ReplicationPlugin
	newReplication(base replicationBase) Replication
}

// nativePlugins are the native plugins by name, built in (no cluster state: their manifests
// are fixed).
var nativePlugins = map[string]nativePlugin{
	ReplicationPluginMariaDBNative:      &binlogReplicationPlugin{manifest: mariadbNativeManifest},
	ReplicationPluginMySQLNative:        &binlogReplicationPlugin{manifest: mysqlNativeManifest},
	ReplicationPluginMySQLMariaDBNative: &binlogReplicationPlugin{manifest: mysqlMariaDBNativeManifest},
	ReplicationPluginColumnStoreNative:  &binlogReplicationPlugin{manifest: columnstoreNativeManifest},
	ReplicationPluginPostgreSQLNative:   &postgresReplicationPlugin{},
}

// nativePluginName is the native plugin of a replication into server from source: the
// server's engine, the cluster's topology, and the source's engine when repman monitors it
// (a MySQL source into a MariaDB server is repl-mysql-mariadb-native). Without a known
// engine, the MariaDB one, as every object was before the split.
func (server *ServerMonitor) nativePluginName(source Endpoint) string {
	if server == nil {
		return ReplicationPluginMariaDBNative
	}
	if server.IsPostgreSQLHost() { // the engine, or a PostgreSQL topology declared
		return ReplicationPluginPostgreSQLNative
	}
	if server.DBVersion == nil {
		return ReplicationPluginMariaDBNative
	}
	switch {
	case server.DBVersion.IsMySQLOrPercona():
		return ReplicationPluginMySQLNative
	case server.ClusterGroup != nil && server.ClusterGroup.Conf != nil && server.ClusterGroup.HasConfigTopoColumnstore():
		return ReplicationPluginColumnStoreNative
	case source.Server != nil && source.Server.DBVersion != nil && source.Server.DBVersion.IsMySQLOrPercona():
		return ReplicationPluginMySQLMariaDBNative
	}
	return ReplicationPluginMariaDBNative
}

// newNativeObject is the object of base's plugin (base.manifest.Name), a binlog one for a name
// no native plugin has.
func newNativeObject(base replicationBase) Replication {
	if p, ok := nativePlugins[base.manifest.Name]; ok {
		return p.newReplication(base)
	}
	return &binlogReplication{nativeReplication{base}}
}

// isNativePlugin tells whether name is a built-in native plugin.
func isNativePlugin(name string) bool {
	_, ok := nativePlugins[name]
	return ok
}

// nativeReplication is one channel of a server: MariaDB and MySQL/Percona channels, a
// PostgreSQL subscription or a standby's WAL receiver, with the verbs every native plugin does
// the same way (status, start, stop, reset of the positions, removal, CHANGE MASTER, rejoin).
// Each plugin's object embeds it and adds its own (binlogReplication, postgresReplication).
type nativeReplication struct {
	replicationBase
}

// newNativeReplication is the object of one row the monitor read (refreshNativeLinks), or of a
// channel not defined yet (ReplicationChannelLink): its source resolved from the row's
// Master_Host and Master_Port, then its plugin chosen (nativePluginName).
func (server *ServerMonitor) newNativeReplication(channel string, row dbhelper.SlaveStatus) Replication {
	base := newReplicationBase(ReplicationManifest{}, server, channel, row, row.MasterHost.String, row.MasterPort.String)
	base.manifest = nativePlugins[server.nativePluginName(base.source)].Manifest()
	return newNativeObject(base)
}

// conn is the target, refused without a connection pool (and for a copy decoded from JSON).
func (r *nativeReplication) conn() (*ServerMonitor, error) {
	if r.target == nil {
		return nil, r.noTarget()
	}
	if r.target.Conn == nil {
		return nil, errors.New("No database connection pool")
	}
	return r.target, nil
}

// Status reads the channel's status from the server now (SHOW SLAVE '<channel>' STATUS), not the
// monitor's last read (Row).
func (r *nativeReplication) Status() (dbhelper.SlaveStatus, string, error) {
	if r.target == nil {
		return dbhelper.SlaveStatus{}, "", r.noTarget()
	}
	ss, logs, err := dbhelper.GetSlaveStatus(r.target.Conn, r.name, r.target.DBVersion)
	ss.Kind = string(ReplicationKindNative)
	return ss, logs, err
}

func (r *nativeReplication) Stop(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	// recorded once the statement ran: a failed stop leaves no WARN0352 on a link still running
	logs, err := dbhelper.StopSlave(server.Conn, r.name, server.DBVersion)
	if err == nil {
		server.recordLinkChange(r.name, linkOpStop, reason)
	}
	return logs, err
}

func (r *nativeReplication) Start(reason string) (string, error) {
	if r.target == nil {
		return "", r.noTarget()
	}
	if r.target.Conn == nil {
		return "", errors.New("No database connection")
	}
	logs, err := dbhelper.StartSlave(r.target.Conn, r.name, r.target.DBVersion)
	if err == nil {
		r.target.clearLinkChange(r.name)
	}
	return logs, err
}

// ResetPositions clears the channel's positions and relay logs and keeps the link (RESET
// SLAVE without ALL): what a reseed may do, never delete the link (law T27, #2093).
func (r *nativeReplication) ResetPositions(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	logs, err := dbhelper.ResetSlave(server.Conn, false, r.name, server.DBVersion)
	if err == nil {
		server.recordLinkChange(r.name, linkOpResetPositions, reason)
	}
	return logs, err
}

// Remove is RESET SLAVE ALL on the channel. Not recorded, and its earlier record (the stop
// before a promotion) is dropped: the link no longer exists, the promotion that removed it
// (failover, switchover) has its own states, so reason is not used.
func (r *nativeReplication) Remove(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	logs, err := dbhelper.ResetSlave(server.Conn, true, r.name, server.DBVersion)
	if err == nil {
		server.clearLinkChange(r.name)
	}
	return logs, err
}

// ChangeSource points this server's replication at to. Every CHANGE MASTER of a cluster goes
// through it, so the replication TLS material is decided in one place, from the primary's live
// certificate (ReplicationTLSFor, #1933). Recorded once the statement succeeded, on the channel
// opt names.
func (r *nativeReplication) ChangeSource(to Endpoint, opt ChangeSourceOpt, reason string) (string, error) {
	server := r.target
	if server == nil {
		return "", r.noTarget()
	}
	if to.Server != nil {
		opt.Host, opt.Port = to.Server.Host, to.Server.Port
	} else if to.Host != "" {
		opt.Host, opt.Port = to.Host, to.Port
	}
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
	logs, err := dbhelper.ChangeMaster(server.Conn, opt, server.DBVersion)
	if err == nil {
		server.recordLinkChange(opt.Channel, linkOpChangeSource, reason)
	}
	return logs, err
}

// Rejoin is the rejoin family of srv_rejoin.go (flashback, reseed, dump, SST, script, ...):
// the server rejoins the cluster's master, whatever the channel the link names.
func (r *nativeReplication) Rejoin() error {
	if r.target == nil {
		return r.noTarget()
	}
	return r.target.rejoinMaster()
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
	for _, rep := range server.NativeReplications() {
		res, errslave := rep.Stop(reason)
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
	for _, rep := range server.NativeReplications() {
		if rep.Name() != cluster.Conf.MasterConn {
			res, errslave := rep.Stop(reason)
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
		for _, rep := range server.NativeReplications() {
			server.recordLinkChange(rep.Name(), linkOpStop, reason)
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

// refreshNativeLinks is the monitor's read of the server's channels: one native Replication
// per row, its source resolved; Refresh stores them in server.Replications with the CDC feeds.
func (server *ServerMonitor) refreshNativeLinks() (ReplicationList, string, error) {
	cluster := server.ClusterGroup
	rows, logs, err := server.readNativeLinks((!cluster.Conf.MxsBinlogOn || !server.IsMaxscale) && server.DBVersion.IsMariaDB() || server.DBVersion.IsPostgreSQL())
	var list ReplicationList
	if rows != nil {
		list = make(ReplicationList, 0, len(rows))
	}
	for _, row := range rows {
		list = append(list, server.newNativeReplication(row.ConnectionName.String, row))
	}
	if len(rows) > 0 && err == nil && server.DBVersion.IsPostgreSQL() && server.ReplicationSourceName == "" {
		//setting first subscription if we don't have one
		server.ReplicationSourceName = rows[0].ConnectionName.String
	}
	return list, logs, err
}
