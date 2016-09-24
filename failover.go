// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/misc"
)

/* Triggers a master switchover. Returns the new master's URL */
func masterFailover(fail bool) bool {
	logprint("INFO : Starting master switch")
	sme.SetFailoverState()
	// Phase 1: Cleanup and election
	var err error
	if fail == false {
		logprintf("INFO : Flushing tables on %s (master)", master.URL)
		err = dbhelper.FlushTablesNoLog(master.Conn)
		if err != nil {
			logprintf("WARN : Could not flush tables on master", err)
		}
		logprint("INFO : Checking long running updates on master")
		if dbhelper.CheckLongRunningWrites(master.Conn, 10) > 0 {
			logprint("ERROR: Long updates running on master. Cannot switchover")
			sme.RemoveFailoverState()
			return false
		}
	}
	logprint("INFO : Electing a new master")
	for _, s := range slaves {
		s.refresh()
	}
	key := electCandidate(slaves)
	if key == -1 {
		logprint("ERROR: No candidates found")
		sme.RemoveFailoverState()
		return false
	}
	logprintf("INFO : Slave %s [%d] has been elected as a new master", slaves[key].URL, key)
	// Shuffle the server list
	oldMaster := master
	var skey int
	for k, server := range servers {
		if slaves[key].URL == server.URL {
			skey = k
			break
		}
	}
	master = servers[skey]
	master.State = stateMaster
	if multiMaster == false {
		slaves[key].delete(&slaves)
	}
	// Call pre-failover script
	if preScript != "" {
		logprintf("INFO : Calling pre-failover script")
		var out []byte
		out, err = exec.Command(preScript, oldMaster.Host, master.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Pre-failover script complete:", string(out))
	}
	// Phase 2: Reject updates and sync slaves
	if fail == false {
		oldMaster.freeze()
		logprintf("INFO : Rejecting updates on %s (old master)", oldMaster.URL)
		err = dbhelper.FlushTablesWithReadLock(oldMaster.Conn)
		if err != nil {
			logprintf("WARN : Could not lock tables on %s (old master) %s", oldMaster.URL, err)
		}
	}
	// Sync candidate depending on the master status.
	// If it's a switchover, use MASTER_POS_WAIT to sync.
	// If it's a failover, wait for the SQL thread to read all relay logs.
	if fail == false {
		logprint("INFO : Waiting for candidate Master to synchronize")
		oldMaster.refresh()
		if verbose {
			logprintf("DEBUG: Syncing on master GTID Binlog Pos [%s]", oldMaster.BinlogPos.Sprint())
			oldMaster.log()
		}
		dbhelper.MasterPosWait(master.Conn, oldMaster.BinlogPos.Sprint(), 30)
		if verbose {
			logprint("DEBUG: MASTER_POS_WAIT executed.")
			master.log()
		}
	} else {
		err = master.readAllRelayLogs()
		if err != nil {
			logprintf("ERROR: Error while reading relay logs on candidate: %s", err)
		}
	}
	// Phase 3: Prepare new master
	if multiMaster == false {
		logprint("INFO : Stopping slave thread on new master")
		err = dbhelper.StopSlave(master.Conn)
		if err != nil {
			logprint("WARN : Stopping slave failed on new master")
		}
	}
	// Call post-failover script before unlocking the old master.
	if postScript != "" {
		logprintf("INFO : Calling post-failover script")
		var out []byte
		out, err = exec.Command(postScript, oldMaster.Host, master.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Post-failover script complete", string(out))
	}
	if multiMaster == false {
		logprint("INFO : Resetting slave on new master and set read/write mode on")
		err = dbhelper.ResetSlave(master.Conn, true)
		if err != nil {
			logprint("WARN : Reset slave failed on new master")
		}
	}
	err = dbhelper.SetReadOnly(master.Conn, false)
	if err != nil {
		logprint("ERROR: Could not set new master as read-write")
	}
	cm := fmt.Sprintf("CHANGE MASTER TO master_host='%s', master_port=%s, master_user='%s', master_password='%s', master_connect_retry=%d", master.IP, master.Port, rplUser, rplPass, masterConnectRetry)
	if fail == false {
		// Get latest GTID pos
		oldMaster.refresh()
		// Insert a bogus transaction in order to have a new GTID pos on master
		err = dbhelper.FlushTables(master.Conn)
		if err != nil {
			logprint("WARN : Could not flush tables on new master", err)
		}
		// Phase 4: Demote old master to slave
		logprint("INFO : Switching old master as a slave")
		err = dbhelper.UnlockTables(oldMaster.Conn)
		if err != nil {
			logprint("WARN : Could not unlock tables on old master", err)
		}
		dbhelper.StopSlave(oldMaster.Conn) // This is helpful because in some cases the old master can have an old configuration running
		_, err = oldMaster.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos.Sprint() + "'")
		if err != nil {
			logprint("WARN : Could not set gtid_slave_pos on old master", err)
		}
		_, err = oldMaster.Conn.Exec(cm + ", master_use_gtid=slave_pos")
		if err != nil {
			logprint("WARN : Change master failed on old master", err)
		}
		err = dbhelper.StartSlave(oldMaster.Conn)
		if err != nil {
			logprint("WARN : Start slave failed on old master", err)
		}
		if readonly {
			err = dbhelper.SetReadOnly(oldMaster.Conn, true)
			if err != nil {
				logprintf("ERROR: Could not set old master as read-only, %s", err)
			}
		} else {
			err = dbhelper.SetReadOnly(oldMaster.Conn, false)
			if err != nil {
				logprintf("ERROR: Could not set old master as read-write, %s", err)
			}
		}
		_, err = oldMaster.Conn.Exec(fmt.Sprintf("SET GLOBAL max_connections=%s", maxConn))
		// Add the old master to the slaves list
		oldMaster.State = stateSlave
		if multiMaster == false {
			slaves = append(slaves, oldMaster)
		}
	}
	// Phase 5: Switch slaves to new master
	logprint("INFO : Switching other slaves to the new master")
	for _, sl := range slaves {
		// Don't switch if slave was the old master or is in a multiple master setup.
		if sl.URL == oldMaster.URL || sl.State == stateMaster {
			continue
		}
		if fail == false {
			logprintf("INFO : Waiting for slave %s to sync", sl.URL)
			dbhelper.MasterPosWait(sl.Conn, oldMaster.BinlogPos.Sprint(), 30)
			if verbose {
				sl.log()
			}
		}
		logprintf("INFO : Change master on slave %s", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			logprintf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		if fail == false {
			_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos.Sprint() + "'")
			if err != nil {
				logprintf("WARN : Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
			}
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			logprintf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			logprintf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if readonly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				logprintf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		} else {
	  	err = dbhelper.SetReadOnly(sl.Conn, false)
	 		if err != nil {
		 		logprintf("ERROR: Could not remove slave %s as read-only, %s", sl.URL, err)
	 		}
		}

	}



	logprintf("INFO : Master switch on %s complete", master.URL)
	master.FailCount = 0
	if fail == true {
		failoverCtr++
		failoverTs = time.Now().Unix()
	}
	sme.RemoveFailoverState()
	return true
}

// Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate.
func electCandidate(l []*ServerMonitor) int {
	ll := len(l)
	seqList := make([]uint64, ll)
	hiseq := 0
	var max uint64
	for i, sl := range l {
		/* If server is in the ignore list, do not elect it */
		if misc.Contains(ignoreList, sl.URL) {
			if loglevel > 2 {
				logprintf("DEBUG: %s is in the ignore list. Skipping", sl.URL)
			}
			continue
		}
		// TODO: refresh state outside evaluation
		if loglevel > 2 {
			logprintf("DEBUG: Checking eligibility of slave server %s [%d]", sl.URL, i)
		}
		if multiMaster == true && sl.State == stateMaster {
			logprintf("WARN : Slave %s has state Master. Skipping", sl.URL)
			continue
		}
		// The tests below should run only in case of a switchover as they require the master to be up.
		if master.State != stateFailed {
			if dbhelper.CheckBinlogFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Binlog filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			if dbhelper.CheckReplicationFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Replication filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			if gtidCheck && dbhelper.CheckSlaveSync(sl.Conn, master.Conn) == false && rplchecks == true {
				logprintf("WARN : Slave %s not in sync. Skipping", sl.URL)
				continue
			}
			if sl.SemiSyncSlaveStatus == false && failsync == true && rplchecks == true {
				logprintf("WARN : Slave %s not in semi-sync in sync. Skipping", sl.URL)
				continue
			}
		}
		/* binlog + ping  */
		if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host) == false {
			continue
		}
		ss, _ := dbhelper.GetSlaveStatus(sl.Conn)
		if ss.Seconds_Behind_Master.Valid == false && master.State != stateFailed && rplchecks == true {
			logprintf("WARN : Slave %s is stopped. Skipping", sl.URL)
			continue
		}
		if ss.Seconds_Behind_Master.Int64 > maxDelay && rplchecks == true {
			logprintf("WARN : Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, maxDelay, ss.Seconds_Behind_Master.Int64)
			continue
		}

		/* Rig the election if the examined slave is preferred candidate master */
		if sl.URL == prefMaster {
			if loglevel > 2 {
				logprintf("DEBUG: Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
		seqnos := sl.SlaveGtid.GetSeqNos()
		if loglevel > 2 {
			logprintf("DEBUG: Got sequence(s) %v for server [%d]", seqnos, i)
		}
		for _, v := range seqnos {
			seqList[i] += v
		}
		if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
	}
	if max > 0 {
		/* Return key of slave with the highest seqno. */
		return hiseq
	}
	// logprint("ERROR: No suitable candidates found.") TODO: move this outside func
	return -1
}
