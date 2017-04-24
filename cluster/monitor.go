// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"errors"
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
	"github.com/tanji/replication-manager/maxscale"
	"github.com/tanji/replication-manager/state"

	"github.com/tanji/replication-manager/misc"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Conn                        *sqlx.DB
	URL                         string
	DSN                         string `json:"-"`
	Host                        string
	Port                        string
	IP                          string
	GTIDBinlogPos               *gtid.List
	Strict                      string
	ServerID                    uint
	MasterServerID              uint
	MasterHost                  string
	MasterPort                  string
	LogBin                      string
	UsingGtid                   string
	CurrentGtid                 *gtid.List
	SlaveGtid                   *gtid.List
	IOGtid                      *gtid.List
	IOThread                    string
	SQLThread                   string
	ReadOnly                    string
	Delay                       sql.NullInt64
	State                       string
	PrevState                   string
	IOErrno                     uint
	IOError                     string
	SQLErrno                    uint
	SQLError                    string
	FailCount                   int
	FailSuspectHeartbeat        int64
	SemiSyncMasterStatus        bool
	SemiSyncSlaveStatus         bool
	RplMasterStatus             bool
	EventScheduler              bool
	EventStatus                 []dbhelper.Event
	ClusterGroup                *Cluster
	MasterLogFile               string
	MasterLogPos                string
	MasterHeartbeatPeriod       float64
	MasterUseGtid               string
	BinaryLogFile               string
	BinaryLogPos                string
	FailoverMasterLogFile       string
	FailoverMasterLogPos        string
	FailoverSemiSyncSlaveStatus bool
	FailoverIOGtid              *gtid.List
	Process                     *os.Process
	Name                        string //Unique name given by cluster & sha1(URL) used by test to provision
	Conf                        string //Unique Conf given by reg test initMariaDB
	MxsServerName               string //Unique server Name in maxscale conf
	MxsServerStatus             string
	MxsServerConnections        int
	HaveSemiSync                bool
	HaveInnodbTrxCommit         bool
	HaveSyncBinLog              bool
	HaveChecksum                bool
	HaveBinlogRow               bool
	HaveBinlogAnnotate          bool
	HaveBinlogSlowqueries       bool
	HaveBinlogCompress          bool
	Version                     int
	IsMaxscale                  bool
	IsRelay                     bool
	IsSlave                     bool
	IsMaintenance               bool
	MxsVersion                  int
	MxsHaveGtid                 bool
	RelayLogSize                uint64
	Replications                []dbhelper.SlaveStatus
	DBVersion                   *dbhelper.MySQLVersion
	GTIDExecuted                string
}

type serverList []*ServerMonitor

var maxConn string

const (
	stateFailed      string = "Failed"
	stateMaster      string = "Master"
	stateSlave       string = "Slave"
	stateSlaveErr    string = "SlaveErr"
	stateSlaveLate   string = "SlaveLate"
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
func (cluster *Cluster) newServerMonitor(url string) (*ServerMonitor, error) {

	server := new(ServerMonitor)
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
	servertohash := sha1.Sum([]byte(server.URL))
	server.Name = cluster.cfgGroup + "_" + hex.EncodeToString(servertohash[:])

	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", server.Host)
		return server, errmsg
	}
	params := fmt.Sprintf("?timeout=%ds", cluster.conf.Timeout)
	mydsn := func() string {
		dsn := cluster.dbUser + ":" + cluster.dbPass + "@"
		if server.Host != "" {
			dsn += "tcp(" + server.Host + ":" + server.Port + ")/" + params
		} else {
			dsn += "unix(" + cluster.conf.Socket + ")/" + params
		}
		return dsn
	}
	server.DSN = mydsn()
	server.Conn, err = sqlx.Open("mysql", server.DSN)
	return server, err
}

func (server *ServerMonitor) check(wg *sync.WaitGroup) {

	defer wg.Done()
	if server.ClusterGroup.sme.IsInFailover() {
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG: Inside failover, skip server check")
		}
		return
	}

	if server.ClusterGroup.conf.LogLevel > 2 {
		// LogPrint("DEBUG: Checking server", server.Host)
	}

	var err error
	switch server.ClusterGroup.conf.CheckType {
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
		if server.ClusterGroup.conf.LogLevel > 2 {
			server.ClusterGroup.LogPrintf("DEBUG: Failure detection handling for server %s", server.URL)
		}
		if err != sql.ErrNoRows && server.ClusterGroup.master != nil {
			server.FailCount++
			if server.URL == server.ClusterGroup.master.URL {
				server.FailSuspectHeartbeat = server.ClusterGroup.sme.GetHeartbeats()
				if server.ClusterGroup.master.FailCount <= server.ClusterGroup.conf.MaxFail {
					server.ClusterGroup.LogPrintf("WARN : Master Failure detected! Retry %d/%d", server.ClusterGroup.master.FailCount, server.ClusterGroup.conf.MaxFail)
				}
				if server.FailCount >= server.ClusterGroup.conf.MaxFail {
					if server.FailCount == server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrint("WARN : Declaring master as failed")
					}
					server.ClusterGroup.master.State = stateFailed
				} else {
					server.ClusterGroup.master.State = stateSuspect
				}
			} else {
				// not the master
				if server.ClusterGroup.conf.LogLevel > 2 {
					server.ClusterGroup.LogPrintf("DEBUG: Failure detection of no master FailCount %d MaxFail %d", server.FailCount, server.ClusterGroup.conf.MaxFail)
				}
				if server.FailCount >= server.ClusterGroup.conf.MaxFail {
					if server.FailCount == server.ClusterGroup.conf.MaxFail {
						server.ClusterGroup.LogPrintf("WARN : Declaring server %s as failed", server.URL)
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
			server.ClusterGroup.LogPrintf("ALERT : Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
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
					server.ClusterGroup.LogPrint("ERROR: Could not send econf.Mail alert: ", err)
				}
			}
		}
		//return

	}
	// Reset FailCount
	/*	if conf.Verbose>0  {
		server.ClusterGroup.LogPrintf("DEBUG: State comparison %b %b %b %d %d %d ", server.State==stateMaster , server.State==stateSlave  , server.State==stateUnconn ,(server.FailCount > 0) ,((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * conf.MonitoringTicker), conf.FailResetTime) {
	}*/
	if (server.State != stateUnconn && server.State != stateSuspect) && (server.FailCount > 0) && (((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * server.ClusterGroup.conf.MonitoringTicker) > server.ClusterGroup.conf.FailResetTime) {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}
	var ss dbhelper.SlaveStatus
	ss, errss := dbhelper.GetSlaveStatus(server.Conn)
	// We have no replication can this be the old master
	if errss == sql.ErrNoRows {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if server.PrevState == stateFailed {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG: State comparison reinitialized failed server %s as unconnected", server.URL)
			}
			server.State = stateUnconn
			server.FailCount = 0
			if server.ClusterGroup.conf.Autorejoin {
				server.Rejoin()
			} else {
				server.ClusterGroup.LogPrintf("DEBUG: Auto Rejoin is disable")
			}
		} else if server.State != stateMaster {
			if server.ClusterGroup.conf.LogLevel > 1 {
				server.ClusterGroup.LogPrintf("DEBUG: State unconnected set by non-master rule on server %s", server.URL)
			}
			server.State = stateUnconn
		}
		if server.PrevState != server.State {
			server.PrevState = server.State
		}
		return
	} else if errss == nil && server.PrevState == stateFailed {
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
		return errors.New("Connection is close server Unreachable ")
	}
	err := server.Conn.Ping()
	if err != nil {
		return err
	}
	if server.ClusterGroup.conf.MxsBinlogOn {
		mxsversion, _ := dbhelper.GetMaxscaleVersion(server.Conn)
		if mxsversion != "" {
			server.ClusterGroup.LogPrintf("................FoundMaxscale")
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

	masterStatus, err := dbhelper.GetMasterStatus(server.Conn)
	if err != nil {
		// binary log might be closed for that server
	} else {
		server.BinaryLogFile = masterStatus.File
		server.BinaryLogPos = strconv.FormatUint(uint64(masterStatus.Position), 10)
	}

	server.Replications, err = dbhelper.GetAllSlavesStatus(server.Conn)
	if err != nil {
		//	server.ClusterGroup.LogPrintf("ERROR: Could not get show all slaves status on %s", server.DSN)
	}
	slaveStatus, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		server.IsSlave = false
		//	server.ClusterGroup.LogPrintf("ERROR: Could not get show slave status on %s", server.DSN)
		server.UsingGtid = ""
		server.IOThread = "No"
		server.SQLThread = "No"
		server.Delay = sql.NullInt64{Int64: 0, Valid: false}
		//server.MasterServerID = 0 Do not reset as we may need it for recovery
		server.MasterHost = ""
		server.MasterPort = "3306"
		server.IOErrno = 0
		server.IOError = ""
		server.SQLError = ""
		server.SQLErrno = 0
		server.MasterLogFile = ""
		server.MasterLogPos = "0"
		server.MasterHeartbeatPeriod = 0
		server.MasterUseGtid = "No"
	} else {
		server.IsSlave = true
		server.IOGtid = gtid.NewList(slaveStatus.Gtid_IO_Pos)
		server.UsingGtid = slaveStatus.Using_Gtid
		server.IOThread = slaveStatus.Slave_IO_Running
		server.SQLThread = slaveStatus.Slave_SQL_Running
		server.Delay = slaveStatus.Seconds_Behind_Master
		if slaveStatus.Master_Server_Id != 0 {
			server.MasterServerID = slaveStatus.Master_Server_Id
		}
		server.MasterHost = slaveStatus.Master_Host
		server.MasterPort = strconv.FormatUint(uint64(slaveStatus.Master_Port), 10)
		server.IOErrno = slaveStatus.Last_IO_Errno
		server.IOError = slaveStatus.Last_IO_Error
		server.SQLError = slaveStatus.Last_SQL_Error
		server.SQLErrno = slaveStatus.Last_SQL_Errno
		server.MasterLogFile = slaveStatus.Master_Log_File
		server.MasterUseGtid = slaveStatus.Using_Gtid
		server.MasterHeartbeatPeriod = slaveStatus.Slave_heartbeat_period
		server.MasterLogPos = strconv.FormatUint(uint64(slaveStatus.Read_Master_Log_Pos), 10)
	}

	// if MaxScale exit the variables and status part
	if server.ClusterGroup.conf.MxsBinlogOn && server.IsMaxscale {
		return nil
	}
	sv, err := dbhelper.GetVariables(server.Conn)
	if err != nil {
		return err
	}
	server.Version = dbhelper.MariaDBVersion(sv["VERSION"]) // Deprecated
	server.DBVersion, err = dbhelper.GetDBVersion(server.Conn)
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR: Could not get Database Version")
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
	sid, err := strconv.ParseUint(sv["SERVER_ID"], 10, 64)
	if err != nil {
		server.ClusterGroup.LogPrint("ERROR: Could not parse server_id, reason: ", err)
	}
	server.ServerID = uint(sid)
	err = dbhelper.SetDefaultMasterConn(server.Conn, server.ClusterGroup.conf.MasterConn)
	if err != nil {
		return err
	}
	server.EventStatus, err = dbhelper.GetEventStatus(server.Conn)
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR: Could not get events")
	}
	su, _ := dbhelper.GetStatus(server.Conn)
	//server.ClusterGroup.LogPrintf("ERROR: %s %s %s", su["RPL_SEMI_SYNC_MASTER_STATUS"], su["RPL_SEMI_SYNC_SLAVE_STATUS"], server.URL)
	if su["RPL_SEMI_SYNC_MASTER_STATUS"] == "" || su["RPL_SEMI_SYNC_SLAVE_STATUS"] == "" {
		server.HaveSemiSync = false
	} else {
		server.HaveSemiSync = true
	}
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

	//monitor haproxy
	if server.ClusterGroup.conf.HaproxyOn {
		// status, err := haproxy.parse(page)
	}

	// Initialize graphite monitoring
	if server.ClusterGroup.conf.GraphiteMetrics {
		graph, err := graphite.NewGraphite(server.ClusterGroup.conf.GraphiteCarbonHost, server.ClusterGroup.conf.GraphiteCarbonPort)

		if err == nil {
			var metrics = make([]graphite.Metric, 5)
			metrics[0] = graphite.NewMetric(fmt.Sprintf("server%d.replication.delay", server.ServerID), fmt.Sprintf("%d", server.Delay.Int64), time.Now().Unix())
			metrics[1] = graphite.NewMetric(fmt.Sprintf("server%d.status.Queries", server.ServerID), su["QUERIES"], time.Now().Unix())
			metrics[2] = graphite.NewMetric(fmt.Sprintf("server%d.status.ThreadsRunning", server.ServerID), su["THREADS_RUNNING"], time.Now().Unix())
			metrics[3] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesOut", server.ServerID), su["BYTES_SENT"], time.Now().Unix())
			metrics[4] = graphite.NewMetric(fmt.Sprintf("server%d.status.BytesIn", server.ServerID), su["BYTES_RECEIVED"], time.Now().Unix())
			//	metrics[5] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[6] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[7] = graphite.NewMetric(, time.Now().Unix())
			//	metrics[8] = graphite.NewMetric(, time.Now().Unix())
			graph.SendMetrics(metrics)

			graph.Disconnect()
		}
	}
	return nil
}

func (server *ServerMonitor) getMaxscaleInfos(m *maxscale.MaxScale) {
	if server.ClusterGroup.conf.MxsOn == false {
		return
	}
	if server.ClusterGroup.conf.MxsGetInfoMethod == "maxinfo" {

		_, err := m.GetMaxInfoServers("http://" + server.ClusterGroup.conf.MxsHost + ":" + strconv.Itoa(server.ClusterGroup.conf.MxsMaxinfoPort) + "/servers")
		if err != nil {
			server.ClusterGroup.sme.AddState("ERR00020", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00020"], server.URL), ErrFrom: "MON"})
		}
		srvport, _ := strconv.Atoi(server.Port)
		server.MxsServerName, server.MxsServerStatus, server.MxsServerConnections = m.GetMaxInfoServer(server.Host, srvport)
	} else {

		_, err := m.ListServers()
		if err != nil {
			server.ClusterGroup.sme.AddState("ERR00019", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00019"], server.URL), ErrFrom: "MON"})
		} else {
			//		server.ClusterGroup.LogPrint("get MaxScale server list")
			var connections string
			server.MxsServerName, connections, server.MxsServerStatus = m.GetServer(server.IP, server.Port)
			server.MxsServerConnections, _ = strconv.Atoi(connections)
		}
	}

}

/* Check replication health and return status string */
func (server *ServerMonitor) replicationCheck() string {
	if server.ClusterGroup.sme.IsInFailover() || server.State == stateSuspect || server.State == stateFailed || server.IsSlave == false {
		return "Master OK"
	}

	if server.ClusterGroup.master != nil {
		if server.ServerID == server.ClusterGroup.master.ServerID {
			return "Master OK"
		}
	}

	if server.IsRelay == false && server.IsMaxscale == false {

		if server.Delay.Valid == false && server.ClusterGroup.sme.CanMonitor() {

			//	log.Printf("replicationCheck %s %s", server.SQLThread, server.IOThread)
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
			if server.Delay.Int64 > server.ClusterGroup.conf.SwitchMaxDelay && server.ClusterGroup.conf.RplChecks == true {
				server.State = stateSlaveLate
			} else {
				server.State = stateSlave
			}
			return "Behind master"
		}
		server.State = stateSlave
	}
	if server.IsRelay {
		if server.Delay.Valid == false && server.ClusterGroup.sme.CanMonitor() {
			if server.SQLThread == "Yes" && server.IOThread == "No" {
				server.State = stateRelayErr
				return fmt.Sprintf("NOT OK, IO Stopped (%d)", server.IOErrno)
			} else if server.SQLThread == "No" && server.IOThread == "Yes" {
				server.State = stateRelayErr
				return fmt.Sprintf("NOT OK, SQL Stopped (%d)", server.SQLErrno)
			} else if server.SQLThread == "No" && server.IOThread == "No" {
				server.State = stateRelayErr
				return "NOT OK, ALL Stopped"
			} else if server.IOThread == "Connecting" {
				server.State = stateRelay
				return "NOT OK, IO Connecting"
			}
			server.State = stateRelay
			return "Running OK"
		}
		if server.Delay.Int64 > 0 {
			if server.Delay.Int64 > server.ClusterGroup.conf.SwitchMaxDelay && server.ClusterGroup.conf.RplChecks == true {
				server.State = stateRelayLate
			} else {
				server.State = stateRelay
			}
			return "Behind master"
		}
		server.State = stateRelay
	}
	return "Running OK"
}

func (sl serverList) checkAllSlavesRunning() bool {
	if len(sl) == 0 {
		return false
	}
	for _, s := range sl {
		if s.SQLThread != "Yes" || s.IOThread != "Yes" {
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
		server.ClusterGroup.LogPrintf("WARN : Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := server.ClusterGroup.conf.SwitchWaitKill; i > 0; i -= 500 {
		threads := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		if threads == 0 {
			break
		}
		server.ClusterGroup.LogPrintf("INFO : Waiting for %d write threads to complete on %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	maxConn, err = dbhelper.GetVariableByName(server.Conn, "MAX_CONNECTIONS")
	if err != nil {
		server.ClusterGroup.LogPrint("ERROR: Could not get max_connections value on demoted leader")
	} else {
		_, err = server.Conn.Exec("SET GLOBAL max_connections=0")
		if err != nil {
			server.ClusterGroup.LogPrint("ERROR: Could not set max_connections to 0 on demoted leader")
		}
	}
	server.ClusterGroup.LogPrintf("INFO : Terminating all threads on %s", server.URL)
	dbhelper.KillThreads(server.Conn)
	return true
}

func (server *ServerMonitor) ReadAllRelayLogs() error {
	ss, err := dbhelper.GetSlaveStatus(server.Conn)
	if err != nil {
		return err
	}
	server.ClusterGroup.LogPrintf("INFO : Reading all relay logs on %s", server.URL)
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
	server.Refresh()
	server.ClusterGroup.LogPrintf("DEBUG: Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s", server.URL, server.CurrentGtid.Sprint(), server.SlaveGtid.Sprint(), server.GTIDBinlogPos.Sprint())
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
		if server.MasterServerID != sl.MasterServerID {
			return false
		}
	}
	return true
}

func (server *ServerMonitor) HasSlaves(sib []*ServerMonitor) bool {
	for _, sl := range sib {
		if server.ServerID == sl.MasterServerID && sl.ServerID != server.ServerID {
			return true
		}
	}
	return false
}

func (server *ServerMonitor) HasCycling(ServerID uint) bool {

	mycurrentmaster, _ := server.ClusterGroup.GetMasterFromReplication(server)
	if mycurrentmaster != nil {
		if mycurrentmaster.ServerID == ServerID {
			return true
		} else {
			mycurrentmaster.HasCycling(ServerID)
		}

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
