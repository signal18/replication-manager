// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc64"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"

	"github.com/go-sql-driver/mysql"
	"github.com/hpcloud/tail"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	v3 "github.com/signal18/replication-manager/repmanv3"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/gtid"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// ServerMonitor defines a server to monitor.
type ServerMonitor struct {
	Id                          string                  `json:"id"` //Unique name given by cluster & crc64(URL) used by test to provision
	Name                        string                  `json:"name"`
	Domain                      string                  `json:"domain"` // Use to store orchestrator CNI domain .<cluster_name>.svc.<cluster_name>
	ServiceName                 string                  `json:"serviceName"`
	SourceClusterName           string                  `json:"sourceClusterName"` //Used to idenfied server added from other clusters linked with multi source
	Conn                        *sqlx.DB                `json:"-"`
	User                        string                  `json:"user"`
	Pass                        string                  `json:"-"`
	URL                         string                  `json:"url"`
	DSN                         string                  `json:"-"`
	Host                        string                  `json:"host"`
	Port                        string                  `json:"port"`
	TunnelPort                  string                  `json:"tunnelPort"`
	IP                          string                  `json:"ip"`
	Strict                      string                  `json:"strict"`
	ServerID                    uint64                  `json:"serverId"`
	HashUUID                    uint64                  `json:"hashUUID"`
	DomainID                    uint64                  `json:"domainId"`
	GTIDBinlogPos               *gtid.List              `json:"gtidBinlogPos"`
	CurrentGtid                 *gtid.List              `json:"currentGtid"`
	SlaveGtid                   *gtid.List              `json:"slaveGtid"`
	IOGtid                      *gtid.List              `json:"ioGtid"`
	FailoverIOGtid              *gtid.List              `json:"failoverIoGtid"`
	GTIDExecuted                string                  `json:"gtidExecuted"`
	ReadOnly                    string                  `json:"readOnly"`
	State                       string                  `json:"state"`
	PrevState                   string                  `json:"prevState"`
	FailCount                   int                     `json:"failCount"`
	FailSuspectHeartbeat        int64                   `json:"failSuspectHeartbeat"`
	ClusterGroup                *Cluster                `json:"-"` //avoid recusive json
	BinaryLogFile               string                  `json:"binaryLogFile"`
	BinaryLogFilePrevious       string                  `json:"binaryLogFilePrevious"`
	BinaryLogPos                string                  `json:"binaryLogPos"`
	FailoverMasterLogFile       string                  `json:"failoverMasterLogFile"`
	FailoverMasterLogPos        string                  `json:"failoverMasterLogPos"`
	FailoverSemiSyncSlaveStatus bool                    `json:"failoverSemiSyncSlaveStatus"`
	Process                     *os.Process             `json:"process"`
	SemiSyncMasterStatus        bool                    `json:"semiSyncMasterStatus"`
	SemiSyncSlaveStatus         bool                    `json:"semiSyncSlaveStatus"`
	HaveHealthyReplica          bool                    `json:"HaveHealthyReplica"`
	HaveEventScheduler          bool                    `json:"eventScheduler"`
	HaveSemiSync                bool                    `json:"haveSemiSync"`
	HaveInnodbTrxCommit         bool                    `json:"haveInnodbTrxCommit"`
	HaveChecksum                bool                    `json:"haveInnodbChecksum"`
	HaveLogGeneral              bool                    `json:"haveLogGeneral"`
	HaveBinlog                  bool                    `json:"haveBinlog"`
	HaveBinlogSync              bool                    `json:"haveBinLogSync"`
	HaveBinlogRow               bool                    `json:"haveBinlogRow"`
	HaveBinlogAnnotate          bool                    `json:"haveBinlogAnnotate"`
	HaveBinlogSlowqueries       bool                    `json:"haveBinlogSlowqueries"`
	HaveBinlogCompress          bool                    `json:"haveBinlogCompress"`
	HaveBinlogSlaveUpdates      bool                    `json:"HaveBinlogSlaveUpdates"`
	HaveGtidStrictMode          bool                    `json:"haveGtidStrictMode"`
	HaveMySQLGTID               bool                    `json:"haveMysqlGtid"`
	HaveMariaDBGTID             bool                    `json:"haveMariadbGtid"`
	HaveSlowQueryLog            bool                    `json:"haveSlowQueryLog"`
	HavePFSSlowQueryLog         bool                    `json:"havePFSSlowQueryLog"`
	HaveMetaDataLocksLog        bool                    `json:"haveMetaDataLocksLog"`
	HaveQueryResponseTimeLog    bool                    `json:"haveQueryResponseTimeLog"`
	HaveDiskMonitor             bool                    `json:"haveDiskMonitor"`
	HaveSQLErrorLog             bool                    `json:"haveSQLErrorLog"`
	HavePFS                     bool                    `json:"havePFS"`
	HaveWsrep                   bool                    `json:"haveWsrep"`
	HaveReadOnly                bool                    `json:"haveReadOnly"`
	HaveNoMasterOnStart         bool                    `json:"haveNoMasterOnStart"`
	HaveSlaveIdempotent         bool                    `json:"haveSlaveIdempotent"`
	HaveSlaveOptimistic         bool                    `json:"haveSlaveOptimistic "`
	HaveSlaveSerialized         bool                    `json:"haveSlaveSerialized"`
	HaveSlaveAggressive         bool                    `json:"haveSlaveAggressive"`
	HaveSlaveMinimal            bool                    `json:"haveSlaveMinimal"`
	HaveSlaveConservative       bool                    `json:"haveSlaveConservative"`
	IsWsrepSync                 bool                    `json:"isWsrepSync"`
	IsWsrepDonor                bool                    `json:"isWsrepDonor"`
	IsWsrepPrimary              bool                    `json:"isWsrepPrimary"`
	IsMaxscale                  bool                    `json:"isMaxscale"`
	IsRelay                     bool                    `json:"isRelay"`
	IsSlave                     bool                    `json:"isSlave"`
	IsGroupReplicationSlave     bool                    `json:"isGroupReplicationSlave"`
	IsGroupReplicationMaster    bool                    `json:"isGroupReplicationMaster"`
	IsVirtualMaster             bool                    `json:"isVirtualMaster"`
	IsMaintenance               bool                    `json:"isMaintenance"`
	IsCompute                   bool                    `json:"isCompute"` //Used to idenfied spider compute nide
	IsDelayed                   bool                    `json:"isDelayed"`
	IsFull                      bool                    `json:"isFull"`
	IsConfigGen                 bool                    `json:"isConfigGen"`
	Ignored                     bool                    `json:"ignored"`
	IgnoredRO                   bool                    `json:"ignoredRO"`
	Prefered                    bool                    `json:"prefered"`
	PreferedBackup              bool                    `json:"preferedBackup"`
	InCaptureMode               bool                    `json:"inCaptureMode"`
	LongQueryTimeSaved          string                  `json:"longQueryTimeSaved"`
	LongQueryTime               string                  `json:"longQueryTime"`
	LogOutput                   string                  `json:"logOutput"`
	SlowQueryLog                string                  `json:"slowQueryLog"`
	SlowQueryCapture            bool                    `json:"slowQueryCapture"`
	BinlogDumpThreads           int                     `json:"binlogDumpThreads"`
	MxsVersion                  int                     `json:"maxscaleVersion"`
	MxsHaveGtid                 bool                    `json:"maxscaleHaveGtid"`
	MxsServerName               string                  `json:"maxscaleServerName"` //Unique server Name in maxscale conf
	MxsServerStatus             string                  `json:"maxscaleServerStatus"`
	ProxysqlHostgroup           string                  `json:"proxysqlHostgroup"`
	RelayLogSize                uint64                  `json:"relayLogSize"`
	Replications                []dbhelper.SlaveStatus  `json:"replications"`
	LastSeenReplications        []dbhelper.SlaveStatus  `json:"lastSeenReplications"`
	MasterStatus                dbhelper.MasterStatus   `json:"masterStatus"`
	SlaveStatus                 *dbhelper.SlaveStatus   `json:"-"`
	ReplicationSourceName       string                  `json:"replicationSourceName"`
	DBVersion                   *dbhelper.MySQLVersion  `json:"dbVersion"`
	Version                     int                     `json:"-"`
	QPS                         int64                   `json:"qps"`
	ReplicationHealth           string                  `json:"replicationHealth"`
	EventStatus                 []dbhelper.Event        `json:"eventStatus"`
	FullProcessList             []dbhelper.Processlist  `json:"-"`
	Variables                   *config.StringsMap      `json:"-"`
	EngineInnoDB                *config.StringsMap      `json:"engineInnodb"`
	ErrorLog                    s18log.HttpLog          `json:"errorLog"`
	SlowLog                     s18log.SlowLog          `json:"-"`
	Status                      *config.StringsMap      `json:"-"`
	PrevStatus                  *config.StringsMap      `json:"-"`
	PFSQueries                  *config.PFSQueriesMap   `json:"-"` //PFS queries
	SlowPFSQueries              *config.PFSQueriesMap   `json:"-"` //PFS queries from slow
	DictTables                  *config.TablesMap       `json:"-"`
	Tables                      []v3.Table              `json:"-"`
	Disks                       []dbhelper.Disk         `json:"-"`
	Plugins                     *config.PluginsMap      `json:"-"`
	Users                       *config.GrantsMap       `json:"-"`
	MetaDataLocks               []dbhelper.MetaDataLock `json:"-"`
	ErrorLogTailer              *tail.Tail              `json:"-"`
	SlowLogTailer               *tail.Tail              `json:"-"`
	MonitorTime                 int64                   `json:"-"`
	PrevMonitorTime             int64                   `json:"-"`
	maxConn                     string                  `json:"maxConn"` // used to back max connection for failover
	Datadir                     string                  `json:"datadir"`
	SlapOSDatadir               string                  `json:"slaposDatadir"`
	PostgressDB                 string                  `json:"postgressDB"`
	TLSConfigUsed               string                  `json:"tlsConfigUsed"` //used to track TLS config during key rotation
	SSTPort                     string                  `json:"sstPort"`       //used to send data to dbjobs
	Agent                       string                  `json:"agent"`         //used to provision service in orchestrator
	BinaryLogFiles              *config.UIntsMap        `json:"binaryLogFiles"`
	BinaryLogFileOldest         string                  `json:"binaryLogFileOldest"`
	BinaryLogOldestTimestamp    int64                   `json:"binaryLogOldestTimestamp"`
	BinaryLogPurgeBefore        int64                   `json:"binaryLogPurgeBefore"`
	MaxSlowQueryTimestamp       int64                   `json:"maxSlowQueryTimestamp"`
	WorkLoad                    *config.WorkLoadsMap    `json:"workLoad"`
	DelayStat                   *ServerDelayStat        `json:"delayStat"`
	SlaveVariables              SlaveVariables          `json:"slaveVariables"`
	MDevIssues                  ServerBug               `json:"mdevIssues"`
	IsCheckedForMDevIssues      bool                    `json:"isCheckedForMdevIssues"`
	IsInSlowQueryCapture        bool
	IsInPFSQueryCapture         bool
	InPurgingBinaryLog          bool
	IsBackingUpBinaryLog        bool
	IsRefreshingBinlog          bool
	ActiveTasks                 sync.Map
	BinaryLogDir                string
	DBDataDir                   string
}

type ServerBug struct {
	Replication []string
	Service     []string
}

func (sb *ServerBug) HasMdevBug(key string) bool {
	for _, r := range sb.Replication {
		if r == key {
			return true
		}
	}

	for _, s := range sb.Service {
		if s == key {
			return true
		}
	}

	return false
}

type SlaveVariables struct {
	SlaveParallelMaxQueued int    `json:"slaveParallelMaxQueued"`
	SlaveParallelMode      string `json:"slaveParallelMode"`
	SlaveParallelThreads   int    `json:"slaveParallelThreads"`
	SlaveParallelWorkers   int    `json:"slaveParallelWorkers"`
	SlaveTypeConversions   string `json:"slaveTypeConversions"`
}

type serverList []*ServerMonitor

const (
	stateFailed       string = "Failed"
	stateMaster       string = "Master"
	stateSlave        string = "Slave"
	stateSlaveErr     string = "SlaveErr"
	stateSlaveLate    string = "SlaveLate"
	stateMaintenance  string = "Maintenance"
	stateUnconn       string = "StandAlone"
	stateErrorAuth    string = "ErrorAuth"
	stateSuspect      string = "Suspect"
	stateShard        string = "Shard"
	stateProv         string = "Provision"
	stateMasterAlone  string = "MasterAlone"
	stateRelay        string = "Relay"
	stateRelayErr     string = "RelayErr"
	stateRelayLate    string = "RelayLate"
	stateWsrep        string = "Wsrep"
	stateWsrepDonor   string = "WsrepDonor"
	stateWsrepLate    string = "WsrepUnsync"
	stateProxyRunning string = "ProxyRunning"
	stateProxyDesync  string = "ProxyDesync"
)

const (
	ConstTLSNoConfig      string = ""
	ConstTLSOldConfig     string = "&tls=tlsconfigold"
	ConstTLSCurrentConfig string = "&tls=tlsconfig"
)

/* Initializes a server object compute if spider node*/
func (cluster *Cluster) newServerMonitor(url string, user string, pass string, compute bool, domain string) (*ServerMonitor, error) {
	var err error
	server := new(ServerMonitor)
	server.QPS = 0
	server.IsCompute = compute
	server.Domain = domain
	server.TLSConfigUsed = ConstTLSCurrentConfig
	server.ClusterGroup = cluster
	server.DBVersion, _ = dbhelper.NewMySQLVersion("Unknowed-0.0.0", "")
	server.Name, server.Port, server.PostgressDB = misc.SplitHostPortDB(url)
	server.ServiceName = cluster.Name + "/svc/" + server.Name
	server.IsGroupReplicationSlave = false
	server.IsGroupReplicationMaster = false
	if cluster.Conf.ProvNetCNI && cluster.GetOrchestrator() == config.ConstOrchestratorOpenSVC {
		// OpenSVC and Sharding proxy monitoring
		if server.IsCompute {
			if cluster.Conf.ClusterHead != "" {
				url = server.Name + "." + cluster.Conf.ClusterHead + ".svc." + cluster.Conf.ProvOrchestratorCluster + ":3306"
			} else {
				url = server.Name + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster + ":3306"
			}
		}
		url = server.Name + server.Domain + ":3306"
	}
	var sid uint64
	//will be overide in Refresh with show variables server_id, used for provisionning configurator for server_id
	sid, err = strconv.ParseUint(strconv.FormatUint(crc64.Checksum([]byte(server.Name+server.Port), server.GetCluster().GetCrcTable()), 10), 10, 64)
	server.ServerID = sid
	server.Id = fmt.Sprintf("%s%d", "db", sid)

	if cluster.Conf.TunnelHost != "" {
		go server.Tunnel()
	}

	server.SetCredential(url, user, pass)
	server.ReplicationSourceName = cluster.Conf.MasterConn

	// Initiate sync.Map pointers
	server.Variables = config.NewStringsMap()
	server.EngineInnoDB = config.NewStringsMap()
	server.Status = config.NewStringsMap()
	server.PrevStatus = config.NewStringsMap()
	server.PFSQueries = config.NewPFSQueriesMap()
	server.SlowPFSQueries = config.NewPFSQueriesMap()
	server.DictTables = config.NewTablesMap()
	server.Plugins = config.NewPluginsMap()
	server.Users = config.NewGrantsMap()
	server.BinaryLogFiles = config.NewUIntsMap()
	server.WorkLoad = config.NewWorkLoadsMap()

	server.HaveSemiSync = true
	server.HaveInnodbTrxCommit = true
	server.HaveChecksum = true
	server.HaveBinlogSync = true
	server.HaveBinlogRow = true
	server.HaveBinlogAnnotate = true
	server.HaveBinlogCompress = true
	server.HaveBinlogSlowqueries = true
	server.MxsHaveGtid = false
	// consider all nodes are maxscale to avoid sending command until discoverd
	server.IsRelay = false
	server.IsMaxscale = true
	server.IsDelayed = server.IsInDelayedHost()
	// NOTE: does this make sense to set the state to the same?
	server.SetPrevState(stateSuspect)
	server.SetState(stateSuspect)

	server.Datadir = cluster.Conf.WorkingDir + "/" + cluster.Name + "/" + server.Host + "_" + server.Port
	if _, err := os.Stat(server.Datadir); os.IsNotExist(err) {
		os.MkdirAll(server.Datadir, os.ModePerm)
		os.MkdirAll(server.Datadir+"/log", os.ModePerm)
		os.MkdirAll(server.Datadir+"/var", os.ModePerm)
		os.MkdirAll(server.Datadir+"/init", os.ModePerm)
	}

	errLogFile := server.Datadir + "/log/log_error.log"
	slowLogFile := server.Datadir + "/log/log_slow_query.log"
	if _, err := os.Stat(errLogFile); os.IsNotExist(err) {
		nofile, _ := os.OpenFile(errLogFile, os.O_WRONLY|os.O_CREATE, 0600)
		nofile.Close()
	}
	if _, err := os.Stat(slowLogFile); os.IsNotExist(err) {
		nofile, _ := os.OpenFile(slowLogFile, os.O_WRONLY|os.O_CREATE, 0600)
		nofile.Close()
	}
	server.ErrorLogTailer, _ = tail.TailFile(errLogFile, tail.Config{Follow: true, ReOpen: true})
	server.SlowLogTailer, _ = tail.TailFile(slowLogFile, tail.Config{Follow: true, ReOpen: true})
	server.ErrorLog = s18log.NewHttpLog(cluster.Conf.MonitorErrorLogLength)
	server.SlowLog = s18log.NewSlowLog(cluster.Conf.MonitorLongQueryLogLength)
	go server.ErrorLogWatcher()
	go server.SlowLogWatcher()
	server.SetIgnored(cluster.IsInIgnoredHosts(server))
	server.SetIgnoredReadonly(cluster.IsInIgnoredReadonly(server))
	server.SetPreferedBackup(cluster.IsInPreferedBackupHosts(server))
	server.SetPrefered(cluster.IsInPreferedHosts(server))
	server.ReloadSaveInfosVariables()
	server.DelayStat = new(ServerDelayStat)
	server.DelayStat.ResetDelayStat()

	server.CurrentWorkLoad()
	server.WorkLoad.Set("max", server.WorkLoad.Get("current"))
	server.WorkLoad.Set("average", server.WorkLoad.Get("current"))

	/*if cluster.Conf.MasterSlavePgStream || cluster.Conf.MasterSlavePgLogical {
		server.Conn, err = sqlx.Open("postgres", server.DSN)
	} else {
		server.Conn, err = sqlx.Open("mysql", server.DSN)
	}*/
	return server, err
}

func (server *ServerMonitor) Ping(wg *sync.WaitGroup) {
	cluster := server.ClusterGroup
	defer wg.Done()

	if cluster.vmaster != nil {
		if cluster.vmaster.ServerID == server.ServerID {
			server.IsVirtualMaster = true
		} else {
			server.IsVirtualMaster = false
		}
	}
	var conn *sqlx.DB
	var err error
	switch cluster.Conf.CheckType {
	case "tcp":
		conn, err = server.GetNewDBConn()
	case "agent":
		var resp *http.Response
		resp, err = http.Get("http://" + server.Host + ":10001/check/")
		if resp.StatusCode != 200 {
			// if 404, consider server down or agent killed. Don't initiate anything
			err = fmt.Errorf("HTTP Response Code Error: %d", resp.StatusCode)
		}
	}
	// manage IP based DNS may failed if backend server as changed IP  try to resolv it and recreate new DSN
	//server.SetCredential(server.URL, server.User, server.Pass)
	// Handle failure cases here
	if err != nil {
		// Copy the last known server states or they will be cleared at next monitoring loop
		if server.State != stateFailed {
			cluster.StateMachine.CopyOldStateFromUnknowServer(server.URL)
		}
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlDbg, "Failure detection handling for server %s %s", server.URL, err)
		// cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,config.LvlErr, "Failure detection handling for server %s %s", server.URL, err)

		if driverErr, ok := err.(*mysql.MySQLError); ok {
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlDbg, "Driver Error %s %d ", server.URL, driverErr.Number)

			// access denied
			if driverErr.Number == 1045 {
				server.SetState(stateErrorAuth)
				cluster.SetState("ERR00004", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00004"], server.URL, err.Error()), ErrFrom: "SRV"})
				//if vault and credential change, then repare
				server.CheckMonitoringCredentialsRotation()
				return
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Driver Error %s %d ", server.URL, driverErr.Number)
			}
		}
		if err != sql.ErrNoRows {
			server.FailCount++
			if cluster.master == nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Master not defined")
			}
			if cluster.GetMaster() != nil && server.URL == cluster.GetMaster().URL && server.GetCluster().GetTopology() != topoUnknown {
				server.FailSuspectHeartbeat = cluster.StateMachine.GetHeartbeats()
				if cluster.GetMaster().FailCount <= cluster.Conf.MaxFail {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Master Failure detected! Retry %d/%d", cluster.GetMaster().FailCount, cluster.Conf.MaxFail)
				}
				if server.FailCount >= cluster.Conf.MaxFail {
					if server.FailCount == cluster.Conf.MaxFail {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Declaring db master as failed %s", server.URL)
					}
					cluster.GetMaster().SetState(stateFailed)
					server.DelWaitStopCookie()
					server.DelUnprovisionCookie()
				} else {
					cluster.GetMaster().SetState(stateSuspect)

				}
			} else {
				// not the master or a virtual master
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Failure detection of no master FailCount %d MaxFail %d", server.FailCount, cluster.Conf.MaxFail)
				if server.FailCount >= cluster.Conf.MaxFail && server.GetCluster().GetTopology() != topoUnknown {
					if server.FailCount == cluster.Conf.MaxFail {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Declaring replica %s as failed", server.URL)
						server.SetState(stateFailed)
						server.DelWaitStopCookie()
						server.DelUnprovisionCookie()

						// if wsrep could enter here but still server is not a slave
						// Remove from slave list if exists
						if server.Replications != nil && cluster.slaves != nil {
							server.LastSeenReplications = server.Replications
							server.delete(&cluster.slaves)
						}
						server.Replications = nil
					}
				} else {
					server.SetState(stateSuspect)
					////////
				}
			}
		}
		// Send alert if state has changed
		if server.PrevState != server.State {
			//if cluster.Conf.Verbose {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
			if server.State != stateSuspect {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "Server %s state changed from %s to %s", server.URL, server.PrevState, server.State)
				cluster.backendStateChangeProxies()
				server.SendAlert()
				server.ProcessFailedSlave()
			}
		}
		if server.PrevState != server.State {
			server.SetPrevState(server.State)
		}
		return
	}

	// From here we have a new connection

	//Without topology we should never declare a server failed
	if (server.State == stateErrorAuth || server.State == stateFailed) && server.GetCluster().GetTopology() == topoUnknown && server.PrevState != stateSuspect {
		server.SetState(stateSuspect)
	}

	// For orchestrator to trigger a start via tracking state URL
	if server.PrevState == stateFailed {
		server.DelWaitStartCookie()
		server.DelRestartCookie()
		server.DelProvisionCookie()
		server.DelReprovisionCookie()

	}

	// reaffect a global DB pool object if we never get it , ex dynamic seeding
	if server.Conn == nil {
		server.Conn = conn
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Assigning a global connection on server %s", server.URL)
		return
	}
	// We will leave when in failover to avoid refreshing variables and status
	if cluster.StateMachine.IsInFailover() {
		//	conn.Close()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Inside failover, skiping refresh")
		return
	}
	err = server.Refresh()
	if err != nil {
		// reaffect a global DB pool object if we never get it , ex dynamic seeding
		server.Conn = conn
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server refresh failed but ping connect %s", err)
		return
	}

	defer conn.Close()

	// Reset FailCount
	if (server.State != stateFailed && server.State != stateErrorAuth && server.State != stateSuspect) && (server.FailCount > 0) /*&& (((cluster.StateMachine.GetHeartbeats() - server.FailSuspectHeartbeat) * cluster.Conf.MonitoringTicker) > cluster.Conf.FailResetTime)*/ {
		server.FailCount = 0
		server.FailSuspectHeartbeat = 0
	}
	//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, "niac %s: %s", server.URL, server.DBVersion)
	var ss dbhelper.SlaveStatus
	ss, _, errss := dbhelper.GetSlaveStatus(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
	// We have no replicatieon can this be the old master
	//  1617 is no multi source channel found
	noChannel := false
	if errss != nil {
		if strings.Contains(errss.Error(), "1617") || strings.Contains(errss.Error(), "3074") {
			// This is a special case when using muti source there is a error instead of empty resultset when no replication is defined on channel
			//	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral,LvlInfo, " server: %s replication no channel err 1617 %s ", server.URL, errss)
			noChannel = true
		}
	}
	if errss == sql.ErrNoRows || noChannel {
		// If we reached this stage with a previously failed server, reintroduce
		// it as unconnected server.master
		if server.PrevState == stateFailed || server.PrevState == stateErrorAuth /*|| server.PrevState == stateSuspect*/ {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "State changed, init failed server %s as unconnected", server.URL)
			if cluster.Conf.ReadOnly && !server.HaveWsrep && cluster.IsDiscovered() {
				//GetMaster abstract master for galera multi master and master slave
				if server.GetCluster().GetMaster() != nil {
					if cluster.Status == ConstMonitorActif && server.GetCluster().GetMaster().Id != server.Id && !server.IsIgnoredReadonly() && !cluster.IsInFailover() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Read Only on unconnected server %s as active monitor and other master is discovered", server.URL)
						server.SetReadOnly()
					} else if cluster.Status == ConstMonitorStandby && cluster.Conf.Arbitration && !server.IsIgnoredReadonly() && !cluster.IsInFailover() {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Read Only on unconnected server %s as a standby monitor ", server.URL)
						server.SetReadOnly()
					}
				}
			}

			if cluster.Topology == topoActivePassive {
				server.SetState(stateMaster)
			} else if cluster.GetTopology() != topoMultiMasterWsrep || cluster.GetTopology() != topoMultiMasterGrouprep {
				if server.IsGroupReplicationSlave {
					server.SetState(stateSlave)
				} else {
					server.SetState(stateUnconn)
				}
			}
			server.FailCount = 0
			cluster.backendStateChangeProxies()
			server.SendAlert()
			if cluster.Conf.Autorejoin && cluster.IsActive() {
				server.RejoinMaster()
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Auto Rejoin is disabled")
			}

		} else if server.State != stateMaster && server.PrevState != stateUnconn && server.State == stateUnconn {
			// Master will never get discovery in topology if it does not get unconnected first it default to suspect
			//	if cluster.GetTopology() != topoMultiMasterWsrep {
			if server.IsGroupReplicationSlave {
				server.SetState(stateSlave)
			} else {
				server.SetState(stateUnconn)
			}

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "From state %s to unconnected and non leader on server %s", server.PrevState, server.URL)
			//	}
			if cluster.Conf.ReadOnly && !server.HaveWsrep && cluster.IsDiscovered() && !server.IsIgnoredReadonly() && !cluster.IsInFailover() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Setting Read Only on unconnected server: %s no master state and replication found", server.URL)
				server.SetReadOnly()
			}

			if server.State != stateSuspect {
				cluster.backendStateChangeProxies()
				server.SendAlert()
			}
		} else if server.GetCluster().GetMaster() != nil && server.GetCluster().GetMaster().Id != server.Id && server.PrevState == stateSuspect && !server.HaveWsrep && cluster.IsDiscovered() && !cluster.IsInFailover() {
			// a case of a standalone transite to suspect but never get to standalone back
			if server.IsGroupReplicationSlave {
				server.SetState(stateSlave)
			} else {
				server.SetState(stateUnconn)
			}
		} else if server.GetCluster().GetTopology() == topoActivePassive {
			if server.PrevState == stateSuspect || (server.PrevState == stateMaintenance && !server.IsMaintenance) {
				//if active-passive topo and no replication, put the state at standalone
				server.SetState(stateMaster)
			}
		}
	} else if cluster.IsActive() && errss == nil && (server.PrevState == stateFailed) {
		// Is Slave
		server.rejoinSlave(ss)
	}

	if server.PrevState != server.State {
		server.SetPrevState(server.State)
		if server.PrevState != stateSuspect {
			cluster.backendStateChangeProxies()
			server.SendAlert()
		}
	}
}

func (server *ServerMonitor) ProcessFailedSlave() {
	cluster := server.ClusterGroup
	if server.State == stateSlaveErr {
		if cluster.Conf.ReplicationErrorScript != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Calling replication error script")
			var out []byte
			out, err := exec.Command(cluster.Conf.ReplicationErrorScript, server.URL, server.PrevState, server.State).CombinedOutput()
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "%s", err)
			}
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Replication error script complete:", string(out))
		}
		if server.HasReplicationSQLThreadRunning() && cluster.Conf.ReplicationRestartOnSQLErrorMatch != "" {
			ss, err := server.GetSlaveStatus(server.ReplicationSourceName)
			if err != nil {
				return
			}
			matched, err := regexp.Match(cluster.Conf.ReplicationRestartOnSQLErrorMatch, []byte(ss.LastSQLError.String))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "ERROR", "Rexep failed replication-restart-on-sqlerror-match %s %s", cluster.Conf.ReplicationRestartOnSQLErrorMatch, err)
			} else if matched {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Rexep restart slave  %s  matching: %s", cluster.Conf.ReplicationRestartOnSQLErrorMatch, ss.LastSQLError.String)
				server.SkipReplicationEvent()
				server.StartSlave()
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Skip event and restart slave on %s", server.URL)
			}
		}
	}
}

var start_time time.Time

// Refresh a server object
func (server *ServerMonitor) Refresh() error {
	cluster := server.ClusterGroup
	var err error

	var cpu_usage_dt int64

	cpu_usage_dt = 1

	if server.Conn == nil {
		return errors.New("Connection is nil, server unreachable")
	}
	if server.Conn.Unsafe() == nil {
		//	server.State = stateFailed
		return errors.New("Connection is unsafe, server unreachable")
	}
	err = server.Conn.Ping()
	if err != nil {
		return err
	}
	server.CheckVersion()

	if cluster.Conf.MxsBinlogOn {
		mxsversion, _ := dbhelper.GetMaxscaleVersion(server.Conn)
		if mxsversion != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Found Maxscale")
			server.IsMaxscale = true
			server.IsRelay = true
			server.MxsVersion = dbhelper.MariaDBVersion(mxsversion)
			server.SetState(stateRelay)
		} else {
			server.IsMaxscale = false
		}
	} else {
		server.IsMaxscale = false
	}
	if !(cluster.Conf.MxsBinlogOn && server.IsMaxscale) {
		// maxscale don't support show variables
		server.PrevMonitorTime = server.MonitorTime
		server.MonitorTime = time.Now().Unix()
		logs := ""
		server.DBVersion, logs, err = dbhelper.GetDBVersion(server.Conn)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlErr, "Could not get database version %s %s", server.URL, err)

		vars, logs, err := dbhelper.GetVariables(server.Conn, server.DBVersion)
		server.Variables = config.FromNormalStringMap(server.Variables, vars)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlErr, "Could not get database variables %s %s", server.URL, err)
		if err != nil {
			return nil
		}
		if !server.DBVersion.IsPostgreSQL() {
			if cluster.Conf.MultiMasterGrouprep {
				server.IsGroupReplicationMaster, err = dbhelper.IsGroupReplicationMaster(server.Conn, server.DBVersion, server.Host)
				server.IsGroupReplicationSlave, err = dbhelper.IsGroupReplicationSlave(server.Conn, server.DBVersion, server.Host)
				if server.IsGroupReplicationSlave && server.State == stateUnconn {
					server.SetState(stateSlave)
				}
			}
			server.HaveEventScheduler = server.HasEventScheduler()
			server.Strict = server.Variables.Get("GTID_STRICT_MODE")
			server.ReadOnly = server.Variables.Get("READ_ONLY")
			server.LongQueryTime = server.Variables.Get("LONG_QUERY_TIME")
			server.LogOutput = server.Variables.Get("LOG_OUTPUT")
			server.SlowQueryLog = server.Variables.Get("SLOW_QUERY_LOG")
			server.HaveReadOnly = server.HasReadOnly()
			server.HaveSlaveIdempotent = server.HasSlaveIndempotent()
			server.HaveSlaveOptimistic = server.HasSlaveParallelOptimistic()
			server.HaveSlaveSerialized = server.HasSlaveParallelSerialized()
			server.HaveSlaveAggressive = server.HasSlaveParallelAggressive()
			server.HaveSlaveMinimal = server.HasSlaveParallelMinimal()
			server.HaveSlaveConservative = server.HasSlaveParallelConservative()
			server.HaveBinlog = server.HasBinlog()
			server.HaveBinlogRow = server.HasBinlogRow()
			server.HaveBinlogAnnotate = server.HasBinlogRowAnnotate()
			server.HaveBinlogSync = server.HasBinlogDurable()
			server.HaveBinlogCompress = server.HasBinlogCompress()
			server.HaveBinlogSlaveUpdates = server.HasBinlogSlaveUpdates()
			server.HaveBinlogSlowqueries = server.HasBinlogSlowSlaveQueries()
			server.HaveGtidStrictMode = server.HasGtidStrictMode()
			server.HaveInnodbTrxCommit = server.HasInnoDBRedoLogDurable()
			server.HaveChecksum = server.HasInnoDBChecksum()
			server.HaveWsrep = server.HasWsrep()
			server.HaveSlowQueryLog = server.HasLogSlowQuery()
			server.HavePFS = server.HasLogPFS()
			if server.HavePFS {
				server.HavePFSSlowQueryLog = server.HasLogPFSSlowQuery()
			}
			server.HaveMySQLGTID = server.HasMySQLGTID()
			server.RelayLogSize, _ = strconv.ParseUint(server.Variables.Get("RELAY_LOG_SPACE_LIMIT"), 10, 64)
			server.SlaveVariables = server.GetSlaveVariables()

			if server.DBVersion.IsMariaDB() {
				server.GTIDBinlogPos = gtid.NewList(server.Variables.Get("GTID_BINLOG_POS"))
				server.CurrentGtid = gtid.NewList(server.Variables.Get("GTID_CURRENT_POS"))
				server.SlaveGtid = gtid.NewList(server.Variables.Get("GTID_SLAVE_POS"))

				sid, err := strconv.ParseUint(server.Variables.Get("GTID_DOMAIN_ID"), 10, 64)
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not parse domain_id, reason: %s", err)
				} else {
					server.DomainID = uint64(sid)
				}

			} else {
				server.GTIDBinlogPos = gtid.NewMySQLList(server.Variables.Get("GTID_EXECUTED"), server.GetCluster().GetCrcTable())
				server.GTIDExecuted = server.Variables.Get("GTID_EXECUTED")
				server.CurrentGtid = server.GTIDBinlogPos
				server.SlaveGtid = gtid.NewList(server.Variables.Get("GTID_SLAVE_POS"))
				server.HashUUID = crc64.Checksum([]byte(strings.ToUpper(server.Variables.Get("SERVER_UUID"))), server.GetCluster().GetCrcTable())
				//		fmt.Fprintf(os.Stdout, "gniac2 "+strings.ToUpper(server.Variables.Get("SERVER_UUID"))+" "+strconv.FormatUint(server.HashUUID, 10))
			}

			var sid uint64
			sid, err = strconv.ParseUint(server.Variables.Get("SERVER_ID"), 10, 64)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could not parse server_id, reason: %s", err)
			}
			server.ServerID = uint64(sid)

			server.EventStatus, logs, err = dbhelper.GetEventStatus(server.Conn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get events status %s %s", server.URL, err)
			if err != nil {
				cluster.SetState("ERR00073", state.State{ErrType: config.LvlErr, ErrDesc: clusterError["ERR00073"], ErrFrom: "MON", ServerUrl: server.URL})
			}
			if cluster.StateMachine.GetHeartbeats()%30 == 0 {
				server.SaveInfos()
				if server.GetCluster().GetTopology() != topoActivePassive && server.GetCluster().GetTopology() != topoMultiMasterWsrep {
					server.CheckPrivileges()
				}

			} else {
				cluster.StateMachine.PreserveState("ERR00007")
				cluster.StateMachine.PreserveState("ERR00006")
				cluster.StateMachine.PreserveState("ERR00008")
				cluster.StateMachine.PreserveState("ERR00015")
				cluster.StateMachine.PreserveState("ERR00078")
				cluster.StateMachine.PreserveState("ERR00009")
			}
			if cluster.Conf.FailEventScheduler && server.IsMaster() && !server.HasEventScheduler() {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Enable Event Scheduler on master")
				logs, err := server.SetEventScheduler(true)
				cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not enable event scheduler on the  master")
			}

			if cluster.StateMachine.GetHeartbeats()%cpu_usage_dt == 0 && server.HasUserStats() {
				start_time = server.CpuFromStatWorkLoad(start_time)
			}
			server.CurrentWorkLoad()
			server.AvgWorkLoad()
			server.MaxWorkLoad()
			cluster.StateMachine.PreserveGroup("MDEV")
		} // end not postgress

		// get Users
		users, logs, err := dbhelper.GetUsers(server.Conn, server.DBVersion)
		server.Users = config.FromNormalGrantsMap(server.Users, users)
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get database users %s %s", server.URL, err)
		if cluster.Conf.MonitorScheduler {
			server.JobsCheckRunning()
		}

		if cluster.Conf.MonitorProcessList {
			server.FullProcessList, logs, err = dbhelper.GetProcesslist(server.Conn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get process %s %s", server.URL, err)
			if err != nil {
				cluster.SetState("ERR00075", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00075"], err), ServerUrl: server.URL, ErrFrom: "MON"})
			}
		}
	}
	if server.InCaptureMode {
		cluster.SetState("WARN0085", state.State{ErrType: config.LvlInfo, ErrDesc: clusterError["WARN0085"], ServerUrl: server.URL, ErrFrom: "MON"})
	}

	logs := ""
	server.MasterStatus, logs, err = dbhelper.GetMasterStatus(server.Conn, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get master status %s %s", server.URL, err)
	if err != nil {
		// binary log might be closed for that server
	} else {
		server.BinaryLogFile = server.MasterStatus.File
		server.BinaryLogPos = strconv.FormatUint(uint64(server.MasterStatus.Position), 10)

		//Detach binlog process from main process
		go server.CheckBinaryLogs()
	}

	if !server.DBVersion.IsPostgreSQL() {
		server.BinlogDumpThreads, logs, err = dbhelper.GetBinlogDumpThreads(server.Conn, server.DBVersion)
		if err != nil {
			if strings.Contains(err.Error(), "Errcode: 28 ") || strings.Contains(err.Error(), "errno: 28 ") {
				// No space left on device
				server.IsFull = true
				cluster.SetState("WARN0100", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0100"], server.URL, err), ServerUrl: server.URL, ErrFrom: "CONF"})
				return nil
			}
		}
		server.IsFull = false
		cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get binlogDumpthreads status %s %s", server.URL, err)
		if err != nil {
			cluster.SetState("ERR00014", state.State{ErrType: config.LvlErr, ErrDesc: fmt.Sprintf(clusterError["ERR00014"], server.URL, err), ServerUrl: server.URL, ErrFrom: "CONF"})
		}

		if cluster.Conf.MonitorInnoDBStatus {
			// SHOW ENGINE INNODB STATUS
			engine, logs, err := dbhelper.GetEngineInnoDBVariables(server.Conn)
			server.EngineInnoDB = config.FromNormalStringMap(server.EngineInnoDB, engine)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get engine innodb status %s %s", server.URL, err)
		}
		go server.GetPFSQueries()
		go server.GetSlowLogTable()
		if server.HaveDiskMonitor {
			server.Disks, logs, err = dbhelper.GetDisks(server.Conn, server.DBVersion)
		}
		if cluster.Conf.MonitorScheduler {
			server.CheckDisks()
		}

	} // End not PG

	// Set channel source name is dangerous with multi cluster

	// SHOW SLAVE STATUS

	if !(cluster.Conf.MxsBinlogOn && server.IsMaxscale) && server.DBVersion.IsMariaDB() || server.DBVersion.IsPostgreSQL() {
		server.Replications, logs, err = dbhelper.GetAllSlavesStatus(server.Conn, server.DBVersion)
		if len(server.Replications) > 0 && err == nil && server.DBVersion.IsPostgreSQL() && server.ReplicationSourceName == "" {
			//setting first subscription if we don't have one
			server.ReplicationSourceName = server.Replications[0].ConnectionName.String
		}
	} else {
		server.Replications, logs, err = dbhelper.GetChannelSlaveStatus(server.Conn, server.DBVersion)
	}
	cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get slaves status %s %s", server.URL, err)

	// select a replication status get an err if repliciations array is empty
	server.SlaveStatus, err = server.GetSlaveStatus(server.ReplicationSourceName)
	if err != nil {
		// Do not reset  server.MasterServerID = 0 as we may need it for recovery
		if server.IsGroupReplicationSlave {
			server.IsSlave = server.IsGroupReplicationSlave
		} else {
			server.IsSlave = false
		}
	} else {

		server.IsSlave = true
		if server.DBVersion.IsPostgreSQL() {
			//PostgresQL as no server_id concept mimic via internal server id for topology detection
			var sid uint64
			sid, err = strconv.ParseUint(strconv.FormatUint(crc64.Checksum([]byte(server.SlaveStatus.MasterHost.String+server.SlaveStatus.MasterPort.String), cluster.GetCrcTable()), 10), 10, 64)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "PG Could not assign server_id s", err)
			}
			server.SlaveStatus.MasterServerID = sid
			for i := range server.Replications {
				server.Replications[i].MasterServerID = sid
			}

			server.SlaveGtid = gtid.NewList(server.SlaveStatus.GtidSlavePos.String)

		} else {
			if server.SlaveStatus.UsingGtid.String == "Slave_Pos" || server.SlaveStatus.UsingGtid.String == "Current_Pos" {
				server.HaveMariaDBGTID = true
			} else {
				server.HaveMariaDBGTID = false
			}
			if server.DBVersion.IsMySQLOrPerconaGreater57() && server.HasGTIDReplication() {
				server.SlaveGtid = gtid.NewList(server.SlaveStatus.ExecutedGtidSet.String)
			}
		}
	}

	// if MaxScale exit at fetch variables and status part as not supported
	if cluster.Conf.MxsBinlogOn && server.IsMaxscale {
		return nil
	}
	server.PrevStatus = config.FromStringSyncMap(server.PrevStatus, server.Status)
	status, logs, _ := dbhelper.GetStatus(server.Conn, server.DBVersion)
	server.Status = config.FromNormalStringMap(server.Status, status)

	server.HaveSemiSync = server.HasSemiSync()
	server.SemiSyncMasterStatus = server.IsSemiSyncMaster()
	server.SemiSyncSlaveStatus = server.IsSemiSyncReplica()
	server.IsWsrepSync = server.HasWsrepSync()
	server.IsWsrepDonor = server.HasWsrepDonor()
	server.IsWsrepPrimary = server.HasWsrepPrimary()

	server.ReplicationHealth = server.CheckReplication()

	if server.IsSlave == true {
		if cluster.Conf.DelayStatCapture {
			if server.State == stateFailed || server.State == stateSlaveErr {
				server.DelayStat.UpdateSlaveErrorStat(cluster.Conf.DelayStatRotate)
			}
		}
	}

	//Since there is no len within sync.Map we use exists instead
	if pqps, ok := server.PrevStatus.CheckAndGet("QUERIES"); ok {
		qps, _ := strconv.ParseInt(server.Status.Get("QUERIES"), 10, 64)
		prevqps, _ := strconv.ParseInt(pqps, 10, 64)
		if server.MonitorTime-server.PrevMonitorTime > 0 {
			server.QPS = (qps - prevqps) / (server.MonitorTime - server.PrevMonitorTime)
		}
	}

	if server.HasHighNumberSlowQueries() {
		cluster.SetState("WARN0088", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["WARN0088"], server.URL), ServerUrl: server.URL, ErrFrom: "MON"})
	}
	// monitor plugins
	if !server.DBVersion.IsPostgreSQL() {
		if cluster.StateMachine.GetHeartbeats()%60 == 0 {
			if cluster.Conf.MonitorPlugins {
				plugins, _, _ := dbhelper.GetPlugins(server.Conn, server.DBVersion)
				server.Plugins = config.FromNormalPluginsMap(server.Plugins, plugins)
				server.HaveMetaDataLocksLog = server.HasInstallPlugin("METADATA_LOCK_INFO")
				server.HaveQueryResponseTimeLog = server.HasInstallPlugin("QUERY_RESPONSE_TIME")
				server.HaveDiskMonitor = server.HasInstallPlugin("DISK")
				server.HaveSQLErrorLog = server.HasInstallPlugin("SQL_ERROR_LOG")
			}
			server.BinlogDumpThreads, logs, err = dbhelper.GetBinlogDumpThreads(server.Conn, server.DBVersion)
			if err != nil {
				if strings.Contains(err.Error(), "Errcode: 28 ") || strings.Contains(err.Error(), "errno: 28 ") {
					// No space left on device
					server.IsFull = true
					cluster.SetState("WARN0100", state.State{ErrType: config.LvlWarn, ErrDesc: fmt.Sprintf(clusterError["WARN0100"], server.URL, err), ServerUrl: server.URL, ErrFrom: "CONF"})
					return nil
				} else {
					cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get plugins  %s %s", server.URL, err)
				}
			}
			server.IsFull = false
		}
		if server.HaveMetaDataLocksLog {
			server.MetaDataLocks, logs, err = dbhelper.GetMetaDataLock(server.Conn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Monitor", config.LvlDbg, "Could not get Metat data locks  %s %s", server.URL, err)
		}
	}
	server.CheckMaxConnections()

	// Initialize graphite monitoring
	if cluster.Conf.GraphiteMetrics {
		go server.SendDatabaseStats()
	}
	return nil
}

/* Handles write freeze and shoot existing transactions on a server */
func (server *ServerMonitor) freeze() bool {
	cluster := server.ClusterGroup
	if cluster.Conf.FailEventScheduler {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes from Event Scheduler on %s", server.URL)
		logs, err := server.SetEventScheduler(false)
		cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlErr, "Could not disable event scheduler on %s", server.URL)
	}
	if cluster.Conf.FailEventStatus {
		for _, v := range server.EventStatus {
			if v.Status == 3 {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
				logs, err := dbhelper.SetEventStatus(server.Conn, v, 3)
				cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not Set DISABLE ON SLAVE for event %s %s on old master", v.Db, v.Name)
			}
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes stopping all slaves on %s", server.URL)
	logs, err := server.StopAllSlaves()
	cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlErr, "Could not stop replicas source on %s ", server.URL)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes set read only on %s", server.URL)
	logs, err = dbhelper.SetReadOnly(server.Conn, true)
	cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlInfo, "Could not set %s as read-only: %s", server.URL, err)
	if err != nil {
		return false
	}
	for i := cluster.Conf.SwitchWaitKill; i > 0; i -= 500 {
		threads, logs, err := dbhelper.CheckLongRunningWrites(server.Conn, 0)
		cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlErr, "Could not check long running writes %s as read-only: %s", server.URL, err)
		if threads == 0 {
			break
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes Waiting for %d write threads to complete %s", threads, server.URL)
		time.Sleep(500 * time.Millisecond)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes saving max_connections on %s ", server.URL)

	server.maxConn, logs, err = dbhelper.GetVariableByName(server.Conn, "MAX_CONNECTIONS", server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlErr, "Could not save max_connections value on %s", server.URL)
	if err != nil {

	} else {
		if cluster.Conf.SwitchDecreaseMaxConn {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes decreasing max_connections to 1 on %s ", server.URL)
			logs, err := dbhelper.SetMaxConnections(server.Conn, strconv.FormatInt(cluster.Conf.SwitchDecreaseMaxConnValue, 10), server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "Freeze", config.LvlErr, "Could not set max_connections to 1 on %s %s", server.URL, err)
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Freezing writes killing all other remaining threads on  %s", server.URL)
	dbhelper.KillThreads(server.Conn, server.DBVersion)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Freezing writes rejecting writes via FTWRL on %s ", server.URL)
	logs, err = dbhelper.FlushTablesWithReadLock(server.Conn, server.DBVersion)
	cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not lock tables on %s : %s", server.URL, err)

	// https://github.com/signal18/replication-manager/issues/378
	logs, err = dbhelper.FlushBinaryLogs(server.Conn)
	cluster.LogSQL(logs, err, server.URL, "MasterFailover", config.LvlErr, "Could not flush binary logs on %s", server.URL)

	if cluster.Conf.FailoverSemiSyncState {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "INFO", "Set semisync replica and disable semisync leader %s", server.URL)
		logs, err := server.SetSemiSyncReplica()
		cluster.LogSQL(logs, err, server.URL, "Rejoin", config.LvlErr, "Failed Set semisync replica and disable semisync  %s, %s", server.URL, err)
	}

	return true
}

func (server *ServerMonitor) ReadAllRelayLogs() error {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Reading all relay logs on %s", server.URL)
	if server.DBVersion.IsMariaDB() && server.HaveMariaDBGTID {
		ss, logs, err := dbhelper.GetMSlaveStatus(server.Conn, "", server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "ReadAllRelayLogs", config.LvlErr, "Could not get slave status %s %s", server.URL, err)
		if err != nil {
			return err
		}
		server.Refresh()
		myGtid_IO_Pos := gtid.NewList(ss.GtidIOPos.String)
		myGtid_Slave_Pos := server.SlaveGtid
		//myGtid_Slave_Pos := gtid.NewList(ss.GtidSlavePos.String)
		//https://jira.mariadb.org/browse/MDEV-14182

		for myGtid_Slave_Pos.Equal(myGtid_IO_Pos) == false && ss.UsingGtid.String != "" && ss.GtidSlavePos.String != "" && server.State != stateFailed {
			server.Refresh()
			ss, logs, err = dbhelper.GetMSlaveStatus(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "ReadAllRelayLogs", config.LvlErr, "Could not get slave status %s %s", server.URL, err)

			if err != nil {
				return err
			}
			time.Sleep(500 * time.Millisecond)
			myGtid_IO_Pos = gtid.NewList(ss.GtidIOPos.String)
			myGtid_Slave_Pos = server.SlaveGtid

			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting sync IO_Pos:%s, Slave_Pos:%s", myGtid_IO_Pos.Sprint(), myGtid_Slave_Pos.Sprint())
		}
	} else {
		ss, logs, err := dbhelper.GetSlaveStatus(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "ReadAllRelayLogs", config.LvlErr, "Could not get slave status %s %s", server.URL, err)
		if err != nil {
			return err
		}
		for true {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Waiting sync IO_Pos:%s/%s, Slave_Pos:%s %s", ss.MasterLogFile, ss.ReadMasterLogPos.String, ss.RelayMasterLogFile, ss.ExecMasterLogPos.String)
			if ss.MasterLogFile == ss.RelayMasterLogFile && ss.ReadMasterLogPos == ss.ExecMasterLogPos {
				break
			}
			ss, logs, err = dbhelper.GetSlaveStatus(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
			cluster.LogSQL(logs, err, server.URL, "ReadAllRelayLogs", config.LvlErr, "Could not get slave status %s %s", server.URL, err)
			if err != nil {
				return err
			}
			if strings.Contains(ss.SlaveSQLRunningState.String, "Slave has read all relay log") {
				break
			}

			time.Sleep(500 * time.Millisecond)
		}
	}
	return nil
}

func (server *ServerMonitor) LogReplPostion() {
	cluster := server.ClusterGroup
	server.Refresh()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Server:%s Current GTID:%s Slave GTID:%s Binlog Pos:%s", server.URL, server.CurrentGtid.Sprint(), server.SlaveGtid.Sprint(), server.GTIDBinlogPos.Sprint())
	return
}

func (server *ServerMonitor) Close() {
	server.Conn.Close()
	return
}

func (server *ServerMonitor) writeState() error {
	server.LogReplPostion()
	f, err := os.Create("/tmp/repmgr.state")
	if err != nil {
		return err
	}
	if server.GTIDBinlogPos == nil {
		return errors.New("No GTID Binlog Position")
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

func (server *ServerMonitor) StopSlave() (string, error) {
	cluster := server.ClusterGroup
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	return dbhelper.StopSlave(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
}

func (server *ServerMonitor) StopAllSlaves() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	sql := ""
	var lasterror error
	for _, rep := range server.Replications {
		res, errslave := dbhelper.StopSlave(server.Conn, rep.ConnectionName.String, server.DBVersion)
		sql += res
		if errslave != nil {
			lasterror = errslave
		}
	}

	return sql, lasterror
}

func (server *ServerMonitor) StopAllExtraSourceSlaves() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	sql := ""
	var lasterror error
	for _, rep := range server.Replications {
		if rep.ConnectionName.String != cluster.Conf.MasterConn {
			res, errslave := dbhelper.StopSlave(server.Conn, rep.ConnectionName.String, server.DBVersion)
			sql += res
			if errslave != nil {
				lasterror = errslave
			}
		}
	}

	return sql, lasterror
}

func (server *ServerMonitor) StartSlave() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No databse connection")
	}
	cluster := server.ClusterGroup
	return dbhelper.StartSlave(server.Conn, cluster.Conf.MasterConn, server.DBVersion)

}

func (server *ServerMonitor) ResetMaster() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	return dbhelper.ResetMaster(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
}

func (server *ServerMonitor) ResetPFSQueries() error {
	return server.ExecQueryNoBinLog("truncate performance_schema.events_statements_summary_by_digest")
}

func (server *ServerMonitor) StopSlaveIOThread() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	return dbhelper.StopSlaveIOThread(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
}

func (server *ServerMonitor) StopSlaveSQLThread() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	return dbhelper.StopSlaveSQLThread(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
}

func (server *ServerMonitor) ResetSlave() (string, error) {
	cluster := server.ClusterGroup
	return dbhelper.ResetSlave(server.Conn, true, cluster.Conf.MasterConn, server.DBVersion)
}

func (server *ServerMonitor) FlushLogs() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	return dbhelper.FlushBinaryLogsLocal(server.Conn)
}

func (server *ServerMonitor) FlushTables() (string, error) {
	if server.Conn == nil {
		return "", errors.New("No database connection pool")
	}
	return dbhelper.FlushTables(server.Conn)
}

func (server *ServerMonitor) Uprovision() {
	cluster := server.ClusterGroup
	cluster.OpenSVCUnprovisionDatabaseService(server)
}

func (server *ServerMonitor) Provision() {
	cluster := server.ClusterGroup
	cluster.OpenSVCProvisionDatabaseService(server)
}

func (server *ServerMonitor) SkipReplicationEvent() {
	cluster := server.ClusterGroup
	server.StopSlave()
	dbhelper.SkipBinlogEvent(server.Conn, cluster.Conf.MasterConn, server.DBVersion)
	server.StartSlave()
}

func (server *ServerMonitor) KillThread(id string) (string, error) {
	return dbhelper.KillThread(server.Conn, id, server.DBVersion)
}

func (server *ServerMonitor) KillQuery(id string) (string, error) {
	return dbhelper.KillQuery(server.Conn, id, server.DBVersion)
}

func (server *ServerMonitor) ExecQueryNoBinLog(query string) error {
	cluster := server.ClusterGroup
	Conn, err := server.GetNewDBConn()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error connection in exec query no log %s %s", query, err)
		return err
	}
	defer Conn.Close()
	_, err = Conn.Exec("set sql_log_bin=0")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error disabling binlog %s", err)
		return err
	}
	_, err = Conn.Exec(query)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Error query %s %s", query, err)
		return err
	}
	return err
}

func (server *ServerMonitor) ExecScriptSQL(queries []string) (error, bool) {
	cluster := server.ClusterGroup
	hasreadonlyvar := false
	if server.State == stateFailed {
		errmsg := "Can't execute script on failed server: " + server.URL
		return errors.New(errmsg), hasreadonlyvar
	}
	for _, query := range queries {
		if strings.Trim(query, " ") == "" {
			continue
		}
		_, err := server.Conn.Exec(query)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Apply config: %s %s", query, err)
			if driverErr, ok := err.(*mysql.MySQLError); ok {
				// access denied
				if driverErr.Number == 1238 {
					hasreadonlyvar = true
				}
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Apply dynamic config: %s", query)
	}
	return nil, hasreadonlyvar
}

func (server *ServerMonitor) InstallPlugin(name string) error {
	val, ok := server.Plugins.CheckAndGet(name)

	if !ok {
		return errors.New("Plugin not loaded")
	} else {
		if val.Status == "NOT INSTALLED" {
			query := "INSTALL PLUGIN " + name + " SONAME '" + val.Library.String + "'"
			err := server.ExecQueryNoBinLog(query)
			if err != nil {
				return err
			}
			val.Status = "ACTIVE"
			server.Plugins.Set(name, val)
		} else {
			return errors.New("Already Install Plugin")
		}
	}
	return nil
}

func (server *ServerMonitor) UnInstallPlugin(name string) error {
	val, ok := server.Plugins.CheckAndGet(name)
	if !ok {
		return errors.New("Plugin not loaded")
	} else {
		if val.Status == "ACTIVE" {
			query := "UNINSTALL PLUGIN " + name
			err := server.ExecQueryNoBinLog(query)
			if err != nil {
				return err
			}
			val.Status = "NOT INSTALLED"
			server.Plugins.Set(name, val)
		} else {
			return errors.New("Already not installed Plugin")
		}
	}
	return nil
}

func (server *ServerMonitor) Capture(cstate *state.CapturedState) error {
	cluster := server.ClusterGroup
	if server.InCaptureMode {
		return nil
	}
	//Log the server url
	cstate.ServerURLs = append(cstate.ServerURLs, server.URL)
	// cluster.GetStateMachine().CapturedState.Store(cstate.ErrKey, cstate)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Capture %s on server %s", cstate.ErrKey, server.URL)

	go server.CaptureLoop(cluster.GetStateMachine().GetHeartbeats())
	go server.JobCapturePurge(cluster.Conf.WorkingDir+"/"+cluster.Name, cluster.Conf.MonitorCaptureFileKeep)
	return nil
}

func (server *ServerMonitor) SaveInfos() error {
	type Save struct {
		Variables             map[string]string      `json:"variables"`
		ProcessList           []dbhelper.Processlist `json:"processlist"`
		Status                map[string]string      `json:"status"`
		SlaveStatus           []dbhelper.SlaveStatus `json:"slavestatus"`
		MaxSlowQueryTimestamp int64                  `json:"maxSlowQueryTimestamp"`
	}
	var clsave Save
	server.Variables.ToNormalMap(clsave.Variables)
	clsave.Status = server.Status.ToNewMap()
	clsave.ProcessList = server.FullProcessList
	clsave.SlaveStatus = server.LastSeenReplications
	clsave.MaxSlowQueryTimestamp = server.MaxSlowQueryTimestamp
	saveJSON, _ := json.MarshalIndent(clsave, "", "\t")
	err := os.WriteFile(server.Datadir+"/serverstate.json", saveJSON, 0644)
	if err != nil {
		return errors.New("SaveInfos" + err.Error())
	}
	return nil
}

func (server *ServerMonitor) ReloadSaveInfosVariables() error {
	cluster := server.ClusterGroup
	type Save struct {
		Variables             map[string]string      `json:"variables"`
		ProcessList           []dbhelper.Processlist `json:"processlist"`
		Status                map[string]string      `json:"status"`
		SlaveStatus           []dbhelper.SlaveStatus `json:"slavestatus"`
		MaxSlowQueryTimestamp int64                  `json:"maxSlowQueryTimestamp"`
	}

	var clsave Save
	file, err := os.ReadFile(server.Datadir + "/serverstate.json")
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "No file found %s: %v\n", server.Datadir+"/serverstate.json", err)
		return err
	}
	err = json.Unmarshal(file, &clsave)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "File error: %v\n", err)
		return err
	}
	if server.Variables == nil {
		server.Variables = new(config.StringsMap)
	}
	server.Variables = config.FromNormalStringMap(server.Variables, clsave.Variables)
	server.MaxSlowQueryTimestamp = clsave.MaxSlowQueryTimestamp
	return nil
}

func (server *ServerMonitor) CaptureLoop(start int64) {
	cluster := server.ClusterGroup

	server.SetInCaptureMode(true)
	defer server.SetInCaptureMode(false)

	type Save struct {
		ProcessList  []dbhelper.Processlist `json:"processlist"`
		InnoDBStatus string                 `json:"innodbstatus"`
		Status       map[string]string      `json:"status"`
		SlaveSatus   []dbhelper.SlaveStatus `json:"slavestatus"`
	}

	t := time.Now()
	logs := ""
	var err error
	var curHB int64 = start
	for {

		var clsave Save
		clsave.ProcessList,
			logs, err = dbhelper.GetProcesslist(server.Conn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "CaptureLoop", config.LvlErr, "Failed Processlist for server %s: %s ", server.URL, err)

		clsave.InnoDBStatus, logs, err = dbhelper.GetEngineInnoDBStatus(server.Conn)
		cluster.LogSQL(logs, err, server.URL, "CaptureLoop", config.LvlErr, "Failed InnoDB Status for server %s: %s ", server.URL, err)
		clsave.Status, logs, err = dbhelper.GetStatus(server.Conn, server.DBVersion)
		cluster.LogSQL(logs, err, server.URL, "CaptureLoop", config.LvlErr, "Failed Status for server %s: %s ", server.URL, err)

		if !(cluster.Conf.MxsBinlogOn && server.IsMaxscale) && server.DBVersion.IsMariaDB() {
			clsave.SlaveSatus, logs, err = dbhelper.GetAllSlavesStatus(server.Conn, server.DBVersion)
		} else {
			clsave.SlaveSatus, logs, err = dbhelper.GetChannelSlaveStatus(server.Conn, server.DBVersion)
		}
		cluster.LogSQL(logs, err, server.URL, "CaptureLoop", config.LvlErr, "Failed Slave Status for server %s: %s ", server.URL, err)

		saveJSON, _ := json.MarshalIndent(clsave, "", "\t")
		err = os.WriteFile(cluster.Conf.WorkingDir+"/"+cluster.Name+"/capture_"+server.Name+"_"+t.Format("20060102150405")+".json", saveJSON, 0644)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Exit loop %s with error %v\n", server.URL, err)
			return
		}

		for curHB == cluster.GetStateMachine().GetHeartbeats() {
			time.Sleep(10 * time.Millisecond)
		}

		curHB = cluster.GetStateMachine().GetHeartbeats()

		if curHB >= start+5 {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Exit loop %s. Start HB: %d, Stop HB: %d ", server.URL, start, curHB-1)
			break
		}
	}
}

func (server *ServerMonitor) RotateSystemLogs() {
	cluster := server.ClusterGroup
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Log rotate on %s", server.URL)

	if server.HasLogsInSystemTables() && !server.IsDown() {
		if server.HasLogSlowQuery() {
			server.RotateTableToTime("mysql", "slow_log")
		}
		if server.HasLogGeneral() {
			server.RotateTableToTime("mysql", "general_log")
		}
	}
}

func (server *ServerMonitor) RotateTableToTime(database string, table string) {
	cluster := server.ClusterGroup
	currentTime := time.Now()
	timeStampString := currentTime.Format("20060102150405")
	newtablename := table + "_" + timeStampString
	temptable := table + "_temp"
	query := "CREATE TABLE IF NOT EXISTS " + database + "." + temptable + " LIKE " + database + "." + table
	server.ExecQueryNoBinLog(query)
	query = "RENAME TABLE  " + database + "." + table + " TO " + database + "." + newtablename + " , " + database + "." + temptable + " TO " + database + "." + table
	server.ExecQueryNoBinLog(query)
	query = "select table_name from information_schema.tables where table_schema='" + database + "' and table_name like '" + table + "_%' order by table_name desc limit " + strconv.Itoa(cluster.Conf.SchedulerMaintenanceDatabaseLogsTableKeep) + ",100"
	cleantables := []string{}

	err := server.Conn.Select(&cleantables, query)
	if err != nil {
		return
	}
	for _, row := range cleantables {
		server.ExecQueryNoBinLog("DROP TABLE " + database + "." + row)
	}
}

func (server *ServerMonitor) WaitInnoDBPurge() error {
	query := "SET GLOBAL innodb_purge_rseg_truncate_frequency=1"
	server.ExecQueryNoBinLog(query)
	ct := 0
	for {
		if server.EngineInnoDB.Get("history_list_lenght_inside_innodb") == "0" {
			return nil
		}
		if ct == 1200 {
			return errors.New("Waiting to long for history_list_lenght_inside_innodb 0")
		}
	}
}

func (server *ServerMonitor) Shutdown() error {
	if server.Conn == nil {
		return errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	cmd := "SHUTDOWN"
	if server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 4 && server.IsMaster() {
		cmd = "SHUTDOWN WAIT FOR ALL SLAVES"
	}
	_, err := server.Conn.Exec(cmd)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Shutdown failed %s", err)
		return err
	}
	return nil
}

func (server *ServerMonitor) ChangeMasterTo(master *ServerMonitor, master_use_gitd string) error {
	logs := ""
	cluster := server.ClusterGroup
	var err error
	if server.State == stateFailed {
		return errors.New("Change master canceled cause by state failed")
	}

	hasMyGTID := server.HasMySQLGTID()
	if cluster.Conf.MultiMasterGrouprep {
		//MySQL group replication
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        "",
			Port:        "",
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "GROUP_REPL",
			Channel:     "group_replication_recovery",
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
			SSL:         cluster.Conf.ReplicationSSL,
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Group Replication bootstrapped  for", server.URL)
	} else if cluster.Conf.ForceSlaveNoGtid == false && server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 {
		//mariadb using GTID
		master.Refresh()
		_, err = server.Conn.Exec("SET GLOBAL gtid_slave_pos = \"" + master.CurrentGtid.Sprint() + "\"")
		if err != nil {
			return err
		}
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        master.Host,
			Port:        master.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        master_use_gitd,
			Channel:     cluster.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
			SSL:         cluster.Conf.ReplicationSSL,
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication bootstrapped with %s as master", master.URL)
	} else if hasMyGTID && cluster.Conf.ForceSlaveNoGtid == false {
		// MySQL GTID
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        master.Host,
			Port:        master.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "MASTER_AUTO_POSITION",
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
			SSL:         cluster.Conf.ReplicationSSL,
			Channel:     cluster.Conf.MasterConn,
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication bootstrapped with MySQL GTID replication style and %s as master", master.URL)

	} else {
		// Old Style file pos as default
		logs, err = dbhelper.ChangeMaster(server.Conn, dbhelper.ChangeMasterOpt{
			Host:        master.Host,
			Port:        master.Port,
			User:        cluster.GetRplUser(),
			Password:    cluster.GetRplPass(),
			Retry:       strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatRetry),
			Heartbeat:   strconv.Itoa(cluster.Conf.ForceSlaveHeartbeatTime),
			Mode:        "POSITIONAL",
			Logfile:     master.BinaryLogFile,
			Logpos:      master.BinaryLogPos,
			Channel:     cluster.Conf.MasterConn,
			IsDelayed:   server.IsDelayed,
			Delay:       strconv.Itoa(cluster.Conf.HostsDelayedTime),
			SSL:         cluster.Conf.ReplicationSSL,
			PostgressDB: server.PostgressDB,
		}, server.DBVersion)
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Replication bootstrapped with old replication style and %s as master", master.URL)

	}
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", config.LvlErr, "Replication can't be bootstrap for server %s with %s as master: %s ", server.URL, master.URL, err)
	}
	_, err = server.StartSlave()
	if err != nil {
		err = errors.New(fmt.Sprintln("Can't start slave: ", err))
	}
	return err
}

func (server *ServerMonitor) CertificatesReload() error {
	if server.Conn == nil {
		return errors.New("No database connection pool")
	}
	cluster := server.ClusterGroup
	cmd := "ALTER INSTANCE RELOAD TLS"
	if server.DBVersion.IsMariaDB() && server.DBVersion.Major >= 10 && server.DBVersion.Minor >= 4 {
		cmd = "FLUSH SSL"
	}
	_, err := server.Conn.Exec(cmd)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Reload certificatd %s", err)
		return err
	}
	return nil
}

func (server *ServerMonitor) BootstrapGroupReplication() error {
	logs, err := dbhelper.BootstrapGroupReplication(server.Conn, server.DBVersion)
	cluster := server.ClusterGroup
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", config.LvlErr, "Group Replication can't be bootstrap on server %s :%s ", server.URL, err)
		return err
	}
	return nil
}

func (server *ServerMonitor) StartGroupReplication() error {
	logs, err := dbhelper.StartGroupReplication(server.Conn, server.DBVersion)
	cluster := server.ClusterGroup
	if err != nil {
		cluster.LogSQL(logs, err, server.URL, "BootstrapReplication", config.LvlErr, "Group Replication can't be joined on server %s :%s ", server.URL, err)
		return err
	}
	return nil
}

func (server *ServerMonitor) CurrentWorkLoad() {
	new_current_WorkLoad := server.WorkLoad.GetOrNew("current")
	new_current_WorkLoad.Connections = server.GetServerConnections()
	new_current_WorkLoad.CpuThreadPool = server.GetCPUUsageFromThreadsPool()
	new_current_WorkLoad.QPS = server.QPS
	server.WorkLoad.Set("current", new_current_WorkLoad)

}

func (server *ServerMonitor) AvgWorkLoad() {
	new_avg_WorkLoad := server.WorkLoad.Get("average")
	if server.WorkLoad.Get("average").Connections > 0 {
		new_avg_WorkLoad.Connections = (server.GetServerConnections() + server.WorkLoad.Get("average").Connections) / 2
	} else {
		new_avg_WorkLoad.Connections = server.GetServerConnections()
	}

	if server.WorkLoad.Get("average").CpuThreadPool > 0 {
		new_avg_WorkLoad.CpuThreadPool = (server.GetCPUUsageFromThreadsPool() + server.WorkLoad.Get("average").CpuThreadPool) / 2
	} else {
		new_avg_WorkLoad.CpuThreadPool = server.GetCPUUsageFromThreadsPool()
	}

	if server.WorkLoad.Get("average").QPS > 0 {
		new_avg_WorkLoad.QPS = (server.QPS + server.WorkLoad.Get("average").QPS) / 2
	} else {
		new_avg_WorkLoad.QPS = server.WorkLoad.Get("average").QPS
	}

	server.WorkLoad.Set("average", new_avg_WorkLoad)
}

func (server *ServerMonitor) MaxWorkLoad() {
	max_workLoad := server.WorkLoad.Get("max")
	if server.GetServerConnections() > server.WorkLoad.Get("max").Connections {
		max_workLoad.Connections = server.GetServerConnections()
	}

	if server.QPS > server.WorkLoad.Get("max").QPS {
		max_workLoad.QPS = server.QPS
	}

	if server.GetCPUUsageFromThreadsPool() > server.WorkLoad.Get("max").CpuThreadPool {
		max_workLoad.CpuThreadPool = server.GetCPUUsageFromThreadsPool()
	}

	server.WorkLoad.Set("max", max_workLoad)
}

func (server *ServerMonitor) CpuFromStatWorkLoad(start_time time.Time) time.Time {
	if server.WorkLoad.Get("current").BusyTime != "" {

		old_cpu_time := server.WorkLoad.Get("current").CpuUserStats
		current_workLoad := server.WorkLoad.Get("current")
		new_cpu_usage, _ := server.GetCPUUsageFromStats(start_time)
		current_workLoad.BusyTime, _ = server.GetBusyTimeFromStats()
		current_workLoad.CpuUserStats = new_cpu_usage
		server.WorkLoad.Set("current", current_workLoad)

		if old_cpu_time != 0 {
			avg_workLoad := server.WorkLoad.Get("average")
			avg_workLoad.CpuUserStats = (current_workLoad.CpuUserStats + old_cpu_time) / 2
			server.WorkLoad.Set("average", avg_workLoad)
		}
		if current_workLoad.CpuUserStats > server.WorkLoad.Get("max").CpuUserStats {
			max_workLoad := server.WorkLoad.Get("max")
			max_workLoad.CpuUserStats = current_workLoad.CpuUserStats
			server.WorkLoad.Set("max", max_workLoad)

		}
		return time.Now()

	} else {
		current_workLoad := server.WorkLoad.Get("current")
		current_workLoad.BusyTime, _ = server.GetBusyTimeFromStats()
		server.WorkLoad.Set("current", current_workLoad)
		return time.Now()
	}
}
