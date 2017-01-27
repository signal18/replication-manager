// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// monitor.go
package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/tanji/replication-manager/alert"
	"github.com/tanji/replication-manager/dbhelper"
	"github.com/tanji/replication-manager/graphite"
	"github.com/tanji/replication-manager/gtid"
	"github.com/tanji/replication-manager/misc"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Conn                 *sqlx.DB
	URL                  string
	DSN                  string
	Host                 string
	Port                 string
	IP                   string
	BinlogPos            *gtid.List
	Strict               string
	ServerID             uint
	MasterServerID       uint
	MasterHost           string
	LogBin               string
	UsingGtid            string
	CurrentGtid          *gtid.List
	SlaveGtid            *gtid.List
	IOGtid               *gtid.List
	IOThread             string
	SQLThread            string
	ReadOnly             string
	Delay                sql.NullInt64
	State                string
	PrevState            string
	IOErrno              uint
	IOError              string
	SQLErrno             uint
	SQLError             string
	FailCount            int
	FailSuspectHeartbeat int64
	SemiSyncMasterStatus bool
	SemiSyncSlaveStatus  bool
	RplMasterStatus      bool
	EventScheduler       bool
	EventsStatus         []dbhelper.Event
}

type serverList []*ServerMonitor

var maxConn string

const (
	stateFailed    string = "Failed"
	stateMaster    string = "Master"
	stateSlave     string = "Slave"
	stateSlaveErr  string = "SlaveErr"
	stateSlaveLate string = "SlaveLate"
	stateUnconn    string = "Unconnected"
	stateSuspect   string = "Suspect"
	stateShard     string = "Shard"
)

/* Initializes a server object */
func newServerMonitor(url string) (*ServerMonitor, error) {
	server := new(ServerMonitor)
	server.URL = url
	server.Host, server.Port = misc.SplitHostPort(url)
	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", server.Host)
		return server, errmsg
	}
	params := fmt.Sprintf("?timeout=%ds", conf.Timeout)
	mydsn := func() string {
		dsn := dbUser + ":" + dbPass + "@"
		if server.Host != "" {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
		} else {
			dsn += "unix(" + conf.Socket + ")/" + params
		}
		return dsn
	}
	server.DSN = mydsn()
	server.Conn, err = sqlx.Open("mysql", server.DSN)
	return server, err
}

func (server *ServerMonitor) check(wg *sync.WaitGroup) {

	defer wg.Done()
	if sme.IsInFailover() {
		if conf.LogLevel > 2 {
			logprintf("DEBUG: Inside failover, skip server check")
		}
		return
	}
	if server.PrevState != server.State {
		server.PrevState = server.State
	}

	if conf.LogLevel > 2 {
		// logprint("DEBUG: Checking server", server.Host)
	}

	var err error
	switch conf.CheckType {
	case "tcp":
		err = server.Conn.Ping()
	case "agent":
		var resp *http.Response
		resp, err = http.Get("http://" + server.Host + ":10001/check/")
		if resp.StatusCode != 200 {
			// if 404, consider server down or agent killed. Don't initiate anything
			err = fmt.Errorf("HTTP Response Code Error: %d", resp.StatusCode)
		}
	}

	// Handle failure cases here
	if err != nil {
		if conf.LogLevel > 2 {
			logprintf("DEBUG: Failure detection handling for server %s", server.URL)
		}
		if err != sql.ErrNoRows && (server.State == stateMaster || server.State == stateSuspect) {
			server.FailCount++
			server.FailSuspectHeartbeat = sme.GetHeartbeats()
			if server.URL == master.URL {
				if master.FailCount <= conf.MaxFail {
					logprintf("WARN : Master Failure detected! Retry %d/%d", master.FailCount, conf.MaxFail)
				}
				if server.FailCount >= conf.MaxFail {
					if server.FailCount == conf.MaxFail {
						logprint("WARN : Declaring master as failed")
					}
					master.State = stateFailed
				} else {
					master.State = stateSuspect
				}
			}
		} else {
			if server.State != stateMaster && server.State != stateFailed {
				server.FailCount++
				if server.FailCount >= conf.MaxFail {
					if server.FailCount == conf.MaxFail {
						logprintf("WARN : Declaring server %s as failed", server.URL)
						server.State = stateFailed
					} else {
						server.State = stateSuspect
					}
					// remove from slave list
					server.delete(&slaves)
				}
			}
		}
		// Send alert if state has changed
		if server.PrevState != server.State {
			//if conf.Verbose {
			logprintf("ALERT : Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
			//}
			if conf.MailTo != "" {
				a := alert.Alert{
					From:        conf.MailFrom,
					To:          conf.MailTo,
					Type:        server.State,
					Origin:      server.URL,
					Destination: conf.MailSMTPAddr,
				}
				err = a.Email()
				if err != nil {
					logprint("ERROR: Could not send econf.Mail alert: ", err)
				}
			}
		}
		return
	}
	// Reset FailCount
	/*	if conf.Verbose>0  {
		logprintf("DEBUG: State comparison %b %b %b %d %d %d ", server.State==stateMaster , server.State==stateSlave  , server.State==stateUnconn ,(server.FailCount > 0) ,((sme.GetHeartbeats() - server.FailSuspectHeartbeat) * conf.MonitoringTicker), conf.FailResetTime) {
	}*/
	if (server.State != stateUnconn && server.State != stateSuspect) && (server.FailCount > 0) && (((sme.GetHeartbeats() - server.FailSuspectHeartbeat) * conf.MonitoringTicker) > conf.FailResetTime) {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}
	_, err = dbhelper.GetSlaveStatus(server.Conn)
	if err == sql.ErrNoRows {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if server.PrevState == stateFailed {
			if conf.LogLevel > 1 {
				logprintf("DEBUG: State comparison reinitialized failed server %s as unconnected", server.URL)
			}
			server.State = stateUnconn
			server.FailCount = 0
			if conf.Autorejoin {
				// Check if master exists in topology before rejoining.
				if server.URL != master.URL {
					logprintf("INFO : Rejoining previously failed server %s", server.URL)
					err = server.rejoin()
					if err != nil {
						logprintf("ERROR: Failed to autojoin previously failed server %s", server.URL)
					}
				}
			}
		} else if server.State != stateMaster {
			if conf.LogLevel > 1 {
				logprintf("DEBUG: State unconnected set by non-master rule on server %s", server.URL)
			}
			server.State = stateUnconn
		}
		return
	}

	// In case of state change, reintroduce the server in the slave list
	if server.PrevState == stateFailed || server.PrevState == stateUnconn {
		server.State = stateSlave
		server.FailCount = 0
		slaves = append(slaves, server)
		if conf.ReadOnly {
			err = dbhelper.SetReadOnly(server.Conn, true)
			if err != nil {
				logprintf("ERROR: Could not set rejoining slave %s as read-only, %s", server.URL, err)
			}
		}
	}
}

/* Refresh a server object */
func (server *ServerMonitor) refresh() error {
	err := server.Conn.Ping()
	if err != nil {
		return err
	}
	sv, err := dbhelper.GetVariables(server.Conn)
	if err != nil {
		return err
	}
	if sv["EVENT_SCHEDULER"] != "ON" {
		server.EventScheduler = false
	} else {
		server.EventScheduler = true
	}
	server.BinlogPos = gtid.NewList(sv["GTID_BINLOG_POS"])
	server.Strict = sv["GTID_STRICT_MODE"]
	server.LogBin = sv["LOG_BIN"]
	server.ReadOnly = sv["READ_ONLY"]
	server.CurrentGtid = gtid.NewList(sv["GTID_CURRENT_POS"])
	server.SlaveGtid = gtid.NewList(sv["GTID_SLAVE_POS"])
	sid, _ := strconv.ParseUint(sv["SERVER_ID"], 10, 0)
	server.ServerID = uint(sid)
	err = dbhelper.SetDefaultMasterConn(server.Conn, conf.MasterConn)
	if err != nil {
		return err
	}
	server.EventsStatus, err = dbhelper.GetEnventsStatus(server.Conn)
	if err != nil {
		logprintf("ERROR: Could not get events")
		return err
	}
	su := dbhelper.GetStatus(server.Conn)
	if su["RPL_SEMI_SYNC_MASTER_STATUS"] == "ON" {
		server.SemiSyncMasterStatus = true
	} else {
		server.SemiSyncMasterStatus = false
	}
	if su["RPL_SEMI_SYNC_SLAVE_STATUS"] == "ON" {
		server.SemiSyncSlaveStatus = true
	} else {
		server.SemiSyncSlaveStatus = false
	}

	slaveStatus, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		server.UsingGtid = ""
		server.IOThread = "No"
		server.SQLThread = "No"
		server.Delay = sql.NullInt64{Int64: 0, Valid: false}
		server.MasterServerID = 0
		server.MasterHost = ""
		server.IOErrno = 0
		server.IOError = ""
		server.SQLError = ""
		server.SQLErrno = 0
	} else {
		server.IOGtid = gtid.NewList(slaveStatus.Gtid_IO_Pos)
		server.UsingGtid = slaveStatus.Using_Gtid
		server.IOThread = slaveStatus.Slave_IO_Running
		server.SQLThread = slaveStatus.Slave_SQL_Running
		server.Delay = slaveStatus.Seconds_Behind_Master
		server.MasterServerID = slaveStatus.Master_Server_Id
		server.MasterHost = slaveStatus.Master_Host
		server.IOErrno = slaveStatus.Last_IO_Errno
		server.IOError = slaveStatus.Last_IO_Error
		server.SQLError = slaveStatus.Last_SQL_Error
		server.SQLErrno = slaveStatus.Last_SQL_Errno
	}

	// Initialize graphite monitoring

	if conf.GraphiteMetrics {
		graph, err := graphite.NewGraphite(conf.GraphiteCarbonHost, conf.GraphiteCarbonPort)
		if err == nil {
			graph.SimpleSend(fmt.Sprintf("server%d.replication.delay", server.ServerID), fmt.Sprintf("%d", server.Delay.Int64))
			graph.SimpleSend(fmt.Sprintf("server%d.status.ComSelect", server.ServerID), su["COM_SELECT"])
			graph.SimpleSend(fmt.Sprintf("server%d.status.Queries", server.ServerID), su["QUERIES"])
			graph.SimpleSend(fmt.Sprintf("server%d.status.ThreadsRunning", server.ServerID), su["THREADS_RUNNING"])
			graph.SimpleSend(fmt.Sprintf("server%d.status.BytesOut", server.ServerID), su["BYTES_SENT"])
			graph.SimpleSend(fmt.Sprintf("server%d.status.BytesIn", server.ServerID), su["BYTES_RECEIVED"])
			graph.Disconnect()
		}
	}

	return nil
}

/* Check replication health and return status string */
func (server *ServerMonitor) replicationCheck() string {

	if sme.IsInFailover() || server.State == stateMaster || server.State == stateSuspect || server.State == stateUnconn || server.State == stateFailed {
		return "Master OK"
	}
	if server.Delay.Valid == false && sme.CanMonitor() {
		if server.SQLThread == "Yes" && server.IOThread == "No" {
			server.State = stateSlaveErr
			return fmt.Sprintf("NOT OK, IO Stopped (%d)", server.IOErrno)
		} else if server.SQLThread == "No" && server.IOThread == "Yes" {
			server.State = stateSlaveErr
			return fmt.Sprintf("NOT OK, SQL Stopped (%d)", server.SQLErrno)
		} else if server.SQLThread == "No" && server.IOThread == "No" {
			server.State = stateSlaveErr
			return "NOT OK, ALL Stopped"
		} else if server.IOThread == "Connecting" {
			server.State = stateSlave
			return "NOT OK, IO Connecting"
		}
		server.State = stateSlave
		return "Running OK"
	}
	if server.Delay.Int64 > 0 {
		if server.Delay.Int64 > conf.MaxDelay && conf.RplChecks == true {
			server.State = stateSlaveLate
		} else {
			server.State = stateSlave
		}
		return "Behind master"
	}
	server.State = stateSlave
	return "Running OK"
}

func (sl serverList) checkAllSlavesRunning() bool {
	for _, s := range sl {
		if s.SQLThread != "Yes" || s.IOThread != "Yes" {
			return false
		}
	}
	return true
}

/* Check Consistency parameters on server */
func (server *ServerMonitor) acidTest() bool {
	syncBin := dbhelper.GetVariableByName(server.Conn, "SYNC_BINLOG")
	logFlush := dbhelper.GetVariableByName(server.Conn, "INNODB_FLUSH_LOG_AT_TRX_COMMIT")
	if syncBin == "1" && logFlush == "1" {
		return true
	}
	return false
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		logprintf("WARN : Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := conf.WaitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		logprintf("INFO : Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	maxConn = dbhelper.GetVariableByName(server.Conn, "MAX_CONNECTIONS")
	_, err = server.Conn.Exec("SET GLOBAL max_connections=0")
	if err != nil {
		logprint("ERROR: Could not set max_connections to 0 on demoted leader")
	}
	logprintf("INFO : Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
}

func (server *ServerMonitor) readAllRelayLogs() error {
	ss, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		return err
	}
	logprintf("INFO : Reading all relay logs on %s", server.URL)
	for ss.Master_Log_File != ss.Relay_Master_Log_File && ss.Read_Master_Log_Pos == ss.Exec_Master_Log_Pos {
		ss, err = dbhelper.GetSlaveStatus(server.Conn)
		if err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil
}

func (server *ServerMonitor) log() {
	server.refresh()
	logprintf("DEBUG: Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s", server.URL, server.CurrentGtid.Sprint(), server.SlaveGtid.Sprint(), server.BinlogPos.Sprint())
	return
}

func (server *ServerMonitor) close() {
	server.Conn.Close()
	return
}

func (server *ServerMonitor) writeState() error {
	server.log()
	f, err := os.Create("/tmp/repmgr.state")
	if err != nil {
		return err
	}
	_, err = f.WriteString(server.BinlogPos.Sprint())
	if err != nil {
		return err
	}
	return nil
}

func (server *ServerMonitor) hasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		if server.MasterServerID != sl.MasterServerID {
			return false
		}
	}
	return true
}

func (server *ServerMonitor) delete(sl *serverList) {
	lsm := *sl
	for k, s := range lsm {
		if server.URL == s.URL {
			lsm[k] = lsm[len(lsm)-1]
			lsm[len(lsm)-1] = nil
			lsm = lsm[:len(lsm)-1]
			break
		}
	}
	*sl = lsm
}

func (server *ServerMonitor) rejoin() error {
	if conf.ReadOnly {
		dbhelper.SetReadOnly(server.Conn, true)
	}
	cm := "CHANGE MASTER TO master_host='" + master.IP + "', master_port=" + master.Port + ", master_user='" + rplUser + "', master_password='" + rplPass + "', MASTER_USE_GTID=CURRENT_POS"
	_, err := server.Conn.Exec(cm)
	dbhelper.StartSlave(server.Conn)
	return err
}
