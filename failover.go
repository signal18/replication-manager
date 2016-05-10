package main

import (
	"os/exec"
	"time"

	"github.com/tanji/mariadb-tools/dbhelper"
)

/* Triggers a master switchover. Returns the new master's URL */
func masterFailover(fail bool) {
	logprint("INFO : Starting master switch")
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
			return
		}
	}
	logprint("INFO : Electing a new master")
	key := master.electCandidate(slaves)
	if key == -1 {
		return
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
		out, err := exec.Command(preScript, oldMaster.Host, master.Host).CombinedOutput()
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
	logprint("INFO : Switching master")
	if fail == false {
		logprint("INFO : Waiting for candidate Master to synchronize")
		oldMaster.refresh()
		if verbose {
			logprintf("DEBUG: Syncing on master GTID Binlog Pos [%s]", oldMaster.BinlogPos)
			oldMaster.log()
		}
		dbhelper.MasterPosWait(master.Conn, oldMaster.BinlogPos)
		if verbose {
			logprint("DEBUG: MASTER_POS_WAIT executed.")
			master.log()
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
		out, err := exec.Command(postScript, oldMaster.Host, master.Host).CombinedOutput()
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
	cm := "CHANGE MASTER TO master_host='" + master.IP + "', master_port=" + master.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
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
		_, err = oldMaster.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos + "'")
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
		}
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
			dbhelper.MasterPosWait(sl.Conn, oldMaster.BinlogPos)
			if verbose {
				sl.log()
			}
		}
		logprint("INFO : Change master on slave", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			logprintf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		if fail == false {
			_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + oldMaster.BinlogPos + "'")
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
		}
	}
	if postScript != "" {
		logprintf("INFO : Calling post-failover script")
		out, err := exec.Command(postScript, oldMaster.Host, master.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Post-failover script complete", string(out))
	}
	logprintf("INFO : Master switch on %s complete", master.URL)
	failCount = 0
	if fail == true {
		failoverCtr++
		failoverTs = time.Now().Unix()
	}
	return
}
