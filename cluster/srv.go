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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/hpcloud/tail"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/dbhelper"
	"github.com/signal18/replication-manager/gtid"
	"github.com/signal18/replication-manager/httplog"
	"github.com/signal18/replication-manager/slowlog"
	"github.com/signal18/replication-manager/state"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Id                          string                    `json:"id"` //Unique name given by cluster & crc64(URL) used by test to provision
	Name                        string                    `json:"name"`
	Conn                        *sqlx.DB                  `json:"-"`
	User                        string                    `json:"user"`
	Pass                        string                    `json:"-"`
	URL                         string                    `json:"url"`
	DSN                         string                    `json:"-"`
	Host                        string                    `json:"host"`
	Port                        string                    `json:"port"`
	IP                          string                    `json:"ip"`
	Strict                      string                    `json:"strict"`
	ServerID                    uint                      `json:"serverId"`
	LogBin                      string                    `json:"logBin"`
	GTIDBinlogPos               *gtid.List                `json:"gtidBinlogPos"`
	CurrentGtid                 *gtid.List                `json:"currentGtid"`
	SlaveGtid                   *gtid.List                `json:"slaveGtid"`
	IOGtid                      *gtid.List                `json:"ioGtid"`
	FailoverIOGtid              *gtid.List                `json:"failoverIoGtid"`
	GTIDExecuted                string                    `json:"gtidExecuted"`
	ReadOnly                    string                    `json:"readOnly"`
	State                       string                    `json:"state"`
	PrevState                   string                    `json:"prevState"`
	FailCount                   int                       `json:"failCount"`
	FailSuspectHeartbeat        int64                     `json:"failSuspectHeartbeat"`
	SemiSyncMasterStatus        bool                      `json:"semiSyncMasterStatus"`
	SemiSyncSlaveStatus         bool                      `json:"semiSyncSlaveStatus"`
	RplMasterStatus             bool                      `json:"rplMasterStatus"`
	EventScheduler              bool                      `json:"eventScheduler"`
	EventStatus                 []dbhelper.Event          `json:"eventStatus"`
	FullProcessList             []dbhelper.Processlist    `json:"-"`
	ClusterGroup                *Cluster                  `json:"-"` //avoid recusive json
	BinaryLogFile               string                    `json:"binaryLogFile"`
	BinaryLogPos                string                    `json:"binaryLogPos"`
	FailoverMasterLogFile       string                    `json:"failoverMasterLogFile"`
	FailoverMasterLogPos        string                    `json:"failoverMasterLogPos"`
	FailoverSemiSyncSlaveStatus bool                      `json:"failoverSemiSyncSlaveStatus"`
	Process                     *os.Process               `json:"process"`
	HaveSemiSync                bool                      `json:"haveSemiSync"`
	HaveInnodbTrxCommit         bool                      `json:"haveInnodbTrxCommit"`
	HaveSyncBinLog              bool                      `json:"haveSyncBinLog"`
	HaveChecksum                bool                      `json:"haveChecksum"`
	HaveBinlogRow               bool                      `json:"haveBinlogRow"`
	HaveBinlogAnnotate          bool                      `json:"haveBinlogAnnotate"`
	HaveBinlogSlowqueries       bool                      `json:"haveBinlogSlowqueries"`
	HaveBinlogCompress          bool                      `json:"haveBinlogCompress"`
	HaveLogSlaveUpdates         bool                      `json:"haveLogSlaveUpdates"`
	HaveGtidStrictMode          bool                      `json:"haveGtidStrictMode"`
	HaveMySQLGTID               bool                      `json:"haveMysqlGtid"`
	HaveMariaDBGTID             bool                      `json:"haveMariadbGtid"`
	HaveWsrep                   bool                      `json:"haveWsrep"`
	HaveReadOnly                bool                      `json:"haveReadOnly"`
	IsWsrepSync                 bool                      `json:"isWsrepSync"`
	IsWsrepDonor                bool                      `json:"isWsrepDonor"`
	IsMaxscale                  bool                      `json:"isMaxscale"`
	IsRelay                     bool                      `json:"isRelay"`
	IsSlave                     bool                      `json:"isSlave"`
	IsVirtualMaster             bool                      `json:"isVirtualMaster"`
	IsMaintenance               bool                      `json:"isMaintenance"`
	Ignored                     bool                      `json:"ignored"`
	Prefered                    bool                      `json:"prefered"`
	MxsVersion                  int                       `json:"maxscaleVersion"`
	MxsHaveGtid                 bool                      `json:"maxscaleHaveGtid"`
	MxsServerName               string                    `json:"maxscaleServerName"` //Unique server Name in maxscale conf
	MxsServerStatus             string                    `json:"maxscaleServerStatus"`
	ProxysqlHostgroup           string                    `json:"proxysqlHostgroup"`
	RelayLogSize                uint64                    `json:"relayLogSize"`
	Replications                []dbhelper.SlaveStatus    `json:"replications"`
	LastSeenReplications        []dbhelper.SlaveStatus    `json:"lastSeenReplications"`
	MasterStatus                dbhelper.MasterStatus     `json:"masterStatus"`
	ReplicationSourceName       string                    `json:"replicationSourceName"`
	DBVersion                   *dbhelper.MySQLVersion    `json:"dbVersion"`
	QPS                         int64                     `json:"qps"`
	ReplicationHealth           string                    `json:"replicationHealth"`
	TestConfig                  string                    `json:"testConfig"`
	Variables                   map[string]string         `json:"variables"`
	EngineInnoDB                map[string]string         `json:"engineInnodb"`
	ErrorLog                    httplog.HttpLog           `json:"errorLog"`
	SlowLog                     slowlog.SlowLog           `json:"slowLog"`
	Status                      map[string]string         `json:"-"`
	Queries                     map[string]string         `json:"-"` //PFS queries
	DictTables                  map[string]dbhelper.Table `json:"-"`
	Users                       map[string]dbhelper.Grant `json:"-"`
	ErrorLogTailer              *tail.Tail                `json:"-"`
	SlowLogTailer               *tail.Tail                `json:"-"`
	PrevStatus                  map[string]string         `json:"-"`
	PrevMonitorTime             int64                     `json:"-"`
	MonitorTime                 int64                     `json:"-"`
	Version                     int                       `json:"-"`
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
	stateWsrep       string = "Wsrep"
	stateWsrepDonor  string = "WsrepDonor"
	stateWsrepLate   string = "WsrepLate"
)

/* Initializes a server object */
func (cluster *Cluster) newServerMonitor(url string, user string, pass string, conf string, name string) (*ServerMonitor, error) {
	crcTable := crc64.MakeTable(crc64.ECMA)
	server := new(ServerMonitor)
	server.ClusterGroup = cluster
	server.Name = name
	if server.Name == "" {
		server.Name = url
	}
	server.Id = "db" + strconv.FormatUint(crc64.Checksum([]byte(cluster.Name+server.Name), crcTable), 10)
	if url == "" {
		url = server.Id + "." + server.ClusterGroup.Conf.ProvCodeApp + ".svc." + server.ClusterGroup.Conf.ProvNetCNICluster + ":3306"
	}

	server.SetCredential(url, user, pass)
	server.ReplicationSourceName = cluster.Conf.MasterConn
	server.TestConfig = conf
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

	server.State = stateSuspect
	server.PrevState = stateSuspect

	errLogFile := server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/" + server.Id + "_log_error.log"
	slowLogFile := server.ClusterGroup.Conf.WorkingDir + "/" + server.ClusterGroup.Name + "/" + server.Id + "_log_slow_query.log"
	if _, err := os.Stat(errLogFile); os.IsNotExist(err) {
		nofile, _ := os.OpenFile(errLogFile, os.O_WRONLY|os.O_CREATE, 0600)
		nofile.Close()
	}
	if _, err := os.Stat(slowLogFile); os.IsNotExist(err) {
		nofile, _ := os.OpenFile(errLogFile, os.O_WRONLY|os.O_CREATE, 0600)
		nofile.Close()
	}
	server.ErrorLogTailer, _ = tail.TailFile(errLogFile, tail.Config{Follow: true, ReOpen: true})
	server.SlowLogTailer, _ = tail.TailFile(slowLogFile, tail.Config{Follow: true, ReOpen: true})
	server.ErrorLog = httplog.NewHttpLog(20)
	server.SlowLog = slowlog.NewSlowLog(20)
	go server.ErrorLogWatcher()
	go server.SlowLogWatcher()
	server.SetIgnored(cluster.IsInIgnoredHosts(server))
	server.SetPrefered(cluster.IsInPreferedHosts(server))
	var err error
	server.IP, err = dbhelper.CheckHostAddr(server.Host)
	if err != nil {
		errmsg := fmt.Errorf("ERROR: DNS resolution error for host %s", server.Host)
		return server, errmsg
	}

	server.Conn, err = sqlx.Open("mysql", server.DSN)

	return server, err
}

func (server *ServerMonitor) Ping(wg *sync.WaitGroup) {

	defer wg.Done()
	if server.ClusterGroup.sme.IsInFailover() {
		server.ClusterGroup.LogPrintf(LvlDbg, "Inside failover, skip server check")
		return
	}

	if server.ClusterGroup.vmaster != nil {
		if server.ClusterGroup.vmaster.ServerID == server.ServerID {
			server.IsVirtualMaster = true
		} else {
			server.IsVirtualMaster = false
		}
	}
	var conn *sqlx.DB
	var err error
	switch server.ClusterGroup.Conf.CheckType {
	case "tcp":
		conn, err = sqlx.Connect("mysql", server.DSN)
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
		server.ClusterGroup.LogPrintf(LvlDbg, "Failure detection handling for server %s", server.URL)
		if driverErr, ok := err.(*mysql.MySQLError); ok {
			// access denied
			if driverErr.Number == 1045 {
				server.State = stateUnconn
				server.ClusterGroup.SetState("ERR00004", state.State{ErrType: "ERROR", ErrDesc: fmt.Sprintf(clusterError["ERR00004"], server.URL, err.Error()), ErrFrom: "TOPO"})
			}
		}
		if err != sql.ErrNoRows {
			server.FailCount++
			if server.ClusterGroup.master != nil && server.URL == server.ClusterGroup.master.URL {
				server.FailSuspectHeartbeat = server.ClusterGroup.sme.GetHeartbeats()
				if server.ClusterGroup.master.FailCount <= server.ClusterGroup.Conf.MaxFail {
					server.ClusterGroup.LogPrintf("INFO", "Master Failure detected! Retry %d/%d", server.ClusterGroup.master.FailCount, server.ClusterGroup.Conf.MaxFail)
				}
				if server.FailCount >= server.ClusterGroup.Conf.MaxFail {
					if server.FailCount == server.ClusterGroup.Conf.MaxFail {
						server.ClusterGroup.LogPrintf("INFO", "Declaring master as failed")
					}
					server.ClusterGroup.master.State = stateFailed
				} else {
					server.ClusterGroup.master.State = stateSuspect
				}
			} else {
				// not the master
				server.ClusterGroup.LogPrintf(LvlDbg, "Failure detection of no master FailCount %d MaxFail %d", server.FailCount, server.ClusterGroup.Conf.MaxFail)
				if server.FailCount >= server.ClusterGroup.Conf.MaxFail {
					if server.FailCount == server.ClusterGroup.Conf.MaxFail {
						server.ClusterGroup.LogPrintf("INFO", "Declaring server %s as failed", server.URL)
						server.State = stateFailed
						// remove from slave list
						server.delete(&server.ClusterGroup.slaves)
						if server.Replications != nil {
							server.LastSeenReplications = server.Replications
						}
						server.Replications = nil
					}
				} else {
					server.State = stateSuspect
				}
			}
		}
		// Send alert if state has changed
		if server.PrevState != server.State {
			//if cluster.Conf.Verbose {
			server.ClusterGroup.LogPrintf("ALERT", "Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
			server.ClusterGroup.backendStateChangeProxies()
			server.SendAlert()
		}
		if server.PrevState != server.State {
			server.PrevState = server.State
		}
		return
	}
	// reaffect the connection if we lost it
	if server.Conn == nil {
		server.Conn = conn
	} else {
		defer conn.Close()
	}
	// from here we have connection
	server.Refresh()

	// Reset FailCount
	if (server.State != stateFailed && server.State != stateUnconn && server.State != stateSuspect) && (server.FailCount > 0) /*&& (((server.ClusterGroup.sme.GetHeartbeats() - server.FailSuspectHeartbeat) * server.ClusterGroup.Conf.MonitoringTicker) > server.ClusterGroup.Conf.FailResetTime)*/ {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}

	var ss dbhelper.SlaveStatus
	ss, errss := dbhelper.GetSlaveStatus(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
	// We have no replicatieon can this be the old master
	//  1617 is no multi source channel found
	noChannel := false
	if errss != nil {
		if strings.Contains(errss.Error(), "1617") {
			noChannel = true
		}
	}
	if errss == sql.ErrNoRows || noChannel {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.
		if server.PrevState == stateFailed {
			server.ClusterGroup.LogPrintf(LvlDbg, "State comparison reinitialized failed server %s as unconnected", server.URL)
			if server.ClusterGroup.Conf.ReadOnly && server.HaveWsrep == false && server.ClusterGroup.IsDiscovered() {
				if server.ClusterGroup.master != nil {
					if server.ClusterGroup.Status == ConstMonitorActif && server.ClusterGroup.master.Id != server.Id {
						server.ClusterGroup.LogPrintf(LvlInfo, "Setting Read Only on unconnected server %s as actif monitor and other master dicovereds", server.URL)
						server.SetReadOnly()
					} else if server.ClusterGroup.Status == ConstMonitorStandby {
						server.ClusterGroup.LogPrintf(LvlInfo, "Setting Read Only on unconnected server %s as a standby monitor ", server.URL)
						server.SetReadOnly()
					}
				}
			}
			server.State = stateUnconn
			server.FailCount = 0
			if server.ClusterGroup.Conf.Autorejoin && server.ClusterGroup.IsActive() {
				server.RejoinMaster()
			} else {
				server.ClusterGroup.LogPrintf("INFO", "Auto Rejoin is disabled")
			}

		} else if server.State != stateMaster && server.PrevState != stateUnconn {
			server.ClusterGroup.LogPrintf(LvlDbg, "State unconnected set by non-master rule on server %s", server.URL)
			if server.ClusterGroup.Conf.ReadOnly && server.HaveWsrep == false && server.ClusterGroup.IsDiscovered() {
				server.ClusterGroup.LogPrintf(LvlInfo, "Setting Read Only on unconnected server: %s no master state and found replication", server.URL)
				server.SetReadOnly()
			}
			server.State = stateUnconn
		}

		if server.PrevState != server.State {
			server.PrevState = server.State
		}
		return
	} else if server.ClusterGroup.IsActive() && errss == nil && (server.PrevState == stateFailed || server.PrevState == stateSuspect) {
		server.rejoinSlave(ss)
	}

	if server.PrevState != server.State {
		server.PrevState = server.State
	}
}

// Refresh a server object
func (server *ServerMonitor) Refresh() error {
	if server.Conn == nil {
		//	server.State = stateFailed
		return errors.New("Connection is closed, server unreachable")
	}
	if server.Conn.Unsafe() == nil {
		//	server.State = stateFailed
		return errors.New("Connection is closed, server unreachable")
	}
	conn, err := sqlx.Connect("mysql", server.DSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	if server.ClusterGroup.Conf.MxsBinlogOn {
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

	if !(server.ClusterGroup.Conf.MxsBinlogOn && server.IsMaxscale) {
		// maxscale don't support show variables
		server.PrevMonitorTime = server.MonitorTime
		server.MonitorTime = time.Now().Unix()
		server.Variables, err = dbhelper.GetVariables(server.Conn)

		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get variables %s", err)
			return err
		}
		server.Version = dbhelper.MariaDBVersion(server.Variables["VERSION"]) // Deprecated
		server.DBVersion, err = dbhelper.GetDBVersion(server.Conn)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get database version")
		}

		if server.Variables["EVENT_SCHEDULER"] != "ON" {
			server.EventScheduler = false
		} else {
			server.EventScheduler = true
		}
		server.Strict = server.Variables["GTID_STRICT_MODE"]
		server.LogBin = server.Variables["LOG_BIN"]
		server.ReadOnly = server.Variables["READ_ONLY"]
		if server.Variables["READ_ONLY"] != "ON" {
			server.HaveReadOnly = false
		} else {
			server.HaveReadOnly = true
		}
		if server.Variables["LOG_BIN_COMPRESS"] != "ON" {
			server.HaveBinlogCompress = false
		} else {
			server.HaveBinlogCompress = true
		}
		if server.Variables["GTID_STRICT_MODE"] != "ON" {
			server.HaveGtidStrictMode = false
		} else {
			server.HaveGtidStrictMode = true
		}
		if server.Variables["LOG_SLAVE_UPDATES"] != "ON" {
			server.HaveLogSlaveUpdates = false
		} else {
			server.HaveLogSlaveUpdates = true
		}
		if server.Variables["INNODB_FLUSH_LOG_AT_TRX_COMMIT"] != "1" {
			server.HaveInnodbTrxCommit = false
		} else {
			server.HaveInnodbTrxCommit = true
		}
		if server.Variables["SYNC_BINLOG"] != "1" {
			server.HaveSyncBinLog = false
		} else {
			server.HaveSyncBinLog = true
		}
		if server.Variables["INNODB_CHECKSUM"] == "NONE" {
			server.HaveChecksum = false
		} else {
			server.HaveChecksum = true
		}
		if server.Variables["BINLOG_FORMAT"] != "ROW" {
			server.HaveBinlogRow = false
		} else {
			server.HaveBinlogRow = true
		}
		if server.Variables["BINLOG_ANNOTATE_ROW_EVENTS"] != "ON" {
			server.HaveBinlogAnnotate = false
		} else {
			server.HaveBinlogAnnotate = true
		}
		if server.Variables["LOG_SLOW_SLAVE_STATEMENTS"] != "ON" {
			server.HaveBinlogSlowqueries = false
		} else {
			server.HaveBinlogSlowqueries = true
		}
		if server.Variables["WSREP_ON"] != "ON" {
			server.HaveWsrep = false
		} else {
			server.HaveWsrep = true
		}
		if server.Variables["ENFORCE_GTID_CONSISTENCY"] == "ON" && server.Variables["GTID_MODE"] == "ON" {
			server.HaveMySQLGTID = true
		}

		server.RelayLogSize, _ = strconv.ParseUint(server.Variables["RELAY_LOG_SPACE_LIMIT"], 10, 64)

		if server.DBVersion.IsMariaDB() {
			server.GTIDBinlogPos = gtid.NewList(server.Variables["GTID_BINLOG_POS"])
			server.CurrentGtid = gtid.NewList(server.Variables["GTID_CURRENT_POS"])
			server.SlaveGtid = gtid.NewList(server.Variables["GTID_SLAVE_POS"])
		} else {
			server.GTIDBinlogPos = gtid.NewMySQLList(server.Variables["GTID_EXECUTED"])
			server.GTIDExecuted = server.Variables["GTID_EXECUTED"]
			server.CurrentGtid = gtid.NewMySQLList(server.Variables["GTID_EXECUTED"])
			server.SlaveGtid = gtid.NewList(server.Variables["GTID_SLAVE_POS"])
		}

		var sid uint64
		sid, err = strconv.ParseUint(server.Variables["SERVER_ID"], 10, 64)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not parse server_id, reason: %s", err)
		}
		server.ServerID = uint(sid)

		server.EventStatus, err = dbhelper.GetEventStatus(server.Conn)
		if err != nil {
			server.ClusterGroup.LogPrintf("ERROR", "Could not get events")
		}
		// get Users
		server.Users, _ = dbhelper.GetUsers(server.Conn)
		if server.ClusterGroup.Conf.MonitorScheduler {
			server.JobsCheckRunning()
		}
		server.FullProcessList, _ = dbhelper.GetProcesslist(server.Conn)
	}

	// SHOW MASTER STATUS
	server.MasterStatus, err = dbhelper.GetMasterStatus(server.Conn)
	if err != nil {
		// binary log might be closed for that server
	} else {
		server.BinaryLogFile = server.MasterStatus.File
		server.BinaryLogPos = strconv.FormatUint(uint64(server.MasterStatus.Position), 10)
	}
	if server.ClusterGroup.Conf.GraphiteEmbedded {
		// SHOW ENGINE INNODB STATUS
		server.EngineInnoDB, err = dbhelper.GetEngineInnoDB(server.Conn)
		// GET PFS query digest
		server.Queries, err = dbhelper.GetQueries(server.Conn)
	}

	// Set channel source name is dangerous with multi cluster

	// SHOW SLAVE STATUS

	if !(server.ClusterGroup.Conf.MxsBinlogOn && server.IsMaxscale) && server.DBVersion.IsMariaDB() {
		server.Replications, err = dbhelper.GetAllSlavesStatus(server.Conn)
	} else {
		server.Replications, err = dbhelper.GetChannelSlaveStatus(server.Conn)
	}
	if err != nil {
		server.ClusterGroup.LogPrintf("ERROR", "Could not get slaves status %s", err)
		return err
	}
	// select a replication status get an err if repliciations array is empty
	slaveStatus, err := server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		// Do not reset  server.MasterServerID = 0 as we may need it for recovery
		server.IsSlave = false
	} else {
		server.IsSlave = true
		if slaveStatus.UsingGtid.String == "Slave_Pos" || slaveStatus.UsingGtid.String == "Current_Pos" {
			server.HaveMariaDBGTID = true
		} else {
			server.HaveMariaDBGTID = false
		}
		if server.DBVersion.IsMySQL57() && server.HasGTIDReplication() {
			server.SlaveGtid = gtid.NewList(slaveStatus.ExecutedGtidSet.String)
		}
	}
	server.ReplicationHealth = server.CheckReplication()
	// if MaxScale exit at fetch variables and status part as not supported
	if server.ClusterGroup.Conf.MxsBinlogOn && server.IsMaxscale {
		return nil
	}
	server.PrevStatus = server.Status
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
	if server.Status["WSREP_LOCAL_STATE"] == "2" {
		server.IsWsrepDonor = true
	} else {
		server.IsWsrepDonor = false
	}
	if len(server.PrevStatus) > 0 {
		qps, _ := strconv.ParseInt(server.Status["QUERIES"], 10, 64)
		prevqps, _ := strconv.ParseInt(server.PrevStatus["QUERIES"], 10, 64)
		if server.MonitorTime-server.PrevMonitorTime > 0 {
			server.QPS = (qps - prevqps) / (server.MonitorTime - server.PrevMonitorTime)
		}
	}

	// Initialize graphite monitoring
	if server.ClusterGroup.Conf.GraphiteMetrics {
		go server.SendDatabaseStats(slaveStatus)
	}
	return nil
}

/* Handles write freeze and existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	err := dbhelper.SetReadOnly(server.Conn, true)
	if err != nil {
		server.ClusterGroup.LogPrintf("INFO", "Could not set %s as read-only: %s", server.URL, err)
		return false
	}
	for i := server.ClusterGroup.Conf.SwitchWaitKill; i > 0; i -= 500 {
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
		server.Refresh()
		myGtid_IO_Pos := gtid.NewList(ss.GtidIOPos.String)
		myGtid_Slave_Pos := server.SlaveGtid
		//myGtid_Slave_Pos := gtid.NewList(ss.GtidSlavePos.String)
		//https://jira.mariadb.org/browse/MDEV-14182

		for myGtid_Slave_Pos.Equal(myGtid_IO_Pos) == false && ss.UsingGtid.String != "" && ss.GtidSlavePos.String != "" {
			server.Refresh()
			ss, err = dbhelper.GetMSlaveStatus(server.Conn, server.ClusterGroup.Conf.MasterConn)
			if err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			myGtid_IO_Pos = gtid.NewList(ss.GtidIOPos.String)
			myGtid_Slave_Pos = server.SlaveGtid

			server.ClusterGroup.LogPrintf("INFO", "Status IO_Pos:%s, Slave_Pos:%s", myGtid_IO_Pos.Sprint(), myGtid_Slave_Pos.Sprint())
		}
	} else {
		ss, err := dbhelper.GetSlaveStatus(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
		if err != nil {
			return err
		}
		for ss.MasterLogFile != ss.RelayMasterLogFile && ss.ReadMasterLogPos == ss.ExecMasterLogPos {
			ss, err = dbhelper.GetSlaveStatus(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
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

func (server *ServerMonitor) StopSlave() error {
	return dbhelper.StopSlave(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
}

func (server *ServerMonitor) StartSlave() error {
	return dbhelper.StartSlave(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())

}

func (server *ServerMonitor) StopSlaveIOThread() error {
	return dbhelper.StopSlaveIOThread(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
}

func (server *ServerMonitor) StopSlaveSQLThread() error {
	return dbhelper.StopSlaveSQLThread(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
}

func (server *ServerMonitor) ResetSlave() error {
	return dbhelper.ResetSlave(server.Conn, true, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
}

func (server *ServerMonitor) FlushTables() error {
	return dbhelper.FlushTables(server.Conn)
}

func (server *ServerMonitor) Uprovision() {
	server.ClusterGroup.OpenSVCUnprovisionDatabaseService(server)
}

func (server *ServerMonitor) Provision() {
	server.ClusterGroup.OpenSVCProvisionDatabaseService(server)
}

func (server *ServerMonitor) SkipReplicationEvent() {
	server.StopSlave()
	dbhelper.SkipBinlogEvent(server.Conn, server.ClusterGroup.Conf.MasterConn, server.DBVersion.IsMariaDB(), server.DBVersion.IsMySQL())
	server.StartSlave()
}
