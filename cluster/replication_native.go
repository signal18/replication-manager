// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

// The native plugin: a channel of the server's own replication, MariaDB and MySQL/Percona
// channels, PostgreSQL subscriptions and the standby's WAL receiver (the engine branches are
// dbhelper's, T1/T23). It is the code that was spread over srv.go (StopSlave, StartSlave,
// ResetSlave, ...), srv_set.go (ChangeMaster) and the monitor's status read, moved here without a
// change of what it sends: this file is the only one of the cluster, server, mcp and regtest
// packages that calls dbhelper's replication link helpers (TestNoReplicationLinkSQLOutsideBackend).
// One manifest for every engine until step 3 of the design splits it.

type nativeReplicationPlugin struct {
	unsupportedReplicationPlugin
}

// nativeTopologies are the topologies the native code sets up today (BootstrapReplication).
var nativeTopologies = []ReplicationTopology{
	{Name: config.TopoMasterSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMasterSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencySemiSync},
	{Name: config.TopoMultiTierSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMultiMaster, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMultiMasterRing, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoActivePassive, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoBinlogServer, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMultiMasterWsrep, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyGroup},
	{Name: config.TopoMultiMasterGrouprep, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyGroup},
	{Name: config.TopoMasterSlavePgStream, Transport: ReplicationTransportPhysical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMasterSlavePgLog, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoColumnstore, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
}

// nativeManifest: every action but the bootstrap and the creation of a link, which stay in
// prov.go until step 5.
var nativeManifest = ReplicationManifest{
	Name:       ReplicationPluginNative,
	Type:       string(ReplicationKindNative),
	Source:     []string{"MariaDB", "MySQL", "Percona", "PostgreSQL"},
	Target:     []string{"MariaDB", "MySQL", "Percona", "PostgreSQL"},
	Topologies: nativeTopologies,
	Actions: []string{
		ReplicationActionRemove, ReplicationActionStatus, ReplicationActionWaitCaughtUp,
		ReplicationActionStart, ReplicationActionStop,
		ReplicationActionStartCopyEvents, ReplicationActionStopCopyEvents,
		ReplicationActionStartApplyEvents, ReplicationActionStopApplyEvents,
		ReplicationActionResetPositions, ReplicationActionSetPosition, ReplicationActionChangeSource,
		ReplicationActionPromote, ReplicationActionSkipEvent, ReplicationActionRejoin,
		ReplicationActionConfigure, ReplicationActionRotateCredential,
	},
}

func (p *nativeReplicationPlugin) Manifest() ReplicationManifest { return nativeManifest }

// Bootstrap: step 5 of the design moves BootstrapReplication (prov.go) here; until then the
// bootstrap runs from prov.go.
func (p *nativeReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, ReplicationPluginNative)
}

// Create: step 5, with the bootstrap.
func (p *nativeReplicationPlugin) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionCreate, name, ReplicationPluginNative)
}

// nativeReplication is one channel of a server: MariaDB and MySQL/Percona channels, a
// PostgreSQL subscription or a standby's WAL receiver. The engine branches are dbhelper's.
type nativeReplication struct {
	replicationBase
}

// nativeReplication is the object of one row the monitor read (refreshNativeLinks), or of a
// channel not defined yet (ReplicationChannelLink): its source resolved from the row's
// Master_Host and Master_Port.
func (server *ServerMonitor) nativeReplication(channel string, row dbhelper.SlaveStatus) *nativeReplication {
	return &nativeReplication{replicationBase: newReplicationBase(nativeManifest, server, channel, row, row.MasterHost.String, row.MasterPort.String)}
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

// StartCopyEvents starts the IO thread. The record of a stop stays until the tick sees both
// threads run (checkLinkChanges): half a link started is not a link running.
func (r *nativeReplication) StartCopyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.StartSlaveIOThread(server.Conn, r.name, server.DBVersion)
}

// StopCopyEvents stops the IO thread, recorded once the statement succeeded.
func (r *nativeReplication) StopCopyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	logs, err := dbhelper.StopSlaveIOThread(server.Conn, r.name, server.DBVersion)
	if err == nil {
		server.recordLinkChange(r.name, linkOpStopCopyEvents, reason)
	}
	return logs, err
}

// StartApplyEvents starts the SQL thread; the record stays as for StartCopyEvents.
func (r *nativeReplication) StartApplyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.StartSlaveSQLThread(server.Conn, r.name, server.DBVersion)
}

// StopApplyEvents stops the SQL thread, recorded once the statement succeeded.
func (r *nativeReplication) StopApplyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	logs, err := dbhelper.StopSlaveSQLThread(server.Conn, r.name, server.DBVersion)
	if err == nil {
		server.recordLinkChange(r.name, linkOpStopApplyEvents, reason)
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

// Promote is the failover and switchover's promotion of the candidate, moved from
// cluster_fail.go with its statements and its log lines: on MySQL and Percona the channel is
// stopped first (a reset needs every thread stopped), then RESET SLAVE ALL on the channel
// (Remove), then read_only off (SetReadWrite), each step tried whatever the previous one gave,
// as before. PostgreSQL: pg_promote() (PostgresPromote), the standby leaves recovery. Each
// failure is logged here; the error joins them.
func (r *nativeReplication) Promote(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	if server.IsPostgreSQLHost() {
		return dbhelper.PostgresPromote(server.Conn, postgresPromoteWaitSeconds)
	}
	cluster := server.ClusterGroup
	var all string
	var errs []error
	if server.DBVersion.IsMySQLOrPercona() {
		// Need to stop all threads to reset on MySQL
		logs, err := r.Stop(reason)
		cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Failed stop slave on new master %s %s", server.URL, err)
		all += logs
		errs = append(errs, err)
	}
	logs, err := r.Remove(reason)
	cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Failed reset slave on new master %s %s", server.URL, err)
	all += logs
	errs = append(errs, err)
	if err := server.SetReadWrite(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set new master as read-write")
		errs = append(errs, err)
	}
	return all, errors.Join(errs...)
}

// SetPosition sets where the apply resumes: gtid_slave_pos on MariaDB (SetGTIDSlavePos, every
// caller's statement until now), GTID_PURGED on MySQL and Percona (SetGtidPurged, the backup
// verification's). A global of the server, not of the channel; not recorded (the callers stop
// the link around it). PostgreSQL: a subscription's position is its replication origin, which
// repman does not set: ErrReplicationNotSupported.
func (r *nativeReplication) SetPosition(at Position, reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	switch {
	case server.DBVersion.IsPostgreSQL():
		return "", r.notSupported(ReplicationActionSetPosition + " on PostgreSQL")
	case server.DBVersion.IsMySQLOrPercona():
		return dbhelper.SetGtidPurged(server.Conn, server.DBVersion, at.GTID)
	}
	return dbhelper.SetGTIDSlavePos(server.Conn, at.GTID)
}

// WaitCaughtUp waits for the channel to apply until: MASTER_GTID_WAIT on MariaDB
// (MasterWaitGTIDReached), MASTER_POS_WAIT / SOURCE_POS_WAIT of the channel otherwise
// (MasterPosWaitReached), the statements WaitSyncToMaster and WaitSyncToTarget sent. The
// timeout is ErrReplicationNotCaughtUp; a wait the server aborted (NULL) an error.
// PostgreSQL: the wait reads the primary's LSN (postgresWaitSync, cluster_fail_postgres.go)
// until step 3 moves it here: ErrReplicationNotSupported.
func (r *nativeReplication) WaitCaughtUp(until Position, timeout time.Duration) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	if server.DBVersion.IsPostgreSQL() {
		return "", r.notSupported(ReplicationActionWaitCaughtUp + " on PostgreSQL")
	}
	secs := int(timeout / time.Second)
	var reached bool
	var logs string
	if server.DBVersion.IsMariaDB() {
		reached, logs, err = dbhelper.MasterWaitGTIDReached(server.Conn, until.GTID, secs)
	} else {
		reached, logs, err = dbhelper.MasterPosWaitReached(server.Conn, server.DBVersion, until.File, until.Pos, secs, r.name)
	}
	if err == nil && !reached {
		err = fmt.Errorf("%w: %s after %s", ErrReplicationNotCaughtUp, until.String(), timeout)
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

// SkipEvent skips one event; reason is not used: the skip runs between a stop and a start,
// which record and clear the link's state.
func (r *nativeReplication) SkipEvent(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.SkipBinlogEvent(server.Conn, r.name, server.DBVersion)
}

// Rejoin is the rejoin family of srv_rejoin.go (flashback, reseed, dump, SST, script, ...):
// the server rejoins the cluster's master, whatever the channel the link names.
func (r *nativeReplication) Rejoin() error {
	if r.target == nil {
		return r.noTarget()
	}
	return r.target.rejoinMaster()
}

// setSlaveHeartbeat changes a channel's period; a variable so a test sees the call.
var setSlaveHeartbeat = dbhelper.SetSlaveHeartbeat

// Configure applies opts on the channel, each option with the statement the force-slave-*
// enforcement sent before (CheckSlaveSettings, checkReplicationHeartbeatPeriod): the GTID mode
// (SetSlaveGTIDMode), gtid_strict_mode (SetSlaveGTIDModeStrict), the exec mode
// (SetSlaveExecMode), the parallel mode (SetSlaveParallelMode, which stops and starts the SQL
// thread) and the heartbeat period (SetSlaveHeartbeat). The decision of what to set, and the
// states, stay the monitor's. PostgreSQL has none of these: ErrReplicationNotSupported.
func (r *nativeReplication) Configure(opts LinkOptions, reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	if opts.empty() {
		return "", nil
	}
	if server.DBVersion.IsPostgreSQL() {
		return "", r.notSupported(ReplicationActionConfigure + " on PostgreSQL")
	}
	var all string
	var errs []error
	run := func(logs string, err error) {
		all += logs
		errs = append(errs, err)
	}
	if opts.GTIDMode != "" {
		run(dbhelper.SetSlaveGTIDMode(server.Conn, opts.GTIDMode, r.name, server.DBVersion))
	}
	if opts.GTIDStrict {
		run(dbhelper.SetSlaveGTIDModeStrict(server.Conn, server.DBVersion))
	}
	if opts.ExecMode != "" {
		run(dbhelper.SetSlaveExecMode(server.Conn, opts.ExecMode, r.name, server.DBVersion))
	}
	if opts.ParallelMode != "" {
		run(dbhelper.SetSlaveParallelMode(server.Conn, opts.ParallelMode, r.name, server.DBVersion))
	}
	if opts.HeartbeatPeriod != "" {
		run(setSlaveHeartbeat(server.Conn, opts.HeartbeatPeriod, r.name, server.DBVersion))
	}
	return all, errors.Join(errs...)
}

// RotateCredential sets the cluster's replication account (GetRplUser, GetRplPass) on the
// channel: ChangeReplicationPassword, the rejoin's statement after a rotation (it stops the
// channel to change it). PostgreSQL: a subscription's password is in its conninfo, which repman
// does not rotate yet: ErrReplicationNotSupported.
func (r *nativeReplication) RotateCredential(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	if server.DBVersion.IsPostgreSQL() {
		return "", r.notSupported(ReplicationActionRotateCredential + " on PostgreSQL")
	}
	cluster := server.ClusterGroup
	return dbhelper.ChangeReplicationPassword(server.Conn, dbhelper.ChangeMasterOpt{
		User:     cluster.GetRplUser(),
		Password: cluster.GetRplPass(),
		Channel:  r.name,
	}, server.DBVersion)
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
		list = append(list, server.nativeReplication(row.ConnectionName.String, row))
	}
	if len(rows) > 0 && err == nil && server.DBVersion.IsPostgreSQL() && server.ReplicationSourceName == "" {
		//setting first subscription if we don't have one
		server.ReplicationSourceName = rows[0].ConnectionName.String
	}
	return list, logs, err
}
