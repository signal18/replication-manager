// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
)

// Master change of a PostgreSQL LOGICAL replication topology (publication on the primary,
// subscription on each replica). Unlike WAL streaming nothing restarts: a subscriber becomes
// the publisher by dropping its subscription, the old publisher follows it by subscribing.
//
// The freeze of the old primary is default_transaction_read_only (there is no read_only nor
// FLUSH TABLES WITH READ LOCK on PostgreSQL): new transactions of every session are
// read-only, the open client sessions are terminated, the apply worker is not affected.
// The old primary KEEPS that read-only default as a subscriber; it is lifted when it is
// promoted again.
//
// Logical replication does not replicate DDL: the publication must exist on the new primary
// BEFORE a subscription to it is created, or the subscriber's slot decodes changes with a
// catalog that lacks it and fails for ever (seen on preprod 2026-10-06).

// isPostgresLogical: the cluster's master change is PostgreSQL logical replication.
func (cluster *Cluster) isPostgresLogical() bool {
	if cluster.GetTopology() == config.TopoMasterSlavePgLog {
		return true
	}
	return cluster.Conf.TopologyTarget == config.TopoMasterSlavePgLog && cluster.master != nil && cluster.master.IsPostgreSQLHost()
}

// electPostgresLogicalCandidate returns the subscriber to promote: the one whose
// subscription is enabled and applying, a preferred one first.
func (cluster *Cluster) electPostgresLogicalCandidate() (*ServerMonitor, string) {
	var elected *ServerMonitor
	var subscription string
	for _, sl := range cluster.slaves {
		if sl == nil || sl.Conn == nil || sl.IsDown() || sl.IsIgnored() || sl.IsMaintenance || !sl.IsPostgreSQLHost() {
			continue
		}
		if sl.SourceClusterName != "" && sl.SourceClusterName != cluster.Name {
			continue
		}
		ss, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
		if err != nil || ss.SlaveIORunning.String != "Yes" || ss.ConnectionName.String == "" {
			continue
		}
		if elected == nil || (sl.IsPrefered() && !elected.IsPrefered()) {
			elected, subscription = sl, ss.ConnectionName.String
		}
	}
	return elected, subscription
}

// postgresLogicalPromote turns a subscriber into the publisher: publication first, then the
// subscription goes, then writes are allowed.
func (cluster *Cluster) postgresLogicalPromote(candidate *ServerMonitor, subscription string, publisherAlive bool) error {
	cluster.postgresInstallDDLReplication(candidate)
	logs, err := dbhelper.PostgresEnsurePublication(candidate.Conn, cluster.Conf.MasterConn)
	cluster.LogSQL(logs, err, candidate.URL, "MasterFailover", config.LvlErr, "Could not create the publication on %s: %s", candidate.URL, err)
	if err != nil {
		return err
	}
	logs, err = dbhelper.PostgresDropSubscription(candidate.Conn, subscription, publisherAlive)
	cluster.LogSQL(logs, err, candidate.URL, "MasterFailover", config.LvlErr, "Could not drop the subscription of %s: %s", candidate.URL, err)
	if err != nil {
		return err
	}
	return candidate.SetReadWrite()
}

// postgresLogicalSubscribe makes a server follow the new primary: a subscription without
// data copy (it has the data up to the switch), and the read-only default as a replica.
func (cluster *Cluster) postgresLogicalSubscribe(server *ServerMonitor, primary *ServerMonitor) error {
	// the DDL replication objects before the subscription: the log table must exist here
	// for its rows to be applied, and the apply trigger with it
	cluster.postgresInstallDDLReplication(server)
	if err := server.ChangeMasterTo(primary, "SLAVE_POS"); err != nil {
		return err
	}
	logs, err := server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not set the read-only default on %s: %s", server.URL, err)
	return err
}

func (cluster *Cluster) postgresLogicalRecordChange(old, candidate *ServerMonitor, switchover bool, ss *dbhelper.SlaveStatus) *Crash {
	crash := new(Crash)
	crash.Switchover = switchover
	crash.UnixTimestamp = time.Now().Unix()
	crash.URL = old.URL
	crash.ElectedMasterURL = candidate.URL
	if ss != nil {
		// the candidate's position as a subscriber, read before its subscription went
		crash.FailoverMasterLogFile = ss.MasterLogFile.String
		crash.FailoverMasterLogPos = ss.ReadMasterLogPos.String
	}
	crash.FailoverIOGtid = gtid.NewList("0-0-0")
	cluster.Crashes = append(cluster.Crashes, crash)
	cluster.ensureCrashArchive(crash)
	cluster.LoadFailoverHistory()
	cluster.ConfigManager.SaveConfig(cluster, true)
	return crash
}

// postgresLogicalSwitchover: freeze the publisher, wait for the subscriber to confirm
// everything, promote it, make the old publisher its subscriber, switch the routes.
func (cluster *Cluster) postgresLogicalSwitchover() bool {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-----------------------------------------------")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting PostgreSQL logical replication switchover")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-----------------------------------------------")
	old := cluster.master
	if old == nil || old.Conn == nil || old.IsDown() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a running primary")
		return false
	}
	candidate, subscription := cluster.electPostgresLogicalCandidate()
	if candidate == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found: no subscriber is applying")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Subscriber %s has been elected as a new primary", candidate.URL)
	cluster.failoverPreScript(false)

	// freeze: no new write on the old primary, then wait for the subscriber to confirm all
	logs, err := old.SetReadOnly()
	cluster.LogSQL(logs, err, old.URL, "MasterFailover", config.LvlErr, "Could not freeze %s: %s", old.URL, err)
	if err != nil {
		return false
	}
	n, logs, err := dbhelper.PostgresTerminateClientBackends(old.Conn)
	cluster.LogSQL(logs, err, old.URL, "MasterFailover", config.LvlWarn, "Could not terminate the client sessions of %s: %s", old.URL, err)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Primary %s frozen (read-only default), %d client sessions terminated", old.URL, n)
	caughtUp := false
	for deadline := time.Now().Add(time.Duration(cluster.Conf.SwitchWaitTrx) * time.Second); time.Now().Before(deadline); {
		if ok, _, err := dbhelper.PostgresSubscriberCaughtUp(old.Conn, subscription); err == nil && ok {
			caughtUp = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !caughtUp {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Subscriber %s did not confirm all the changes of %s within %d s (switchover-wait-trx), switchover cancelled, primary unfrozen", candidate.URL, old.URL, cluster.Conf.SwitchWaitTrx)
		if err := old.SetReadWrite(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not unfreeze %s: %s", old.URL, err)
		}
		return false
	}

	ss, _ := candidate.GetSlaveStatus(candidate.ReplicationSourceName)
	if err := cluster.postgresLogicalPromote(candidate, subscription, true); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Promotion of %s failed, primary %s unfrozen", candidate.URL, old.URL)
		if err := old.SetReadWrite(); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not unfreeze %s: %s", old.URL, err)
		}
		return false
	}
	cluster.oldMaster = old
	cluster.master = candidate
	cluster.master.SetMaster()
	cluster.master.delete(&cluster.slaves)
	crash := cluster.postgresLogicalRecordChange(old, candidate, true, ss)

	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverPostScript(false)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Old primary %s subscribes to %s", old.URL, candidate.URL)
	if err := cluster.postgresLogicalSubscribe(old, candidate); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Old primary %s could not subscribe to %s: %s", old.URL, candidate.URL, err)
	} else {
		old.SetState(stateSlave)
		cluster.slaves = append(cluster.slaves, old)
		cluster.finishCrashRecord(crash, RejoinResultNoDivergence)
	}
	cluster.postgresLogicalRepointSubscribers()
	cluster.backendStateChangeProxies()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	cluster.MasterChangeTs = time.Now().Unix()
	return true
}

// postgresLogicalFailover: the publisher is lost, a subscriber becomes the publisher. The
// other subscribers are re-pointed to it; the lost publisher is rejoined when it comes back.
func (cluster *Cluster) postgresLogicalFailover() bool {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "---------------------------------------------")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting PostgreSQL logical replication failover")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "---------------------------------------------")
	if cluster.master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot failover without a known primary")
		return false
	}
	candidate, subscription := cluster.electPostgresLogicalCandidate()
	if candidate == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found: no subscriber with an enabled subscription")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Subscriber %s has been elected as a new primary", candidate.URL)
	old := cluster.master
	cluster.failoverPreScript(true)
	ss, _ := candidate.GetSlaveStatus(candidate.ReplicationSourceName)
	if err := cluster.postgresLogicalPromote(candidate, subscription, false); err != nil {
		return false
	}
	cluster.oldMaster = old
	cluster.master = candidate
	cluster.master.SetMaster()
	cluster.master.delete(&cluster.slaves)
	cluster.postgresLogicalRecordChange(old, candidate, false, ss)

	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverPostScript(true)
	cluster.postgresLogicalRepointSubscribers()
	cluster.backendStateChangeProxies()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	cluster.MasterChangeTs = time.Now().Unix()
	cluster.FailoverCtr++
	cluster.FailoverTs = time.Now().Unix()
	return true
}

// postgresLogicalRepointSubscribers makes the other subscribers follow the new publisher.
func (cluster *Cluster) postgresLogicalRepointSubscribers() {
	for _, sl := range cluster.slaves {
		if sl == nil || sl.Conn == nil || sl.IsDown() || sl.URL == cluster.master.URL || (cluster.oldMaster != nil && sl.URL == cluster.oldMaster.URL) {
			continue
		}
		name, logs, err := dbhelper.PostgresSubscriptionName(sl.Conn)
		cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not read the subscription of %s: %s", sl.URL, err)
		if name != "" {
			alive := cluster.oldMaster != nil && !cluster.oldMaster.IsDown()
			logs, err = dbhelper.PostgresDropSubscription(sl.Conn, name, alive)
			cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not drop the subscription of %s: %s", sl.URL, err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Subscriber %s re-pointed to the new primary %s", sl.URL, cluster.master.URL)
		if err := cluster.postgresLogicalSubscribe(sl, cluster.master); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not re-point %s to %s: %s", sl.URL, cluster.master.URL, err)
		}
	}
}

// postgresLogicalRejoin brings a former publisher back as a subscriber of the new one. Its
// own publication and the leftover slot of its former subscriber are dropped. Writes it took
// after the failover are NOT reconciled by logical replication: they are reported, the
// operator decides (a re-copy is a logical dump restore, the backup job's business).
func (server *ServerMonitor) postgresLogicalRejoin() error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()
	if master == nil || master.Id == server.Id || master.IsDown() || master.Conn == nil {
		return nil
	}
	if name, _, _ := dbhelper.PostgresSubscriptionName(server.Conn); name != "" {
		return nil // already a subscriber
	}
	last := cluster.lastCrash()
	if last == nil || last.URL != server.URL || last.ElectedMasterURL != master.URL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "PostgreSQL server %s is a second publisher but is not the former primary of the last master change: not rejoined, check the cluster", server.URL)
		return nil
	}
	if !cluster.Conf.Autorejoin {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Former primary %s is back and auto rejoin is disabled: subscribe it to %s", server.URL, master.URL)
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoining former primary %s as a subscriber of %s", server.URL, master.URL)
	logs, err := server.SetReadOnly()
	cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Could not freeze %s: %s", server.URL, err)
	// the slot the new primary held on it as a subscriber, left behind by the failover
	for _, s := range cluster.Servers {
		if s != nil && s.URL != server.URL && s.IsPostgreSQLHost() {
			logs, err := dbhelper.PostgresDropReplicationSlot(server.Conn, cluster.postgresSubscriptionNameFor())
			cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlWarn, "Could not drop the leftover slot on %s: %s", server.URL, err)
			break
		}
	}
	if err := cluster.postgresLogicalSubscribe(server, master); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rejoin of %s failed: %s", server.URL, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Former primary %s subscribes to %s from now on: the writes it took after the failover, if any, are not reconciled by logical replication", server.URL, master.URL)
	return nil
}

// postgresSubscriptionNameFor is the subscription (and slot) name replication-manager uses:
// the replication source name, "alltables" by default (dbhelper.ChangeMaster).
func (cluster *Cluster) postgresSubscriptionNameFor() string {
	if cluster.Conf.MasterConn != "" {
		return cluster.Conf.MasterConn
	}
	return "alltables"
}

// postgresSchemaSyncOnDiff is what the schema diff of the monitor (MonitorTableSchemaDiff,
// WARN0164: tables of the master missing or different on a replica) triggers on a logical
// replication subscriber: PostgreSQL does not replicate DDL, so a table created on the
// publisher is unknown to the subscriber and its apply worker dies on every change to it
// ("logical replication target relation does not exist", replication stopped). The
// subscriber's jobs sidecar is asked to create the missing tables from the primary's
// definition and refresh the subscription (task pgschemasync); the diff re-arms it until the
// subscriber matches.
func (cluster *Cluster) postgresSchemaSyncOnDiff(sl *ServerMonitor) {
	if !cluster.isPostgresLogical() || sl == nil || !sl.IsPostgreSQLHost() || sl.IsDown() {
		return
	}
	master := cluster.GetMaster()
	if master == nil || master.IsDown() {
		return
	}
	if sl.hasCookie(postgresJobCookie(string(config.ConstTaskPgSchemaSync))) {
		return // already asked, the sidecar is on it
	}
	sl.streamTasks.Store(postgresNextStartTargetKey(string(config.ConstTaskPgSchemaSync)), master.Host+":"+master.Port)
	if err := sl.setTaskCookie(string(config.ConstTaskPgSchemaSync)); err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModTask, config.LvlInfo, "Schema sync requested from the jobs sidecar of %s: the subscriber differs from the publication", sl.URL)
	}
}

// postgresInstallDDLReplication installs (or, with replication-pg-logical-ddl off, disables)
// the trigger-based DDL replication on a server of a logical replication cluster: idempotent,
// run at bootstrap, when a server subscribes and when one is promoted.
func (cluster *Cluster) postgresInstallDDLReplication(server *ServerMonitor) {
	if server == nil || server.Conn == nil {
		return
	}
	if !cluster.Conf.PgLogicalDDLReplication {
		logs, err := dbhelper.PostgresDropDDLReplication(server.Conn)
		cluster.LogSQL(logs, err, server.URL, "Bootstrap", config.LvlWarn, "Could not disable the DDL replication on %s: %s", server.URL, err)
		return
	}
	logs, err := dbhelper.PostgresInstallDDLReplication(server.Conn)
	cluster.LogSQL(logs, err, server.URL, "Bootstrap", config.LvlErr, "Could not install the DDL replication on %s: %s", server.URL, err)
}

// postgresRefreshSubscriptionOnDDL runs every tick on a logical subscriber: when a replicated
// DDL arrived (the DDL log grew, an index lookup) and the publication has tables the
// subscription has not, the subscription is refreshed once for the whole batch, so a table
// a replicated DDL created joins the subscription with its rows copied (REFRESH cannot run
// in the apply transaction that executed the DDL).
func (server *ServerMonitor) postgresRefreshSubscriptionOnDDL() {
	cluster := server.ClusterGroup
	if !cluster.isPostgresLogical() || !server.IsSlave || server.Conn == nil {
		return
	}
	id, _, err := dbhelper.PostgresDDLLogMaxID(server.Conn)
	if err != nil || id == server.pgDDLLogSeen {
		return
	}
	server.pgDDLLogSeen = id
	master := cluster.GetMaster()
	if master == nil || master.Conn == nil {
		return
	}
	name := cluster.postgresSubscriptionNameFor()
	needed, _, err := dbhelper.PostgresSubscriptionNeedsRefresh(master.Conn, server.Conn, name)
	if err != nil || !needed {
		return
	}
	logs, err := dbhelper.PostgresRefreshSubscription(server.Conn, name)
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlInfo, "Subscription of %s refreshed after a replicated DDL: %s", server.URL, err)
}
