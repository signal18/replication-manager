// monitor.go
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/mariadb-tools/dbhelper"
)

type ServerMonitor struct {
	Conn           *sqlx.DB
	URL            string
	Host           string
	Port           string
	IP             string
	BinlogPos      string
	Strict         string
	ServerId       uint
	MasterServerId uint
	MasterHost     string
	LogBin         string
	UsingGtid      string
	CurrentGtid    string
	SlaveGtid      string
	IOThread       string
	SQLThread      string
	ReadOnly       string
	Delay          sql.NullInt64
	State          string
	PrevState      string
}

/* Initializes a server object */
func newServerMonitor(url string) (*ServerMonitor, error) {
	server := new(ServerMonitor)
	server.URL = url
	server.Host, server.Port = splitHostPort(url)
	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		return server, errors.New(fmt.Sprintf("ERROR: DNS resolution error for host %s", server.Host))
	}
	server.Conn, err = dbhelper.MySQLConnect(dbUser, dbPass, dbhelper.GetAddress(server.Host, server.Port, *socket))
	if err != nil {
		server.State = STATE_FAILED
		return server, err
	}
	server.State = STATE_UNCONN
	return server, nil
}

/* Refresh a server object */
func (sm *ServerMonitor) refresh() error {
	err := sm.Conn.Ping()
	if err != nil {
		return err
	}
	sv, err := dbhelper.GetVariables(sm.Conn)
	if err != nil {
		return err
	}
	sm.BinlogPos = sv["GTID_BINLOG_POS"]
	sm.Strict = sv["GTID_STRICT_MODE"]
	sm.LogBin = sv["LOG_BIN"]
	sm.ReadOnly = sv["READ_ONLY"]
	sm.CurrentGtid = sv["GTID_CURRENT_POS"]
	sm.SlaveGtid = sv["GTID_SLAVE_POS"]
	sid, _ := strconv.ParseUint(sv["SERVER_ID"], 10, 0)
	sm.ServerId = uint(sid)
	slaveStatus, err := dbhelper.GetSlaveStatus(sm.Conn)
	if err != nil {
		return err
	}
	sm.UsingGtid = slaveStatus.Using_Gtid
	sm.IOThread = slaveStatus.Slave_IO_Running
	sm.SQLThread = slaveStatus.Slave_SQL_Running
	sm.Delay = slaveStatus.Seconds_Behind_Master
	sm.MasterServerId = slaveStatus.Master_Server_Id
	sm.MasterHost = slaveStatus.Master_Host
	sm.State = STATE_SLAVE
	return err
}

/* Check replication health and return status string */
func (sm *ServerMonitor) healthCheck() string {
	if sm.Delay.Valid == false {
		if sm.SQLThread == "Yes" && sm.IOThread == "No" {
			return "NOT OK, IO Stopped"
		} else if sm.SQLThread == "No" && sm.IOThread == "Yes" {
			return "NOT OK, SQL Stopped"
		} else {
			return "NOT OK, ALL Stopped"
		}
	} else {
		if sm.Delay.Int64 > 0 {
			return "Behind master"
		}
		return "Running OK"
	}
}

/* Triggers a master switchover. Returns the new master's URL */
func (master *ServerMonitor) switchover() (string, int) {
	logprint("INFO : Starting switchover")
	// Phase 1: Cleanup and election
	logprintf("INFO : Flushing tables on %s (master)", master.URL)
	err := dbhelper.FlushTablesNoLog(master.Conn)
	if err != nil {
		logprintf("WARN : Could not flush tables on master", err)
	}
	logprint("INFO : Checking long running updates on master")
	if dbhelper.CheckLongRunningWrites(master.Conn, 10) > 0 {
		logprint("ERROR: Long updates running on master. Cannot switchover")
		return "", -1
	}
	logprint("INFO : Electing a new master")
	var nmUrl string
	key := master.electCandidate(slaves)
	if key == -1 {
		return "", -1
	}
	nmUrl = slaves[key].URL
	logprintf("INFO : Slave %s has been elected as a new master", nmUrl)
	newMaster, err := newServerMonitor(nmUrl)
	if *preScript != "" {
		logprintf("INFO : Calling pre-failover script")
		out, err := exec.Command(*preScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Pre-failover script complete:", string(out))
	}
	// Phase 2: Reject updates and sync slaves
	master.freeze()
	logprintf("INFO : Rejecting updates on %s (old master)", master.URL)
	err = dbhelper.FlushTablesWithReadLock(master.Conn)
	if err != nil {
		logprintf("WARN : Could not lock tables on %s (old master) %s", master.URL, err)
	}
	logprint("INFO : Switching master")
	logprint("INFO : Waiting for candidate master to synchronize")
	masterGtid := dbhelper.GetVariableByName(master.Conn, "GTID_BINLOG_POS")
	if *verbose {
		logprintf("DEBUG: Syncing on master GTID Current Pos [%s]", masterGtid)
		master.log()
	}
	dbhelper.MasterPosWait(newMaster.Conn, masterGtid)
	if *verbose {
		logprint("DEBUG: MASTER_POS_WAIT executed.")
		newMaster.log()
	}
	// Phase 3: Prepare new master
	logprint("INFO : Stopping slave thread on new master")
	err = dbhelper.StopSlave(newMaster.Conn)
	if err != nil {
		logprint("WARN : Stopping slave failed on new master")
	}
	// Call post-failover script before unlocking the old master.
	if *postScript != "" {
		logprintf("INFO : Calling post-failover script")
		out, err := exec.Command(*postScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			logprint("ERROR:", err)
		}
		logprint("INFO : Post-failover script complete", string(out))
	}
	logprint("INFO : Resetting slave on new master and set read/write mode on")
	err = dbhelper.ResetSlave(newMaster.Conn, true)
	if err != nil {
		logprint("WARN : Reset slave failed on new master")
	}
	err = dbhelper.SetReadOnly(newMaster.Conn, false)
	if err != nil {
		logprint("ERROR: Could not set new master as read-write")
	}
	newGtid := dbhelper.GetVariableByName(master.Conn, "GTID_BINLOG_POS")
	// Insert a bogus transaction in order to have a new GTID pos on master
	err = dbhelper.FlushTables(newMaster.Conn)
	if err != nil {
		logprint("WARN : Could not flush tables on new master", err)
	}
	// Phase 4: Demote old master to slave
	cm := "CHANGE MASTER TO master_host='" + newMaster.IP + "', master_port=" + newMaster.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
	logprint("INFO : Switching old master as a slave")
	err = dbhelper.UnlockTables(master.Conn)
	if err != nil {
		logprint("WARN : Could not unlock tables on old master", err)
	}
	dbhelper.StopSlave(master.Conn) // This is helpful because in some cases the old master can have an old configuration running
	_, err = master.Conn.Exec("SET GLOBAL gtid_slave_pos='" + newGtid + "'")
	if err != nil {
		logprint("WARN : Could not set gtid_slave_pos on old master", err)
	}
	_, err = master.Conn.Exec(cm + ", master_use_gtid=slave_pos")
	if err != nil {
		logprint("WARN : Change master failed on old master", err)
	}
	err = dbhelper.StartSlave(master.Conn)
	if err != nil {
		logprint("WARN : Start slave failed on old master", err)
	}
	if *readonly {
		err = dbhelper.SetReadOnly(master.Conn, true)
		if err != nil {
			logprintf("ERROR: Could not set old master as read-only, %s", err)
		}
	}
	// Phase 5: Switch slaves to new master
	logprint("INFO : Switching other slaves to the new master")
	var oldMasterKey int
	for k, sl := range slaves {
		if sl.URL == newMaster.URL {
			slaves[k].URL = master.URL
			oldMasterKey = k
			if *verbose {
				logprintf("DEBUG: New master %s found in slave slice at key %d, reinstancing URL to %s", sl.URL, k, master.URL)
			}
			continue
		}
		logprintf("INFO : Waiting for slave %s to sync", sl.URL)
		dbhelper.MasterPosWait(sl.Conn, masterGtid)
		if *verbose {
			sl.log()
		}
		logprintf("INFO : Change master on slave %s", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			logprintf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec("SET GLOBAL gtid_slave_pos='" + newGtid + "'")
		if err != nil {
			logprintf("WARN : Could not set gtid_slave_pos on slave %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			logprintf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			logprintf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if *readonly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				logprintf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		}
	}
	logprint("INFO : Switchover complete")
	return newMaster.URL, oldMasterKey
}

/* Triggers a master failover. Returns the new master's URL and key */
func (master *ServerMonitor) failover() (string, int) {
	log.Println("INFO : Starting failover and electing a new master")
	var nmUrl string
	key := master.electCandidate(slaves)
	if key == -1 {
		return "", -1
	}
	nmUrl = slaves[key].URL
	log.Printf("INFO : Slave %s has been elected as a new master", nmUrl)
	slaves[key].writeState()
	newMaster, err := newServerMonitor(nmUrl)
	if *preScript != "" {
		log.Printf("INFO : Calling pre-failover script")
		out, err := exec.Command(*preScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("INFO : Pre-failover script complete:", string(out))
	}
	log.Println("INFO : Switching master")
	log.Println("INFO : Stopping slave thread on new master")
	err = dbhelper.StopSlave(newMaster.Conn)
	if err != nil {
		log.Println("WARN : Stopping slave failed on new master")
	}
	cm := "CHANGE MASTER TO master_host='" + newMaster.IP + "', master_port=" + newMaster.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "'"
	log.Println("INFO : Resetting slave on new master and set read/write mode on")
	err = dbhelper.ResetSlave(newMaster.Conn, true)
	if err != nil {
		log.Println("WARN : Reset slave failed on new master")
	}
	err = dbhelper.SetReadOnly(newMaster.Conn, false)
	if err != nil {
		log.Println("ERROR: Could not set new master as read-write")
	}
	log.Println("INFO : Switching other slaves to the new master")
	for _, sl := range slaves {
		log.Printf("INFO : Change master on slave %s", sl.URL)
		err := dbhelper.StopSlave(sl.Conn)
		if err != nil {
			log.Printf("WARN : Could not stop slave on server %s, %s", sl.URL, err)
		}
		_, err = sl.Conn.Exec(cm)
		if err != nil {
			log.Printf("ERROR: Change master failed on slave %s, %s", sl.URL, err)
		}
		err = dbhelper.StartSlave(sl.Conn)
		if err != nil {
			log.Printf("ERROR: could not start slave on server %s, %s", sl.URL, err)
		}
		if *readonly {
			err = dbhelper.SetReadOnly(sl.Conn, true)
			if err != nil {
				log.Printf("ERROR: Could not set slave %s as read-only, %s", sl.URL, err)
			}
		}
	}
	if *postScript != "" {
		log.Printf("INFO : Calling post-failover script")
		out, err := exec.Command(*postScript, master.Host, newMaster.Host).CombinedOutput()
		if err != nil {
			log.Println("ERROR:", err)
		}
		log.Println("INFO : Post-failover script complete", string(out))
	}
	log.Println("INFO : Failover complete")
	return newMaster.URL, key
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		logprintf("WARN : Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := *waitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		logprintf("INFO : Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	logprintf("INFO : Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
}

/* Returns a candidate from a list of slaves. If there's only one slave it will be the de facto candidate. */
func (master *ServerMonitor) electCandidate(l []*ServerMonitor) int {
	ll := len(l)
	if *verbose {
		logprintf("DEBUG: Processing %d candidates", ll)
	}
	seqList := make([]uint64, ll)
	i := 0
	hiseq := 0
	for _, sl := range l {
		if *failover == "" {
			if *verbose {
				logprintf("DEBUG: Checking eligibility of slave server %s", sl.URL)
			}
			if dbhelper.CheckSlavePrerequisites(sl.Conn, sl.Host) == false {
				continue
			}
			if dbhelper.CheckBinlogFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Binlog filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			if dbhelper.CheckReplicationFilters(master.Conn, sl.Conn) == false {
				logprintf("WARN : Replication filters differ on master and slave %s. Skipping", sl.URL)
				continue
			}
			ss, _ := dbhelper.GetSlaveStatus(sl.Conn)
			if ss.Seconds_Behind_Master.Valid == false {
				logprintf("WARN : Slave %s is stopped. Skipping", sl.URL)
				continue
			}
			if ss.Seconds_Behind_Master.Int64 > *maxDelay {
				logprintf("WARN : Slave %s has more than %d seconds of replication delay (%d). Skipping", sl.URL, *maxDelay, ss.Seconds_Behind_Master.Int64)
				continue
			}
			if *gtidCheck && dbhelper.CheckSlaveSync(sl.Conn, master.Conn) == false {
				logprintf("WARN : Slave %s not in sync. Skipping", sl.URL)
				continue
			}
		}
		/* If server is in the ignore list, do not elect it */
		if contains(ignoreList, sl.URL) {
			if *verbose {
				logprintf("DEBUG: %s is in the ignore list. Skipping", sl.URL)
			}
			continue
		}
		/* Rig the election if the examined slave is preferred candidate master */
		if sl.URL == *prefMaster {
			if *verbose {
				logprintf("DEBUG: Election rig: %s elected as preferred master", sl.URL)
			}
			return i
		}
		seqList[i] = getSeqFromGtid(dbhelper.GetVariableByName(sl.Conn, "GTID_CURRENT_POS"))
		var max uint64
		if i == 0 {
			max = seqList[0]
		} else if seqList[i] > max {
			max = seqList[i]
			hiseq = i
		}
		i++
	}
	if i > 0 {
		/* Return key of slave with the highest seqno. */
		return hiseq
	} else {
		log.Println("ERROR: No suitable candidates found.")
		return -1
	}
}

func (server *ServerMonitor) log() {
	server.refresh()
	logprintf("DEBUG: Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s\n", server.URL, server.CurrentGtid, server.SlaveGtid, server.BinlogPos)
	return
}

func (server *ServerMonitor) writeState() error {
	server.log()
	f, err := os.Create("/tmp/repmgr.state")
	if err != nil {
		return err
	}
	_, err = f.WriteString(server.BinlogPos)
	if err != nil {
		return err
	}
	return nil
}

func (s *ServerMonitor) hasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		if s.MasterServerId != sl.MasterServerId {
			return false
		}
	}
	return true
}
