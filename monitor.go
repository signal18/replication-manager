// monitor.go
package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
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
	return server, nil
}

/* Refresh a server object */
func (sm *ServerMonitor) refresh() error {
	err := sm.Conn.Ping()
	if err != nil {
		// we want the failed state for masters to be set by the monitor
		if sm.State != STATE_MASTER {
			sm.State = STATE_FAILED
			// remove from slave list
			slaves = sm.delete(slaves)
		}
		return err
	}
	sv, err := dbhelper.GetVariables(sm.Conn)
	if err != nil {
		return err
	}
	sm.PrevState = sm.State
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
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if sm.State == STATE_FAILED {
			sm.State = STATE_UNCONN
			if *autorejoin {
				if *verbose {
					logprint("INFO : Rejoining previously failed server", sm.URL)
				}
				err := sm.rejoin()
				if err != nil {
					logprint("ERROR: Failed to autojoin previously failed server", sm.URL)
				}
			}
		}
		return err
	}
	sm.UsingGtid = slaveStatus.Using_Gtid
	sm.IOThread = slaveStatus.Slave_IO_Running
	sm.SQLThread = slaveStatus.Slave_SQL_Running
	sm.Delay = slaveStatus.Seconds_Behind_Master
	sm.MasterServerId = slaveStatus.Master_Server_Id
	sm.MasterHost = slaveStatus.Master_Host
	// In case of state change, reintroduce the server in the slave list
	if sm.PrevState == STATE_FAILED || sm.PrevState == STATE_UNCONN {
		sm.State = STATE_SLAVE
		slaves = append(slaves, sm)
	}
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
		logprint("ERROR: No suitable candidates found.")
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

func (server *ServerMonitor) delete(lsm []*ServerMonitor) []*ServerMonitor {
	for k, s := range lsm {
		if server.URL == s.URL {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
		}
	}
	return lsm
}

func (server *ServerMonitor) rejoin() error {
	if *readonly {
		dbhelper.SetReadOnly(server.Conn, true)
	}
	cm := "CHANGE MASTER TO master_host='" + master.IP + "', master_port=" + master.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "', MASTER_USE_GTID=CURRENT_POS"
	_, err := server.Conn.Exec(cm)
	dbhelper.StartSlave(server.Conn)
	return err
}
