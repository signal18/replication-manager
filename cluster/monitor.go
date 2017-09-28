// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"database/sql"
	"errors"
	"fmt"
	"hash/crc64"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/alert"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/gtid"

	"github.com/signal18/replication-manager/misc"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Id       string //Unique name given by cluster & crc64(URL) used by test to provision
	Conn     *sqlx.DB
	User     string
	Pass     string
	URL      string
	DSN      string `json:"-"`
	Host     string
	Port     string
	IP       string
	Strict   string
	ServerID uint

	LogBin                      string
	GTIDBinlogPos               *gtid.List
	CurrentGtid                 *gtid.List
	SlaveGtid                   *gtid.List
	IOGtid                      *gtid.List
	FailoverIOGtid              *gtid.List
	GTIDExecuted                string
	ReadOnly                    string
	State                       string
	PrevState                   string
	FailCount                   int
	FailSuspectHeartbeat        int64
	SemiSyncMasterStatus        bool
	SemiSyncSlaveStatus         bool
	RplMasterStatus             bool
	EventScheduler              bool
	EventStatus                 []dbhelper.Event
	ClusterGroup                *Cluster
	BinaryLogFile               string
	BinaryLogPos                string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	FailoverSemiSyncSlaveStatus bool
	Process                     *os.Process
	Conf                        string //Unique Conf given by reg test initMariaDB
	MxsServerName               string //Unique server Name in maxscale conf
	MxsServerStatus             string
	MxsServerConnections        int
	ProxysqlHostgroup           string
	HaveSemiSync                bool
	HaveInnodbTrxCommit         bool
	HaveSyncBinLog              bool
	HaveChecksum                bool
	HaveBinlogRow               bool
	HaveBinlogAnnotate          bool
	HaveBinlogSlowqueries       bool
	HaveBinlogCompress          bool
	HaveLogSlaveUpdates         bool
	HaveGtidStrictMode          bool
	HaveWsrep                   bool
	Version                     int
	IsWsrepSync                 bool
	IsMaxscale                  bool
	IsRelay                     bool
	IsSlave                     bool
	IsMaintenance               bool
	MxsVersion                  int
	MxsHaveGtid                 bool
	RelayLogSize                uint64
	Replications                []dbhelper.SlaveStatus
	ReplicationSourceName       string
	DBVersion                   *dbhelper.MySQLVersion
	Status                      map[string]string
	ReplicationHealth           string
}

type serverList []*ServerMonitor

var maxConn string

const (
	stateFailed      string = "Failed"
	stateMaster      string = "Master"
	stateSlave       string = "Slave"
	stateSlaveErr    string = "SlaveErr"
	stateSlaveLate   string = "SlaveLate"
	stateMaintenance string = "Maintenance"
	stateUnconn      string = "StandAlone"
	stateSuspect     string = "Suspect"
	stateShard       string = "Shard"
	stateProv        string = "Provision"
	stateMasterAlone string = "MasterAlone"
	stateRelay       string = "Relay"
	stateRelayErr    string = "RelayErr"
	stateRelayLate   string = "RelayLate"
)

/* Initializes a server object */
func (cluster *Cluster) newServerMonitor(url string, user string, pass string) (*ServerMonitor, error) {

	server := new(ServerMonitor)
	server.User = user
	server.Pass = pass
	server.HaveSemiSync = true
	server.HaveInnodbTrxCommit = true
	server.HaveSyncBinLog = true
	server.HaveChecksum = true
	server.HaveBinlogRow = true
	server.HaveBinlogAnnotate = true
	server.HaveBinlogCompress = true
	server.HaveBinlogSlowqueries = true
	server.MxsHaveGtid = false
	// consider all nodes are maxscale to avoid sending command until discoverd
	server.IsRelay = false
	server.IsMaxscale = true
	server.ClusterGroup = cluster
	server.URL = url
	server.Host, server.Port = misc.SplitHostPort(url)
	crcTable := crc64.MakeTable(crc64.ECMA)
	server.Id = strconv.FormatUint(crc64.Checksum([]byte(server.URL), crcTable), 10)

	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", server.Host)
		return server, errmsg
	}
	params := fmt.Sprintf("?timeout=%ds&readTimeout=%ds", cluster.conf.Timeout, cluster.conf.ReadTimeout)

	mydsn := func() string {
		dsn := server.User + ":" + server.Pass + "@"
		if server.Host != "" {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
		} else {
			dsn += "unix(" + cluster.conf.Socket + ")/" + params
		}
		return dsn
	}
	server.DSN = mydsn()
	if cluster.haveDBTLSCert {
		mysql.RegisterTLSConfig("tlsconfig", cluster.tlsconf)
		server.DSN = server.DSN + "&tls=tlsconfig"
	}
	server.Conn, err = sqlx.Open("mysql", server.DSN)

	return server, err
}

func (server *ServerMonitor) check(wg *sync.WaitGroup) {

	defer wg.Done()
	if server.ClusterGroup.sme.IsInFailover() {
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG", "Inside failover, skip server check")
		}
		return
	}

	if server.ClusterGroup.conf.LogLevel > 2 {
		// server.ClusterGroup.LogPrintf("INFO", "Checking server %s", server.Host)
	}

	var conn *sqlx.DB
	var err error
	switch server.ClusterGroup.conf.CheckType {
	case "tcp":
		conn, err = sqlx.Connect("mysql", server.DSN)
		defer conn.Close()
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
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG", "Failure detection handling for server %s", server.URL)
		}
		if err != sql.ErrNoRows {
			server.FailCount++
			if server.ClusterGroup.master != nil && server.URL == server.ClusterGroup.master.URL {
				server.FailSuspectHeartbeat = server.ClusterGroup.sme.GetHeartbeats()
				if server.ClusterGroup.master.FailCount <= server.ClusterGroup.conf.MaxFail {
					server.ClusterGroup.LogPrintf("INFO", "Master Failure detected! Retry %d/%d", server.ClusterGroup.master.FailCount, server.ClusterGroup.conf.MaxFail)
				}
				if server.FailCount >= server.ClusterGroup.conf.MaxFail {
					if server.FailCount == server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrintf("INFO", "Declaring master as failed")
					}
					server.ClusterGroup.master.State = stateFailed
				} else {
					server.ClusterGroup.master.State = stateSuspect
				}
			} else {
				// not the master
				if server.ClusterGroup.conf.LogLevel > 2 {
					server.ClusterGroup.LogPrintf("DEBUG", "Failure detection of no master FailCount %d MaxFail %d", server.FailCount, server.ClusterGroup.conf.MaxFail)
				}
				if server.FailCount >= server.ClusterGroup.conf.MaxFail {
					if server.FailCount == server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrintf("INFO", "Declaring server %s as failed", server.URL)
						server.State = stateFailed
						// remove from slave list
						server.delete(&server.ClusterGroup.slaves)
					}
				} else {
					server.State = stateSuspect
				}
			}
		}
		// Send alert if state has changed
		if server.PrevState != server.State {
			//if cluster.conf.Verbose {
			server.ClusterGroup.LogPrintf("ALERT", "Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
			//}
			if server.ClusterGroup.conf.MailTo != "" {
				a := alert.Alert{
					From:        server.ClusterGroup.conf.MailFrom,
					To:          server.ClusterGroup.conf.MailTo,
					Type:        server.State,
					Origin:      server.URL,
					Destination: server.ClusterGroup.conf.MailSMTPAddr,
				}
				err = a.Email()
				if err != nil {
					server.ClusterGroup.LogPrintf("ERROR", "Could not send mail alert: %s ", err)
				}
			}
		}
		if server.ClusterGroup.conf.AlertScript != "" {
			server.ClusterGroup.LogPrintf("INFO", "Calling alert script")
			var out []byte
			out, err = exec.Command(server.ClusterGroup.conf.AlertScript, server.URL, server.PrevState, server.State).CombinedOutput()
			if err != nil {
				server.ClusterGroup.LogPrintf("ERROR", "%s", err)
			}
			server.ClusterGroup.LogPrintf("INFO", "Alert script complete:", string(out))
		}
	}

	// Reset FailCount
	if (server.State != stateFailed && server.State != stateUnconn && server.State != stateSuspect) && (server.FailCount > 0) /*&& (((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * server.ClusterGroup.conf.MonitoringTicker) > server.ClusterGroup.conf.FailResetTime)*/ {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}

	var ss dbhelper.SlaveStatus
	ss, errss := dbhelper.GetSlaveStatus(server.Conn)
	// We have no replicatieon can this be the old master
	if errss == sql.ErrNoRows {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if server.PrevState == stateFailed {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG", "State comparison reinitialized failed server %s as unconnected", server.URL)
			}
			server.SetReadOnly()
			server.State = stateUnconn
			server.FailCount = 0
			if server.ClusterGroup.conf.Autorejoin {
				server.RejoinMaster()
			} else {
				server.ClusterGroup.LogPrintf("INFO", "Auto Rejoin is disabled")
			}

		} else if server.State != stateMaster && server.PrevState != stateUnconn {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG", "State unconnected set by non-master rule on server %s", server.URL)
			}
			server.SetReadOnly()
			server.State = stateUnconn
		}

		if server.PrevState != server.State {
			server.PrevState = server.State
		}
		return
	} else if errss == nil && (server.PrevState == stateFailed || server.PrevState == stateSuspect) {
		server.rejoinSlave(ss)
	}

	if server.PrevState != server.State {
		server.PrevState = server.State
	}
}

// Refresh a server object
func (server *ServerMonitor) Refresh() error {

	if server.Conn.Unsafe() == nil {
		server.State = stateFailed
		return errors.New("Connection is closed, server unreachable")
	}
	conn, err := sqlx.Connect("mysql", server.DSN)
	defer conn.Close()
	if err != nil {
		return err
	}
	if server.ClusterGroup.conf.MxsBinlogOn {
		mxsversion, _ := dbhelper.GetMaxscaleVersion(server.Conn)
		if mxsversion != "" {
			server.ClusterGroup.LogPrintf("INFO", "Found Maxscale")
			server.IsMaxscale = true
			server.IsRelay = true
			server.MxsVersion = dbhelper.MariaDBVersion(mxsversion)
			server.State = stateRelay
		} else {
			server.IsMaxscale = false
		}
	} else {
		server.IsMaxscale = false

	}

	if !(server.ClusterGroup.conf.MxsBinlogOn && server.IsMaxscale) {
		// maxscale don't support show variables
		var sv map[string]string
		sv, err = dbhelper.GetVariables(server.Conn)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get varaibles %s", err)
			return err
		}
		server.Version = dbhelper.MariaDBVersion(sv["VERSION"]) // Deprecated
		server.DBVersion, err = dbhelper.GetDBVersion(server.Conn)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get database version")
		}

		if sv["EVENT_SCHEDULER"] != "ON" {
			server.EventScheduler = false
		} else {
			server.EventScheduler = true
		}
		server.GTIDBinlogPos = gtid.NewList(sv["GTID_BINLOG_POS"])
		server.GTIDExecuted = sv["GTID_EXECUTED"]
		server.Strict = sv["GTID_STRICT_MODE"]
		server.LogBin = sv["LOG_BIN"]
		server.ReadOnly = sv["READ_ONLY"]
		if sv["lOG_BIN_COMPRESS"] != "ON" {
			server.HaveBinlogCompress = false
		} else {
			server.HaveBinlogCompress = true
		}
		if sv["GTID_STRICT_MODE"] != "ON" {
			server.HaveGtidStrictMode = false
		} else {
			server.HaveGtidStrictMode = true
		}
		if sv["LOG_SLAVE_UPDATES"] != "ON" {
			server.HaveLogSlaveUpdates = false
		} else {
			server.HaveLogSlaveUpdates = true
		}
		if sv["INNODB_FLUSH_LOG_AT_TRX_COMMIT"] != "1" {
			server.HaveInnodbTrxCommit = false
		} else {
			server.HaveInnodbTrxCommit = true
		}
		if sv["SYNC_BINLOG"] != "1" {
			server.HaveSyncBinLog = false
		} else {
			server.HaveSyncBinLog = true
		}
		if sv["INNODB_CHECKSUM"] == "NONE" {
			server.HaveChecksum = false
		} else {
			server.HaveChecksum = true
		}
		if sv["BINLOG_FORMAT"] != "ROW" {
			server.HaveBinlogRow = false
		} else {
			server.HaveBinlogRow = true
		}
		if sv["BINLOG_ANNOTATE_ROW_EVENTS"] != "ON" {
			server.HaveBinlogAnnotate = false
		} else {
			server.HaveBinlogAnnotate = true
		}
		if sv["LOG_SLOW_SLAVE_STATEMENTS"] != "ON" {
			server.HaveBinlogSlowqueries = false
		} else {
			server.HaveBinlogSlowqueries = true
		}

		server.RelayLogSize, _ = strconv.ParseUint(sv["RELAY_LOG_SPACE_LIMIT"], 10, 64)
		server.CurrentGtid = gtid.NewList(sv["GTID_CURRENT_POS"])
		server.SlaveGtid = gtid.NewList(sv["GTID_SLAVE_POS"])
		var sid uint64
		sid, err = strconv.ParseUint(sv["SERVER_ID"], 10, 64)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not parse server_id, reason: %s", err)
		}
		server.ServerID = uint(sid)
		err = dbhelper.SetDefaultMasterConn(server.Conn, server.ClusterGroup.conf.MasterConn)
		if err != nil {
			return err
		}
		server.EventStatus, err = dbhelper.GetEventStatus(server.Conn)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get events")
		}
	}
	// SHOW MASTER STATUS
	masterStatus, err := dbhelper.GetMasterStatus(server.Conn)
	if err != nil {
		// binary log might be closed for that server
	} else {
		server.BinaryLogFile = masterStatus.File
		server.BinaryLogPos = strconv.FormatUint(uint64(masterStatus.Position), 10)
	}

	// SHOW SLAVE STATUS

	if !(server.ClusterGroup.conf.MxsBinlogOn && server.IsMaxscale) && server.DBVersion.IsMariaDB() {
		server.Replications, err = dbhelper.GetAllSlavesStatus(server.Conn)
	} else {
		server.Replications, err = dbhelper.GetChannelSlaveStatus(server.Conn)
	}
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Could not get slaves status %s", err)
		return err
	}
	// select a replication status get an err if repliciations array is empty
	slaveStatus, err := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		// Do not reset  server.MasterServerID = 0 as we may need it for recovery
		server.IsSlave = false
	} else {
		server.IsSlave = true
	}
	server.ReplicationHealth = server.replicationCheck()
	// if MaxScale exit at fetch variables and status part as not supported
	if server.ClusterGroup.conf.MxsBinlogOn && server.IsMaxscale {
		return nil
	}
	server.Status, _ = dbhelper.GetStatus(server.Conn)
	//server.ClusterGroup.LogPrintf("ERROR: %s %s %s", su["RPL_SEMI_SYNC_MASTER_STATUS"], su["RPL_SEMI_SYNC_SLAVE_STATUS"], server.URL)
	if server.Status["RPL_SEMI_SYNC_MASTER_STATUS"] == "" || server.Status["RPL_SEMI_SYNC_SLAVE_STATUS"] == "" {
		server.HaveSemiSync = false
	} else {
		server.HaveSemiSync = true
	}
	if server.Status["RPL_SEMI_SYNC_MASTER_STATUS"] == "ON" {
		server.SemiSyncMasterStatus = true
	} else {
		server.SemiSyncMasterStatus = false
	}
	if server.Status["RPL_SEMI_SYNC_SLAVE_STATUS"] == "ON" {
		server.SemiSyncSlaveStatus = true
	} else {
		server.SemiSyncSlaveStatus = false
	}

	if server.Status["WSREP_LOCAL_STATE"] == "4" {
		server.IsWsrepSync = true
	} else {
		server.IsWsrepSync = false
	}

	// Initialize graphite monitoring
	if server.ClusterGroup.conf.GraphiteMetrics {
		go server.SendDatabaseStats(slaveStatus)
	}
	return nil
}

func (server *ServerMonitor) getNamedSlaveStatus(name string) (*dbhelper.SlaveStatus, error) {
	if server.Replications != nil {
		for _, ss := range server.Replications {
			if ss.ConnectionName.String == name {
				return &ss, nil
			}
		}
	}
	return nil, errors.New("Empty replications channels")
}

/* Check replication health and return status string */
func (server *ServerMonitor) replicationCheck() string {
	if server.ClusterGroup.sme.IsInFailover() {
		return "In Failover"
	}
	if (server.State == stateSuspect || server.State == stateFailed) && server.IsSlave == false {
		return "Master OK"
	}

	if server.ClusterGroup.master != nil {
		if server.ServerID == server.ClusterGroup.master.ServerID {
			return "Master OK"
		}
	}
	if server.IsMaintenance {
		server.State = stateMaintenance
		return "Maintenance"
	}
	// when replication stopped Valid is null
	ss, err := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		return "Not a slave"
	}
	if ss.SecondsBehindMaster.Valid == false && server.ClusterGroup.sme.CanMonitor() {

		//	log.Printf("replicationCheck %s %s", server.SQLThread, server.IOThread)
		if ss.SlaveSQLRunning.String == "Yes" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, IO Stopped (%s)", ss.LastIOErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "Yes" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return fmt.Sprintf("NOT OK, SQL Stopped (%s)", ss.LastSQLErrno.String)
		} else if ss.SlaveSQLRunning.String == "No" && ss.SlaveIORunning.String == "No" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveErr
			} else if server.IsRelay {
				server.State = stateRelayErr
			}
			return "NOT OK, ALL Stopped"
		} else if ss.SlaveSQLRunning.String == "Connecting" {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
			return "NOT OK, IO Connecting"
		}

		if server.IsRelay == false && server.IsMaxscale == false {
			server.State = stateSlave
		} else if server.IsRelay {
			server.State = stateRelay
		}
		return "Running OK"
	}

	if ss.SecondsBehindMaster.Int64 > 0 {
		if ss.SecondsBehindMaster.Int64 > server.ClusterGroup.conf.FailMaxDelay && server.ClusterGroup.conf.RplChecks == true {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlaveLate
			} else if server.IsRelay {
				server.State = stateRelayLate
			}

		} else {
			if server.IsRelay == false && server.IsMaxscale == false {
				server.State = stateSlave
			} else if server.IsRelay {
				server.State = stateRelay
			}
		}
		return "Behind master"
	}
	if server.IsRelay == false && server.IsMaxscale == false {
		server.State = stateSlave
	} else if server.IsRelay {
		server.State = stateRelayLate
	}
	return "Running OK"
}

func (sl serverList) checkAllSlavesRunning() bool {
	if len(sl) == 0 {
		return false
	}
	for _, s := range sl {
		ss, sserr := s.getNamedSlaveStatus(s.ReplicationSourceName)
		if sserr != nil {
			return false
		}
		if ss.SlaveSQLRunning.String != "Yes" || ss.SlaveSQLRunning.String != "Yes" {
			return false
		}
	}
	return true
}

/* Check Consistency parameters on server */
func (server *ServerMonitor) acidTest() bool {
	syncBin, _ := dbhelper.GetVariableByName(server.Conn, "SYNC_BINLOG")
	logFlush, _ := dbhelper.GetVariableByName(server.Conn, "INNODB_FLUSH_LOG_AT_TRX_COMMIT")
	if syncBin == "1" && logFlush == "1" {
		return true
	}
	return false
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		server.ClusterGroup.LogPrintf("INFO", "Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := server.ClusterGroup.conf.SwitchWaitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		server.ClusterGroup.LogPrintf("INFO", "Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	maxConn, err = dbhelper.GetVariableByName(server.Conn, "MAX_CONNECTIONS")
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Could not get max_connections value on demoted leader")
	} else {
		_, err = server.Conn.Exec("SET GLOBAL max_connections=0")
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not set max_connections to 0 on demoted leader")
		}
	}
	server.ClusterGroup.LogPrintf("INFO", "Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
}

func (server *ServerMonitor) ReadAllRelayLogs() error {

	server.ClusterGroup.LogPrintf("INFO", "Reading all relay logs on %s", server.URL)
	if server.DBVersion.IsMariaDB() {
		ss, err := dbhelper.GetMSlaveStatus(server.Conn, "")
		if err != nil {
			return err
		}
		myGtid_IO_Pos := gtid.NewList(ss.GtidIOPos.String)
		myGtid_Slave_Pos := gtid.NewList(ss.GtidSlavePos.String)

		//server.ClusterGroup.LogPrintf("INFO", "Status IO_Pos:%s, Slave_Pos:%s equal: %s", myGtid_IO_Pos.Sprint(), myGtid_Slave_Pos.Sprint(), myGtid_Slave_Pos.Equal(myGtid_IO_Pos))

		for myGtid_Slave_Pos.Equal(myGtid_IO_Pos) == false && ss.UsingGtid.String != "" && ss.GtidSlavePos.String != "" {
			ss, err = dbhelper.GetMSlaveStatus(server.Conn, "")
			if err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			myGtid_IO_Pos = gtid.NewList(ss.GtidIOPos.String)
			myGtid_Slave_Pos = gtid.NewList(ss.GtidSlavePos.String)

			server.ClusterGroup.LogPrintf("INFO", "Status IO_Pos:%s, Slave_Pos:%s", ss.GtidIOPos, ss.GtidSlavePos)
		}
	} else {
		ss, err := dbhelper.GetSlaveStatus(server.Conn)
		if err != nil {
			return err
		}
		for ss.MasterLogFile != ss.RelayMasterLogFile && ss.ReadMasterLogPos == ss.ExecMasterLogPos {
			ss, err = dbhelper.GetSlaveStatus(server.Conn)
			if err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	return nil
}

func (server *ServerMonitor) log() {
	server.Refresh()
	server.ClusterGroup.LogPrintf("INFO", "Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s", server.URL, server.CurrentGtid.Sprint(), server.SlaveGtid.Sprint(), server.GTIDBinlogPos.Sprint())
	return
}

func (server *ServerMonitor) Close() {
	server.Conn.Close()
	return
}

func (server *ServerMonitor) writeState() error {
	server.log()
	f, err := os.Create("/tmp/repmgr.state")
	if err != nil {
		return err
	}
	_, err = f.WriteString(server.GTIDBinlogPos.Sprint())
	if err != nil {
		return err
	}
	return nil
}

// check if node see same master as the passed list
func (server *ServerMonitor) HasSiblings(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.getNamedSlaveStatus(sl.ReplicationSourceName)
		if err != nil {
			return false
		}
		ssserver, err := server.getNamedSlaveStatus(server.ReplicationSourceName)
		if err != nil {
			return false
		}
		if sssib.MasterServerID != ssserver.MasterServerID {
			return false
		}
	}
	return true
}

func (server *ServerMonitor) HasSlaves(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		sssib, err := sl.getNamedSlaveStatus(sl.ReplicationSourceName)
		if err == nil {
			if server.ServerID == sssib.MasterServerID && sl.ServerID != server.ServerID {
				return true
			}
		}
	}
	return false
}

func (server *ServerMonitor) HasCycling() bool {
	currentSlave := server
	searchServerID := server.ServerID

	for range server.ClusterGroup.servers {
		currentMaster, _ := server.ClusterGroup.GetMasterFromReplication(currentSlave)
		if currentMaster != nil {
			//	server.ClusterGroup.LogPrintf("INFO", "Cycling my current master id :%d me id:%d", currentMaster.ServerID, currentSlave.ServerID)
			if currentMaster.ServerID == searchServerID {
				return true
			} else {
				currentSlave = currentMaster
			}
		} else {
			return false
		}
	}
	return false
}

// IsDown() returns true is the server is Failed or Suspect
func (server *ServerMonitor) IsDown() bool {
	if server.State == stateFailed || server.State == stateSuspect {
		return true
	}
	return false
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

func (server *ServerMonitor) IsIgnored() bool {

	if misc.Contains(server.ClusterGroup.ignoreList, server.URL) {
		return true
	}
	return false
}

func (server *ServerMonitor) IsIOThreadRunning() bool {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveIORunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) IsSQLThreadRunning() bool {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return false
	}
	if ss.SlaveSQLRunning.String == "Yes" {
		return true
	}
	return false
}

func (server *ServerMonitor) GetReplicationServerID() uint {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.MasterServerID
}

func (server *ServerMonitor) GetReplicationDelay() int64 {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	if ss.SecondsBehindMaster.Valid == false {
		return 0
	}
	return ss.SecondsBehindMaster.Int64
}

func (server *ServerMonitor) GetReplicationHearbeatPeriod() float64 {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return 0
	}
	return ss.SlaveHeartbeatPeriod
}

func (server *ServerMonitor) GetReplicationUsingGtid() string {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return "No"
	}
	return ss.UsingGtid.String
}

func (server *ServerMonitor) GetReplicationMasterHost() string {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return ""
	}
	return ss.MasterHost.String
}

func (server *ServerMonitor) GetReplicationMasterPort() string {
	ss, sserr := server.getNamedSlaveStatus(server.ReplicationSourceName)
	if sserr != nil {
		return "3306"
	}
	return ss.MasterPort.String
}

func (server *ServerMonitor) IsReplicationBroken() bool {
	if server.IsSQLThreadRunning() == false || server.IsIOThreadRunning() == false {
		return true
	}
	return false
}

func (server *ServerMonitor) SetReadOnly() error {
	err := dbhelper.SetReadOnly(server.Conn, true)
	return err
}

func (server *ServerMonitor) SetReadWrite() error {
	err := dbhelper.SetReadOnly(server.Conn, false)
	return err
}
