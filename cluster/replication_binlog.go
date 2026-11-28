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

// The binlog native plugin: one Go type registered four times, one manifest each
// (repl-mariadb-mariadb-native, repl-mysql-mysql-native, repl-mysql-mariadb-native,
// repl-columnstore-columnstore-native). The manifest decides the engines and the topologies
// (Galera for MariaDB only, Group Replication for MySQL only); the SQL differences between
// MariaDB, MySQL and Percona are dbhelper's: no engine branch here (T1, T23). The ColumnStore
// servers replicate their binlog as any MariaDB master-slave: the same object, under its own
// manifest (its topology); their rebootstrap stays cluster_columnstore_replica.go until the
// bootstrap moves into the plugins (step 5).

const (
	ReplicationPluginMariaDBNative      = "repl-mariadb-mariadb-native"
	ReplicationPluginMySQLNative        = "repl-mysql-mysql-native"
	ReplicationPluginMySQLMariaDBNative = "repl-mysql-mariadb-native"
	ReplicationPluginColumnStoreNative  = "repl-columnstore-columnstore-native"
)

// binlogTopologies are the topologies every binlog plugin sets up today (BootstrapReplication).
var binlogTopologies = []ReplicationTopology{
	{Name: config.TopoMasterSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMasterSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencySemiSync},
	{Name: config.TopoMultiTierSlave, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMultiMaster, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoMultiMasterRing, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoActivePassive, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
	{Name: config.TopoBinlogServer, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync},
}

// binlogActions: every verb but the bootstrap and the creation of a link, which stay in prov.go
// until step 5.
var binlogActions = []string{
	ReplicationActionRemove, ReplicationActionStatus, ReplicationActionWaitCaughtUp,
	ReplicationActionStart, ReplicationActionStop,
	ReplicationActionStartCopyEvents, ReplicationActionStopCopyEvents,
	ReplicationActionStartApplyEvents, ReplicationActionStopApplyEvents,
	ReplicationActionResetPositions, ReplicationActionSetPosition, ReplicationActionChangeSource,
	ReplicationActionPromote, ReplicationActionSkipEvent, ReplicationActionRejoin,
	ReplicationActionConfigure, ReplicationActionRotateCredential,
}

func withTopologies(extra ...ReplicationTopology) []ReplicationTopology {
	return append(append([]ReplicationTopology{}, binlogTopologies...), extra...)
}

var (
	mariadbNativeManifest = ReplicationManifest{
		Name: ReplicationPluginMariaDBNative, Type: string(ReplicationKindNative),
		Source: []string{"MariaDB"}, Target: []string{"MariaDB"},
		Topologies: withTopologies(ReplicationTopology{Name: config.TopoMultiMasterWsrep, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyGroup}),
		Actions:    binlogActions,
	}
	mysqlNativeManifest = ReplicationManifest{
		Name: ReplicationPluginMySQLNative, Type: string(ReplicationKindNative),
		Source: []string{"MySQL", "Percona"}, Target: []string{"MySQL", "Percona"},
		Topologies: withTopologies(ReplicationTopology{Name: config.TopoMultiMasterGrouprep, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyGroup}),
		Actions:    binlogActions,
	}
	mysqlMariaDBNativeManifest = ReplicationManifest{
		Name: ReplicationPluginMySQLMariaDBNative, Type: string(ReplicationKindNative),
		Source: []string{"MySQL", "Percona"}, Target: []string{"MariaDB"},
		Topologies: withTopologies(),
		Actions:    binlogActions,
	}
	columnstoreNativeManifest = ReplicationManifest{
		Name: ReplicationPluginColumnStoreNative, Type: string(ReplicationKindNative),
		Source: []string{"MariaDB"}, Target: []string{"MariaDB"},
		Topologies: []ReplicationTopology{{Name: config.TopoColumnstore, Transport: ReplicationTransportLogical, Consistency: ReplicationConsistencyAsync}},
		Actions:    binlogActions,
	}
)

// binlogReplicationPlugin is the binlog plugin under one of its manifests.
type binlogReplicationPlugin struct {
	unsupportedReplicationPlugin
	manifest ReplicationManifest
}

func (p *binlogReplicationPlugin) Manifest() ReplicationManifest { return p.manifest }

func (p *binlogReplicationPlugin) newReplication(base replicationBase) Replication {
	return &binlogReplication{nativeReplication{base}}
}

// Bootstrap: step 5 of the design moves BootstrapReplication (prov.go) here; until then the
// bootstrap runs from prov.go.
func (p *binlogReplicationPlugin) Bootstrap(c *Cluster, topology string, master *ServerMonitor) error {
	return fmt.Errorf("%w: %s of topology %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionBootstrap, topology, p.manifest.Name)
}

// Create: step 5, with the bootstrap.
func (p *binlogReplicationPlugin) Create(source Endpoint, target *ServerMonitor, name, reason string) (Replication, error) {
	return nil, fmt.Errorf("%w: %s of %q by %s (step 5)", ErrReplicationNotSupported, ReplicationActionCreate, name, p.manifest.Name)
}

// binlogReplication is one binlog channel of a MariaDB, MySQL or Percona server: the common
// native verbs, plus the copy and the apply apart, the promotion, the position, the skip of an
// event, the options and the credential.
type binlogReplication struct {
	nativeReplication
}

// StartCopyEvents starts the IO thread. The record of a stop stays until the tick sees both
// threads run (checkLinkChanges): half a link started is not a link running.
func (r *binlogReplication) StartCopyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.StartSlaveIOThread(server.Conn, r.name, server.DBVersion)
}

// StopCopyEvents stops the IO thread, recorded once the statement succeeded.
func (r *binlogReplication) StopCopyEvents(reason string) (string, error) {
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
func (r *binlogReplication) StartApplyEvents(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.StartSlaveSQLThread(server.Conn, r.name, server.DBVersion)
}

// StopApplyEvents stops the SQL thread, recorded once the statement succeeded.
func (r *binlogReplication) StopApplyEvents(reason string) (string, error) {
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

// Promote ends this edge, the link part of the failover and switchover's promotion of the
// candidate, moved from cluster_fail.go with its statements and its log lines: on MySQL and
// Percona the channel is stopped first (a reset needs every thread stopped), then RESET SLAVE ALL
// on the channel (Remove), the reset tried whatever the stop gave, as before. read_only is the
// server's, not the link's: the failover sets it off itself (SetReadWrite) after reading the new
// master's position (openNewMaster). Whether the stop comes first is dbhelper's
// (ResetAllNeedsStop). Each failure is logged here; the error joins them. PostgreSQL's is
// postgresReplication.Promote.
func (r *binlogReplication) Promote(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	cluster := server.ClusterGroup
	var all string
	var errs []error
	if dbhelper.ResetAllNeedsStop(server.DBVersion) {
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
	return all, errors.Join(errs...)
}

// SetPosition sets where the apply resumes (dbhelper.SetReplicaPosition). MariaDB:
// gtid_slave_pos, the link's resume position (every caller's statement until now). MySQL and
// Percona: GTID_PURGED (the backup verification's), which is the server's whole GTID state, not
// the link's: valid only right after a restore, once RESET MASTER emptied gtid_executed; on a
// server whose gtid_executed is not empty it is refused (ErrReplicationPositionNotEmpty), never
// attempted. Not recorded (the callers stop the link around it).
func (r *binlogReplication) SetPosition(at Position, reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	logs, err := dbhelper.SetReplicaPosition(server.Conn, server.DBVersion, at.GTID)
	if errors.Is(err, ErrReplicationPositionNotEmpty) {
		err = fmt.Errorf("%s: %w", server.URL, err)
	}
	return logs, err
}

// WaitCaughtUp waits for the channel to apply until (dbhelper.ReplicaWaitReached):
// MASTER_GTID_WAIT on MariaDB, MASTER_POS_WAIT / SOURCE_POS_WAIT of the channel otherwise, the
// statements WaitSyncToMaster and WaitSyncToTarget sent. The timeout is
// ErrReplicationNotCaughtUp; a wait the server aborted (NULL) an error.
func (r *binlogReplication) WaitCaughtUp(until Position, timeout time.Duration) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	reached, logs, err := dbhelper.ReplicaWaitReached(server.Conn, server.DBVersion, until.GTID, until.File, until.Pos, int(timeout/time.Second), r.name)
	if err == nil && !reached {
		err = fmt.Errorf("%w: %s after %s", ErrReplicationNotCaughtUp, until.String(), timeout)
	}
	return logs, err
}

// SkipEvent skips one event; reason is not used: the skip runs between a stop and a start,
// which record and clear the link's state.
func (r *binlogReplication) SkipEvent(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	return dbhelper.SkipBinlogEvent(server.Conn, r.name, server.DBVersion)
}

// setSlaveHeartbeat changes a channel's period; a variable so a test sees the call.
var setSlaveHeartbeat = dbhelper.SetSlaveHeartbeat

// Configure applies opts on the channel, each option with the statement the force-slave-*
// enforcement sent before (CheckSlaveSettings, checkReplicationHeartbeatPeriod): the GTID mode
// (SetSlaveGTIDMode), gtid_strict_mode (SetSlaveGTIDModeStrict), the exec mode
// (SetSlaveExecMode), the parallel mode (SetSlaveParallelMode, which stops and starts the SQL
// thread) and the heartbeat period (SetSlaveHeartbeat). The decision of what to set, and the
// states, stay the monitor's.
func (r *binlogReplication) Configure(opts LinkOptions, reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	if opts.empty() {
		return "", nil
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
// channel to change it).
func (r *binlogReplication) RotateCredential(reason string) (string, error) {
	server, err := r.conn()
	if err != nil {
		return "", err
	}
	cluster := server.ClusterGroup
	return dbhelper.ChangeReplicationPassword(server.Conn, dbhelper.ChangeMasterOpt{
		User:     cluster.GetRplUser(),
		Password: cluster.GetRplPass(),
		Channel:  r.name,
	}, server.DBVersion)
}
