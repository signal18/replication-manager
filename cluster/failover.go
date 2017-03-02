// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"fmt"
	"os/exec"
	"strconv"
	"time"

	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/misc"
)

// MasterFailover triggers a master switchover and returns the new master URL
func (cluster *Cluster) MasterFailover(fail bool) bool {
	cluster.LogPrint("INFO : Starting master switch")
	cluster.sme.SetFailoverState()
	// Phase 1: Cleanup and election
	var err error
	if fail == false {
		cluster.LogPrint("INFO : Checking long running updates on master")
		if dbhelper.CheckLongRunningWrites(cluster.master.Conn, cluster.conf.WaitWrite) > 0 {
			cluster.LogPrint("ERROR: Long updates running on master. Cannot switchover")
			cluster.sme.RemoveFailoverState()
			return false
		}

		cluster.LogPrintf("INFO : Flushing tables on %s (master)", cluster.master.URL)
		workerFlushTable := make(chan error, 1)

		go func() {
			var err2 error
			err2 = dbhelper.FlushTablesNoLog(cluster.master.Conn)

			workerFlushTable <- err2
		}()
		select {
		case err = <-workerFlushTable:
			if err != nil {
				cluster.LogPrintf("WARN : Could not flush tables on master", err)
			}
		case <-time.After(time.Second * time.Duration(cluster.conf.WaitTrx)):
			cluster.LogPrint("ERROR: Long  running trx on master. Cannot switchover")
			cluster.sme.RemoveFailoverState()
			return false
		}

	}
	cluster.LogPrint("INFO : Electing a new master")
	for _, s := range cluster.slaves {
		s.refresh()
	}
	key := cluster.electCandidate(cluster.slaves)
	if key == -1 {
		cluster.LogPrint("ERROR: No candidates found")
		cluster.sme.RemoveFailoverState()
		return false
	}
	cluster.LogPrintf("INFO : Slave %s [%d] has been elected as a new master", cluster.slaves[key].URL, key)
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
		cluster.LogPrintf("INFO : Calling pre-failover script")
		var out []byte
		out, err = exec.Command(cluster.conf.PreScript, oldMaster.Host, cluster.master.Host).CombinedOutput()
		if err != nil {
			cluster.LogPrint("ERROR:", err)
		}
		cluster.LogPrint("INFO : Pre-failover script complete:", string(out))
	}

	// Phase 2: Reject updates and sync slaves
	if fail == false {
		if cluster.conf.FailEventStatus {
			for _, v := range cluster.master.EventStatus {
				if v.Status == 3 {
					cluster.LogPrintf("INFO : Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
					err = dbhelper.SetEventStatus(oldMaster.Conn, v, 3)
					if err != nil {
						cluster.LogPrintf("ERROR: Could not Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
					}
				}
			}
		}
		if cluster.conf.FailEventScheduler {

			cluster.LogPrintf("INFO : Disable Event Scheduler on old master")
			err = dbhelper.SetEventScheduler(oldMaster.Conn, false)
			if err != nil {
				cluster.LogPrint("ERROR: Could not disable event scheduler on old master")
			}
		}
		oldMaster.freeze()
		cluster.LogPrintf("INFO : Rejecting updates on %s (old master)", oldMaster.URL)
		err = dbhelper.FlushTablesWithReadLock(oldMaster.Conn)
		if err != nil {
			cluster.LogPrintf("WARN : Could not lock tables on %s (old master) %s", oldMaster.URL, err)
		}
	}
	// Sync candidate depending on the master status.
	// If it's a switchover, use MASTER_POS_WAIT to sync.
	// If it's a failover, wait for the SQL thread to read all relay logs.
	if fail == false {
		cluster.LogPrint("INFO : Waiting for candidate Master to synchronize")
		oldMaster.refresh()
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG: Syncing on master GTID Binlog Pos [%s]", oldMaster.BinlogPos.Sprint())
			oldMaster.log()
		}
		dbhelper.MasterPosWait(cluster.master.Conn, oldMaster.BinlogPos.Sprint(), 30)
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrint("DEBUG: MASTER_POS_WAIT executed.")
			cluster.master.log()
		}

	} else {
		cluster.LogPrint("INFO : Waiting for candidate Master to apply relay log")
		err = cluster.master.readAllRelayLogs()
		if err != nil {
			cluster.LogPrintf("ERROR: Error while reading relay logs on candidate: %s", err)
		}
		cluster.LogPrint("INFO : Save replication status before electing")
		cluster.LogPrintf("INFO : master_log_file=%s", cluster.master.MasterLogFile)
		cluster.LogPrintf("INFO : master_log_pos=%s", cluster.master.MasterLogPos)
		cluster.LogPrintf("INFO : Candidate was in sync=%t", cluster.master.SemiSyncSlaveStatus)
		cluster.master.FailoverMasterLogFile = cluster.master.MasterLogFile
		cluster.master.FailoverMasterLogPos = cluster.master.MasterLogPos
		cluster.master.FailoverIOGtid = cluster.master.IOGtid
		cluster.master.FailoverSemiSyncSlaveStatus = cluster.master.SemiSyncSlaveStatus

	}
	// Phase 3: Prepare new master
	if cluster.conf.MultiMaster == false {
		cluster.LogPrint("INFO : Stopping slave thread on new master")
		err = dbhelper.StopSlave(cluster.master.Conn)
		if err != nil {
			cluster.LogPrint("WARN : Stopping slave failed on new master")
		}
	}
	// Call post-failover script before unlocking the old master.
	if cluster.conf.PostScript != "" {
		cluster.LogPrintf("INFO : Calling post-failover script")
		var out []byte
		out, err = exec.Command(cluster.conf.PostScript, oldMaster.Host, cluster.master.Host).CombinedOutput()
		if err != nil {
			cluster.LogPrint("ERROR:", err)
		}
		cluster.LogPrint("INFO : Post-failover script complete", string(out))
	}
	if cluster.conf.HaproxyOn {
		cluster.initHaproxy()
	}
	// Signal MaxScale that we have a new topology
	if cluster.conf.MxsOn == true {
		cluster.initMaxscale(oldMaster)
	}
	if cluster.conf.MultiMaster == false {
		cluster.LogPrint("INFO : Resetting slave on new master and set read/write mode on")
		err = dbhelper.ResetSlave(cluster.master.Conn, true)
		if err != nil {
			cluster.LogPrint("WARN : Reset slave failed on new master")
		}
	}
	err = dbhelper.SetReadOnly(cluster.master.Conn, false)
	if err != nil {
		cluster.LogPrint("ERROR: Could not set new master as read-write")
	}
	if cluster.conf.FailEventScheduler {
		cluster.LogPrintf("INFO : Enable Event Scheduler on the new master")
		err = dbhelper.SetEventScheduler(cluster.master.Conn, true)
		if err != nil {
			cluster.LogPrint("ERROR: Could not enable event scheduler on the new master")
		}

	}
	if cluster.conf.FailEventStatus {
		for _, v := range cluster.master.EventStatus {
			if v.Status == 3 {
				cluster.LogPrintf("INFO : Set ENABLE for event %s %s on new master", v.Db, v.Name)
				err = dbhelper.SetEventStatus(cluster.master.Conn, v, 1)
				if err != nil {
					cluster.LogPrintf("ERROR: Could not  Set ENABLE for event %s %s on new master", v.Db, v.Name)
				}
			}
		}
	}
	cm := fmt.Sprintf("CHANGE MASTER TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_connect_retry=%d, master_heartbeat_period=%d", cluster.master.IP, cluster.master.Port, cluster.rplUser, cluster.rplPass, cluster.conf.MasterConnectRetry, 1)
	if fail == false {
		// Get latest GTID pos
		oldMaster.refresh()
		// Insert a bogus transaction in order to have a new GTID pos on master
		err = dbhelper.FlushTables(cluster.master.Conn)
		if err != nil {
			cluster.LogPrint("WARN : Could not flush tables on new cluster.master", err)
		}
		// Phase 4: Demote old master to slave
		cluster.LogPrint("INFO : Switching old master as a slave")
		err = dbhelper.UnlockTables(oldMaster.Conn)
		if err != nil {
			cluster.LogPrint("WARN : Could not unlock tables on old master", err)
		}
		dbhelper.StopSlave(oldMaster.Conn) // This is helpful because in some cases the old master can have an old configuration running
		_, err = oldMaster.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos.Sprint() + "'")
		if err != nil {
			cluster.LogPrint("WARN : Could not set gtid_slave_pos on old master", err)
		}
		_, err = oldMaster.Conn.Exec(cm + ", master_use_gtid=slave_pos")
		if err != nil {
			cluster.LogPrint("WARN : Change master failed on old master", err)
		}
		err = dbhelper.StartSlave(oldMaster.Conn)
		if err != nil {
			cluster.LogPrint("WARN : Start slave failed on old master", err)
		}
		if cluster.conf.ReadOnly {
			err = dbhelper.SetReadOnly(oldMaster.Conn, true)
			if err != nil {
				cluster.LogPrintf("ERROR: Could not set old master as read-only, %s", err)
			}
		} else {
			err = dbhelper.SetReadOnly(oldMaster.Conn, false)
			if err != nil {
				cluster.LogPrintf("ERROR: Could not set old master as read-write, %s", err)
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
	cluster.LogPrint("INFO : Switching other slaves to the new master")
	for _, sl := range cluster.slaves {
		// Don't switch if slave was the old master or is in a multiple master setup.
		if sl.URL == oldMaster.URL || sl.State == stateMaster {
			continue
		}
		if fail == false {
			cluster.LogPrintf("INFO : Waiting for slave %s to sync", sl.URL)
			dbhelper.MasterPosWait(sl.Conn, oldMaster.BinlogPos.Sprint(), 30)
			if cluster.conf.LogLevel > 2 {
				sl.log()
			}
		}
		cluster.LogPrintf("INFO : Change master on slave %s", sl.URL)
		err = dbhelper.StopSlave(sl.Conn)
		if err != nil {
			cluster.LogPrintf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		if fail == false {
			_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos.Sprint() + "'")
			if err != nil {
				cluster.LogPrintf("WARN : Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
			}
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			cluster.LogPrintf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			cluster.LogPrintf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if cluster.conf.ReadOnly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				cluster.LogPrintf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		} else {
			err = dbhelper.SetReadOnly(sl.Conn, false)
			if err != nil {
				cluster.LogPrintf("ERROR: Could not remove slave %s as read-only, %s", sl.URL, err)
			}
		}
	}

	if fail == true && cluster.conf.PrefMaster != oldMaster.URL && cluster.master.URL != cluster.conf.PrefMaster && cluster.conf.PrefMaster != "" {
		prm := cluster.foundPreferedMaster(cluster.slaves)
		if prm != nil {
			cluster.LogPrint("INFO: Not on Prefered Master after Failover")
			cluster.MasterFailover(false)
		}
	}

	cluster.LogPrintf("INFO : Master switch on %s complete", cluster.master.URL)
	cluster.master.FailCount = 0
	if fail == true {
		cluster.failoverCtr++
		cluster.failoverTs = time.Now().Unix()
	}
	cluster.sme.RemoveFailoverState()
	return true
}

func (cluster *Cluster) initMaxscale(oldmaster *ServerMonitor) {
	m := maxscale.MaxScale{Host: cluster.conf.MxsHost, Port: cluster.conf.MxsPort, User: cluster.conf.MxsUser, Pass: cluster.conf.MxsPass}
	err := m.Connect()
	if err != nil {
		cluster.LogPrint("ERROR: Could not connect to MaxScale:", err)
		return
	}
	defer m.Close()
	if cluster.master.MxsServerName == "" {
		cluster.LogPrint("ERROR: MaxScale server name undiscovered")
		return
	}
	//disable monitoring
	if cluster.conf.MxsMonitor == false {
		var monitor string
		if cluster.conf.MxsGetInfoMethod == "maxinfo" {
			cluster.LogPrint("INFO: Getting Maxscale monitor via maxinfo")

			m.GetMaxInfoMonitors("http://" + cluster.conf.MxsHost + ":" + strconv.Itoa(cluster.conf.MxsMaxinfoPort) + "/monitors")
			monitor = m.GetMaxInfoMonitor()

		} else {
			cluster.LogPrint("INFO: Getting Maxscale monitor via maxadmin")
			_, err := m.ListMonitors()
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could list monitors monitor:%s", err)
			}
			monitor = m.GetMonitor()
		}
		if monitor != "" {
			cmd := "shutdown monitor \"" + monitor + "\""
			cluster.LogPrintf("INFO: %s", cmd)
			err = m.ShutdownMonitor(monitor)
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not shutdown monitor:%s", err)
			}
		} else {
			cluster.LogPrint("INFO: MaxScale No running Monitor")
		}
	}

	err = m.Command("set server " + cluster.master.MxsServerName + " master")
	if err != nil {
		cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
	}
	err = m.Command("clear server " + cluster.master.MxsServerName + " slave")
	if err != nil {
		cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
	}
	if err != nil {
		cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
	}
	if cluster.conf.MxsMonitor == false {
		for _, s := range cluster.slaves {
			err = m.Command("clear server " + s.MxsServerName + " master")
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
			}
			err = m.Command("set server " + s.MxsServerName + " slave")
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
			}
		}
		if oldmaster != nil {
			err = m.Command("clear server " + oldmaster.MxsServerName + " master")
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
			}
			err = m.Command("set server " + oldmaster.MxsServerName + " slave")
			if err != nil {
				cluster.LogPrint("ERROR: MaxScale client could not send command:%s", err)
			}
		}

	}

}

// Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate.
func (cluster *Cluster) electCandidate(l []*ServerMonitor) int {
	ll := len(l)
	seqList := make([]uint64, ll)
	hiseq := 0
	var max uint64
	var seqnos []uint64
	for i, sl := range l {
		/* If server is in the ignore list, do not elect it */
		if misc.Contains(cluster.ignoreList, sl.URL) {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG: %s is in the ignore list. Skipping", sl.URL)
			}
			continue
		}
		// TODO: refresh state outside evaluation
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG: Checking eligibility of slave server %s [%d]", sl.URL, i)
		}
		if cluster.conf.MultiMaster == true && sl.State == stateMaster {
			cluster.LogPrintf("WARN : Slave %s has state Master. Skipping", sl.URL)
			continue
		}

		// The tests below should run only in case of a switchover as they require the master to be up.
		if cluster.master.State != stateFailed && cluster.isSlaveElectableForSwitchover(sl) == false {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("WARN : Slave %s has isSlaveElectableForSwitchover false. Skipping", sl.URL)
			}
			continue
		}
		if sl.IsRelay {
			cluster.LogPrintf("WARN : Slave %s is Relay . Skipping", sl.URL)
			continue
		}

		/* binlog + ping  */
		if cluster.isSlaveElectable(sl) == false {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("WARN : Slave %s has isSlaveElectable false. Skipping", sl.URL)
			}
			continue
		}

		/* Rig the election if the examined slave is preferred candidate master in switchover */
		if sl.URL == cluster.conf.PrefMaster && cluster.master.State != stateFailed {
			if cluster.conf.LogLevel > 2 {
				cluster.LogPrintf("DEBUG: Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}

		if cluster.master.State != stateFailed {
			seqnos = sl.SlaveGtid.GetSeqNos()

		} else {
			seqnos = sl.IOGtid.GetSeqNos()
		}
		if cluster.conf.LogLevel > 2 {
			cluster.LogPrintf("DEBUG: Got sequence(s) %v for server [%d]", seqnos, i)
		}
		for _, v := range seqnos {
			seqList[i] += v
		}
		if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
	} //end loop all slaves
	if max > 0 {
		/* Return key of slave with the highest seqno. */
		return hiseq
	}
	// cluster.LogPrint("ERROR: No suitable candidates found.") TODO: move this outside func
	return -1
}

func (cluster *Cluster) isSlaveElectable(sl *ServerMonitor) bool {
	ss, _ := dbhelper.GetSlaveStatus(sl.Conn)

	/* binlog + ping  */
	if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host) == false {
		cluster.LogPrintf("WARN : Slave %s do not ping or have no binlogs. Skipping", sl.URL)
		return false
	}
	if ss.Seconds_Behind_Master.Int64 > cluster.conf.MaxDelay && cluster.conf.RplChecks == true {
		cluster.LogPrintf("WARN : Unsafe failover condition. Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, cluster.conf.MaxDelay, ss.Seconds_Behind_Master.Int64)
		return false
	}
	if ss.Slave_SQL_Running == "No" && cluster.conf.RplChecks {
		cluster.LogPrintf("WARN : Unsafe failover condition. Slave %s SQL Thread is stopped. Skipping", sl.URL)
		return false
	}
	if sl.SemiSyncSlaveStatus == false && cluster.conf.FailSync == true && cluster.conf.RplChecks == true {
		cluster.LogPrintf("WARN : Slave %s not in semi-sync in sync. Skipping", sl.URL)
		return false
	}

	return true
}

func (cluster *Cluster) isSlaveElectableForSwitchover(sl *ServerMonitor) bool {
	ss, _ := dbhelper.GetSlaveStatus(sl.Conn)
	hasBinLogs, err := dbhelper.CheckBinlogFilters(cluster.master.Conn, sl.Conn)
	if err != nil {
		cluster.LogPrint("ERROR: Could not check binlog filters")
		return false
	}
	if hasBinLogs == false && cluster.conf.CheckBinFilter == true {
		cluster.LogPrintf("WARN : Binlog filters differ on master and slave %s. Skipping", sl.URL)
		return false
	}
	if dbhelper.CheckReplicationFilters(cluster.master.Conn, sl.Conn) == false && cluster.conf.CheckReplFilter == true {
		cluster.LogPrintf("WARN : Replication filters differ on master and slave %s. Skipping", sl.URL)
		return false
	}
	if cluster.conf.GtidCheck && dbhelper.CheckSlaveSync(sl.Conn, cluster.master.Conn) == false && cluster.conf.RplChecks == true {
		cluster.LogPrintf("WARN : Slave %s not in sync. Skipping", sl.URL)
		return false
	}
	if sl.SemiSyncSlaveStatus == false && cluster.conf.SwitchSync == true && cluster.conf.RplChecks == true {
		cluster.LogPrintf("WARN : Slave %s not in semi-sync in sync. Skipping", sl.URL)
		return false
	}
	if ss.Seconds_Behind_Master.Valid == false && cluster.conf.RplChecks == true {
		cluster.LogPrintf("WARN : Slave %s is stopped. Skipping", sl.URL)
		return false
	}

	if sl.IsMaxscale || sl.IsRelay {
		cluster.LogPrintf("WARN : Slave %s is relay. Skipping", sl.URL)
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
