// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/state"
)

// MasterFailover triggers a leader change and returns the new master URL when single possible leader
func (cluster *Cluster) MasterFailover(fail bool) bool {
	if cluster.GetTopology() == topoMultiMasterRing || cluster.GetTopology() == topoMultiMasterWsrep || cluster.GetTopology() == topoMultiMasterGrouprep {
		res := cluster.VMasterFailover(fail)
		return res
	}
	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancel already in failover")
		return false
	}

	cluster.StateMachine.SetFailoverState()
	defer cluster.StateMachine.RemoveFailoverState()
	// Phase 1: Cleanup and election
	var err error
	if fail == false {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "--------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting master switchover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "--------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checking long running updates on master %d", cluster.Conf.SwitchWaitWrite)
		if cluster.master == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a master")
			return false
		}
		if cluster.master.Conn == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a master connection")
			return false
		}
		qt, logs, err := dbhelper.CheckLongRunningWrites(cluster.master.Conn, cluster.Conf.SwitchWaitWrite)
		cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlDbg, "CheckLongRunningWrites")
		if qt > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Long updates running on master. Cannot switchover")

			return false
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Flushing tables on master %s", cluster.master.URL)
		workerFlushTable := make(chan error, 1)
		if cluster.master.DBVersion.IsMariaDB() && cluster.master.DBVersion.Major > 10 && cluster.master.DBVersion.Minor >= 1 {

			go func() {
				var err2 error
				logs, err2 = dbhelper.MariaDBFlushTablesNoLogTimeout(cluster.master.Conn, strconv.FormatInt(cluster.Conf.SwitchWaitTrx+2, 10))
				cluster.LogSQL(logs, err2, cluster.master.URL, "MasterFailover", config.LvlDbg, "MariaDBFlushTablesNoLogTimeout")
				workerFlushTable <- err2
			}()
		} else {
			go func() {
				var err2 error
				logs, err2 = dbhelper.FlushTablesNoLog(cluster.master.Conn)
				cluster.LogSQL(logs, err2, cluster.master.URL, "MasterFailover", config.LvlDbg, "FlushTablesNoLog")
				workerFlushTable <- err2
			}()

		}

		select {
		case err = <-workerFlushTable:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not flush tables on master", err)
			}
		case <-time.After(time.Second * time.Duration(cluster.Conf.SwitchWaitTrx)):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Long running trx on master at least %d, can not switchover ", cluster.Conf.SwitchWaitTrx)
			return false
		}

	} else {
		if cluster.Conf.MultiMasterGrouprep {
			// group replication auto elect a new master in case of failure do nothing
			return true
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting master failover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "------------------------")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Electing a new master")
	for _, s := range cluster.slaves {
		s.Refresh()
	}
	key := -1
	if fail {
		key = cluster.electFailoverCandidate(cluster.slaves, true)
	} else {
		key = cluster.electSwitchoverCandidate(cluster.slaves, true)
	}
	if key == -1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found")
		return false
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Slave %s has been elected as a new master", cluster.slaves[key].URL)

	if fail && !cluster.isSlaveElectable(cluster.slaves[key], true) {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Elected slave have issue cancelling failover", cluster.slaves[key].URL)
		return false
	}
	// Shuffle the server list
	var skey int
	for k, server := range cluster.Servers {
		if cluster.slaves[key].URL == server.URL {
			skey = k
			break
		}
	}
	cluster.oldMaster = cluster.master
	cluster.master = cluster.Servers[skey]
	cluster.master.SetMaster()
	if cluster.Conf.MultiMaster == false {
		cluster.slaves[key].delete(&cluster.slaves)
	}
	cluster.failoverPreScript(fail)

	// Phase 2: Reject updates and sync slaves on switchover
	if fail == false {
		cluster.oldMaster.freeze()
	}
	// Sync candidate depending on the master status.
	// If it's a switchover, use MASTER_POS_WAIT to sync.
	// If it's a failover, wait for the SQL thread to read all relay logs.
	// If maxsclale we should wait for relay catch via old style

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting for candidate master %s to apply relay log", cluster.master.URL)
	err = cluster.master.ReadAllRelayLogs()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error while reading relay logs on candidate %s: %s", cluster.master.URL, err)
	}

	//cluster.failoverCrash()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Save replication status and crash infos before opening traffic")
	ms, err := cluster.master.GetSlaveStatus(cluster.master.ReplicationSourceName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failover can not fetch replication info on new master: %s", err)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "master_log_file=%s", ms.MasterLogFile.String)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "master_log_pos=%s", ms.ReadMasterLogPos.String)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Candidate semisync %t", cluster.master.SemiSyncSlaveStatus)
	crash := new(Crash)
	crash.URL = cluster.oldMaster.URL
	crash.ElectedMasterURL = cluster.master.URL
	crash.FailoverMasterLogFile = ms.MasterLogFile.String
	crash.FailoverMasterLogPos = ms.ReadMasterLogPos.String
	crash.NewMasterLogFile = cluster.master.BinaryLogFile
	crash.NewMasterLogPos = cluster.master.BinaryLogPos
	if cluster.master.DBVersion.IsMariaDB() {
		if cluster.Conf.MxsBinlogOn {
			crash.FailoverIOGtid = cluster.master.CurrentGtid
		} else {
			crash.FailoverIOGtid = gtid.NewList(ms.GtidIOPos.String)
		}
	} else if cluster.master.DBVersion.IsMySQLOrPerconaGreater57() && cluster.master.HasGTIDReplication() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "MySQL GTID saving crash info for replication ExexecutedGtidSet %s", ms.ExecutedGtidSet.String)
		crash.FailoverIOGtid = gtid.NewMySQLList(strings.ToUpper(ms.ExecutedGtidSet.String), cluster.GetCrcTable())
	}
	cluster.master.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus
	crash.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus

	// if relay server than failover and switchover converge to a new binlog  make this happen
	var relaymaster *ServerMonitor
	if cluster.Conf.MxsBinlogOn || cluster.Conf.MultiTierSlave {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Candidate master has to catch up with relay server log position")
		relaymaster = cluster.GetRelayServer()
		if relaymaster != nil {
			rs, err := relaymaster.GetSlaveStatus(relaymaster.ReplicationSourceName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Can't find slave status on relay server %s", relaymaster.URL)
			}
			relaymaster.Refresh()

			binlogfiletoreach, _ := strconv.Atoi(strings.Split(rs.MasterLogFile.String, ".")[1])
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Relay server log pos reached %d", binlogfiletoreach)
			logs, err := dbhelper.ResetMaster(cluster.master.Conn, cluster.Conf.MasterConn, cluster.master.DBVersion)
			cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlInfo, "Reset Master on candidate Master")
			ctbinlog := 0
			for ctbinlog < binlogfiletoreach {
				ctbinlog++
				logs, err := dbhelper.FlushBinaryLogsLocal(cluster.master.Conn)
				cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlInfo, "Flush Log on new Master %d", ctbinlog)
			}
			time.Sleep(2 * time.Second)
			ms, logs, err := dbhelper.GetMasterStatus(cluster.master.Conn, cluster.master.DBVersion)
			cluster.master.FailoverMasterLogFile = ms.File
			cluster.master.FailoverMasterLogPos = "4"
			crash.FailoverMasterLogFile = ms.File
			crash.FailoverMasterLogPos = "4"
			cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlInfo, "Backing up master pos %s %s", crash.FailoverMasterLogFile, crash.FailoverMasterLogPos)

		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No relay server found")
		}
	} // end relay server

	// Phase 3: Prepare new master
	if !cluster.Conf.MultiMaster && !cluster.Conf.MultiMasterGrouprep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Stopping slave threads on new master")
		if cluster.master.DBVersion.IsMariaDB() || (cluster.master.DBVersion.IsMariaDB() == false && cluster.master.DBVersion.Minor < 7) {
			logs, err := cluster.master.StopSlave()
			cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Failed stopping slave on new master %s %s", cluster.master.URL, err)
		}
		if cluster.master.ClusterGroup.Conf.FailoverSemiSyncState {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Enable semisync leader and disable semisync replica on %s", cluster.master.URL)
			logs, err := cluster.master.SetSemiSyncLeader()
			cluster.LogSQL(logs, err, cluster.master.URL, "Rejoin", config.LvlErr, "Failed enable semisync leader and disable semisync replica on %s %s", cluster.master.URL, err)
		}
	}
	cluster.Crashes = append(cluster.Crashes, crash)
	t := time.Now()
	crash.Save(cluster.WorkingDir + "/failover." + t.Format("20060102150405") + ".json")
	crash.Purge(cluster.WorkingDir, cluster.Conf.FailoverLogFileKeep)
	cluster.Save()

	if !cluster.Conf.MultiMaster && !cluster.Conf.MultiMasterGrouprep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Resetting slave on new master and set read/write mode on")
		if cluster.master.DBVersion.IsMySQLOrPercona() {
			// Need to stop all threads to reset on MySQL
			logs, err := cluster.master.StopSlave()
			cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Failed stop slave on new master %s %s", cluster.master.URL, err)
		}

		logs, err := cluster.master.ResetSlave()
		cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Failed reset slave on new master %s %s", cluster.master.URL, err)
	}
	if fail == false {
		// Get Fresh GTID pos before open traffic
		cluster.master.Refresh()
	}
	err = cluster.master.SetReadWrite()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set new master as read-write")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover proxies")
	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverPostScript(fail)
	cluster.failoverEnableEventScheduler()
	// Insert a bogus transaction in order to have a new GTID pos on master
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Inject fake transaction on new master %s ", cluster.master.URL)
	logs, err := dbhelper.FlushTables(cluster.master.Conn)
	cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Could not flush tables on new master for fake trx %s", err)

	if fail == false {
		// Get latest GTID pos
		//cluster.master.Refresh() moved just before opening writes
		cluster.oldMaster.Refresh()

		// ********
		// Phase 4: Demote old master to slave
		// ********
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Killing new connections on old master showing before update route")
		dbhelper.KillThreads(cluster.oldMaster.Conn, cluster.oldMaster.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Switching old leader to slave")
		logs, err := dbhelper.UnlockTables(cluster.oldMaster.Conn)
		cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not unlock tables on old master %s", err)

		// Moved in freeze
		//cluster.oldMaster.StopSlave() // This is helpful in some cases the old master can have an old replication running
		one_shoot_slave_pos := false
		if cluster.oldMaster.DBVersion.IsMariaDB() && cluster.oldMaster.HaveMariaDBGTID == false && cluster.oldMaster.DBVersion.Major >= 10 && cluster.Conf.SwitchoverCopyOldLeaderGtid {
			logs, err := dbhelper.SetGTIDSlavePos(cluster.oldMaster.Conn, cluster.master.GTIDBinlogPos.Sprint())
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set old master gtid_slave_pos , reason: %s", err)
			one_shoot_slave_pos = true
		}

		cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not check old master GTID status: %s", err)
		var changeMasterErr error
		var changemasteropt dbhelper.ChangeMasterOpt
		changemasteropt.Host = cluster.master.Host
		changemasteropt.Port = cluster.master.Port
		changemasteropt.User = cluster.GetRplUser()
		changemasteropt.Password = cluster.GetRplPass()
		changemasteropt.Logfile = cluster.master.BinaryLogFile
		changemasteropt.Logpos = cluster.master.BinaryLogPos
		changemasteropt.Retry = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry)
		changemasteropt.Heartbeat = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime)
		changemasteropt.SSL = cluster.Conf.ReplicationSSL
		changemasteropt.Channel = cluster.Conf.MasterConn
		changemasteropt.IsDelayed = cluster.oldMaster.IsDelayed
		changemasteropt.Delay = strconv.Itoa(cluster.oldMaster.ClusterGroup.Conf.HostsDelayedTime)
		changemasteropt.PostgressDB = cluster.master.PostgressDB
		oldmasterneedslavestart := true
		if cluster.oldMaster.HasMariaDBGTID() == false && cluster.oldMaster.HasMySQLGTID() == false {
			changemasteropt.Mode = "POSITIONAL"
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Doing positional switch of old Master")
		} else if cluster.oldMaster.HasMySQLGTID() == true {
			// We can do MySQL 5.7 style failover
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Doing MySQL GTID switch of the old master")
			changemasteropt.Mode = "MASTER_AUTO_POSITION"
		} else if cluster.Conf.MxsBinlogOn == false {
			// current pos is needed on old master as writes diverges from slave pos
			// if gtid_slave_pos was forced use slave_pos : positional to GTID promotion
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Doing MariaDB GTID switch of the old master")
			if one_shoot_slave_pos {
				changemasteropt.Mode = "SLAVE_POS"
			} else {
				changemasteropt.Mode = "CURRENT_POS"
			}
		} else {
			// Is Maxscale
			// Don't start slave until the relay as been point to new master
			oldmasterneedslavestart = false
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pointing old master to relay server")
			if relaymaster.MxsHaveGtid {
				changemasteropt.Mode = "SLAVE_POS"
				changemasteropt.Host = relaymaster.Host
				changemasteropt.Port = relaymaster.Port
			} else {
				changemasteropt.Mode = "POSITIONAL"
				changemasteropt.Host = relaymaster.Host
				changemasteropt.Port = relaymaster.Port
				changemasteropt.Logfile = crash.FailoverMasterLogFile
				changemasteropt.Logpos = crash.FailoverMasterLogPos
			}
		}
		logs, changeMasterErr = dbhelper.ChangeMaster(cluster.oldMaster.Conn, changemasteropt, cluster.oldMaster.DBVersion)
		cluster.LogSQL(logs, changeMasterErr, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Change master failed on old master, reason:%s ", changeMasterErr)
		if oldmasterneedslavestart {
			logs, err = cluster.oldMaster.StartSlave()
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Start slave failed on old master,%s reason:  %s ", cluster.oldMaster.URL, err)
		}

		if cluster.Conf.ReadOnly {
			logs, err = cluster.oldMaster.SetReadOnly()
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set old master as read-only, %s", err)
			/*	} else {
				logs, err = cluster.oldMaster.SetReadWrite()
				cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set old master as read-write, %s", err)
			*/
		}
		if cluster.Conf.SwitchDecreaseMaxConn {

			logs, err := dbhelper.SetMaxConnections(cluster.oldMaster.Conn, cluster.oldMaster.maxConn, cluster.oldMaster.DBVersion)
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set max connection, %s", err)

		}
		// Add the old master to the slaves list

		cluster.oldMaster.SetState(stateSlave)
		if cluster.Conf.MultiMaster == false {
			cluster.slaves = append(cluster.slaves, cluster.oldMaster)
		}
	}
	// End Old Alive Leader as new replica

	// Multi source on old leader case
	cluster.FailoverExtraMultiSource(cluster.oldMaster, cluster.master, fail)

	// ********
	// Phase 5: Switch slaves to new master
	// ********

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Switching other slaves to the new master")
	for _, sl := range cluster.slaves {
		// Don't switch if slave was the old master or is in a multiple master setup or with relay server.
		if sl.URL == cluster.oldMaster.URL || sl.State == stateMaster || (sl.IsRelay == false && cluster.Conf.MxsBinlogOn == true) {
			continue
		}
		// maxscale is in the list of slave

		if fail == false && cluster.Conf.MxsBinlogOn == false && cluster.Conf.SwitchSlaveWaitCatch {
			sl.WaitSyncToMaster(cluster.oldMaster)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Change master on slave %s", sl.URL)
		logs, err = sl.StopSlave()
		cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not stop slave on server %s, %s", sl.URL, err)
		if fail == false && cluster.Conf.MxsBinlogOn == false && cluster.Conf.SwitchSlaveWaitCatch {
			if cluster.Conf.SwitchoverCopyOldLeaderGtid && sl.DBVersion.IsMariaDB() {
				logs, err := dbhelper.SetGTIDSlavePos(sl.Conn, cluster.oldMaster.GTIDBinlogPos.Sprint())
				cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
			}
		}

		var changeMasterErr error

		var changemasteropt dbhelper.ChangeMasterOpt
		changemasteropt.Host = cluster.master.Host
		changemasteropt.Port = cluster.master.Port
		changemasteropt.User = cluster.GetRplUser()
		changemasteropt.Password = cluster.GetRplPass()
		changemasteropt.Retry = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry)
		changemasteropt.Heartbeat = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime)
		changemasteropt.SSL = cluster.Conf.ReplicationSSL
		changemasteropt.Channel = cluster.Conf.MasterConn
		changemasteropt.IsDelayed = sl.IsDelayed
		changemasteropt.Delay = strconv.Itoa(sl.ClusterGroup.Conf.HostsDelayedTime)
		changemasteropt.PostgressDB = cluster.master.PostgressDB

		// Not MariaDB and not using MySQL GTID, 2.0 stop doing any thing until pseudo GTID
		if sl.HasMariaDBGTID() == false && cluster.master.HasMySQLGTID() == false {

			if cluster.Conf.AutorejoinSlavePositionalHeartbeat == true {

				pseudoGTID, logs, err := sl.GetLastPseudoGTID()
				cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not get pseudoGTID on slave %s, %s", sl.URL, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found pseudoGTID %s", pseudoGTID)
				slFile, slPos, logs, err := sl.GetBinlogPosFromPseudoGTID(pseudoGTID)
				cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not find pseudoGTID in slave %s, %s", sl.URL, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found Coordinates on slave %s, %s", slFile, slPos)
				slSkip, logs, err := sl.GetNumberOfEventsAfterPos(slFile, slPos)
				cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not find number of events after pseudoGTID in slave %s, %s", sl.URL, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found %d events to skip after coordinates on slave %s,%s", slSkip, slFile, slPos)

				mFile, mPos, logs, err := cluster.master.GetBinlogPosFromPseudoGTID(pseudoGTID)
				cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Could not find pseudoGTID in master %s, %s", cluster.master.URL, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found coordinate on master %s ,%s", mFile, mPos)
				mFile, mPos, logs, err = cluster.master.GetBinlogPosAfterSkipNumberOfEvents(mFile, mPos, slSkip)
				cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Could not skip event after pseudoGTID in master %s, %s", cluster.master.URL, err)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found skip coordinate on master %s, %s", mFile, mPos)

				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Doing Positional switch of slave %s", sl.URL)
				changemasteropt.Logfile = mFile
				changemasteropt.Logpos = mPos
				changemasteropt.Mode = "POSITIONAL"
				logs, changeMasterErr = dbhelper.ChangeMaster(sl.Conn, changemasteropt, sl.DBVersion)
			} else {
				sl.SetMaintenance()
			}
			// do nothing stay connected to dead master proceed with relay fix later

		} else if cluster.oldMaster.DBVersion.IsMySQLOrPerconaGreater57() && cluster.master.HasMySQLGTID() == true {
			logs, changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
				Host:        cluster.master.Host,
				Port:        cluster.master.Port,
				User:        cluster.GetRplUser(),
				Password:    cluster.GetRplPass(),
				Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
				Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
				Mode:        "MASTER_AUTO_POSITION",
				SSL:         cluster.Conf.ReplicationSSL,
				Channel:     cluster.Conf.MasterConn,
				IsDelayed:   sl.IsDelayed,
				Delay:       strconv.Itoa(sl.ClusterGroup.Conf.HostsDelayedTime),
				PostgressDB: cluster.master.PostgressDB,
			}, sl.DBVersion)
		} else if cluster.Conf.MxsBinlogOn == false {
			//MariaDB all cases use GTID

			logs, changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
				Host:        cluster.master.Host,
				Port:        cluster.master.Port,
				User:        cluster.GetRplUser(),
				Password:    cluster.GetRplPass(),
				Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
				Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
				Mode:        "SLAVE_POS",
				SSL:         cluster.Conf.ReplicationSSL,
				Channel:     cluster.Conf.MasterConn,
				IsDelayed:   sl.IsDelayed,
				Delay:       strconv.Itoa(sl.ClusterGroup.Conf.HostsDelayedTime),
				PostgressDB: cluster.master.PostgressDB,
			}, sl.DBVersion)
		} else { // We deduct we are in maxscale binlog server , but can have support for GTID or not

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Pointing relay to the new master: %s:%s", cluster.master.Host, cluster.master.Port)
			if sl.MxsHaveGtid {
				logs, changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
					Host:        cluster.master.Host,
					Port:        cluster.master.Port,
					User:        cluster.GetRplUser(),
					Password:    cluster.GetRplPass(),
					Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
					Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
					Mode:        "SLAVE_POS",
					SSL:         cluster.Conf.ReplicationSSL,
					Channel:     cluster.Conf.MasterConn,
					IsDelayed:   sl.IsDelayed,
					Delay:       strconv.Itoa(sl.ClusterGroup.Conf.HostsDelayedTime),
					PostgressDB: cluster.master.PostgressDB,
				}, sl.DBVersion)
			} else {
				logs, changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
					Host:      cluster.master.Host,
					Port:      cluster.master.Port,
					User:      cluster.GetRplUser(),
					Password:  cluster.GetRplPass(),
					Retry:     strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
					Heartbeat: strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
					Mode:      "MXS",
					SSL:       cluster.Conf.ReplicationSSL,
				}, sl.DBVersion)
			}
		}
		cluster.LogSQL(logs, changeMasterErr, sl.URL, "MasterFailover", config.LvlErr, "Change master failed on slave %s, %s", sl.URL, changeMasterErr)
		logs, err = sl.StartSlave()
		cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not start slave on server %s, %s", sl.URL, err)
		// now start the old master as relay is ready
		if cluster.Conf.MxsBinlogOn && fail == false {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Restarting old master replication relay server ready")
			cluster.oldMaster.StartSlave()
		}
		if cluster.Conf.ReadOnly && cluster.Conf.MxsBinlogOn == false && !sl.IsIgnoredReadonly() {
			logs, err = sl.SetReadOnly()
			cluster.LogSQL(logs, err, sl.URL, "MasterFailover", config.LvlErr, "Could not set slave %s as read-only, %s", sl.URL, err)
		} else {
			if cluster.Conf.MxsBinlogOn == false {
				err = sl.SetReadWrite()
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not remove slave %s as read-only, %s", sl.URL, err)
				}
			}
		}
	}
	// if consul or internal proxy need to adapt read only route to new slaves
	cluster.backendStateChangeProxies()

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	if fail == true {
		cluster.FailoverCtr++
		cluster.FailoverTs = time.Now().Unix()
	}

	// Not a prefered master this code is not default
	// such code is to dangerous documentation is needed
	/*	if cluster.Conf.FailoverSwitchToPrefered && fail == true && cluster.Conf.PrefMaster != "" && !cluster.master.IsPrefered() {
		prm := cluster.foundPreferedMaster(cluster.slaves)
		if prm != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Switchover after failover not on a prefered leader after failover")
			cluster.MasterFailover(false)
		}
	}*/

	return true
}

func (cluster *Cluster) failoverProxiesWaitMonitor() {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting %ds for unmanaged proxy to monitor route change", cluster.Conf.SwitchSlaveWaitRouteChange)
	time.Sleep(time.Duration(cluster.Conf.SwitchSlaveWaitRouteChange) * time.Second)
}

func (cluster *Cluster) failoverEnableEventScheduler() {

	if cluster.Conf.FailEventScheduler {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Enable Event Scheduler on the new master")
		logs, err := cluster.master.SetEventScheduler(true)
		cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Could not enable event scheduler on the new master")
	}
	if cluster.Conf.FailEventStatus {
		for _, v := range cluster.master.EventStatus {
			if v.Status == 3 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set ENABLE for event %s %s on new master", v.Db, v.Name)
				logs, err := dbhelper.SetEventStatus(cluster.master.Conn, v, 1)
				cluster.LogSQL(logs, err, cluster.master.URL, "MasterFailover", config.LvlErr, "Could not Set ENABLE for event %s %s on new master", v.Db, v.Name)
			}
		}
	}
}

// FailoverExtraMultiSource care of master extra muti source replications
func (cluster *Cluster) FailoverExtraMultiSource(oldMaster *ServerMonitor, NewMaster *ServerMonitor, fail bool) error {

	for _, rep := range oldMaster.Replications {

		if rep.ConnectionName.String != cluster.Conf.MasterConn {
			myparentrplpassword := ""
			parentCluster := cluster.GetParentClusterFromReplicationSource(rep)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Failover replication source %s ", rep.ConnectionName.String)
			// need a way to found parent replication password
			if parentCluster != nil {
				myparentrplpassword = parentCluster.GetRplPass()
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Unable to found a monitored cluster for replication source %s ", rep.ConnectionName.String)
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Moving source %s with empty password to preserve replication stream on new master", rep.ConnectionName.String)
			}

			var changemasteropt dbhelper.ChangeMasterOpt
			changemasteropt.Host = rep.MasterHost.String
			changemasteropt.Port = rep.MasterPort.String
			changemasteropt.User = rep.MasterUser.String
			changemasteropt.Password = myparentrplpassword
			changemasteropt.Retry = strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry)
			changemasteropt.Heartbeat = strconv.Itoa(int(rep.SlaveHeartbeatPeriod))
			changemasteropt.Logfile = rep.MasterLogFile.String
			changemasteropt.Logpos = rep.ExecMasterLogPos.String
			changemasteropt.SSL = cluster.Conf.ReplicationSSL
			changemasteropt.Channel = rep.ConnectionName.String
			changemasteropt.IsDelayed = false
			changemasteropt.Delay = "0"
			changemasteropt.DoDomainIds = rep.DoDomainIds.String
			changemasteropt.IgnoreDomainIds = rep.IgnoreDomainIds.String
			changemasteropt.IgnoreServerIds = rep.IgnoreServerIds.String
			changemasteropt.PostgressDB = NewMaster.PostgressDB
			if strings.ToUpper(rep.UsingGtid.String) == "NO" {
				changemasteropt.Mode = "POSITIONAL"
			} else {
				if strings.ToUpper(rep.UsingGtid.String) == "SLAVE_POS" || strings.ToUpper(rep.UsingGtid.String) == "CURRENT_POS" {
					changemasteropt.Mode = strings.ToUpper(rep.UsingGtid.String)

				} else if rep.RetrievedGtidSet.Valid && rep.ExecutedGtidSet.String != "" {
					changemasteropt.Mode = "MASTER_AUTO_POSITION"
				}
			}
			logs, err := dbhelper.ChangeMaster(NewMaster.Conn, changemasteropt, NewMaster.DBVersion)
			cluster.LogSQL(logs, err, NewMaster.URL, "MasterFailover", config.LvlErr, "Change master failed on slave %s, %s", NewMaster.URL, err)
			if fail == false && err == nil {
				logs, err := dbhelper.ResetSlave(oldMaster.Conn, true, rep.ConnectionName.String, oldMaster.DBVersion)
				cluster.LogSQL(logs, err, oldMaster.URL, "MasterFailover", config.LvlErr, "Reset replication source %s failed on %s, %s", rep.ConnectionName.String, oldMaster.URL, err)
			}
			logs, err = dbhelper.StartSlave(NewMaster.Conn, rep.ConnectionName.String, NewMaster.DBVersion)
			cluster.LogSQL(logs, err, NewMaster.URL, "MasterFailover", config.LvlErr, "Start replication source %s failed on %s, %s", rep.ConnectionName.String, NewMaster.URL, err)

		}
	}
	return nil
}

func (cluster *Cluster) electSwitchoverGroupReplicationCandidate(l []*ServerMonitor, forcingLog bool) int {
	//	Return prefered if exists
	for i, sl := range l {
		if cluster.IsInPreferedHosts(sl) {
			// if (cluster.Conf.LogLevel > 1 || forcingLog) && cluster.IsInFailover() {
			if cluster.IsInFailover() {
				cluster.LogModulePrintf(forcingLog, config.ConstLogModGeneral, config.LvlDbg, "Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
	}
	//	Return one not ignored not full , not prefered
	for i, sl := range l {
		if sl.IsIgnored() {
			cluster.StateMachine.AddState("ERR00037", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00037"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		if cluster.IsInPreferedHosts(sl) {
			continue
		}
		if sl.IsFull {
			continue
		}
		return i
	}
	return -1
}

// Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate.
func (cluster *Cluster) electSwitchoverCandidate(l []*ServerMonitor, forcingLog bool) int {
	ll := len(l)
	seqList := make([]uint64, ll)
	posList := make([]uint64, ll)
	hipos := 0
	hiseq := 0
	var max uint64
	var maxpos uint64

	for i, sl := range l {

		/* If server is in the ignore list, do not elect it in switchover */
		if sl.IsIgnored() {
			cluster.StateMachine.AddState("ERR00037", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00037"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		if sl.IsFull {
			continue
		}
		//Need comment//
		if sl.IsRelay {
			cluster.StateMachine.AddState("ERR00036", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00036"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		if !sl.HasBinlog() && !sl.IsIgnored() {
			cluster.SetState("ERR00013", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00013"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			continue
		}
		if cluster.Conf.MultiMaster == true && sl.State == stateMaster {
			cluster.StateMachine.AddState("ERR00035", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00035"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}

		// The tests below should run only in case of a switchover as they require the master to be up.

		if cluster.isSlaveElectableForSwitchover(sl, forcingLog) == false {
			cluster.StateMachine.AddState("ERR00034", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00034"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		/* binlog + ping  */
		if cluster.isSlaveElectable(sl, forcingLog) == false {
			cluster.StateMachine.AddState("ERR00039", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00039"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}

		/* Rig the election if the examined slave is preferred candidate master in switchover */
		if cluster.IsInPreferedHosts(sl) {
			// if (cluster.Conf.LogLevel > 1 || forcingLog) && cluster.IsInFailover() {
			if cluster.IsInFailover() {
				cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlDbg, "Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
		if sl.HaveNoMasterOnStart == true && cluster.Conf.FailRestartUnsafe == false {
			cluster.StateMachine.AddState("ERR00084", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00084"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		ss, errss := sl.GetSlaveStatus(sl.ReplicationSourceName)
		// not a slave
		if errss != nil && cluster.Conf.FailRestartUnsafe == false {
			//Skip slave in election %s have no master log file, slave might have failed
			cluster.StateMachine.AddState("ERR00033", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00033"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		// Fake position if none as new slave
		filepos := "1"
		logfile := "master.000001"
		if errss == nil {
			filepos = ss.ReadMasterLogPos.String
			logfile = ss.MasterLogFile.String
		}
		if strings.Contains(logfile, ".") == false {
			continue
		}
		for len(filepos) < 12 {
			filepos = "0" + filepos
		}

		pos := strings.Split(logfile, ".")[1] + filepos
		binlogposreach, _ := strconv.ParseUint(pos, 10, 64)

		posList[i] = binlogposreach

		seqnos := gtid.NewList("1-1-1").GetSeqNos()

		if errss == nil {
			if cluster.master.State != stateFailed {
				//	seqnos = sl.SlaveGtid.GetSeqNos()
				seqnos = sl.SlaveGtid.GetSeqDomainIdNos(cluster.master.DomainID)
			} else {
				seqnos = gtid.NewList(ss.GtidIOPos.String).GetSeqDomainIdNos(cluster.master.DomainID)
			}
		}

		for _, v := range seqnos {
			seqList[i] += v
		}
		if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
		if posList[i] > maxpos {
			maxpos = posList[i]
			hipos = i
		}

	} //end loop all slaves
	if max > 0 {
		/* Return key of slave with the highest seqno. */
		return hiseq
	}
	if maxpos > 0 {
		/* Return key of slave with the highest pos. */
		return hipos
	}
	return -1
}

// electFailoverCandidate found the most up to date and look after a possibility to failover on it
func (cluster *Cluster) electFailoverCandidate(l []*ServerMonitor, forcingLog bool) int {

	ll := len(l)
	seqList := make([]uint64, ll)
	posList := make([]uint64, ll)

	var maxseq uint64
	var maxpos uint64
	type Trackpos struct {
		URL                string
		Indice             int
		Pos                uint64
		Seq                uint64
		Prefered           bool
		Ignoredconf        bool
		Ignoredrelay       bool
		Ignoredmultimaster bool
		Ignoredreplication bool
		Weight             uint
		DelayStat          DelayStat
	}

	// HaveOneValidReader is used to state that at least one replicat is available for reading via proxies
	// In such case it is needed to add the leader in the reader server list
	// To avoid oveloading the leader with to many read we ignore replication delay and IO thread status
	HaveOneValidReader := false

	trackposList := make([]Trackpos, ll)
	for i, sl := range l {
		trackposList[i].URL = sl.URL
		trackposList[i].Indice = i
		trackposList[i].Prefered = sl.IsPrefered()
		trackposList[i].Ignoredconf = sl.IsIgnored()
		trackposList[i].Ignoredrelay = sl.IsRelay
		trackposList[i].DelayStat = sl.DelayStat.Total

		//Need comment//
		if sl.IsRelay {
			cluster.StateMachine.AddState("ERR00036", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00036"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			continue
		}
		if sl.IsFull {
			continue
		}
		if cluster.Conf.MultiMaster == true && sl.State == stateMaster {
			cluster.StateMachine.AddState("ERR00035", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00035"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			trackposList[i].Ignoredmultimaster = true
			continue
		}
		if sl.HaveNoMasterOnStart == true && cluster.Conf.FailRestartUnsafe == false {
			cluster.StateMachine.AddState("ERR00084", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00084"], sl.URL), ServerUrl: sl.URL, ErrFrom: "CHECK"})
			continue
		}
		if !sl.HasBinlog() && !sl.IsIgnored() {
			cluster.SetState("ERR00013", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00013"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			continue
		}
		if cluster.GetTopology() == topoMultiMasterWsrep && cluster.vmaster != nil {
			if cluster.vmaster.URL == sl.URL {
				continue
			} else if sl.State == stateWsrep {
				return i
			} else {
				continue
			}
		}
		if cluster.master == nil {
			continue
		}

		ss, errss := sl.GetSlaveStatus(sl.ReplicationSourceName)
		// not a slave
		if errss != nil && cluster.Conf.FailRestartUnsafe == false {
			cluster.StateMachine.AddState("ERR00033", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00033"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			trackposList[i].Ignoredreplication = true
			continue
		}
		trackposList[i].Ignoredreplication = !cluster.isSlaveElectable(sl, false)
		if !HaveOneValidReader {
			HaveOneValidReader = cluster.isSlaveValidReader(sl, false)
		}
		// Fake position if none as new slave
		filepos := "1"
		logfile := "master.000001"
		if errss == nil {
			filepos = ss.ReadMasterLogPos.String
			logfile = ss.MasterLogFile.String
		}
		if strings.Contains(logfile, ".") == false {
			continue
		}
		for len(filepos) < 12 {
			filepos = "0" + filepos
		}

		pos := strings.Split(logfile, ".")[1] + filepos
		binlogposreach, _ := strconv.ParseUint(pos, 10, 64)

		posList[i] = binlogposreach
		trackposList[i].Pos = binlogposreach

		seqnos := gtid.NewList("1-1-1").GetSeqNos()

		if errss == nil {
			if cluster.master.State != stateFailed {
				// Need MySQL GTID support
				seqnos = sl.SlaveGtid.GetSeqDomainIdNos(cluster.master.DomainID)
			} else {
				seqnos = gtid.NewList(ss.GtidIOPos.String).GetSeqDomainIdNos(cluster.master.DomainID)
			}
		}

		for _, v := range seqnos {
			seqList[i] += v
		}
		trackposList[i].Seq = seqList[i]
		if seqList[i] > maxseq {
			maxseq = seqList[i]

		}
		if posList[i] > maxpos {
			maxpos = posList[i]

		}

	} //end loop all slaves

	if !HaveOneValidReader {
		cluster.StateMachine.AddState("ERR00085", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["ERR00085"]), ErrFrom: "CHECK"})
	}

	if !cluster.Conf.FailoverCheckDelayStat {
		sort.Slice(trackposList[:], func(i, j int) bool {
			return trackposList[i].Seq > trackposList[j].Seq
		})
	} else {
		sort.Slice(trackposList[:], func(i, j int) bool {
			if trackposList[i].Seq != trackposList[j].Seq {
				return trackposList[i].Seq > trackposList[j].Seq
			}
			if trackposList[i].DelayStat.SlaveErrCount != trackposList[j].DelayStat.SlaveErrCount {
				return trackposList[i].DelayStat.SlaveErrCount < trackposList[j].DelayStat.SlaveErrCount
			}
			if trackposList[i].DelayStat.DelayCount != trackposList[j].DelayStat.DelayCount {
				return trackposList[i].DelayStat.DelayCount < trackposList[j].DelayStat.DelayCount
			}
			if trackposList[i].DelayStat.DelayAvg != trackposList[j].DelayStat.DelayAvg {
				return trackposList[i].DelayStat.DelayAvg < trackposList[j].DelayStat.DelayAvg
			}
			return trackposList[i].DelayStat.Counter > trackposList[j].DelayStat.Counter
		})
	}
	if forcingLog {
		data, _ := json.MarshalIndent(trackposList, "", "\t")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Election matrice: %s ", data)
	}

	if maxseq > 0 {
		/* Return key of slave with the highest seqno. */

		//send the prefered if equal max
		for _, p := range trackposList {
			if p.Seq == maxseq && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == false && p.Prefered == true {
				return p.Indice
			}
		}
		//send one with maxseq
		for _, p := range trackposList {
			if p.Seq == maxseq && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == false {
				return p.Indice
			}
		}
		//send one with maxseq but also ignored
		for _, p := range trackposList {
			if p.Seq == maxseq && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == true {
				if forcingLog {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlInfo, "Ignored server is the most up to date ")
				}
				return p.Indice
			}

		}

		data, _ := json.MarshalIndent(trackposList, "", "\t")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlDbg, "Election matrice maxseq >0: %s ", data)
		return -1
	}

	if !cluster.Conf.FailoverCheckDelayStat {
		sort.Slice(trackposList[:], func(i, j int) bool {
			return trackposList[i].Pos > trackposList[j].Pos
		})
	} else {
		sort.Slice(trackposList[:], func(i, j int) bool {
			if trackposList[i].Pos != trackposList[j].Pos {
				return trackposList[i].Pos > trackposList[j].Pos
			}
			if trackposList[i].DelayStat.SlaveErrCount != trackposList[j].DelayStat.SlaveErrCount {
				return trackposList[i].DelayStat.SlaveErrCount < trackposList[j].DelayStat.SlaveErrCount
			}
			if trackposList[i].DelayStat.DelayCount != trackposList[j].DelayStat.DelayCount {
				return trackposList[i].DelayStat.DelayCount < trackposList[j].DelayStat.DelayCount
			}
			if trackposList[i].DelayStat.DelayAvg != trackposList[j].DelayStat.DelayAvg {
				return trackposList[i].DelayStat.DelayAvg < trackposList[j].DelayStat.DelayAvg
			}
			return trackposList[i].DelayStat.Counter > trackposList[j].DelayStat.Counter
		})
	}

	if maxpos > 0 {
		/* Return key of slave with the highest pos. */
		for _, p := range trackposList {
			if p.Pos == maxpos && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == false && p.Prefered == true {
				return p.Indice
			}
		}
		//send one with maxpos
		for _, p := range trackposList {
			if p.Pos == maxpos && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == false {
				return p.Indice
			}
		}
		//send one with maxpos and ignored
		for _, p := range trackposList {
			if p.Pos == maxpos && p.Ignoredrelay == false && p.Ignoredmultimaster == false && p.Ignoredreplication == false && p.Ignoredconf == true {
				if forcingLog {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlInfo, "Ignored server is the most up to date ")
				}
				return p.Indice
			}
		}

		data, _ := json.MarshalIndent(trackposList, "", "\t")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlDbg, "Election matrice maxpos>0: %s ", data)
		return -1
	}

	data, _ := json.MarshalIndent(trackposList, "", "\t")
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlDbg, "Election matrice: %s ", data)
	return -1
}

func (cluster *Cluster) isSlaveElectable(sl *ServerMonitor, forcingLog bool) bool {
	ss, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModFailedElection, config.LvlWarn, "Error in getting slave status in testing slave electable %s: %s  ", sl.URL, err)
		return false
	}
	//if master is alived and IO Thread stops then not a good candidate and not forced
	if ss.SlaveIORunning.String == "No" && cluster.Conf.RplChecks && !cluster.IsMasterFailed() {
		cluster.StateMachine.AddState("ERR00087", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00087"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Unsafe failover condition. Slave %s IO Thread is stopped %s. Skipping", sl.URL, ss.LastIOError.String)
		// }
		return false
	}

	/* binlog + ping  */
	if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host, sl.DBVersion) == false {
		cluster.StateMachine.AddState("ERR00040", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00040"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Slave %s does not ping or has no binlogs. Skipping", sl.URL)
		// }
		return false
	}
	if sl.IsMaintenance {
		cluster.StateMachine.AddState("ERR00047", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00047"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Slave %s is in maintenance. Skipping", sl.URL)
		// }
		return false
	}

	if ss.SecondsBehindMaster.Int64 > cluster.Conf.FailMaxDelay && cluster.Conf.FailMaxDelay != -1 && cluster.Conf.RplChecks == true {
		cluster.StateMachine.AddState("ERR00041", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00041"]+" Sql: "+sl.GetProcessListReplicationLongQuery(), sl.URL, cluster.Conf.FailMaxDelay, ss.SecondsBehindMaster.Int64), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Unsafe failover condition. Slave %s has more than failover-max-delay %d seconds with replication delay %d. Skipping", sl.URL, cluster.Conf.FailMaxDelay, ss.SecondsBehindMaster.Int64)
		// }

		return false
	}

	if ss.SlaveSQLRunning.String == "No" && cluster.Conf.RplChecks {
		cluster.StateMachine.AddState("ERR00042", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00042"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Unsafe failover condition. Slave %s SQL Thread is stopped. Skipping", sl.URL)
		// }
		return false
	}

	//if master is alived and connection issues, we have to refetch password from vault
	if ss.SlaveIORunning.String == "Connecting" && !cluster.IsMasterFailed() {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlDbg, "isSlaveElect lastIOErrno: %s", ss.LastIOErrno.String)
		if ss.LastIOErrno.String == "1045" {
			cluster.StateMachine.AddState("ERR00088", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00088"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
			sl.SetReplicationCredentialsRotation(ss)
		}
	}

	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.Conf.FailSync && cluster.Conf.RplChecks {
		cluster.StateMachine.AddState("ERR00043", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00043"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		// }
		return false
	}
	if sl.IsIgnored() {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Slave is in ignored list %s", sl.URL)
		// }
		return false
	}
	return true
}

func (cluster *Cluster) isSlaveValidReader(sl *ServerMonitor, forcingLog bool) bool {
	ss, err := sl.GetSlaveStatus(sl.ReplicationSourceName)
	if err != nil {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Error in getting slave status in testing slave electable %s: %s  ", sl.URL, err)
		return false
	}

	if sl.IsMaintenance {
		cluster.StateMachine.AddState("ERR00047", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00047"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Slave %s is in maintenance. Skipping", sl.URL)
		// }
		return false
	}

	/*if ss.SecondsBehindMaster.Int64 > cluster.Conf.FailMaxDelay && cluster.Conf.FailMaxDelay != -1  {
		cluster.StateMachine.AddState("ERR00041", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00041"]+" Sql: "+sl.GetProcessListReplicationLongQuery(), sl.URL, cluster.Conf.FailMaxDelay, ss.SecondsBehindMaster.Int64), ErrFrom: "CHECK", ServerUrl: sl.URL})
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogModulePrintf(forcingLog, config.ConstLogModGeneral,LvlWarn, "Unsafe failover condition. Slave %s has more than failover-max-delay %d seconds with replication delay %d. Skipping", sl.URL, cluster.Conf.FailMaxDelay, ss.SecondsBehindMaster.Int64)
		}

		return false
	}
	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.Conf.FailSync && cluster.Conf.RplChecks {
		cluster.StateMachine.AddState("ERR00043", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00043"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogModulePrintf(forcingLog, config.ConstLogModGeneral,LvlWarn, "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		}
		return false
	}
	*/
	if ss.SlaveSQLRunning.String == "No" {
		cluster.StateMachine.AddState("ERR00042", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00042"], sl.URL), ErrFrom: "CHECK", ServerUrl: sl.URL})
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Unsafe failover condition. Slave %s SQL Thread is stopped. Skipping", sl.URL)
		// }
		return false
	}
	if sl.IsIgnored() {
		// if cluster.Conf.LogLevel > 1 || forcingLog {
		cluster.LogModulePrintf(forcingLog, config.ConstLogModFailedElection, config.LvlWarn, "Slave is in ignored list %s", sl.URL)
		// }
		return false
	}
	return true
}

func (cluster *Cluster) foundPreferedMaster(l []*ServerMonitor) *ServerMonitor {
	for _, sl := range l {
		if strings.Contains(cluster.Conf.PrefMaster, sl.URL) && cluster.master.State != stateFailed {
			return sl
		}
	}
	return nil
}

// VMasterFailover triggers a leader change and returns the new master URL when all possible leader multimaster ring or galera
func (cluster *Cluster) VMasterFailover(fail bool) bool {
	if cluster.IsInFailover() {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cancel already in failover")
		return false
	}

	cluster.StateMachine.SetFailoverState()
	defer cluster.StateMachine.RemoveFailoverState()
	// Phase 1: Cleanup and election
	var err error
	cluster.oldMaster = cluster.vmaster
	if fail == false {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "----------------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting virtual master switchover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "----------------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checking long running updates on virtual master %d", cluster.Conf.SwitchWaitWrite)
		if cluster.vmaster == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a virtual master")
			return false
		}
		if cluster.vmaster.Conn == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Cannot switchover without a vmaster connection")
			return false
		}
		qt, logs, err := dbhelper.CheckLongRunningWrites(cluster.vmaster.Conn, cluster.Conf.SwitchWaitWrite)
		cluster.LogSQL(logs, err, cluster.vmaster.URL, "MasterFailover", config.LvlDbg, "CheckLongRunningWrites")
		if qt > 0 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Long updates running on virtual master. Cannot switchover")

			return false
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Flushing tables on virtual master %s", cluster.vmaster.URL)
		workerFlushTable := make(chan error, 1)

		go func() {
			var err2 error
			logs, err2 = dbhelper.FlushTablesNoLog(cluster.vmaster.Conn)
			cluster.LogSQL(logs, err, cluster.vmaster.URL, "MasterFailover", config.LvlDbg, "FlushTablesNoLog")

			workerFlushTable <- err2
		}()
		select {
		case err = <-workerFlushTable:
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "Could not flush tables on master", err)
			}
		case <-time.After(time.Second * time.Duration(cluster.Conf.SwitchWaitTrx)):
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Long running trx on master at least %d, can not switchover ", cluster.Conf.SwitchWaitTrx)
			return false
		}
		cluster.master = cluster.vmaster
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-------------------------------")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting virtual master failover")
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "-------------------------------")
		cluster.oldMaster = cluster.master
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Electing a new virtual master")
	for _, s := range cluster.slaves {
		s.Refresh()
	}
	key := -1
	if cluster.GetTopology() != topoMultiMasterWsrep && cluster.GetTopology() != topoMultiMasterGrouprep {
		key = cluster.electVirtualCandidate(cluster.oldMaster, true)
	} else {
		if cluster.Conf.MultiMasterGrouprep {
			key = cluster.electSwitchoverGroupReplicationCandidate(cluster.slaves, true)
		} else {
			key = cluster.electFailoverCandidate(cluster.slaves, true)
		}

	}
	if key == -1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No candidates found")
		return false
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s has been elected as a new master", cluster.slaves[key].URL)

	// Shuffle the server list

	var skey int
	for k, server := range cluster.Servers {
		if cluster.slaves[key].URL == server.URL {
			skey = k
			break
		}
	}
	cluster.vmaster = cluster.Servers[skey]
	cluster.master = cluster.Servers[skey]
	cluster.failoverPreScript(fail)

	// Phase 2: Reject updates and sync slaves on switchover
	if fail == false && cluster.GetTopology() != topoMultiMasterWsrep {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Rejecting updates on %s (old master)", cluster.oldMaster.URL)
		cluster.oldMaster.freeze()
	}
	if !fail && cluster.Conf.MultiMasterGrouprep {
		result, errswitch := cluster.slaves[key].SetGroupReplicationPrimary()

		if errswitch == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s elected as new leader %s", cluster.slaves[key].URL, result)

		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server %s failed elected as new leader %s", cluster.slaves[key].URL, result)

	}
	// Failover for ring
	if cluster.GetTopology() != topoMultiMasterWsrep && cluster.GetTopology() != topoMultiMasterGrouprep {
		// Sync candidate depending on the master status.
		// If it's a switchover, use MASTER_POS_WAIT to sync.
		// If it's a failover, wait for the SQL thread to read all relay logs.
		// If maxsclale we should wait for relay catch via old style

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting for candidate master to apply relay log")
		err = cluster.master.ReadAllRelayLogs()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error while reading relay logs on candidate: %s", err)
		}

		crash := new(Crash)
		crash.URL = cluster.oldMaster.URL
		crash.ElectedMasterURL = cluster.master.URL
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Save replication status before electing")
		ms, err := cluster.master.GetSlaveStatus(cluster.master.ReplicationSourceName)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Faiover can not fetch replication info on new master: %s", err)
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "master_log_file=%s", ms.MasterLogFile.String)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "master_log_pos=%s", ms.ReadMasterLogPos.String)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Candidate was in sync=%t", cluster.master.SemiSyncSlaveStatus)
		//		cluster.master.FailoverMasterLogFile = cluster.master.MasterLogFile
		//		cluster.master.FailoverMasterLogPos = cluster.master.MasterLogPos
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO ", "Save crash information")

		crash.FailoverMasterLogFile = ms.MasterLogFile.String
		crash.FailoverMasterLogPos = ms.ReadMasterLogPos.String
		if cluster.master.DBVersion.IsMariaDB() {
			if cluster.Conf.MxsBinlogOn {
				//	cluster.master.FailoverIOGtid = cluster.master.CurrentGtid
				crash.FailoverIOGtid = cluster.master.CurrentGtid
			} else {
				//	cluster.master.FailoverIOGtid = gtid.NewList(ms.GtidIOPos.String)
				crash.FailoverIOGtid = gtid.NewList(ms.GtidIOPos.String)
			}
		} else if cluster.master.DBVersion.IsMySQLOrPerconaGreater57() && cluster.master.HasGTIDReplication() {
			crash.FailoverIOGtid = gtid.NewMySQLList(strings.ToUpper(ms.ExecutedGtidSet.String), cluster.GetCrcTable())
		}
		cluster.master.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus
		crash.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus
		cluster.Crashes = append(cluster.Crashes, crash)
		cluster.Save()
		t := time.Now()
		crash.Save(cluster.WorkingDir + "/failover." + t.Format("20060102150405") + ".json")
		crash.Purge(cluster.WorkingDir, cluster.Conf.FailoverLogFileKeep)
	}

	// Phase 3: Prepare new master

	err = cluster.master.SetReadWrite()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set new master as read-write %s", err)
	}
	// Call post-failover script before unlocking the old master.
	cluster.failoverProxies()
	cluster.failoverProxiesWaitMonitor()
	cluster.failoverEnableEventScheduler()
	cluster.failoverPostScript(fail)
	if cluster.Conf.FailEventStatus {
		for _, v := range cluster.master.EventStatus {
			if v.Status == 3 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set ENABLE for event %s %s on new master", v.Db, v.Name)
				logs, err := dbhelper.SetEventStatus(cluster.vmaster.Conn, v, 1)
				cluster.LogSQL(logs, err, cluster.vmaster.URL, "MasterFailover", config.LvlErr, "Could not Set ENABLE for event %s %s on new master", v.Db, v.Name)
			}
		}
	}

	if fail == false {
		// Get latest GTID pos
		cluster.oldMaster.Refresh()

		// ********
		// Phase 4: Demote old master to slave
		// ********
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Switching old master as a slave")
		logs, err := dbhelper.UnlockTables(cluster.oldMaster.Conn)
		cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not unlock tables on old master %s", err)

		if cluster.Conf.ReadOnly {

			logs, err = cluster.oldMaster.SetReadOnly()
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set old master as read-only, %s", err)

		} else {
			err = cluster.oldMaster.SetReadWrite()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not set old master as read-write, %s", err)
			}
		}
		// Galara does not freeze old master because of bug https://jira.mariadb.org/browse/MDEV-9134
		if cluster.Conf.SwitchDecreaseMaxConn && cluster.GetTopology() != topoMultiMasterWsrep {
			logs, err := dbhelper.SetMaxConnections(cluster.oldMaster.Conn, cluster.oldMaster.maxConn, cluster.oldMaster.DBVersion)
			cluster.LogSQL(logs, err, cluster.oldMaster.URL, "MasterFailover", config.LvlErr, "Could not set max connections on %s %s", cluster.oldMaster.URL, err)
		}
		// Add the old master to the slaves list
		if cluster.Conf.MultiMasterGrouprep {
			cluster.oldMaster.SetState(stateSlave)
			cluster.slaves = append(cluster.slaves, cluster.oldMaster)
		}
	}
	if cluster.GetTopology() == topoMultiMasterRing {
		// ********
		// Phase 5: Closing loop
		// ********
		cluster.CloseRing(cluster.oldMaster)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Virtual Master switch on %s complete", cluster.vmaster.URL)
	cluster.vmaster.FailCount = 0
	if fail == true {
		cluster.FailoverCtr++
		cluster.FailoverTs = time.Now().Unix()
	}
	cluster.master = nil

	return true
}

func (cluster *Cluster) electVirtualCandidate(oldMaster *ServerMonitor, forcingLog bool) int {

	for i, sl := range cluster.Servers {
		/* If server is in the ignore list, do not elect it */
		if sl.IsIgnored() {
			cluster.StateMachine.AddState("ERR00037", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00037"], sl.URL), ErrFrom: "CHECK"})
			// if cluster.Conf.LogLevel > 1 || forcingLog {
			cluster.LogModulePrintf(forcingLog, config.ConstLogModGeneral, config.LvlDbg, "%s is in the ignore list. Skipping", sl.URL)
			// }
			continue
		}
		if sl.State != stateFailed && sl.ServerID != oldMaster.ServerID {
			return i
		}

	}
	return -1
}

func (cluster *Cluster) CloseRing(oldMaster *ServerMonitor) error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Closing ring around %s", cluster.oldMaster.URL)
	child := cluster.GetRingChildServer(cluster.oldMaster)
	if child == nil {
		return errors.New("Can't find child in ring")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Child is %s", child.URL)
	parent := cluster.GetRingParentServer(oldMaster)
	if parent == nil {
		return errors.New("Can't find parent in ring")
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Parent is %s", parent.URL)
	logs, err := child.StopSlave()
	cluster.LogSQL(logs, err, child.URL, "MasterFailover", config.LvlErr, "Could not stop slave on server %s, %s", child.URL, err)

	hasMyGTID := parent.HasMySQLGTID()

	var changeMasterErr error

	// Not MariaDB and not using MySQL GTID, 2.0 stop doing any thing until pseudo GTID
	if parent.DBVersion.IsMySQLOrPerconaGreater57() && hasMyGTID == true {
		logs, changeMasterErr = dbhelper.ChangeMaster(child.Conn, dbhelper.ChangeMasterOpt{
			Host:        parent.Host,
			Port:        parent.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "",
			SSL:         cluster.Conf.ReplicationSSL,
			Channel:     cluster.Conf.MasterConn,
			PostgressDB: parent.PostgressDB,
		}, child.DBVersion)
	} else {
		//MariaDB all cases use GTID

		logs, changeMasterErr = dbhelper.ChangeMaster(child.Conn, dbhelper.ChangeMasterOpt{
			Host:        parent.Host,
			Port:        parent.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "SLAVE_POS",
			SSL:         cluster.Conf.ReplicationSSL,
			Channel:     cluster.Conf.MasterConn,
			PostgressDB: parent.PostgressDB,
		}, child.DBVersion)
	}

	cluster.LogSQL(logs, changeMasterErr, child.URL, "MasterFailover", config.LvlErr, "Could not change masteron server %s, %s", child.URL, changeMasterErr)

	logs, err = child.StartSlave()
	cluster.LogSQL(logs, err, child.URL, "MasterFailover", config.LvlErr, "Could not start slave on server %s, %s", child.URL, err)

	return nil
}
