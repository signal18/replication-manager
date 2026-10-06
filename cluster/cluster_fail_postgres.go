// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
)

// postgresPromoteWaitSeconds bounds the wait for a promotion to complete.
const postgresPromoteWaitSeconds = 60

// isPostgresStreaming tells whether the cluster's master change is PostgreSQL's: a WAL
// streaming topology, whose failover shares nothing with the MariaDB/MySQL statements
// (no read_only, no CHANGE MASTER, no GTID): a standby is promoted, the others follow it.
func (cluster *Cluster) isPostgresStreaming() bool {
	if cluster.GetTopology() == config.TopoMasterSlavePgStream {
		return true
	}
	return cluster.Conf.TopologyTarget == config.TopoMasterSlavePgStream && cluster.master != nil && cluster.master.IsPostgreSQLHost()
}

// postgresFailover is the failover of a WAL streaming topology, called by MasterFailover
// inside the failover state.
//
// Failover (primary lost): the standby that RECEIVED the most WAL is promoted -- PostgreSQL
// replays everything it received before opening to writes -- and the other standbys are
// pointed to it. The lost primary is not touched: when it comes back it is a second
// primary on an older timeline and must rejoin as a standby (rewind or re-seed), which is
// the job of the database sidecar, not of this function.
//
// Switchover goes the same way, see postgresSwitchover.
func (cluster *Cluster) postgresFailover(fail bool) bool {
	if cluster.isPostgresLogical() {
		if fail {
			return cluster.postgresLogicalFailover()
		}
		return cluster.postgresLogicalSwitchover()
	}
	if !fail {
		return cluster.postgresSwitchover()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "------------------------------------")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting PostgreSQL primary failover")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "------------------------------------")
	if cluster.master == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot failover without a known primary")
		return false
	}

	// Phase 1: election
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Electing a new primary")
	candidate, received := cluster.electPostgresCandidate()
	if candidate == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Standby %s has been elected as a new primary (received WAL position %d)", candidate.URL, received)

	ss, _ := candidate.GetSlaveStatus(candidate.ReplicationSourceName)
	cluster.oldMaster = cluster.master
	cluster.master = candidate
	cluster.failoverPreScript(fail)

	// Phase 2: promotion. Nothing was changed before this point: a failed promotion
	// leaves the standby a standby, so the previous primary is restored.
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Promoting %s", candidate.URL)
	logs, err := dbhelper.PostgresPromote(candidate.Conn, postgresPromoteWaitSeconds)
	cluster.LogSQL(logs, err, candidate.URL, "MasterFailover", config.LvlErr, "Could not promote %s: %s", candidate.URL, err)
	if err != nil {
		cluster.master = cluster.oldMaster
		cluster.oldMaster = nil
		return false
	}
	cluster.master.SetMaster()
	cluster.master.delete(&cluster.slaves)

	crash := new(Crash)
	crash.UnixTimestamp = time.Now().Unix()
	crash.URL = cluster.oldMaster.URL
	crash.ElectedMasterURL = cluster.master.URL
	crash.FailoverMasterLogFile = ss.MasterLogFile.String
	crash.FailoverMasterLogPos = ss.ReadMasterLogPos.String
	crash.FailoverIOGtid = gtid.NewList(fmt.Sprintf("0-0-%d", received))
	cluster.Crashes = append(cluster.Crashes, crash)
	cluster.ensureCrashArchive(crash)
	cluster.LoadFailoverHistory()
	cluster.ConfigManager.SaveConfig(cluster, true)

	// Phase 3: routes
	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverPostScript(fail)

	// Phase 4: the other standbys follow the new primary
	cluster.postgresPointStandbysToMaster()
	cluster.backendStateChangeProxies()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	cluster.MasterChangeTs = time.Now().Unix()
	cluster.FailoverCtr++
	cluster.FailoverTs = time.Now().Unix()
	return true
}

// electPostgresCandidate returns the standby to promote and the WAL position it received:
// the most advanced one among the standbys that are up, not ignored and not in maintenance;
// a preferred standby wins a tie.
func (cluster *Cluster) electPostgresCandidate() (*ServerMonitor, uint64) {
	var elected *ServerMonitor
	var best uint64
	for _, sl := range cluster.slaves {
		if sl == nil || sl.Conn == nil || sl.IsDown() || sl.IsIgnored() || sl.IsMaintenance || !sl.IsPostgreSQLHost() {
			continue
		}
		if sl.SourceClusterName != "" && sl.SourceClusterName != cluster.Name {
			continue
		}
		received, logs, err := dbhelper.PostgresReceivedLSN(sl.Conn)
		cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not read the received WAL position of %s: %s", sl.URL, err)
		if err != nil {
			continue
		}
		if elected == nil || received > best || (received == best && sl.IsPrefered() && !elected.IsPrefered()) {
			elected, best = sl, received
		}
	}
	return elected, best
}

// postgresPointStandbysToMaster makes the remaining standbys follow the new primary (its
// new timeline is followed by default: recovery_target_timeline = latest).
func (cluster *Cluster) postgresPointStandbysToMaster() {
	for _, sl := range cluster.slaves {
		if sl == nil || sl.IsDown() || sl.URL == cluster.master.URL || (cluster.oldMaster != nil && sl.URL == cluster.oldMaster.URL) {
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pointing standby %s to the new primary %s", sl.URL, cluster.master.URL)
		logs, err := dbhelper.PostgresSetPrimary(sl.Conn, cluster.master.Host, cluster.master.Port)
		cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not point standby %s to the new primary: %s", sl.URL, err)
	}
}

// postgresAppOfServer returns the app that runs a monitored PostgreSQL server: the service
// replication-manager stops and starts to change the server's role.
func (cluster *Cluster) postgresAppOfServer(s *ServerMonitor) *App {
	for _, a := range cluster.Apps {
		if a != nil && a.Port == s.Port && (a.Host == s.Host || a.Name == s.Name) {
			return a
		}
	}
	return nil
}

// postgresSwitchover is the switchover of a WAL streaming topology. PostgreSQL cannot
// demote a running primary, so the primary is restarted as a standby:
//
//  1. the jobs sidecar of the primary arms its next start as a standby of the candidate
//     (nothing changes yet: the primary keeps serving);
//  2. the primary's service is stopped: a clean shutdown sends all its WAL to the standbys,
//     and from here no write is accepted anywhere;
//  3. the candidate is promoted once the primary is gone, routes are switched;
//  4. the old primary's service is started: it comes back as a standby of the new primary,
//     with its data (no copy: it stopped before the promotion, the timelines do not fork).
//
// If the promotion fails the old primary is started again and promoted back.
func (cluster *Cluster) postgresSwitchover() bool {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-------------------------------------")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting PostgreSQL primary switchover")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-------------------------------------")
	old := cluster.master
	if old == nil || old.Conn == nil || old.IsDown() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a running primary")
		return false
	}
	if cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PostgreSQL switchover restarts the primary as a standby: it needs the OpenSVC orchestrator")
		return false
	}
	app := cluster.postgresAppOfServer(old)
	if app == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "PostgreSQL switchover restarts the primary as a standby: %s is not a service of this cluster", old.URL)
		return false
	}
	candidate, _ := cluster.electPostgresCandidate()
	if candidate == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found")
		return false
	}
	if ss, err := candidate.GetSlaveStatus(candidate.ReplicationSourceName); err != nil || ss.SlaveIORunning.String != "Yes" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Candidate %s is not streaming from the primary, cancelling switchover", candidate.URL)
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Standby %s has been elected as a new primary", candidate.URL)

	// 1. arm the old primary's next start
	if err := old.armPostgresNextStart(config.ConstTaskPgStandby, candidate); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover cancelled, nothing was changed: %s", err)
		return false
	}
	cluster.failoverPreScript(false)

	// 2. stop the primary
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping primary %s", old.URL)
	if err := cluster.OpenSVCStopAppService(app, ""); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not stop primary %s: %s. Its next start is armed as a standby of %s", old.URL, err, candidate.URL)
		return false
	}
	if !cluster.postgresWaitWalReceiverGone(candidate, 180*time.Second) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Primary %s still streams to %s after the stop request, switchover abandoned: check the service of %s, its next start is armed as a standby", old.URL, candidate.URL, old.URL)
		return false
	}
	received, logs, err := dbhelper.PostgresReceivedLSN(candidate.Conn)
	cluster.LogSQL(logs, err, candidate.URL, "MasterFailover", config.LvlErr, "Could not read the received WAL position of %s: %s", candidate.URL, err)
	ss, _ := candidate.GetSlaveStatus(candidate.ReplicationSourceName)

	// 3. promote
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Promoting %s", candidate.URL)
	logs, err = dbhelper.PostgresPromote(candidate.Conn, postgresPromoteWaitSeconds)
	cluster.LogSQL(logs, err, candidate.URL, "MasterFailover", config.LvlErr, "Could not promote %s: %s", candidate.URL, err)
	if err != nil {
		cluster.postgresSwitchoverRollback(old, app)
		return false
	}
	cluster.oldMaster = old
	cluster.master = candidate
	cluster.master.SetMaster()
	cluster.master.delete(&cluster.slaves)
	// the old primary is a replica from here: a stale Master state on a stopped server
	// is what discovery would otherwise pick up as the master again
	old.SetState(stateSlave)
	cluster.slaves = append(cluster.slaves, old)

	crash := new(Crash)
	crash.Switchover = true
	crash.UnixTimestamp = time.Now().Unix()
	crash.URL = old.URL
	crash.ElectedMasterURL = candidate.URL
	crash.FailoverMasterLogFile = ss.MasterLogFile.String
	crash.FailoverMasterLogPos = ss.ReadMasterLogPos.String
	crash.FailoverIOGtid = gtid.NewList(fmt.Sprintf("0-0-%d", received))
	cluster.Crashes = append(cluster.Crashes, crash)
	cluster.ensureCrashArchive(crash)
	cluster.LoadFailoverHistory()
	cluster.ConfigManager.SaveConfig(cluster, true)

	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverPostScript(false)

	// 4. the old primary comes back as a standby
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting %s as a standby of %s", old.URL, candidate.URL)
	startErr := cluster.postgresStartApp(app)
	if startErr == nil {
		// stopped before the promotion: no divergent tail by construction
		cluster.finishCrashRecord(crash, RejoinResultNoDivergence)
	}
	cluster.postgresPointStandbysToMaster()
	cluster.backendStateChangeProxies()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	cluster.MasterChangeTs = time.Now().Unix()
	if startErr != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover to %s done but the old primary %s could not be started: %s. Start its service: it is armed to come back as a standby of %s", candidate.URL, old.URL, startErr, candidate.URL)
		return false
	}
	return true
}

// postgresStartApp starts the service of a PostgreSQL server, retrying while the
// orchestrator still runs the stop that preceded (409, orchestration in progress).
func (cluster *Cluster) postgresStartApp(app *App) error {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		err := cluster.OpenSVCStartAppService(app, "")
		if err == nil {
			return nil
		}
		msg := err.Error()
		if !(strings.Contains(msg, "409") || strings.Contains(msg, "in progress")) || time.Now().After(deadline) {
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Orchestrator still busy with %s, start retried in 5 s", app.GetServiceName())
		time.Sleep(5 * time.Second)
	}
}

// postgresWaitWalReceiverGone waits until a standby no longer receives from its primary:
// the primary has shut down and has sent everything it had.
func (cluster *Cluster) postgresWaitWalReceiverGone(standby *ServerMonitor, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int
		if err := standby.Conn.Get(&n, "SELECT count(*) FROM pg_stat_wal_receiver WHERE status = 'streaming'"); err == nil && n == 0 {
			return true
		}
		time.Sleep(time.Second)
	}
	return false
}

// postgresSwitchoverRollback restores the old primary after a failed promotion: it was
// stopped and armed to start as a standby, so it is started and promoted back.
func (cluster *Cluster) postgresSwitchoverRollback(old *ServerMonitor, app *App) {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover failed at the promotion: starting %s again and promoting it back", old.URL)
	if err := cluster.postgresStartApp(app); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not start %s: %s. NO PRIMARY: start its service, then promote it", old.URL, err)
		return
	}
	deadline := time.Now().Add(180 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(3 * time.Second)
		var inRecovery bool
		if err := old.Conn.Get(&inRecovery, "SELECT pg_is_in_recovery()"); err != nil {
			continue
		}
		if !inRecovery {
			return
		}
		logs, err := dbhelper.PostgresPromote(old.Conn, postgresPromoteWaitSeconds)
		cluster.LogSQL(logs, err, old.URL, "MasterFailover", config.LvlErr, "Could not promote %s back: %s", old.URL, err)
		if err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "%s is primary again", old.URL)
			return
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "NO PRIMARY: %s did not come back within 3 minutes, promote it when it is up", old.URL)
}

// postgresRejoin brings a former primary back after a failover. It was not stopped before
// the promotion, so its timeline may have forked: its data is copied again from the new
// primary. The jobs sidecar arms the re-seed, the restart of the service applies it.
func (server *ServerMonitor) postgresRejoin() error {
	cluster := server.ClusterGroup
	master := cluster.GetMaster()
	if master == nil || master.Id == server.Id || master.IsDown() {
		return nil
	}
	var inRecovery bool
	if err := server.Conn.Get(&inRecovery, "SELECT pg_is_in_recovery()"); err != nil || inRecovery {
		// not reachable yet, or already a standby
		return err
	}
	if cluster.isPostgresLogical() {
		return server.postgresLogicalRejoin()
	}
	// Only the RECORDED former primary of the last master change is re-seeded, and only
	// from the primary that change elected, which must be up and really a primary. A master
	// pointer alone is not enough: with the old primary stopped and no replica left, the
	// generic discovery once re-designated the stopped server as master and this rejoin
	// cleared the data of the server that had just been promoted (2026-10-06).
	last := cluster.lastCrash()
	if last == nil || last.URL != server.URL || last.ElectedMasterURL != master.URL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "PostgreSQL server %s is a second primary but is not the former primary of the last master change: not rejoined, check the cluster", server.URL)
		return nil
	}
	var masterInRecovery bool
	if master.Conn == nil || master.Conn.Get(&masterInRecovery, "SELECT pg_is_in_recovery()") != nil || masterInRecovery {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Former primary %s not rejoined: %s is not a running primary", server.URL, master.URL)
		return nil
	}
	if !cluster.Conf.Autorejoin {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "PostgreSQL server %s is back as a second primary and auto rejoin is disabled: re-seed it as a standby of %s", server.URL, master.URL)
		return nil
	}
	app := cluster.postgresAppOfServer(server)
	if app == nil || cluster.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "PostgreSQL server %s is back as a second primary and is not a service of this cluster: re-seed it as a standby of %s", server.URL, master.URL)
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejoining former primary %s: re-seed from %s", server.URL, master.URL)
	if err := server.armPostgresNextStart(config.ConstTaskPgReseed, master); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rejoin of %s failed: %s", server.URL, err)
		return err
	}
	if err := cluster.OpenSVCRestartAppService(app, "", ""); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Rejoin of %s: could not restart its service: %s", server.URL, err)
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Former primary %s restarted to re-seed from %s", server.URL, master.URL)
	return nil
}

// lastCrash returns the record of the last failover or switchover, nil when none.
func (cluster *Cluster) lastCrash() *Crash {
	if len(cluster.Crashes) == 0 {
		return nil
	}
	return cluster.Crashes[len(cluster.Crashes)-1]
}
