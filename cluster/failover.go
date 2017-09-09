// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/gtid"
	"github.com/signal18/replication-manager/misc"
	"github.com/signal18/replication-manager/state"
)

// MasterFailover triggers a master switchover and returns the new master URL
func (cluster *Cluster) MasterFailover(fail bool) bool {
	cluster.sme.SetFailoverState()
	// Phase 1: Cleanup and election
	var err error
	if fail == false {
		cluster.LogPrintf("INFO", "--------------------------")
		cluster.LogPrintf("INFO", "Starting master switchover")
		cluster.LogPrintf("INFO", "--------------------------")
		cluster.LogPrintf("INFO", "Checking long running updates on master %d", cluster.conf.SwitchWaitWrite)
		if cluster.master == nil {
			cluster.LogPrintf("ERROR", "Cannot switchover without a master")
			return false
		}
		if cluster.master.Conn == nil {
			cluster.LogPrintf("ERROR", "Cannot switchover without a master connection")
			return false
		}
		if dbhelper.CheckLongRunningWrites(cluster.master.Conn, cluster.conf.SwitchWaitWrite) > 0 {
			cluster.LogPrintf("ERROR", "Long updates running on master. Cannot switchover")
			cluster.sme.RemoveFailoverState()
			return false
		}

		cluster.LogPrintf("INFO", "Flushing tables on master %s", cluster.master.URL)
		workerFlushTable := make(chan error, 1)

		go func() {
			var err2 error
			err2 = dbhelper.FlushTablesNoLog(cluster.master.Conn)

			workerFlushTable <- err2
		}()
		select {
		case err = <-workerFlushTable:
			if err != nil {
				cluster.LogPrintf("WARN", "Could not flush tables on master", err)
			}
		case <-time.After(time.Second * time.Duration(cluster.conf.SwitchWaitTrx)):
			cluster.LogPrintf("ERROR", "Long running trx on master at least %d, can not switchover ", cluster.conf.SwitchWaitTrx)
			cluster.sme.RemoveFailoverState()
			return false
		}

	} else {
		cluster.LogPrintf("INFO", "------------------------")
		cluster.LogPrintf("INFO", "Starting master failover")
		cluster.LogPrintf("INFO", "------------------------")
	}
	cluster.LogPrintf("INFO", "Electing a new master")
	for _, s := range cluster.slaves {
		s.Refresh()
	}
	key := cluster.electCandidate(cluster.slaves, true)
	if key == -1 {
		cluster.LogPrintf("ERROR", "No candidates found")
		cluster.sme.RemoveFailoverState()
		return false
	}
	cluster.LogPrintf("INFO", "Slave %s [%d] has been elected as a new master", cluster.slaves[key].URL, key)
	// Shuffle the server list
	oldMaster := cluster.master
	var skey int
	for k, server := range cluster.servers {
		if cluster.slaves[key].URL == server.URL {
			skey = k
			break
		}
	}
	cluster.master = cluster.servers[skey]
	cluster.master.State = stateMaster
	if cluster.conf.MultiMaster == false {
		cluster.slaves[key].delete(&cluster.slaves)
	}
	// Call pre-failover script
	if cluster.conf.PreScript != "" {
		cluster.LogPrintf("INFO", "Calling pre-failover script")
		var out []byte
		out, err = exec.Command(cluster.conf.PreScript, oldMaster.Host, cluster.master.Host, oldMaster.Port, cluster.master.Port, oldMaster.MxsServerName, cluster.master.MxsServerName).CombinedOutput()
		if err != nil {
			cluster.LogPrintf("ERROR", "%s", err)
		}
		cluster.LogPrintf("INFO", "Pre-failover script complete:", string(out))
	}

	// Phase 2: Reject updates and sync slaves
	if fail == false {
		if cluster.conf.FailEventStatus {
			for _, v := range cluster.master.EventStatus {
				if v.Status == 3 {
					cluster.LogPrintf("INFO", "Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
					err = dbhelper.SetEventStatus(oldMaster.Conn, v, 3)
					if err != nil {
						cluster.LogPrintf("ERROR", "Could not Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
					}
				}
			}
		}
		if cluster.conf.FailEventScheduler {

			cluster.LogPrintf("INFO", "Disable Event Scheduler on old master")
			err = dbhelper.SetEventScheduler(oldMaster.Conn, false)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not disable event scheduler on old master")
			}
		}
		oldMaster.freeze()
		cluster.LogPrintf("INFO", "Rejecting updates on %s (old master)", oldMaster.URL)
		err = dbhelper.FlushTablesWithReadLock(oldMaster.Conn)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not lock tables on %s (old master) %s", oldMaster.URL, err)
		}
	}
	// Sync candidate depending on the master status.
	// If it's a switchover, use MASTER_POS_WAIT to sync.
	// If it's a failover, wait for the SQL thread to read all relay logs.
	// If maxsclale we should wait for relay catch via old style
	crash := new(Crash)
	crash.URL = oldMaster.URL
	crash.ElectedMasterURL = cluster.master.URL

	if fail == false && cluster.conf.MxsBinlogOn == false && cluster.master.DBVersion.IsMariaDB() {
		cluster.LogPrintf("INFO", "Waiting for candidate Master to synchronize")
		oldMaster.Refresh()
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG", "Syncing on master GTID Binlog Pos [%s]", oldMaster.GTIDBinlogPos.Sprint())
			oldMaster.log()
		}
		dbhelper.MasterWaitGTID(cluster.master.Conn, oldMaster.GTIDBinlogPos.Sprint(), 30)
	} else {

		cluster.LogPrintf("INFO", "Waiting for candidate master to apply relay log")
		err = cluster.master.ReadAllRelayLogs()
		if err != nil {
			cluster.LogPrintf("ERROR", "Error while reading relay logs on candidate: %s", err)
		}
		cluster.LogPrintf("INFO ", "Save replication status before electing")
		cluster.LogPrintf("INFO", "master_log_file=%s", cluster.master.MasterLogFile)
		cluster.LogPrintf("INFO", "master_log_pos=%s", cluster.master.MasterLogPos)
		cluster.LogPrintf("INFO", "Candidate was in sync=%t", cluster.master.SemiSyncSlaveStatus)
		cluster.master.FailoverMasterLogFile = cluster.master.MasterLogFile
		cluster.master.FailoverMasterLogPos = cluster.master.MasterLogPos
		crash.FailoverMasterLogFile = cluster.master.MasterLogFile
		crash.FailoverMasterLogPos = cluster.master.MasterLogPos
		if cluster.master.DBVersion.IsMariaDB() {
			if cluster.conf.MxsBinlogOn {
				cluster.master.FailoverIOGtid = cluster.master.CurrentGtid
				crash.FailoverIOGtid = cluster.master.CurrentGtid
			} else {
				cluster.master.FailoverIOGtid = cluster.master.IOGtid
				crash.FailoverIOGtid = cluster.master.IOGtid
			}
		}
		cluster.master.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus
		crash.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus
	}

	// if relay server than failover and switchover converge to a new binlog  make this happen
	var relaymaster *ServerMonitor
	if cluster.conf.MxsBinlogOn || cluster.conf.MultiTierSlave {
		cluster.LogPrintf("INFO", "Candidate master has to catch up with relay server log position")
		relaymaster = cluster.GetRelayServer()
		if relaymaster != nil {
			relaymaster.Refresh()

			binlogfiletoreach, _ := strconv.Atoi(strings.Split(relaymaster.MasterLogFile, ".")[1])
			cluster.LogPrintf("INFO", "Relay server log pos reached %d", binlogfiletoreach)
			dbhelper.ResetMaster(cluster.master.Conn)
			cluster.LogPrintf("INFO", "Reset Master on candidate Master")
			ctbinlog := 0
			for ctbinlog < binlogfiletoreach {
				ctbinlog++
				cluster.LogPrintf("INFO", "Flush Log on new Master %d", ctbinlog)
				dbhelper.FlushLogs(cluster.master.Conn)
			}
			time.Sleep(2 * time.Second)
			ms, _ := dbhelper.GetMasterStatus(cluster.master.Conn)
			cluster.master.FailoverMasterLogFile = ms.File
			cluster.master.FailoverMasterLogPos = "4"
			crash.FailoverMasterLogFile = ms.File
			crash.FailoverMasterLogPos = "4"

			//strconv.FormatUint(uint64(ms.Position), 10)
			cluster.LogPrintf("INFO", "Backing up master pos %s %s", cluster.master.FailoverMasterLogFile, cluster.master.FailoverMasterLogPos)
		} else {
			cluster.LogPrintf("ERROR", "No relay server found")
		}
	}
	// Phase 3: Prepare new master
	if cluster.conf.MultiMaster == false {
		cluster.LogPrintf("INFO", "Stopping slave thread on new master")
		if cluster.master.DBVersion.IsMariaDB() || (cluster.master.DBVersion.IsMariaDB() == false && cluster.master.DBVersion.Minor < 7) {
			err = dbhelper.StopSlave(cluster.master.Conn)
			if err != nil {
				cluster.LogPrintf("ERROR", "Stopping slave failed on new master")
			} else {
				// if server is mysql 5.7 we just need to stop the IO thread
				err = dbhelper.StopSlaveIOThread(cluster.master.Conn)
				if err != nil {
					cluster.LogPrintf("ERROR", "Stopping IO thread failed on new master")
				}
			}
		}
	}
	cluster.crashes = append(cluster.crashes, crash)
	cluster.Save()
	// Call post-failover script before unlocking the old master.
	if cluster.conf.PostScript != "" {
		cluster.LogPrintf("INFO", "Calling post-failover script")
		var out []byte
		out, err = exec.Command(cluster.conf.PostScript, oldMaster.Host, cluster.master.Host, oldMaster.Port, cluster.master.Port, oldMaster.MxsServerName, cluster.master.MxsServerName).CombinedOutput()
		if err != nil {
			cluster.LogPrintf("ERROR", "%s", err)
		}
		cluster.LogPrintf("INFO", "Post-failover script complete", string(out))
	}
	cluster.failoverProxies()
	if cluster.conf.MultiMaster == false {
		cluster.LogPrintf("INFO", "Resetting slave on new master and set read/write mode on")
		if cluster.master.DBVersion.IsMySQL() {
			// Need to stop all threads to reset on MySQL
			dbhelper.StopSlave(cluster.master.Conn)
		}

		err = dbhelper.ResetSlave(cluster.master.Conn, true)

		if err != nil {
			cluster.LogPrintf("ERROR", "Reset slave failed on new master, reason:%s ", err)
		}
	}
	err = dbhelper.SetReadOnly(cluster.master.Conn, false)
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not set new master as read-write")
	}
	if cluster.conf.FailEventScheduler {
		cluster.LogPrintf("INFO", "Enable Event Scheduler on the new master")
		err = dbhelper.SetEventScheduler(cluster.master.Conn, true)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not enable event scheduler on the new master")
		}

	}
	if cluster.conf.FailEventStatus {
		for _, v := range cluster.master.EventStatus {
			if v.Status == 3 {
				cluster.LogPrintf("INFO", "Set ENABLE for event %s %s on new master", v.Db, v.Name)
				err = dbhelper.SetEventStatus(cluster.master.Conn, v, 1)
				if err != nil {
					cluster.LogPrintf("ERROR", "Could not Set ENABLE for event %s %s on new master", v.Db, v.Name)
				}
			}
		}
	}
	// Insert a bogus transaction in order to have a new GTID pos on master
	cluster.LogPrintf("INFO", "Inject fake transaction on new master %s ", cluster.master.URL)
	err = dbhelper.FlushTables(cluster.master.Conn)
	if err != nil {
		cluster.LogPrintf("ERROR", "Could not flush tables on new master for fake trx %s", err)
	}

	if fail == false {
		// Get latest GTID pos
		oldMaster.Refresh()

		// Phase 4: Demote old master to slave
		cluster.LogPrintf("INFO", "Switching old master as a slave")
		err = dbhelper.UnlockTables(oldMaster.Conn)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not unlock tables on old master %s", err)
		}
		dbhelper.StopSlave(oldMaster.Conn) // This is helpful because in some cases the old master can have an old configuration running
		if cluster.conf.FailForceGtid && oldMaster.DBVersion.IsMariaDB() {
			_, err = oldMaster.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.GTIDBinlogPos.Sprint() + "'")
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not set gtid_slave_pos on old master, reason: %s", err)
			}
		}
		hasMyGTID, err := dbhelper.HasMySQLGTID(oldMaster.Conn)
		if err != nil {
			cluster.LogPrint("WARN : Could not get MySQL GTID status: ", err)
		}
		var changeMasterErr error
		// Do positional switch if we are an old MySQL version
		if oldMaster.DBVersion.IsMariaDB() == false && hasMyGTID == false {
			cluster.LogPrintf("INFO", "Doing positional switch of old Master")
			changeMasterErr = dbhelper.ChangeMaster(oldMaster.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Logfile:   cluster.master.BinaryLogFile,
				Logpos:    cluster.master.BinaryLogPos,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "POSITIONAL",
			})
			if changeMasterErr != nil {
				cluster.LogPrintf("ERROR", "Change master failed on old master, reason:%s ", changeMasterErr)
			}
			err = dbhelper.StartSlave(oldMaster.Conn)
			if err != nil {
				cluster.LogPrintf("ERROR", "Start slave failed on old master, reason: %s", err)
			}
		} else if oldMaster.DBVersion.IsMySQL57() && hasMyGTID == true {
			// We can do MySQL 5.7 style failover
			cluster.LogPrintf("INFO", "Doing MySQL GTID switch of the old master")
			changeMasterErr = dbhelper.ChangeMaster(oldMaster.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "",
			})
			if changeMasterErr != nil {
				cluster.LogPrintf("ERROR", "Change master failed on old master %s", changeMasterErr)
			}
			err = dbhelper.StartSlave(oldMaster.Conn)
			if err != nil {
				cluster.LogPrintf("ERROR", "Start slave failed on old master %s", err)
			}
		} else if cluster.conf.MxsBinlogOn == false {
			cluster.LogPrintf("INFO", "Doing MariaDB GTID switch of the old master")
			changeMasterErr = dbhelper.ChangeMaster(oldMaster.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "SLAVE_POS",
			})
			if changeMasterErr != nil {
				cluster.LogPrintf("ERROR", "Change master failed on old master %s", changeMasterErr)
			}
			err = dbhelper.StartSlave(oldMaster.Conn)
			if err != nil {
				cluster.LogPrintf("ERROR", "Start slave failed on old master %s", err)
			}
		} else {
			// Don't start slave until the relay as been point to new master
			cluster.LogPrintf("INFO", "Pointing old master to relay server")
			if relaymaster.MxsHaveGtid {
				err = dbhelper.ChangeMaster(oldMaster.Conn, dbhelper.ChangeMasterOpt{
					Host:      relaymaster.Host,
					Port:      relaymaster.Port,
					User:      cluster.rplUser,
					Password:  cluster.rplPass,
					Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
					Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
					Mode:      "SLAVE_POS",
				})
			} else {
				err = dbhelper.ChangeMaster(oldMaster.Conn, dbhelper.ChangeMasterOpt{
					Host:      relaymaster.Host,
					Port:      relaymaster.Port,
					User:      cluster.rplUser,
					Password:  cluster.rplPass,
					Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
					Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
					Mode:      "POSITIONAL",
					Logfile:   cluster.master.FailoverMasterLogFile,
					Logpos:    cluster.master.FailoverMasterLogPos,
				})
			}
		}

		if cluster.conf.ReadOnly {
			err = dbhelper.SetReadOnly(oldMaster.Conn, true)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not set old master as read-only, %s", err)
			}
		} else {
			err = dbhelper.SetReadOnly(oldMaster.Conn, false)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not set old master as read-write, %s", err)
			}
		}
		oldMaster.Conn.Exec(fmt.Sprintf("SET GLOBAL max_connections=%s", maxConn))
		// Add the old master to the slaves list
		oldMaster.State = stateSlave
		if cluster.conf.MultiMaster == false {
			cluster.slaves = append(cluster.slaves, oldMaster)
		}
	}
	// Phase 5: Switch slaves to new master
	cluster.LogPrintf("INFO", "Switching other slaves to the new master")
	for _, sl := range cluster.slaves {
		// Don't switch if slave was the old master or is in a multiple master setup or with relay server.
		if sl.URL == oldMaster.URL || sl.State == stateMaster || (sl.IsRelay == false && cluster.conf.MxsBinlogOn == true) {
			continue
		}
		// maxscale is in the list of slave

		if fail == false && cluster.conf.MxsBinlogOn == false {
			cluster.LogPrintf("INFO", "Waiting for slave %s to sync", sl.URL)
			if sl.DBVersion.Flavor == "MariaDB" {
				dbhelper.MasterWaitGTID(sl.Conn, oldMaster.GTIDBinlogPos.Sprint(), 30)
			} else {
				dbhelper.MasterPosWait(sl.Conn, oldMaster.BinaryLogFile, oldMaster.BinaryLogPos, 30)
			}
			if cluster.conf.LogLevel > 2 {
				sl.log()
			}
		}
		cluster.LogPrintf("INFO", "Change master on slave %s", sl.URL)
		err = dbhelper.StopSlave(sl.Conn)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not stop slave on server %s, %s", sl.URL, err)
		}
		//possible dead code
		if fail == false && cluster.conf.MxsBinlogOn == false {
			if cluster.conf.FailForceGtid && sl.DBVersion.IsMariaDB() {
				_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.GTIDBinlogPos.Sprint() + "'")
				if err != nil {
					cluster.LogPrintf("ERROR", "Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
				}
			}
		}
		hasMyGTID, err := dbhelper.HasMySQLGTID(cluster.master.Conn)
		var changeMasterErr error
		if sl.DBVersion.IsMariaDB() == false && hasMyGTID == false {
			cluster.LogPrintf("INFO", "Doing Positional switch of slave %s", sl.DSN)
			changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Logfile:   cluster.master.BinaryLogFile,
				Logpos:    cluster.master.MasterLogPos,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "POSITIONAL",
			})
		} else if oldMaster.DBVersion.IsMySQL57() && hasMyGTID == true {
			changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "",
			})
		} else if cluster.conf.MxsBinlogOn == false {
			changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
				Host:      cluster.master.Host,
				Port:      cluster.master.Port,
				User:      cluster.rplUser,
				Password:  cluster.rplPass,
				Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
				Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
				Mode:      "SLAVE_POS",
			})
		} else {
			cluster.LogPrintf("INFO", "Pointing relay to the new master: %s:%s", cluster.master.Host, cluster.master.Port)
			if sl.MxsHaveGtid {
				changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
					Host:      cluster.master.Host,
					Port:      cluster.master.Port,
					User:      cluster.rplUser,
					Password:  cluster.rplPass,
					Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
					Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
					Mode:      "SLAVE_POS",
				})
			} else {
				changeMasterErr = dbhelper.ChangeMaster(sl.Conn, dbhelper.ChangeMasterOpt{
					Host:      cluster.master.Host,
					Port:      cluster.master.Port,
					User:      cluster.rplUser,
					Password:  cluster.rplPass,
					Retry:     strconv.Itoa(cluster.conf.ForceSlaveHeartbeatRetry),
					Heartbeat: strconv.Itoa(cluster.conf.ForceSlaveHeartbeatTime),
					Mode:      "MXS",
				})
			}
		}
		if changeMasterErr != nil {
			cluster.LogPrintf("ERROR", "Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could not start slave on server %s, %s", sl.URL, err)
		}
		// now start the old master as relay is ready
		if cluster.conf.MxsBinlogOn && fail == false {
			cluster.LogPrintf("INFO", "Restating old master replication relay server ready")
			dbhelper.StartSlave(oldMaster.Conn)
		}
		if cluster.conf.ReadOnly && cluster.conf.MxsBinlogOn == false {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could not set slave %s as read-only, %s", sl.URL, err)
			}
		} else {
			if cluster.conf.MxsBinlogOn == false {
				err = dbhelper.SetReadOnly(sl.Conn, false)
				if err != nil {
					cluster.LogPrintf("ERROR", "Could not remove slave %s as read-only, %s", sl.URL, err)
				}
			}
		}
	}

	if fail == true && cluster.conf.PrefMaster != oldMaster.URL && cluster.master.URL != cluster.conf.PrefMaster && cluster.conf.PrefMaster != "" {
		prm := cluster.foundPreferedMaster(cluster.slaves)
		if prm != nil {
			cluster.LogPrintf("INFO", "Not on Preferred Master after failover")
			cluster.MasterFailover(false)
		}
	}

	cluster.LogPrintf("INFO", "Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	if fail == true {
		cluster.failoverCtr++
		cluster.failoverTs = time.Now().Unix()
	}
	cluster.sme.RemoveFailoverState()
	return true
}

// Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate.
func (cluster *Cluster) electCandidate(l []*ServerMonitor, forcingLog bool) int {
	ll := len(l)
	seqList := make([]uint64, ll)
	posList := make([]uint64, ll)
	hipos := 0
	hiseq := 0
	var max uint64
	var maxpos uint64

	for i, sl := range l {
		/* If server is in the ignore list, do not elect it */
		if misc.Contains(cluster.ignoreList, sl.URL) {
			cluster.sme.AddState("ERR00037", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00037"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "%s is in the ignore list. Skipping", sl.URL)
			}
			continue
		}
		// TODO: refresh state outside evaluation
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("DEBUG", "Checking eligibility of slave server %s [%d]", sl.URL, i)
		}
		if sl.IsRelay {
			cluster.sme.AddState("ERR00036", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00036"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Slave %s is Relay . Skipping", sl.URL)
			}
			continue
		}
		if cluster.conf.MultiMaster == true && sl.State == stateMaster {
			cluster.sme.AddState("ERR00035", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00035"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Slave %s has state Master. Skipping", sl.URL)
			}
			continue
		}

		// The tests below should run only in case of a switchover as they require the master to be up.
		if cluster.master.State != stateFailed && cluster.isSlaveElectableForSwitchover(sl, forcingLog) == false {
			cluster.sme.AddState("ERR00034", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00034"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Slave %s has isSlaveElectableForSwitchover false. Skipping", sl.URL)
			}
			continue
		}

		/* binlog + ping  */
		if cluster.isSlaveElectable(sl, forcingLog) == false {
			cluster.sme.AddState("ERR00039", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00039"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Slave %s has isSlaveElectable false. Skipping", sl.URL)
			}
			continue
		}

		/* Rig the election if the examined slave is preferred candidate master in switchover */
		if sl.URL == cluster.conf.PrefMaster && cluster.master.State != stateFailed {
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
		//old style replication
		if sl.MasterLogFile == "" && cluster.conf.FailRestartUnsafe == false {
			cluster.sme.AddState("ERR00033", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00033"], sl.URL), ErrFrom: "CHECK"})
			if cluster.conf.LogLevel > 1 || forcingLog {
				cluster.LogPrintf("DEBUG", "Election %s have no master log file, may be failed slave", sl.URL)
			}
			continue
		}
		// Fake position if none as new slave
		filepos := "1"
		logfile := "master.000001"
		if sl.MasterLogFile != "" {
			filepos = sl.MasterLogPos
			logfile = sl.MasterLogFile

		}
		for len(filepos) > 10 {
			filepos = "0" + filepos
		}
		pos := strings.Split(logfile, ".")[1] + filepos
		binlogposreach, _ := strconv.ParseUint(pos, 10, 64)

		posList[i] = binlogposreach

		seqnos := gtid.NewList("1-1-1").GetSeqNos()

		if sl.MasterLogFile != "" {
			if cluster.master.State != stateFailed {
				seqnos = sl.SlaveGtid.GetSeqNos()
			} else {
				seqnos = sl.IOGtid.GetSeqNos()
			}
		}
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("DEBUG", "Got sequence(s) %v for server [%d]", seqnos, i)
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
	// cluster.LogPrint("ERROR: No suitable candidates found.") TODO: move this outside func
	return -1
}

func (cluster *Cluster) isSlaveElectable(sl *ServerMonitor, forcingLog bool) bool {
	ss, _ := sl.getNamedSlaveStatus(sl.ReplicationSourceName)
	/* binlog + ping  */
	if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host) == false {
		cluster.sme.AddState("ERR00040", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00040"], sl.URL), ErrFrom: "CHECK"})
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("DEBUG", "Slave %s does not ping or has no binlogs. Skipping", sl.URL)
		}
		return false
	}
	cluster.LogPrintf("WARN", "Unsafe failover condition. Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, cluster.conf.FailMaxDelay, ss.Seconds_Behind_Master.Int64)
	if ss.Seconds_Behind_Master.Int64 > cluster.conf.FailMaxDelay && cluster.conf.RplChecks == true {
		cluster.sme.AddState("ERR00041", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00041"], sl.URL, cluster.conf.FailMaxDelay, ss.Seconds_Behind_Master.Int64), ErrFrom: "CHECK"})
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Unsafe failover condition. Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, cluster.conf.FailMaxDelay, ss.Seconds_Behind_Master.Int64)
		}
		return false
	}
	if ss.Slave_SQL_Running == "No" && cluster.conf.RplChecks {
		cluster.sme.AddState("ERR00042", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00042"], sl.URL), ErrFrom: "CHECK"})
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Unsafe failover condition. Slave %s SQL Thread is stopped. Skipping", sl.URL)
		}
		return false
	}
	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.conf.FailSync && cluster.conf.RplChecks {
		cluster.sme.AddState("ERR00043", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00043"], sl.URL), ErrFrom: "CHECK"})
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("DEBUG", "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		}
		return false
	}

	return true
}

func (cluster *Cluster) isSlaveElectableForSwitchover(sl *ServerMonitor, forcingLog bool) bool {
	ss, _ := sl.getNamedSlaveStatus(sl.ReplicationSourceName)
	hasBinLogs, err := dbhelper.CheckBinlogFilters(cluster.master.Conn, sl.Conn)
	if err != nil {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Could not check binlog filters")
		}
		return false
	}
	if hasBinLogs == false && cluster.conf.CheckBinFilter == true {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Binlog filters differ on master and slave %s. Skipping", sl.URL)
		}
		return false
	}
	if dbhelper.CheckReplicationFilters(cluster.master.Conn, sl.Conn) == false && cluster.conf.CheckReplFilter == true {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Replication filters differ on master and slave %s. Skipping", sl.URL)
		}
		return false
	}
	if cluster.conf.SwitchGtidCheck && dbhelper.CheckSlaveSync(sl.Conn, cluster.master.Conn) == false && cluster.conf.RplChecks == true {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Equal-GTID option is enabled and GTID position on slave %s differs from master. Skipping", sl.URL)
		}
		return false
	}
	if sl.HaveSemiSync && sl.SemiSyncSlaveStatus == false && cluster.conf.SwitchSync && cluster.conf.RplChecks {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Semi-sync slave %s is out of sync. Skipping", sl.URL)
		}
		return false
	}
	if ss.Seconds_Behind_Master.Valid == false && cluster.conf.RplChecks == true {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Slave %s is stopped. Skipping", sl.URL)
		}
		return false
	}

	if sl.IsMaxscale || sl.IsRelay {
		if cluster.conf.LogLevel > 1 || forcingLog {
			cluster.LogPrintf("WARN", "Slave %s is a relay slave. Skipping", sl.URL)
		}
		return false
	}
	return true
}

func (cluster *Cluster) foundPreferedMaster(l []*ServerMonitor) *ServerMonitor {
	for _, sl := range l {
		if sl.URL == cluster.conf.PrefMaster && cluster.master.State != stateFailed {
			return sl
		}
	}
	return nil
}
