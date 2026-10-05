// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Stephane Varoqui <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
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
// Switchover is refused for now: PostgreSQL cannot demote a running primary, the old
// primary has to be stopped and restarted as a standby.
func (cluster *Cluster) postgresFailover(fail bool) bool {
	if !fail {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Switchover is not available on a PostgreSQL WAL streaming topology yet: a running primary cannot be demoted, it must be restarted as a standby")
		return false
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

	// Phase 4: the other standbys follow the new primary (its new timeline is followed
	// by default: recovery_target_timeline = latest)
	for _, sl := range cluster.slaves {
		if sl == nil || sl.IsDown() || sl.URL == cluster.master.URL {
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pointing standby %s to the new primary %s", sl.URL, cluster.master.URL)
		logs, err := dbhelper.PostgresSetPrimary(sl.Conn, cluster.master.Host, cluster.master.Port)
		cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not point standby %s to the new primary: %s", sl.URL, err)
	}
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
